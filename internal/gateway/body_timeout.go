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
	activityC chan struct{}
	doneC     chan struct{}
	closeOnce sync.Once
	timedOut  atomic.Bool
}

func newIdleTimeoutReadCloser(body io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if body == nil || body == http.NoBody || timeout <= 0 {
		return body
	}

	reader := &idleTimeoutReadCloser{
		body:      body,
		timeout:   timeout,
		activityC: make(chan struct{}, 1),
		doneC:     make(chan struct{}),
	}
	go reader.watch()
	return reader
}

func (r *idleTimeoutReadCloser) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		select {
		case r.activityC <- struct{}{}:
		default:
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
		close(r.doneC)
		err = r.body.Close()
	})
	return err
}

func (r *idleTimeoutReadCloser) watch() {
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()

	for {
		select {
		case <-r.doneC:
			return
		case <-r.activityC:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(r.timeout)
		case <-timer.C:
			r.timedOut.Store(true)
			_ = r.Close()
			return
		}
	}
}
