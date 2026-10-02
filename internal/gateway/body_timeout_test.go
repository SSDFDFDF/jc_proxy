package gateway

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type readyResponseBody struct {
	reader *strings.Reader
	closed chan struct{}
	once   sync.Once
	closes atomic.Int32
}

func (r *readyResponseBody) Read(p []byte) (int, error) {
	select {
	case <-r.closed:
		return 0, net.ErrClosed
	default:
		return r.reader.Read(p)
	}
}
func (r *readyResponseBody) Close() error {
	r.once.Do(func() { r.closes.Add(1); close(r.closed) })
	return nil
}

type blockedDownstreamWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedDownstreamWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return w.ResponseRecorder.Write(p)
}

func TestSlowDownstreamDoesNotExpireHealthyUpstream(t *testing.T) {
	body := &readyResponseBody{reader: strings.NewReader("data: healthy\n\n"), closed: make(chan struct{})}
	r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, nil)
	v := r.vendors["openai"]
	v.interimInterval = 0
	v.upstreamBodyTimeout = 10 * time.Millisecond
	v.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
	})
	w := &blockedDownstreamWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(w.release) }) }
	defer release()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/openai/stream", nil))
	}()
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("downstream write did not start")
	}
	// Exercise the real timer callback while Write, not upstream Read, waits.
	<-time.After(4 * v.upstreamBodyTimeout)
	if body.closes.Load() != 0 {
		t.Error("slow client caused the healthy upstream body to close")
	}
	release()
	select {
	case aborted := <-done:
		if aborted != nil {
			t.Fatalf("healthy response aborted: %v", aborted)
		}
	case <-time.After(time.Second):
		t.Fatal("response did not finish")
	}
	s := v.pool.Snapshot()[0]
	if s.TotalRequests != 1 || s.SuccessCount != 1 || s.OtherErrorCount != 0 || s.InterruptedCount() != 0 || s.Inflight != 0 || s.RecentSuccessCount != 1 || body.closes.Load() != 1 {
		t.Fatalf("wrong accounting after slow downstream: %+v, closes=%d", s, body.closes.Load())
	}
}

func TestBodyTimerIsInactiveOutsideRead(t *testing.T) {
	body := &readyResponseBody{reader: strings.NewReader("ok"), closed: make(chan struct{})}
	r := newIdleTimeoutReadCloser(body, time.Hour).(*idleTimeoutReadCloser)
	defer r.Close()
	if r.timer != nil {
		t.Fatal("timer must not start before the first Read")
	}
	buf := make([]byte, 1)
	if n, err := r.Read(buf); n != 1 || err != nil {
		t.Fatalf("first read=%d %v", n, err)
	}
	r.mu.Lock()
	r.deadline = time.Now().Add(-time.Second)
	r.mu.Unlock()
	r.expire() // an overdue callback outside Read must only disarm
	if body.closes.Load() != 0 {
		t.Fatal("callback outside Read closed the body")
	}
	if n, err := r.Read(buf); n != 1 || err != nil || buf[0] != 'k' {
		t.Fatalf("next read failed: %d %v", n, err)
	}
}

type gatedResponseBody struct {
	started chan struct{}
	release chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (b *gatedResponseBody) Read(p []byte) (int, error) {
	close(b.started)
	select {
	case <-b.release:
		return copy(p, "ok"), io.EOF
	case <-b.closed:
		return 0, net.ErrClosed
	}
}
func (b *gatedResponseBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

func TestStaleBodyTimerCannotExpireCurrentRead(t *testing.T) {
	body := &gatedResponseBody{started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	r := newIdleTimeoutReadCloser(body, time.Hour).(*idleTimeoutReadCloser)
	defer r.Close()
	done := make(chan error, 1)
	go func() { _, err := r.Read(make([]byte, 8)); done <- err }()
	<-body.started
	r.expire() // simulates an earlier timer's callback after a new read starts
	select {
	case <-body.closed:
		t.Fatal("old callback expired the new read before its deadline")
	default:
	}
	close(body.release)
	if err := <-done; err != io.EOF {
		t.Fatalf("read=%v, want EOF", err)
	}
}

func TestExplicitBodyCloseIsNotTimeout(t *testing.T) {
	body := &gatedResponseBody{started: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	r := newIdleTimeoutReadCloser(body, time.Hour).(*idleTimeoutReadCloser)
	done := make(chan error, 1)
	go func() { _, err := r.Read(make([]byte, 8)); done <- err }()
	<-body.started
	_ = r.Close()
	_ = r.Close()
	if err := <-done; !errors.Is(err, net.ErrClosed) || errors.Is(err, errUpstreamBodyTimeout) {
		t.Fatalf("Close was misclassified as timeout: %v", err)
	}
}
