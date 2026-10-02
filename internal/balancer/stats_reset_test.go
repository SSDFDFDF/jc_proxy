package balancer

import (
	"errors"
	"math"
	"net/http"
	"reflect"
	"testing"
	"time"

	"jc_proxy/internal/keystore"
)

func TestResetRuntimeStatsPreservesHealthAndInflight(t *testing.T) {
	h := NewRuntimeStatsHandle(keystore.RuntimeStats{TotalRequests: 100, SuccessCount: 100})
	p, err := NewPoolWithConfigs("adaptive", []KeyConfig{{Key: "key", Stats: h, Version: 7}})
	if err != nil {
		t.Fatal(err)
	}
	idx, _, version, ok := p.AcquireVersioned(nil, nil)
	if !ok {
		t.Fatal("no key")
	}
	p.RecordSample(idx, time.Millisecond, time.Millisecond, false, version)
	p.Cooldown(idx, http.StatusTooManyRequests, "keep cooldown", time.Hour, version)
	// Simulate another request already in flight while a previous one cools it.
	p.keys[idx].Inflight = 1
	before := p.Snapshot()[idx]
	if err := ResetRuntimeStats([]*RuntimeStatsHandle{h, nil, h}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	after := p.Snapshot()[idx]
	if after.RuntimeStats != (keystore.RuntimeStats{}) || h.TotalRequestsFast() != 0 {
		t.Fatal("statistics were not reset")
	}
	if after.Inflight != before.Inflight || after.CooldownUntil != before.CooldownUntil ||
		after.CooldownLevel != before.CooldownLevel || after.Failures != before.Failures || after.Version != before.Version {
		t.Fatal("reset changed health or inflight")
	}
	if h.performanceCost("adaptive") != 4000 || h.performanceCost("highest_success") != 4 || h.performanceCost("lowest_latency") != 2000 {
		t.Fatal("reset did not restore neutral costs")
	}
	if h.sampleCount != 0 || h.nextSample != 0 || !h.lastSample.IsZero() || h.window != ([recentWindowSize]requestSample{}) {
		t.Fatal("old sample buffer survived reset")
	}

	// A completion after reset counts in the new period, not the old one.
	p.RecordSample(idx, 10*time.Millisecond, 20*time.Millisecond, true, version)
	p.ReleaseSuccess(idx, version)
	after = p.Snapshot()[idx]
	if after.TotalRequests != 1 || after.SuccessCount != 1 || after.Inflight != 0 || after.RecentRequests != 1 || after.AvgHeaderMS != 10 {
		t.Fatalf("unexpected post-reset completion: %#v", after)
	}
	if p.keys[idx].liveSamples != 1 || p.keys[idx].failedSamples != 0 || p.keys[idx].nextExplore != math.MinInt64 {
		t.Fatal("old exploration history survived the reset")
	}
}

func TestResetRuntimeStatsFailurePreservesEverything(t *testing.T) {
	h := NewRuntimeStatsHandle(keystore.RuntimeStats{TotalRequests: 20, SuccessCount: 20})
	h.RecordSample(time.Second, 2*time.Second, true)
	before, window, cost := h.Snapshot(), h.window, h.adaptiveCost.Load()
	failure := errors.New("disk failure")
	if err := ResetRuntimeStats([]*RuntimeStatsHandle{h}, func() error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, h.Snapshot()) || h.window != window || h.adaptiveCost.Load() != cost || h.TotalRequestsFast() != 20 || h.generation.Load() != 0 {
		t.Fatal("failed reset changed live stats")
	}
}

func TestResetRuntimeStatsBlocksCompletionsDuringPersistence(t *testing.T) {
	h := NewRuntimeStatsHandle(keystore.RuntimeStats{TotalRequests: 10, SuccessCount: 10})
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ResetRuntimeStats([]*RuntimeStatsHandle{h}, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	if h.mu.TryLock() {
		h.mu.Unlock()
		close(release)
		t.Fatal("stats mutex is not held during persistence")
	}
	completed := make(chan struct{})
	go func() { h.RecordSuccess(); close(completed) }()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-completed
	if h.Snapshot().TotalRequests != 1 {
		t.Fatal("new completion was lost or old stats restored")
	}
}

func TestResetRuntimeStatsClearsIdleExplorationHistory(t *testing.T) {
	p, err := NewPool("adaptive", []string{"key"})
	if err != nil {
		t.Fatal(err)
	}
	idx, _, _ := p.Acquire()
	p.RecordSample(idx, time.Millisecond, time.Millisecond, false)
	p.Release(idx)
	if p.keys[idx].failedSamples == 0 {
		t.Fatal("missing initial failure")
	}
	if err := ResetRuntimeStats([]*RuntimeStatsHandle{p.keys[idx].stats}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	idx, _, _ = p.Acquire()
	defer p.Release(idx)
	state := p.keys[idx]
	if state.failedSamples != 0 || state.liveSamples != 0 || state.nextExplore != math.MinInt64 {
		t.Fatal("picker reused old exploration history")
	}
}
