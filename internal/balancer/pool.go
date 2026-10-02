package balancer

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"jc_proxy/internal/keystore"
)

type KeyConfig struct {
	Key           string
	Status        string
	DisableReason string
	DisabledAt    *time.Time
	DisabledBy    string
	keystore.RuntimeStats
	Version int64
	Stats   *RuntimeStatsHandle
}

type KeyState struct {
	Key           string
	Status        string
	DisableReason string
	DisabledAt    *time.Time
	DisabledBy    string
	keystore.RuntimeStats
	Version         int64
	Inflight        int
	Failures        int
	CooldownUntil   time.Time
	CooldownLevel   int
	stats           *RuntimeStatsHandle
	retired         bool // removed from the current plan; old routers must not reuse it
	lastAttempt     time.Duration
	sampleExpires   time.Duration
	nextExplore     time.Duration
	liveSamples     int
	failedSamples   int
	statsGeneration uint64
}

const maxLastErrorLength = 240

type loadScore struct {
	primary   int
	secondary int
	cost      float64 // only performance strategies use this dimension
}

func (s loadScore) less(other loadScore) bool {
	if s.cost != other.cost {
		return s.cost < other.cost
	}
	if s.primary != other.primary {
		return s.primary < other.primary
	}
	return s.secondary < other.secondary
}

type Pool struct {
	strategy    string
	keys        []*KeyState
	rrIdx       int
	exploration *explorationState // shared across overlapping router generations

	rng           *rand.Rand
	mu            *sync.Mutex // shared by all overlapping router generations of this vendor
	nowf          func() time.Time
	pickerScratch []int // reusable scratch slice for random picks; protected by mu
}

func NewPool(strategy string, keys []string) (*Pool, error) {
	configs := make([]KeyConfig, 0, len(keys))
	for _, key := range keys {
		configs = append(configs, KeyConfig{
			Key:    key,
			Status: keystore.KeyStatusActive,
		})
	}
	return NewPoolWithConfigs(strategy, configs)
}

func NewPoolWithConfigs(strategy string, keys []KeyConfig) (*Pool, error) {
	switch strategy {
	case "round_robin", "random", "least_used", "least_requests", "lowest_latency", "highest_success", "adaptive":
	default:
		return nil, errors.New("invalid strategy")
	}

	states := make([]*KeyState, 0, len(keys))
	createdAt := time.Since(schedulerEpoch)
	for _, cfg := range keys {
		key := strings.TrimSpace(cfg.Key)
		if key == "" {
			continue
		}
		stats := cfg.Stats
		if stats == nil {
			stats = NewRuntimeStatsHandle(cfg.RuntimeStats)
		}
		status := keystore.NormalizeStatus(cfg.Status)
		states = append(states, &KeyState{
			Key:           key,
			Status:        status,
			DisableReason: strings.TrimSpace(cfg.DisableReason),
			DisabledAt:    cfg.DisabledAt,
			DisabledBy:    strings.TrimSpace(cfg.DisabledBy),
			Version:       cfg.Version,
			stats:         stats,
			sampleExpires: createdAt + sampleFreshness,
			lastAttempt:   math.MinInt64,
			nextExplore:   math.MinInt64,
		})
	}

	return &Pool{
		strategy:    strategy,
		exploration: &explorationState{},
		keys:        states,
		mu:          &sync.Mutex{},
		rng:         rand.New(rand.NewSource(time.Now().UnixNano())),
		nowf:        time.Now,
	}, nil
}

func (p *Pool) Acquire() (idx int, key string, ok bool) {
	return p.AcquireExcept(nil)
}

func (p *Pool) AcquireExcept(excluded map[int]struct{}) (idx int, key string, ok bool) {
	return p.AcquireExceptAllowed(excluded, nil)
}

// AcquireExceptAllowed selects an available key index from allowed.
// A nil allowed slice means all keys are eligible; a non-nil empty slice means none.
func (p *Pool) AcquireExceptAllowed(excluded map[int]struct{}, allowed []int) (idx int, key string, ok bool) {
	idx, key, _, ok = p.AcquireVersioned(excluded, allowed)
	return
}

// AcquireVersioned captures the persisted version in the same critical section
// as selection, so a concurrent admin update cannot relabel an older attempt.
func (p *Pool) AcquireVersioned(excluded map[int]struct{}, allowed []int) (idx int, key string, version int64, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.nowf()
	pick := -1
	switch p.strategy {
	case "random":
		pick, ok = p.pickRandomLocked(now, excluded, allowed)
	case "least_used":
		pick, ok = p.pickByScoreLocked(now, excluded, allowed, func(i int) loadScore {
			return loadScore{
				primary:   p.keys[i].Inflight,
				secondary: p.totalRequestsLocked(i),
			}
		})
	case "least_requests":
		pick, ok = p.pickByScoreLocked(now, excluded, allowed, func(i int) loadScore {
			return loadScore{
				primary:   p.totalRequestsLocked(i) + p.keys[i].Inflight,
				secondary: p.keys[i].Inflight,
			}
		})
	case "lowest_latency", "highest_success", "adaptive":
		pick, ok = p.pickPerformanceLocked(now, excluded, allowed)
	default:
		pick, ok = p.pickRoundRobinLocked(now, excluded, allowed)
	}

	if !ok || pick < 0 {
		return 0, "", 0, false
	}

	p.keys[pick].Inflight++
	p.keys[pick].lastAttempt = now.Sub(schedulerEpoch)
	return pick, p.keys[pick].Key, p.keys[pick].Version, true
}

func (p *Pool) Version(idx int) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.keys) {
		return 0
	}
	return p.keys[idx].Version
}

func (p *Pool) ReleaseSuccess(idx int, expectedVersion ...int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return
	}
	current := p.currentVersionLocked(idx, expectedVersion)
	p.keys[idx].stats.RecordSuccess(!current)
	if !current {
		return
	}
	p.keys[idx].Failures = 0
	p.keys[idx].CooldownLevel = 0
}

func (p *Pool) Release(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.releaseInflightLocked(idx)
}

func (p *Pool) ReleaseInterrupted(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.releaseInflightLocked(idx) {
		p.keys[idx].stats.RecordInterrupted()
	}
}

func (p *Pool) ReleaseFailure(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return
	}
	p.keys[idx].stats.RecordError(0, "upstream request failed")
	p.recordFailureLocked(idx)
}

func (p *Pool) Observe(idx int, statusCode int, reason string, expectedVersion ...int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return
	}
	current := p.currentVersionLocked(idx, expectedVersion)
	p.keys[idx].stats.RecordError(statusCode, reason, !current)
	if !current {
		return
	}
	p.keys[idx].Failures = 0
}

func (p *Pool) Cooldown(idx int, statusCode int, reason string, duration time.Duration, expectedVersion ...int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return
	}
	current := p.currentVersionLocked(idx, expectedVersion)
	p.keys[idx].stats.RecordError(statusCode, reason, !current)
	if !current {
		return
	}
	p.recordFailureLocked(idx)
	duration = scaledCooldownDuration(p.keys[idx].CooldownLevel, duration)
	p.keys[idx].CooldownUntil = p.nowf().Add(duration)
}

func (p *Pool) Disable(idx int, statusCode int, reason, by string, expectedVersion ...int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return false
	}
	current := p.currentVersionLocked(idx, expectedVersion)
	p.keys[idx].stats.RecordError(statusCode, reason, !current)
	if !current {
		return false
	}
	p.disableLocked(idx, keystore.KeyStatusDisabledAuto, reason, by)
	return true
}

func (p *Pool) DisableKey(key, reason, by string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.findIndexLocked(key)
	if idx < 0 {
		return false
	}
	p.disableLocked(idx, keystore.KeyStatusDisabledAuto, reason, by)
	return true
}

func (p *Pool) EnableKey(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.findIndexLocked(key)
	if idx < 0 {
		return false
	}
	p.enableLocked(idx)
	return true
}

func (p *Pool) RecoverKey(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.findIndexLocked(key)
	if idx < 0 {
		return false
	}
	p.enableLocked(idx)
	return true
}

// LoadTotals returns only the scheduling counters, without allocating a full
// key snapshot or locking each key's statistics handle. A non-nil allowlist
// restricts work to those indexes, matching aggregate child key selection.
func (p *Pool) LoadTotals(allowed []int) (inflight, totalRequests int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	add := func(i int) {
		if i >= 0 && i < len(p.keys) {
			inflight += int64(p.keys[i].Inflight)
			totalRequests += int64(p.totalRequestsLocked(i))
		}
	}
	if allowed == nil {
		for i := range p.keys {
			add(i)
		}
	} else {
		for _, i := range allowed {
			add(i)
		}
	}
	return
}

func (p *Pool) Snapshot() []KeyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]KeyState, len(p.keys))
	for i := range cp {
		cp[i] = *p.keys[i]
		if cp[i].stats != nil {
			cp[i].RuntimeStats = cp[i].stats.Snapshot()
		}
	}
	return cp
}

// HasAvailable reports whether at least one key (not in excluded) is active
// and out of cooldown. It avoids the full Snapshot copy used on the hot
// failover-retry path.
func (p *Pool) HasAvailable(excluded map[int]struct{}) bool {
	return p.HasAvailableAllowed(excluded, nil)
}

func (p *Pool) HasAvailableAllowed(excluded map[int]struct{}, allowed []int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.nowf()
	if allowed != nil {
		for _, idx := range allowed {
			if p.isAvailableLocked(idx, now, excluded) {
				return true
			}
		}
		return false
	}
	for i := range p.keys {
		if p.isAvailableLocked(i, now, excluded) {
			return true
		}
	}
	return false
}

// ShareRuntimeStateFrom is a commit-time operation on an UNPUBLISHED pool.
// Retained keys share the actual state and mutex, not a point-in-time copy.
// Old in-flight completions therefore update exactly the state new picks read.
func (p *Pool) ShareRuntimeStateFrom(prev *Pool) {
	if prev == nil || prev == p {
		return
	}
	prev.mu.Lock()
	defer prev.mu.Unlock()
	p.mu = prev.mu
	p.exploration = prev.exploration
	index := make(map[string]*KeyState, len(prev.keys))
	for _, state := range prev.keys {
		index[state.Key] = state
	}
	for i, incoming := range p.keys {
		state := index[incoming.Key]
		if state == nil {
			continue
		}
		delete(index, incoming.Key)
		if incoming.Version > state.Version {
			// Only a newer persisted/admin version may override live health.
			// A stale active snapshot must not resurrect an auto-disabled key.
			state.Status = incoming.Status
			state.DisableReason = incoming.DisableReason
			state.DisabledAt = incoming.DisabledAt
			state.DisabledBy = incoming.DisabledBy
			state.Version = incoming.Version
			state.CooldownUntil = time.Time{}
			state.CooldownLevel = 0
			state.Failures = 0
			if keystore.IsActiveStatus(state.Status) {
				state.stats.ClearLastError()
			}
		}
		p.keys[i] = state
	}
	for _, removed := range index {
		removed.retired = true
	}
}

func (p *Pool) Retire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, state := range p.keys {
		state.retired = true
	}
}

func (p *Pool) currentVersionLocked(idx int, expected []int64) bool {
	return !p.keys[idx].retired && (len(expected) == 0 || p.keys[idx].Version == expected[0])
}

func (p *Pool) Stats() []map[string]any {
	now := time.Now()
	snap := p.Snapshot()
	out := make([]map[string]any, 0, len(snap))
	for _, ks := range snap {
		cooldown := 0
		if ks.CooldownUntil.After(now) {
			cooldown = int(ks.CooldownUntil.Sub(now).Seconds())
		}
		disabledAt := ""
		if ks.DisabledAt != nil {
			disabledAt = ks.DisabledAt.UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]any{
			"key_id":                     keystore.KeyID(ks.Key),
			"key_masked":                 maskKey(ks.Key),
			"status":                     ks.Status,
			"disable_reason":             ks.DisableReason,
			"disabled_by":                ks.DisabledBy,
			"disabled_at":                disabledAt,
			"recent_requests":            ks.RecentRequests,
			"recent_success_count":       ks.RecentSuccessCount,
			"header_samples":             ks.HeaderSamples,
			"avg_header_ms":              ks.AvgHeaderMS,
			"response_samples":           ks.ResponseSamples,
			"avg_response_ms":            ks.AvgResponseMS,
			"total_requests":             ks.TotalRequests,
			"success_count":              ks.SuccessCount,
			"interrupted_count":          ks.InterruptedCount(),
			"inflight":                   ks.Inflight,
			"failures":                   ks.Failures,
			"last_status":                ks.LastStatus,
			"cooldown_level":             ks.CooldownLevel,
			"backoff_remaining_seconds":  cooldown,
			"cooldown_remaining_seconds": cooldown,
			"unauthorized_count":         ks.UnauthorizedCount,
			"forbidden_count":            ks.ForbiddenCount,
			"rate_limit_count":           ks.RateLimitCount,
			"other_error_count":          ks.OtherErrorCount,
			"last_error":                 ks.LastError,
		})
	}
	return out
}

func (p *Pool) releaseInflightLocked(idx int) bool {
	if idx < 0 || idx >= len(p.keys) {
		return false
	}
	if p.keys[idx].Inflight > 0 {
		p.keys[idx].Inflight--
	}
	return true
}

func (p *Pool) pickRoundRobinLocked(now time.Time, excluded map[int]struct{}, allowed []int) (int, bool) {
	if len(p.keys) == 0 || (allowed != nil && len(allowed) == 0) {
		return 0, false
	}
	if allowed == nil {
		for try := 0; try < len(p.keys); try++ {
			candidate := (p.rrIdx + try) % len(p.keys)
			if !p.isAvailableLocked(candidate, now, excluded) {
				continue
			}
			p.rrIdx = (candidate + 1) % len(p.keys)
			return candidate, true
		}
		return 0, false
	}

	best := -1
	bestDistance := len(p.keys) + 1
	for _, candidate := range allowed {
		if !p.isAvailableLocked(candidate, now, excluded) {
			continue
		}
		distance := (candidate - p.rrIdx + len(p.keys)) % len(p.keys)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	if best >= 0 {
		p.rrIdx = (best + 1) % len(p.keys)
		return best, true
	}
	return 0, false
}

func (p *Pool) pickRandomLocked(now time.Time, excluded map[int]struct{}, allowed []int) (int, bool) {
	if len(p.keys) == 0 {
		return 0, false
	}
	scratch := p.pickerScratch[:0]
	appendIfAvailable := func(i int) {
		if p.isAvailableLocked(i, now, excluded) {
			scratch = append(scratch, i)
		}
	}
	if allowed == nil {
		for i := range p.keys {
			appendIfAvailable(i)
		}
	} else {
		for _, i := range allowed {
			appendIfAvailable(i)
		}
	}
	p.pickerScratch = scratch
	if len(scratch) == 0 {
		return 0, false
	}
	return scratch[p.rng.Intn(len(scratch))], true
}

func (p *Pool) pickByScoreLocked(now time.Time, excluded map[int]struct{}, allowed []int, scoref func(int) loadScore) (int, bool) {
	if len(p.keys) == 0 {
		return 0, false
	}

	best := -1
	bestScore := loadScore{}
	bestDistance := len(p.keys) + 1
	consider := func(candidate int) {
		if !p.isAvailableLocked(candidate, now, excluded) {
			return
		}
		score := scoref(candidate)
		distance := (candidate - p.rrIdx + len(p.keys)) % len(p.keys)
		if best < 0 || score.less(bestScore) || (score == bestScore && distance < bestDistance) {
			best = candidate
			bestScore = score
			bestDistance = distance
		}
	}
	if allowed == nil {
		start := p.rrIdx % len(p.keys)
		for offset := 0; offset < len(p.keys); offset++ {
			consider((start + offset) % len(p.keys))
		}
	} else {
		for _, candidate := range allowed {
			consider(candidate)
		}
	}
	if best < 0 {
		return 0, false
	}

	p.rrIdx = (best + 1) % len(p.keys)
	return best, true
}

func (p *Pool) isAvailableLocked(idx int, now time.Time, excluded map[int]struct{}) bool {
	if idx < 0 || idx >= len(p.keys) {
		return false
	}
	if _, skip := excluded[idx]; skip {
		return false
	}
	if p.keys[idx].retired || !keystore.IsActiveStatus(p.keys[idx].Status) {
		return false
	}
	return !p.keys[idx].CooldownUntil.After(now)
}

func (p *Pool) totalRequestsLocked(idx int) int {
	if idx < 0 || idx >= len(p.keys) {
		return 0
	}
	if p.keys[idx].stats != nil {
		return int(p.keys[idx].stats.TotalRequestsFast())
	}
	return p.keys[idx].TotalRequests
}

func (p *Pool) recordFailureLocked(idx int) {
	p.keys[idx].Failures++
	if p.keys[idx].CooldownLevel < 10 {
		p.keys[idx].CooldownLevel++
	}
}

func scaledCooldownDuration(level int, base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	if level <= 1 {
		return base
	}

	factor := int64(1)
	for i := 1; i < level; i++ {
		if factor > math.MaxInt64/2 {
			return time.Duration(math.MaxInt64)
		}
		factor *= 2
	}
	if int64(base) > math.MaxInt64/factor {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(int64(base) * factor)
}

func (p *Pool) disableLocked(idx int, status, reason, by string) {
	now := p.nowf().UTC()
	p.keys[idx].Status = keystore.NormalizeStatus(status)
	p.keys[idx].DisableReason = normalizeLastError(reason)
	p.keys[idx].DisabledBy = strings.TrimSpace(by)
	p.keys[idx].DisabledAt = &now
	p.keys[idx].CooldownUntil = time.Time{}
	p.keys[idx].CooldownLevel = 0
	p.keys[idx].Failures = 0
}

func (p *Pool) enableLocked(idx int) {
	p.keys[idx].Status = keystore.KeyStatusActive
	p.keys[idx].DisableReason = ""
	p.keys[idx].DisabledAt = nil
	p.keys[idx].DisabledBy = ""
	p.keys[idx].CooldownUntil = time.Time{}
	p.keys[idx].CooldownLevel = 0
	p.keys[idx].Failures = 0
	if p.keys[idx].stats != nil {
		p.keys[idx].stats.ClearLastError()
	} else {
		p.keys[idx].LastError = ""
	}
}

func (p *Pool) findIndexLocked(key string) int {
	key = strings.TrimSpace(key)
	for i := range p.keys {
		if p.keys[i].Key == key {
			return i
		}
	}
	return -1
}

func formatStatusError(statusCode int) string {
	text := strings.TrimSpace(http.StatusText(statusCode))
	if text == "" {
		return fmt.Sprintf("upstream returned HTTP %d", statusCode)
	}
	return fmt.Sprintf("upstream returned HTTP %d %s", statusCode, text)
}

func normalizeLastError(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	runes := []rune(message)
	if len(runes) <= maxLastErrorLength {
		return message
	}
	if maxLastErrorLength <= 3 {
		return string(runes[:maxLastErrorLength])
	}
	return string(runes[:maxLastErrorLength-3]) + "..."
}

func maskKey(v string) string {
	if len(v) <= 8 {
		return "****"
	}
	return v[:4] + "..." + v[len(v)-4:]
}
