package config

import (
	"strings"
	"testing"
	"time"
)

func TestUploadTimeoutDefaultsDisableAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   *time.Duration
		want    time.Duration
		invalid bool
	}{
		{"default", nil, 300 * time.Second, false},
		{"disabled", durationPtr(0), 0, false},
		{"custom", durationPtr(2 * time.Second), 2 * time.Second, false},
		{"negative", durationPtr(-time.Second), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Vendors: VendorsFromMap(map[string]VendorConfig{"openai": {Upstream: UpstreamConfig{BaseURL: "https://example.invalid", UploadTimeout: tc.value}}})}
			err := cfg.PrepareAndValidate()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "upload_timeout") {
					t.Fatalf("validation=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cloned, err := cfg.Clone()
			if err != nil {
				t.Fatal(err)
			}
			if got := cloned.Vendors[0].Upstream.UploadTimeout; got == nil || *got != tc.want {
				t.Fatalf("timeout=%v want=%v", got, tc.want)
			}
		})
	}
}
