package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"time"
)

type Session struct {
	Username  string
	ExpiresAt time.Time
}

type SessionManager struct {
	ttl      time.Duration
	sessions map[string]Session
	mu       sync.RWMutex
}

func NewSessionManager(ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &SessionManager{ttl: ttl, sessions: map[string]Session{}}
}

func (m *SessionManager) Create(username string) (token string, expiresAt time.Time) {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	sum := sha256.Sum256(raw)
	token = base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(sum[:8])
	expiresAt = time.Now().Add(m.ttl)

	m.mu.Lock()
	m.sessions[token] = Session{Username: username, ExpiresAt: expiresAt}
	m.mu.Unlock()
	return token, expiresAt
}

func (m *SessionManager) Validate(token string) (Session, bool) {
	m.mu.RLock()
	s, ok := m.sessions[token]
	m.mu.RUnlock()
	if !ok {
		return Session{}, false
	}
	if time.Now().After(s.ExpiresAt) {
		m.mu.Lock()
		delete(m.sessions, token)
		m.mu.Unlock()
		return Session{}, false
	}
	return s, true
}

func (m *SessionManager) Delete(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

func (m *SessionManager) DeleteAll() {
	m.mu.Lock()
	m.sessions = map[string]Session{}
	m.mu.Unlock()
}
