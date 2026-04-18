package gateway

import (
	"strings"
	"sync"

	"jc_proxy/internal/balancer"
	"jc_proxy/internal/keystore"
)

type runtimeStatsRegistry struct {
	mu      sync.Mutex
	vendors map[string]map[string]*balancer.RuntimeStatsHandle
}

func newRuntimeStatsRegistry() *runtimeStatsRegistry {
	return &runtimeStatsRegistry{
		vendors: make(map[string]map[string]*balancer.RuntimeStatsHandle),
	}
}

func (r *runtimeStatsRegistry) Handle(vendor, key string, baseline keystore.RuntimeStats) *balancer.RuntimeStatsHandle {
	if r == nil {
		return balancer.NewRuntimeStatsHandle(baseline)
	}

	vendor = strings.TrimSpace(vendor)
	key = strings.TrimSpace(key)
	if vendor == "" || key == "" {
		return balancer.NewRuntimeStatsHandle(baseline)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	perVendor := r.vendors[vendor]
	if perVendor == nil {
		perVendor = make(map[string]*balancer.RuntimeStatsHandle)
		r.vendors[vendor] = perVendor
	}
	if handle, ok := perVendor[key]; ok {
		handle.MergeBaseline(baseline)
		return handle
	}

	handle := balancer.NewRuntimeStatsHandle(baseline)
	perVendor[key] = handle
	return handle
}
