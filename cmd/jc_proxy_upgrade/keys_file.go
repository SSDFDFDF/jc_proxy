package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

// fileKeySnapshot mirrors the on-disk shape of the file-backed key store for
// both layouts: v1 had no schema_version and keyed vendors by name, v2 records
// the version and keys by vendor id.
type fileKeySnapshot struct {
	SchemaVersion int                          `json:"schema_version"`
	Vendors       map[string][]keystore.Record `json:"vendors"`
}

func upgradeKeysFile(path string, mapping map[string]string, dryRun bool) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("upstream key file %s does not exist; the server will create it at v%d", path, config.CurrentSchemaVersion)
			return nil
		}
		return fmt.Errorf("read upstream key file: %w", err)
	}

	var snap fileKeySnapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return fmt.Errorf("parse upstream key file: %w", err)
	}
	if snap.SchemaVersion == 0 {
		// A file written before version tracking existed is v1 by definition.
		snap.SchemaVersion = config.LegacySchemaVersion
	}
	if snap.SchemaVersion == config.CurrentSchemaVersion {
		log.Printf("upstream key file %s already at schema v%d", path, snap.SchemaVersion)
		return nil
	}
	if snap.SchemaVersion > config.CurrentSchemaVersion {
		return config.NewSchemaVersionError("upstream key file "+path, snap.SchemaVersion)
	}

	next := make(map[string][]keystore.Record, len(snap.Vendors))
	for name, records := range snap.Vendors {
		id, ok := mapping[name]
		if !ok {
			// Keep orphan partitions rather than dropping key material.
			generated, err := config.NewVendorID()
			if err != nil {
				return err
			}
			mapping[name] = generated
			id = generated
			log.Printf("orphan key partition %q mapped to %s (no matching vendor in config)", name, id)
		}
		if _, clash := next[id]; clash {
			return fmt.Errorf("vendor id %s would receive two key partitions", id)
		}
		next[id] = records
	}

	log.Printf("upstream key file %s: %d vendor partition(s) to remap", path, len(next))
	if dryRun {
		log.Printf("would rewrite upstream key file %s from v%d to v%d", path, snap.SchemaVersion, config.CurrentSchemaVersion)
		return nil
	}

	backup := fmt.Sprintf("%s.v%d.%s.bak", path, snap.SchemaVersion, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.WriteFile(backup, payload, 0o600); err != nil {
		return fmt.Errorf("write upstream key backup: %w", err)
	}
	log.Printf("upstream key backup written to %s", backup)

	out, err := json.MarshalIndent(fileKeySnapshot{
		SchemaVersion: config.CurrentSchemaVersion,
		Vendors:       next,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal upstream key file: %w", err)
	}
	tmp := path + ".upgrade.tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write migrated upstream key file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace upstream key file: %w", err)
	}
	log.Printf("upstream key file %s migrated to v%d", path, config.CurrentSchemaVersion)
	return nil
}
