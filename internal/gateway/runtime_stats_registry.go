package gateway

import (
	"strings"
	"sync"

	"jc_proxy/internal/balancer"
	"jc_proxy/internal/keystore"
)

// runtimeStatsRegistry keeps one statistics handle per (vendor id, key) pair so
// counters survive router rebuilds. Keying on the immutable vendor id means a
// rename does not orphan a vendor's accumulated statistics.
type runtimeStatsRegistry struct {
	mu      sync.Mutex
	vendors map[string]map[string]*balancer.RuntimeStatsHandle
}

func newRuntimeStatsRegistry() *runtimeStatsRegistry {
	return &runtimeStatsRegistry{
		vendors: make(map[string]map[string]*balancer.RuntimeStatsHandle),
	}
}

func (r *runtimeStatsRegistry) Handle(vendorID, key string, baseline keystore.RuntimeStats) *balancer.RuntimeStatsHandle {
	if r == nil {
		return balancer.NewRuntimeStatsHandle(baseline)
	}

	vendorID = strings.TrimSpace(vendorID)
	key = strings.TrimSpace(key)
	if vendorID == "" || key == "" {
		return balancer.NewRuntimeStatsHandle(baseline)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	perVendor := r.vendors[vendorID]
	if perVendor == nil {
		perVendor = make(map[string]*balancer.RuntimeStatsHandle)
		r.vendors[vendorID] = perVendor
	}
	if handle, ok := perVendor[key]; ok {
		handle.MergeBaseline(baseline)
		return handle
	}

	handle := balancer.NewRuntimeStatsHandle(baseline)
	perVendor[key] = handle
	return handle
}
