package gateway

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

func commitTestRuntime(t *testing.T) (*Runtime, *config.Config, *keystore.FileStore) {
	t.Helper()
	cfg := &config.Config{Vendors: config.VendorsFromMap(map[string]config.VendorConfig{"openai": {Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid"}}})}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatal(err)
	}
	store, err := keystore.NewFileStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(cfg.Vendors[0].ID, []string{"key"}); err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(); _ = store.Close() })
	return rt, cfg, store
}

func TestPrepareAndFailedPersistDoNotMutateLiveHealth(t *testing.T) {
	rt, cfg, store := commitTestRuntime(t)
	old := rt.Snapshot()
	pool := old.vendors["openai"].pool
	idx, _, v, _ := pool.AcquireVersioned(nil, nil)
	pool.Cooldown(idx, 429, "current cooldown", time.Hour, v)
	if err := store.SetStatus(cfg.Vendors[0].ID, "key", keystore.KeyStatusActive, "", "admin"); err != nil {
		t.Fatal(err)
	}
	before := pool.Snapshot()[0]
	called := false
	err := rt.UpdateAndPersist(cfg, func(*config.Config) error {
		called = true
		if rt.Snapshot() != old {
			t.Fatal("published during preparation")
		}
		during := pool.Snapshot()[0]
		if during.CooldownUntil != before.CooldownUntil || during.Version != before.Version || during.LastError != before.LastError {
			t.Fatal("prepare mutated shared state")
		}
		return errors.New("disk failure")
	})
	if err == nil || !called || rt.Snapshot() != old {
		t.Fatal("failed commit published")
	}
	if err := rt.RefreshKeys(); err != nil {
		t.Fatal(err)
	}
	after := rt.Snapshot().vendors["openai"].pool.Snapshot()[0]
	if !after.CooldownUntil.IsZero() || after.Version <= before.Version {
		t.Fatal("successful commit failed to apply admin recovery")
	}
}

func TestInvalidRouterNeverPersists(t *testing.T) {
	rt, cfg, _ := commitTestRuntime(t)
	next, _ := cfg.Clone()
	next.Vendors[0].Upstream.BaseURL = ":invalid"
	called := false
	if err := rt.UpdateAndPersist(next, func(*config.Config) error { called = true; return nil }); err == nil {
		t.Fatal("invalid router accepted")
	}
	if called {
		t.Fatal("persisted before validating complete router")
	}
}

func TestRefreshReadsConfigAfterPendingCommit(t *testing.T) {
	rt, cfg, _ := commitTestRuntime(t)
	old := rt.Snapshot()
	next, _ := cfg.Clone()
	next.Vendors[0].Name = "renamed"
	entered := make(chan struct{})
	release := make(chan struct{})
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- rt.UpdateAndPersist(next, func(*config.Config) error { close(entered); <-release; return nil })
	}()
	<-entered
	// Data-plane snapshots remain available while persistence is waiting.
	if rt.Snapshot() != old {
		t.Fatal("premature publication")
	}
	refreshStarted := make(chan struct{})
	refreshDone := make(chan error, 1)
	go func() { close(refreshStarted); refreshDone <- rt.RefreshKeys() }()
	<-refreshStarted
	close(release)
	if err := <-commitDone; err != nil {
		t.Fatal(err)
	}
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	if rt.Snapshot().vendors["renamed"] == nil || rt.Snapshot().vendors["openai"] != nil {
		t.Fatal("refresh restored obsolete config")
	}
}
