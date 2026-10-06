package balancer

import (
	"math"
	"sync"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

func tuningPtr[T any](v T) *T { return &v }

func tunedPool(t *testing.T, strategy string, tuning *config.PerformanceConfig) (*Pool, *time.Time) {
	t.Helper()
	p, err := NewPoolWithConfigs(strategy, []KeyConfig{{Key: "a"}, {Key: "b"}}, tuning)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.nowf = func() time.Time { return now }
	p.ActivatePerformance()
	return p, &now
}

func TestConfiguredTTLDecayAndWindowReset(t *testing.T) {
	for _, decay := range []time.Duration{0, 20 * time.Second} {
		p, now := tunedPool(t, "adaptive", &config.PerformanceConfig{SampleTTL: tuningPtr(10 * time.Second), DecayDuration: &decay, ExplorationInterval: tuningPtr(0)})
		p.RecordSample(0, time.Millisecond, time.Hour, true)
		p.RecordSample(0, time.Millisecond, time.Hour, true)
		// A fresh 100ms candidate loses initially, but wins when a's old fast
		// score decays towards its 4000 neutral cost.
		start := *now
		for _, elapsed := range []time.Duration{9 * time.Second, 10 * time.Second, 11 * time.Second, 30 * time.Second} {
			*now = start.Add(elapsed)
			for range 2 {
				p.RecordSample(1, 100*time.Millisecond, time.Second, true)
			}
			i, _, _ := p.Acquire()
			p.Release(i)
			want := 0
			if (decay == 0 && elapsed >= 10*time.Second) || elapsed >= 11*time.Second {
				want = 1
			}
			if i != want {
				t.Fatalf("decay=%v elapsed=%v pick=%d want=%d", decay, elapsed, i, want)
			}
		}
		if p.Snapshot()[0].RecentRequests != 2 {
			t.Fatal("selection cleared display statistics")
		}
		p.RecordSample(0, 2*time.Millisecond, time.Hour, true)
		if s := p.Snapshot()[0]; s.RecentRequests != 1 || s.AvgHeaderMS != 2 {
			t.Fatalf("TTL did not reset window on completion: %+v", s)
		}
	}
}

func TestConfiguredScoringFactors(t *testing.T) {
	h := NewRuntimeStatsHandle(keystore.RuntimeStats{})
	h.RecordSample(100*time.Millisecond, time.Hour, true)
	h.RecordSample(-1, -1, false)
	for _, strategy := range []string{"lowest_latency", "highest_success", "adaptive"} {
		for _, power := range []float64{0, 1, 2, 2.5, 4} {
			s := (&config.PerformanceConfig{SuccessPower: &power}).Resolve(strategy)
			base := 100.0
			if strategy == "highest_success" {
				base = 1
			}
			want := base / math.Pow(0.5, power)
			if got := h.configuredCost(strategy, s); math.Abs(got-want) > 1e-9 {
				t.Fatalf("%s power=%v cost=%v want=%v", strategy, power, got, want)
			}
		}
	}
	for range 5 {
		h.RecordSample(-1, -1, false)
	}
	if got := h.configuredCost("adaptive", (&config.PerformanceConfig{SuccessPower: tuningPtr(0.0)}).Resolve("adaptive")); got != 1e12 {
		t.Fatal("disabled all-failed protection")
	}

	p, _ := tunedPool(t, "adaptive", &config.PerformanceConfig{InflightWeight: tuningPtr(0.0), ExplorationInterval: tuningPtr(0)})
	for range 2 {
		p.RecordSample(0, 10*time.Millisecond, time.Hour, true)
		p.RecordSample(1, 20*time.Millisecond, time.Second, true)
	}
	p.keys[0].Inflight = 100
	idx, _, _ := p.Acquire()
	p.Release(idx)
	if idx != 0 {
		t.Fatal("inflight_weight=0 not honored")
	}
	p.performance.InflightWeight = 1
	idx, _, _ = p.Acquire()
	p.Release(idx)
	if idx != 1 {
		t.Fatal("inflight_weight=1 not honored")
	}

	cold := NewRuntimeStatsHandle(keystore.RuntimeStats{})
	if got := cold.configuredCost("adaptive", (&config.PerformanceConfig{LatencyPrior: tuningPtr(250 * time.Millisecond)}).Resolve("adaptive")); got != 1000 {
		t.Fatalf("cold prior cost=%v", got)
	}
}

func TestConfiguredExplorationAndBackoff(t *testing.T) {
	p, now := tunedPool(t, "adaptive", &config.PerformanceConfig{ExplorationInterval: tuningPtr(2), ExplorationBackoffBase: tuningPtr(2 * time.Second), ExplorationBackoffMax: tuningPtr(5 * time.Second)})
	for range 2 {
		p.RecordSample(0, time.Millisecond, time.Millisecond, true)
	}
	i, _, _ := p.Acquire()
	p.Release(i)
	if i != 0 {
		t.Fatal("first request should use known fast key")
	}
	i, _, _ = p.Acquire()
	p.Release(i)
	if i != 1 {
		t.Fatal("second request should explore")
	}
	for _, want := range []time.Duration{2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second} {
		p.RecordSample(1, -1, -1, false)
		if got := p.keys[1].nextExplore - now.Sub(schedulerEpoch); got != want {
			t.Fatalf("backoff=%v want=%v", got, want)
		}
	}
	p.RecordSample(1, time.Millisecond, time.Millisecond, true)
	if p.keys[1].nextExplore != math.MinInt64 {
		t.Fatal("success did not clear exploration backoff")
	}
	p.performance.ExplorationInterval = 0
	for range 40 {
		p.RecordSample(0, time.Millisecond, time.Millisecond, true)
		p.RecordSample(1, time.Hour, time.Hour, true)
		i, _, _ = p.Acquire()
		p.Release(i)
		if i != 0 {
			t.Fatal("disabled exploration selected slow key")
		}
	}
}

func TestCustomPerformanceResetAndConcurrentActivation(t *testing.T) {
	p, _ := tunedPool(t, "adaptive", &config.PerformanceConfig{SampleTTL: tuningPtr(10 * time.Second), SuccessPower: tuningPtr(3.0), LatencyPrior: tuningPtr(250 * time.Millisecond)})
	p.RecordSample(0, time.Millisecond, time.Second, true)
	if err := ResetRuntimeStats([]*RuntimeStatsHandle{p.keys[0].stats}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := p.keys[0].stats.configuredCost("adaptive", p.performance); got != 2000 {
		t.Fatalf("custom reset prior=%v", got)
	}
	if p.keys[0].stats.sampleTTL != 10*time.Second {
		t.Fatal("reset erased TTL")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			i, _, v, ok := p.AcquireVersioned(nil, nil)
			if ok {
				p.CompleteAttempt(i, v, AttemptResult{Action: AttemptSuccess, HeaderElapsed: time.Millisecond, FullElapsed: time.Second})
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			p.ActivatePerformance()
			_ = p.Snapshot()
		}
	}()
	wg.Wait()
}

func TestInactivePerformanceSettingsDoNotChangeOrdinaryPools(t *testing.T) {
	p, _ := tunedPool(t, "round_robin", &config.PerformanceConfig{SampleTTL: tuningPtr(time.Second), SuccessPower: tuningPtr(4.0)})
	if p.performance.SampleTTL != 5*time.Minute || p.customPerformance {
		t.Fatal("ordinary strategy applied performance tuning")
	}
}

func BenchmarkConfiguredPerformance(b *testing.B) {
	for _, power := range []float64{2, 2.5} {
		name := "default"
		if power != 2 {
			name = "fractional"
		}
		b.Run(name, func(b *testing.B) {
			p, err := NewPoolWithConfigs("adaptive", benchPoolKeys(100), &config.PerformanceConfig{SuccessPower: &power})
			if err != nil {
				b.Fatal(err)
			}
			for i := range p.keys {
				p.RecordSample(i, time.Millisecond, time.Second, true)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				i, _, _ := p.Acquire()
				p.Release(i)
			}
		})
	}
}

func TestPerformanceActivationPreservesAgeAndOldCompletionsUseNewTTL(t *testing.T) {
	old, now := tunedPool(t, "adaptive", nil)
	old.RecordSample(0, time.Millisecond, time.Second, true)
	originalTick := old.keys[0].sampleTick
	idx, _, version, _ := old.AcquireVersioned(nil, nil)
	*now = now.Add(20 * time.Second)
	settings := &config.PerformanceConfig{SampleTTL: tuningPtr(10 * time.Second), ExplorationInterval: tuningPtr(0)}
	next, err := NewPoolWithConfigs("adaptive", []KeyConfig{{Key: "a", Stats: old.keys[0].stats}, {Key: "b", Stats: old.keys[1].stats}}, settings)
	if err != nil {
		t.Fatal(err)
	}
	next.nowf = old.nowf
	if old.keys[0].stats.sampleTTL != sampleFreshness {
		t.Fatal("preparation changed live TTL")
	}
	next.ShareRuntimeStateFrom(old)
	next.ActivatePerformance()
	if next.keys[0].sampleExpires != originalTick+10*time.Second || next.keys[0].sampleTick != originalTick {
		t.Fatal("activation refreshed sample age")
	}
	if next.keys[0].Inflight != 1 {
		t.Fatal("activation lost inflight")
	}
	old.CompleteAttempt(idx, version, AttemptResult{Action: AttemptSuccess, HeaderElapsed: time.Millisecond, FullElapsed: time.Second})
	if s := next.Snapshot()[0]; s.RecentRequests != 1 || s.TotalRequests != 1 || s.Inflight != 0 {
		t.Fatalf("old completion did not use committed TTL: %+v", s)
	}
	if old.keys[0].stats.sampleTTL != 10*time.Second {
		t.Fatal("old completion reverted TTL")
	}
}
