package admin

import (
	"sync"
	"time"
)

const (
	loginFailureWindow   = 30 * time.Minute
	loginFailureLimit    = 5
	loginLockBaseDelay   = 15 * time.Second
	loginLockMaxDelay    = 15 * time.Minute
	loginGuardEntryGrace = time.Hour
)

type loginAttempt struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

type LoginGuard struct {
	mu        sync.Mutex
	attempts  map[string]loginAttempt
	now       func() time.Time
	window    time.Duration
	limit     int
	baseDelay time.Duration
	maxDelay  time.Duration
}

func NewLoginGuard() *LoginGuard {
	return &LoginGuard{
		attempts:  map[string]loginAttempt{},
		now:       time.Now,
		window:    loginFailureWindow,
		limit:     loginFailureLimit,
		baseDelay: loginLockBaseDelay,
		maxDelay:  loginLockMaxDelay,
	}
}

func (g *LoginGuard) Blocked(key string) (time.Duration, bool) {
	if g == nil || key == "" {
		return 0, false
	}

	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()

	entry, ok := g.attempts[key]
	if !ok {
		return 0, false
	}
	entry = g.normalizeEntry(entry, now)
	if entry.failures == 0 {
		delete(g.attempts, key)
		return 0, false
	}
	g.attempts[key] = entry
	if entry.lockedUntil.After(now) {
		return entry.lockedUntil.Sub(now), true
	}
	return 0, false
}

func (g *LoginGuard) Failed(key string) time.Duration {
	if g == nil || key == "" {
		return 0
	}

	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()

	entry := g.normalizeEntry(g.attempts[key], now)
	entry.failures++
	entry.lastFailure = now
	if entry.failures >= g.limit {
		entry.lockedUntil = now.Add(g.lockDuration(entry.failures - g.limit))
	}
	g.attempts[key] = entry
	g.pruneLocked(now)
	if entry.lockedUntil.After(now) {
		return entry.lockedUntil.Sub(now)
	}
	return 0
}

func (g *LoginGuard) Succeeded(key string) {
	if g == nil || key == "" {
		return
	}
	g.mu.Lock()
	delete(g.attempts, key)
	g.mu.Unlock()
}

func (g *LoginGuard) normalizeEntry(entry loginAttempt, now time.Time) loginAttempt {
	if entry.lastFailure.IsZero() {
		return entry
	}
	if now.Sub(entry.lastFailure) > g.window {
		return loginAttempt{}
	}
	if !entry.lockedUntil.IsZero() && !entry.lockedUntil.After(now) {
		entry.lockedUntil = time.Time{}
	}
	return entry
}

func (g *LoginGuard) lockDuration(step int) time.Duration {
	delay := g.baseDelay
	for i := 0; i < step; i++ {
		if delay >= g.maxDelay/2 {
			return g.maxDelay
		}
		delay *= 2
	}
	if delay > g.maxDelay {
		return g.maxDelay
	}
	return delay
}

func (g *LoginGuard) pruneLocked(now time.Time) {
	for key, entry := range g.attempts {
		if entry.failures == 0 {
			delete(g.attempts, key)
			continue
		}
		expiredLock := entry.lockedUntil.IsZero() || !entry.lockedUntil.After(now)
		staleFailure := !entry.lastFailure.IsZero() && now.Sub(entry.lastFailure) > g.window+loginGuardEntryGrace
		if expiredLock && staleFailure {
			delete(g.attempts, key)
		}
	}
}
