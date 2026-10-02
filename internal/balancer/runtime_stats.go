package balancer

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"jc_proxy/internal/keystore"
)

type RuntimeStatsHandle struct {
	mu            sync.Mutex
	stats         keystore.RuntimeStats
	totalRequests atomic.Int64
	window        [recentWindowSize]requestSample
	nextSample    int
	sampleCount   int
	lastSample    time.Time
	latencyCost   atomic.Uint64
	successCost   atomic.Uint64
	adaptiveCost  atomic.Uint64
	generation    atomic.Uint64 // statistics reset, independent of admin/health version
}

func NewRuntimeStatsHandle(initial keystore.RuntimeStats) *RuntimeStatsHandle {
	handle := &RuntimeStatsHandle{}
	handle.MergeBaseline(initial)
	return handle
}

func (h *RuntimeStatsHandle) Snapshot() keystore.RuntimeStats {
	if h == nil {
		return keystore.RuntimeStats{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stats
}

// TotalRequestsFast returns an atomically-read count without touching the
// stats mutex. Used by load-aware pickers in the hot path so we don't
// take a second lock while the pool's main mutex is already held.
func (h *RuntimeStatsHandle) TotalRequestsFast() int64 {
	if h == nil {
		return 0
	}
	return h.totalRequests.Load()
}

func (h *RuntimeStatsHandle) MergeBaseline(baseline keystore.RuntimeStats) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	// Persisted averages seed cold-start selection, never fabricate raw samples
	// or overwrite a live window when a router reload reads a stale baseline.
	if h.sampleCount == 0 {
		h.stats.RecentStats = baseline.RecentStats
		h.updateCostsLocked()
	}
	if baseline.TotalRequests > h.stats.TotalRequests {
		h.stats.LastStatus = baseline.LastStatus
		h.stats.LastError = normalizeLastError(baseline.LastError)
	}
	if baseline.TotalRequests > h.stats.TotalRequests {
		h.stats.TotalRequests = baseline.TotalRequests
		h.totalRequests.Store(int64(baseline.TotalRequests))
	}
	if baseline.SuccessCount > h.stats.SuccessCount {
		h.stats.SuccessCount = baseline.SuccessCount
	}
	if baseline.UnauthorizedCount > h.stats.UnauthorizedCount {
		h.stats.UnauthorizedCount = baseline.UnauthorizedCount
	}
	if baseline.ForbiddenCount > h.stats.ForbiddenCount {
		h.stats.ForbiddenCount = baseline.ForbiddenCount
	}
	if baseline.RateLimitCount > h.stats.RateLimitCount {
		h.stats.RateLimitCount = baseline.RateLimitCount
	}
	if baseline.OtherErrorCount > h.stats.OtherErrorCount {
		h.stats.OtherErrorCount = baseline.OtherErrorCount
	}
}

// preserveLast is used for results from an obsolete admin version: count the
// attempt without erasing newer diagnostics or resurrecting cleared errors.
func (h *RuntimeStatsHandle) RecordSuccess(preserveLast ...bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stats.TotalRequests++
	h.stats.SuccessCount++
	if len(preserveLast) == 0 || !preserveLast[0] {
		h.stats.LastStatus = http.StatusOK
		h.stats.LastError = ""
	}
	h.totalRequests.Store(int64(h.stats.TotalRequests))
}

func (h *RuntimeStatsHandle) RecordError(statusCode int, reason string, preserveLast ...bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stats.TotalRequests++
	if len(preserveLast) == 0 || !preserveLast[0] {
		h.stats.LastStatus = statusCode
		h.stats.LastError = normalizeLastError(reason)
	}

	switch statusCode {
	case http.StatusUnauthorized:
		h.stats.UnauthorizedCount++
	case http.StatusForbidden:
		h.stats.ForbiddenCount++
	case http.StatusTooManyRequests:
		h.stats.RateLimitCount++
	default:
		if statusCode >= http.StatusBadRequest || statusCode == 0 {
			h.stats.OtherErrorCount++
		}
	}
	h.totalRequests.Store(int64(h.stats.TotalRequests))
}

// RecordInterrupted counts an attempted request without attributing a client
// cancellation/downstream write failure to the upstream. Keep health diagnostics
// and the performance window intact. The unclassified portion of TotalRequests
// is the interrupted count, so existing file/PG schemas remain compatible.
func (h *RuntimeStatsHandle) RecordInterrupted() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stats.TotalRequests++
	h.totalRequests.Store(int64(h.stats.TotalRequests))
}

func (h *RuntimeStatsHandle) ClearLastError() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stats.LastError = ""
}
