package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/plugins"
)

func TestPluginExecutionStatistics(t *testing.T) {
	for _, script := range []bool{false, true} {
		t.Run(fmtPluginMode(script), func(t *testing.T) {
			p := mustLoadPlugin(t, historyPluginConfig(script))
			if p.snapshot().Calls != 0 || p.snapshot().Last != nil {
				t.Fatal("initialization counted as a request")
			}
			raw := []byte(`{"include":["reasoning.encrypted_content","reasoning.encrypted_content"],"input":[{"type":"reasoning","id":"secret"},{"type":"message","id":"secret","content":"private"},{"type":"function_call","id":"secret","call_id":"keep"}]}`)
			out, err := p.Transform(context.Background(), plugins.Request{Body: raw})
			if err != nil {
				t.Fatal(err)
			}
			stats := p.snapshot()
			want := plugins.Summary{Reported: true, ReasoningRemoved: 1, IDsRemoved: 2, IncludesRemoved: 2, StoreDisabled: true}
			if stats.Calls != 1 || stats.Changed != 1 || stats.Last == nil || stats.Last.Summary != want || stats.Last.InputBytes != len(raw) || stats.Last.OutputBytes != len(out.Body) || stats.Last.DurationNS <= 0 {
				t.Fatalf("changed: %+v %+v", stats, stats.Last)
			}
			encoded, _ := json.Marshal(stats)
			if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "keep") {
				t.Fatalf("request data in statistics: %s", encoded)
			}
			if _, err := p.Transform(context.Background(), plugins.Request{Body: out.Body}); err != nil {
				t.Fatal(err)
			}
			stats = p.snapshot()
			if stats.Unchanged != 1 || stats.Last.Status != "unchanged" || stats.Last.Summary != (plugins.Summary{Reported: true}) {
				t.Fatalf("unchanged: %+v %+v", stats, stats.Last)
			}
			_, err = p.Transform(context.Background(), plugins.Request{Body: []byte(`{"previous_response_id":"secret"}`)})
			stats = p.snapshot()
			if err == nil || stats.Calls != 3 || stats.Rejected != 1 || stats.Last.Status != "rejected" || stats.Last.Summary.Reported {
				t.Fatalf("rejection: %+v %+v", stats, stats.Last)
			}
		})
	}
}

func TestPluginFailureStatistics(t *testing.T) {
	for _, tc := range []struct{ source, reason string }{
		{`throw new Error("private")`, "execution_failed"},
		{`while (true) {}`, "timeout"},
		{`return "x".repeat(33)`, "output_too_large"},
		{`reportHistory("private", 0, 0, false)`, "execution_failed"},
		{`reportHistory(-1, 0, 0, false)`, "execution_failed"},
		{`reportHistory(0.5, 0, 0, false)`, "execution_failed"},
		{`reportHistory(NaN, 0, 0, false)`, "execution_failed"},
		{`reportHistory(Infinity, 0, 0, false)`, "execution_failed"},
		{`reportHistory(67108865, 0, 0, false)`, "execution_failed"},
		{`reportHistory(0, 0, 0, "private")`, "execution_failed"},
	} {
		p := mustLoadPlugin(t, config.PluginConfig{Name: "p", Source: "function onRequest(r) {" + tc.source + "}", MaxBodyBytes: 32, Timeout: 20 * time.Millisecond})
		_, err := p.Transform(context.Background(), plugins.Request{Body: []byte("private")})
		stats := p.snapshot()
		if err == nil || stats.Failed != 1 || stats.Last.Reason != tc.reason || stats.Last.Summary.Reported {
			t.Fatalf("%s: %+v %+v %v", tc.source, stats, stats.Last, err)
		}
	}
	p := mustLoadPlugin(t, historyPluginConfig(false))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Transform(ctx, plugins.Request{})
	if !errors.Is(err, context.Canceled) || p.snapshot().Last.Reason != "canceled" {
		t.Fatal("cancellation missing from stats")
	}
}

func TestPluginPreflightAndUnmatchedStatistics(t *testing.T) {
	r := pluginTestRouter(t, []config.PluginConfig{
		{Name: "p", Builtin: "codex-history-filter", Methods: []string{"POST"}, Paths: []string{"/responses"}, MaxBodyBytes: 32},
		{Name: "next", Builtin: "codex-history-filter", Paths: []string{"/responses"}},
	})
	v := r.vendors["test"]
	req := httptest.NewRequest("POST", "/responses", nil)
	for _, tc := range []struct{ body, encoding, path, reason string }{
		{"{}", "", "/other", ""},
		{"{}", "gzip", "/responses", "content_encoding"},
		{strings.Repeat("x", 33), "", "/responses", "input_too_large"},
		{string([]byte{0xff}), "", "/responses", "invalid_utf8"},
	} {
		req.Header.Set("Content-Encoding", tc.encoding)
		_, err := v.applyRequestPlugins(req, routedPath{path: tc.path}, newBufferedBodySource([]byte(tc.body), false, false))
		if tc.reason == "" {
			if err != nil || v.plugins[0].snapshot().Last != nil {
				t.Fatal("unmatched request recorded")
			}
		} else if err == nil || v.plugins[0].snapshot().Last.Reason != tc.reason {
			t.Fatalf("preflight: %s %v", tc.reason, err)
		}
	}
	stats := r.PluginStats(v.id)[v.id]
	if stats[0].Calls != 0 || stats[0].Blocked != 3 || stats[1].Last != nil || len(r.PluginStats("missing")) != 0 {
		t.Fatalf("preflight attribution or vendor filter: %+v", stats)
	}
}

func TestPluginStatsSurviveReloadOnlyForUnchangedPlugin(t *testing.T) {
	rt, cfg, _ := commitTestRuntime(t)
	file := filepath.Join(t.TempDir(), "plugin.js")
	if err := os.WriteFile(file, []byte(`function onRequest(r) { return r.body; }`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Vendors[0].Plugins = []config.PluginConfig{{Name: "p", Script: file}}
	if err := rt.Update(cfg); err != nil {
		t.Fatal(err)
	}
	id := cfg.Vendors[0].ID
	old := rt.Snapshot().vendorsByID[id].plugins[0]
	if _, err := old.Transform(context.Background(), plugins.Request{Body: []byte("raw")}); err != nil {
		t.Fatal(err)
	}
	if err := rt.RefreshKeys(); err != nil {
		t.Fatal(err)
	}
	current := rt.Snapshot().vendorsByID[id].plugins[0]
	if current.stats != old.stats || current.snapshot().Calls != 1 {
		t.Fatal("key refresh reset plugin statistics")
	}
	// Requests still on the retired router must update the same counters.
	if _, err := old.Transform(context.Background(), plugins.Request{Body: []byte("raw")}); err != nil {
		t.Fatal(err)
	}
	if current.snapshot().Calls != 2 {
		t.Fatal("in-flight request stats lost")
	}
	cfg.Vendors[0].Plugins[0].Timeout = time.Second
	if err := rt.UpdateAndPersist(cfg, func(*config.Config) error { return errors.New("disk failure") }); err == nil {
		t.Fatal("failed save accepted")
	}
	if rt.Snapshot().vendorsByID[id].plugins[0] != current || current.snapshot().Calls != 2 {
		t.Fatal("failed save changed statistics")
	}
	if err := rt.Update(cfg); err != nil {
		t.Fatal(err)
	}
	current = rt.Snapshot().vendorsByID[id].plugins[0]
	if current.stats == old.stats || current.snapshot().Calls != 0 {
		t.Fatal("plugin config change retained obsolete statistics")
	}
	if err := os.WriteFile(file, []byte(`function onRequest() { return "new"; }`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rt.RefreshKeys(); err != nil {
		t.Fatal(err)
	}
	if rt.Snapshot().vendorsByID[id].plugins[0].stats == current.stats {
		t.Fatal("script content change retained obsolete statistics")
	}
}

func TestPluginStatsConcurrentSnapshots(t *testing.T) {
	p := mustLoadPlugin(t, historyPluginConfig(false))
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 32 {
				_, err := p.Transform(context.Background(), plugins.Request{Body: []byte(`{"store":false}`)})
				if err != nil {
					t.Error(err)
				}
				s := p.snapshot()
				if s.Calls != s.Changed+s.Unchanged+s.Rejected+s.Failed {
					t.Error("inconsistent snapshot")
				}
			}
		})
	}
	wg.Wait()
	if p.snapshot().Unchanged != 512 {
		t.Fatal("lost concurrent updates")
	}
}

type noOpStatsPlugin struct{ plugins.Matcher }

func (p noOpStatsPlugin) Transform(_ context.Context, r plugins.Request) (plugins.Result, error) {
	return plugins.Result{Body: r.Body}, nil
}

func BenchmarkPluginObservation(b *testing.B) {
	cfg := historyPluginConfig(false)
	for _, mode := range []string{"direct", "observed", "parallel"} {
		b.Run(mode, func(b *testing.B) {
			var p plugins.RequestPlugin = noOpStatsPlugin{plugins.NewMatcher(cfg)}
			if mode != "direct" {
				p = observeRequestPlugin(p, cfg, "")
			}
			req := plugins.Request{Body: []byte(`{"store":false}`)}
			b.ReportAllocs()
			b.ResetTimer()
			if mode == "parallel" {
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						_, _ = p.Transform(context.Background(), req)
					}
				})
				return
			}
			for b.Loop() {
				_, _ = p.Transform(context.Background(), req)
			}
		})
	}
}
