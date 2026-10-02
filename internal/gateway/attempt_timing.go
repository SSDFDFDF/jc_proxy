package gateway

import "time"

// finish is the single accounting boundary for an upstream attempt. Decisions
// already made for retry/masking survive later client cancellation or body-close
// errors. Successful timings are taken at EOF, before the final downstream write.
// No response-body wrapper, extra timer or per-attempt allocation is needed.
func (a *upstreamAttempt) finish(v *vendorGateway, decision keyDecision, full time.Duration) {
	if a.finished {
		return
	}
	a.finished = true
	if !v.usesManagedUpstreamKeys() {
		return
	}
	switch decision.action {
	case keyActionSuccess:
		v.pool.RecordSample(a.idx, a.headerElapsed, full, true, a.selectedVersion)
	case keyActionInterrupted:
		// Count traffic without changing the upstream's performance/health.
	default:
		// The balancer substitutes 999s/999s for every failed sample.
		v.pool.RecordSample(a.idx, -1, -1, false, a.selectedVersion)
	}
	v.applyDecision(a.idx, a.selectedKey, a.selectedVersion, decision)
}
