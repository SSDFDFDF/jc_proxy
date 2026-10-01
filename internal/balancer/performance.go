package balancer

import (
	"math"
	"time"

	"jc_proxy/internal/keystore"
)

const (
	recentWindowSize    = 5
	sampleFreshness     = 5 * time.Minute
	explorationInterval = 10
)

// Process-local monotonic offsets keep scheduling state compact and let one
// clock conversion serve the entire scan. Persisted statistics need no schema change.
var schedulerEpoch = time.Now()

type explorationState struct{ credit int }

type requestSample struct {
	header  time.Duration // negative means no response headers
	full    time.Duration // negative means body not read to EOF
	success bool
}

// RecordSample records one finished upstream attempt, in completion order.
// Client cancellation/downstream write failures must be filtered by the caller.
// This fixed-size ring needs no allocation, timer, request log or body copy.
func (h *RuntimeStatsHandle) RecordSample(header, full time.Duration, success bool) {
	h.recordSampleAt(header, full, success, time.Now())
}

func (h *RuntimeStatsHandle) recordSampleAt(header, full time.Duration, success bool, now time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.lastSample.IsZero() && now.Sub(h.lastSample) >= sampleFreshness {
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
}

// Costs are precomputed on completion, so selection loads only one atomic per
// candidate rather than acquiring every key's statistics mutex under pool.mu.
func (h *RuntimeStatsHandle) updateCostsLocked() {
	s := h.stats.RecentStats
	if s.RecentRequests > 0 && s.RecentSuccessCount == 0 {
		// A fast failure is not a fast service. All-failed windows go to the
		// back of the queue regardless of tiny error-response timings; normal
		// age-based exploration still allows them to recover.
		penalty := math.Float64bits(1e12)
		h.latencyCost.Store(penalty)
		h.successCost.Store(penalty)
		h.adaptiveCost.Store(penalty)
		return
	}
	p := float64(s.RecentSuccessCount+1) / float64(s.RecentRequests+2) // Laplace smoothing
	header := 1000.0                                                   // neutral 1s prior when timing is unknown
	full := header
	// Display averages include failures. Scheduling averages must not: fast
	// error responses cannot improve a key's latency score.
	if h.sampleCount > 0 {
		var hs, fs int
		var ht, ft float64
		for _, sample := range h.window[:h.sampleCount] {
			if !sample.success {
				continue
			}
			if sample.header >= 0 {
				hs++
				ht += float64(sample.header) / float64(time.Millisecond)
			}
			if sample.full >= 0 {
				fs++
				ft += float64(sample.full) / float64(time.Millisecond)
			}
		}
		if hs > 0 {
			header = ht / float64(hs)
		}
		full = header
		if fs > 0 {
			full = ft / float64(fs)
		}
	} else if s.RecentRequests == s.RecentSuccessCount {
		// Persisted summaries have no separate successful timings. Only an
		// all-success summary can safely seed latency costs after restart.
		if s.HeaderSamples > 0 {
			header = s.AvgHeaderMS
		}
		full = header
		if s.ResponseSamples > 0 {
			full = s.AvgResponseMS
		}
	}
	header = math.Max(1, math.Min(header, 3_600_000))
	full = math.Max(header, math.Min(full, 3_600_000))
	h.latencyCost.Store(math.Float64bits(header / p))
	h.successCost.Store(math.Float64bits(1 / (p * p)))
	h.adaptiveCost.Store(math.Float64bits((0.7*header + 0.3*full) / (p * p)))
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

func (p *Pool) RecordSample(idx int, header, full time.Duration, success bool, expectedVersion ...int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx >= 0 && idx < len(p.keys) && p.currentVersionLocked(idx, expectedVersion) {
		state := p.keys[idx]
		now := p.nowf()
		tick := now.Sub(schedulerEpoch)
		if tick >= state.sampleExpires {
			state.liveSamples = 0
		}
		state.sampleExpires = tick + sampleFreshness
		if state.liveSamples < 2 {
			state.liveSamples++
		}
		if success {
			state.failedSamples = 0
			state.nextExplore = math.MinInt64
		} else {
			if state.failedSamples < 4 {
				state.failedSamples++
			}
			delay := time.Minute * time.Duration(1<<(state.failedSamples-1))
			if delay > sampleFreshness {
				delay = sampleFreshness
			}
			state.nextExplore = tick + delay
		}
		state.stats.recordSampleAt(header, full, success, now)
	}
}

// A single O(eligible keys) scan, no allocation and no per-key stats locks.
// Credit survives requests whose allowlist contains no exploration candidates.
func (p *Pool) pickPerformanceLocked(now time.Time, excluded map[int]struct{}, allowed []int) (int, bool) {
	tick := now.Sub(schedulerEpoch)
	best, probe := -1, -1
	bestCost := 0.0
	bestDistance := len(p.keys) + 1
	credit := p.exploration.credit
	if credit < explorationInterval {
		credit++
	}
	consider := func(i int) {
		if !p.isAvailableLocked(i, now, excluded) {
			return
		}
		state := p.keys[i]
		cost := state.stats.performanceCost(p.strategy)
		expired := tick >= state.sampleExpires
		if expired {
			age := tick - state.sampleExpires
			neutral := 4000.0
			if p.strategy == "lowest_latency" {
				neutral = 2000
			} else if p.strategy == "highest_success" {
				neutral = 4
			}
			if age >= sampleFreshness {
				cost = neutral
			} else {
				weight := float64(age) / float64(sampleFreshness)
				cost += (neutral - cost) * weight
			}
		}
		cost *= float64(state.Inflight + 1)
		distance := i - p.rrIdx
		if distance < 0 {
			distance += len(p.keys)
		}
		if best < 0 || cost < bestCost || (cost == bestCost && distance < bestDistance) {
			best, bestCost, bestDistance = i, cost, distance
		}
		if credit < explorationInterval || state.Inflight != 0 || tick < state.nextExplore {
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
