package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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

func TestAdminEndpoints(t *testing.T) {
	h := makeHandlerForTest(t)
	mux := http.NewServeMux()
	h.Register(mux)

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	lr := httptest.NewRecorder()
	lreq := makeLoopbackRequest(http.MethodPost, "/admin/login", bytes.NewReader(loginBody))
	mux.ServeHTTP(lr, lreq)
	if lr.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", lr.Code, lr.Body.String())
	}
	var loginResp map[string]any
	_ = json.Unmarshal(lr.Body.Bytes(), &loginResp)
	token, _ := loginResp["token"].(string)
	if token == "" {
		t.Fatal("empty token")
	}

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
