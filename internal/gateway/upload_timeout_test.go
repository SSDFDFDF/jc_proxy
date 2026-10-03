package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"
)

func TestUploadDeadlineDoesNotLimitHeaderWaitOrStream(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		name := "http1"
		if h2 {
			name = "http2"
		}
		t.Run(name, func(t *testing.T) {
			uploaded, headers, streamed, finish := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if h2 != (req.ProtoMajor == 2) {
					t.Errorf("unexpected upstream protocol: %s (HTTP/2=%v)", req.Proto, h2)
				}
				_, err := io.Copy(io.Discard, req.Body)
				if err != nil {
					return
				}
				close(uploaded)
				select {
				case <-headers:
				case <-req.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: first\n\n")
				w.(http.Flusher).Flush()
				close(streamed)
				select {
				case <-finish:
				case <-req.Context().Done():
					return
				}
				_, _ = io.WriteString(w, "data: last\n\n")
			}))
			upstream.EnableHTTP2 = h2
			if h2 {
				upstream.StartTLS()
			} else {
				upstream.Start()
			}
			defer upstream.Close()
			r, _ := routerWithMasking(t, upstream.URL, []string{"key"}, nil)
			v := r.vendors["openai"]
			v.interimInterval = 0
			v.upstreamUploadTimeout = 100 * time.Millisecond
			v.client.Transport = upstream.Client().Transport
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req := httptest.NewRequest("POST", "/openai/test", strings.NewReader("input")).WithContext(ctx)
			w := httptest.NewRecorder()
			done := make(chan any, 1)
			go func() { defer func() { done <- recover() }(); r.ServeHTTP(w, req) }()
			select {
			case <-uploaded:
			case <-ctx.Done():
				t.Fatal("upload did not complete")
			}
			select {
			case result := <-done:
				t.Fatalf("deadline killed header wait: %v", result)
			case <-time.After(2 * v.upstreamUploadTimeout):
			}
			close(headers)
			select {
			case <-streamed:
			case <-ctx.Done():
				t.Fatal("stream did not start")
			}
			select {
			case result := <-done:
				t.Fatalf("deadline killed live stream: %v", result)
			case <-time.After(2 * v.upstreamUploadTimeout):
			}
			close(finish)
			select {
			case result := <-done:
				if result != nil {
					t.Fatalf("aborted: %v", result)
				}
			case <-ctx.Done():
				t.Fatal("stream did not finish")
			}
			s := v.pool.Snapshot()[0]
			if s.SuccessCount != 1 || s.OtherErrorCount != 0 || s.InterruptedCount() != 0 || w.Body.String() != "data: first\n\ndata: last\n\n" {
				t.Fatalf("stats=%+v body=%s", s, w.Body.String())
			}
		})
	}
}

type uploadReadDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
	calls    int
}

func (w *uploadReadDeadlineWriter) SetReadDeadline(deadline time.Time) error {
	w.deadline = deadline
	w.calls++
	return nil
}

func TestUploadDeadlineStopAndErrorAttribution(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	w := &uploadReadDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	d := &uploadDeadline{cancel: cancel, downstream: w}
	d.stop()
	d.expire()
	if context.Cause(ctx) != nil || w.calls != 0 {
		t.Fatal("stale timer canceled completed upload or changed its read deadline")
	}
	ctx2, cancel2 := context.WithCancelCause(context.Background())
	defer cancel2(nil)
	// Even if Read has not started, the deadline must prevent one racing with
	// expiry from blocking. ResponseController must also traverse Unwrap.
	d2 := &uploadDeadline{cancel: cancel2, downstream: unwrapResponseWriter{w}}
	d2.expire()
	d2.expire()
	if !errors.Is(context.Cause(ctx2), errUpstreamUploadTimeout) {
		t.Fatal("lost upload timeout")
	}
	if w.calls != 1 || w.deadline.IsZero() || w.deadline.After(time.Now()) {
		t.Fatalf("read deadline not expired exactly once: calls=%d deadline=%s", w.calls, w.deadline)
	}
	root, rootCancel := context.WithCancel(context.Background())
	rootCancel()
	if isCanceledUpstreamError(root, errUpstreamUploadTimeout) {
		t.Fatal("later client cancellation erased upload timeout")
	}
	ctx3, cancel3 := context.WithCancelCause(context.Background())
	defer cancel3(nil)
	body := &requestBodyReader{}
	body.reading.Store(true)
	d3 := &uploadDeadline{cancel: cancel3, body: body}
	d3.expire()
	if !isRequestBodyReadError(context.Cause(ctx3)) || !errors.Is(context.Cause(ctx3), errClientUploadTimeout) {
		t.Fatal("waiting for client data was blamed on upstream")
	}
}

func TestUploadDeadlinePreservesDownstreamReadTimeoutWhenStopped(t *testing.T) {
	for _, phase := range []string{"uploaded", "early-response", "transport-error"} {
		t.Run(phase, func(t *testing.T) {
			original := time.Now().Add(time.Second)
			w := &uploadReadDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), deadline: original}
			v := &vendorGateway{client: &http.Client{}, upstreamUploadTimeout: time.Minute}
			transportErr := errors.New("upstream unavailable")
			v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				defer req.Body.Close()
				if phase == "transport-error" {
					return nil, transportErr
				}
				if phase == "uploaded" {
					_, _ = io.Copy(io.Discard, req.Body)
					httptrace.ContextClientTrace(req.Context()).WroteRequest(httptrace.WroteRequestInfo{})
				}
				return &http.Response{StatusCode: 204, Header: make(http.Header), Body: http.NoBody}, nil
			})
			req, err := http.NewRequest("POST", "http://unused.invalid/test", &requestBodyReader{ReadCloser: io.NopCloser(strings.NewReader("input"))})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := v.doUpstreamRequest(req, w)
			if phase == "transport-error" {
				if !errors.Is(err, transportErr) {
					t.Fatalf("lost transport error: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
			}
			if w.calls != 0 || w.deadline != original {
				t.Fatal("changed the existing downstream read timeout without expiring")
			}
		})
	}
}

func TestUploadDeadlineOnReplayableBodyKeepsDownstreamUsable(t *testing.T) {
	r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key1", "key2"}, nil)
	v := r.vendors["openai"]
	v.interimInterval = 0
	v.upstreamUploadTimeout = 20 * time.Millisecond
	calls := 0
	v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		calls++
		if calls == 1 {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		_, _ = io.Copy(io.Discard, req.Body)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	req, err := http.NewRequest("GET", "/openai/test", strings.NewReader("input"))
	if err != nil {
		t.Fatal(err)
	}
	w := &uploadReadDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	r.ServeHTTP(w, req)
	if w.calls != 0 || calls != 2 || w.Code != 200 || w.Body.String() != "ok" {
		t.Fatalf("retry affected downstream: deadlines=%d attempts=%d status=%d body=%s", w.calls, calls, w.Code, w.Body.String())
	}
}

func TestDisabledUploadDeadlineUsesOriginalRequest(t *testing.T) {
	v := &vendorGateway{client: &http.Client{}}
	req := httptest.NewRequest("POST", "http://upstream.invalid/test", strings.NewReader("input"))
	req.RequestURI = ""
	v.client.Transport = roundTripperFunc(func(got *http.Request) (*http.Response, error) {
		if got.Context() != req.Context() {
			t.Error("disabled timeout changed request context")
		}
		return &http.Response{StatusCode: 204, Header: make(http.Header), Body: http.NoBody}, nil
	})
	resp, err := v.doUpstreamRequest(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
