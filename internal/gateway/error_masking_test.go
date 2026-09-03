package gateway

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/config"
)

func TestMatchErrorMaskRuleSemantics(t *testing.T) {
	rules := []config.ErrorMaskingRule{
		{StatusCodes: []int{405}, StatusCode: http.StatusTooManyRequests},
		{Keywords: []string{"quota exhausted"}, StatusCode: http.StatusInternalServerError},
		{StatusCodes: []int{400}, Keywords: []string{"invalid"}, StatusCode: 418},
	}

	cases := []struct {
		name     string
		status   int
		body     string
		wantOK   bool
		wantCode int
	}{
		{"status only match", http.StatusMethodNotAllowed, "anything", true, http.StatusTooManyRequests},
		{"status only mismatch", http.StatusForbidden, "anything", false, 0},
		{"keyword match", http.StatusBadRequest, "server said quota exhausted twice", true, http.StatusInternalServerError},
		{"keyword mismatch", http.StatusBadRequest, "bad request", false, 0},
		{"status and keyword both required", http.StatusBadRequest, "invalid input", true, 418},
		{"status matches but keyword missing", http.StatusBadRequest, "other error", false, 0},
		{"keyword matches but status missing", http.StatusTeapot, "invalid input", false, 0},
		{"first match wins", http.StatusMethodNotAllowed, "quota exhausted", true, http.StatusTooManyRequests},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule, ok := matchErrorMaskRule(tc.status, tc.body, rules)
			if ok != tc.wantOK {
				t.Fatalf("matchErrorMaskRule(%d, %q) ok = %v, want %v", tc.status, tc.body, ok, tc.wantOK)
			}
			if ok && rule.StatusCode != tc.wantCode {
				t.Fatalf("matched rule status_code = %d, want %d", rule.StatusCode, tc.wantCode)
			}
		})
	}

	// A rule with neither status_codes nor keywords never matches.
	if _, ok := matchErrorMaskRule(http.StatusMethodNotAllowed, "x", []config.ErrorMaskingRule{{StatusCode: 500}}); ok {
		t.Fatal("rule without status_codes/keywords should not match")
	}
}

func TestMatchUpstreamErrorMaskIgnoresSuccessResponses(t *testing.T) {
	policy := config.ErrorPolicyConfig{
		Masking: config.ErrorMaskingConfig{
			Rules: []config.ErrorMaskingRule{
				{StatusCodes: []int{http.StatusOK}, StatusCode: http.StatusTooManyRequests},
			},
		},
	}
	if _, ok := matchUpstreamErrorMask(policy, http.StatusOK, http.Header{}, []byte("ok")); ok {
		t.Fatal("success response must never be masked")
	}
	if _, ok := matchUpstreamErrorMask(policy, http.StatusMethodNotAllowed, http.Header{}, nil); ok {
		t.Fatal("rule matching 200 must not match 405")
	}
}

func TestMatchUpstreamErrorMaskMatchesJSONBodyKeywords(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	policy := config.ErrorPolicyConfig{
		Masking: config.ErrorMaskingConfig{
			Rules: []config.ErrorMaskingRule{
				{Keywords: []string{"quota exhausted"}, StatusCode: http.StatusTooManyRequests},
			},
		},
	}
	preview := []byte(`{"error":{"message":"You exceeded your quota: quota exhausted","type":"insufficient_quota"}}`)
	rule, ok := matchUpstreamErrorMask(policy, http.StatusTooManyRequests, headers, preview)
	if !ok {
		t.Fatal("expected keyword rule to match summarized JSON body")
	}
	if rule.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rule.StatusCode = %d, want 429", rule.StatusCode)
	}
}

func TestMatchGatewayErrorMask(t *testing.T) {
	policy := config.ErrorPolicyConfig{
		Masking: config.ErrorMaskingConfig{
			Rules: []config.ErrorMaskingRule{
				{Keywords: []string{"all vendor keys in cooldown"}, StatusCode: http.StatusTooManyRequests, RetryAfter: "30s"},
				{StatusCodes: []int{http.StatusBadGateway}, StatusCode: http.StatusInternalServerError},
			},
		},
	}
	if rule, ok := matchGatewayErrorMask(policy, http.StatusServiceUnavailable, "all vendor keys in cooldown or disabled"); !ok || rule.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("keyword match on gateway error failed: ok=%v rule=%#v", ok, rule)
	}
	if rule, ok := matchGatewayErrorMask(policy, http.StatusBadGateway, "upstream request failed"); !ok || rule.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status match on gateway error failed: ok=%v rule=%#v", ok, rule)
	}
	if _, ok := matchGatewayErrorMask(config.ErrorPolicyConfig{Masking: config.ErrorMaskingConfig{Enabled: boolPtrFalse()}}, http.StatusBadGateway, "upstream request failed"); ok {
		t.Fatal("masking disabled must disable gateway error masking")
	}
}

func TestExtendDecisionCooldown(t *testing.T) {
	rule := config.ErrorMaskingRule{Cooldown: 45 * time.Second}

	observe := keyDecision{action: keyActionObserve, statusCode: 405}
	extended := extendDecisionCooldown(observe, rule)
	if extended.action != keyActionCooldown || extended.cooldown != 45*time.Second {
		t.Fatalf("observe -> %#v, want cooldown 45s", extended)
	}

	short := keyDecision{action: keyActionCooldown, statusCode: 405, cooldown: 2 * time.Second}
	extended = extendDecisionCooldown(short, rule)
	if extended.cooldown != 45*time.Second {
		t.Fatalf("short cooldown extended to %v, want 45s (max wins)", extended.cooldown)
	}

	long := keyDecision{action: keyActionCooldown, statusCode: 429, cooldown: time.Hour}
	extended = extendDecisionCooldown(long, rule)
	if extended.cooldown != time.Hour {
		t.Fatalf("long cooldown downgraded to %v, want 1h (max wins)", extended.cooldown)
	}

	disable := keyDecision{action: keyActionDisable, statusCode: 401, cooldown: 0}
	extended = extendDecisionCooldown(disable, rule)
	if extended.action != keyActionDisable || extended.cooldown != 0 {
		t.Fatalf("disable decision downgraded: %#v", extended)
	}

	noop := extendDecisionCooldown(observe, config.ErrorMaskingRule{})
	if noop.action != keyActionObserve {
		t.Fatalf("rule without cooldown changed decision: %#v", noop)
	}
}

func TestBuildMaskedResponse(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		mask := buildMaskedResponse(config.ErrorMaskingRule{StatusCode: http.StatusTooManyRequests}, "")
		if mask.statusCode != http.StatusTooManyRequests {
			t.Fatalf("statusCode = %d, want 429", mask.statusCode)
		}
		if mask.contentType != "application/json" {
			t.Fatalf("contentType = %q, want application/json", mask.contentType)
		}
		if !strings.Contains(string(mask.body), `"message":"Too Many Requests"`) || !strings.Contains(string(mask.body), `"type":"gateway_error"`) {
			t.Fatalf("body = %s, want default JSON error body with status text", mask.body)
		}
		if mask.retryAfter != "" {
			t.Fatalf("retryAfter = %q, want empty", mask.retryAfter)
		}
	})

	t.Run("custom body wins over message", func(t *testing.T) {
		mask := buildMaskedResponse(config.ErrorMaskingRule{
			StatusCode:  http.StatusInternalServerError,
			Body:        "plain failure",
			Message:     "ignored",
			ContentType: "text/plain",
		}, "")
		if string(mask.body) != "plain failure" || mask.contentType != "text/plain" {
			t.Fatalf("mask = %#v, want custom body and content type", mask)
		}
	})

	t.Run("message renders json body", func(t *testing.T) {
		mask := buildMaskedResponse(config.ErrorMaskingRule{
			StatusCode: http.StatusTooManyRequests,
			Message:    "rate limited, retry later",
		}, "")
		if !strings.Contains(string(mask.body), `"message":"rate limited, retry later"`) {
			t.Fatalf("body = %s, want message embedded", mask.body)
		}
	})

	t.Run("retry after modes", func(t *testing.T) {
		preserve := buildMaskedResponse(config.ErrorMaskingRule{StatusCode: 429}, "7")
		if preserve.retryAfter != "7" {
			t.Fatalf("preserve retryAfter = %q, want 7", preserve.retryAfter)
		}
		ignore := buildMaskedResponse(config.ErrorMaskingRule{StatusCode: 429, RetryAfter: "ignore"}, "7")
		if ignore.retryAfter != "" {
			t.Fatalf("ignore retryAfter = %q, want empty", ignore.retryAfter)
		}
		synthetic := buildMaskedResponse(config.ErrorMaskingRule{StatusCode: 429, RetryAfter: "90s"}, "")
		if synthetic.retryAfter != "90" {
			t.Fatalf("set retryAfter = %q, want 90", synthetic.retryAfter)
		}
	})
}

// routerWithMasking builds a one-vendor router whose upstream keys are seeded
// and whose error policy carries the given masking rules.
func routerWithMasking(t *testing.T, upstreamURL string, keys []string, mutate func(*config.ErrorPolicyConfig)) (*Router, *config.Config) {
	t.Helper()
	policy := config.ErrorPolicyConfig{}
	if mutate != nil {
		mutate(&policy)
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: config.VendorsFromMap(map[string]config.VendorConfig{
			"openai": {
				Provider:    "openai",
				Upstream:    config.UpstreamConfig{BaseURL: upstreamURL},
				LoadBalance: "round_robin",
				ErrorPolicy: policy,
			},
		}),
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}
	router, err := newTestRouter(cfg, map[string][]string{"openai": keys})
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}
	return router, cfg
}

func TestRouterMasksUpstream405As429(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Leak", "internal-detail")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":{"message":"method not allowed","upstream_node":"node-42"}}`))
	}))
	defer upstream.Close()

	router, _ := routerWithMasking(t, upstream.URL, []string{"k1"}, func(p *config.ErrorPolicyConfig) {
		p.Masking.Rules = []config.ErrorMaskingRule{
			{StatusCodes: []int{http.StatusMethodNotAllowed}, StatusCode: http.StatusTooManyRequests},
		}
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (405 masked)", w.Code)
	}
	if got := w.Header().Get("X-Upstream-Leak"); got != "" {
		t.Fatalf("upstream header leaked through masking: %q", got)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if strings.Contains(w.Body.String(), "upstream_node") || strings.Contains(w.Body.String(), "method not allowed") {
		t.Fatalf("upstream error body leaked through masking: %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"gateway_error"`) {
		t.Fatalf("body = %q, want uniform gateway error body", w.Body.String())
	}
}

// TestRouterMasksUpstream405LoadedFromYAML drives the whole stack from a
// parsed YAML config (not programmatic structs): it proves that the masking
// rules an operator writes in the config file survive loading, validation and
// router construction, and that a live upstream 405 is intercepted and
// replaced for the client (429 in the first scenario, 500 in the second).
func TestRouterMasksUpstream405LoadedFromYAML(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Leak", "secret")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"real upstream detail"}`))
	}))
	defer upstream.Close()

	configYAML := func(maskedStatus int) string {
		return `
schema_version: 2
server:
  listen: ":8092"
storage:
  config:
    driver: "file"
  upstream_keys:
    driver: "file"
    file_path: "./data/upstream_keys.json"
vendors:
  - id: "vid_openai"
    name: "openai"
    provider: "openai"
    upstream:
      base_url: "` + upstream.URL + `"
    load_balance: "round_robin"
    error_policy:
      masking:
        rules:
          - status_codes: [405]
            status_code: ` + strconv.Itoa(maskedStatus) + "\n"
	}

	for _, masked := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		cfg, err := config.LoadBytes([]byte(configYAML(masked)))
		if err != nil {
			t.Fatalf("LoadBytes(masked=%d) failed: %v", masked, err)
		}
		router, err := newTestRouter(cfg, map[string][]string{"openai": {"k1"}})
		if err != nil {
			t.Fatalf("init router failed: %v", err)
		}

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))

		if w.Code != masked {
			t.Fatalf("client status = %d, want masked %d (upstream 405 intercepted)", w.Code, masked)
		}
		if got := w.Header().Get("X-Upstream-Leak"); got != "" {
			t.Fatalf("upstream header leaked through masking: %q", got)
		}
		if strings.Contains(w.Body.String(), "real upstream detail") {
			t.Fatalf("upstream body leaked through masking: %q", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"type":"gateway_error"`) {
			t.Fatalf("body = %q, want uniform gateway error body", w.Body.String())
		}
	}
}

func TestRouterMasksUpstream405As500WithCustomBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("method not allowed"))
	}))
	defer upstream.Close()

	router, _ := routerWithMasking(t, upstream.URL, []string{"k1"}, func(p *config.ErrorPolicyConfig) {
		p.Masking.Rules = []config.ErrorMaskingRule{
			{
				StatusCodes: []int{http.StatusMethodNotAllowed},
				StatusCode:  http.StatusInternalServerError,
				Body:        "upstream rejected the request",
				ContentType: "text/plain",
			},
		}
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (405 masked)", w.Code)
	}
	if w.Body.String() != "upstream rejected the request" {
		t.Fatalf("body = %q, want custom replacement body", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain" {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
}

func TestRouterMasksUpstreamErrorByKeyword(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"billing hard limited for tenant internal-77"}}`))
	}))
	defer upstream.Close()

	router, _ := routerWithMasking(t, upstream.URL, []string{"k1"}, func(p *config.ErrorPolicyConfig) {
		p.Masking.Rules = []config.ErrorMaskingRule{
			{Keywords: []string{"hard limited"}, StatusCode: http.StatusServiceUnavailable},
		}
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (keyword masked)", w.Code)
	}
	if strings.Contains(w.Body.String(), "internal-77") {
		t.Fatalf("sensitive upstream body leaked through masking: %q", w.Body.String())
	}
}

func TestRouterMaskingRetryAfterModes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("method not allowed"))
	}))
	defer upstream.Close()

	cases := []struct {
		name       string
		rule       config.ErrorMaskingRule
		wantHeader string
	}{
		{"preserve by default", config.ErrorMaskingRule{StatusCodes: []int{405}, StatusCode: 429}, "7"},
		{"ignore drops it", config.ErrorMaskingRule{StatusCodes: []int{405}, StatusCode: 429, RetryAfter: "ignore"}, ""},
		{"synthetic value", config.ErrorMaskingRule{StatusCodes: []int{405}, StatusCode: 429, RetryAfter: "30s"}, "30"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := tc.rule
			router, _ := routerWithMasking(t, upstream.URL, []string{"k1"}, func(p *config.ErrorPolicyConfig) {
				p.Masking.Rules = []config.ErrorMaskingRule{rule}
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429", w.Code)
			}
			if got := w.Header().Get("Retry-After"); got != tc.wantHeader {
				t.Fatalf("Retry-After = %q, want %q", got, tc.wantHeader)
			}
		})
	}
}

func TestRouterMaskingRuleCooldownExtendsBackoff(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("method not allowed"))
	}))
	defer upstream.Close()

	router, cfg := routerWithMasking(t, upstream.URL, []string{"k1"}, func(p *config.ErrorPolicyConfig) {
		p.Masking.Rules = []config.ErrorMaskingRule{
			{StatusCodes: []int{http.StatusMethodNotAllowed}, StatusCode: http.StatusTooManyRequests, Cooldown: 45 * time.Second},
		}
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}

	stats := router.VendorStats()[vendorID(t, cfg, "openai")]
	if got := stats[0]["last_status"]; got != http.StatusMethodNotAllowed {
		t.Fatalf("last_status = %#v, want real upstream 405", got)
	}
	if remaining, _ := stats[0]["backoff_remaining_seconds"].(int); remaining <= 0 {
		t.Fatalf("backoff_remaining_seconds = %#v, want > 0 (masking cooldown extends backoff)", stats[0]["backoff_remaining_seconds"])
	}
}

func TestRouterMaskingAppliesAfterFailoverExhausted(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("method not allowed"))
	}))
	defer upstream.Close()

	router, _ := routerWithMasking(t, upstream.URL, []string{"k1", "k2"}, func(p *config.ErrorPolicyConfig) {
		p.Failover.ResponseStatusCodes = []int{http.StatusMethodNotAllowed}
		p.Masking.Rules = []config.ErrorMaskingRule{
			{StatusCodes: []int{http.StatusMethodNotAllowed}, StatusCode: http.StatusTooManyRequests},
		}
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))

	if attempts != 2 {
		t.Fatalf("upstream attempts = %d, want 2 (failover still runs before masking)", attempts)
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 after failover exhausted", w.Code)
	}
}

func TestRouterMaskingSkippedWhenFailoverSucceeds(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer k1" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte("method not allowed"))
			return
		}
		_, _ = w.Write([]byte("ok from k2"))
	}))
	defer upstream.Close()

	router, _ := routerWithMasking(t, upstream.URL, []string{"k1", "k2"}, func(p *config.ErrorPolicyConfig) {
		p.Failover.ResponseStatusCodes = []int{http.StatusMethodNotAllowed}
		p.Masking.Rules = []config.ErrorMaskingRule{
			{StatusCodes: []int{http.StatusMethodNotAllowed}, StatusCode: http.StatusTooManyRequests},
		}
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (retry success must not be masked)", w.Code)
	}
	if w.Body.String() != "ok from k2" {
		t.Fatalf("body = %q, want success body from second key", w.Body.String())
	}
}

func TestRouterMaskingDisabledForwardsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("method not allowed"))
	}))
	defer upstream.Close()

	router, _ := routerWithMasking(t, upstream.URL, []string{"k1"}, func(p *config.ErrorPolicyConfig) {
		p.Masking.Enabled = boolPtrFalse()
		p.Masking.Rules = []config.ErrorMaskingRule{
			{StatusCodes: []int{http.StatusMethodNotAllowed}, StatusCode: http.StatusTooManyRequests},
		}
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 forwarded verbatim when masking disabled", w.Code)
	}
	if w.Body.String() != "method not allowed" {
		t.Fatalf("body = %q, want upstream body verbatim", w.Body.String())
	}
}

func TestRouterDoesNotMaskSuccessResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	router, _ := routerWithMasking(t, upstream.URL, []string{"k1"}, func(p *config.ErrorPolicyConfig) {
		p.Masking.Rules = []config.ErrorMaskingRule{
			{StatusCodes: []int{http.StatusOK}, StatusCode: http.StatusTooManyRequests},
		}
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))

	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("response = %d %q, want 200 ok (success never masked)", w.Code, w.Body.String())
	}
}

func TestRouterMasksGatewayUpstreamFailure(t *testing.T) {
	// Close the server immediately so every upstream attempt fails at the
	// transport level and the gateway synthesizes its 502 response.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	upstreamURL := upstream.URL
	upstream.Close()

	router, _ := routerWithMasking(t, upstreamURL, []string{"k1"}, func(p *config.ErrorPolicyConfig) {
		p.Failover.RequestError = boolPtrFalse()
		p.Masking.Rules = []config.ErrorMaskingRule{
			{StatusCodes: []int{http.StatusBadGateway}, StatusCode: http.StatusInternalServerError},
		}
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (gateway 502 masked)", w.Code)
	}
	if strings.Contains(w.Body.String(), "upstream request failed") {
		t.Fatalf("body = %q, want uniform masked body", w.Body.String())
	}
}

func TestRouterMasksAllKeysInCooldownErrorAs429(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer upstream.Close()

	router, _ := routerWithMasking(t, upstream.URL, []string{"k1"}, func(p *config.ErrorPolicyConfig) {
		p.Masking.Rules = []config.ErrorMaskingRule{
			{
				Keywords:   []string{"all vendor keys in cooldown"},
				StatusCode: http.StatusTooManyRequests,
				Message:    "gateway is rate limited, retry later",
				Cooldown:   0,
				RetryAfter: "30s",
			},
		}
	})

	// First request: upstream 429 puts the only key into cooldown.
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))
	if first.Code != http.StatusTooManyRequests {
		t.Fatalf("first response = %d, want upstream 429 forwarded", first.Code)
	}

	// Second request: no key available, gateway synthesizes 503
	// "all vendor keys in cooldown or disabled", which the masking rule
	// replaces with a uniform 429 + Retry-After.
	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second response = %d %q, want 429 (503 masked)", second.Code, second.Body.String())
	}
	if got := second.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want 30", got)
	}
	if !strings.Contains(second.Body.String(), "gateway is rate limited, retry later") {
		t.Fatalf("body = %q, want masked message", second.Body.String())
	}
}

// TestRouterMasksChildVendorErrorsThroughAggregateRouting proves masking also
// applies when the erroring vendor is reached through an aggregate vendor: the
// child's error policy drives the mask, exactly like its cooldown and failover
// policy already do.
func TestRouterMasksChildVendorErrorsThroughAggregateRouting(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("method not allowed"))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: config.VendorsFromMap(map[string]config.VendorConfig{
			"child": {
				Provider:    "generic",
				Upstream:    config.UpstreamConfig{BaseURL: upstream.URL},
				LoadBalance: "round_robin",
				ErrorPolicy: config.ErrorPolicyConfig{
					Masking: config.ErrorMaskingConfig{
						Rules: []config.ErrorMaskingRule{
							{StatusCodes: []int{http.StatusMethodNotAllowed}, StatusCode: http.StatusTooManyRequests},
						},
					},
				},
			},
			"agg": {
				Provider:    "aggregate",
				LoadBalance: "round_robin",
				Aggregate: config.AggregateConfig{Children: []config.AggregateChild{{
					VendorID: "vid_child",
				}}},
			},
		}),
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}
	router, err := newTestRouter(cfg, map[string][]string{"child": {"k1"}})
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/agg/v1/models", nil))

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (child 405 masked through aggregate route)", w.Code)
	}
	if strings.Contains(w.Body.String(), "method not allowed") {
		t.Fatalf("upstream body leaked through masking: %q", w.Body.String())
	}
}

func boolPtrFalse() *bool {
	v := false
	return &v
}
