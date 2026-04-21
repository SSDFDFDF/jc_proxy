package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/gateway"
	"jc_proxy/internal/keystore"
)

func makeHandlerForTest(t *testing.T) *Handler {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Admin:  config.AdminConfig{Enabled: true, Username: "admin", Password: "admin123"},
		Storage: config.StorageConfig{
			UpstreamKeys: config.UpstreamKeyStoreConfig{
				Driver:   "file",
				FilePath: filepath.Join(tmpDir, "upstream_keys.json"),
			},
		},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream:    config.UpstreamConfig{BaseURL: "https://api.openai.com", Keys: []string{"k1"}},
				LoadBalance: "round_robin",
			},
		},
	}
	_ = cfg.PrepareAndValidate()
	keyStore, err := keystore.New(cfg.Storage.UpstreamKeys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keyStore.Close() })
	if _, err := keystore.BootstrapLegacyKeys(keyStore, cfg); err != nil {
		t.Fatal(err)
	}

	rt, err := gateway.NewRuntime(cfg, keyStore)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(tmpDir, "config.yaml"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sessions := NewSessionManager(cfg.Admin.SessionTTL)
	service := NewService(store, rt, keyStore, sessions, NewAuditLogger(filepath.Join(tmpDir, "audit.log")))
	return NewHandler(service, sessions)
}

func makeLoopbackRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "127.0.0.1:12345"
	return req
}

func loginAdminToken(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	rr := httptest.NewRecorder()
	req := makeLoopbackRequest(http.MethodPost, "/admin/login", bytes.NewReader(loginBody))
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rr.Code, rr.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatal("empty token")
	}
	return token
}

func TestAdminEndpoints(t *testing.T) {
	h := makeHandlerForTest(t)
	mux := http.NewServeMux()
	h.Register(mux)

	token := loginAdminToken(t, mux)

	cases := []string{"/admin/me", "/admin/config", "/admin/stats", "/admin/vendors"}
	for _, p := range cases {
		rr := httptest.NewRecorder()
		req := makeLoopbackRequest(http.MethodGet, p, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s failed: %d %s", p, rr.Code, rr.Body.String())
		}
	}

	vr, _ := json.Marshal(map[string]any{
		"vendor": "anthropic",
		"config": map[string]any{
			"upstream":     map[string]any{"base_url": "https://api.anthropic.com", "keys": []string{"a1"}},
			"load_balance": "round_robin",
		},
	})
	cr := httptest.NewRecorder()
	creq := makeLoopbackRequest(http.MethodPost, "/admin/vendors", bytes.NewReader(vr))
	creq.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(cr, creq)
	if cr.Code != http.StatusOK {
		t.Fatalf("vendor create failed: %d %s", cr.Code, cr.Body.String())
	}

	kr, _ := json.Marshal(map[string]string{"key": "a2"})
	akr := httptest.NewRecorder()
	akreq := makeLoopbackRequest(http.MethodPost, "/admin/vendors/anthropic/keys", bytes.NewReader(kr))
	akreq.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(akr, akreq)
	if akr.Code != http.StatusOK {
		t.Fatalf("add key failed: %d %s", akr.Code, akr.Body.String())
	}
}

func TestAdminVendorTestingEndpoints(t *testing.T) {
	h := makeHandlerForTest(t)

	type upstreamRequest struct {
		Path   string
		Query  string
		Method string
		Auth   string
		Vendor string
		Body   string
	}

	var (
		mu   sync.Mutex
		hits []upstreamRequest
	)

	record := func(req upstreamRequest) {
		mu.Lock()
		defer mu.Unlock()
		hits = append(hits, req)
	}

	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		record(upstreamRequest{
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Method: r.Method,
			Auth:   r.Header.Get("Authorization"),
			Vendor: r.Header.Get("X-Test-Vendor"),
			Body:   string(body),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer upstreamA.Close()

	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		record(upstreamRequest{
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Method: r.Method,
			Auth:   r.Header.Get("Authorization"),
			Vendor: r.Header.Get("X-Test-Vendor"),
			Body:   string(body),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstreamB.Close()

	cfg, err := h.service.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	vendorCfg := cfg.Vendors["openai"]
	vendorCfg.Upstream.BaseURL = upstreamA.URL
	vendorCfg.InjectedHeader = map[string]string{
		"X-Test-Vendor": "{{vendor}}",
	}
	vendorCfg.PathRewrites = map[string]string{
		"/alias/*": "/v1/*",
	}
	cfg.Vendors["openai"] = vendorCfg
	if err := h.service.UpdateConfig("tester", cfg); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.Register(mux)
	token := loginAdminToken(t, mux)

	metaReq := makeLoopbackRequest(http.MethodGet, "/admin/vendors/openai/test-meta", nil)
	metaReq.Header.Set("Authorization", "Bearer "+token)
	metaResp := httptest.NewRecorder()
	mux.ServeHTTP(metaResp, metaReq)
	if metaResp.Code != http.StatusOK {
		t.Fatalf("test-meta failed: %d %s", metaResp.Code, metaResp.Body.String())
	}

	var meta VendorTestMetaResponse
	if err := json.Unmarshal(metaResp.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.BaseURL != upstreamA.URL {
		t.Fatalf("meta base_url = %q, want %q", meta.BaseURL, upstreamA.URL)
	}
	if !meta.DefaultKeyAvailable {
		t.Fatalf("default key should be available")
	}
	if len(meta.ModelEndpoints) == 0 || meta.ModelEndpoints[0] != "/v1/models" {
		t.Fatalf("unexpected model endpoints: %#v", meta.ModelEndpoints)
	}
	if len(meta.RequestPresets) == 0 || meta.RequestPresets[0].Endpoint != "/v1/chat/completions" {
		t.Fatalf("unexpected request presets: %#v", meta.RequestPresets)
	}

	runBody, _ := json.Marshal(map[string]any{
		"method":   http.MethodGet,
		"endpoint": "/alias/models?limit=20",
	})
	runReq := makeLoopbackRequest(http.MethodPost, "/admin/vendors/openai/test", bytes.NewReader(runBody))
	runReq.Header.Set("Authorization", "Bearer "+token)
	runResp := httptest.NewRecorder()
	mux.ServeHTTP(runResp, runReq)
	if runResp.Code != http.StatusOK {
		t.Fatalf("default vendor test failed: %d %s", runResp.Code, runResp.Body.String())
	}

	var runPayload VendorTestResponse
	if err := json.Unmarshal(runResp.Body.Bytes(), &runPayload); err != nil {
		t.Fatal(err)
	}
	if runPayload.UsedKeySource != "default" {
		t.Fatalf("used_key_source = %q, want default", runPayload.UsedKeySource)
	}
	if !strings.Contains(runPayload.ResolvedURL, "/v1/models?limit=20") {
		t.Fatalf("resolved_url = %q", runPayload.ResolvedURL)
	}

	mu.Lock()
	if len(hits) == 0 {
		mu.Unlock()
		t.Fatal("expected upstream hit")
	}
	firstHit := hits[0]
	mu.Unlock()
	if firstHit.Path != "/v1/models" {
		t.Fatalf("default test path = %q, want /v1/models", firstHit.Path)
	}
	if firstHit.Query != "limit=20" {
		t.Fatalf("default test query = %q, want limit=20", firstHit.Query)
	}
	if firstHit.Auth != "Bearer k1" {
		t.Fatalf("default test auth = %q, want Bearer k1", firstHit.Auth)
	}
	if firstHit.Vendor != "openai" {
		t.Fatalf("default test vendor header = %q, want openai", firstHit.Vendor)
	}

	overrideBody, _ := json.Marshal(map[string]any{
		"base_url": upstreamB.URL,
		"method":   http.MethodPost,
		"endpoint": "/v1/chat/completions",
		"body":     "{\"model\":\"manual\"}",
		"key":      "manual-secret",
	})
	overrideReq := makeLoopbackRequest(http.MethodPost, "/admin/vendors/openai/test", bytes.NewReader(overrideBody))
	overrideReq.Header.Set("Authorization", "Bearer "+token)
	overrideResp := httptest.NewRecorder()
	mux.ServeHTTP(overrideResp, overrideReq)
	if overrideResp.Code != http.StatusOK {
		t.Fatalf("manual vendor test failed: %d %s", overrideResp.Code, overrideResp.Body.String())
	}

	var overridePayload VendorTestResponse
	if err := json.Unmarshal(overrideResp.Body.Bytes(), &overridePayload); err != nil {
		t.Fatal(err)
	}
	if overridePayload.UsedKeySource != "manual" {
		t.Fatalf("used_key_source = %q, want manual", overridePayload.UsedKeySource)
	}
	if overridePayload.BaseURL != upstreamB.URL {
		t.Fatalf("override base_url = %q, want %q", overridePayload.BaseURL, upstreamB.URL)
	}

	mu.Lock()
	if len(hits) < 2 {
		mu.Unlock()
		t.Fatal("expected second upstream hit")
	}
	secondHit := hits[1]
	mu.Unlock()
	if secondHit.Path != "/v1/chat/completions" {
		t.Fatalf("manual test path = %q", secondHit.Path)
	}
	if secondHit.Auth != "Bearer manual-secret" {
		t.Fatalf("manual test auth = %q, want Bearer manual-secret", secondHit.Auth)
	}
	if secondHit.Method != http.MethodPost {
		t.Fatalf("manual test method = %q, want POST", secondHit.Method)
	}
	if secondHit.Body != "{\"model\":\"manual\"}" {
		t.Fatalf("manual test body = %q", secondHit.Body)
	}
}

func TestAdminLoginBlockedForRemoteAddr(t *testing.T) {
	h := makeHandlerForTest(t)
	cfg, err := h.service.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Admin.AllowedCIDRs = []string{"127.0.0.1/32"}
	if err := h.service.store.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	req := httptest.NewRequest(http.MethodPost, "/admin/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "203.0.113.8:43210"

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("remote admin request should be hidden, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestAdminLoginAllowsRemoteAddrWhenCIDRsEmpty(t *testing.T) {
	h := makeHandlerForTest(t)
	mux := http.NewServeMux()
	h.Register(mux)

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	req := httptest.NewRequest(http.MethodPost, "/admin/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "203.0.113.8:43210"

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("remote admin request should be allowed when cidrs empty, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestAdminLoginAllowsTrustedProxyForwardedAddr(t *testing.T) {
	h := makeHandlerForTest(t)
	cfg, err := h.service.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Admin.AllowedCIDRs = []string{"203.0.113.8/32"}
	cfg.Admin.TrustedProxyCIDRs = []string{"10.0.0.0/8"}
	if err := h.service.store.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	req := httptest.NewRequest(http.MethodPost, "/admin/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "10.1.2.3:43210"
	req.Header.Set("X-Forwarded-For", "203.0.113.8")

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("trusted proxy forwarded admin request should be allowed, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestAdminLoginIgnoresSpoofedForwardedAddrFromUntrustedPeer(t *testing.T) {
	h := makeHandlerForTest(t)
	cfg, err := h.service.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Admin.AllowedCIDRs = []string{"203.0.113.8/32"}
	if err := h.service.store.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	req := httptest.NewRequest(http.MethodPost, "/admin/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "198.51.100.12:43210"
	req.Header.Set("X-Forwarded-For", "203.0.113.8")

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("spoofed forwarded admin request should be hidden, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestAdminLoginRateLimitedAndAudited(t *testing.T) {
	h := makeHandlerForTest(t)
	h.loginGuard.limit = 2
	h.loginGuard.baseDelay = 30 * time.Second
	h.loginGuard.maxDelay = 30 * time.Second

	mux := http.NewServeMux()
	h.Register(mux)

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong-pass"})
	for i, want := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		rr := httptest.NewRecorder()
		req := makeLoopbackRequest(http.MethodPost, "/admin/login", bytes.NewReader(loginBody))
		mux.ServeHTTP(rr, req)
		if rr.Code != want {
			t.Fatalf("attempt %d code = %d, want %d (%s)", i+1, rr.Code, want, rr.Body.String())
		}
	}

	lockedReqBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	lockedReq := makeLoopbackRequest(http.MethodPost, "/admin/login", bytes.NewReader(lockedReqBody))
	lockedResp := httptest.NewRecorder()
	mux.ServeHTTP(lockedResp, lockedReq)
	if lockedResp.Code != http.StatusTooManyRequests {
		t.Fatalf("locked login should be rate limited, got %d %s", lockedResp.Code, lockedResp.Body.String())
	}

	auditLog, err := os.ReadFile(h.service.audit.path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(auditLog)
	if !strings.Contains(text, `"action":"admin.login.failed"`) {
		t.Fatalf("audit log missing failed login entry: %s", text)
	}
	if !strings.Contains(text, `"action":"admin.login.rate_limited"`) {
		t.Fatalf("audit log missing rate limited login entry: %s", text)
	}
}
