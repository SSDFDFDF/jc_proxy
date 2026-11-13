// Package plugins defines the shared contract for request-body plugins.
// Implementations live in subpackages and do not depend on the gateway.
package plugins

import (
	"context"
	"strings"
	"time"

	"jc_proxy/internal/config"
)

// RequestPlugin is the request-body extension point. Implementations must be
// safe for concurrent use and must treat Body as immutable. A transform never
// receives credentials or an upstream response and cannot change routing,
// authentication or retry policy.
type RequestPlugin interface {
	Name() string
	Matches(method, path string) bool
	BodyLimit() int64
	Transform(context.Context, Request) (Result, error)
}

type Request struct {
	Body     []byte `json:"body"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	VendorID string `json:"vendor_id"`
	Vendor   string `json:"vendor"`
	Provider string `json:"provider"`
}

// Result carries the transformed body and an optional numeric summary. Summary
// is produced during transformation, never by parsing the body a second time.
type Result struct {
	Body    []byte
	Summary Summary
}

// Summary contains the supported history-filter counters. Other plugins can
// leave Reported false; execution outcome and byte counts are still tracked.
// Do not put request contents, IDs or exception text into runtime statistics.
type Summary struct {
	Reported         bool  `json:"reported"`
	ReasoningRemoved int64 `json:"reasoning_removed"`
	IDsRemoved       int64 `json:"ids_removed"`
	IncludesRemoved  int64 `json:"includes_removed"`
	StoreDisabled    bool  `json:"store_disabled"`
}

// RejectError is an intentional client rejection, separate from plugin
// execution failures. Neither kind is an upstream attempt or a key failure.
type RejectError struct {
	StatusCode int
	Message    string
}

func (e *RejectError) Error() string { return e.Message }

// Matcher provides immutable configuration shared by native and script plugins.
// Construct it once when loading a plugin, then embed it in the implementation.
type Matcher struct {
	name      string
	methods   []string
	paths     []string
	timeout   time.Duration
	bodyLimit int64
}

func NewMatcher(cfg config.PluginConfig) Matcher {
	return Matcher{
		name:      cfg.Name,
		methods:   append([]string(nil), cfg.Methods...),
		paths:     append([]string(nil), cfg.Paths...),
		timeout:   cfg.ExecutionTimeout(),
		bodyLimit: cfg.BodyLimit(),
	}
}

func (m Matcher) Name() string                    { return m.name }
func (m Matcher) BodyLimit() int64                { return m.bodyLimit }
func (m Matcher) ExecutionTimeout() time.Duration { return m.timeout }

func (m Matcher) Matches(method, path string) bool {
	methodOK := len(m.methods) == 0
	for _, configured := range m.methods {
		if method == configured {
			methodOK = true
			break
		}
	}
	if !methodOK {
		return false
	}
	if len(m.paths) == 0 {
		return true
	}
	for _, pattern := range m.paths {
		if pattern == path || (strings.HasSuffix(pattern, "*") && strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))) {
			return true
		}
	}
	return false
}
