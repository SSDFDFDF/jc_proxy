package balancer

import (
	"time"

	"jc_proxy/internal/keystore"
)

type AttemptAction string

const (
	AttemptInterrupted AttemptAction = "interrupted"
	AttemptSuccess     AttemptAction = "success"
	AttemptObserve     AttemptAction = "observe"
	AttemptCooldown    AttemptAction = "cooldown"
	AttemptDisable     AttemptAction = "disable"
)

type AttemptResult struct {
	Action                     AttemptAction
	StatusCode                 int
	Reason                     string
	Cooldown                   time.Duration
	HeaderElapsed, FullElapsed time.Duration
}

// CompleteAttempt settles one acquired attempt. The caller owns exactly-once
// execution. Pool -> stats is the normal lock order; Reset never takes pool locks.
// Holding stats.mu across both sample and counters gives reset/snapshots one
// commit point, while holding pool.mu also makes version and health consistent.
// The return value authorizes persisting an auto-disable for this admin version.
func (p *Pool) CompleteAttempt(idx int, version int64, result AttemptResult) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return false
	}
	current := p.currentVersionLocked(idx, []int64{version})
	state := p.keys[idx]
	h := state.stats
	h.mu.Lock()
	defer h.mu.Unlock()

	if result.Action == AttemptInterrupted {
		h.stats.TotalRequests++
		h.totalRequests.Store(int64(h.stats.TotalRequests))
		return false
	}
	if result.Action != AttemptSuccess && result.Action != AttemptObserve && result.Action != AttemptCooldown && result.Action != AttemptDisable {
		return false
	}
	if current {
		now := p.nowf()
		generation := h.recordSampleLocked(result.HeaderElapsed, result.FullElapsed, result.Action == AttemptSuccess, now)
		state.noteSample(generation, now.Sub(schedulerEpoch), result.Action == AttemptSuccess)
	}
	if result.Action == AttemptSuccess {
		h.recordSuccessLocked(!current)
	} else {
		h.recordErrorLocked(result.StatusCode, result.Reason, !current)
	}
	if !current {
		return false
	}
	switch result.Action {
	case AttemptSuccess:
		state.Failures = 0
		state.CooldownLevel = 0
	case AttemptObserve:
		state.Failures = 0
	case AttemptCooldown:
		p.recordFailureLocked(idx)
		state.CooldownUntil = p.nowf().Add(scaledCooldownDuration(state.CooldownLevel, result.Cooldown))
	case AttemptDisable:
		p.disableLocked(idx, keystore.KeyStatusDisabledAuto, result.Reason, "system:auto")
		return true
	}
	return false
}
