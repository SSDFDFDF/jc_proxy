package balancer

import (
	"fmt"
	"math"
	"testing"
	"time"

	"jc_proxy/internal/keystore"
)

func explorationPool(t *testing.T, strategy string) (*Pool, *time.Time) {
	t.Helper()
	p, err := NewPool(strategy, []string{"healthy", "cold-a", "cold-b"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.nowf = func() time.Time { return now }
	for range 5 {
		p.RecordSample(0, time.Millisecond, 2*time.Millisecond, true)
	}
	return p, &now
}

func TestMixedFastFailuresNeverImprovePerformanceCost(t *testing.T) {
	for _, strategy := range []string{"lowest_latency", "highest_success", "adaptive"} {
		t.Run(strategy, func(t *testing.T) {
			p, _ := NewPool(strategy, []string{"healthy", "flaky"})
			for i := 0; i < 5; i++ {
				p.RecordSample(0, time.Second, time.Second, true)
				if i == 0 {
					p.RecordSample(1, time.Second, time.Second, true)
				} else {
					p.RecordSample(1, time.Millisecond, time.Millisecond, false)
				}
			}
			i, _, _ := p.Acquire()
			if i != 0 {
				t.Fatal("fast failures won selection")
			}
			if got := p.Snapshot()[1].AvgHeaderMS; got != (1000+4*999000)/5.0 {
				t.Fatalf("display must include failure penalties: %v", got)
			}
		})
	}
}

func TestExplorationColdKeysReceiveTwoSamples(t *testing.T) {
	for _, strategy := range []string{"lowest_latency", "highest_success", "adaptive"} {
		t.Run(strategy, func(t *testing.T) {
			p, now := explorationPool(t, strategy)
			counts := [3]int{}
			for range 40 {
				*now = now.Add(time.Millisecond)
				i, _, ok := p.Acquire()
				if !ok {
					t.Fatal("no key")
				}
				counts[i]++
				p.RecordSample(i, time.Second, time.Second, true)
				// Keep the primary key faster than the exploratory candidates.
				if i == 0 {
					for range 5 {
						p.RecordSample(0, time.Millisecond, 2*time.Millisecond, true)
					}
				}
				p.Release(i)
			}
			if counts[1] != 2 || counts[2] != 2 {
				t.Fatalf("cold sample counts = %v", counts)
			}
		})
	}
}

func TestExplorationCreditSurvivesAllowlistsAndReload(t *testing.T) {
	p, now := explorationPool(t, "adaptive")
	for range 9 {
		i, _, _ := p.AcquireExceptAllowed(nil, []int{0})
		p.Release(i)
	}
	next, _ := NewPool("adaptive", []string{"cold-b", "healthy", "cold-a"})
	next.nowf = p.nowf
	next.ShareRuntimeStateFrom(p)
	// The tenth request has no due candidates. Keep the credit for request 11.
	i, _, _ := next.AcquireExceptAllowed(nil, []int{1})
	next.Release(i)
	if next.exploration.credit != 10 {
		t.Fatal("lost exploration credit")
	}
	*now = now.Add(time.Second)
	i, key, _ := next.AcquireExceptAllowed(nil, []int{1, 2})
	next.Release(i)
	if key != "cold-a" {
		t.Fatalf("eligible cold key starved: %s", key)
	}
	if p.exploration.credit != 0 {
		t.Fatal("old and new generations do not share budget")
	}
}

func TestExplorationDoesNotDuplicateInflightOrIgnoreEligibility(t *testing.T) {
	p, _ := explorationPool(t, "adaptive")
	p.exploration.credit = 10
	i, key, _ := p.Acquire()
	if key != "cold-a" {
		t.Fatalf("first probe: %s", key)
	}
	p.exploration.credit = 10
	j, key, _ := p.Acquire()
	if key != "cold-b" {
		t.Fatalf("inflight key received duplicate probe: %s", key)
	}
	p.Release(j)
	p.DisableKey("cold-b", "test", "test")
	p.exploration.credit = 10
	j, key, _ = p.AcquireExceptAllowed(map[int]struct{}{i: {}}, []int{0, 1, 2})
	if key != "healthy" {
		t.Fatalf("eligibility bypass: %s", key)
	}
	p.Release(j)
	p.Release(i)
	if _, _, ok := p.AcquireExceptAllowed(nil, []int{}); ok {
		t.Fatal("empty allowlist bypassed")
	}
	if p.exploration.credit != 10 {
		t.Fatal("unavailable candidates consumed budget")
	}
}

func TestExplorationCancellationRotatesCandidates(t *testing.T) {
	p, now := explorationPool(t, "adaptive")
	p.exploration.credit = 10
	i, _, _ := p.Acquire()
	p.Release(i) // no sample: client cancellation
	*now = now.Add(time.Second)
	p.exploration.credit = 10
	_, key, _ := p.Acquire()
	if key != "cold-b" {
		t.Fatalf("cancelled key monopolized exploration: %s", key)
	}
}

func TestExplorationFailureBackoffAndRecovery(t *testing.T) {
	p, now := explorationPool(t, "adaptive")
	for _, delay := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		p.RecordSample(1, -1, -1, false)
		if got := p.keys[1].nextExplore - now.Sub(schedulerEpoch); got != delay {
			t.Fatalf("backoff %v, want %v", got, delay)
		}
		p.exploration.credit = 10
		i, _, _ := p.AcquireExceptAllowed(nil, []int{0, 1})
		p.Release(i)
		if i != 0 {
			t.Fatal("probe ignored backoff")
		}
		*now = now.Add(delay)
		p.RecordSample(0, time.Millisecond, time.Millisecond, true)
		p.RecordSample(0, time.Millisecond, time.Millisecond, true)
		p.exploration.credit = 10
		i, _, _ = p.AcquireExceptAllowed(nil, []int{0, 1})
		p.Release(i)
		if i != 1 {
			t.Fatal("failed key did not receive recovery opportunity")
		}
	}
	p.RecordSample(1, time.Millisecond, time.Millisecond, true)
	if p.keys[1].failedSamples != 0 || p.keys[1].nextExplore != math.MinInt64 {
		t.Fatal("success did not clear backoff")
	}
}

func TestExpiredSamplesDecayAndRebuildWindow(t *testing.T) {
	for _, strategy := range []string{"lowest_latency", "highest_success", "adaptive"} {
		t.Run(strategy, func(t *testing.T) {
			p, now := explorationPool(t, strategy)
			for range 5 {
				p.RecordSample(1, -1, -1, false)
			}
			*now = now.Add(10 * time.Minute)
			// A stale failure window becomes neutral; fresh slow service can lose to it.
			p.RecordSample(0, 10*time.Second, 10*time.Second, true)
			p.exploration.credit = 0
			if strategy == "highest_success" { // fresh one-success prior is better than neutral
				p.RecordSample(0, -1, -1, false)
				p.RecordSample(0, -1, -1, false)
			}
			i, _, _ := p.AcquireExceptAllowed(nil, []int{0, 1})
			p.Release(i)
			if i != 1 {
				t.Fatal("old failure penalty did not expire")
			}
			p.RecordSample(1, 20*time.Millisecond, 30*time.Millisecond, true)
			s := p.Snapshot()[1].RecentStats
			if s.RecentRequests != 1 || s.RecentSuccessCount != 1 || s.AvgHeaderMS != 20 {
				t.Fatalf("stale window not rebuilt: %+v", s)
			}
		})
	}
}

func TestPersistedMixedWindowDoesNotSeedFastErrorLatency(t *testing.T) {
	h := NewRuntimeStatsHandle(keystore.RuntimeStats{RecentStats: keystore.RecentStats{
		RecentRequests: 5, RecentSuccessCount: 1, HeaderSamples: 5, AvgHeaderMS: 200.8,
	}})
	if got := h.performanceCost("lowest_latency"); got < 3000 {
		t.Fatalf("mixed baseline seeded fast errors: %v", got)
	}
}

// Exercise fresh, expired and allowlisted pools at realistic larger sizes.
func BenchmarkExplorationScale(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		for _, mode := range []string{"fresh", "expired", "allowlist-10"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				keys := make([]string, size)
				for i := range keys {
					keys[i] = fmt.Sprintf("key-%d", i)
				}
				p, _ := NewPool("adaptive", keys)
				now := time.Now()
				p.nowf = func() time.Time { return now }
				for i := range keys {
					for range 2 {
						p.RecordSample(i, time.Millisecond, time.Millisecond, true)
					}
				}
				if mode == "expired" {
					now = now.Add(6 * time.Minute)
				}
				var allowed []int
				if mode == "allowlist-10" {
					allowed = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
				}
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						i, _, _ := p.AcquireExceptAllowed(nil, allowed)
						p.Release(i)
					}
				})
			})
		}
	}
}
