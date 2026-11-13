package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
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
