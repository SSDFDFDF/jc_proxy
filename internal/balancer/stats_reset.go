package balancer

import (
	"sync"
	"time"

	"jc_proxy/internal/keystore"
)

// Bulk resets are rare. Serializing them avoids lock-order problems between
// overlapping handle sets without adding any locks to the request hot path.
var runtimeStatsResetMu sync.Mutex

// ResetRuntimeStats holds all selected statistics handles across persistence.
// On a definite persistence failure, no in-memory statistics change. Completions
// waiting on these locks are recorded in the new period; requests are not aborted.
// persist must not call back into pools, handles or the runtime registry.
func ResetRuntimeStats(handles []*RuntimeStatsHandle, persist func() error) error {
	runtimeStatsResetMu.Lock()
	defer runtimeStatsResetMu.Unlock()

	seen := make(map[*RuntimeStatsHandle]bool, len(handles))
	locked := make([]*RuntimeStatsHandle, 0, len(handles))
	for _, h := range handles {
		if h == nil || seen[h] {
			continue
		}
		seen[h] = true
		h.mu.Lock()
		locked = append(locked, h)
	}
	defer func() {
		for i := len(locked) - 1; i >= 0; i-- {
			locked[i].mu.Unlock()
		}
	}()

	if err := persist(); err != nil {
		return err
	}
	for _, h := range locked {
		h.stats = keystore.RuntimeStats{}
		h.totalRequests.Store(0)
		h.window = [recentWindowSize]requestSample{}
		h.nextSample, h.sampleCount = 0, 0
		h.lastSample = time.Time{}
		h.updateCostsLocked()
		h.generation.Add(1)
	}
	return nil
}
