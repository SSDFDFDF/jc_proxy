package admin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"jc_proxy/internal/config"
)

func TestConcurrentConfigMutationsDoNotLoseUpdates(t *testing.T) {
	s := newTestService(t)
	id := vendorIDFromService(t, s, "openai")
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := range 12 {
		wg.Add(2)
		go func(i int) { defer wg.Done(); errs <- s.AddClientKey("test", id, fmt.Sprintf("client-%d", i)) }(i)
		go func(i int) {
			defer wg.Done()
			_, err := s.CreateVendor("test", fmt.Sprintf("vendor-%d", i), config.VendorConfig{Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid"}})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Vendors) != 13 {
		t.Fatalf("lost vendors: %d", len(cfg.Vendors))
	}
	v, _ := cfg.VendorByID(id)
	if len(v.ClientAuth.Keys) != 12 {
		t.Fatalf("lost client keys: %d", len(v.ClientAuth.Keys))
	}
	data, err := os.ReadFile(s.store.path)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := config.LoadBootstrapBytesNoEnv(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(disk.Vendors) != 13 {
		t.Fatal("disk and control plane diverged")
	}
	if got := len(s.runtime.Snapshot().VendorStats()); got != 13 {
		t.Fatalf("runtime vendors=%d", got)
	}
}

func TestFailedConfigSaveDoesNotPublishRouterOrResetSessions(t *testing.T) {
	s := newTestService(t)
	original, _ := s.store.GetConfig()
	oldRouter := s.runtime.Snapshot()
	// A file cannot be created under an existing ordinary file.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	s.store.path = filepath.Join(blocker, "config.yaml")
	next, _ := original.Clone()
	next.Vendors[0].Name = "changed"
	next.Admin.Username = "changed-admin"
	if err := s.UpdateConfig("test", next); err == nil {
		t.Fatal("expected persistence failure")
	}
	if s.runtime.Snapshot() != oldRouter {
		t.Fatal("failed save published new router")
	}
	current, _ := s.store.GetConfig()
	if current.Vendors[0].Name != original.Vendors[0].Name || current.Admin.Username != original.Admin.Username {
		t.Fatal("failed save changed stored config")
	}
}

type controlledConfigBackend struct {
	fail  bool
	saves int
}

func (b *controlledConfigBackend) Load() (*loadedConfig, error) { return nil, nil }
func (b *controlledConfigBackend) Save(*config.Config) error {
	b.saves++
	if b.fail {
		return errors.New("test database failure")
	}
	return nil
}
func (b *controlledConfigBackend) Close() error { return nil }

func TestRemoteConfigHasSingleCommitPoint(t *testing.T) {
	s := newTestService(t)
	b := &controlledConfigBackend{fail: true}
	s.store.remote = b
	s.store.useRemote = true
	// Even an unwritable bootstrap path must not cause a post-DB-commit error.
	s.store.path = filepath.Join(t.TempDir(), "parent", "bootstrap.yaml")
	if err := os.WriteFile(filepath.Dir(s.store.path), []byte("not-dir"), 0600); err != nil {
		t.Fatal(err)
	}
	next, _ := s.store.GetConfig()
	next.Vendors[0].Name = "remote-new"
	old := s.runtime.Snapshot()
	if err := s.UpdateConfig("test", next); err == nil {
		t.Fatal("expected database failure")
	}
	if s.runtime.Snapshot() != old {
		t.Fatal("published before database commit")
	}
	b.fail = false
	if err := s.UpdateConfig("test", next); err != nil {
		t.Fatal(err)
	}
	current, _ := s.store.GetConfig()
	if current.Vendors[0].Name != "remote-new" || s.runtime.Snapshot() == old {
		t.Fatal("successful database commit not published")
	}
	if b.saves != 2 {
		t.Fatalf("saves=%d", b.saves)
	}
}

func TestAtomicConfigWritesUseUniqueTemporaryFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := testConfig()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- writeConfigFile(path, cfg) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadBootstrapBytesNoEnv(payload); err != nil {
		t.Fatal(err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*.tmp"))
	if len(leftovers) > 0 {
		t.Fatalf("leaked temporary files: %v", leftovers)
	}
}
