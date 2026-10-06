package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jc_proxy/internal/config"
)

func TestClientAccessRequiresConfiguredKeysAndExplicitOptOut(t *testing.T) {
	enabled, disabled := true, false
	for _, provider := range []string{"generic", "openai", "anthropic", "gemini", "deepseek", "azure_openai", "aggregate", "passthrough"} {
		for _, tc := range []struct {
			name   string
			auth   config.ClientAuthConfig
			header string
			token  string
			allow  bool
		}{
			{"unconfigured", config.ClientAuthConfig{}, "", "", false},
			{"unconfigured-with-token", config.ClientAuthConfig{}, "Authorization", "Bearer client", false},
			{"enabled-empty", config.ClientAuthConfig{Enabled: &enabled}, "", "", false},
			{"disabled-empty", config.ClientAuthConfig{Enabled: &disabled}, "", "", false},
			{"disabled-blanks", config.ClientAuthConfig{Enabled: &disabled, Keys: []string{" ", "\t"}}, "", "", false},
			{"default-requires-token", config.ClientAuthConfig{Keys: []string{"client"}}, "", "", false},
			{"default-rejects-wrong-token", config.ClientAuthConfig{Keys: []string{"client"}}, "Authorization", "Bearer wrong", false},
			{"default-bearer", config.ClientAuthConfig{Keys: []string{" client ", "client"}}, "Authorization", "Bearer client", true},
			{"default-api-key", config.ClientAuthConfig{Keys: []string{"client"}}, "X-API-Key", "client", true},
			{"enabled-requires-token", config.ClientAuthConfig{Enabled: &enabled, Keys: []string{"client"}}, "", "", false},
			{"enabled-valid", config.ClientAuthConfig{Enabled: &enabled, Keys: []string{"client"}}, "Authorization", "Bearer client", true},
			{"explicit-opt-out", config.ClientAuthConfig{Enabled: &disabled, Keys: []string{"client"}}, "", "", true},
			{"explicit-opt-out-ignores-token", config.ClientAuthConfig{Enabled: &disabled, Keys: []string{"client"}}, "Authorization", "Bearer wrong", true},
		} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				vendor := config.VendorConfig{
					Provider: provider, Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid"}, ClientAuth: tc.auth,
				}
				if provider == "passthrough" {
					vendor.Provider = "generic"
					vendor.UpstreamAuth.Mode = "passthrough"
				}
				vendors := map[string]config.VendorConfig{"entry": vendor}
				upstreamName := "entry"
				if provider == "aggregate" {
					vendor.Aggregate.Children = []config.AggregateChild{{VendorID: "vid_child", Weight: 1}}
					vendors["entry"] = vendor
					// Internal dispatch is authorized by the aggregate entry, not
					// the child's separate public endpoint credentials.
					vendors["child"] = config.VendorConfig{Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid"}}
					upstreamName = "child"
				}
				cfg := &config.Config{Vendors: config.VendorsFromMap(vendors)}
				router, err := NewWithUpstreamKeyRecords(cfg, seedUpstreamKeys(cfg, map[string][]string{upstreamName: {"upstream-key"}}), nil)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				router.vendors[upstreamName].client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody}, nil
				})
				req := httptest.NewRequest(http.MethodGet, "/entry/v1/models", nil)
				if tc.header != "" {
					req.Header.Set(tc.header, tc.token)
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				wantStatus, wantCalls := http.StatusUnauthorized, 0
				if tc.allow {
					wantStatus, wantCalls = http.StatusNoContent, 1
				}
				if w.Code != wantStatus || calls != wantCalls {
					t.Fatalf("status=%d calls=%d, want %d/%d: %s", w.Code, calls, wantStatus, wantCalls, w.Body)
				}
				if !tc.allow && router.vendors[upstreamName].pool.Snapshot()[0].TotalRequests != 0 {
					t.Fatal("denied request must not acquire or count upstream traffic")
				}
				if provider == "aggregate" {
					w = httptest.NewRecorder()
					router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/child/v1/models", nil))
					if w.Code != http.StatusUnauthorized || calls != wantCalls {
						t.Fatal("aggregate access opened unconfigured child's public endpoint")
					}
				}
			})
		}
	}
}

func TestAuthorizeClientFailsClosedWithoutPreparedConfig(t *testing.T) {
	for _, keys := range [][]string{nil, {"", " ", "\t"}} {
		v := &vendorGateway{clientAuth: clientAuthKeys(config.ClientAuthConfig{Keys: keys}), clientAuthDisabled: true}
		if err := v.authorizeClient(httptest.NewRequest("GET", "/", nil)); err == nil || !strings.Contains(err.Error(), "no client api keys") {
			t.Fatalf("empty keys bypassed auth: %v", err)
		}
	}
}
