package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jc_proxy/internal/config"
)

func historyPluginConfig(script bool) config.PluginConfig {
	p := config.PluginConfig{Name: "history", Builtin: "codex-history-filter", Methods: []string{"POST"}, Paths: []string{"/v1/responses", "/responses"}, Timeout: 2 * time.Second}
	if script {
		p.Builtin = ""
		p.Script = "../../plugins/codex-history-filter.js"
	}
	return p
}

func mustLoadPlugin(t testing.TB, cfg config.PluginConfig) RequestPlugin {
	t.Helper()
	plugins, err := loadRequestPlugins([]config.PluginConfig{cfg})
	if err != nil {
		t.Fatal(err)
	}
	return plugins[0]
}

func assertJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("invalid output %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestHistoryPlugins(t *testing.T) {
	for _, script := range []bool{false, true} {
		t.Run(fmtPluginMode(script), func(t *testing.T) {
			p := mustLoadPlugin(t, historyPluginConfig(script))
			input := `{"store":true,"reasoning":{"effort":"high"},"include":["reasoning.encrypted_content","message.output_text.logprobs"],"input":[{"type":"reasoning","id":"rs","encrypted_content":"old"},{"role":"user","id":"msg","content":[{"type":"input_image","file_id":"file","id":"nested"}]},{"type":"function_call","id":"fc","call_id":"pair","arguments":"{}"},{"type":"function_call_output","id":"out","call_id":"pair","output":"ok"},{"type":"custom_tool_call","id":"custom","call_id":"custom-pair"},{"type":"custom_tool_call_output","id":"custom-out","call_id":"custom-pair"},{"type":"computer_call","id":"keep"}],"metadata":{"id":"untouched"}}`
			want := `{"store":false,"reasoning":{"effort":"high"},"include":["message.output_text.logprobs"],"input":[{"role":"user","content":[{"type":"input_image","file_id":"file","id":"nested"}]},{"type":"function_call","call_id":"pair","arguments":"{}"},{"type":"function_call_output","call_id":"pair","output":"ok"},{"type":"custom_tool_call","call_id":"custom-pair"},{"type":"custom_tool_call_output","call_id":"custom-pair"},{"type":"computer_call","id":"keep"}],"metadata":{"id":"untouched"}}`
			raw := []byte(input)
			got, err := p.Transform(context.Background(), PluginRequest{Body: raw})
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, got, want)
			if string(raw) != input {
				t.Fatal("mutated original history")
			}
			for _, input := range []string{`{}`, `{"input":"hello"}`, `{"input":[],"include":[]}`, `{"input":[null,42,"text"]}`} {
				if _, err := p.Transform(context.Background(), PluginRequest{Body: []byte(input)}); err != nil {
					t.Fatalf("%s: %v", input, err)
				}
			}
			for _, input := range []string{`not json`, `[]`, `null`, `{"input":null}`, `{"include":null}`, `{"previous_response_id":"resp"}`, `{"conversation":"conv"}`, `{"context_management":[{"type":"compaction"}]}`, `{"input":[{"type":"item_reference","id":"ref"}]}`, `{"input":[{"type":"message","encrypted_content":"old"}]}`} {
				_, err := p.Transform(context.Background(), PluginRequest{Body: []byte(input)})
				var rejection *PluginRejectError
				if !errors.As(err, &rejection) || rejection.StatusCode != 400 {
					t.Fatalf("%s: %v", input, err)
				}
			}
		})
	}
}

func fmtPluginMode(script bool) string {
	if script {
		return "javascript"
	}
	return "native"
}

func TestNativeHistoryPreservesLargeNumbers(t *testing.T) {
	p := mustLoadPlugin(t, historyPluginConfig(false))
	out, err := p.Transform(context.Background(), PluginRequest{Body: []byte(`{"metadata":{"number":9007199254740993},"input":[{"role":"user","content":"hi","id":"remove"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("9007199254740993")) {
		t.Fatalf("number changed: %s", out)
	}
}

func TestScriptIsolationAndLimits(t *testing.T) {
	p := mustLoadPlugin(t, config.PluginConfig{Name: "isolated", Source: `let count = 0; function onRequest(r) { count++; return r.vendor + ":" + count + ":" + r.body; }`, Timeout: 2 * time.Second})
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Go(func() {
			got, err := p.Transform(context.Background(), PluginRequest{Body: []byte("raw"), Vendor: "vendor"})
			if err != nil || string(got) != "vendor:1:raw" {
				t.Errorf("state leaked: %s %v", got, err)
			}
		})
	}
	wg.Wait()
	for _, source := range []string{`function onRequest(`, `const x = 1;`, `while(true) {}`, `function onRequest() {} ; throw new Error("request-secret")`} {
		_, err := loadRequestPlugins([]config.PluginConfig{{Name: "invalid", Source: source, Timeout: 10 * time.Millisecond}})
		if err == nil || strings.Contains(err.Error(), "request-secret") {
			t.Fatalf("invalid script: %v", err)
		}
	}
	for _, source := range []string{`function onRequest() { while(true) {} }`, `function onRequest() { return { get secret() { while(true) {} } }; }`, `async function onRequest() { return "async"; }`, `function onRequest() { return "x".repeat(33); }`} {
		p := mustLoadPlugin(t, config.PluginConfig{Name: "limited", Source: source, Timeout: 10 * time.Millisecond, MaxBodyBytes: 32})
		if _, err := p.Transform(context.Background(), PluginRequest{}); err == nil {
			t.Fatal("unbounded or invalid return accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Transform(ctx, PluginRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestPluginRouterReloadIsAtomic(t *testing.T) {
	rt, cfg, _ := commitTestRuntime(t)
	file := filepath.Join(t.TempDir(), "plugin.js")
	write := func(source string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`function onRequest() { return "old"; }`)
	cfg.Vendors[0].Plugins = []config.PluginConfig{{Name: "file", Script: file}}
	if err := rt.Update(cfg); err != nil {
		t.Fatal(err)
	}
	old := rt.Snapshot()
	write(`function onRequest() { return "new"; }`)
	out, err := old.vendors["openai"].plugins[0].Transform(context.Background(), PluginRequest{})
	if err != nil || string(out) != "old" {
		t.Fatal("script read on request path")
	}
	if err := rt.RefreshKeys(); err != nil {
		t.Fatal(err)
	}
	out, err = rt.Snapshot().vendors["openai"].plugins[0].Transform(context.Background(), PluginRequest{})
	if err != nil || string(out) != "new" {
		t.Fatal("script failed to reload")
	}
	current := rt.Snapshot()
	write(`function onRequest(`)
	persisted := false
	if err := rt.UpdateAndPersist(cfg, func(*config.Config) error { persisted = true; return nil }); err == nil {
		t.Fatal("invalid update accepted")
	}
	if persisted || rt.Snapshot() != current {
		t.Fatal("invalid script changed live router/config")
	}
}

func pluginTestRouter(t *testing.T, plugins []config.PluginConfig) *Router {
	t.Helper()
	cfg := &config.Config{Vendors: testVendorsWithoutClientAuth(map[string]config.VendorConfig{"test": {
		Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid"}, Plugins: plugins,
		ClientHeaders: config.ClientHeadersConfig{Allowlist: []string{"Content-Type", "Content-Length", "Content-MD5", "Digest"}},
	}})}
	r, err := newTestRouter(cfg, map[string][]string{"test": {"key1", "key2"}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPluginGatewayFailuresDoNotAffectKeys(t *testing.T) {
	for _, tc := range []struct {
		name, source, body, encoding string
		limit                        int64
		status                       int
	}{
		{"exception", `throw new Error(r.body)`, "secret", "", 100, 500},
		{"output limit", `return "x".repeat(101)`, "body", "", 100, 500},
		{"input limit", `return r.body`, "large", "", 4, 413},
		{"compression", `return r.body`, "gzip", "gzip", 100, 415},
		{"reject", `reject(422, "unsupported input")`, "secret", "", 100, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := pluginTestRouter(t, []config.PluginConfig{{Name: "p", Source: "function onRequest(r) {" + tc.source + "}", MaxBodyBytes: tc.limit}})
			calls := 0
			r.vendors["test"].client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("must not reach upstream")
			})
			for _, chunked := range []bool{false, true} {
				req := httptest.NewRequest("POST", "/test/v1/responses", strings.NewReader(tc.body))
				req.Header.Set("Content-Encoding", tc.encoding)
				if chunked {
					req.ContentLength = -1
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != tc.status || calls != 0 || strings.Contains(w.Body.String(), "secret") {
					t.Fatalf("status %d: %s, calls %d", w.Code, w.Body, calls)
				}
			}
			for _, state := range r.vendors["test"].pool.Snapshot() {
				if state.TotalRequests != 0 {
					t.Fatal("plugin failure counted as key traffic")
				}
			}
		})
	}
}

func TestPluginRunsAfterAuthAndUsesExistingMasking(t *testing.T) {
	r := pluginTestRouter(t, []config.PluginConfig{{Name: "reject", Source: `function onRequest() { reject(422, "unsupported input"); }`}})
	v := r.vendors["test"]
	p := &countedPlugin{RequestPlugin: v.plugins[0]}
	v.plugins[0] = p
	v.clientAuthDisabled = false
	v.clientAuth = map[string]struct{}{"client-key": {}}
	v.errorPolicy.Masking.Rules = []config.ErrorMaskingRule{{StatusCodes: []int{422}, StatusCode: 400, Body: `{"error":"filtered"}`}}
	req := httptest.NewRequest("POST", "/test/v1/responses", strings.NewReader("raw"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 || p.calls.Load() != 0 {
		t.Fatal("plugin ran before client authentication")
	}
	req = httptest.NewRequest("POST", "/test/v1/responses", strings.NewReader("raw"))
	req.Header.Set("Authorization", "Bearer client-key")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 400 || w.Body.String() != `{"error":"filtered"}` || p.calls.Load() != 1 {
		t.Fatalf("masking not applied: %d %s", w.Code, w.Body)
	}
}

func TestPluginBufferDoesNotEnableUnsafePostReplay(t *testing.T) {
	r := pluginTestRouter(t, []config.PluginConfig{historyPluginConfig(false)})
	v := r.vendors["test"]
	v.errorPolicy.Failover = config.ErrorFailoverConfig{RequestError: configBoolPtr(true), ResponseStatusCodes: []int{500}, MaxAttempts: 2}
	for _, transportFailure := range []bool{false, true} {
		calls := 0
		v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			_, _ = io.Copy(io.Discard, req.Body)
			_ = req.Body.Close()
			if transportFailure {
				return nil, errors.New("upstream failed after upload")
			}
			return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("upstream error"))}, nil
		})
		req := httptest.NewRequest("POST", "/test/v1/responses", strings.NewReader(`{"input":"hi"}`))
		req.ContentLength = -1
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if calls != 1 || w.Code < 500 {
			t.Fatalf("unsafe POST replayed: %d calls, status %d", calls, w.Code)
		}
	}
}

type countedPlugin struct {
	RequestPlugin
	calls atomic.Int32
}

func (p *countedPlugin) Transform(ctx context.Context, req PluginRequest) ([]byte, error) {
	p.calls.Add(1)
	return p.RequestPlugin.Transform(ctx, req)
}

func TestPluginKeyRetryReusesTransformedBodyAndPreservesSSE(t *testing.T) {
	r := pluginTestRouter(t, []config.PluginConfig{historyPluginConfig(false)})
	v := r.vendors["test"]
	counted := &countedPlugin{RequestPlugin: v.plugins[0]}
	v.plugins[0] = counted
	v.errorPolicy.Failover.ResponseStatusCodes = []int{429}
	var bodies [][]byte
	stream := "data: {\"type\":\"response.completed\"}\n\n"
	v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = req.Body.Close()
		bodies = append(bodies, body)
		if req.ContentLength != int64(len(body)) || req.Header.Get("Content-MD5") != "" || req.Header.Get("Digest") != "" {
			t.Fatal("stale body headers")
		}
		if len(bodies) == 1 {
			return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("rate limited"))}, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
	})
	req := httptest.NewRequest("POST", "/test/v1/responses", strings.NewReader(`{"input":[{"type":"reasoning"},{"type":"message","id":"old","content":"你好"}]}`))
	req.Header.Set("Content-MD5", "stale")
	req.Header.Set("Digest", "stale")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != stream || len(bodies) != 2 || counted.calls.Load() != 1 {
		t.Fatalf("status=%d bodies=%d plugins=%d response=%s", w.Code, len(bodies), counted.calls.Load(), w.Body)
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("key retry body changed")
	}
	assertJSONEqual(t, bodies[0], `{"store":false,"input":[{"type":"message","content":"你好"}]}`)
}

func TestPluginChainMatchingAndVendorTest(t *testing.T) {
	plugins := []config.PluginConfig{
		{Name: "first", Source: `function onRequest(r) { return r.body + ":" + r.vendor + ":" + r.path; }`, Methods: []string{"POST"}, Paths: []string{"/v2/*"}},
		{Name: "second", Source: `function onRequest(r) { return r.body + ":second"; }`, Methods: []string{"POST"}, Paths: []string{"/v2/*"}},
		{Name: "disabled", Script: "/missing/file.js", Enabled: boolPtrFalse()},
	}
	r := pluginTestRouter(t, plugins)
	v := r.vendors["test"]
	v.rewrites = newRewriteMatcher(map[string]string{"/v1/responses": "/v2/responses"})
	var got string
	v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		got = string(body)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	for _, tc := range []struct{ method, path, want string }{{"POST", "/v1/responses", "raw:test:/v2/responses:second"}, {"GET", "/v1/responses", "raw"}, {"POST", "/other", "raw"}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, "/test"+tc.path, strings.NewReader("raw")))
		if w.Code != 200 || got != tc.want {
			t.Fatalf("%s %s: %d %q", tc.method, tc.path, w.Code, got)
		}
	}
	_, err := v.executeTest(context.Background(), VendorTestRequest{Endpoint: "/v1/responses", Method: "POST", Body: "raw", Key: "key1"})
	if err != nil || got != "raw:test:/v2/responses:second" {
		t.Fatalf("vendor test bypassed plugins: %q %v", got, err)
	}
}

func TestAggregatePluginsUseEachChildOriginalBody(t *testing.T) {
	for _, secondPlugin := range []bool{false, true} {
		vendors := map[string]config.VendorConfig{
			"a":   {Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid"}, Plugins: []config.PluginConfig{{Name: "a", Source: `function onRequest(r) { return r.body + ":a"; }`}}},
			"b":   {Upstream: config.UpstreamConfig{BaseURL: "https://example.invalid"}},
			"agg": {Provider: "aggregate", Aggregate: config.AggregateConfig{Children: []config.AggregateChild{{VendorID: "vid_a", Priority: 0}, {VendorID: "vid_b", Priority: 1}}, Retry: config.AggregateRetryConfig{StatusCodes: []int{429}, MaxAttempts: 2}}},
		}
		if secondPlugin {
			b := vendors["b"]
			b.Plugins = []config.PluginConfig{{Name: "b", Source: `function onRequest(r) { return r.body + ":b"; }`}}
			vendors["b"] = b
		}
		cfg := &config.Config{Vendors: testVendorsWithoutClientAuth(vendors)}
		r, err := newTestRouter(cfg, map[string][]string{"a": {"a-key"}, "b": {"b-key"}})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]string{}
		for name, status := range map[string]int{"a": 429, "b": 200} {
			r.vendors[name].client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(req.Body)
				_ = req.Body.Close()
				seen[name] = string(body)
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
			})
		}
		req := httptest.NewRequest("POST", "/agg/v1/responses", strings.NewReader("raw"))
		req.ContentLength = -1
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		wantB := "raw"
		if secondPlugin {
			wantB += ":b"
		}
		if w.Code != 200 || seen["a"] != "raw:a" || seen["b"] != wantB {
			t.Fatalf("aggregate body contamination: %d %+v", w.Code, seen)
		}
	}
}

func BenchmarkRequestPlugins(b *testing.B) {
	for _, size := range []int{4096, 128 << 10} {
		raw := []byte(`{"input":[{"type":"reasoning","id":"rs"},{"type":"message","id":"msg","role":"user","content":"` + strings.Repeat("x", size) + `"}]}`)
		for _, mode := range []string{"none", "unmatched", "native", "javascript"} {
			b.Run(mode+"/"+fmt.Sprint(size), func(b *testing.B) {
				v := &vendorGateway{name: "bench"}
				if mode != "none" {
					v.plugins = []RequestPlugin{mustLoadPlugin(b, historyPluginConfig(mode == "javascript"))}
				}
				req := httptest.NewRequest("POST", "/v1/responses", nil)
				path := routedPath{path: "/v1/responses"}
				if mode == "unmatched" {
					path.path = "/v1/models"
				}
				source := newBufferedBodySource(raw, false, false)
				b.ReportAllocs()
				b.SetBytes(int64(len(raw)))
				b.ResetTimer()
				for b.Loop() {
					if len(v.plugins) > 0 {
						if _, err := v.applyRequestPlugins(req, path, source); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}
