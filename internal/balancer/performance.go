package balancer

import (
	"math"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

const (
	recentWindowSize    = 5
	failedSamplePenalty = 999 * time.Second
	sampleFreshness     = 5 * time.Minute
)

// Process-local monotonic offsets keep scheduling state compact and let one
// clock conversion serve the entire scan. Persisted statistics need no schema change.
var schedulerEpoch = time.Now()

type explorationState struct{ credit int }

type requestSample struct {
	header  time.Duration // successful timing, or the fixed failure penalty
	full    time.Duration // successful timing, or the fixed failure penalty
	success bool
}

// RecordSample records one finished upstream attempt, in completion order.
// Client cancellation/downstream write failures must be filtered by the caller.
// This fixed-size ring needs no allocation, timer, request log or body copy.
func (h *RuntimeStatsHandle) RecordSample(header, full time.Duration, success bool) {
	h.recordSampleAt(header, full, success, time.Now())
}

func (h *RuntimeStatsHandle) recordSampleAt(header, full time.Duration, success bool, now time.Time) uint64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.recordSampleLocked(header, full, success, now)
}

// recordSampleLocked also participates in atomic attempt settlement.
func (h *RuntimeStatsHandle) recordSampleLocked(header, full time.Duration, success bool, now time.Time) uint64 {
	// Failed attempts must never look fast or disappear from timing averages,
	// including network failures and bodies discarded before EOF.
	if !success {
		header, full = failedSamplePenalty, failedSamplePenalty
	}
	if !h.lastSample.IsZero() && now.Sub(h.lastSample) >= h.sampleTTL {
		h.sampleCount, h.nextSample = 0, 0
	}
	h.lastSample = now
	h.window[h.nextSample] = requestSample{header: header, full: full, success: success}
	h.nextSample = (h.nextSample + 1) % recentWindowSize
	if h.sampleCount < recentWindowSize {
		h.sampleCount++
	}
	summary := keystore.RecentStats{RecentRequests: h.sampleCount}
	for _, sample := range h.window[:h.sampleCount] {
		if sample.success {
			summary.RecentSuccessCount++
		}
		if sample.header >= 0 {
			summary.HeaderSamples++
			summary.AvgHeaderMS += float64(sample.header) / float64(time.Millisecond)
		}
		if sample.full >= 0 {
			summary.ResponseSamples++
			summary.AvgResponseMS += float64(sample.full) / float64(time.Millisecond)
		}
	}
	if summary.HeaderSamples > 0 {
		summary.AvgHeaderMS /= float64(summary.HeaderSamples)
	}
	if summary.ResponseSamples > 0 {
		summary.AvgResponseMS /= float64(summary.ResponseSamples)
	}
	h.stats.RecentStats = summary
	h.updateCostsLocked()
	return h.generation.Load()
}

// Default costs and successful timing inputs are precomputed on completion.
// Custom factors use two atomic inputs without taking per-key statistics locks.
func (h *RuntimeStatsHandle) updateCostsLocked() {
	s := h.stats.RecentStats
	if s.RecentRequests > 0 && s.RecentSuccessCount == 0 {
		// A fast failure is not a fast service. All-failed windows go to the
		// back of the queue regardless of tiny error-response timings; normal
		// age-based exploration still allows them to recover.
		penalty := math.Float64bits(1e12)
		h.successRate.Store(math.Float64bits(0))
		h.latencyCost.Store(penalty)
		h.successCost.Store(penalty)
		h.adaptiveCost.Store(penalty)
		return
	}
	p := float64(s.RecentSuccessCount+1) / float64(s.RecentRequests+2) // Laplace smoothing
	hasHeader := false
	header := 1000.0 // neutral 1s prior when timing is unknown
	// Display averages include fixed failure penalties. Scheduling continues to
	// use successful timings plus the success-rate penalty (not both penalties).
	if h.sampleCount > 0 {
		var hs int
		var ht float64
		for _, sample := range h.window[:h.sampleCount] {
			if !sample.success {
				continue
			}
			if sample.header >= 0 {
				hs++
				ht += float64(sample.header) / float64(time.Millisecond)
			}
		}
		if hs > 0 {
			header = ht / float64(hs)
			hasHeader = true
		}
	} else if s.RecentRequests == s.RecentSuccessCount {
		// Persisted summaries have no separate successful timings. Only an
		// all-success summary can safely seed latency costs after restart.
		if s.HeaderSamples > 0 {
			header = s.AvgHeaderMS
			hasHeader = true
		}
	}
	header = math.Max(1, math.Min(header, 3_600_000))
	h.successRate.Store(math.Float64bits(p))
	if hasHeader {
		h.successHeader.Store(math.Float64bits(header))
	} else {
		h.successHeader.Store(math.Float64bits(-1))
	}
	h.latencyCost.Store(math.Float64bits(header / p))
	h.successCost.Store(math.Float64bits(1 / (p * p)))
	// Full response time remains a diagnostic statistic only: output length,
	// long-lived streams and downstream backpressure are not service latency.
	// Adaptive favors reliability more strongly than lowest_latency.
	h.adaptiveCost.Store(math.Float64bits(header / (p * p)))
}

func (h *RuntimeStatsHandle) performanceCost(strategy string) float64 {
	switch strategy {
	case "lowest_latency":
		return math.Float64frombits(h.latencyCost.Load())
	case "highest_success":
		return math.Float64frombits(h.successCost.Load())
	default:
		return math.Float64frombits(h.adaptiveCost.Load())
	}
}

func performanceBaseCost(strategy string, header, probability, power float64) float64 {
	if probability == 0 {
		return 1e12 // preserve all-failed protection even with success_power=0
	}
	if strategy == "highest_success" {
		header = 1
	}
	// Fast paths retain default hot-path costs; custom fractional powers use Pow.
	switch power {
	case 0:
		return header
	case 1:
		return header / probability
	case 2:
		return header / (probability * probability)
	default:
		return header / math.Pow(probability, power)
	}
}

func (h *RuntimeStatsHandle) configuredCost(strategy string, settings config.PerformanceSettings) float64 {
	probability := math.Float64frombits(h.successRate.Load())
	header := math.Float64frombits(h.successHeader.Load())
	if header < 0 {
		header = float64(settings.LatencyPrior) / float64(time.Millisecond)
	}
	return performanceBaseCost(strategy, header, probability, settings.SuccessPower)
}

func explorationBackoff(settings config.PerformanceSettings, failures int) time.Duration {
	delay := settings.ExplorationBackoffBase
	for n := 1; n < failures && delay < settings.ExplorationBackoffMax; n++ {
		delay = min(delay*2, settings.ExplorationBackoffMax)
	}
	return delay
}

// ActivatePerformance is a commit-only operation. Preparing a router must not
// change a shared handle or an old pool's sampling policy before persistence.
// Old in-flight attempts share KeyState and settle with the newly committed TTL.
func (p *Pool) ActivatePerformance() {
	p.mu.Lock()
	defer p.mu.Unlock()
	tick := p.nowf().Sub(schedulerEpoch)
	for _, state := range p.keys {
		state.syncStatsGeneration(state.stats.generation.Load(), tick)
		state.performance = &p.performance
		state.sampleExpires = state.sampleTick + p.performance.SampleTTL
		if state.failedSamples > 0 {
			state.nextExplore = state.sampleTick + explorationBackoff(p.performance, state.failedSamples)
		}
		state.stats.mu.Lock()
		state.stats.sampleTTL = p.performance.SampleTTL
		state.stats.mu.Unlock()
	}
	// Keep earned exploration credit bounded when the interval changes.
	p.exploration.credit = min(p.exploration.credit, p.performance.ExplorationInterval)
}

func (p *Pool) RecordSample(idx int, header, full time.Duration, success bool, expectedVersion ...int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx >= 0 && idx < len(p.keys) && p.currentVersionLocked(idx, expectedVersion) {
		state := p.keys[idx]
		now := p.nowf()
		state.stats.mu.Lock()
		state.stats.sampleTTL = state.performance.SampleTTL
		generation := state.stats.recordSampleLocked(header, full, success, now)
		state.stats.mu.Unlock()
		state.noteSample(generation, now.Sub(schedulerEpoch), success)
	}
}

// Caller holds the pool mutex. Statistics are already committed to this generation.
func (state *KeyState) noteSample(generation uint64, tick time.Duration, success bool) {
	state.syncStatsGeneration(generation, tick)
	if tick >= state.sampleExpires {
		state.liveSamples = 0
	}
	state.sampleExpires = tick + state.performance.SampleTTL
	state.sampleTick = tick
	if state.liveSamples < 2 {
		state.liveSamples++
	}
	if success {
		state.failedSamples = 0
		state.nextExplore = math.MinInt64
	} else {
		if state.failedSamples < 32 {
			state.failedSamples++
		}
		state.nextExplore = tick + explorationBackoff(*state.performance, state.failedSamples)
	}
}

// Reset exploration history lazily under the pool mutex, without taking pool
// locks while a bulk reset holds statistics locks (pool -> stats is the normal
// lock order). Inflight, cooldown, failures and disabled state are untouched.
func (state *KeyState) syncStatsGeneration(generation uint64, tick time.Duration) {
	if state.statsGeneration == generation {
		return
	}
	state.statsGeneration = generation
	state.lastAttempt, state.nextExplore = math.MinInt64, math.MinInt64
	state.sampleTick = tick
	state.sampleExpires = tick + state.performance.SampleTTL
	state.liveSamples, state.failedSamples = 0, 0
}

// A single O(eligible keys) scan, no allocation and no per-key stats locks.
// Credit survives requests whose allowlist contains no exploration candidates.
func (p *Pool) pickPerformanceLocked(now time.Time, excluded map[int]struct{}, allowed []int) (int, bool) {
	tick := now.Sub(schedulerEpoch)
	best, probe := -1, -1
	bestCost := 0.0
	bestDistance := len(p.keys) + 1
	credit := p.exploration.credit
	interval := p.performance.ExplorationInterval
	if credit < interval {
		credit++
	}
	consider := func(i int) {
		if !p.isAvailableLocked(i, now, excluded) {
			return
		}
		state := p.keys[i]
		state.syncStatsGeneration(state.stats.generation.Load(), tick)
		cost := state.stats.performanceCost(p.strategy)
		if p.customPerformance {
			cost = state.stats.configuredCost(p.strategy, p.performance)
		}
		expired := tick >= state.sampleExpires
		if expired {
			age := tick - state.sampleExpires
			neutral := performanceBaseCost(p.strategy, float64(p.performance.LatencyPrior)/float64(time.Millisecond), 0.5, p.performance.SuccessPower)
			if age >= p.performance.DecayDuration {
				cost = neutral
			} else {
				weight := float64(age) / float64(p.performance.DecayDuration)
				cost += (neutral - cost) * weight
			}
		}
		cost *= 1 + float64(state.Inflight)*p.performance.InflightWeight
		distance := i - p.rrIdx
		if distance < 0 {
			distance += len(p.keys)
		}
		if best < 0 || cost < bestCost || (cost == bestCost && distance < bestDistance) {
			best, bestCost, bestDistance = i, cost, distance
		}
		if interval == 0 || credit < interval || state.Inflight != 0 || tick < state.nextExplore {
			return
		}
		due := state.liveSamples < 2 || state.failedSamples > 0 || expired
		if due && (probe < 0 || state.lastAttempt < p.keys[probe].lastAttempt) {
			probe = i
		}
	}
	if allowed == nil {
		for i := range p.keys {
			consider(i)
		}
	} else {
		for _, i := range allowed {
			consider(i)
		}
	}
	if best < 0 {
		return 0, false
	}
	p.exploration.credit = credit
	if probe >= 0 {
		best = probe
		p.exploration.credit = 0
	}
	p.rrIdx = (best + 1) % len(p.keys)
	return best, true
}
