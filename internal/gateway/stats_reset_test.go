package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

func statsResetTestRuntime(t *testing.T) (*Runtime, *config.Config, *keystore.FileStore, *RuntimeStatsPersister) {
	t.Helper()
	cfg := &config.Config{Vendors: config.VendorsFromMap(map[string]config.VendorConfig{
		"a": {Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid"}, LoadBalance: "least_requests"},
		"b": {Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid"}, LoadBalance: "adaptive"},
	})}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatal(err)
	}
	store, err := keystore.NewFileStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range cfg.Vendors {
		if _, err := store.Append(v.ID, []string{"key"}); err != nil {
			t.Fatal(err)
		}
		if err := store.ApplyRuntimeStatsDeltas(map[string][]keystore.RuntimeStatsDelta{v.ID: {{
			Key: "key", RuntimeStats: keystore.RuntimeStats{TotalRequests: 20, SuccessCount: 20, LastStatus: 200,
				RecentStats: keystore.RecentStats{RecentRequests: 5, RecentSuccessCount: 5, HeaderSamples: 5, AvgHeaderMS: 12}},
		}}}); err != nil {
			t.Fatal(err)
		}
	}
	rt, err := NewRuntime(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewRuntimeStatsPersister(rt, store, RuntimeStatsPersisterOptions{FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(); _ = rt.Close(); _ = store.Close() })
	return rt, cfg, store, p
}

func TestStatsResetVendorAllAndRestart(t *testing.T) {
	rt, cfg, store, p := statsResetTestRuntime(t)
	a, _ := cfg.VendorByName("a")
	b, _ := cfg.VendorByName("b")
	hA := rt.stats.Handle(a.ID, "key", keystore.RuntimeStats{})
	hB := rt.stats.Handle(b.ID, "key", keystore.RuntimeStats{})
	hA.RecordSuccess() // intentionally unflushed: must be discarded by reset
	hB.RecordSuccess() // must still be flushed, not absorbed into a new baseline
	for i := 0; i < 2; i++ {
		if count, err := p.Reset(a.ID); err != nil || count != 1 {
			t.Fatalf("vendor reset = %d, %v", count, err)
		}
		if hA.Snapshot() != (keystore.RuntimeStats{}) || hA.TotalRequestsFast() != 0 {
			t.Fatal("runtime was not reset")
		}
		if err := p.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.RefreshKeys(); err != nil {
		t.Fatal(err)
	}
	if hA.Snapshot().TotalRequests != 0 || hB.Snapshot().TotalRequests != 21 {
		t.Fatal("refresh resurrected stats or reset the wrong vendor")
	}
	hA.RecordSuccess()
	hA.RecordSample(time.Millisecond, 2*time.Millisecond, true)
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	aRecords, _ := store.List(a.ID)
	bRecords, _ := store.List(b.ID)
	if aRecords[0].TotalRequests != 1 || aRecords[0].RecentRequests != 1 || bRecords[0].TotalRequests != 21 {
		t.Fatal("incorrect baseline after reset")
	}
	if _, err := store.Append("orphan", []string{"old"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyRuntimeStatsDeltas(map[string][]keystore.RuntimeStatsDelta{
		"orphan": {{Key: "old", RuntimeStats: keystore.RuntimeStats{TotalRequests: 99}}},
	}); err != nil {
		t.Fatal(err)
	}
	if count, err := p.Reset(""); err != nil || count != 3 {
		t.Fatalf("all reset = %d, %v", count, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := keystore.NewFileStore(store.Info().FilePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	all, _ := reopened.ListAll()
	for id, records := range all {
		if records[0].RuntimeStats != (keystore.RuntimeStats{}) {
			t.Fatalf("%s restored stats at close", id)
		}
	}
	restarted, err := NewRuntime(cfg, reopened)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	for _, states := range restarted.Snapshot().VendorStateSnapshots() {
		if states[0].RuntimeStats != (keystore.RuntimeStats{}) {
			t.Fatal("restart restored stats")
		}
	}
	if _, err := p.Reset(""); err == nil {
		t.Fatal("closed persister accepted reset")
	}
}

func TestStatsResetFailureRetainsBaseline(t *testing.T) {
	rt, cfg, store, p := statsResetTestRuntime(t)
	a, _ := cfg.VendorByName("a")
	handle := rt.stats.Handle(a.ID, "key", keystore.RuntimeStats{})
	handle.RecordSuccess()
	before := handle.Snapshot()
	baseline := p.last[a.ID]["key"]
	blocker := store.Info().FilePath + ".tmp"
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Reset(a.ID); err == nil {
		t.Fatal("reset unexpectedly succeeded")
	}
	if before != handle.Snapshot() || baseline != p.last[a.ID]["key"] {
		t.Fatal("failed reset changed live stats or baseline")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	records, _ := store.List(a.ID)
	if records[0].TotalRequests != 21 {
		t.Fatalf("retry counted %d, want 21", records[0].TotalRequests)
	}
	if _, err := p.Reset(a.ID); err != nil {
		t.Fatal(err)
	}
}

func TestStatsResetRetiredHandlesAndLateCompletions(t *testing.T) {
	rt, cfg, store, p := statsResetTestRuntime(t)
	a, _ := cfg.VendorByName("a")
	oldPool := rt.Snapshot().vendors["a"].pool
	idx, _, version, ok := oldPool.AcquireVersioned(nil, nil)
	if !ok {
		t.Fatal("acquire failed")
	}
	if _, err := store.Delete(a.ID, []string{"key"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.RefreshKeys(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Reset(a.ID); err != nil {
		t.Fatal(err)
	}
	oldPool.ReleaseSuccess(idx, version)
	if _, err := store.Append(a.ID, []string{"key"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.RefreshKeys(); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	records, _ := store.List(a.ID)
	if records[0].TotalRequests != 1 {
		t.Fatalf("re-added key restored pre-reset stats: %d", records[0].TotalRequests)
	}
}

type blockingStatsResetStore struct {
	*keystore.FileStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockingStatsResetStore) ResetRuntimeStats(vendorID string) (int, error) {
	close(s.entered)
	<-s.release
	return s.FileStore.ResetRuntimeStats(vendorID)
}

func TestStatsResetSerializesFlushRefreshAndCompletions(t *testing.T) {
	rt, cfg, store, p := statsResetTestRuntime(t)
	a, _ := cfg.VendorByName("a")
	pool := rt.Snapshot().vendors["a"].pool
	idx, _, version, ok := pool.AcquireVersioned(nil, nil)
	if !ok {
		t.Fatal("acquire failed")
	}
	blocked := &blockingStatsResetStore{FileStore: store, entered: make(chan struct{}), release: make(chan struct{})}
	p.store = blocked
	done := make(chan error, 1)
	go func() { _, err := p.Reset(a.ID); done <- err }()
	<-blocked.entered
	if p.mu.TryLock() {
		p.mu.Unlock()
		close(blocked.release)
		t.Fatal("reset does not serialize Flush")
	}
	if rt.updateMu.TryLock() {
		rt.updateMu.Unlock()
		close(blocked.release)
		t.Fatal("reset does not serialize router preparation")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(3)
	go func() { defer wg.Done(); errs <- p.Flush() }()
	go func() { defer wg.Done(); errs <- rt.RefreshKeys() }()
	go func() {
		defer wg.Done()
		pool.RecordSample(idx, time.Millisecond, 2*time.Millisecond, true, version)
		pool.ReleaseSuccess(idx, version)
	}()
	close(blocked.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	records, _ := store.List(a.ID)
	if records[0].TotalRequests != 1 || records[0].RecentRequests != 1 {
		t.Fatalf("concurrent completion lost or duplicated: %#v", records[0].RuntimeStats)
	}
	if state := rt.Snapshot().vendors["a"].pool.Snapshot()[idx]; state.Inflight != 0 {
		t.Fatal("inflight leaked across reset")
	}
}

type statsStoreWithoutReset struct{}

func (statsStoreWithoutReset) ApplyRuntimeStatsDeltas(map[string][]keystore.RuntimeStatsDelta) error {
	return errors.New("unused")
}

func TestStatsResetUnsupportedStore(t *testing.T) {
	rt, cfg, _, p := statsResetTestRuntime(t)
	a, _ := cfg.VendorByName("a")
	p.store = statsStoreWithoutReset{}
	if _, err := p.Reset(a.ID); err == nil {
		t.Fatal("unsupported reset succeeded")
	}
	if rt.Snapshot().vendors["a"].pool.Snapshot()[0].TotalRequests != 20 {
		t.Fatal("unsupported reset changed runtime")
	}
}
