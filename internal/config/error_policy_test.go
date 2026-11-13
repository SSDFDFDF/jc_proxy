package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestErrorRuleListsSurviveCloneAndSerialization(t *testing.T) {
	for _, auto := range []ErrorAutoDisableConfig{
		{},
		{StatusCodes: []int{}, Keywords: []string{}},
		{Enabled: boolPtr(false), StatusCodes: []int{}, Keywords: []string{}},
		{StatusCodes: []int{498}},
		{Keywords: []string{"custom credential error"}},
	} {
		cfg := &Config{Vendors: VendorsFromMap(map[string]VendorConfig{"child": {Provider: "generic", Upstream: UpstreamConfig{BaseURL: "http://example.invalid"}, ErrorPolicy: ErrorPolicyConfig{AutoDisable: auto}}})}
		if err := cfg.PrepareAndValidate(); err != nil {
			t.Fatal(err)
		}
		want := cfg.Vendors[0].ErrorPolicy
		cloned, err := cfg.Clone()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := EncodeYAML(cloned)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadBytes(encoded)
		if err != nil {
			t.Fatal(err)
		}
		for _, roundtrip := range []*Config{cloned, loaded} {
			got := roundtrip.Vendors[0].ErrorPolicy
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("policy changed across serialization: want=%+v got=%+v", want, got)
			}
			if got.Cooldown.RequestError.Duration != 0 || boolValue(got.Failover.RequestError, false) || len(got.Cooldown.ResponseRules) != 0 || len(got.Failover.ResponseStatusCodes) != 0 {
				t.Fatalf("roundtrip added actions: %+v", got)
			}
			payload, err := json.Marshal(got.AutoDisable)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(payload, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["status_codes"] == nil || fields["keywords"] == nil {
				t.Fatal("JSON must preserve empty lists for console editing")
			}
		}
	}
}

func TestLoadLegacyAggregateErrorPolicy(t *testing.T) {
	policy := ErrorPolicyConfig{
		AutoDisable: ErrorAutoDisableConfig{Enabled: boolPtr(false), StatusCodes: []int{401}, Keywords: []string{"invalid_api_key"}},
		Cooldown: ErrorCooldownConfig{
			RequestError:  ErrorCooldownRule{Enabled: boolPtr(true), Duration: time.Second},
			ResponseRules: []ErrorResponseCooldownRule{{StatusCodes: []int{429}, Duration: time.Minute}},
		},
		Failover: ErrorFailoverConfig{RequestError: boolPtr(false), ResponseStatusCodes: []int{429}, MaxAttempts: 5},
		Masking: ErrorMaskingConfig{Enabled: boolPtr(false), Rules: []ErrorMaskingRule{
			{StatusCodes: []int{503}, Keywords: []string{"overloaded"}, StatusCode: 429, Body: `{"error":"busy"}`, ContentType: "application/json", RetryAfter: "5s", Cooldown: time.Minute},
		}},
	}
	legacy := &Config{SchemaVersion: CurrentSchemaVersion, Vendors: []VendorEntry{
		{ID: "child", Name: "child", VendorConfig: VendorConfig{
			Provider: "generic", Upstream: UpstreamConfig{BaseURL: "http://example.invalid"}, ErrorPolicy: policy,
			Plugins: []PluginConfig{{Name: "history", Builtin: "codex-history-filter"}},
		}},
		{ID: "entry", Name: "cc", VendorConfig: VendorConfig{
			Provider: "aggregate", ErrorPolicy: policy, Aggregate: AggregateConfig{
				Children: []AggregateChild{{VendorID: "child", Weight: 2, KeyIDs: []string{"key-id"}}},
				Retry:    AggregateRetryConfig{Enabled: boolPtr(true), NetworkError: boolPtr(true), StatusCodes: []int{429}, MaxAttempts: 3},
			},
		}},
	}}
	// Marshal directly: live config validation must continue rejecting this
	// aggregate policy, even though old persisted payloads are loadable.
	payload, err := yaml.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	for name, load := range map[string]func([]byte) (*Config, error){
		"file": LoadBytes, "bootstrap": LoadBootstrapBytes, "pgsql": LoadBootstrapBytesNoEnv,
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := load(payload)
			if err != nil {
				t.Fatalf("legacy aggregate prevented startup: %v", err)
			}
			child, _ := cfg.VendorByID("child")
			if !reflect.DeepEqual(child.ErrorPolicy, policy) || len(child.Plugins) != 1 {
				t.Fatal("child policy or plugins changed")
			}
			agg, _ := cfg.VendorByID("entry")
			wantMasking := policy.Masking
			wantMasking.Rules = append([]ErrorMaskingRule(nil), policy.Masking.Rules...)
			wantMasking.Rules[0].Cooldown = 0
			if !reflect.DeepEqual(agg.ErrorPolicy, ErrorPolicyConfig{Masking: wantMasking}) {
				t.Fatalf("unexpected aggregate policy: %+v", agg.ErrorPolicy)
			}
			if !reflect.DeepEqual(agg.Aggregate, legacy.Vendors[1].Aggregate) {
				t.Fatal("aggregate routing or retry changed")
			}
			encoded, err := EncodeYAML(cfg)
			if err != nil {
				t.Fatal(err)
			}
			reloaded, err := LoadBootstrapBytesNoEnv(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.Vendors, reloaded.Vendors) {
				t.Fatal("normalized config changed on reload")
			}
		})
	}
	if err := legacy.PrepareAndValidate(); err == nil {
		t.Fatal("newly submitted aggregate key policy was silently accepted")
	}
	if _, err := legacy.Clone(); err == nil {
		t.Fatal("runtime clone bypassed strict policy validation")
	}
}

func TestLegacyAggregateNormalizationKeepsOtherValidation(t *testing.T) {
	for _, tc := range []struct{ name, extra, want string }{
		{"invalid masking", "    error_policy:\n      masking:\n        rules:\n          - status_codes: [503]\n            status_code: 200\n", "masking"},
		{"plugins", "    plugins:\n      - name: history\n        builtin: codex-history-filter\n", "non-aggregate"},
		{"key balancing", "    load_balance: adaptive\n", "managed-key strategy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadBootstrapBytesNoEnv([]byte("schema_version: 3\nvendors:\n  - id: cc\n    name: cc\n    provider: aggregate\n" + tc.extra))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid aggregate accepted: %v", err)
			}
		})
	}
}

func TestAggregateRejectsKeyPoliciesAndAcceptsResponseMasking(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy ErrorPolicyConfig
		valid  bool
	}{
		{"empty", ErrorPolicyConfig{}, true},
		{"masking", ErrorPolicyConfig{Masking: ErrorMaskingConfig{Rules: []ErrorMaskingRule{{StatusCodes: []int{503}, StatusCode: 429}}}}, true},
		{"auto-switch-off", ErrorPolicyConfig{AutoDisable: ErrorAutoDisableConfig{Enabled: boolPtr(false)}}, false},
		{"auto-disable", ErrorPolicyConfig{AutoDisable: ErrorAutoDisableConfig{StatusCodes: []int{401}}}, false},
		{"cooldown", ErrorPolicyConfig{Cooldown: ErrorCooldownConfig{ResponseRules: []ErrorResponseCooldownRule{{StatusCodes: []int{429}, Duration: time.Minute}}}}, false},
		{"key-attempt-limit", ErrorPolicyConfig{Failover: ErrorFailoverConfig{MaxAttempts: 5}}, false},
		{"key-failover", ErrorPolicyConfig{Failover: ErrorFailoverConfig{ResponseStatusCodes: []int{429}}}, false},
		{"mask-cooldown", ErrorPolicyConfig{Masking: ErrorMaskingConfig{Rules: []ErrorMaskingRule{{StatusCodes: []int{429}, StatusCode: 500, Cooldown: time.Minute}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Vendors: []VendorEntry{
				{ID: "vid_child", Name: "child", VendorConfig: VendorConfig{Provider: "generic", Upstream: UpstreamConfig{BaseURL: "http://example.invalid"}}},
				{ID: "vid_agg", Name: "agg", VendorConfig: VendorConfig{Provider: "aggregate", Aggregate: AggregateConfig{Children: []AggregateChild{{VendorID: "vid_child"}}}, ErrorPolicy: tc.policy}},
			}}
			err := cfg.PrepareAndValidate()
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && (err == nil || !strings.Contains(err.Error(), "child vendors")) {
				t.Fatalf("unsupported aggregate policy should explain ownership: %v", err)
			}
		})
	}
}
