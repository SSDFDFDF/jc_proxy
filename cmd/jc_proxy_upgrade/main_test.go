package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

const legacyConfigYAML = `server:
  listen: ":8092"
storage:
  config:
    driver: "file"
  upstream_keys:
    driver: "file"
    file_path: "./data/upstream_keys.json"
vendors:
  openai:
    provider: "openai"
    upstream:
      base_url: "https://api.openai.com"
      keys:
        - "sk-inline-legacy"
    load_balance: "round_robin"
  anthropic:
    provider: "anthropic"
    upstream:
      base_url: "https://api.anthropic.com"
    load_balance: "round_robin"
  pool:
    provider: "aggregate"
    load_balance: "round_robin"
    aggregate:
      children:
        - vendor: "openai"
          weight: 2
        - vendor: "anthropic"
`

func legacyMapping(t *testing.T) map[string]string {
	t.Helper()
	mapping, err := resolveVendorIDs([]byte(legacyConfigYAML), nil)
	if err != nil {
		t.Fatalf("resolveVendorIDs() error = %v", err)
	}
	for _, name := range []string{"openai", "anthropic", "pool"} {
		if mapping[name] == "" {
			t.Fatalf("vendor %q was not assigned an id: %#v", name, mapping)
		}
	}
	return mapping
}

func TestMigrateConfigPayloadConvertsVendorsToArray(t *testing.T) {
	mapping := legacyMapping(t)
	out, err := migrateConfigPayload([]byte(legacyConfigYAML), mapping)
	if err != nil {
		t.Fatalf("migrateConfigPayload() error = %v", err)
	}

	// The migrated payload must be readable by the running binary.
	cfg, err := config.LoadBootstrapBytesNoEnv(out)
	if err != nil {
		t.Fatalf("migrated config does not load: %v\n%s", err, out)
	}
	if cfg.SchemaVersion != config.CurrentSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", cfg.SchemaVersion, config.CurrentSchemaVersion)
	}
	if len(cfg.Vendors) != 3 {
		t.Fatalf("vendor count = %d, want 3", len(cfg.Vendors))
	}

	for name, id := range mapping {
		entry, ok := cfg.VendorByID(id)
		if !ok {
			t.Fatalf("vendor id %s missing after migration", id)
		}
		if entry.Name != name {
			t.Fatalf("vendor %s name = %q, want %q", id, entry.Name, name)
		}
	}

	// Aggregate children must now reference ids, keeping weights intact.
	agg, ok := cfg.VendorByID(mapping["pool"])
	if !ok {
		t.Fatal("aggregate vendor missing after migration")
	}
	if len(agg.Aggregate.Children) != 2 {
		t.Fatalf("aggregate children = %#v", agg.Aggregate.Children)
	}
	if agg.Aggregate.Children[0].VendorID != mapping["openai"] {
		t.Fatalf("first child = %q, want %q", agg.Aggregate.Children[0].VendorID, mapping["openai"])
	}
	if agg.Aggregate.Children[0].Weight != 2 {
		t.Fatalf("child weight lost: %d", agg.Aggregate.Children[0].Weight)
	}
	if agg.Aggregate.Children[1].VendorID != mapping["anthropic"] {
		t.Fatalf("second child = %q, want %q", agg.Aggregate.Children[1].VendorID, mapping["anthropic"])
	}

	// Unrelated fields survive, and the retired inline key list is dropped.
	if cfg.Server.Listen != ":8092" {
		t.Fatalf("server.listen = %q", cfg.Server.Listen)
	}
	if strings.Contains(string(out), "sk-inline-legacy") {
		t.Fatalf("inline upstream keys should be dropped, got:\n%s", out)
	}
}

func TestMigrateConfigPayloadIsIdempotent(t *testing.T) {
	mapping := legacyMapping(t)
	first, err := migrateConfigPayload([]byte(legacyConfigYAML), mapping)
	if err != nil {
		t.Fatalf("first migration failed: %v", err)
	}
	second, err := migrateConfigPayload(first, mapping)
	if err != nil {
		t.Fatalf("second migration failed: %v", err)
	}
	var a, b any
	if err := yaml.Unmarshal(first, &a); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(second, &b); err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("re-running the migration changed the payload:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// Re-running against an already migrated payload must reuse the stored ids
// rather than mint new ones, so a partially completed run can be resumed.
func TestResolveVendorIDsReusesExistingIDs(t *testing.T) {
	mapping := legacyMapping(t)
	migrated, err := migrateConfigPayload([]byte(legacyConfigYAML), mapping)
	if err != nil {
		t.Fatal(err)
	}
	again, err := resolveVendorIDs(migrated, nil)
	if err != nil {
		t.Fatalf("resolveVendorIDs() error = %v", err)
	}
	for name, id := range mapping {
		if again[name] != id {
			t.Fatalf("vendor %q id changed on re-run: %q -> %q", name, id, again[name])
		}
	}
}

func TestUpgradeConfigFileWritesBackupAndDryRunChangesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(legacyConfigYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	mapping := legacyMapping(t)

	if err := upgradeConfigFile(path, []byte(legacyConfigYAML), mapping, true); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != legacyConfigYAML {
		t.Fatal("dry run must not modify the config file")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dry run created extra files: %d", len(entries))
	}

	if err := upgradeConfigFile(path, []byte(legacyConfigYAML), mapping, false); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	migrated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	version, err := config.DetectSchemaVersion(migrated)
	if err != nil {
		t.Fatal(err)
	}
	if version != config.CurrentSchemaVersion {
		t.Fatalf("migrated file version = %d", version)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "config.yaml.v1.*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected exactly one backup, got %#v", backups)
	}
	original, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != legacyConfigYAML {
		t.Fatal("backup does not match the original payload")
	}
}

func TestUpgradeKeysFileRepartitionsByVendorID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "upstream_keys.json")
	legacy := map[string]any{
		"vendors": map[string][]keystore.Record{
			"openai": {
				{Key: "k1", Status: keystore.KeyStatusActive, Remark: "primary"},
				{Key: "k2", Status: keystore.KeyStatusDisabledManual, DisableReason: "quota"},
			},
			"anthropic": {
				{Key: "a1", Status: keystore.KeyStatusActive},
			},
		},
	}
	payload, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	mapping := legacyMapping(t)
	if err := upgradeKeysFile(path, mapping, true); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if after, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if string(after) != string(payload) {
		t.Fatal("dry run must not modify the key file")
	}

	if err := upgradeKeysFile(path, mapping, false); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}

	// The migrated file must be readable by the production store, and the
	// records must keep their status, remark and reason.
	store, err := keystore.NewFileStore(path)
	if err != nil {
		t.Fatalf("migrated key file rejected by the store: %v", err)
	}
	defer store.Close()

	records, err := store.List(mapping["openai"])
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("openai records = %#v", records)
	}
	byKey := map[string]keystore.Record{}
	for _, record := range records {
		byKey[record.Key] = record
	}
	if byKey["k1"].Remark != "primary" {
		t.Fatalf("remark lost: %#v", byKey["k1"])
	}
	if byKey["k2"].Status != keystore.KeyStatusDisabledManual || byKey["k2"].DisableReason != "quota" {
		t.Fatalf("disabled state lost: %#v", byKey["k2"])
	}
	if _, err := store.List("openai"); err == nil {
		if leftover, _ := store.List("openai"); len(leftover) != 0 {
			t.Fatal("records are still addressable by the old vendor name")
		}
	}
}

// A v1 key file that the upgrade command has not touched must be refused by the
// running binary instead of being read with names treated as ids.
func TestFileStoreRefusesLegacyKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upstream_keys.json")
	payload := `{"vendors":{"openai":[{"key":"k1","status":"active"}]}}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := keystore.NewFileStore(path); err == nil {
		t.Fatal("expected the store to refuse a v1 key file")
	} else if !strings.Contains(err.Error(), config.UpgradeCommandHint) {
		t.Fatalf("error should point at the upgrade command: %v", err)
	}
}
