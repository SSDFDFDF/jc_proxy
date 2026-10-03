package balancer

import (
	"sync"
	"testing"
	"time"

	"jc_proxy/internal/keystore"
)

func TestCompleteAttemptStaysAtomicAcrossReset(t *testing.T) {
	p, _ := NewPool("adaptive", []string{"key"})
	h := p.keys[0].stats
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			idx, _, version, ok := p.AcquireVersioned(nil, nil)
			if !ok {
				t.Error("acquire failed")
				return
			}
			p.CompleteAttempt(idx, version, AttemptResult{Action: AttemptSuccess, HeaderElapsed: time.Millisecond, FullElapsed: 2 * time.Millisecond})
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			if err := ResetRuntimeStats([]*RuntimeStatsHandle{h}, func() error { return nil }); err != nil {
				t.Error(err)
			}
		}
	}()
	for range 5000 {
		s := h.Snapshot()
		if s.TotalRequests != s.SuccessCount || s.RecentRequests != min(s.TotalRequests, recentWindowSize) {
			t.Errorf("half-settled outcome: %+v", s)
			break
		}
	}
	wg.Wait()
	if p.Snapshot()[0].Inflight != 0 {
		t.Fatal("inflight leaked")
	}
}

func TestCompleteAttemptPreservesVersionAndNeutralOutcomes(t *testing.T) {
	for _, action := range []AttemptAction{AttemptSuccess, AttemptObserve, AttemptCooldown, AttemptDisable, AttemptInterrupted} {
		t.Run(string(action), func(t *testing.T) {
			p, _ := NewPool("adaptive", []string{"key"})
			idx, _, version, _ := p.AcquireVersioned(nil, nil)
			p.EnableKey("key") // Pool recovery does not change its version; model admin publication explicitly.
			p.mu.Lock()
			p.keys[0].Version++
			p.mu.Unlock()
			if p.CompleteAttempt(idx, version, AttemptResult{Action: action, StatusCode: 502, Reason: "obsolete", Cooldown: time.Hour}) {
				t.Fatal("stale attempt authorized persistence")
			}
			s := p.Snapshot()[0]
			if s.TotalRequests != 1 || s.RecentRequests != 0 || s.Inflight != 0 || s.LastError != "" || s.Status != keystore.KeyStatusActive || !s.CooldownUntil.IsZero() {
				t.Fatalf("stale outcome changed health: %+v", s)
			}
			if action == AttemptInterrupted && s.InterruptedCount() != 1 {
				t.Fatal("neutral count lost")
			}
		})
	}
}

func TestCompleteAttemptFailureSampleAndCount(t *testing.T) {
	for _, action := range []AttemptAction{AttemptObserve, AttemptCooldown, AttemptDisable} {
		p, _ := NewPool("adaptive", []string{"key"})
		idx, _, version, _ := p.AcquireVersioned(nil, nil)
		disabled := p.CompleteAttempt(idx, version, AttemptResult{Action: action, StatusCode: 429, Reason: "rate limit", Cooldown: time.Second})
		s := p.Snapshot()[0]
		if disabled != (action == AttemptDisable) || s.TotalRequests != 1 || s.RateLimitCount != 1 || s.LastStatus != 429 || s.RecentRequests != 1 || s.AvgHeaderMS != 999000 || s.AvgResponseMS != 999000 {
			t.Fatalf("action=%s stats=%+v", action, s)
		}
	}
}
