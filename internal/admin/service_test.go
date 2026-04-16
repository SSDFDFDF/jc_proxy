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
	token, _, err := s.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("login before rotate failed: %v", err)
	}
	if err := s.RotatePassword("admin", "new-pass"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login("admin", "new-pass"); err != nil {
		t.Fatalf("login with rotated password failed: %v", err)
	}
	if _, ok := s.sessions.Validate(token); ok {
		t.Fatal("session should be invalidated after password rotate")
	}

	cfg, err := s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admin.Password != "" {
		t.Fatalf("Admin.Password = %q, want empty after rotate", cfg.Admin.Password)
	}
	if cfg.Admin.PasswordHash == "" {
		t.Fatal("Admin.PasswordHash is empty after rotate")
	}
}

func TestUpdateConfigInvalidatesSessionsWhenAdminUsernameChanges(t *testing.T) {
	s := newTestService(t)
	token, _, err := s.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	cfg, err := s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Admin.Username = "next-admin"
	if err := s.UpdateConfig("admin", cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.sessions.Validate(token); ok {
		t.Fatal("session should be invalidated after admin username update")
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

func TestBuildRuntimeStatsResponse(t *testing.T) {
	vendors := map[string][]map[string]any{
		"openai": {
			{
				"key_masked":                "sk-jcp-active",
				"status":                    keystore.KeyStatusActive,
				"backoff_remaining_seconds": 0,
				"inflight":                  0,
				"failures":                  0,
				"unauthorized_count":        0,
				"forbidden_count":           0,
				"rate_limit_count":          0,
				"other_error_count":         0,
				"last_error":                "",
			},
			{
				"key_masked":                "sk-jcp-disabled",
				"status":                    keystore.KeyStatusDisabledAuto,
				"backoff_remaining_seconds": 0,
				"inflight":                  0,
				"failures":                  1,
				"unauthorized_count":        0,
				"forbidden_count":           0,
				"rate_limit_count":          0,
				"other_error_count":         0,
				"last_error":                "quota exhausted",
			},
			{
				"key_masked":                "sk-jcp-backoff",
				"status":                    keystore.KeyStatusActive,
				"backoff_remaining_seconds": 12,
				"inflight":                  1,
				"failures":                  0,
				"unauthorized_count":        0,
				"forbidden_count":           0,
				"rate_limit_count":          2,
				"other_error_count":         0,
				"last_error":                "rate limit",
			},
		},
	}

	resp := buildRuntimeStatsResponse(vendors, RuntimeStatsQuery{
		Vendor:   "openai",
		Filter:   "issues",
		Page:     1,
		PageSize: 1,
	})

	meta, ok := resp["meta"].(RuntimeStatsMeta)
	if !ok {
		t.Fatalf("unexpected meta type: %T", resp["meta"])
	}
	if meta.Total != 2 {
		t.Fatalf("unexpected filtered total: %d", meta.Total)
	}

	rows, ok := resp["vendors"].(map[string][]map[string]any)
	if !ok {
		t.Fatalf("unexpected vendors type: %T", resp["vendors"])
	}
	if got := len(rows["openai"]); got != 1 {
		t.Fatalf("unexpected page size after pagination: %d", got)
	}
	if got := rows["openai"][0]["key_masked"]; got != "sk-jcp-disabled" {
		t.Fatalf("unexpected first filtered row: %v", got)
	}

	resp = buildRuntimeStatsResponse(vendors, RuntimeStatsQuery{
		Vendor:   "openai",
		Q:        "rate limit",
		Page:     1,
		PageSize: 20,
	})
	rows = resp["vendors"].(map[string][]map[string]any)
	if got := len(rows["openai"]); got != 1 {
		t.Fatalf("unexpected keyword match count: %d", got)
	}
	if got := rows["openai"][0]["key_masked"]; got != "sk-jcp-backoff" {
		t.Fatalf("unexpected keyword match row: %v", got)
	}
}
