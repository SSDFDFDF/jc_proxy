package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type AuditLogger struct {
	path string
	mu   sync.Mutex
}

type AuditEvent struct {
	Time   time.Time      `json:"time"`
	Actor  string         `json:"actor"`
	Action string         `json:"action"`
	Detail map[string]any `json:"detail,omitempty"`
}

func NewAuditLogger(path string) *AuditLogger {
	return &AuditLogger{path: path}
}

func (a *AuditLogger) Log(actor, action string, detail map[string]any) {
	if a == nil || a.path == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	_ = os.MkdirAll(filepath.Dir(a.path), 0o755)
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(AuditEvent{
		Time:   time.Now().UTC(),
		Actor:  actor,
		Action: action,
		Detail: detail,
	})
}
