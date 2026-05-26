package gateway

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"

	"jc_proxy/internal/config"
)

const (
	pooledResponseCopyBufferBytes    = 32 << 10
	streamingResponseCopyBufferBytes = 4 << 10
)

var errAbortDownstreamResponse = errors.New("abort downstream response")

type flushWriter struct {
	writer  io.Writer
	flusher http.Flusher
}

func (w *flushWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		w.flusher.Flush()
	}
	return n, err
}

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

func captureResponsePreview(body io.ReadCloser, limit int, header http.Header) ([]byte, io.Reader, error) {
	if body == nil || body == http.NoBody || limit <= 0 {
		return nil, http.NoBody, nil
	}
	rawPreview, err := io.ReadAll(io.LimitReader(body, int64(limit)))
	if err != nil {
		return nil, nil, err
	}
	preview := maybeDecompressPreview(rawPreview, header)
	return preview, io.MultiReader(bytes.NewReader(rawPreview), body), nil
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
		reader = flate.NewReader(bytes.NewReader(data))
	default:
		return data
	}

	decompressed, err := io.ReadAll(reader)
	if err != nil || len(decompressed) == 0 {
		return data
	}
	return decompressed
}

func (r *Router) responseCopyBuffer(streaming bool) ([]byte, func()) {
	if streaming {
		return make([]byte, streamingResponseCopyBufferBytes), func() {}
	}

	buf := r.bufPool.Get().(*[]byte)
	return *buf, func() {
		r.bufPool.Put(buf)
	}
}

func (r *Router) writeUpstreamResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, body io.Reader, vg *vendorGateway, idx int, selectedKey string, selectedVersion int64, decision keyDecision, decisionApplied bool, interim *interimResponseSender) error {
	defer resp.Body.Close()

	if body == nil {
		body = http.NoBody
	}

	streaming := shouldFlushResponse(req, resp)
	buf, releaseBuf := r.responseCopyBuffer(streaming)
	defer releaseBuf()

	writer := io.Writer(w)
	if streaming {
		if flusher, ok := w.(http.Flusher); ok {
			writer = &flushWriter{writer: w, flusher: flusher}
		}
	}

	applyCompletedDecisionIfNeeded := func() {
		if !decisionApplied {
			vg.applyDecision(idx, selectedKey, selectedVersion, decision)
			decisionApplied = true
		}
	}

	applyInterruptedDecisionIfNeeded := func() {
		if !decisionApplied {
			if resp.StatusCode >= http.StatusBadRequest {
				vg.applyDecision(idx, selectedKey, selectedVersion, decision)
			} else {
				vg.applyDecision(idx, selectedKey, selectedVersion, keyDecision{action: keyActionNone})
			}
			decisionApplied = true
		}
	}

	committed := false
	commitUpstream := func() {
		if committed {
			return
		}
		commitFinalResponse(interim, func() {
			copyResponseHeaders(w.Header(), resp.Header)
			w.WriteHeader(resp.StatusCode)
		})
		committed = true
		if streaming {
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}

	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			commitUpstream()
			if _, writeErr := writer.Write(buf[:n]); writeErr != nil {
				applyInterruptedDecisionIfNeeded()
				return nil
			}
		}

		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			commitUpstream()
			applyCompletedDecisionIfNeeded()
			return nil
		}
		if isCanceledUpstreamError(req.Context(), readErr) {
			applyInterruptedDecisionIfNeeded()
			return nil
		}

		applyInterruptedDecisionIfNeeded()
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
	switch statusCode {
	case http.StatusTooManyRequests:
		return boolOrDefault(retry.RateLimit, true)
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusInternalServerError:
		return boolOrDefault(retry.ServerError, true)
	default:
		return containsStatusCode(retry.StatusCodes, statusCode)
	}
}
