package admin

import (
	"encoding/json"
	"testing"
	"time"

	"jc_proxy/internal/config"
)

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
