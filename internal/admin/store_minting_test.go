package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jc_proxy/internal/config"
)

// The admin credentials carry a hash so NewStore never generates a bootstrap
// password: any persistence these tests observe is caused by minting alone.
const mintingStoreConfigYAML = `schema_version: 2
admin:
  enabled: true
  username: "admin"
  password_hash: "stub-hash"
storage:
  upstream_keys:
    driver: "file"
    file_path: "UPSTREAM_KEYS_PATH"
vendors:
  - name: "openai"
    provider: "openai"
    upstream:
      base_url: "https://api.openai.com"
`

func TestNewStoreWritesBackMintedVendorIDs(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	payload := strings.ReplaceAll(mintingStoreConfigYAML, "UPSTREAM_KEYS_PATH", filepath.Join(tmpDir, "upstream_keys.json"))
	if err := os.WriteFile(configPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	bootstrap, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !bootstrap.MintedVendorIDs() {
		t.Fatal("MintedVendorIDs() = false, want true for an id-less config file")
	}

	store, err := NewStore(configPath, bootstrap)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first, err := store.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	id := first.Vendors[0].ID
	if id == "" {
		t.Fatal("stored config has an empty vendor id")
	}

	// The write-back is the point: a second boot must load the SAME id, or
	// every key partition stored under it is orphaned.
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("reload after write-back: %v", err)
	}
	if reloaded.MintedVendorIDs() {
		t.Fatal("rewritten config file should carry ids and not mint again")
	}
	if reloaded.Vendors[0].ID != id {
		t.Fatalf("vendor id changed across restarts: %q -> %q", id, reloaded.Vendors[0].ID)
	}

	second, err := NewStore(configPath, reloaded)
	if err != nil {
		t.Fatalf("second NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	cfg, err := second.GetConfig()
	if err != nil {
		t.Fatalf("second GetConfig() error = %v", err)
	}
	if cfg.Vendors[0].ID != id {
		t.Fatalf("second boot vendor id = %q, want %q", cfg.Vendors[0].ID, id)
	}
}

type fakeConfigBackend struct {
	loaded *loadedConfig
	saved  []*config.Config
}

func (f *fakeConfigBackend) Load() (*loadedConfig, error) { return f.loaded, nil }

func (f *fakeConfigBackend) Save(cfg *config.Config) error {
	snapshot, err := cfg.Clone()
	if err != nil {
		return err
	}
	f.saved = append(f.saved, snapshot)
	return nil
}

func (f *fakeConfigBackend) Close() error { return nil }

func stubRemoteBackend(t *testing.T, fake *fakeConfigBackend) {
	t.Helper()
	restore := newRemoteConfigBackend
	newRemoteConfigBackend = func(config.ConfigStorePGSQLConfig) (configBackend, error) {
		return fake, nil
	}
	t.Cleanup(func() { newRemoteConfigBackend = restore })
}

func remoteTestBootstrap(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		Admin: config.AdminConfig{
			Enabled:      true,
			Username:     "admin",
			PasswordHash: "stub-hash",
		},
		Storage: config.StorageConfig{
			Config: config.ConfigStoreConfig{
				Driver: "pgsql",
				PGSQL:  config.ConfigStorePGSQLConfig{DSN: "postgres://stub"},
			},
			UpstreamKeys: config.UpstreamKeyStoreConfig{
				Driver:   "file",
				FilePath: filepath.Join(t.TempDir(), "upstream_keys.json"),
			},
		},
	}
	if err := cfg.PrepareBootstrap(); err != nil {
		t.Fatalf("PrepareBootstrap() error = %v", err)
	}
	return cfg
}

func TestNewStoreSavesMintedVendorIDsToRemote(t *testing.T) {
	remoteCfg, err := config.LoadBootstrapBytesNoEnv([]byte(`schema_version: 2
vendors:
  - name: "openai"
    provider: "openai"
    upstream:
      base_url: "https://api.openai.com"
`))
	if err != nil {
		t.Fatalf("load remote payload: %v", err)
	}
	if !remoteCfg.MintedVendorIDs() {
		t.Fatal("remote payload without ids should report minting")
	}
	fake := &fakeConfigBackend{loaded: &loadedConfig{cfg: remoteCfg}}
	stubRemoteBackend(t, fake)

	store, err := NewStore("", remoteTestBootstrap(t))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if len(fake.saved) != 1 {
		t.Fatalf("remote Save called %d time(s), want 1", len(fake.saved))
	}
	savedID := fake.saved[0].Vendors[0].ID
	if savedID == "" || savedID != remoteCfg.Vendors[0].ID {
		t.Fatalf("saved vendor id = %q, want the minted id %q", savedID, remoteCfg.Vendors[0].ID)
	}
}

func TestNewStoreDoesNotRewriteRemoteWithStableIDs(t *testing.T) {
	remoteCfg, err := config.LoadBootstrapBytesNoEnv([]byte(`schema_version: 2
vendors:
  - id: "vid_openai"
    name: "openai"
    provider: "openai"
    upstream:
      base_url: "https://api.openai.com"
`))
	if err != nil {
		t.Fatalf("load remote payload: %v", err)
	}
	if remoteCfg.MintedVendorIDs() {
		t.Fatal("remote payload with ids must not report minting")
	}
	fake := &fakeConfigBackend{loaded: &loadedConfig{cfg: remoteCfg}}
	stubRemoteBackend(t, fake)

	store, err := NewStore("", remoteTestBootstrap(t))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if len(fake.saved) != 0 {
		t.Fatalf("remote Save called %d time(s), want 0", len(fake.saved))
	}
}
