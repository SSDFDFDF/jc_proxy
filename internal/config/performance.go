package config

import (
	"fmt"
	"math"
	"time"
)

// PerformanceConfig tunes managed-key scoring. Nil fields use defaults; zero
// means immediate post-TTL neutralization or disables exploration/a scoring
// factor where supported. Keep omitted factors unresolved so switching
// strategies keeps their defaults.
type PerformanceConfig struct {
	SampleTTL              *time.Duration `yaml:"sample_ttl,omitempty" json:"sample_ttl,omitempty"`
	DecayDuration          *time.Duration `yaml:"decay_duration,omitempty" json:"decay_duration,omitempty"`
	ExplorationInterval    *int           `yaml:"exploration_interval,omitempty" json:"exploration_interval,omitempty"`
	ExplorationBackoffBase *time.Duration `yaml:"exploration_backoff_base,omitempty" json:"exploration_backoff_base,omitempty"`
	ExplorationBackoffMax  *time.Duration `yaml:"exploration_backoff_max,omitempty" json:"exploration_backoff_max,omitempty"`
	SuccessPower           *float64       `yaml:"success_power,omitempty" json:"success_power,omitempty"`
	InflightWeight         *float64       `yaml:"inflight_weight,omitempty" json:"inflight_weight,omitempty"`
	LatencyPrior           *time.Duration `yaml:"latency_prior,omitempty" json:"latency_prior,omitempty"`
}

// PerformanceSettings is an immutable, resolved value used by the scheduler.
type PerformanceSettings struct {
	SampleTTL, DecayDuration                      time.Duration
	ExplorationInterval                           int
	ExplorationBackoffBase, ExplorationBackoffMax time.Duration
	SuccessPower, InflightWeight                  float64
	LatencyPrior                                  time.Duration
}

func IsPerformanceStrategy(strategy string) bool {
	return strategy == "lowest_latency" || strategy == "highest_success" || strategy == "adaptive"
}

func (c *PerformanceConfig) Resolve(strategy string) PerformanceSettings {
	s := PerformanceSettings{
		SampleTTL: 5 * time.Minute, DecayDuration: 5 * time.Minute,
		ExplorationInterval: 10, ExplorationBackoffBase: time.Minute, ExplorationBackoffMax: 5 * time.Minute,
		SuccessPower: 2, InflightWeight: 1, LatencyPrior: time.Second,
	}
	if strategy == "lowest_latency" {
		s.SuccessPower = 1
	}
	if c == nil {
		return s
	}
	if c.SampleTTL != nil {
		s.SampleTTL = *c.SampleTTL
	}
	if c.DecayDuration != nil {
		s.DecayDuration = *c.DecayDuration
	}
	if c.ExplorationInterval != nil {
		s.ExplorationInterval = *c.ExplorationInterval
	}
	if c.ExplorationBackoffBase != nil {
		s.ExplorationBackoffBase = *c.ExplorationBackoffBase
	}
	if c.ExplorationBackoffMax != nil {
		s.ExplorationBackoffMax = *c.ExplorationBackoffMax
	}
	if c.SuccessPower != nil {
		s.SuccessPower = *c.SuccessPower
	}
	if c.InflightWeight != nil {
		s.InflightWeight = *c.InflightWeight
	}
	if c.LatencyPrior != nil {
		s.LatencyPrior = *c.LatencyPrior
	}
	return s
}

func (c *PerformanceConfig) Validate() error {
	s := c.Resolve("adaptive")
	for _, field := range []struct {
		name            string
		value, min, max time.Duration
	}{
		{"sample_ttl", s.SampleTTL, time.Second, 24 * time.Hour},
		{"decay_duration", s.DecayDuration, 0, 24 * time.Hour},
		{"exploration_backoff_base", s.ExplorationBackoffBase, time.Second, 24 * time.Hour},
		{"exploration_backoff_max", s.ExplorationBackoffMax, time.Second, 24 * time.Hour},
		{"latency_prior", s.LatencyPrior, time.Millisecond, time.Hour},
	} {
		if field.value < field.min || field.value > field.max {
			return fmt.Errorf("performance.%s must be between %s and %s", field.name, field.min, field.max)
		}
	}
	if s.ExplorationInterval < 0 || s.ExplorationInterval > 10000 {
		return fmt.Errorf("performance.exploration_interval must be between 0 and 10000 (0 disables exploration)")
	}
	if s.ExplorationBackoffMax < s.ExplorationBackoffBase {
		return fmt.Errorf("performance.exploration_backoff_max must be >= exploration_backoff_base")
	}
	for _, field := range []struct {
		name       string
		value, max float64
	}{
		{"success_power", s.SuccessPower, 4}, {"inflight_weight", s.InflightWeight, 10},
	} {
		if math.IsNaN(field.value) || math.IsInf(field.value, 0) || field.value < 0 || field.value > field.max {
			return fmt.Errorf("performance.%s must be finite and between 0 and %g", field.name, field.max)
		}
	}
	return nil
}
