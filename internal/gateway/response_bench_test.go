package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/config"
)

// In-process comparisons isolate CPU/allocation changes. They include the
// recorder/transport fixture and are not production latency or QPS estimates.
func BenchmarkResponsePipeline(b *testing.B) {
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
		mask                    bool
	}{
		{"json", "application/json", `{"ok":true}`, 200, false},
		{"sse", "text/event-stream", "data: ok\n\n", 200, false},
		{"error", "application/json", `{"error":{"message":"temporarily unavailable","type":"server_error","code":"bad_gateway"}}`, 502, false},
		{"masked-error", "application/json", `{"error":{"message":"temporarily unavailable","type":"server_error","code":"bad_gateway"}}`, 502, true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			r := newBenchRouter(b, "http://unused.invalid")
			v := r.vendors["openai"]
			v.errorPolicy.Cooldown.NoDefaultBackoff = true
			if tc.mask {
				v.errorPolicy.Masking.Rules = []config.ErrorMaskingRule{{Keywords: []string{"temporarily"}, StatusCode: 500}}
			}
			v.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			req := httptest.NewRequest("POST", "/openai/test", nil) // don't benchmark retries
			want := tc.status
			if tc.mask {
				want = 500
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != want {
					b.Fatal(w.Code)
				}
			}
		})
	}
}

// Compare the incremental cost of the upload watchdog on a non-empty request.
func BenchmarkUploadWatchdog(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		b.Run(name, func(b *testing.B) {
			r := newBenchRouter(b, "http://unused.invalid")
			v := r.vendors["openai"]
			v.upstreamUploadTimeout = 0
			if enabled {
				v.upstreamUploadTimeout = 5 * time.Minute
			}
			v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				_, err := io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
				if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.WroteRequest != nil {
					trace.WroteRequest(httptrace.WroteRequestInfo{Err: err})
				}
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
			})
			req, err := http.NewRequest("POST", "http://proxy.invalid/openai/test", strings.NewReader(`{"model":"test","input":"hello"}`))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != 200 {
					b.Fatal(w.Code)
				}
			}
		})
	}
}

func BenchmarkInterimDisabled(b *testing.B) {
	w := httptest.NewRecorder()
	b.ReportAllocs()
	for b.Loop() {
		s := newInterimResponseSender(w, 0)
		s.commitFinal(func() {})
		s.stop()
	}
}

type benchmarkChunkBody struct{ remaining int }

func (r *benchmarkChunkBody) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	r.remaining -= n
	return n, nil
}
func (*benchmarkChunkBody) Close() error { return nil }

func BenchmarkBodyReadTimeoutChunks(b *testing.B) {
	buf := make([]byte, 4096)
	b.ReportAllocs()
	b.SetBytes(64 * 4096)
	for b.Loop() {
		r := newIdleTimeoutReadCloser(&benchmarkChunkBody{remaining: 64 * 4096}, time.Minute)
		for {
			_, err := r.Read(buf)
			if err != nil {
				if err != io.EOF {
					b.Fatal(err)
				}
				break
			}
		}
		_ = r.Close()
	}
}
