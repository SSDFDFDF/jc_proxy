package balancer

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/keystore"
)

func TestRoundRobinAcquire(t *testing.T) {
	p, err := NewPool("round_robin", []string{"k1", "k2", "k3"}, 3, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for i := 0; i < 4; i++ {
		idx, key, ok := p.Acquire()
		if !ok {
			t.Fatal("acquire failed")
		}
		got = append(got, key)
		p.ReleaseSuccess(idx)
	}

	want := []string{"k1", "k2", "k3", "k1"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("round robin mismatch at %d: got %s want %s", i, got[i], want[i])
		}
	}

	snap := p.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 key states, got %d", len(snap))
	}
	if snap[0].TotalRequests != 2 || snap[0].SuccessCount != 2 {
		t.Fatalf("unexpected counters for k1: total=%d success=%d", snap[0].TotalRequests, snap[0].SuccessCount)
	}
	if snap[1].TotalRequests != 1 || snap[1].SuccessCount != 1 {
		t.Fatalf("unexpected counters for k2: total=%d success=%d", snap[1].TotalRequests, snap[1].SuccessCount)
	}
	if snap[2].TotalRequests != 1 || snap[2].SuccessCount != 1 {
		t.Fatalf("unexpected counters for k3: total=%d success=%d", snap[2].TotalRequests, snap[2].SuccessCount)
	}
}

func TestLeastUsedPrefersLowerInflightThenLowerUsage(t *testing.T) {
	p, err := NewPoolWithConfigs("least_used", []KeyConfig{
		{
			Key:          "k1",
			Status:       keystore.KeyStatusActive,
			RuntimeStats: keystore.RuntimeStats{TotalRequests: 10},
		},
		{
			Key:          "k2",
			Status:       keystore.KeyStatusActive,
			RuntimeStats: keystore.RuntimeStats{TotalRequests: 1},
		},
	}, 3, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	idx, key, ok := p.Acquire()
	if !ok {
		t.Fatal("first acquire failed")
	}
	if key != "k2" {
		t.Fatalf("expected lower-usage key first, got %s", key)
	}

	idx2, key2, ok := p.Acquire()
	if !ok {
		t.Fatal("second acquire failed")
	}
	if key2 != "k1" {
		t.Fatalf("expected lower-inflight key second, got %s", key2)
	}

	p.ReleaseSuccess(idx)
	p.ReleaseSuccess(idx2)
}

func TestLeastRequestsBalancesByUsageCount(t *testing.T) {
	p, err := NewPoolWithConfigs("least_requests", []KeyConfig{
		{
			Key:          "k1",
			Status:       keystore.KeyStatusActive,
			RuntimeStats: keystore.RuntimeStats{TotalRequests: 5},
		},
		{
			Key:          "k2",
			Status:       keystore.KeyStatusActive,
			RuntimeStats: keystore.RuntimeStats{TotalRequests: 5},
		},
		{
			Key:          "k3",
			Status:       keystore.KeyStatusActive,
			RuntimeStats: keystore.RuntimeStats{TotalRequests: 6},
		},
	}, 3, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for i := 0; i < 3; i++ {
		idx, key, ok := p.Acquire()
		if !ok {
			t.Fatalf("acquire %d failed", i)
		}
		got = append(got, key)
		p.ReleaseSuccess(idx)
	}

	want := []string{"k1", "k2", "k3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("least_requests mismatch at %d: got %s want %s", i, got[i], want[i])
		}
	}
}

func TestCooldownSkipsAcquire(t *testing.T) {
	p, err := NewPool("round_robin", []string{"k1"}, 2, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.nowf = func() time.Time { return now }

	idx, _, ok := p.Acquire()
	if !ok {
		t.Fatal("acquire failed")
	}
	p.Cooldown(idx, http.StatusTooManyRequests, "rate limited", 30*time.Second)

	if _, _, ok := p.Acquire(); ok {
		t.Fatal("expected key in cooldown")
	}

	now = now.Add(31 * time.Second)
	if _, _, ok := p.Acquire(); !ok {
		t.Fatal("expected key to recover after cooldown")
	}
}

func TestCooldownDurationExponentiallyScalesFromConfiguredBase(t *testing.T) {
	p, err := NewPool("round_robin", []string{"k1"}, 10, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.nowf = func() time.Time { return now }

	wantDurations := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}
	for i, want := range wantDurations {
		idx, _, ok := p.Acquire()
		if !ok {
			t.Fatalf("acquire %d failed", i)
		}
		p.Cooldown(idx, http.StatusTooManyRequests, formatStatusError(http.StatusTooManyRequests), 5*time.Second)

		ks := p.Snapshot()[0]
		if got := ks.CooldownUntil.Sub(now); got != want {
			t.Fatalf("cooldown %d duration = %v, want %v", i, got, want)
		}
		now = now.Add(want + time.Second)
	}
}

func TestTransientCooldownEscalatesAndResetsAfterSuccess(t *testing.T) {
	p, err := NewPool("round_robin", []string{"k1"}, 10, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.nowf = func() time.Time { return now }

	idx, _, ok := p.Acquire()
	if !ok {
		t.Fatal("first acquire failed")
	}
	p.Cooldown(idx, http.StatusInternalServerError, formatStatusError(http.StatusInternalServerError), 2*time.Second)
	if got := p.Snapshot()[0].CooldownUntil.Sub(now); got != 2*time.Second {
		t.Fatalf("first transient cooldown = %v, want %v", got, 2*time.Second)
	}

	now = now.Add(3 * time.Second)
	idx, _, ok = p.Acquire()
	if !ok {
		t.Fatal("second acquire failed")
	}
	p.Cooldown(idx, 0, "dial tcp timeout", 2*time.Second)
	ks := p.Snapshot()[0]
	if got := ks.CooldownUntil.Sub(now); got != 4*time.Second {
		t.Fatalf("second transient cooldown = %v, want %v", got, 4*time.Second)
	}
	if got := ks.CooldownLevel; got != 2 {
		t.Fatalf("cooldown level after repeated transient failures = %d, want 2", got)
	}

	now = now.Add(6 * time.Second)
	idx, _, ok = p.Acquire()
	if !ok {
		t.Fatal("success acquire failed")
	}
	p.ReleaseSuccess(idx)
	ks = p.Snapshot()[0]
	if got := ks.CooldownLevel; got != 0 {
		t.Fatalf("cooldown level after success = %d, want 0", got)
	}

	idx, _, ok = p.Acquire()
	if !ok {
		t.Fatal("post-success acquire failed")
	}
	p.Cooldown(idx, http.StatusInternalServerError, formatStatusError(http.StatusInternalServerError), 2*time.Second)
	if got := p.Snapshot()[0].CooldownUntil.Sub(now); got != 2*time.Second {
		t.Fatalf("cooldown after reset = %v, want %v", got, 2*time.Second)
	}
}

func TestCooldownCategories(t *testing.T) {
	p, err := NewPool("round_robin", []string{"k1"}, 10, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.nowf = func() time.Time { return now }

	idx, _, ok := p.Acquire()
	if !ok {
		t.Fatal("acquire for 401 failed")
	}
	p.Cooldown(idx, http.StatusUnauthorized, formatStatusError(http.StatusUnauthorized), time.Second)
	now = now.Add(2 * time.Second)

	idx, _, ok = p.Acquire()
	if !ok {
		t.Fatal("acquire for 403 failed")
	}
	p.Cooldown(idx, http.StatusForbidden, formatStatusError(http.StatusForbidden), time.Second)
	now = now.Add(2 * time.Second)

	idx, _, ok = p.Acquire()
	if !ok {
		t.Fatal("acquire for 429 failed")
	}
	p.Cooldown(idx, http.StatusTooManyRequests, formatStatusError(http.StatusTooManyRequests), time.Second)

	snap := p.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one key state, got %d", len(snap))
	}
	ks := snap[0]
	if ks.UnauthorizedCount != 1 {
		t.Fatalf("unexpected unauthorized count: %d", ks.UnauthorizedCount)
	}
	if ks.ForbiddenCount != 1 {
		t.Fatalf("unexpected forbidden count: %d", ks.ForbiddenCount)
	}
	if ks.RateLimitCount != 1 {
		t.Fatalf("unexpected rate limit count: %d", ks.RateLimitCount)
	}
	if ks.Failures != 3 {
		t.Fatalf("unexpected failures count: %d", ks.Failures)
	}
	if ks.TotalRequests != 3 {
		t.Fatalf("unexpected total requests count: %d", ks.TotalRequests)
	}
	if ks.SuccessCount != 0 {
		t.Fatalf("unexpected success count: %d", ks.SuccessCount)
	}
	if !strings.Contains(ks.LastError, "429") {
		t.Fatalf("expected last error to mention 429, got %q", ks.LastError)
	}
}

func TestOtherErrorsAggregatedAndLastErrorPreserved(t *testing.T) {
	p, err := NewPool("round_robin", []string{"k1"}, 2, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p.nowf = func() time.Time { return now }

	idx, _, ok := p.Acquire()
	if !ok {
		t.Fatal("acquire for 500 failed")
	}
	p.Cooldown(idx, http.StatusInternalServerError, formatStatusError(http.StatusInternalServerError), time.Second)

	snap := p.Snapshot()
	ks := snap[0]
	if ks.OtherErrorCount != 1 {
		t.Fatalf("unexpected other error count after 500: %d", ks.OtherErrorCount)
	}
	if ks.TotalRequests != 1 {
		t.Fatalf("unexpected total requests after 500: %d", ks.TotalRequests)
	}
	if ks.SuccessCount != 0 {
		t.Fatalf("unexpected success count after 500: %d", ks.SuccessCount)
	}
	if ks.Failures != 1 {
		t.Fatalf("500 should increase cooldown failure count, got %d", ks.Failures)
	}
	if !strings.Contains(ks.LastError, "500") {
		t.Fatalf("expected last error to mention 500, got %q", ks.LastError)
	}

	now = now.Add(2 * time.Second)
	idx, _, ok = p.Acquire()
	if !ok {
		t.Fatal("acquire for request error failed")
	}
	p.Cooldown(idx, 0, "dial tcp timeout", time.Second)

	snap = p.Snapshot()
	ks = snap[0]
	if ks.OtherErrorCount != 2 {
		t.Fatalf("unexpected other error count after request error: %d", ks.OtherErrorCount)
	}
	if ks.TotalRequests != 2 {
		t.Fatalf("unexpected total requests after request error: %d", ks.TotalRequests)
	}
	if ks.LastError != "dial tcp timeout" {
		t.Fatalf("unexpected last error: %q", ks.LastError)
	}
}

func TestLastErrorIsTruncated(t *testing.T) {
	p, err := NewPool("round_robin", []string{"k1"}, 2, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	idx, _, ok := p.Acquire()
	if !ok {
		t.Fatal("acquire for long error failed")
	}
	longMessage := strings.Repeat("x", maxLastErrorLength+25)
	p.Observe(idx, 0, longMessage)

	ks := p.Snapshot()[0]
	if len([]rune(ks.LastError)) != maxLastErrorLength {
		t.Fatalf("unexpected last error length: %d", len([]rune(ks.LastError)))
	}
	if !strings.HasSuffix(ks.LastError, "...") {
		t.Fatalf("expected truncated last error to end with ellipsis, got %q", ks.LastError)
	}
}
