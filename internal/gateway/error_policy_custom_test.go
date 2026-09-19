package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

func TestClassifyResponseUsesCustomInvalidKeyStatusCode(t *testing.T) {
	decision := classifyResponse("openai", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			StatusCodes: []int{498},
		},
	}, 498, http.Header{}, []byte(`{"error":"custom invalid key"}`))

	if decision.action != keyActionDisable {
		t.Fatalf("decision.action = %q, want %q", decision.action, keyActionDisable)
	}
	if decision.statusCode != 498 {
		t.Fatalf("decision.statusCode = %d, want 498", decision.statusCode)
	}
}

func TestClassifyResponseExtractsReadableJSONReason(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json; charset=utf-8")

	decision := classifyResponse("openai", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			StatusCodes: []int{http.StatusUnauthorized},
		},
	}, http.StatusUnauthorized, headers, []byte(`{"error":{"message":"Incorrect API key provided","type":"authentication_error","code":"invalid_api_key"}}`))

	if decision.action != keyActionDisable {
		t.Fatalf("decision.action = %q, want %q", decision.action, keyActionDisable)
	}
	if strings.Contains(decision.reason, "{") {
		t.Fatalf("decision.reason = %q, want parsed summary instead of raw JSON", decision.reason)
	}
	for _, want := range []string{"Incorrect API key provided", "authentication_error", "invalid_api_key"} {
		if !strings.Contains(decision.reason, want) {
			t.Fatalf("decision.reason = %q, want substring %q", decision.reason, want)
		}
	}
}

func TestClassifyResponseUsesBinaryPlaceholderReason(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/octet-stream")

	decision := classifyResponse("openai", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			StatusCodes: []int{498},
		},
	}, 498, headers, []byte{0x1b, 0x8f, 0x00, 0xff, 0x42, 0x10})

	if decision.action != keyActionDisable {
		t.Fatalf("decision.action = %q, want %q", decision.action, keyActionDisable)
	}
	if !strings.Contains(decision.reason, "<non-text response body: 6 bytes; content-type=application/octet-stream>") {
		t.Fatalf("decision.reason = %q, want non-text placeholder", decision.reason)
	}
}

func TestClassifyResponseDoesNotMisclassifyBinaryUnauthorizedAsInvalidKey(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/octet-stream")

	decision := classifyResponse("openai", config.ErrorPolicyConfig{}, http.StatusUnauthorized, headers, []byte{0x00, 0xff, 0x81, 0x10})

	if decision.action != keyActionCooldown {
		t.Fatalf("decision.action = %q, want %q", decision.action, keyActionCooldown)
	}
	if strings.Contains(decision.reason, "\x00") {
		t.Fatalf("decision.reason = %q, should not contain raw binary bytes", decision.reason)
	}
}

func TestRouterCustomInvalidKeyKeywordDisablesKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"custom bad credential"}}`))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: config.VendorsFromMap(map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
				},
				LoadBalance: "round_robin",
				ErrorPolicy: config.ErrorPolicyConfig{
					AutoDisable: config.ErrorAutoDisableConfig{
						Keywords: []string{"custom bad credential"},
					},
				},
			},
		}),
	}
	ctrl := &testKeyController{
		records: map[string][]keystore.Record{
			"vid_openai": {
				{Key: "k1", Status: keystore.KeyStatusActive},
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := NewWithUpstreamKeyRecords(cfg, ctrl.records, ctrl)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected upstream 403 response, got %d", w.Code)
	}
	waitForTestCondition(t, time.Second, func() bool {
		_, _, status, _, _ := ctrl.snapshot()
		return status == keystore.KeyStatusDisabledAuto
	})
	_, _, lastStatus, lastReason, _ := ctrl.snapshot()
	if lastStatus != keystore.KeyStatusDisabledAuto {
		t.Fatalf("lastStatus = %q, want %q", lastStatus, keystore.KeyStatusDisabledAuto)
	}
	if !strings.Contains(lastReason, "invalid key") {
		t.Fatalf("lastReason = %q, want invalid key marker", lastReason)
	}

	stats := router.VendorStats()["vid_openai"]
	if got := stats[0]["status"]; got != keystore.KeyStatusDisabledAuto {
		t.Fatalf("key status = %#v, want %q", got, keystore.KeyStatusDisabledAuto)
	}
}

func TestRouterUsesCustomCooldownResponseRule(t *testing.T) {
	attempts := make([]string, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		attempts = append(attempts, auth)
		if auth == "Bearer k1" {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte(`{"error":"custom cooldown"}`))
			return
		}
		_, _ = w.Write([]byte("ok from fallback key"))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: config.VendorsFromMap(map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
				},
				LoadBalance: "round_robin",
				ErrorPolicy: config.ErrorPolicyConfig{
					Cooldown: config.ErrorCooldownConfig{
						ResponseRules: []config.ErrorResponseCooldownRule{
							{StatusCodes: []int{http.StatusTeapot}, Duration: 45 * time.Second},
						},
					},
					Failover: config.ErrorFailoverConfig{
						ResponseStatusCodes: []int{http.StatusTeapot},
					},
				},
			},
		}),
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := newTestRouter(cfg, map[string][]string{"openai": {"k1", "k2"}})
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected failover success, got %d", w.Code)
	}
	if got := w.Body.String(); got != "ok from fallback key" {
		t.Fatalf("unexpected body: %q", got)
	}
	if len(attempts) != 2 || attempts[0] != "Bearer k1" || attempts[1] != "Bearer k2" {
		t.Fatalf("unexpected attempt order: %#v", attempts)
	}

	stats := router.VendorStats()["vid_openai"]
	if got := stats[0]["last_status"]; got != http.StatusTeapot {
		t.Fatalf("first key last_status = %#v, want %d", got, http.StatusTeapot)
	}
	if got := stats[0]["backoff_remaining_seconds"]; got == 0 {
		t.Fatalf("expected first key to enter cooldown, got %#v", got)
	}
}

func TestRouterUsesCustomFailoverResponseCodesWithoutCooldown(t *testing.T) {
	attempts := make([]string, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		attempts = append(attempts, auth)
		if auth == "Bearer k1" {
			w.WriteHeader(430)
			_, _ = w.Write([]byte(`{"error":"switch key"}`))
			return
		}
		_, _ = w.Write([]byte("ok after switch"))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: config.VendorsFromMap(map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
				},
				LoadBalance: "least_used",
				ErrorPolicy: config.ErrorPolicyConfig{
					Failover: config.ErrorFailoverConfig{
						ResponseStatusCodes: []int{430},
					},
				},
			},
		}),
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := newTestRouter(cfg, map[string][]string{"openai": {"k1", "k2"}})
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected failover success, got %d", w.Code)
	}
	if got := w.Body.String(); got != "ok after switch" {
		t.Fatalf("unexpected body: %q", got)
	}
	if len(attempts) != 2 || attempts[0] != "Bearer k1" || attempts[1] != "Bearer k2" {
		t.Fatalf("unexpected attempt order: %#v", attempts)
	}

	stats := router.VendorStats()["vid_openai"]
	if got := stats[0]["backoff_remaining_seconds"]; got != 0 {
		t.Fatalf("expected first key to avoid cooldown, got %#v", got)
	}
	if got := stats[0]["failures"]; got != 0 {
		t.Fatalf("expected first key to avoid failure cooldown bookkeeping, got %#v", got)
	}
	if got := stats[0]["last_status"]; got != 430 {
		t.Fatalf("first key last_status = %#v, want 430", got)
	}
}

func TestClassifyResponsePaymentRequiredDefaultsToCooldownWithoutAutoDisable(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	body := []byte(`{"error":{"message":"Billing issue or payment required","code":"insufficient_quota"}}`)

	// 1. When ErrorPolicyConfig is completely empty (no explicit auto-disable configuration)
	decision := classifyResponse("openai", config.ErrorPolicyConfig{
		Cooldown: config.ErrorCooldownConfig{
			PaymentRequired: config.ErrorCooldownRule{Duration: 3 * time.Hour},
		},
	}, http.StatusPaymentRequired, headers, body)

	if decision.action != keyActionCooldown {
		t.Fatalf("decision.action = %q, want %q (should enter cooldown instead of auto-disabling)", decision.action, keyActionCooldown)
	}
	if decision.statusCode != http.StatusPaymentRequired {
		t.Fatalf("decision.statusCode = %d, want 402", decision.statusCode)
	}
	if decision.cooldown != 3*time.Hour {
		t.Fatalf("decision.cooldown = %v, want 3h", decision.cooldown)
	}

	// 2. When 402 is not configured in StatusCodes
	decisionFalse := classifyResponse("openai", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			StatusCodes: []int{401},
		},
		Cooldown: config.ErrorCooldownConfig{
			PaymentRequired: config.ErrorCooldownRule{Duration: 30 * time.Minute},
		},
	}, http.StatusPaymentRequired, headers, body)

	if decisionFalse.action != keyActionCooldown {
		t.Fatalf("decisionFalse.action = %q, want %q", decisionFalse.action, keyActionCooldown)
	}
	if decisionFalse.cooldown != 30*time.Minute {
		t.Fatalf("decisionFalse.cooldown = %v, want 30m", decisionFalse.cooldown)
	}
}

func TestClassifyResponsePaymentRequiredExplicitTrueAutoDisables(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	body := []byte(`{"error":{"message":"You exceeded your current quota"}}`)

	decision := classifyResponse("openai", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			StatusCodes: []int{402},
		},
	}, http.StatusPaymentRequired, headers, body)

	if decision.action != keyActionDisable {
		t.Fatalf("decision.action = %q, want %q", decision.action, keyActionDisable)
	}
	if decision.statusCode != http.StatusPaymentRequired {
		t.Fatalf("decision.statusCode = %d, want 402", decision.statusCode)
	}
	if !strings.Contains(decision.reason, "auto disabled: billing or quota exhausted") {
		t.Fatalf("decision.reason = %q, want auto disabled marker", decision.reason)
	}
}

func TestClassifyResponseQuotaExhaustedDefaultsToRateLimitCooldown(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	body := []byte(`{"error":{"message":"insufficient_quota: you exceeded your current quota","type":"insufficient_quota"}}`)

	// When Keywords are not configured, it should NOT auto-disable on 429 quota exhaustion
	decision := classifyResponse("openai", config.ErrorPolicyConfig{
		Cooldown: config.ErrorCooldownConfig{
			RateLimit: config.ErrorCooldownRule{Duration: 5 * time.Second},
		},
	}, http.StatusTooManyRequests, headers, body)

	if decision.action != keyActionCooldown {
		t.Fatalf("decision.action = %q, want %q (should fall back to rate limit cooldown)", decision.action, keyActionCooldown)
	}
	if decision.statusCode != http.StatusTooManyRequests {
		t.Fatalf("decision.statusCode = %d, want 429", decision.statusCode)
	}
}

func TestClassifyResponseQuotaExhaustedExplicitTrueAutoDisables(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	body := []byte(`{"error":{"message":"insufficient_quota: you exceeded your current quota","type":"insufficient_quota"}}`)

	decision := classifyResponse("openai", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			Keywords: []string{"insufficient_quota"},
		},
	}, http.StatusTooManyRequests, headers, body)

	if decision.action != keyActionDisable {
		t.Fatalf("decision.action = %q, want %q", decision.action, keyActionDisable)
	}
	if decision.statusCode != http.StatusTooManyRequests {
		t.Fatalf("decision.statusCode = %d, want 429", decision.statusCode)
	}
	if !strings.Contains(decision.reason, "auto disabled: quota exhausted") {
		t.Fatalf("decision.reason = %q, want quota exhausted marker", decision.reason)
	}
}

func TestClassifyResponsePureConfigRuleEngine(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")

	// 1. Status code 402 configured via StatusCodes triggers auto-disable
	decision402 := classifyResponse("any_provider", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			StatusCodes: []int{402},
		},
	}, http.StatusPaymentRequired, headers, []byte(`{"error":"balance low"}`))
	if decision402.action != keyActionDisable || decision402.statusCode != 402 {
		t.Fatalf("expected 402 disable, got action=%q, code=%d", decision402.action, decision402.statusCode)
	}

	// 2. Status code 402 NOT configured does NOT trigger auto-disable, enters 3h cooldown
	decision402Cooldown := classifyResponse("any_provider", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			StatusCodes: []int{401},
		},
		Cooldown: config.ErrorCooldownConfig{
			PaymentRequired: config.ErrorCooldownRule{Duration: 3 * time.Hour},
		},
	}, http.StatusPaymentRequired, headers, []byte(`{"error":"balance low"}`))
	if decision402Cooldown.action != keyActionCooldown || decision402Cooldown.cooldown != 3*time.Hour {
		t.Fatalf("expected 402 cooldown 3h, got action=%q, cooldown=%v", decision402Cooldown.action, decision402Cooldown.cooldown)
	}

	// 3. Keyword match triggers auto-disable on 429
	decisionKw := classifyResponse("any_provider", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			Keywords: []string{"insufficient_quota", "余额不足"},
		},
	}, http.StatusTooManyRequests, headers, []byte(`{"error":{"message":"账户余额不足，请充值"}}`))
	if decisionKw.action != keyActionDisable {
		t.Fatalf("expected keyword disable, got action=%q", decisionKw.action)
	}

	// 4. Ordinary rate limit without matching keyword enters cooldown
	decisionNoKw := classifyResponse("any_provider", config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			Keywords: []string{"insufficient_quota", "余额不足"},
		},
		Cooldown: config.ErrorCooldownConfig{
			RateLimit: config.ErrorCooldownRule{Duration: 10 * time.Second},
		},
	}, http.StatusTooManyRequests, headers, []byte(`{"error":{"message":"Rate limit reached: 3 requests per minute"}}`))
	if decisionNoKw.action != keyActionCooldown || decisionNoKw.cooldown != 10*time.Second {
		t.Fatalf("expected 429 cooldown, got action=%q", decisionNoKw.action)
	}
}

func TestClassifyResponseAutoDisableMasterSwitchDisabled(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	disabled := false

	// Even if StatusCodes contains 401 and Keywords contains "incorrect_api_key",
	// Enabled: false disables the auto-disable policy completely.
	policy := config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			Enabled:     &disabled,
			StatusCodes: []int{http.StatusUnauthorized, http.StatusPaymentRequired},
			Keywords:    []string{"incorrect_api_key"},
		},
		Cooldown: config.ErrorCooldownConfig{
			Unauthorized:    config.ErrorCooldownRule{Duration: 30 * time.Minute},
			PaymentRequired: config.ErrorCooldownRule{Duration: 3 * time.Hour},
		},
	}

	// 1. 401 test
	decision401 := classifyResponse("openai", policy, http.StatusUnauthorized, headers, []byte(`{"error":{"message":"incorrect_api_key"}}`))
	if decision401.action == keyActionDisable {
		t.Fatalf("expected 401 not to auto-disable when Enabled=false, got %q", decision401.action)
	}
	if decision401.action != keyActionCooldown {
		t.Fatalf("expected 401 to fall back to cooldown, got %q", decision401.action)
	}

	// 2. 402 test
	decision402 := classifyResponse("openai", policy, http.StatusPaymentRequired, headers, []byte(`{"error":{"message":"billing"}}`))
	if decision402.action == keyActionDisable {
		t.Fatalf("expected 402 not to auto-disable when Enabled=false, got %q", decision402.action)
	}
}

