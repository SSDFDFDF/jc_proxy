package gateway

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
)

const (
	pooledResponseCopyBufferBytes    = 32 << 10
	streamingResponseCopyBufferBytes = 4 << 10
)

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

func (r *Router) writeUpstreamResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, body io.Reader, vg *vendorGateway, idx int, selectedKey string, selectedVersion int64, decision keyDecision, decisionApplied bool) {
	defer resp.Body.Close()

	if body == nil {
		body = http.NoBody
	}

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	streaming := shouldFlushResponse(req, resp)
	buf, releaseBuf := r.responseCopyBuffer(streaming)
	defer releaseBuf()

	writer := io.Writer(w)
	if streaming {
		if flusher, ok := w.(http.Flusher); ok {
			writer = &flushWriter{writer: w, flusher: flusher}
			flusher.Flush()
		}
	}

	_, copyErr := io.CopyBuffer(writer, body, buf)
	if copyErr != nil {
		if !decisionApplied {
			if resp.StatusCode >= http.StatusBadRequest {
				vg.applyDecision(idx, selectedKey, selectedVersion, decision)
			} else {
				vg.applyDecision(idx, selectedKey, selectedVersion, keyDecision{action: keyActionNone})
			}
		}
		return
	}

	if !decisionApplied {
		vg.applyDecision(idx, selectedKey, selectedVersion, decision)
	}
}
