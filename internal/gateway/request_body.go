package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"syscall"
)

const maxReplayableRequestBodyBytes = 4 << 20

type requestBodySource struct {
	replayable    bool
	safeRetry     bool
	contentLength int64
	newBody       func() (io.ReadCloser, error)
}

func (s *requestBodySource) Body() (io.ReadCloser, error) {
	if s == nil || s.newBody == nil {
		return http.NoBody, nil
	}
	return s.newBody()
}

func (s *requestBodySource) canRetryRequestError(decision keyDecision) bool {
	if s == nil || !s.replayable || !s.safeRetry {
		return false
	}
	return shouldRetryDecision(decision)
}

func (s *requestBodySource) canRetryResponse(statusCode int, decision keyDecision) bool {
	if s == nil || !s.replayable || !shouldRetryDecision(decision) {
		return false
	}
	if s.safeRetry {
		return true
	}
	switch statusCode {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

func prepareRequestBody(req *http.Request) (*requestBodySource, error) {
	safeRetry := isSafeRetryMethod(req.Method)

	if req == nil || req.Body == nil || req.Body == http.NoBody || req.ContentLength == 0 {
		return &requestBodySource{
			replayable:    true,
			safeRetry:     safeRetry,
			contentLength: 0,
			newBody: func() (io.ReadCloser, error) {
				return http.NoBody, nil
			},
		}, nil
	}

	if req.GetBody != nil {
		return &requestBodySource{
			replayable:    true,
			safeRetry:     safeRetry,
			contentLength: req.ContentLength,
			newBody:       req.GetBody,
		}, nil
	}

	if req.ContentLength < 0 {
		return newSingleUseBodySource(req.Body, req.ContentLength, safeRetry), nil
	}
	if !shouldBufferRequestBody(req.Header.Get("Content-Type")) {
		return newSingleUseBodySource(req.Body, req.ContentLength, safeRetry), nil
	}
	if req.ContentLength > maxReplayableRequestBodyBytes {
		return newSingleUseBodySource(req.Body, req.ContentLength, safeRetry), nil
	}

	buf, overflow, err := readReplayableRequestBody(req.Body, maxReplayableRequestBodyBytes)
	if err != nil {
		return nil, err
	}
	if overflow {
		reader := io.MultiReader(bytes.NewReader(buf), req.Body)
		return newSingleUseBodySource(io.NopCloser(reader), req.ContentLength, safeRetry), nil
	}

	raw := append([]byte(nil), buf...)
	return &requestBodySource{
		replayable:    true,
		safeRetry:     safeRetry,
		contentLength: int64(len(raw)),
		newBody: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(raw)), nil
		},
	}, nil
}

func newSingleUseBodySource(body io.ReadCloser, contentLength int64, safeRetry bool) *requestBodySource {
	used := false
	return &requestBodySource{
		replayable:    false,
		safeRetry:     safeRetry,
		contentLength: contentLength,
		newBody: func() (io.ReadCloser, error) {
			if used {
				return nil, errors.New("request body is not replayable")
			}
			used = true
			return body, nil
		},
	}
}

func readReplayableRequestBody(body io.ReadCloser, limit int64) ([]byte, bool, error) {
	if body == nil || body == http.NoBody {
		return nil, false, nil
	}
	lr := &io.LimitedReader{R: body, N: limit + 1}
	buf, err := io.ReadAll(lr)
	if err != nil {
		return nil, false, err
	}
	return buf, int64(len(buf)) > limit, nil
}

func shouldBufferRequestBody(contentType string) bool {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = contentType
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return !strings.HasPrefix(mediaType, "multipart/")
}

func isClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "use of closed network connection")
}

func isCanceledUpstreamError(ctx context.Context, err error) bool {
	if ctx == nil || err == nil || ctx.Err() == nil {
		return false
	}
	if errors.Is(err, ctx.Err()) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if isClientDisconnectError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "request canceled") ||
		strings.Contains(msg, "operation was canceled")
}

func isSafeRetryMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func shouldRetryDecision(decision keyDecision) bool {
	if !decision.failover {
		return false
	}
	switch decision.action {
	case keyActionCooldown, keyActionDisable, keyActionObserve:
		return true
	default:
		return false
	}
}
