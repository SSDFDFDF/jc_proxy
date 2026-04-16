package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jc_proxy/internal/config"
)

func TestStoreGeneratesInitialAdminPassword(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Admin: config.AdminConfig{
			Enabled:  true,
			Username: "admin",
		},
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
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("PrepareAndValidate() error = %v", err)
	}

	configPath := filepath.Join(tmpDir, "config.yaml")
	store, err := NewStore(configPath, cfg)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	password := store.GeneratedAdminPassword()
	if password == "" {
		t.Fatal("GeneratedAdminPassword() returned empty password")
	}

	saved, err := store.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if saved.Admin.Password != "" {
		t.Fatalf("saved admin password = %q, want empty", saved.Admin.Password)
	}
	if saved.Admin.PasswordHash == "" {
		t.Fatal("saved admin password_hash is empty")
	}
	if !VerifyPassword(password, saved.Admin.PasswordHash) {
		t.Fatal("generated password does not match saved hash")
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(raw)
	if strings.Contains(text, password) {
		t.Fatal("config file should not contain generated plaintext password")
	}
	if !strings.Contains(text, "password_hash:") {
		t.Fatal("config file should persist generated password hash")
	}
}

func TestStoreAllowsBootstrapConfigWithoutLocalConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Admin: config.AdminConfig{
			Enabled:  true,
			Username: "admin",
		},
		Storage: config.StorageConfig{
			UpstreamKeys: config.UpstreamKeyStoreConfig{
				Driver:   "file",
				FilePath: filepath.Join(tmpDir, "upstream_keys.json"),
			},
		},
	}
	if err := cfg.PrepareBootstrap(); err != nil {
		t.Fatalf("PrepareBootstrap() error = %v", err)
	}

	store, err := NewStore("", cfg)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if store.GeneratedAdminPassword() == "" {
		t.Fatal("GeneratedAdminPassword() returned empty password")
	}
	saved, err := store.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if saved.Admin.PasswordHash == "" {
		t.Fatal("saved admin password_hash is empty")
	}
	if len(saved.Vendors) != 0 {
		t.Fatalf("len(saved.Vendors) = %d, want 0", len(saved.Vendors))
	}
}
