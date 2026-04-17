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
			InvalidKeyStatusCodes: []int{498},
		},
	}, 498, http.Header{}, []byte(`{"error":"custom invalid key"}`))

	if decision.action != keyActionDisable {
		t.Fatalf("decision.action = %q, want %q", decision.action, keyActionDisable)
	}
	if decision.statusCode != 498 {
		t.Fatalf("decision.statusCode = %d, want 498", decision.statusCode)
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
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
				},
				LoadBalance: "round_robin",
				ErrorPolicy: config.ErrorPolicyConfig{
					AutoDisable: config.ErrorAutoDisableConfig{
						InvalidKeyKeywords: []string{"custom bad credential"},
					},
				},
			},
		},
	}
	ctrl := &testKeyController{
		records: map[string][]keystore.Record{
			"openai": {
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
	if ctrl.lastStatus != keystore.KeyStatusDisabledAuto {
		t.Fatalf("lastStatus = %q, want %q", ctrl.lastStatus, keystore.KeyStatusDisabledAuto)
	}
	if !strings.Contains(ctrl.lastReason, "invalid key") {
		t.Fatalf("lastReason = %q, want invalid key marker", ctrl.lastReason)
	}

	stats := router.VendorStats()["openai"]
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
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1", "k2"},
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
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
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

	stats := router.VendorStats()["openai"]
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
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1", "k2"},
				},
				LoadBalance: "least_used",
				ErrorPolicy: config.ErrorPolicyConfig{
					Failover: config.ErrorFailoverConfig{
						ResponseStatusCodes: []int{430},
					},
				},
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
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

	stats := router.VendorStats()["openai"]
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
