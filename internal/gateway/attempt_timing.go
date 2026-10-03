package gateway

import (
	"time"

	"jc_proxy/internal/balancer"
)

// finish is the exactly-once boundary. Pool.CompleteAttempt commits the sample,
// cumulative count and health together, so Reset cannot split this outcome.
// A known HTTP failure survives later client cancellation or cleanup errors.
func (a *upstreamAttempt) finish(v *vendorGateway, decision keyDecision, full time.Duration) {
	if a.finished {
		return
	}
	a.finished = true
	if !v.usesManagedUpstreamKeys() || v.pool == nil || a.idx < 0 {
		return
	}
	if v.pool.CompleteAttempt(a.idx, a.selectedVersion, balancer.AttemptResult{
		Action: decision.action, StatusCode: decision.statusCode, Reason: decision.reason,
		Cooldown: decision.cooldown, HeaderElapsed: a.headerElapsed, FullElapsed: full,
	}) {
		v.persistDisabledKeyAsync(a.selectedKey, a.selectedVersion, decision.reason)
	}
}
