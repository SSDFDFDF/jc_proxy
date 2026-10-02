package gateway

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"jc_proxy/internal/config"
)

const (
	pooledResponseCopyBufferBytes    = 32 << 10
	streamingResponseCopyBufferBytes = 4 << 10
)

var errAbortDownstreamResponse = errors.New("abort downstream response")

// Long-lived streams retain only 4 KiB each, not a 32 KiB non-streaming buffer.
var streamingResponseBuffers = sync.Pool{New: func() any {
	buf := make([]byte, streamingResponseCopyBufferBytes)
	return &buf
}}

func shouldFlushResponse(req *http.Request, resp *http.Response) bool {
	if resp != nil {
		contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
		if strings.HasPrefix(contentType, "text/event-stream") {
			return true
		}
	}
	if req == nil {
		return false
	}
	stream := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("stream")))
	if stream == "true" || stream == "1" || stream == "yes" {
		return true
	}
	return strings.Contains(strings.ToLower(req.Header.Get("Accept")), "text/event-stream")
}

type capturedResponsePreview struct {
	raw      []byte
	decoded  []byte
	complete bool
}

// Only forwarding needs to replay the preview; retry/masking can discard it
// without allocating an extra reader chain or reading the prefix twice.
func (p capturedResponsePreview) replay(body io.Reader) io.Reader {
	if p.complete {
		return bytes.NewReader(p.raw)
	}
	if len(p.raw) == 0 {
		return body
	}
	return io.MultiReader(bytes.NewReader(p.raw), body)
}

func captureResponsePreview(body io.ReadCloser, limit int, header http.Header) (capturedResponsePreview, error) {
	if body == nil || body == http.NoBody {
		return capturedResponsePreview{complete: true}, nil
	}
	if limit <= 0 {
		return capturedResponsePreview{}, nil
	}
	reader := &io.LimitedReader{R: body, N: int64(limit)}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return capturedResponsePreview{}, err
	}
	return capturedResponsePreview{raw: raw, decoded: maybeDecompressPreview(raw, header), complete: reader.N > 0}, nil
}

func maybeDecompressPreview(data []byte, header http.Header) []byte {
	enc := strings.ToLower(header.Get("Content-Encoding"))
	if enc == "" {
		return data
	}

	var reader io.Reader
	switch {
	case strings.Contains(enc, "gzip"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return data
		}
		defer gz.Close()
		reader = gz
	case strings.Contains(enc, "deflate"):
		fr := flate.NewReader(bytes.NewReader(data))
		defer fr.Close()
		reader = fr
	default:
		return data
	}

	// A small compressed preview can expand enormously. Bound diagnostic
	// decompression too; never inflate an untrusted error body without a cap.
	decompressed, err := io.ReadAll(io.LimitReader(reader, 64<<10))
	if err != nil || len(decompressed) == 0 {
		return data
	}
	return decompressed
}

func (r *Router) responseCopyBuffer(streaming bool) ([]byte, func()) {
	if streaming {
		buf := streamingResponseBuffers.Get().(*[]byte)
		return *buf, func() { streamingResponseBuffers.Put(buf) }
	}
	buf := r.bufPool.Get().(*[]byte)
	return *buf, func() {
		r.bufPool.Put(buf)
	}
}

func (r *Router) writeUpstreamResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, body io.Reader, vg *vendorGateway, attempt *upstreamAttempt, decision keyDecision, interim *interimResponseSender) error {
	defer resp.Body.Close()
	// Downstream write failures (including panics) still count as attempted
	// traffic, but must not poison upstream health. finish is exactly once.
	defer attempt.finish(vg, keyDecision{action: keyActionInterrupted}, -1)

	if body == nil {
		body = http.NoBody
	}

	streaming := shouldFlushResponse(req, resp)
	buf, releaseBuf := r.responseCopyBuffer(streaming)
	defer releaseBuf()

	var flusher *http.ResponseController
	if streaming {
		// Prefer FlushError when available (including through Unwrap). A
		// buffered Write can succeed even though sending it to the client fails.
		flusher = http.NewResponseController(w)
	}

	finishReadFailure := func(err error) {
		if attempt.finished {
			return // a known HTTP failure was already classified and counted
		}
		failure := classifyRequestError(vg.provider, vg.errorPolicy, fmt.Sprintf("upstream response interrupted: %v", err))
		failure.statusCode = http.StatusBadGateway
		attempt.finish(vg, failure, -1)
	}

	committed, hasBody := false, false
	commitUpstream := func() {
		if committed {
			return
		}
		commitFinalResponse(interim, func() {
			copyResponseHeaders(w.Header(), resp.Header)
			w.WriteHeader(resp.StatusCode)
		})
		committed = true
	}

	for {
		n, readErr := body.Read(buf)
		full := time.Duration(-1)
		if readErr != nil {
			full = time.Since(attempt.started)
		}
		canceled := isCanceledUpstreamError(req.Context(), readErr)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !canceled {
			// Preserve a known upstream failure even if writing these last
			// bytes also fails or cancels the downstream context.
			finishReadFailure(readErr)
		}
		if n > 0 {
			hasBody = true
			commitUpstream()
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return nil
			}
			if flusher != nil {
				if err := flusher.Flush(); errors.Is(err, http.ErrNotSupported) {
					flusher = nil // preserve writers without streaming support
				} else if err != nil {
					return nil
				}
			}
		}

		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			if !hasBody && expectsUpstreamResponseBody(attempt.request, resp) {
				finishReadFailure(errors.New("empty upstream response"))
				writeHTTPError(w, interim, "upstream returned an empty response", http.StatusBadGateway)
				return nil
			}
			commitUpstream()
			if !hasBody && flusher != nil {
				// No body Write exists to flush this empty stream.
				if err := flusher.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
					return nil
				}
			}
			attempt.finish(vg, decision, full)
			return nil
		}
		if canceled {
			return nil
		}

		if !committed {
			if resp.StatusCode >= http.StatusBadRequest {
				commitUpstream()
			} else {
				writeHTTPError(w, interim, "upstream response interrupted", http.StatusBadGateway)
			}
			return nil
		}
		if resp.StatusCode >= http.StatusBadRequest {
			return nil
		}
		return errAbortDownstreamResponse
	}
}

// HTTP permits empty responses in general. Reject them only when a successful
// response promises JSON/SSE or a known inference API requires a result. Do not
// turn legitimate HEAD, OPTIONS, 204/205, redirects or empty acknowledgments
// from generic endpoints into upstream failures.
func expectsUpstreamResponseBody(req *http.Request, resp *http.Response) bool {
	if req == nil || resp == nil || req.Method == http.MethodHead || req.Method == http.MethodOptions ||
		resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusResetContent {
		return false
	}
	contentType := normalizedContentType(resp.Header)
	if resp.ContentLength > 0 || contentType == "application/json" || strings.HasSuffix(contentType, "+json") || contentType == "text/event-stream" {
		return true
	}
	if req.Method != http.MethodPost || req.URL == nil {
		return false
	}
	path := strings.TrimRight(req.URL.Path, "/")
	for _, suffix := range []string{"/chat/completions", "/completions", "/responses", "/messages", "/embeddings", ":generateContent", ":streamGenerateContent"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func writeHTTPError(w http.ResponseWriter, interim *interimResponseSender, message string, statusCode int) {
	if interim == nil {
		http.Error(w, message, statusCode)
		return
	}
	commitFinalResponse(interim, func() {
		http.Error(w, message, statusCode)
	})
}

func commitFinalResponse(interim *interimResponseSender, fn func()) {
	if interim == nil {
		fn()
		return
	}
	interim.commitFinal(fn)
}

// aggregateRetryable reports whether the given response status code or
// request error should trigger a retry at the aggregate level (i.e. try
// a different child vendor).
func aggregateRetryable(statusCode int, err error, retry config.AggregateRetryConfig) bool {
	if !boolOrDefault(retry.Enabled, true) {
		return false
	}
	if err != nil {
		return boolOrDefault(retry.NetworkError, true)
	}
	// Aggregate retries are failure recovery only; never discard a successful
	// response because a custom status list is overly broad.
	if statusCode < http.StatusBadRequest {
		return false
	}
	switch statusCode {
	case http.StatusTooManyRequests:
		return boolOrDefault(retry.RateLimit, true)
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusInternalServerError:
		return boolOrDefault(retry.ServerError, true)
	default:
		return containsStatusCode(retry.StatusCodes, statusCode)
	}
}
