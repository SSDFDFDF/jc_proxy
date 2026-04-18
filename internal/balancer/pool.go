package balancer

import (
	"errors"
	"fmt"
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
	Version       int64
	Inflight      int
	Failures      int
	CooldownUntil time.Time
	CooldownLevel int
	stats         *RuntimeStatsHandle
}

const maxLastErrorLength = 240

type Pool struct {
	strategy         string
	keys             []KeyState
	rrIdx            int
	backoffThreshold int
	backoffDuration  time.Duration

	rng  *rand.Rand
	mu   sync.Mutex
	nowf func() time.Time
}

func NewPool(strategy string, keys []string, backoffThreshold int, backoffDuration time.Duration) (*Pool, error) {
	configs := make([]KeyConfig, 0, len(keys))
	for _, key := range keys {
		configs = append(configs, KeyConfig{
			Key:    key,
			Status: keystore.KeyStatusActive,
		})
	}
	return NewPoolWithConfigs(strategy, configs, backoffThreshold, backoffDuration)
}

func NewPoolWithConfigs(strategy string, keys []KeyConfig, backoffThreshold int, backoffDuration time.Duration) (*Pool, error) {
	switch strategy {
	case "round_robin", "random", "least_used":
	default:
		return nil, errors.New("invalid strategy")
	}
	if backoffThreshold <= 0 {
		backoffThreshold = 3
	}
	if backoffDuration <= 0 {
		backoffDuration = 3 * time.Hour
	}

	states := make([]KeyState, 0, len(keys))
	for _, cfg := range keys {
		key := strings.TrimSpace(cfg.Key)
		if key == "" {
			continue
		}
		stats := cfg.Stats
		if stats == nil {
			stats = NewRuntimeStatsHandle(cfg.RuntimeStats)
		} else {
			stats.MergeBaseline(cfg.RuntimeStats)
		}
		status := keystore.NormalizeStatus(cfg.Status)
		states = append(states, KeyState{
			Key:           key,
			Status:        status,
			DisableReason: strings.TrimSpace(cfg.DisableReason),
			DisabledAt:    cfg.DisabledAt,
			DisabledBy:    strings.TrimSpace(cfg.DisabledBy),
			Version:       cfg.Version,
			stats:         stats,
		})
	}

	return &Pool{
		strategy:         strategy,
		keys:             states,
		backoffThreshold: backoffThreshold,
		backoffDuration:  backoffDuration,
		rng:              rand.New(rand.NewSource(time.Now().UnixNano())),
		nowf:             time.Now,
	}, nil
}

func (p *Pool) Acquire() (idx int, key string, ok bool) {
	return p.AcquireExcept(nil)
}

func (p *Pool) AcquireExcept(excluded map[int]struct{}) (idx int, key string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.nowf()
	available := make([]int, 0, len(p.keys))
	for i := range p.keys {
		if _, skip := excluded[i]; skip {
			continue
		}
		if !keystore.IsActiveStatus(p.keys[i].Status) {
			continue
		}
		if p.keys[i].CooldownUntil.After(now) {
			continue
		}
		available = append(available, i)
	}
	if len(available) == 0 {
		return 0, "", false
	}

	pick := 0
	switch p.strategy {
	case "random":
		pick = available[p.rng.Intn(len(available))]
	case "least_used":
		pick = available[0]
		for _, i := range available[1:] {
			if p.keys[i].Inflight < p.keys[pick].Inflight {
				pick = i
			}
		}
	default:
		for try := 0; try < len(p.keys); try++ {
			candidate := p.rrIdx % len(p.keys)
			p.rrIdx++
			if _, skip := excluded[candidate]; skip {
				continue
			}
			if !keystore.IsActiveStatus(p.keys[candidate].Status) {
				continue
			}
			if p.keys[candidate].CooldownUntil.After(now) {
				continue
			}
			pick = candidate
			break
		}
	}

	p.keys[pick].Inflight++
	return pick, p.keys[pick].Key, true
}

func (p *Pool) Version(idx int) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.keys) {
		return 0
	}
	return p.keys[idx].Version
}

func (p *Pool) ReleaseSuccess(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return
	}
	p.recordSuccessLocked(idx)
	p.keys[idx].Failures = 0
	p.keys[idx].CooldownLevel = 0
}

func (p *Pool) Release(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.releaseInflightLocked(idx)
}

func (p *Pool) ReleaseFailure(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return
	}
	p.keys[idx].TotalRequests++
	p.recordFailureLocked(idx)
}

func (p *Pool) Observe(idx int, statusCode int, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return
	}
	p.recordErrorLocked(idx, statusCode, reason)
	p.keys[idx].Failures = 0
}

func (p *Pool) Cooldown(idx int, statusCode int, reason string, duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return
	}
	p.recordErrorLocked(idx, statusCode, reason)
	p.recordFailureLocked(idx)
	if duration <= 0 {
		duration = p.backoffDuration
	}
	p.keys[idx].CooldownUntil = p.nowf().Add(duration)
}

func (p *Pool) Disable(idx int, statusCode int, reason, by string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.releaseInflightLocked(idx) {
		return
	}
	p.recordErrorLocked(idx, statusCode, reason)
	p.disableLocked(idx, keystore.KeyStatusDisabledAuto, reason, by)
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
	return true
}

func (p *Pool) Snapshot() []KeyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]KeyState, len(p.keys))
	copy(cp, p.keys)
	for i := range cp {
		if cp[i].stats != nil {
			cp[i].RuntimeStats = cp[i].stats.Snapshot()
		}
	}
	return cp
}

func (p *Pool) MergeRuntimeStats(states []KeyState) {
	if len(states) == 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	index := make(map[string]KeyState, len(states))
	for _, state := range states {
		index[strings.TrimSpace(state.Key)] = state
	}

	for i := range p.keys {
		current := &p.keys[i]
		prev, ok := index[current.Key]
		if !ok {
			continue
		}
		if current.stats != nil {
			current.stats.MergeBaseline(prev.RuntimeStats)
			continue
		}
		if prev.TotalRequests > current.TotalRequests {
			current.TotalRequests = prev.TotalRequests
		}
		if prev.SuccessCount > current.SuccessCount {
			current.SuccessCount = prev.SuccessCount
		}
		if prev.UnauthorizedCount > current.UnauthorizedCount {
			current.UnauthorizedCount = prev.UnauthorizedCount
		}
		if prev.ForbiddenCount > current.ForbiddenCount {
			current.ForbiddenCount = prev.ForbiddenCount
		}
		if prev.RateLimitCount > current.RateLimitCount {
			current.RateLimitCount = prev.RateLimitCount
		}
		if prev.OtherErrorCount > current.OtherErrorCount {
			current.OtherErrorCount = prev.OtherErrorCount
		}
		if prev.TotalRequests >= current.TotalRequests {
			current.LastStatus = prev.LastStatus
			current.LastError = prev.LastError
		}
	}
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
			"key_masked":                 maskKey(ks.Key),
			"status":                     ks.Status,
			"disable_reason":             ks.DisableReason,
			"disabled_by":                ks.DisabledBy,
			"disabled_at":                disabledAt,
			"total_requests":             ks.TotalRequests,
			"success_count":              ks.SuccessCount,
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

func (p *Pool) recordFailureLocked(idx int) {
	p.keys[idx].Failures++
	if p.keys[idx].Failures >= p.backoffThreshold {
		p.keys[idx].Failures = 0
	}
	if p.keys[idx].CooldownLevel < 10 {
		p.keys[idx].CooldownLevel++
	}
}

func (p *Pool) recordSuccessLocked(idx int) {
	if p.keys[idx].stats != nil {
		p.keys[idx].stats.RecordSuccess()
		return
	}
	p.keys[idx].TotalRequests++
	p.keys[idx].SuccessCount++
	p.keys[idx].LastStatus = http.StatusOK
	p.keys[idx].LastError = ""
}

func (p *Pool) recordErrorLocked(idx int, statusCode int, reason string) {
	if p.keys[idx].stats != nil {
		p.keys[idx].stats.RecordError(statusCode, reason)
		return
	}
	p.keys[idx].TotalRequests++
	p.keys[idx].LastStatus = statusCode
	p.keys[idx].LastError = normalizeLastError(reason)

	switch statusCode {
	case http.StatusUnauthorized:
		p.keys[idx].UnauthorizedCount++
	case http.StatusForbidden:
		p.keys[idx].ForbiddenCount++
	case http.StatusTooManyRequests:
		p.keys[idx].RateLimitCount++
	default:
		if statusCode >= http.StatusBadRequest || statusCode == 0 {
			p.keys[idx].OtherErrorCount++
		}
	}
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
