package config

import (
	"strings"
	"testing"
)

func TestManagedKeyPerformanceStrategies(t *testing.T) {
	for _, strategy := range []string{"lowest_latency", "highest_success", "adaptive"} {
		t.Run(strategy, func(t *testing.T) {
			cfg := &Config{Vendors: VendorsFromMap(map[string]VendorConfig{"child": {LoadBalance: strategy, Upstream: UpstreamConfig{BaseURL: "https://example.com"}}})}
			if err := cfg.PrepareAndValidate(); err != nil {
				t.Fatal(err)
			}
			cfg.Vendors = append(cfg.Vendors, VendorEntry{ID: "vid_agg", Name: "agg", VendorConfig: VendorConfig{Provider: "aggregate", LoadBalance: strategy}})
			if err := cfg.PrepareAndValidate(); err == nil || !strings.Contains(err.Error(), "managed-key strategy") {
				t.Fatalf("aggregate silently accepted key strategy: %v", err)
			}
		})
	}
}
