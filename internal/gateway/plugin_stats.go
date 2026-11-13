package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/plugins"
)

var errPluginOutputLimit = errors.New("plugin output exceeds body limit")

// loadedRequestPlugin owns bounded, in-memory statistics. No request contents
// are retained, and unconfigured/unmatched requests never touch these counters.
type loadedRequestPlugin struct {
	plugins.RequestPlugin
	fingerprint [32]byte
	kind        string
	stats       *pluginRuntimeStats
}

type pluginRuntimeStats struct {
	mu       sync.Mutex
	counters PluginStats
	last     PluginExecution
}

type PluginStats struct {
	Name      string           `json:"name"`
	Kind      string           `json:"kind"`
	Calls     uint64           `json:"calls"`
	Changed   uint64           `json:"changed"`
	Unchanged uint64           `json:"unchanged"`
	Rejected  uint64           `json:"rejected"`
	Failed    uint64           `json:"failed"`
	Blocked   uint64           `json:"blocked"`
	Last      *PluginExecution `json:"last,omitempty"`
}

type PluginExecution struct {
	At          time.Time       `json:"at"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason,omitempty"`
	DurationNS  int64           `json:"duration_ns"`
	InputBytes  int             `json:"input_bytes"`
	OutputBytes int             `json:"output_bytes"`
	Summary     plugins.Summary `json:"summary"`
}

func observeRequestPlugin(p plugins.RequestPlugin, cfg config.PluginConfig, source string) *loadedRequestPlugin {
	// Hash only at load time, including the actual file contents. Configuration
	// and source text are not retained by the statistics or exposed in the API.
	encoded, _ := json.Marshal(cfg)
	hash := sha256.New()
	_, _ = hash.Write(encoded)
	_, _ = hash.Write([]byte(source))
	var fingerprint [32]byte
	copy(fingerprint[:], hash.Sum(nil))
	kind := "javascript"
	if cfg.Builtin != "" {
		kind = "builtin"
	}
	return &loadedRequestPlugin{RequestPlugin: p, fingerprint: fingerprint, kind: kind, stats: &pluginRuntimeStats{}}
}

func (p *loadedRequestPlugin) Transform(ctx context.Context, req plugins.Request) (plugins.Result, error) {
	result, _, err := p.transform(ctx, req)
	return result, err
}

func (p *loadedRequestPlugin) transform(ctx context.Context, req plugins.Request) (plugins.Result, bool, error) {
	start := time.Now()
	result, err := p.RequestPlugin.Transform(ctx, req)
	if err == nil && int64(len(result.Body)) > p.BodyLimit() {
		err = errPluginOutputLimit
	}
	end := time.Now()
	last := PluginExecution{At: end, DurationNS: end.Sub(start).Nanoseconds(), InputBytes: len(req.Body)}
	changed := false
	if err == nil {
		last.Status = "unchanged"
		changed = !bytes.Equal(req.Body, result.Body)
		if changed {
			last.Status = "changed"
		}
		last.OutputBytes, last.Summary = len(result.Body), result.Summary
	} else {
		var rejected *plugins.RejectError
		last.Status, last.Reason = "failed", "execution_failed"
		switch {
		case errors.As(err, &rejected):
			last.Status, last.Reason = "rejected", "rejected"
		case errors.Is(err, context.DeadlineExceeded):
			last.Reason = "timeout"
		case errors.Is(err, context.Canceled):
			last.Reason = "canceled"
		case errors.Is(err, errPluginOutputLimit):
			last.Reason = "output_too_large"
		}
	}
	p.stats.record(last)
	return result, changed, err
}

func (p *loadedRequestPlugin) blocked(reason string, inputBytes int) {
	p.stats.record(PluginExecution{At: time.Now(), Status: "blocked", Reason: reason, InputBytes: inputBytes})
}

func (s *pluginRuntimeStats) record(last PluginExecution) {
	// Transforming, comparing bodies and reading the clock happen outside the
	// lock. Only a few counters and one fixed-size snapshot are copied here.
	s.mu.Lock()
	if last.Status == "blocked" {
		s.counters.Blocked++
	} else {
		s.counters.Calls++
		switch last.Status {
		case "changed":
			s.counters.Changed++
		case "unchanged":
			s.counters.Unchanged++
		case "rejected":
			s.counters.Rejected++
		case "failed":
			s.counters.Failed++
		}
	}
	if !last.At.Before(s.last.At) {
		s.last = last
	}
	s.mu.Unlock()
}

func (p *loadedRequestPlugin) snapshot() PluginStats {
	p.stats.mu.Lock()
	out, last := p.stats.counters, p.stats.last
	p.stats.mu.Unlock()
	out.Name, out.Kind = p.Name(), p.kind
	if !last.At.IsZero() {
		out.Last = &last
	}
	return out
}

// PluginStats reports only loaded plugins on original vendors. Aggregate
// traffic is included in its selected child's counters. Filtering is by vendor
// ID, independent of the key search/pagination in the same admin response.
func (r *Router) PluginStats(vendorID string) map[string][]PluginStats {
	out := make(map[string][]PluginStats)
	for id, v := range r.vendorsByID {
		if len(v.plugins) == 0 || (vendorID != "" && id != vendorID) {
			continue
		}
		rows := make([]PluginStats, 0, len(v.plugins))
		for _, p := range v.plugins {
			rows = append(rows, p.snapshot())
		}
		out[id] = rows
	}
	return out
}

func (v *vendorGateway) sharePluginStatsFrom(prev *vendorGateway) {
	for _, p := range v.plugins {
		for _, old := range prev.plugins {
			if p.fingerprint == old.fingerprint {
				p.stats = old.stats
				break
			}
		}
	}
}
