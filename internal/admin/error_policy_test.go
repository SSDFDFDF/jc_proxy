package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"jc_proxy/internal/config"
)

func TestStoreLoadsLegacyAggregatePolicy(t *testing.T) {
	const payload = `schema_version: 3
admin:
  enabled: false
vendors:
  - id: child
    name: child
    provider: generic
    upstream:
      base_url: https://example.invalid
    error_policy:
      auto_disable:
        status_codes: [401]
  - id: cc
    name: cc
    provider: aggregate
    aggregate:
      children:
        - vendor_id: child
    error_policy:
      auto_disable:
        enabled: false
      cooldown:
        request_error:
          enabled: false
          duration: 0s
      failover:
        request_error: false
        max_attempts: 5
      masking:
        rules:
          - status_codes: [503]
            status_code: 429
            cooldown: 1m
`
	for _, remote := range []bool{false, true} {
		name := "file"
		if remote {
			name = "pgsql"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			bootstrap, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			var backend *fakeConfigBackend
			if remote {
				// Use the same decoder as PGConfigBackend.Load; no production DB.
				loaded, err := config.LoadBootstrapBytesNoEnv([]byte(payload))
				if err != nil {
					t.Fatal(err)
				}
				backend = &fakeConfigBackend{loaded: &loadedConfig{cfg: loaded}}
				stubRemoteBackend(t, backend)
				bootstrap = remoteTestBootstrap(t)
			}
			store, err := NewStore(path, bootstrap)
			if err != nil {
				t.Fatalf("init admin store failed: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			cfg, err := store.GetConfig()
			if err != nil {
				t.Fatal(err)
			}
			agg, _ := cfg.VendorByID("cc")
			want := config.ErrorMaskingConfig{Rules: []config.ErrorMaskingRule{{StatusCodes: []int{503}, StatusCode: 429}}}
			if err := cfg.ValidateBootstrap(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(agg.ErrorPolicy.Masking, want) {
				t.Fatalf("unexpected policy: %+v", agg.ErrorPolicy)
			}
			if backend != nil && len(backend.saved) != 0 {
				t.Fatal("compatibility read should not rewrite database")
			}
			disk, err := os.ReadFile(path)
			if err != nil || string(disk) != payload {
				t.Fatal("compatibility read rewrote local configuration")
			}
			if err := store.UpdateConfig(cfg); err != nil {
				t.Fatal(err)
			}
			if remote {
				if len(backend.saved) != 1 {
					t.Fatal("normalized config was not saved")
				}
				cfg = backend.saved[0]
			} else {
				cfg, err = config.Load(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			agg, _ = cfg.VendorByID("cc")
			if !reflect.DeepEqual(agg.ErrorPolicy.Masking, want) {
				t.Fatal("normal save restored stale aggregate policy")
			}
			child, _ := cfg.VendorByID("child")
			if !reflect.DeepEqual(child.ErrorPolicy.AutoDisable.StatusCodes, []int{401}) {
				t.Fatal("child policy changed")
			}
		})
	}
}

func TestUpdateVendorClearsErrorRuleListsWithoutRestoringDefaults(t *testing.T) {
	s := newTestService(t)
	cfg, err := s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := cfg.VendorByName("openai")
	if !ok {
		t.Fatal("vendor missing")
	}
	vc := entry.VendorConfig
	vc.ErrorPolicy = config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{StatusCodes: []int{401}, Keywords: []string{"bad credential"}},
		Failover:    config.ErrorFailoverConfig{ResponseStatusCodes: []int{429}},
		Masking:     config.ErrorMaskingConfig{Rules: []config.ErrorMaskingRule{{StatusCodes: []int{405}, StatusCode: 429}}},
	}
	if err := s.UpdateVendor("admin", entry.ID, vc); err != nil {
		t.Fatal(err)
	}
	// A real JSON payload must distinguish explicit empty lists from omitted
	// fields, including masking.rules without an enabled flag.
	if err := json.Unmarshal([]byte(`{"error_policy":{"auto_disable":{"status_codes":[],"keywords":[]},"failover":{"response_status_codes":[]},"masking":{"rules":[]}}}`), &vc); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateVendor("admin", entry.ID, vc); err != nil {
		t.Fatal(err)
	}
	cfg, err = s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := cfg.VendorByID(entry.ID)
	if !ok {
		t.Fatal("vendor missing after update")
	}
	p := updated.ErrorPolicy
	if len(p.AutoDisable.StatusCodes) != 0 || len(p.AutoDisable.Keywords) != 0 || len(p.Failover.ResponseStatusCodes) != 0 || len(p.Masking.Rules) != 0 {
		t.Fatalf("cleared rules were restored: %+v", p)
	}
}

func TestUpdateVendorConvertsSupplierToAggregateWithoutInheritingKeyPolicy(t *testing.T) {
	s := newTestService(t)
	id := vendorIDFromService(t, s, "openai")
	cfg, err := s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := cfg.VendorByID(id)
	vc := entry.VendorConfig
	vc.ErrorPolicy = config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{Enabled: testBoolPtr(true), StatusCodes: []int{401}},
		Cooldown:    config.ErrorCooldownConfig{RequestError: config.ErrorCooldownRule{Duration: time.Minute}},
		Failover:    config.ErrorFailoverConfig{RequestError: testBoolPtr(true)},
		Masking:     config.ErrorMaskingConfig{Rules: []config.ErrorMaskingRule{{StatusCodes: []int{429}, StatusCode: 500, Cooldown: time.Minute}}},
	}
	if err := s.UpdateVendor("admin", id, vc); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateVendor("admin", id, config.VendorConfig{Provider: "aggregate"}); err != nil {
		t.Fatalf("conversion inherited supplier policy: %v", err)
	}
	cfg, err = s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	converted, _ := cfg.VendorByID(id)
	if converted.Provider != "aggregate" || converted.ErrorPolicy.AutoDisable.Enabled != nil || converted.ErrorPolicy.Cooldown.RequestError.Duration != 0 || len(converted.ErrorPolicy.Masking.Rules) != 0 {
		t.Fatalf("unexpected converted policy: %+v", converted)
	}
	// Explicit supplier-only policies must still be rejected, not stripped.
	if err := s.UpdateVendor("admin", id, config.VendorConfig{Provider: "aggregate", ErrorPolicy: vc.ErrorPolicy}); err == nil {
		t.Fatal("aggregate accepted an explicit supplier policy")
	}
}
