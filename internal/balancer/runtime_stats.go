package balancer

import (
	"net/http"
	"sync"
	"sync/atomic"

	"jc_proxy/internal/keystore"
)

type RuntimeStatsHandle struct {
	mu            sync.Mutex
	stats         keystore.RuntimeStats
	totalRequests atomic.Int64
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

func (h *RuntimeStatsHandle) RecordSuccess() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stats.TotalRequests++
	h.stats.SuccessCount++
	h.stats.LastStatus = http.StatusOK
	h.stats.LastError = ""
	h.totalRequests.Store(int64(h.stats.TotalRequests))
}

func (h *RuntimeStatsHandle) RecordError(statusCode int, reason string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stats.TotalRequests++
	h.stats.LastStatus = statusCode
	h.stats.LastError = normalizeLastError(reason)

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

func (h *RuntimeStatsHandle) ClearLastError() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stats.LastError = ""
}
