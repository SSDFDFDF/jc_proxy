package admin

import (
	"path/filepath"
	"testing"

	"jc_proxy/internal/config"
	"jc_proxy/internal/gateway"
	"jc_proxy/internal/keystore"
)

func testConfig() *config.Config {
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Admin:  config.AdminConfig{Enabled: true, Username: "admin", Password: "admin123", SessionTTL: 0},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream:    config.UpstreamConfig{BaseURL: "https://api.openai.com", Keys: []string{"k1"}},
				LoadBalance: "round_robin",
			},
		},
	}
	_ = cfg.PrepareAndValidate()
	return cfg
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	cfg := testConfig()
	tmpDir := t.TempDir()
	cfg.Storage.UpstreamKeys.FilePath = filepath.Join(tmpDir, "upstream_keys.json")
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
	return NewService(store, rt, keyStore, sessions, NewAuditLogger(filepath.Join(tmpDir, "audit.log")))
}

func TestLogin(t *testing.T) {
	s := newTestService(t)
	token, _, err := s.Login("admin", "admin123")
	if err != nil || token == "" {
		t.Fatalf("login failed: %v", err)
	}
}

func TestVendorAndKeyCRUD(t *testing.T) {
	s := newTestService(t)
	actor := "admin"

	if err := s.AddUpstreamKey(actor, "openai", "k2"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUpstreamKey(actor, "openai", "k2"); err != nil {
		t.Fatal(err)
	}

	if err := s.AddClientKey(actor, "openai", "ck1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteClientKey(actor, "openai", "ck1"); err != nil {
		t.Fatal(err)
	}

	vc := config.VendorConfig{
		Upstream:    config.UpstreamConfig{BaseURL: "https://api.anthropic.com", Keys: []string{"a1"}},
		LoadBalance: "round_robin",
	}
	if err := s.UpsertVendor(actor, "anthropic", vc); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVendor(actor, "anthropic"); err != nil {
		t.Fatal(err)
	}
}

func TestRotatePassword(t *testing.T) {
	s := newTestService(t)
	if err := s.RotatePassword("admin", "new-pass"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login("admin", "new-pass"); err != nil {
		t.Fatalf("login with rotated password failed: %v", err)
	}
}

func TestEnableDisableUpstreamKey(t *testing.T) {
	s := newTestService(t)
	if err := s.DisableUpstreamKey("admin", "openai", "k1", "manual test", keystore.KeyStatusDisabledManual); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListUpstreamKeys()
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Items["openai"][0].Status; got != keystore.KeyStatusDisabledManual {
		t.Fatalf("unexpected key status after disable: %q", got)
	}

	if err := s.EnableUpstreamKey("admin", "openai", "k1"); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListUpstreamKeys()
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Items["openai"][0].Status; got != keystore.KeyStatusActive {
		t.Fatalf("unexpected key status after enable: %q", got)
	}
}
