package gateway

import (
	"testing"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

// seedUpstreamKeys turns a vendor-name keyed seed map into the vendor-id keyed
// record map the router consumes. Tests declare seeds by name because that is
// what reads well in a fixture, while production storage is partitioned by the
// immutable vendor id.
func seedUpstreamKeys(cfg *config.Config, seed map[string][]string) map[string][]keystore.Record {
	if len(seed) == 0 {
		return nil
	}
	out := make(map[string][]keystore.Record, len(seed))
	for name, keys := range seed {
		entry, ok := cfg.VendorByName(name)
		if !ok {
			continue
		}
		records := make([]keystore.Record, 0, len(keys))
		for _, key := range keys {
			records = append(records, keystore.NormalizeRecord(keystore.Record{
				Key:    key,
				Status: keystore.KeyStatusActive,
			}))
		}
		out[entry.ID] = records
	}
	return out
}

// newTestRouter builds a router whose vendors already hold the given upstream
// keys, replacing the removed inline `upstream.keys` config field.
func newTestRouter(cfg *config.Config, seed map[string][]string) (*Router, error) {
	return NewWithUpstreamKeyRecords(cfg, seedUpstreamKeys(cfg, seed), nil)
}

// vendorID resolves a vendor display name to its generated id, for tests that
// need to address id-keyed structures such as runtime statistics.
func vendorID(t *testing.T, cfg *config.Config, name string) string {
	t.Helper()
	entry, ok := cfg.VendorByName(name)
	if !ok {
		t.Fatalf("vendor %q not found in config", name)
	}
	return entry.ID
}

// aggregateTestSeed is the default upstream key seed for the aggregate fixture:
// each child starts with one usable key.
func aggregateTestSeed() map[string][]string {
	return map[string][]string{
		"child_a": {"child-a-key"},
		"child_b": {"child-b-key"},
	}
}

// mutateTestVendor edits the configuration of one vendor addressed by its
// display name, since vendors are stored as an ordered array.
func mutateTestVendor(t *testing.T, cfg *config.Config, name string, fn func(*config.VendorConfig)) {
	t.Helper()
	idx := cfg.VendorIndexByName(name)
	if idx < 0 {
		t.Fatalf("vendor %q not found in config", name)
	}
	fn(&cfg.Vendors[idx].VendorConfig)
}
