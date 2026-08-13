package config

import (
	"errors"
	"strings"
	"testing"
)

const v2VendorArrayYAML = `
schema_version: 2
server:
  listen: ":8092"
storage:
  config:
    driver: "file"
  upstream_keys:
    driver: "file"
    file_path: "./data/upstream_keys.json"
vendors:
  - id: "v_primary"
    name: "openai"
    provider: "openai"
    upstream:
      base_url: "https://api.openai.com"
  - id: "v_secondary"
    name: "anthropic"
    provider: "anthropic"
    upstream:
      base_url: "https://api.anthropic.com"
  - id: "v_agg"
    name: "pool"
    provider: "aggregate"
    aggregate:
      children:
        - vendor_id: "v_primary"
        - vendor_id: "v_secondary"
`

func TestLoadBytesReadsVendorArrayAndAggregateIDs(t *testing.T) {
	cfg, err := LoadBytes([]byte(v2VendorArrayYAML))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	if len(cfg.Vendors) != 3 {
		t.Fatalf("vendor count = %d, want 3", len(cfg.Vendors))
	}
	// Configuration order is preserved, unlike the old map layout.
	if cfg.Vendors[0].Name != "openai" || cfg.Vendors[2].Name != "pool" {
		t.Fatalf("vendor order not preserved: %q ... %q", cfg.Vendors[0].Name, cfg.Vendors[2].Name)
	}
	entry, ok := cfg.VendorByID("v_primary")
	if !ok || entry.Name != "openai" {
		t.Fatalf("VendorByID(v_primary) = %#v, %v", entry, ok)
	}
	byName, ok := cfg.VendorByName("anthropic")
	if !ok || byName.ID != "v_secondary" {
		t.Fatalf("VendorByName(anthropic) = %#v, %v", byName, ok)
	}
	agg, _ := cfg.VendorByID("v_agg")
	if len(agg.Aggregate.Children) != 2 || agg.Aggregate.Children[0].VendorID != "v_primary" {
		t.Fatalf("aggregate children = %#v", agg.Aggregate.Children)
	}
}

// A v1 payload keyed vendors by name and carried no schema_version. The running
// binary must refuse it with an actionable message instead of a type error.
func TestLoadBytesRefusesLegacySchema(t *testing.T) {
	legacy := `
server:
  listen: ":8092"
vendors:
  openai:
    upstream:
      base_url: "https://api.openai.com"
`
	_, err := LoadBytes([]byte(legacy))
	if err == nil {
		t.Fatal("expected a schema version error for a v1 payload")
	}
	var versionErr *SchemaVersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("error = %v, want *SchemaVersionError", err)
	}
	if versionErr.Found != LegacySchemaVersion || versionErr.Want != CurrentSchemaVersion {
		t.Fatalf("version error = %+v", versionErr)
	}
	if !strings.Contains(err.Error(), UpgradeCommandHint) {
		t.Fatalf("error should point at the upgrade command, got %q", err.Error())
	}
}

func TestLoadBytesRefusesNewerSchema(t *testing.T) {
	_, err := LoadBytes([]byte("schema_version: 99\nserver:\n  listen: \":8092\"\n"))
	if err == nil {
		t.Fatal("expected a schema version error for a newer payload")
	}
	if !strings.Contains(err.Error(), "newer than this binary supports") {
		t.Fatalf("unexpected error for newer schema: %v", err)
	}
}

func TestDetectSchemaVersion(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    int
	}{
		{"empty means fresh install", "", CurrentSchemaVersion},
		{"missing field means v1", "server:\n  listen: \":1\"\n", LegacySchemaVersion},
		{"explicit version wins", "schema_version: 2\n", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectSchemaVersion([]byte(tc.payload))
			if err != nil {
				t.Fatalf("DetectSchemaVersion() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("DetectSchemaVersion() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestApplyDefaultsMintsMissingVendorID(t *testing.T) {
	cfg, err := LoadBytes([]byte(`
schema_version: 2
server:
  listen: ":8092"
vendors:
  - name: "openai"
    upstream:
      base_url: "https://api.openai.com"
`))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	if cfg.Vendors[0].ID == "" {
		t.Fatal("expected a generated vendor id for a hand written entry")
	}
	if !strings.HasPrefix(cfg.Vendors[0].ID, vendorIDPrefix) {
		t.Fatalf("generated id = %q, want the %q prefix", cfg.Vendors[0].ID, vendorIDPrefix)
	}
}

func TestValidateRejectsDuplicateVendorIDsAndNames(t *testing.T) {
	base := func() *Config {
		return &Config{
			SchemaVersion: CurrentSchemaVersion,
			Server:        ServerConfig{Listen: ":8092"},
			Vendors: []VendorEntry{
				{ID: "v_a", Name: "openai", VendorConfig: VendorConfig{Upstream: UpstreamConfig{BaseURL: "https://a.test"}}},
				{ID: "v_b", Name: "anthropic", VendorConfig: VendorConfig{Upstream: UpstreamConfig{BaseURL: "https://b.test"}}},
			},
		}
	}

	dupID := base()
	dupID.Vendors[1].ID = "v_a"
	if err := dupID.PrepareAndValidate(); err == nil || !strings.Contains(err.Error(), "duplicate vendor id") {
		t.Fatalf("duplicate id error = %v", err)
	}

	dupName := base()
	dupName.Vendors[1].Name = "openai"
	if err := dupName.PrepareAndValidate(); err == nil || !strings.Contains(err.Error(), "duplicate vendor name") {
		t.Fatalf("duplicate name error = %v", err)
	}
}

func TestValidateVendorName(t *testing.T) {
	valid := []string{"openai", "openai-prod", "team.openai", "v2_openai"}
	for _, name := range valid {
		if err := ValidateVendorName(name); err != nil {
			t.Fatalf("ValidateVendorName(%q) = %v, want nil", name, err)
		}
	}
	// Names are request path segments, so anything that breaks routing or
	// collides with an internal route is refused.
	invalid := []string{"", " openai", "open ai", "a/b", "a?b", "a#b", ".", "..", "console", "admin", "healthz", "HEALTHZ"}
	for _, name := range invalid {
		if err := ValidateVendorName(name); err == nil {
			t.Fatalf("ValidateVendorName(%q) = nil, want an error", name)
		}
	}
}

func TestValidateAggregateChildMustReferenceExistingID(t *testing.T) {
	cfg := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Server:        ServerConfig{Listen: ":8092"},
		Vendors: []VendorEntry{
			{ID: "v_child", Name: "openai", VendorConfig: VendorConfig{Upstream: UpstreamConfig{BaseURL: "https://a.test"}}},
			{ID: "v_agg", Name: "pool", VendorConfig: VendorConfig{
				Provider: "aggregate",
				Aggregate: AggregateConfig{Children: []AggregateChild{
					{VendorID: "openai"}, // the name, not the id
				}},
			}},
		},
	}
	if err := cfg.PrepareAndValidate(); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("aggregate child validation error = %v", err)
	}

	cfg.Vendors[1].Aggregate.Children[0].VendorID = "v_child"
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("PrepareAndValidate() error = %v", err)
	}
}

// Renaming is a pure config edit: the id and every id-keyed reference survive a
// full encode/decode round trip.
func TestRenameKeepsIDAndAggregateReferenceThroughEncodeDecode(t *testing.T) {
	cfg, err := LoadBytes([]byte(v2VendorArrayYAML))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	idx := cfg.VendorIndexByName("openai")
	if idx < 0 {
		t.Fatal("vendor openai not found")
	}
	cfg.Vendors[idx].Name = "openai-prod"

	encoded, err := EncodeYAML(cfg)
	if err != nil {
		t.Fatalf("EncodeYAML() error = %v", err)
	}
	reloaded, err := LoadBytes(encoded)
	if err != nil {
		t.Fatalf("reload after rename: %v", err)
	}
	entry, ok := reloaded.VendorByID("v_primary")
	if !ok {
		t.Fatal("vendor id did not survive the rename round trip")
	}
	if entry.Name != "openai-prod" {
		t.Fatalf("name after round trip = %q, want %q", entry.Name, "openai-prod")
	}
	agg, _ := reloaded.VendorByID("v_agg")
	if agg.Aggregate.Children[0].VendorID != "v_primary" {
		t.Fatalf("aggregate child reference broke on rename: %#v", agg.Aggregate.Children)
	}
}

func TestVendorHelpersSetAndDelete(t *testing.T) {
	cfg := &Config{SchemaVersion: CurrentSchemaVersion}
	cfg.SetVendor(VendorEntry{ID: "v_a", Name: "a"})
	cfg.SetVendor(VendorEntry{ID: "v_b", Name: "b"})
	cfg.SetVendor(VendorEntry{ID: "v_a", Name: "a2"})
	if len(cfg.Vendors) != 2 {
		t.Fatalf("vendor count = %d, want 2", len(cfg.Vendors))
	}
	if name, _ := cfg.VendorNameByID("v_a"); name != "a2" {
		t.Fatalf("SetVendor did not replace in place, name = %q", name)
	}
	if !cfg.DeleteVendorByID("v_a") {
		t.Fatal("DeleteVendorByID(v_a) = false")
	}
	if cfg.DeleteVendorByID("v_a") {
		t.Fatal("DeleteVendorByID on a missing id should report false")
	}
	if got := cfg.VendorIDs(); len(got) != 1 || got[0] != "v_b" {
		t.Fatalf("VendorIDs() = %#v", got)
	}
}

func TestLoadStorageOnlyIgnoresUnreadableVendorLayout(t *testing.T) {
	// A v1 payload cannot be loaded normally, but tooling still has to find the
	// databases it must migrate.
	legacy := `
storage:
  config:
    driver: "pgsql"
    pgsql:
      dsn: "postgres://localhost/jc"
  upstream_keys:
    driver: "pgsql"
    pgsql:
      dsn: "postgres://localhost/jc"
vendors:
  openai:
    upstream:
      base_url: "https://api.openai.com"
`
	storage, err := LoadStorageOnly([]byte(legacy))
	if err != nil {
		t.Fatalf("LoadStorageOnly() error = %v", err)
	}
	if storage.Config.Driver != "pgsql" || storage.UpstreamKeys.Driver != "pgsql" {
		t.Fatalf("drivers = %q / %q", storage.Config.Driver, storage.UpstreamKeys.Driver)
	}
	if storage.UpstreamKeys.PGSQL.Table != "jc_proxy_upstream_keys" {
		t.Fatalf("table default not applied: %q", storage.UpstreamKeys.PGSQL.Table)
	}
}
