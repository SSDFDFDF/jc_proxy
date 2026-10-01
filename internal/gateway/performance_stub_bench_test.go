package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jc_proxy/internal/balancer"
)

// In-process transport isolates proxy overhead from loopback scheduling noise.
// It is not a production throughput or network-latency benchmark.
func BenchmarkAggregateChildLoadScore(b *testing.B) {
	keys := make([]string, 100)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
	}
	pool, _ := balancer.NewPool("round_robin", keys)
	entry := aggregateChildEntry{vendor: &vendorGateway{pool: pool}}
	b.ReportAllocs()
	for b.Loop() {
		aggregateChildLoadScore(entry, true)
	}
}

func BenchmarkRouter_StubTransport(b *testing.B) {
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		b.Run(contentType, func(b *testing.B) {
			r := newBenchRouter(b, "http://unused.invalid")
			r.vendors["openai"].client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader("response body"))}, nil
			})
			req := httptest.NewRequest("GET", "/openai/test", nil)
			b.ReportAllocs()
			b.ResetTimer()
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
