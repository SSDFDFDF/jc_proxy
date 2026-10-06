package gateway

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"jc_proxy/internal/balancer"
	"jc_proxy/internal/config"
)

func TestPerformanceConfigCommitAndFailedPersistence(t *testing.T) {
	rt, cfg, store := commitTestRuntime(t)
	vendorID := cfg.Vendors[0].ID
	if _, err := store.Append(vendorID, []string{"slow"}); err != nil {
		t.Fatal(err)
	}
	cfg.Vendors[0].LoadBalance = "adaptive"
	disabled := 0
	cfg.Vendors[0].Performance = &config.PerformanceConfig{ExplorationInterval: &disabled}
	if err := rt.Update(cfg); err != nil {
		t.Fatal(err)
	}
	pool := rt.Snapshot().vendors["openai"].pool
	for i, state := range pool.Snapshot() {
		header := 10 * time.Millisecond
		if state.Key == "slow" {
			header = 20 * time.Millisecond
		}
		for range 2 {
			pool.RecordSample(i, header, time.Hour, true)
		}
	}
	// Hold fast key inflight; default concurrency factor favors idle slow key.
	var held []int
	for range 3 {
		i, key, _, ok := pool.AcquireVersioned(nil, []int{0})
		if !ok || key != "key" {
			t.Fatal("unexpected fixture order")
		}
		held = append(held, i)
	}
	pick := func(p *balancer.Pool, want string) {
		t.Helper()
		i, key, ok := p.Acquire()
		if !ok || key != want {
			t.Fatalf("pick=%s want=%s", key, want)
		}
		p.Release(i)
	}
	pick(pool, "slow")
	next, err := cfg.Clone()
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	next.Vendors[0].Performance.InflightWeight = &zero
	old := rt.Snapshot()
	if err := rt.UpdateAndPersist(next, func(*config.Config) error { pick(pool, "slow"); return errors.New("disk failure") }); err == nil {
		t.Fatal("expected persistence failure")
	}
	if rt.Snapshot() != old {
		t.Fatal("published failed update")
	}
	pick(pool, "slow")
	before := pool.Snapshot()
	if err := rt.Update(next); err != nil {
		t.Fatal(err)
	}
	current := rt.Snapshot().vendors["openai"].pool
	pick(current, "key")
	if got := current.Snapshot(); got[0].Inflight != before[0].Inflight || got[0].RuntimeStats != before[0].RuntimeStats {
		t.Fatal("tuning reset live state")
	}
	for _, i := range held {
		pool.Release(i)
	}
	// Old generation completions and health still target the shared key state.
	i, _, version, ok := pool.AcquireVersioned(nil, []int{0})
	if !ok {
		t.Fatal("acquire")
	}
	pool.CompleteAttempt(i, version, balancer.AttemptResult{Action: balancer.AttemptCooldown, StatusCode: http.StatusTooManyRequests, Cooldown: time.Hour, Reason: "test"})
	if s := current.Snapshot()[0]; s.RateLimitCount != 1 || s.CooldownUntil.IsZero() {
		t.Fatal("lost old generation health")
	}
	if err := rt.RefreshKeys(); err != nil {
		t.Fatal(err)
	}
	if rt.cfg.Load().Vendors[0].Performance.Resolve("adaptive").InflightWeight != 0 {
		t.Fatal("refresh lost tuning")
	}
}
