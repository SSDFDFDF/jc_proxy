package balancer

import (
	"net/http"
	"testing"
	"time"

	"jc_proxy/internal/keystore"
)

func TestInterruptedAttemptPreservesHealthAndPerformance(t *testing.T) {
	p, err := NewPoolWithConfigs("adaptive", []KeyConfig{{Key: "key", RuntimeStats: keystore.RuntimeStats{
		TotalRequests: 4, SuccessCount: 3, OtherErrorCount: 1,
		LastStatus: http.StatusBadGateway, LastError: "prior upstream error",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	p.RecordSample(0, time.Second, 2*time.Second, true)
	idx, _, ok := p.Acquire()
	if !ok {
		t.Fatal("acquire failed")
	}
	before := p.Snapshot()[0]
	cost := p.keys[0].stats.performanceCost("adaptive")
	p.ReleaseInterrupted(idx)
	after := p.Snapshot()[0]
	if after.TotalRequests != 5 || after.InterruptedCount() != 1 || after.Inflight != 0 || after.SuccessCount != 3 || after.OtherErrorCount != 1 {
		t.Fatalf("incorrect traffic accounting: %+v", after)
	}
	if after.LastStatus != before.LastStatus || after.LastError != before.LastError || after.Failures != before.Failures || after.CooldownUntil != before.CooldownUntil || after.RecentStats != before.RecentStats || p.keys[0].stats.performanceCost("adaptive") != cost {
		t.Fatalf("interruption changed upstream health/performance: before=%+v after=%+v", before, after)
	}
	if got := p.Stats()[0]["interrupted_count"]; got != 1 {
		t.Fatalf("API interrupted count = %v", got)
	}
	delta, monotonic := after.RuntimeStats.DeltaSince(before.RuntimeStats)
	if !monotonic {
		t.Fatal("interruption made counters non-monotonic")
	}
	stored := before.RuntimeStats
	stored.ApplyDelta(delta)
	if stored != after.RuntimeStats {
		t.Fatalf("delta lost interruption/diagnostics: %+v", stored)
	}
}

func TestFailurePenaltyIgnoresMeasuredOrMissingDurations(t *testing.T) {
	for _, elapsed := range []time.Duration{-1, time.Nanosecond, 120 * time.Second, 1500 * time.Second} {
		h := NewRuntimeStatsHandle(keystore.RuntimeStats{})
		h.RecordSample(elapsed, elapsed, false)
		s := h.Snapshot().RecentStats
		if s.RecentRequests != 1 || s.RecentSuccessCount != 0 || s.HeaderSamples != 1 || s.ResponseSamples != 1 || s.AvgHeaderMS != 999000 || s.AvgResponseMS != 999000 {
			t.Fatalf("elapsed=%v summary=%+v", elapsed, s)
		}
	}
}
