package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

func TestPreviewReadFailurePreservesRequestErrorRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, method              string
		requestError, serverError bool
		wantCalls                 int
	}{
		{"request-errors-disabled", "GET", false, true, 1},
		{"request-errors-enabled", "GET", true, false, 2},
		{"both-enabled", "GET", true, true, 2},
		{"both-disabled", "GET", false, false, 1},
		{"non-idempotent-upload", "POST", true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := routerWithMasking(t, "http://unused.invalid", []string{"k1", "k2"}, func(p *config.ErrorPolicyConfig) {
				p.Failover.RequestError = &tc.requestError
				p.Failover.ServerError = &tc.serverError
			})
			v := r.vendors["openai"]
			v.interimInterval = 0
			calls := 0
			v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					return nil, err
				}
				if calls == 1 {
					return &http.Response{StatusCode: 502, Header: make(http.Header), Body: timingErrorBody{}}, nil
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, "/openai/test", strings.NewReader("uploaded")))
			wantStatus := http.StatusBadGateway
			if tc.wantCalls > 1 {
				wantStatus = http.StatusOK
			}
			if calls != tc.wantCalls || w.Code != wantStatus {
				t.Fatalf("calls=%d status=%d, want %d/%d", calls, w.Code, tc.wantCalls, wantStatus)
			}
			states := v.pool.Snapshot()
			first := states[0]
			if first.TotalRequests != 1 || first.OtherErrorCount != 1 || first.LastStatus != 502 || first.AvgHeaderMS != 999000 || first.AvgResponseMS != 999000 || first.Inflight != 0 || first.InterruptedCount() != 0 {
				t.Fatalf("lost original error accounting: %+v", first)
			}
			if second := states[1]; second.TotalRequests != tc.wantCalls-1 || second.SuccessCount != tc.wantCalls-1 || second.Inflight != 0 {
				t.Fatalf("incorrect retry accounting: %+v", second)
			}
		})
	}
}

// Like net/http, Write can succeed into a buffer while FlushError detects that
// the client cannot receive those bytes. Flush alone discards this error.
type failingFlushWriter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (w *failingFlushWriter) FlushError() error {
	w.flushes++
	return io.ErrClosedPipe
}
func (w *failingFlushWriter) Flush() { _ = w.FlushError() }

func TestStreamingFlushFailureAccounting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		readError bool
		failed    int
	}{
		{"body", 200, false, 0},
		{"empty-stream", 204, false, 0},
		{"known-http-error", 502, false, 1},
		{"known-read-error", 200, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, nil)
			v := r.vendors["openai"]
			v.interimInterval = 0
			v.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				var body io.ReadCloser = io.NopCloser(strings.NewReader("data: result\n\n"))
				if tc.status == 204 {
					body = http.NoBody
				} else if tc.readError {
					body = &bytesAndErrorBody{}
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
			})
			w := &failingFlushWriter{ResponseRecorder: httptest.NewRecorder()}
			r.ServeHTTP(w, httptest.NewRequest("POST", "/openai/test", nil))
			s := v.pool.Snapshot()[0]
			if w.flushes != 1 || s.TotalRequests != 1 || s.SuccessCount != 0 || s.OtherErrorCount != tc.failed || s.RecentRequests != tc.failed || s.InterruptedCount() != 1-tc.failed || s.Inflight != 0 {
				t.Fatalf("flushes=%d stats=%+v", w.flushes, s)
			}
			if tc.failed == 0 && (s.Failures != 0 || s.LastError != "" || !s.CooldownUntil.IsZero()) {
				t.Fatalf("flush failure poisoned upstream health: %+v", s)
			}
			if tc.failed > 0 && (s.LastStatus != 502 || s.AvgHeaderMS != 999000 || s.AvgResponseMS != 999000) {
				t.Fatalf("flush failure erased known upstream failure: %+v", s)
			}
		})
	}
}

// Hide optional interfaces unless Unwrap is intentionally implemented.
type plainResponseWriter struct{ http.ResponseWriter }
type unwrapResponseWriter struct{ http.ResponseWriter }

func (w unwrapResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestStreamingOptionalFlushSupport(t *testing.T) {
	for _, tc := range []struct {
		name        string
		wrap        func(http.ResponseWriter) http.ResponseWriter
		interrupted int
	}{
		{"unsupported", func(w http.ResponseWriter) http.ResponseWriter { return plainResponseWriter{w} }, 0},
		{"unwrapped-error", func(w http.ResponseWriter) http.ResponseWriter { return unwrapResponseWriter{w} }, 1},
	} {
		for _, empty := range []bool{false, true} {
			name := tc.name + "/body"
			if empty {
				name = tc.name + "/empty"
			}
			t.Run(name, func(t *testing.T) {
				r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, nil)
				v := r.vendors["openai"]
				v.interimInterval = 0
				v.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
					var body io.ReadCloser = &stagedReadCloser{chunks: [][]byte{[]byte("data: first\n\n"), []byte("data: second\n\n")}}
					status := 200
					if empty {
						body, status = http.NoBody, 204
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
				})
				w := &failingFlushWriter{ResponseRecorder: httptest.NewRecorder()}
				r.ServeHTTP(tc.wrap(w), httptest.NewRequest("POST", "/openai/test", nil))
				s := v.pool.Snapshot()[0]
				if w.flushes != tc.interrupted || s.TotalRequests != 1 || s.SuccessCount != 1-tc.interrupted || s.InterruptedCount() != tc.interrupted || s.OtherErrorCount != 0 || s.Inflight != 0 {
					t.Fatalf("flushes=%d stats=%+v", w.flushes, s)
				}
			})
		}
	}
}

type observingFlushWriter struct {
	http.ResponseWriter
	writeErr, flushErr error
}

func (w *observingFlushWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.writeErr = err
	return n, err
}
func (w *observingFlushWriter) FlushError() error {
	w.flushErr = http.NewResponseController(w.ResponseWriter).Flush()
	return w.flushErr
}
func (w *observingFlushWriter) Flush() { _ = w.FlushError() }

func TestRealHTTPStreamingFlushFailureIsInterrupted(t *testing.T) {
	r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, nil)
	v := r.vendors["openai"]
	v.interimInterval = 0
	v.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: result\n\n"))}, nil
	})
	type result struct {
		stats                           keystore.RuntimeStats
		deadlineErr, writeErr, flushErr error
	}
	done := make(chan result, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		deadlineErr := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(-time.Second))
		observed := &observingFlushWriter{ResponseWriter: w}
		r.ServeHTTP(observed, req)
		done <- result{v.pool.Snapshot()[0].RuntimeStats, deadlineErr, observed.writeErr, observed.flushErr}
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Second
	resp, err := client.Get(server.URL + "/openai/stream")
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Error("expired write deadline unexpectedly delivered a response")
	}
	select {
	case got := <-done:
		if got.deadlineErr != nil || got.writeErr != nil || got.flushErr == nil {
			t.Fatalf("did not exercise a flush-only failure: %+v", got)
		}
		if s := got.stats; s.TotalRequests != 1 || s.InterruptedCount() != 1 || s.SuccessCount != 0 || s.OtherErrorCount != 0 || s.RecentRequests != 0 {
			t.Fatalf("real flush failure was not an interruption: %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not settle")
	}
}
