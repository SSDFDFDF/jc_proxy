package admin

import (
	"testing"

	"jc_proxy/internal/config"
)

// vendorIDForTest resolves a vendor display name to its generated id. Admin APIs
// and the key store are addressed by id, while fixtures read better by name.
func vendorIDForTest(t *testing.T, cfg *config.Config, name string) string {
	t.Helper()
	entry, ok := cfg.VendorByName(name)
	if !ok {
		t.Fatalf("vendor %q not found in config", name)
	}
	return entry.ID
}

// vendorIDFromService resolves a vendor name through the service's stored config.
func vendorIDFromService(t *testing.T, s *Service, name string) string {
	t.Helper()
	cfg, err := s.store.GetConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return vendorIDForTest(t, cfg, name)
}

// mutateVendorForTest edits one vendor's configuration in place, addressed by
// display name.
func mutateVendorForTest(t *testing.T, cfg *config.Config, name string, fn func(*config.VendorConfig)) {
	t.Helper()
	idx := cfg.VendorIndexByName(name)
	if idx < 0 {
		t.Fatalf("vendor %q not found in config", name)
	}
	fn(&cfg.Vendors[idx].VendorConfig)
}
