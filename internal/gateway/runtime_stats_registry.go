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
		// Router preparation must not mutate the live handle before commit.
		return handle
	}

	handle := balancer.NewRuntimeStatsHandle(baseline)
	perVendor[key] = handle
	return handle
}

// Reset includes handles no longer present in the published router. Otherwise
// removing and re-adding a key could resurrect its pre-reset counters.
func (r *runtimeStatsRegistry) Reset(vendorID string, persist func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var handles []*balancer.RuntimeStatsHandle
	for id, keys := range r.vendors {
		if vendorID != "" && id != vendorID {
			continue
		}
		for _, handle := range keys {
			handles = append(handles, handle)
		}
	}
	return balancer.ResetRuntimeStats(handles, persist)
}
