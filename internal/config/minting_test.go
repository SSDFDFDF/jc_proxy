package config

import (
	"strings"
	"testing"
)

const mintingConfigYAML = `schema_version: 2
vendors:
  - name: "openai"
    provider: "openai"
    upstream:
      base_url: "https://api.openai.com"
`

func TestLoadMintsMissingVendorIDs(t *testing.T) {
	cfg, err := LoadBootstrapBytesNoEnv([]byte(mintingConfigYAML))
	if err != nil {
		t.Fatalf("LoadBootstrapBytesNoEnv() error = %v", err)
	}
	if !cfg.MintedVendorIDs() {
		t.Fatal("MintedVendorIDs() = false, want true for a payload without ids")
	}
	if len(cfg.Vendors) != 1 {
		t.Fatalf("vendor count = %d, want 1", len(cfg.Vendors))
	}
	id := cfg.Vendors[0].ID
	if err := ValidateVendorID(id); err != nil {
		t.Fatalf("minted id %q is invalid: %v", id, err)
	}
	if !strings.HasPrefix(id, "v_") {
		t.Fatalf("minted id %q should carry the generated prefix", id)
	}

	// A clone keeps the id values but must not itself report minting; the
	// flag belongs to the load that actually invented the ids.
	clone, err := cfg.Clone()
	if err != nil {
		t.Fatalf("Clone() error = %v", err)
	}
	if clone.MintedVendorIDs() {
		t.Fatal("a clone must not report minted ids")
	}
	if clone.Vendors[0].ID != id {
		t.Fatalf("clone id = %q, want %q", clone.Vendors[0].ID, id)
	}
}

func TestLoadKeepsExplicitVendorIDs(t *testing.T) {
	payload := `schema_version: 2
vendors:
  - id: "vid_custom"
    name: "openai"
    provider: "openai"
    upstream:
      base_url: "https://api.openai.com"
`
	cfg, err := LoadBootstrapBytesNoEnv([]byte(payload))
	if err != nil {
		t.Fatalf("LoadBootstrapBytesNoEnv() error = %v", err)
	}
	if cfg.MintedVendorIDs() {
		t.Fatal("MintedVendorIDs() = true for a payload that already carries ids")
	}
	if cfg.Vendors[0].ID != "vid_custom" {
		t.Fatalf("id = %q, want vid_custom", cfg.Vendors[0].ID)
	}
}
