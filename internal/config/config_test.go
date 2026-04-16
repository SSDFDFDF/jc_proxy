package config

import (
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
