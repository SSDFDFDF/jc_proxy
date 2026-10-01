package balancer

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"jc_proxy/internal/keystore"
)

func sharedTestPool(t *testing.T, version int64) *Pool {
	t.Helper()
	p, err := NewPoolWithConfigs("least_used", []KeyConfig{{Key: "a", Version: version}, {Key: "b", Version: version}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOldCompletionsUpdateNewPoolHealth(t *testing.T) {
	old := sharedTestPool(t, 1)
	idx, _, version, ok := old.AcquireVersioned(nil, nil)
	if !ok {
		t.Fatal("acquire")
	}
	next := sharedTestPool(t, 1)
	next.ShareRuntimeStateFrom(old)
	if next.Snapshot()[idx].Inflight != 1 {
		t.Fatal("reload lost active request")
	}
	selected, key, _ := next.Acquire()
	if key != "b" {
		t.Fatal("least_used ignored old generation load")
	}
	next.Release(selected)
	old.Cooldown(idx, 429, "late throttle", time.Minute, version)
	s := next.Snapshot()[idx]
	if s.Inflight != 0 || s.CooldownLevel != 1 || s.Failures != 1 || s.LastError != "late throttle" {
		t.Fatalf("late outcome not shared: %+v", s)
	}
	if !s.CooldownUntil.After(time.Now()) {
		t.Fatal("cooldown lost")
	}
	third := sharedTestPool(t, 1)
	third.ShareRuntimeStateFrom(next)
	if third.keys[idx] != old.keys[idx] || third.mu != old.mu {
		t.Fatal("state was copied instead of shared")
	}
	if next.Disable(idx, 401, "bad key", "auto", version) != true {
		t.Fatal("disable not applied")
	}
	if third.Snapshot()[idx].Status != keystore.KeyStatusDisabledAuto {
		t.Fatal("late disable not shared")
	}
}

func TestStaleAttemptCannotUndoManualEnable(t *testing.T) {
	for _, action := range []string{"cooldown", "disable", "success"} {
		t.Run(action, func(t *testing.T) {
			old := sharedTestPool(t, 1)
			idx, _, version, _ := old.AcquireVersioned(nil, nil)
			next := sharedTestPool(t, 2) // committed manual enable/recover
			next.ShareRuntimeStateFrom(old)
			switch action {
			case "cooldown":
				old.Cooldown(idx, 429, "stale", time.Hour, version)
			case "disable":
				if old.Disable(idx, 401, "stale", "auto", version) {
					t.Fatal("stale disable requested persistence")
				}
			case "success":
				next.Cooldown(idx, 429, "current", time.Minute, 2)
				old.ReleaseSuccess(idx, version)
			}
			old.RecordSample(idx, time.Millisecond, -1, false, version)
			s := next.Snapshot()[idx]
			if s.Status != keystore.KeyStatusActive || s.Inflight != 0 || s.Version != 2 || s.RecentRequests != 0 {
				t.Fatalf("stale outcome altered state: %+v", s)
			}
			if action == "success" {
				if s.CooldownLevel != 1 || s.LastError != "current" {
					t.Fatal("stale success cleared current health")
				}
			} else if !s.CooldownUntil.IsZero() || s.LastError != "" {
				t.Fatal("old request overrode enable")
			}
			if s.TotalRequests < 1 {
				t.Fatal("stale request lost lifetime counter")
			}
		})
	}
}

func TestStalePreparedSnapshotCannotResurrectAutoDisabledKey(t *testing.T) {
	old := sharedTestPool(t, 1)
	next := sharedTestPool(t, 1) // snapshot before the old response arrives
	idx, _, version, _ := old.AcquireVersioned(nil, nil)
	old.Disable(idx, http.StatusUnauthorized, "bad key", "auto", version)
	next.ShareRuntimeStateFrom(old)
	if next.Snapshot()[idx].Status != keystore.KeyStatusDisabledAuto {
		t.Fatal("stale active baseline resurrected key")
	}
}

func TestRemovedKeyIsRetiredAndReaddedKeyGetsFreshHealth(t *testing.T) {
	old := sharedTestPool(t, 1)
	idx, _, version, _ := old.AcquireVersioned(nil, nil)
	next, _ := NewPool("round_robin", []string{"b"})
	next.ShareRuntimeStateFrom(old)
	if _, _, ok := old.AcquireExceptAllowed(nil, []int{idx}); ok {
		t.Fatal("old plan reused deleted key")
	}
	readded := sharedTestPool(t, 1)
	readded.ShareRuntimeStateFrom(next)
	old.Cooldown(idx, 429, "deleted", time.Hour, version)
	if !readded.Snapshot()[0].CooldownUntil.IsZero() {
		t.Fatal("deleted incarnation poisoned replacement")
	}
}

func TestConcurrentGenerationsReleaseWithoutLeakingInflight(t *testing.T) {
	old := sharedTestPool(t, 1)
	next := sharedTestPool(t, 1)
	next.ShareRuntimeStateFrom(old)
	var wg sync.WaitGroup
	for _, p := range []*Pool{old, next} {
		wg.Add(1)
		go func(p *Pool) {
			defer wg.Done()
			for range 1000 {
				idx, _, v, _ := p.AcquireVersioned(nil, nil)
				p.ReleaseSuccess(idx, v)
			}
		}(p)
	}
	for range 100 {
		_ = next.Snapshot()
	}
	wg.Wait()
	var total int
	for _, s := range next.Snapshot() {
		if s.Inflight != 0 {
			t.Fatal("inflight leaked")
		}
		total += s.TotalRequests
	}
	if total != 2000 {
		t.Fatalf("requests=%d", total)
	}
}
