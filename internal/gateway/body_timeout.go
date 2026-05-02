package gateway

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type idleTimeoutReadCloser struct {
	body      io.ReadCloser
	timeout   time.Duration
	timer     atomic.Pointer[time.Timer]
	timedOut  atomic.Bool
	closeOnce sync.Once
}

func newIdleTimeoutReadCloser(body io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if body == nil || body == http.NoBody || timeout <= 0 {
		return body
	}

	r := &idleTimeoutReadCloser{
		body:    body,
		timeout: timeout,
	}
	r.timer.Store(time.AfterFunc(timeout, r.expire))
	return r
}

func (r *idleTimeoutReadCloser) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		if t := r.timer.Load(); t != nil {
			t.Reset(r.timeout)
		}
	}
	if err != nil {
		if err == io.EOF {
			_ = r.Close()
			return n, err
		}
		if r.timedOut.Load() {
			_ = r.Close()
			return n, fmt.Errorf("upstream body timeout after %s: %w", r.timeout, err)
		}
	}
	return n, err
}

func (r *idleTimeoutReadCloser) Close() error {
	var err error
	r.closeOnce.Do(func() {
		if t := r.timer.Load(); t != nil {
			t.Stop()
		}
		err = r.body.Close()
	})
	return err
}

func (r *idleTimeoutReadCloser) expire() {
	r.timedOut.Store(true)
	_ = r.Close()
}
