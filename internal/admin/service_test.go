package admin

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/gateway"
	"jc_proxy/internal/keystore"
)

func testBoolPtr(v bool) *bool {
	b := v
	return &b
}

func testConfig() *config.Config {
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Admin:  config.AdminConfig{Enabled: true, Username: "admin", Password: "admin123", SessionTTL: 0},
		Vendors: config.VendorsFromMap(map[string]config.VendorConfig{
			"openai": {
				Upstream:    config.UpstreamConfig{BaseURL: "https://api.openai.com"},
				LoadBalance: "round_robin",
			},
		}),
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
	if _, err := keyStore.Append(vendorIDForTest(t, cfg, "openai"), []string{"k1"}); err != nil {
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
	openaiID := vendorIDFromService(t, s, "openai")

	if err := s.AddUpstreamKey(actor, openaiID, "k2"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUpstreamKey(actor, openaiID, "k2"); err != nil {
		t.Fatal(err)
	}

	if err := s.AddClientKey(actor, openaiID, "ck1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteClientKey(actor, openaiID, "ck1"); err != nil {
		t.Fatal(err)
	}

	vc := config.VendorConfig{
		Upstream:    config.UpstreamConfig{BaseURL: "https://api.anthropic.com"},
		LoadBalance: "round_robin",
	}
	anthropicID, err := s.CreateVendor(actor, "anthropic", vc)
	if err != nil {
		t.Fatal(err)
	}
	if anthropicID == "" {
		t.Fatal("CreateVendor returned an empty id")
	}
	if _, err := s.CreateVendor(actor, "anthropic", vc); err == nil {
		t.Fatal("expected duplicate vendor name to be rejected")
	}
	if err := s.DeleteVendor(actor, anthropicID); err != nil {
		t.Fatal(err)
	}
}

// A rename must touch nothing but the display/route name: the id, the upstream
// key partition and its recorded status all stay put.
func TestRenameVendorKeepsIDAndKeys(t *testing.T) {
	s := newTestService(t)
	actor := "admin"
	openaiID := vendorIDFromService(t, s, "openai")

	if err := s.SetUpstreamKeyRemark(actor, openaiID, "k1", "primary"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameVendor(actor, openaiID, "openai-prod"); err != nil {
		t.Fatal(err)
	}

	cfg, err := s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.VendorByName("openai"); ok {
		t.Fatal("old vendor name still resolves after rename")
	}
	entry, ok := cfg.VendorByName("openai-prod")
	if !ok {
		t.Fatal("renamed vendor not found")
	}
	if entry.ID != openaiID {
		t.Fatalf("vendor id changed on rename: %q -> %q", openaiID, entry.ID)
	}

	records, err := s.keyStore.List(openaiID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Key != "k1" || records[0].Remark != "primary" {
		t.Fatalf("upstream keys did not survive the rename: %#v", records)
	}
}

func TestRenameVendorRejectsDuplicateName(t *testing.T) {
	s := newTestService(t)
	actor := "admin"
	openaiID := vendorIDFromService(t, s, "openai")

	if _, err := s.CreateVendor(actor, "anthropic", config.VendorConfig{
		Upstream:    config.UpstreamConfig{BaseURL: "https://api.anthropic.com"},
		LoadBalance: "round_robin",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameVendor(actor, openaiID, "anthropic"); err == nil {
		t.Fatal("expected rename to a taken name to be rejected")
	}
	if err := s.RenameVendor(actor, openaiID, "console"); err == nil {
		t.Fatal("expected rename to a reserved route name to be rejected")
	}
	if err := s.RenameVendor(actor, openaiID, "bad/name"); err == nil {
		t.Fatal("expected rename to an invalid path segment to be rejected")
	}
}

func TestUpsertVendorPreservesHiddenErrorPolicyFields(t *testing.T) {
	s := newTestService(t)

	cfg, err := s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	openaiID := vendorIDForTest(t, cfg, "openai")

	current, _ := cfg.VendorByName("openai")
	currentVC := current.VendorConfig
	currentVC.Provider = "openai"
	currentVC.ErrorPolicy = config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			Enabled:     testBoolPtr(true),
			StatusCodes: []int{401},
			Keywords:    []string{"bad_key"},
		},
		Cooldown: config.ErrorCooldownConfig{
			RateLimit:      config.ErrorCooldownRule{Enabled: testBoolPtr(true), Duration: 37 * time.Second},
			OpenAISlowDown: config.ErrorCooldownRule{Enabled: testBoolPtr(true), Duration: 11 * time.Minute},
			ResponseRules: []config.ErrorResponseCooldownRule{
				{StatusCodes: []int{http.StatusTooManyRequests}, Duration: 9 * time.Second},
			},
		},
		Failover: config.ErrorFailoverConfig{
			RequestError: testBoolPtr(true),
			RateLimit:    testBoolPtr(false),
			ServerError:  testBoolPtr(false),
		},
		Masking: config.ErrorMaskingConfig{
			Rules: []config.ErrorMaskingRule{
				{StatusCodes: []int{http.StatusMethodNotAllowed}, StatusCode: http.StatusTooManyRequests, Cooldown: 45 * time.Second},
			},
		},
	}
	mutateVendorForTest(t, cfg, "openai", func(vc *config.VendorConfig) { *vc = currentVC })
	if err := s.UpdateConfig("admin", cfg); err != nil {
		t.Fatal(err)
	}

	update := currentVC
	update.LoadBalance = "least_used"
	// The console editor does not surface masking; an update payload without
	// masking state must not wipe the configured rules.
	update.ErrorPolicy.Masking = config.ErrorMaskingConfig{}
	update.ErrorPolicy = config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{
			Enabled:     testBoolPtr(false),
			StatusCodes: []int{401, 403},
			Keywords:    []string{"incorrect_api_key"},
		},
		Cooldown: config.ErrorCooldownConfig{
			ResponseRules: []config.ErrorResponseCooldownRule{
				{StatusCodes: []int{http.StatusTeapot}, Duration: 45 * time.Second},
			},
		},
		Failover: config.ErrorFailoverConfig{
			RequestError:        testBoolPtr(false),
			ResponseStatusCodes: []int{http.StatusTeapot},
		},
	}

	if err := s.UpdateVendor("admin", openaiID, update); err != nil {
		t.Fatal(err)
	}

	reloaded, err := s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.VendorByID(openaiID)
	if !ok {
		t.Fatal("vendor missing after update")
	}

	if got.LoadBalance != "least_used" {
		t.Fatalf("LoadBalance = %q, want %q", got.LoadBalance, "least_used")
	}
	if got.ErrorPolicy.AutoDisable.Enabled == nil || *got.ErrorPolicy.AutoDisable.Enabled {
		t.Fatalf("AutoDisable.Enabled = %#v, want false updated", got.ErrorPolicy.AutoDisable.Enabled)
	}
	if len(got.ErrorPolicy.AutoDisable.StatusCodes) != 2 || got.ErrorPolicy.AutoDisable.StatusCodes[1] != 403 {
		t.Fatalf("AutoDisable.StatusCodes = %#v, want [401, 403]", got.ErrorPolicy.AutoDisable.StatusCodes)
	}
	if len(got.ErrorPolicy.AutoDisable.Keywords) != 1 || got.ErrorPolicy.AutoDisable.Keywords[0] != "incorrect_api_key" {
		t.Fatalf("AutoDisable.Keywords = %#v, want [incorrect_api_key]", got.ErrorPolicy.AutoDisable.Keywords)
	}
	if got.ErrorPolicy.Cooldown.RateLimit.Duration != 37*time.Second {
		t.Fatalf("Cooldown.RateLimit.Duration = %v, want %v", got.ErrorPolicy.Cooldown.RateLimit.Duration, 37*time.Second)
	}
	if got.ErrorPolicy.Cooldown.RateLimit.Enabled == nil || !*got.ErrorPolicy.Cooldown.RateLimit.Enabled {
		t.Fatalf("Cooldown.RateLimit.Enabled = %#v, want true preserved", got.ErrorPolicy.Cooldown.RateLimit.Enabled)
	}
	if got.ErrorPolicy.Cooldown.OpenAISlowDown.Duration != 11*time.Minute {
		t.Fatalf("Cooldown.OpenAISlowDown.Duration = %v, want %v", got.ErrorPolicy.Cooldown.OpenAISlowDown.Duration, 11*time.Minute)
	}
	if got.ErrorPolicy.Failover.RateLimit == nil || *got.ErrorPolicy.Failover.RateLimit {
		t.Fatalf("Failover.RateLimit = %#v, want false preserved", got.ErrorPolicy.Failover.RateLimit)
	}
	if got.ErrorPolicy.Failover.ServerError == nil || *got.ErrorPolicy.Failover.ServerError {
		t.Fatalf("Failover.ServerError = %#v, want false preserved", got.ErrorPolicy.Failover.ServerError)
	}
	if got.ErrorPolicy.Failover.RequestError == nil || *got.ErrorPolicy.Failover.RequestError {
		t.Fatalf("Failover.RequestError = %#v, want false updated", got.ErrorPolicy.Failover.RequestError)
	}
	if len(got.ErrorPolicy.Cooldown.ResponseRules) != 1 || got.ErrorPolicy.Cooldown.ResponseRules[0].StatusCodes[0] != http.StatusTeapot {
		t.Fatalf("Cooldown.ResponseRules = %#v, want updated visible rule", got.ErrorPolicy.Cooldown.ResponseRules)
	}
	if len(got.ErrorPolicy.Masking.Rules) != 1 {
		t.Fatalf("Masking.Rules = %#v, want preserved masking rule", got.ErrorPolicy.Masking.Rules)
	}
	rule := got.ErrorPolicy.Masking.Rules[0]
	if len(rule.StatusCodes) != 1 || rule.StatusCodes[0] != http.StatusMethodNotAllowed || rule.StatusCode != http.StatusTooManyRequests || rule.Cooldown != 45*time.Second {
		t.Fatalf("Masking.Rules[0] = %#v, want 405->429 rule with 45s cooldown", rule)
	}

	// The console editor now surfaces masking, so a payload that explicitly
	// carries masking state must replace the previous rules instead of being
	// silently merged back to the stored ones.
	withMasking := got.VendorConfig
	withMasking.ErrorPolicy.Masking = config.ErrorMaskingConfig{
		Enabled: testBoolPtr(true),
		Rules: []config.ErrorMaskingRule{
			{Keywords: []string{"hard limited"}, StatusCode: http.StatusInternalServerError, RetryAfter: "ignore"},
		},
	}
	if err := s.UpdateVendor("admin", openaiID, withMasking); err != nil {
		t.Fatal(err)
	}
	reloaded, err = s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	got, ok = reloaded.VendorByID(openaiID)
	if !ok {
		t.Fatal("vendor missing after masking update")
	}
	if got.ErrorPolicy.Masking.Enabled == nil || !*got.ErrorPolicy.Masking.Enabled {
		t.Fatalf("Masking.Enabled = %#v, want true", got.ErrorPolicy.Masking.Enabled)
	}
	if len(got.ErrorPolicy.Masking.Rules) != 1 {
		t.Fatalf("Masking.Rules = %#v, want exactly the updated rule", got.ErrorPolicy.Masking.Rules)
	}
	updated := got.ErrorPolicy.Masking.Rules[0]
	if len(updated.Keywords) != 1 || updated.Keywords[0] != "hard limited" || updated.StatusCode != http.StatusInternalServerError || updated.RetryAfter != "ignore" {
		t.Fatalf("updated masking rule = %#v, want keyword hard limited -> 500 with retry_after ignore", updated)
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
	openaiID := vendorIDFromService(t, s, "openai")
	if err := s.DisableUpstreamKey("admin", openaiID, "k1", "manual test", keystore.KeyStatusDisabledManual); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListUpstreamKeys()
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Items[openaiID][0].Status; got != keystore.KeyStatusDisabledManual {
		t.Fatalf("unexpected key status after disable: %q", got)
	}

	if err := s.EnableUpstreamKey("admin", openaiID, "k1"); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListUpstreamKeys()
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Items[openaiID][0].Status; got != keystore.KeyStatusActive {
		t.Fatalf("unexpected key status after enable: %q", got)
	}
}

func TestRecoverUpstreamKeysSetsStatusActive(t *testing.T) {
	s := newTestService(t)
	openaiID := vendorIDFromService(t, s, "openai")
	if err := s.DisableUpstreamKey("admin", openaiID, "k1", "quota", keystore.KeyStatusDisabledAuto); err != nil {
		t.Fatal(err)
	}

	if err := s.RecoverUpstreamKeys("admin", openaiID, []string{"k1"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListUpstreamKeys()
	if err != nil {
		t.Fatal(err)
	}
	if got := list.Items[openaiID][0].Status; got != keystore.KeyStatusActive {
		t.Fatalf("unexpected key status after recover: %q", got)
	}
	if got := list.Items[openaiID][0].DisableReason; got != "" {
		t.Fatalf("disable reason should be cleared after recover, got %q", got)
	}
}

func TestReplaceUpstreamKeysPreservesDisabledStatusAndKeyID(t *testing.T) {
	s := newTestService(t)
	openaiID := vendorIDFromService(t, s, "openai")
	if err := s.DisableUpstreamKey("admin", openaiID, "k1", "quota", keystore.KeyStatusDisabledAuto); err != nil {
		t.Fatal(err)
	}

	if err := s.ReplaceUpstreamKeys("admin", openaiID, []string{"k1"}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListUpstreamKeys()
	if err != nil {
		t.Fatal(err)
	}
	items := list.Items[openaiID]
	if len(items) != 1 {
		t.Fatalf("expected one key, got %d", len(items))
	}
	if got := items[0].Status; got != keystore.KeyStatusDisabledAuto {
		t.Fatalf("unexpected key status after replace: %q", got)
	}
	if got := items[0].KeyID; got == "" || got != keystore.KeyID("k1") {
		t.Fatalf("unexpected key id after replace: %q", got)
	}
}

func TestSetUpstreamKeyRemarkIsReturnedByList(t *testing.T) {
	s := newTestService(t)
	openaiID := vendorIDFromService(t, s, "openai")
	if err := s.SetUpstreamKeyRemark("admin", openaiID, "k1", "生产对话接口"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListUpstreamKeys()
	if err != nil {
		t.Fatal(err)
	}
	items := list.Items[openaiID]
	if len(items) != 1 || items[0].Remark != "生产对话接口" {
		t.Fatalf("unexpected key remark response: %+v", items)
	}
}

func TestBuildRuntimeStatsResponse(t *testing.T) {
	vendors := map[string][]map[string]any{
		"vid_openai": {
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
		VendorID: "vid_openai",
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
	if got := len(rows["vid_openai"]); got != 1 {
		t.Fatalf("unexpected page size after pagination: %d", got)
	}
	if got := rows["vid_openai"][0]["key_masked"]; got != "sk-jcp-disabled" {
		t.Fatalf("unexpected first filtered row: %v", got)
	}

	resp = buildRuntimeStatsResponse(vendors, RuntimeStatsQuery{
		VendorID: "vid_openai",
		Q:        "rate limit",
		Page:     1,
		PageSize: 20,
	})
	rows = resp["vendors"].(map[string][]map[string]any)
	if got := len(rows["vid_openai"]); got != 1 {
		t.Fatalf("unexpected keyword match count: %d", got)
	}
	if got := rows["vid_openai"][0]["key_masked"]; got != "sk-jcp-backoff" {
		t.Fatalf("unexpected keyword match row: %v", got)
	}
}
