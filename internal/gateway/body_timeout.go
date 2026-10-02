package gateway

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

var errUpstreamBodyTimeout = errors.New("upstream body timeout")

// Only a blocked upstream Read is timed. Time spent writing/flushing to the
// downstream must not close a healthy upstream or penalize its key.
// One timer is reused for the body. Updating the read deadline does not touch
// the timer heap on every chunk: a callback checks the current deadline and
// reschedules itself, or disarms while the caller is outside Read.
type idleTimeoutReadCloser struct {
	body    io.ReadCloser
	timeout time.Duration

	mu        sync.Mutex
	timer     *time.Timer
	deadline  time.Time
	armed     bool
	reading   bool
	closed    bool
	timedOut  bool
	closeOnce sync.Once
}

func newIdleTimeoutReadCloser(body io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if body == nil || body == http.NoBody || timeout <= 0 {
		return body
	}
	return &idleTimeoutReadCloser{body: body, timeout: timeout}
}

func (r *idleTimeoutReadCloser) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.closed {
		timedOut := r.timedOut
		r.mu.Unlock()
		if timedOut {
			return 0, r.timeoutError(net.ErrClosed)
		}
		return 0, net.ErrClosed
	}
	r.reading = true
	r.deadline = time.Now().Add(r.timeout)
	if !r.armed {
		r.armed = true
		if r.timer == nil {
			r.timer = time.AfterFunc(r.timeout, r.expire)
		} else {
			r.timer.Reset(r.timeout)
		}
	}
	r.mu.Unlock()

	n, err := r.body.Read(p)
	r.mu.Lock()
	r.reading = false
	timedOut := r.timedOut
	r.mu.Unlock()
	if timedOut {
		// Close may unblock Read with EOF or even buffered bytes. Neither
		// turns an expired read into a successful complete response.
		return n, r.timeoutError(err)
	}
	if err == io.EOF {
		_ = r.Close()
	}
	return n, err
}

func (r *idleTimeoutReadCloser) timeoutError(cause error) error {
	// Do not wrap EOF: callers must not mistake a timeout for completion.
	return fmt.Errorf("%w after %s: %v", errUpstreamBodyTimeout, r.timeout, cause)
}

func (r *idleTimeoutReadCloser) Close() error {
	r.mu.Lock()
	r.closed = true
	r.reading = false
	r.armed = false
	if r.timer != nil {
		r.timer.Stop()
	}
	r.mu.Unlock()
	var err error
	r.closeOnce.Do(func() { err = r.body.Close() })
	return err
}

func (r *idleTimeoutReadCloser) expire() {
	r.mu.Lock()
	if r.closed || !r.reading {
		r.armed = false
		r.mu.Unlock()
		return
	}
	if remaining := time.Until(r.deadline); remaining > 0 {
		// A callback from an earlier read cannot expire a later read.
		r.timer.Reset(remaining)
		r.mu.Unlock()
		return
	}
	r.timedOut = true
	r.closed = true
	r.armed = false
	r.mu.Unlock()
	_ = r.Close()
}
