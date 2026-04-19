package config

import (
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testConfigYAML = `
server:
  listen: ":8092"

storage:
  config:
    driver: "file"
  upstream_keys:
    driver: "file"
    file_path: "./data/upstream_keys.json"

vendors:
  openai:
    upstream:
      base_url: "https://api.openai.com"
`

func TestLoadBytesAppliesCriticalEnvOverrides(t *testing.T) {
	t.Setenv("JC_PROXY_SERVER_PORT", "18092")
	t.Setenv("DATABASE_URL", "postgres://db-user:db-pass@127.0.0.1:5432/jc_proxy?sslmode=disable")
	t.Setenv("JC_PROXY_STORAGE_MODE", "pgsql")
	t.Setenv("JC_PROXY_STORAGE_PGSQL_MAX_OPEN_CONNS", "9")
	t.Setenv("JC_PROXY_STORAGE_CONFIG_PGSQL_TABLE", "runtime_configs")
	t.Setenv("JC_PROXY_STORAGE_UPSTREAM_KEYS_PGSQL_TABLE", "runtime_upstream_keys")

	cfg, err := LoadBytes([]byte(testConfigYAML))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}

	if cfg.Server.Listen != ":18092" {
		t.Fatalf("Server.Listen = %q, want %q", cfg.Server.Listen, ":18092")
	}
	if cfg.Storage.Config.Driver != "pgsql" {
		t.Fatalf("Storage.Config.Driver = %q, want pgsql", cfg.Storage.Config.Driver)
	}
	if cfg.Storage.UpstreamKeys.Driver != "pgsql" {
		t.Fatalf("Storage.UpstreamKeys.Driver = %q, want pgsql", cfg.Storage.UpstreamKeys.Driver)
	}
	if cfg.Storage.Config.PGSQL.DSN != "postgres://db-user:db-pass@127.0.0.1:5432/jc_proxy?sslmode=disable" {
		t.Fatalf("Storage.Config.PGSQL.DSN = %q", cfg.Storage.Config.PGSQL.DSN)
	}
	if cfg.Storage.UpstreamKeys.PGSQL.DSN != "postgres://db-user:db-pass@127.0.0.1:5432/jc_proxy?sslmode=disable" {
		t.Fatalf("Storage.UpstreamKeys.PGSQL.DSN = %q", cfg.Storage.UpstreamKeys.PGSQL.DSN)
	}
	if cfg.Storage.Config.PGSQL.Table != "runtime_configs" {
		t.Fatalf("Storage.Config.PGSQL.Table = %q", cfg.Storage.Config.PGSQL.Table)
	}
	if cfg.Storage.UpstreamKeys.PGSQL.Table != "runtime_upstream_keys" {
		t.Fatalf("Storage.UpstreamKeys.PGSQL.Table = %q", cfg.Storage.UpstreamKeys.PGSQL.Table)
	}
	if cfg.Storage.Config.PGSQL.MaxOpenConns != 9 {
		t.Fatalf("Storage.Config.PGSQL.MaxOpenConns = %d, want 9", cfg.Storage.Config.PGSQL.MaxOpenConns)
	}
	if cfg.Storage.UpstreamKeys.PGSQL.MaxOpenConns != 9 {
		t.Fatalf("Storage.UpstreamKeys.PGSQL.MaxOpenConns = %d, want 9", cfg.Storage.UpstreamKeys.PGSQL.MaxOpenConns)
	}
}

func TestLoadBootstrapBytesKeepsAdminCredentialsFromConfigOverEnv(t *testing.T) {
	t.Setenv("JC_PROXY_ADMIN_USERNAME", "env-admin")
	t.Setenv("JC_PROXY_ADMIN_PASSWORD", "env-password")
	t.Setenv("JC_PROXY_ADMIN_PASSWORD_HASH", "pbkdf2$120000$envsalt$envhash")

	cfg, err := LoadBootstrapBytes([]byte(`
admin:
  enabled: true
  username: db-admin
  password: ""
  password_hash: "pbkdf2$120000$dbsalt$dbhash"

storage:
  config:
    driver: "pgsql"
    pgsql:
      dsn: "postgres://db-user:db-pass@127.0.0.1:5432/jc_proxy?sslmode=disable"
  upstream_keys:
    driver: "pgsql"
    pgsql:
      dsn: "postgres://db-user:db-pass@127.0.0.1:5432/jc_proxy?sslmode=disable"
`))
	if err != nil {
		t.Fatalf("LoadBootstrapBytes() error = %v", err)
	}

	if cfg.Admin.Username != "db-admin" {
		t.Fatalf("Admin.Username = %q, want db-admin", cfg.Admin.Username)
	}
	if cfg.Admin.Password != "" {
		t.Fatalf("Admin.Password = %q, want empty", cfg.Admin.Password)
	}
	if cfg.Admin.PasswordHash != "pbkdf2$120000$dbsalt$dbhash" {
		t.Fatalf("Admin.PasswordHash = %q, want db hash", cfg.Admin.PasswordHash)
	}
}

func TestLoadBootstrapBytesNoEnvDoesNotApplyAdminCredentialOverrides(t *testing.T) {
	t.Setenv("JC_PROXY_ADMIN_USERNAME", "env-admin")
	t.Setenv("JC_PROXY_ADMIN_PASSWORD", "env-password")
	t.Setenv("JC_PROXY_ADMIN_PASSWORD_HASH", "pbkdf2$120000$envsalt$envhash")

	cfg, err := LoadBootstrapBytesNoEnv([]byte(`
admin:
  enabled: true

storage:
  config:
    driver: "pgsql"
    pgsql:
      dsn: "postgres://db-user:db-pass@127.0.0.1:5432/jc_proxy?sslmode=disable"
  upstream_keys:
    driver: "pgsql"
    pgsql:
      dsn: "postgres://db-user:db-pass@127.0.0.1:5432/jc_proxy?sslmode=disable"
`))
	if err != nil {
		t.Fatalf("LoadBootstrapBytesNoEnv() error = %v", err)
	}

	if cfg.Admin.Username != "admin" {
		t.Fatalf("Admin.Username = %q, want default admin", cfg.Admin.Username)
	}
	if cfg.Admin.Password != "" {
		t.Fatalf("Admin.Password = %q, want empty", cfg.Admin.Password)
	}
	if cfg.Admin.PasswordHash != "" {
		t.Fatalf("Admin.PasswordHash = %q, want empty", cfg.Admin.PasswordHash)
	}
}

func TestLoadReadsDotEnvFromConfigDir(t *testing.T) {
	preserveEnv(t,
		"JC_PROXY_SERVER_LISTEN",
		"JC_PROXY_STORAGE_MODE",
		"JC_PROXY_STORAGE_PGSQL_DSN",
	)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(testConfigYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	dotEnv := "JC_PROXY_SERVER_LISTEN=0.0.0.0:19092\nJC_PROXY_STORAGE_MODE=pgsql\nJC_PROXY_STORAGE_PGSQL_DSN=postgres://dotenv:dotenv@127.0.0.1:5432/jc_proxy?sslmode=disable\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotEnv), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Listen != "0.0.0.0:19092" {
		t.Fatalf("Server.Listen = %q, want %q", cfg.Server.Listen, "0.0.0.0:19092")
	}
	if cfg.Storage.Config.Driver != "pgsql" || cfg.Storage.UpstreamKeys.Driver != "pgsql" {
		t.Fatalf("storage drivers = (%q, %q), want both pgsql", cfg.Storage.Config.Driver, cfg.Storage.UpstreamKeys.Driver)
	}
	if cfg.Storage.Config.PGSQL.DSN != "postgres://dotenv:dotenv@127.0.0.1:5432/jc_proxy?sslmode=disable" {
		t.Fatalf("Storage.Config.PGSQL.DSN = %q", cfg.Storage.Config.PGSQL.DSN)
	}
	if cfg.Storage.UpstreamKeys.PGSQL.DSN != "postgres://dotenv:dotenv@127.0.0.1:5432/jc_proxy?sslmode=disable" {
		t.Fatalf("Storage.UpstreamKeys.PGSQL.DSN = %q", cfg.Storage.UpstreamKeys.PGSQL.DSN)
	}
}

func TestLoadKeepsExplicitEnvHigherThanDotEnv(t *testing.T) {
	preserveEnv(t, "JC_PROXY_SERVER_LISTEN")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(testConfigYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("JC_PROXY_SERVER_LISTEN=:19092\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("JC_PROXY_SERVER_LISTEN", ":20000")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Listen != ":20000" {
		t.Fatalf("Server.Listen = %q, want %q", cfg.Server.Listen, ":20000")
	}
}

func TestLoadBootstrapBytesAllowsEnvOnlyPGSQL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://bootstrap:bootstrap@127.0.0.1:5432/jc_proxy?sslmode=disable")
	t.Setenv("JC_PROXY_STORAGE_MODE", "pgsql")
	t.Setenv("JC_PROXY_ADMIN_ENABLED", "true")
	t.Setenv("JC_PROXY_ADMIN_SESSION_TTL", "6h")

	cfg, err := LoadBootstrapBytes(nil)
	if err != nil {
		t.Fatalf("LoadBootstrapBytes() error = %v", err)
	}

	if !cfg.Admin.Enabled {
		t.Fatal("Admin.Enabled = false, want true")
	}
	if cfg.Admin.SessionTTL != 6*time.Hour {
		t.Fatalf("Admin.SessionTTL = %v, want 6h", cfg.Admin.SessionTTL)
	}
	if cfg.Storage.Config.Driver != "pgsql" || cfg.Storage.UpstreamKeys.Driver != "pgsql" {
		t.Fatalf("storage drivers = (%q, %q), want both pgsql", cfg.Storage.Config.Driver, cfg.Storage.UpstreamKeys.Driver)
	}
	if cfg.Storage.Config.PGSQL.DSN != "postgres://bootstrap:bootstrap@127.0.0.1:5432/jc_proxy?sslmode=disable" {
		t.Fatalf("Storage.Config.PGSQL.DSN = %q", cfg.Storage.Config.PGSQL.DSN)
	}
	if len(cfg.Vendors) != 0 {
		t.Fatalf("len(Vendors) = %d, want 0", len(cfg.Vendors))
	}
}

func TestLoadBootstrapRequiresPGSQLWhenConfigFileMissing(t *testing.T) {
	preserveEnv(t,
		"DATABASE_URL",
		"JC_PROXY_STORAGE_MODE",
		"JC_PROXY_STORAGE_CONFIG_DRIVER",
		"JC_PROXY_STORAGE_PGSQL_DSN",
	)

	missingPath := filepath.Join(t.TempDir(), "missing.yaml")
	cfg, err := LoadBootstrap(missingPath)
	if err == nil {
		t.Fatalf("LoadBootstrap() cfg = %#v, want error", cfg)
	}
	if !strings.Contains(err.Error(), "storage.config.driver=pgsql") {
		t.Fatalf("LoadBootstrap() error = %v, want storage.config.driver=pgsql", err)
	}
}

func TestLoadDefaultsAdminToDisabledWithoutCIDRRestriction(t *testing.T) {
	cfg, err := LoadBytes([]byte(testConfigYAML))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}

	if cfg.Admin.Enabled {
		t.Fatal("Admin.Enabled = true, want false")
	}
	if cfg.Admin.Password != "" {
		t.Fatalf("Admin.Password = %q, want empty", cfg.Admin.Password)
	}
	if len(cfg.Admin.AllowedCIDRs) != 0 {
		t.Fatalf("Admin.AllowedCIDRs length = %d, want 0", len(cfg.Admin.AllowedCIDRs))
	}
	if cfg.Server.WriteTimeout != 0 {
		t.Fatalf("Server.WriteTimeout = %v, want 0", cfg.Server.WriteTimeout)
	}
	policy := cfg.Vendors["openai"].ErrorPolicy
	if !boolValue(policy.AutoDisable.InvalidKey, false) {
		t.Fatal("ErrorPolicy.AutoDisable.InvalidKey = false, want true")
	}
	if !boolValue(policy.Cooldown.RequestError.Enabled, false) || policy.Cooldown.RequestError.Duration != 2*time.Second {
		t.Fatalf("request_error cooldown = (%v, %v)", boolValue(policy.Cooldown.RequestError.Enabled, false), policy.Cooldown.RequestError.Duration)
	}
	if !boolValue(policy.Failover.RateLimit, false) {
		t.Fatal("ErrorPolicy.Failover.RateLimit = false, want true")
	}
	if len(policy.Cooldown.ResponseRules) != 0 {
		t.Fatalf("len(ErrorPolicy.Cooldown.ResponseRules) = %d, want 0", len(policy.Cooldown.ResponseRules))
	}
	if len(policy.Failover.ResponseStatusCodes) != 0 {
		t.Fatalf("len(ErrorPolicy.Failover.ResponseStatusCodes) = %d, want 0", len(policy.Failover.ResponseStatusCodes))
	}
}

func TestLoadBytesSupportsCustomErrorPolicyRules(t *testing.T) {
	cfg, err := LoadBytes([]byte(`
server:
  listen: ":8092"

storage:
  config:
    driver: "file"
  upstream_keys:
    driver: "file"
    file_path: "./data/upstream_keys.json"

vendors:
  openai:
    upstream:
      base_url: "https://api.openai.com"
    error_policy:
      auto_disable:
        invalid_key_status_codes: [400, 401]
        invalid_key_keywords: ["bad credential", "key revoked"]
      cooldown:
        response_rules:
          - status_codes: [418, 429]
            duration: 45s
            retry_after: "override"
          - keywords: ["slow down"]
            duration: 10m
            retry_after: "max"
      failover:
        response_status_codes: [418, 430]
`))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}

	policy := cfg.Vendors["openai"].ErrorPolicy
	if len(policy.AutoDisable.InvalidKeyStatusCodes) != 2 || policy.AutoDisable.InvalidKeyStatusCodes[0] != 400 || policy.AutoDisable.InvalidKeyStatusCodes[1] != 401 {
		t.Fatalf("invalid_key_status_codes = %#v", policy.AutoDisable.InvalidKeyStatusCodes)
	}
	if len(policy.AutoDisable.InvalidKeyKeywords) != 2 || policy.AutoDisable.InvalidKeyKeywords[0] != "bad credential" || policy.AutoDisable.InvalidKeyKeywords[1] != "key revoked" {
		t.Fatalf("invalid_key_keywords = %#v", policy.AutoDisable.InvalidKeyKeywords)
	}
	if len(policy.Cooldown.ResponseRules) != 2 {
		t.Fatalf("len(response_rules) = %d, want 2", len(policy.Cooldown.ResponseRules))
	}
	if policy.Cooldown.ResponseRules[0].Duration != 45*time.Second || policy.Cooldown.ResponseRules[0].RetryAfter != "override" {
		t.Fatalf("first response rule = %#v", policy.Cooldown.ResponseRules[0])
	}
	if len(policy.Failover.ResponseStatusCodes) != 2 || policy.Failover.ResponseStatusCodes[0] != 418 || policy.Failover.ResponseStatusCodes[1] != 430 {
		t.Fatalf("response_status_codes = %#v", policy.Failover.ResponseStatusCodes)
	}
}

func TestLoadBytesRejectsInvalidCustomErrorPolicyRules(t *testing.T) {
	_, err := LoadBytes([]byte(`
server:
  listen: ":8092"

storage:
  config:
    driver: "file"
  upstream_keys:
    driver: "file"
    file_path: "./data/upstream_keys.json"

vendors:
  openai:
    upstream:
      base_url: "https://api.openai.com"
    error_policy:
      cooldown:
        response_rules:
          - status_codes: [700]
            duration: 5s
      failover:
        response_status_codes: [99]
`))
	if err == nil {
		t.Fatal("LoadBytes() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "invalid status code") {
		t.Fatalf("LoadBytes() error = %v, want invalid status code", err)
	}
}

func TestLoadAllowsBootstrapAdminWithoutCredentials(t *testing.T) {
	cfg, err := LoadBytes([]byte(`
server:
  listen: ":8092"

admin:
  enabled: true
  username: "admin"
  password: ""
  password_hash: ""

storage:
  config:
    driver: "file"
  upstream_keys:
    driver: "file"
    file_path: "./data/upstream_keys.json"

vendors:
  openai:
    upstream:
      base_url: "https://api.openai.com"
`))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	if !cfg.Admin.Enabled {
		t.Fatal("Admin.Enabled = false, want true")
	}
	if cfg.Admin.HasCredentials() {
		t.Fatal("Admin.HasCredentials() = true, want false")
	}
	if !cfg.NeedsBootstrapAdminPassword() {
		t.Fatal("NeedsBootstrapAdminPassword() = false, want true")
	}
}

func TestLoadBytesAppliesAdminCIDROverride(t *testing.T) {
	t.Setenv("JC_PROXY_ADMIN_ALLOWED_CIDRS", "10.0.0.0/8,192.168.0.0/16")
	t.Setenv("JC_PROXY_ADMIN_TRUSTED_PROXY_CIDRS", "172.16.0.0/12")

	cfg, err := LoadBytes([]byte(testConfigYAML))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}

	want := []string{"10.0.0.0/8", "192.168.0.0/16"}
	if len(cfg.Admin.AllowedCIDRs) != len(want) {
		t.Fatalf("Admin.AllowedCIDRs length = %d, want %d", len(cfg.Admin.AllowedCIDRs), len(want))
	}
	for i, item := range want {
		if cfg.Admin.AllowedCIDRs[i] != item {
			t.Fatalf("Admin.AllowedCIDRs[%d] = %q, want %q", i, cfg.Admin.AllowedCIDRs[i], item)
		}
	}
	if len(cfg.Admin.TrustedProxyCIDRs) != 1 || cfg.Admin.TrustedProxyCIDRs[0] != "172.16.0.0/12" {
		t.Fatalf("Admin.TrustedProxyCIDRs = %#v, want [172.16.0.0/12]", cfg.Admin.TrustedProxyCIDRs)
	}
}

func TestResolveRequestAddrUsesTrustedProxyHeaders(t *testing.T) {
	trusted, err := ParseAdminTrustedProxyCIDRs([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("ParseAdminTrustedProxyCIDRs() error = %v", err)
	}
	headers := http.Header{}
	headers.Set("X-Forwarded-For", "203.0.113.8, 10.2.3.4")

	addr, err := ResolveRequestAddr("10.1.1.1:443", headers, trusted)
	if err != nil {
		t.Fatalf("ResolveRequestAddr() error = %v", err)
	}
	if addr != netip.MustParseAddr("203.0.113.8") {
		t.Fatalf("ResolveRequestAddr() = %s, want 203.0.113.8", addr)
	}
}

func TestResolveRequestAddrIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	trusted, err := ParseAdminTrustedProxyCIDRs([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("ParseAdminTrustedProxyCIDRs() error = %v", err)
	}
	headers := http.Header{}
	headers.Set("X-Forwarded-For", "203.0.113.8")

	addr, err := ResolveRequestAddr("198.51.100.9:443", headers, trusted)
	if err != nil {
		t.Fatalf("ResolveRequestAddr() error = %v", err)
	}
	if addr != netip.MustParseAddr("198.51.100.9") {
		t.Fatalf("ResolveRequestAddr() = %s, want 198.51.100.9", addr)
	}
}

func TestParseAdminCredentialLayerYAML(t *testing.T) {
	layer, err := ParseAdminCredentialLayerYAML([]byte(`
admin:
  username: db-admin
  password_hash: db-hash
`))
	if err != nil {
		t.Fatalf("ParseAdminCredentialLayerYAML() error = %v", err)
	}

	if layer.Username == nil || *layer.Username != "db-admin" {
		t.Fatalf("Username = %#v, want db-admin", layer.Username)
	}
	if layer.Password != nil {
		t.Fatalf("Password = %#v, want nil", layer.Password)
	}
	if layer.PasswordHash == nil || *layer.PasswordHash != "db-hash" {
		t.Fatalf("PasswordHash = %#v, want db-hash", layer.PasswordHash)
	}
}

func TestResolveClientHeaderAllowlistIncludesPresetAndExplicitHeaders(t *testing.T) {
	headers := ResolveClientHeaderAllowlist(ClientHeadersConfig{
		Preset:    "openai",
		Allowlist: []string{"X-Custom-Header", "OpenAI-Project"},
	})

	for _, want := range []string{"Content-Type", "Idempotency-Key", "Openai-Project", "X-Custom-Header"} {
		if !containsString(headers, want) {
			t.Fatalf("ResolveClientHeaderAllowlist() missing %q in %#v", want, headers)
		}
	}
}

func TestPrepareAndValidateRejectsUnknownClientHeaderPreset(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{Listen: ":8092"},
		Storage: StorageConfig{
			Config:       ConfigStoreConfig{Driver: "file"},
			UpstreamKeys: UpstreamKeyStoreConfig{Driver: "file", FilePath: "./data/upstream_keys.json"},
		},
		Vendors: map[string]VendorConfig{
			"openai": {
				Upstream: UpstreamConfig{BaseURL: "https://api.openai.com"},
				ClientHeaders: ClientHeadersConfig{
					Preset: "unknown-preset",
				},
			},
		},
	}

	err := cfg.PrepareAndValidate()
	if err == nil || !strings.Contains(err.Error(), "client_headers.preset") {
		t.Fatalf("PrepareAndValidate() error = %v, want invalid client_headers.preset", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func preserveEnv(t *testing.T, keys ...string) {
	t.Helper()

	saved := make(map[string]*string, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			copyValue := value
			saved[key] = &copyValue
		} else {
			saved[key] = nil
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset env %s: %v", key, err)
		}
	}

	t.Cleanup(func() {
		for _, key := range keys {
			value := saved[key]
			var err error
			if value == nil {
				err = os.Unsetenv(key)
			} else {
				err = os.Setenv(key, *value)
			}
			if err != nil {
				t.Fatalf("restore env %s: %v", key, err)
			}
		}
	})
}
