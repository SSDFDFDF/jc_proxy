package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

var (
	errUpstreamUploadTimeout = errors.New("upstream upload timeout")
	errClientUploadTimeout   = errors.New("client upload deadline exceeded")
)

type uploadDeadline struct {
	mu         sync.Mutex
	timer      *time.Timer
	done       bool
	cancel     context.CancelCauseFunc
	body       *requestBodyReader
	downstream http.ResponseWriter
}

func (d *uploadDeadline) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.done = true
	if d.timer != nil {
		d.timer.Stop()
	}
}

func (d *uploadDeadline) expire() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.done {
		d.done = true
		var cause error = errUpstreamUploadTimeout
		if d.body != nil && d.body.reading.Load() {
			// Waiting for incoming body data is not evidence against the Key.
			cause = &requestBodyReadError{cause: errClientUploadTimeout}
		}
		// Publish the cause before the downstream read deadline can cancel
		// the incoming HTTP/1 context and obscure who ended the upload.
		d.cancel(cause)
		if d.downstream != nil {
			// Canceling the upstream cannot wake a client Body.Read. Expire
			// its read deadline too, including a Read racing with cancel.
			// HTTP/2 applies this to this stream, not the shared connection.
			// No deadline is changed on the successful path, so the server's
			// existing (possibly earlier) ReadTimeout remains in force.
			// Custom writers must expose SetReadDeadline or Unwrap; otherwise
			// the server's read timeout remains the fallback.
			_ = http.NewResponseController(d.downstream).SetReadDeadline(time.Now())
		}
	}
}

// A request-local deadline covers obtaining a connection and uploading a body,
// not waiting for response headers or consuming a long stream. Never set a
// deadline on a shared HTTP/2 connection or a total http.Client.Timeout.
// downstream is supplied only for a single-use, live client body. Buffered or
// replayable bodies must not expire the downstream connection when retrying.
func (v *vendorGateway) doUpstreamRequest(req *http.Request, downstream http.ResponseWriter) (*http.Response, error) {
	if v.upstreamUploadTimeout <= 0 || req.Body == nil || req.Body == http.NoBody {
		return v.client.Do(req)
	}
	ctx, cancel := context.WithCancelCause(req.Context())
	body, _ := req.Body.(*requestBodyReader)
	deadline := &uploadDeadline{cancel: cancel, body: body, downstream: downstream}
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			deadline.stop()
		}
	}}
	request := req.WithContext(httptrace.WithClientTrace(ctx, trace))
	deadline.mu.Lock()
	deadline.timer = time.AfterFunc(v.upstreamUploadTimeout, deadline.expire)
	deadline.mu.Unlock()
	resp, err := v.client.Do(request)
	deadline.stop() // also ends the phase on an early rejection or transport error
	if err != nil {
		cause := context.Cause(ctx)
		cancel(nil)
		if isRequestBodyReadError(cause) {
			return resp, cause
		}
		if errors.Is(cause, errUpstreamUploadTimeout) {
			return resp, fmt.Errorf("%w after %s: %v", errUpstreamUploadTimeout, v.upstreamUploadTimeout, err)
		}
		return resp, err
	}
	resp.Body = &uploadResponseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type uploadResponseBody struct {
	io.ReadCloser
	cancel context.CancelCauseFunc
}

func (b *uploadResponseBody) Close() error {
	b.cancel(nil)
	return b.ReadCloser.Close()
}
