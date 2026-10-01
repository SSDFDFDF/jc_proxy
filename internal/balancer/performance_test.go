package balancer

import (
	"sync"
	"testing"
	"time"

	"jc_proxy/internal/keystore"
)

func TestRecentWindowEvictionAndMissingTimings(t *testing.T) {
	h := NewRuntimeStatsHandle(keystore.RuntimeStats{})
	for i := 1; i <= 7; i++ {
		h.RecordSample(time.Duration(i)*time.Millisecond, time.Duration(i*10)*time.Millisecond, true)
	}
	s := h.Snapshot().RecentStats
	if s.RecentRequests != 5 || s.HeaderSamples != 5 || s.ResponseSamples != 5 || s.AvgHeaderMS != 5 || s.AvgResponseMS != 50 || s.RecentSuccessCount != 5 {
		t.Fatalf("window = %+v", s)
	}
	h.RecordSample(-1, -1, false) // evicts sample 3, not a zero-latency response
	s = h.Snapshot().RecentStats
	if s.HeaderSamples != 4 || s.ResponseSamples != 4 || s.AvgHeaderMS != 5.5 || s.AvgResponseMS != 55 || s.RecentSuccessCount != 4 {
		t.Fatalf("failed attempt = %+v", s)
	}
	for range 5 {
		h.RecordSample(-1, -1, false)
	}
	s = h.Snapshot().RecentStats
	if s.HeaderSamples != 0 || s.ResponseSamples != 0 || s.AvgHeaderMS != 0 || s.RecentRequests != 5 {
		t.Fatalf("evicted timings = %+v", s)
	}
}

func TestRecentBaselineDoesNotFabricateSamplesOrOverwriteLiveWindow(t *testing.T) {
	baseline := keystore.RuntimeStats{RecentStats: keystore.RecentStats{RecentRequests: 5, RecentSuccessCount: 4, HeaderSamples: 5, AvgHeaderMS: 90}}
	h := NewRuntimeStatsHandle(baseline)
	if h.Snapshot().RecentStats != baseline.RecentStats {
		t.Fatal("cold-start summary lost")
	}
	h.RecordSample(time.Millisecond, 2*time.Millisecond, true)
	h.MergeBaseline(baseline)
	s := h.Snapshot().RecentStats
	if s.RecentRequests != 1 || s.AvgHeaderMS != 1 || s.AvgResponseMS != 2 {
		t.Fatalf("live sample overwritten/fabricated: %+v", s)
	}
}

func TestPerformanceStrategies(t *testing.T) {
	for _, strategy := range []string{"lowest_latency", "highest_success", "adaptive"} {
		t.Run(strategy, func(t *testing.T) {
			p, err := NewPool(strategy, []string{"good", "bad", "slow"})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			p.nowf = func() time.Time { return now }
			for range 5 {
				p.RecordSample(0, 10*time.Millisecond, 20*time.Millisecond, true)
				p.RecordSample(1, time.Nanosecond, time.Nanosecond, false) // fast errors must not win
				p.RecordSample(2, 100*time.Millisecond, 200*time.Millisecond, true)
			}
			idx, _, ok := p.Acquire()
			if !ok || idx != 0 {
				t.Fatalf("pick = %d, %v", idx, ok)
			}
			p.Release(idx)
			// Expired samples become exploration candidates after failure backoff.
			now = now.Add(6 * time.Minute)
			seen := map[int]bool{idx: true}
			for range 80 {
				now = now.Add(time.Millisecond)
				i, _, ok := p.Acquire()
				if !ok {
					t.Fatal("acquire failed")
				}
				seen[i] = true
				p.Release(i)
			}
			if len(seen) != 3 {
				t.Fatalf("starved recovery probe: %v", seen)
			}
			p.DisableKey("slow", "test", "test")
			for range 40 {
				i, _, ok := p.AcquireExceptAllowed(map[int]struct{}{0: {}}, []int{-1, 0, 1, 2, 99})
				if !ok || i != 1 {
					t.Fatalf("eligibility bypass: %d, %v", i, ok)
				}
				p.Release(i)
			}
			if _, _, ok := p.AcquireExceptAllowed(nil, []int{}); ok {
				t.Fatal("empty allowlist ignored")
			}
			p.Cooldown(1, 429, "test", time.Hour)
			if _, _, ok := p.AcquireExcept(map[int]struct{}{0: {}}); ok {
				t.Fatal("cooldown bypassed")
			}
		})
	}
}

func TestAdaptiveAccountsForFullResponseAndInflight(t *testing.T) {
	p, _ := NewPool("adaptive", []string{"long-stream", "short"})
	for range 5 {
		p.RecordSample(0, 10*time.Millisecond, 10*time.Second, true)
		p.RecordSample(1, 20*time.Millisecond, 100*time.Millisecond, true)
	}
	idx, _, _ := p.Acquire()
	if idx != 1 {
		t.Fatalf("adaptive ignored full response: %d", idx)
	}
	p.Release(idx)
	p.keys[1].Inflight = 1000
	idx, _, _ = p.Acquire()
	if idx != 0 {
		t.Fatalf("adaptive ignored inflight: %d", idx)
	}
	p.Release(idx)
}

func TestRecentConcurrentRecordAndPick(t *testing.T) {
	p, _ := NewPool("adaptive", []string{"a", "b"})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				i, _, _ := p.Acquire()
				p.RecordSample(i, time.Millisecond, 2*time.Millisecond, true)
				p.ReleaseSuccess(i)
				_ = p.Snapshot()
			}
		}()
	}
	wg.Wait()
	if s := p.Snapshot(); s[0].TotalRequests+s[1].TotalRequests != 1600 {
		t.Fatal("lost counters")
	}
}

func TestLeastRequestsKeepsIntegerPrecision(t *testing.T) {
	large := int64(1) << 53
	if int64(int(large)) != large {
		t.Skip("requires 64-bit int")
	}
	p, _ := NewPoolWithConfigs("least_requests", []KeyConfig{
		{Key: "more", RuntimeStats: keystore.RuntimeStats{TotalRequests: int(large + 1)}},
		{Key: "less", RuntimeStats: keystore.RuntimeStats{TotalRequests: int(large)}},
	})
	_, key, ok := p.Acquire()
	if !ok || key != "less" {
		t.Fatalf("lost integer precision: %q", key)
	}
}

func TestLoadTotalsMatchesSnapshot(t *testing.T) {
	p, _ := NewPoolWithConfigs("round_robin", []KeyConfig{
		{Key: "a", RuntimeStats: keystore.RuntimeStats{TotalRequests: 3}},
		{Key: "b", RuntimeStats: keystore.RuntimeStats{TotalRequests: 7}},
	})
	p.Acquire()
	for _, allowed := range [][]int{nil, {}, {1, -1, 99}, {0, 1}} {
		inflight, total := p.LoadTotals(allowed)
		var wantInflight, wantTotal int64
		snap := p.Snapshot()
		if allowed == nil {
			allowed = []int{0, 1}
		}
		for _, i := range allowed {
			if i >= 0 && i < len(snap) {
				wantInflight += int64(snap[i].Inflight)
				wantTotal += int64(snap[i].TotalRequests)
			}
		}
		if inflight != wantInflight || total != wantTotal {
			t.Fatalf("load=%d/%d, want %d/%d", inflight, total, wantInflight, wantTotal)
		}
	}
}

func BenchmarkRecentRecordSample(b *testing.B) {
	h := NewRuntimeStatsHandle(keystore.RuntimeStats{})
	b.ReportAllocs()
	for b.Loop() {
		h.RecordSample(time.Millisecond, 2*time.Millisecond, true)
	}
}

func BenchmarkPool_Performance(b *testing.B) {
	for _, strategy := range []string{"lowest_latency", "highest_success", "adaptive"} {
		b.Run(strategy, func(b *testing.B) { benchAcquireRelease(b, strategy, 100) })
	}
}
