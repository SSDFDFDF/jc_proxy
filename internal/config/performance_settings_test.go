package config

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func performancePtr[T any](v T) *T { return &v }

func TestPerformanceDefaultsAndRoundTrip(t *testing.T) {
	var omitted *PerformanceConfig
	for _, strategy := range []string{"lowest_latency", "highest_success", "adaptive"} {
		s := omitted.Resolve(strategy)
		if s.SampleTTL != 5*time.Minute || s.DecayDuration != 5*time.Minute || s.ExplorationInterval != 10 || s.ExplorationBackoffBase != time.Minute || s.ExplorationBackoffMax != 5*time.Minute || s.LatencyPrior != time.Second || s.InflightWeight != 1 {
			t.Fatalf("defaults: %+v", s)
		}
		wantPower := 2.0
		if strategy == "lowest_latency" {
			wantPower = 1
		}
		if s.SuccessPower != wantPower {
			t.Fatalf("power=%v", s.SuccessPower)
		}
	}
	var vendor VendorConfig
	if err := yaml.Unmarshal([]byte(`provider: generic
upstream:
  base_url: https://example.invalid
load_balance: adaptive
performance:
  sample_ttl: 90s
  decay_duration: 0s
  exploration_interval: 0
  exploration_backoff_base: 10s
  exploration_backoff_max: 2m
  success_power: 0
  inflight_weight: 0
  latency_prior: 250ms
`), &vendor); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Vendors: VendorsFromMap(map[string]VendorConfig{"test": vendor})}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatal(err)
	}
	clone, err := cfg.Clone()
	if err != nil {
		t.Fatal(err)
	}
	want := PerformanceSettings{SampleTTL: 90 * time.Second, DecayDuration: 0, ExplorationInterval: 0, ExplorationBackoffBase: 10 * time.Second, ExplorationBackoffMax: 2 * time.Minute, SuccessPower: 0, InflightWeight: 0, LatencyPrior: 250 * time.Millisecond}
	for _, c := range []*Config{cfg, clone} {
		if got := c.Vendors[0].Performance.Resolve("adaptive"); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		data, err := json.Marshal(c.Vendors[0])
		if err != nil {
			t.Fatal(err)
		}
		var decoded VendorConfig
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if got := decoded.Performance.Resolve("adaptive"); got != want {
			t.Fatalf("JSON: %+v", got)
		}
	}
	// {} and null preserve strategy-dependent defaults, not last strategy's power.
	for _, raw := range []string{`{}`, `{"performance":{}}`, `{"performance":null}`, `{"performance":{"success_power":null}}`} {
		var v VendorConfig
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		if v.Performance.Resolve("lowest_latency").SuccessPower != 1 || v.Performance.Resolve("adaptive").SuccessPower != 2 {
			t.Fatal("defaults frozen across strategy changes")
		}
	}
}

func TestPerformanceRejectsInvalidSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config PerformanceConfig
	}{
		{"sample_ttl", PerformanceConfig{SampleTTL: performancePtr(time.Duration(0))}},
		{"sample_ttl", PerformanceConfig{SampleTTL: performancePtr(25 * time.Hour)}},
		{"decay_duration", PerformanceConfig{DecayDuration: performancePtr(-time.Second)}},
		{"decay_duration", PerformanceConfig{DecayDuration: performancePtr(25 * time.Hour)}},
		{"exploration_interval", PerformanceConfig{ExplorationInterval: performancePtr(-1)}},
		{"exploration_interval", PerformanceConfig{ExplorationInterval: performancePtr(10001)}},
		{"exploration_backoff_base", PerformanceConfig{ExplorationBackoffBase: performancePtr(time.Duration(0))}},
		{"exploration_backoff_max", PerformanceConfig{ExplorationBackoffMax: performancePtr(time.Second)}},
		{"success_power", PerformanceConfig{SuccessPower: performancePtr(-1.0)}},
		{"success_power", PerformanceConfig{SuccessPower: performancePtr(4.1)}},
		{"success_power", PerformanceConfig{SuccessPower: performancePtr(math.NaN())}},
		{"inflight_weight", PerformanceConfig{InflightWeight: performancePtr(math.Inf(1))}},
		{"inflight_weight", PerformanceConfig{InflightWeight: performancePtr(11.0)}},
		{"latency_prior", PerformanceConfig{LatencyPrior: performancePtr(time.Duration(0))}},
		{"latency_prior", PerformanceConfig{LatencyPrior: performancePtr(2 * time.Hour)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.config.Validate(); err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("err=%v", err)
			}
			cfg := &Config{Vendors: VendorsFromMap(map[string]VendorConfig{"test": {Upstream: UpstreamConfig{BaseURL: "https://example.invalid"}, Performance: &tc.config}})}
			if err := cfg.PrepareAndValidate(); err == nil {
				t.Fatal("invalid vendor tuning accepted")
			}
		})
	}
}
