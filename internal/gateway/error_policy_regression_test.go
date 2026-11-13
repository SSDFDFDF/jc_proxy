package gateway

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

func TestEmptyErrorPoliciesForwardWithoutHealthActionsOrRetries(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		for _, status := range []int{401, 402, 403, 429, 500, 502, 503, 504, 529} {
			t.Run(fmt.Sprintf("aggregate=%v/status=%d", aggregate, status), func(t *testing.T) {
				cfg := newAggregateRetryTestConfig(config.AggregateRetryConfig{})
				for i := range cfg.Vendors {
					cfg.Vendors[i].ErrorPolicy = config.ErrorPolicyConfig{}
				}
				r, err := newTestRouter(cfg, map[string][]string{"child_a": {"k1", "k2"}, "child_b": {"k3"}})
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				body := "invalid_api_key incorrect_api_key insufficient_quota slow down"
				transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"3600"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				r.vendors["child_a"].client.Transport = transport
				r.vendors["child_b"].client.Transport = transport
				path := "/child_a/test"
				if aggregate {
					path = "/agg/test"
				}
				for range 2 {
					w := httptest.NewRecorder()
					r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
					if w.Code != status || w.Body.String() != body || w.Header().Get("Retry-After") != "3600" {
						t.Fatalf("response=%d %q %v", w.Code, w.Body.String(), w.Header())
					}
				}
				if calls != 2 {
					t.Fatalf("empty policy retried: %d calls", calls)
				}
				for _, states := range r.VendorStateSnapshots() {
					for _, state := range states {
						if state.Status != keystore.KeyStatusActive || state.Failures != 0 || state.CooldownLevel != 0 || !state.CooldownUntil.IsZero() || state.Inflight != 0 {
							t.Fatalf("empty policy changed health: %+v", state)
						}
					}
				}
			})
		}
	}
}

func TestEmptyRequestErrorPolicyDoesNotRetryOrCooldown(t *testing.T) {
	cfg := newAggregateRetryTestConfig(config.AggregateRetryConfig{})
	for i := range cfg.Vendors {
		cfg.Vendors[i].ErrorPolicy = config.ErrorPolicyConfig{}
	}
	r, err := newTestRouter(cfg, aggregateTestSeed())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("dial failed") })
	r.vendors["child_a"].client.Transport = transport
	r.vendors["child_b"].client.Transport = transport
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/agg/test", nil))
	if calls != 1 || w.Code != 502 {
		t.Fatalf("calls=%d response=%d", calls, w.Code)
	}
	for _, states := range r.VendorStateSnapshots() {
		for _, s := range states {
			if s.Failures != 0 || !s.CooldownUntil.IsZero() || s.Status != keystore.KeyStatusActive {
				t.Fatalf("empty request-error policy changed health: %+v", s)
			}
		}
	}
}

func TestUnmatchedExplicitCooldownHasNoFallback(t *testing.T) {
	policy := config.ErrorPolicyConfig{Cooldown: config.ErrorCooldownConfig{ResponseRules: []config.ErrorResponseCooldownRule{{StatusCodes: []int{418}, Duration: time.Minute}}}}
	for _, status := range []int{401, 402, 403, 429, 500, 529} {
		d := classifyResponse("openai", policy, status, http.Header{"Retry-After": {"3600"}}, []byte("slow down insufficient_quota"))
		if d.action != keyActionObserve || d.cooldown != 0 || d.failover {
			t.Fatalf("unmatched HTTP %d: %+v", status, d)
		}
	}
	policy.Cooldown.ResponseRules = []config.ErrorResponseCooldownRule{{StatusCodes: []int{429}, Duration: time.Minute}}
	if d := classifyResponse("openai", policy, 429, http.Header{"Retry-After": {"3600"}}, nil); d.cooldown != time.Minute {
		t.Fatalf("Retry-After mode must be explicit: %+v", d)
	}
	policy.AutoDisable.Keywords = []string{"余额不足"}
	if d := classifyResponse("generic", policy, 409, nil, []byte(`{"error":{"message":"other error"},"vendor_custom":{"description":"\u4f59\u989d\u4e0d\u8db3"}}`)); d.action != keyActionDisable {
		t.Fatalf("custom JSON keyword must be decoded and matched: %+v", d)
	}
}

func TestExplicitRetryStatusListsHaveSameMeaning(t *testing.T) {
	for _, status := range []int{401, 429, 500, 529} {
		policy := config.ErrorPolicyConfig{Failover: config.ErrorFailoverConfig{ResponseStatusCodes: []int{529}}}
		retry := config.AggregateRetryConfig{StatusCodes: []int{529}}
		want := status == 529
		if shouldFailoverResponse(status, policy) != want || aggregateRetryable(status, nil, retry) != want {
			t.Fatalf("lists disagree for status %d", status)
		}
	}
}

func TestHotUpdateBlocksOldAutoDisableAndRespectsFailedPublication(t *testing.T) {
	for _, mode := range []string{"disabled", "cleared", "empty-policy", "changed-rules", "disable-reenable", "converted-aggregate", "failed-persist", "unchanged-policy", "presentation-only", "retry-only", "keys-refresh"} {
		t.Run(mode, func(t *testing.T) {
			cfg := &config.Config{Vendors: testVendorsWithoutClientAuth(map[string]config.VendorConfig{
				"child": {Provider: "generic", Upstream: config.UpstreamConfig{BaseURL: "http://unused.invalid"}, ErrorPolicy: config.ErrorPolicyConfig{AutoDisable: config.ErrorAutoDisableConfig{StatusCodes: []int{401}}}},
			})}
			if err := cfg.PrepareAndValidate(); err != nil {
				t.Fatal(err)
			}
			ctrl := &testKeyController{records: seedUpstreamKeys(cfg, map[string][]string{"child": {"key"}})}
			rt, err := NewRuntime(cfg, ctrl)
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			oldVendor := rt.Snapshot().vendors["child"]
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			rt.Snapshot().vendors["child"].client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				close(started)
				<-release
				return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("denied"))}, nil
			})
			go func() {
				defer close(done)
				rt.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/child/test", nil))
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			next, err := cfg.Clone()
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "disabled", "failed-persist":
				next.Vendors[0].ErrorPolicy.AutoDisable.Enabled = configBoolPtr(false)
			case "cleared":
				next.Vendors[0].ErrorPolicy.AutoDisable.StatusCodes = []int{}
				next.Vendors[0].ErrorPolicy.AutoDisable.Keywords = []string{}
			case "empty-policy":
				next.Vendors[0].ErrorPolicy = config.ErrorPolicyConfig{}
			case "changed-rules":
				next.Vendors[0].ErrorPolicy.AutoDisable.StatusCodes = []int{403}
			case "disable-reenable":
				next.Vendors[0].ErrorPolicy.AutoDisable.Enabled = configBoolPtr(false)
			case "converted-aggregate":
				next.Vendors[0].Provider = "aggregate"
				next.Vendors[0].ErrorPolicy = config.ErrorPolicyConfig{}
			case "presentation-only":
				next.Vendors[0].ErrorPolicy.Masking.Rules = []config.ErrorMaskingRule{{StatusCodes: []int{401}, StatusCode: 500}}
			case "retry-only":
				next.Vendors[0].ErrorPolicy.Failover.ResponseStatusCodes = []int{401}
			}
			if mode == "failed-persist" {
				if err := rt.UpdateAndPersist(next, func(*config.Config) error { return errors.New("disk failed") }); err == nil {
					t.Fatal("failed persist succeeded")
				}
			} else if mode == "keys-refresh" {
				if err := rt.RefreshKeys(); err != nil {
					t.Fatal(err)
				}
			} else if err := rt.Update(next); err != nil {
				t.Fatal(err)
			}
			if mode == "disable-reenable" {
				if err := rt.Update(cfg); err != nil {
					t.Fatal(err)
				}
			}
			once.Do(func() { close(release) })
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("request did not finish")
			}
			s := oldVendor.pool.Snapshot()[0]
			if mode == "failed-persist" || mode == "unchanged-policy" || mode == "presentation-only" || mode == "retry-only" || mode == "keys-refresh" {
				if s.Status != keystore.KeyStatusDisabledAuto {
					t.Fatalf("failed publication altered current policy: %+v", s)
				}
			} else {
				_, _, persisted, _, _ := ctrl.snapshot()
				if s.Status != keystore.KeyStatusActive || persisted != "" {
					t.Fatalf("old request disabled after policy update: %+v, persisted=%q", s, persisted)
				}
			}
			if s.TotalRequests != 1 || s.UnauthorizedCount != 1 || s.Inflight != 0 {
				t.Fatalf("lost attempt accounting: %+v", s)
			}
		})
	}
}

func TestHotUpdateClearedCooldownBlocksOldAttempts(t *testing.T) {
	for _, mode := range []string{"response", "request", "masking"} {
		t.Run(mode, func(t *testing.T) {
			policy := config.ErrorPolicyConfig{}
			switch mode {
			case "response":
				policy.Cooldown.ResponseRules = []config.ErrorResponseCooldownRule{{StatusCodes: []int{429}, Duration: time.Minute}}
			case "request":
				policy.Cooldown.RequestError = config.ErrorCooldownRule{Duration: time.Minute}
			case "masking":
				policy.Masking.Rules = []config.ErrorMaskingRule{{StatusCodes: []int{429}, StatusCode: 500, Cooldown: time.Minute}}
			}
			cfg := &config.Config{Vendors: testVendorsWithoutClientAuth(map[string]config.VendorConfig{
				"child": {Provider: "generic", Upstream: config.UpstreamConfig{BaseURL: "http://unused.invalid"}, ErrorPolicy: policy},
			})}
			if err := cfg.PrepareAndValidate(); err != nil {
				t.Fatal(err)
			}
			ctrl := &testKeyController{records: seedUpstreamKeys(cfg, map[string][]string{"child": {"key"}})}
			rt, err := NewRuntime(cfg, ctrl)
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			rt.Snapshot().vendors["child"].client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				close(started)
				<-release
				if mode == "request" {
					return nil, errors.New("dial failed")
				}
				return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("limited"))}, nil
			})
			go func() {
				defer close(done)
				rt.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/child/test", nil))
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			next, err := cfg.Clone()
			if err != nil {
				t.Fatal(err)
			}
			next.Vendors[0].ErrorPolicy = config.ErrorPolicyConfig{}
			if err := rt.Update(next); err != nil {
				t.Fatal(err)
			}
			once.Do(func() { close(release) })
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("request did not finish")
			}
			s := rt.Snapshot().vendors["child"].pool.Snapshot()[0]
			if !s.CooldownUntil.IsZero() || s.Failures != 0 || s.TotalRequests != 1 || s.SuccessCount != 0 || s.Inflight != 0 {
				t.Fatalf("old request applied cleared cooldown or lost accounting: %+v", s)
			}
		})
	}
}

func TestAggregateOwnsFinalErrorMasking(t *testing.T) {
	for _, mode := range []string{"upstream", "interrupted-preview", "network", "no-child", "empty-success"} {
		t.Run(mode, func(t *testing.T) {
			cfg := newAggregateRetryTestConfig(config.AggregateRetryConfig{})
			for i := range cfg.Vendors {
				cfg.Vendors[i].ErrorPolicy = config.ErrorPolicyConfig{Masking: config.ErrorMaskingConfig{Rules: []config.ErrorMaskingRule{{StatusCodes: []int{405, 502, 503}, StatusCode: 500}}}}
			}
			mutateTestVendor(t, cfg, "agg", func(v *config.VendorConfig) {
				v.ErrorPolicy.Masking.Rules = []config.ErrorMaskingRule{{StatusCodes: []int{405, 502, 503}, StatusCode: 429, Message: "uniform error"}}
			})
			seed := aggregateTestSeed()
			if mode == "no-child" {
				seed = nil
			}
			r, err := newTestRouter(cfg, seed)
			if err != nil {
				t.Fatal(err)
			}
			transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
				if mode == "network" {
					return nil, errors.New("dial failed")
				}
				if mode == "empty-success" {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: http.NoBody}, nil
				}
				if mode == "interrupted-preview" {
					return &http.Response{StatusCode: 405, Header: make(http.Header), Body: &bytesAndErrorBody{}}, nil
				}
				return &http.Response{StatusCode: 405, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private upstream error"))}, nil
			})
			r.vendors["child_a"].client.Transport = transport
			r.vendors["child_b"].client.Transport = transport
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/agg/test", nil))
			if w.Code != 429 || !strings.Contains(w.Body.String(), "uniform error") || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("aggregate mask was bypassed: %d %q", w.Code, w.Body.String())
			}
			if mode == "upstream" {
				states := r.vendors["child_a"].pool.Snapshot()
				if states[0].LastStatus != 405 || states[0].OtherErrorCount != 1 || states[0].Failures != 0 {
					t.Fatalf("mask changed real health outcome: %+v", states[0])
				}
				w = httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", "/child_a/test", nil))
				if w.Code != 500 {
					t.Fatal("direct child must retain its own presentation policy")
				}
			}
		})
	}
}

func TestInterruptedErrorPreviewPreservesStatusAndKeywordPolicies(t *testing.T) {
	for _, filter := range []string{"status", "keyword"} {
		t.Run(filter, func(t *testing.T) {
			r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, func(p *config.ErrorPolicyConfig) {
				p.AutoDisable.Keywords = []string{"partial"}
				rule := config.ErrorMaskingRule{StatusCode: 429, Message: "uniform error"}
				if filter == "status" {
					rule.StatusCodes = []int{405}
				} else {
					rule.Keywords = []string{"partial"}
				}
				p.Masking.Rules = []config.ErrorMaskingRule{rule}
			})
			v := r.vendors["openai"]
			v.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 405, Header: http.Header{"Retry-After": {"30"}}, Body: &bytesAndErrorBody{}}, nil
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/openai/test", nil))
			if w.Code != 429 || !strings.Contains(w.Body.String(), "uniform error") || w.Header().Get("Retry-After") != "30" {
				t.Fatalf("interrupted preview bypassed original policy: %d %q %v", w.Code, w.Body.String(), w.Header())
			}
			s := v.pool.Snapshot()[0]
			if s.Status != keystore.KeyStatusDisabledAuto || s.LastStatus != 405 || s.OtherErrorCount != 1 || s.TotalRequests != 1 || s.Inflight != 0 {
				t.Fatalf("partial preview lost health classification or accounting: %+v", s)
			}
		})
	}
}

type beforeReadBody struct {
	io.Reader
	before func()
	once   sync.Once
}

func (b *beforeReadBody) Read(p []byte) (int, error) {
	b.once.Do(b.before)
	return b.Reader.Read(p)
}
func (b *beforeReadBody) Close() error { return nil }

func TestAggregatePreparationFailureCanSwitchBeforeSendingPOST(t *testing.T) {
	for _, stage := range []string{"preparation", "upstream"} {
		t.Run(stage, func(t *testing.T) {
			cfg := newAggregateRetryTestConfig(config.AggregateRetryConfig{StatusCodes: []int{503}})
			for i := range cfg.Vendors {
				cfg.Vendors[i].ErrorPolicy = config.ErrorPolicyConfig{}
			}
			r, err := newTestRouter(cfg, map[string][]string{"child_a": {"a"}, "child_b": {"b"}})
			if err != nil {
				t.Fatal(err)
			}
			calls := make(map[string]int)
			for _, name := range []string{"child_a", "child_b"} {
				v := r.vendors[name]
				v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					calls[name]++
					body, err := io.ReadAll(req.Body)
					if err != nil || string(body) != "payload" {
						t.Fatalf("request body lost during child switch: %q %v", body, err)
					}
					status := 200
					if name == "child_a" {
						status = 503
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("result"))}, nil
				})
			}
			req := httptest.NewRequest("POST", "/agg/test", strings.NewReader("payload"))
			req.GetBody = nil
			if stage == "preparation" {
				req.Body = &beforeReadBody{Reader: strings.NewReader("payload"), before: func() {
					// The child was available when selected; its last key becomes
					// unavailable while the request body is being buffered.
					r.vendors["child_a"].pool.DisableKey("a", "admin disabled", "admin")
				}}
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if stage == "preparation" {
				if w.Code != 200 || calls["child_a"] != 0 || calls["child_b"] != 1 {
					t.Fatalf("unsent POST could not switch children: status=%d calls=%v", w.Code, calls)
				}
			} else if w.Code != 503 || calls["child_a"] != 1 || calls["child_b"] != 0 {
				t.Fatalf("POST was replayed after upstream 503: status=%d calls=%v", w.Code, calls)
			}
		})
	}
}
