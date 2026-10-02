package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jc_proxy/internal/config"
)

func TestErrorAttemptsCountOnceAcrossRetryMaskAndDelivery(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		mask bool
		code int
	}{
		{"retry-discards-body-before-eof", []string{"k1", "k2"}, false, 200},
		{"mask-discards-body-before-eof", []string{"k1"}, true, 500},
		{"forward-complete-error", []string{"k1"}, false, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := routerWithMasking(t, "http://unused.invalid", tc.keys, func(p *config.ErrorPolicyConfig) {
				if tc.mask {
					p.Masking.Rules = []config.ErrorMaskingRule{{StatusCodes: []int{502}, StatusCode: 500}}
				}
			})
			calls := 0
			r.vendors["openai"].client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls > 1 {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
				}
				// Bigger than preview + masking drain, so discarded bodies are
				// not read to EOF. The failure still gets a complete penalty pair.
				return &http.Response{StatusCode: 502, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 128<<10)))}, nil
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/openai/v1/models", nil))
			if w.Code != tc.code || calls != len(tc.keys) {
				t.Fatalf("status=%d calls=%d", w.Code, calls)
			}
			states := r.vendors["openai"].pool.Snapshot()
			for i, state := range states {
				if state.TotalRequests != 1 || state.Inflight != 0 || state.RecentRequests != 1 || state.HeaderSamples != 1 || state.ResponseSamples != 1 || state.InterruptedCount() != 0 {
					t.Fatalf("key %d counted incorrectly: %+v", i, state)
				}
				if i == 0 && (state.OtherErrorCount != 1 || state.LastStatus != 502 || state.AvgHeaderMS != 999000 || state.AvgResponseMS != 999000) {
					t.Fatalf("error lost real status/penalty: %+v", state)
				}
				if i > 0 && (state.SuccessCount != 1 || state.RecentSuccessCount != 1) {
					t.Fatalf("retry success not recorded: %+v", state)
				}
			}
		})
	}
}
