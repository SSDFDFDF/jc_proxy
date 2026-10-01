package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"jc_proxy/internal/config"
)

// Read metadata before migration, including the idempotent vendor_id branch:
// never stamp a future schema with this binary's older version.
func keyStoreSchemaVersion(db *sql.DB, metaTable string) (int, error) {
	var exists *string
	if err := db.QueryRow(`SELECT to_regclass($1)::text`, metaTable).Scan(&exists); err != nil {
		return 0, fmt.Errorf("probe key metadata: %w", err)
	}
	if exists == nil {
		return config.LegacySchemaVersion, nil
	}
	var raw string
	err := db.QueryRow(fmt.Sprintf("SELECT meta_value FROM %s WHERE meta_key = 'schema_version'", quoteIdent(metaTable))).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return config.LegacySchemaVersion, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read key schema version: %w", err)
	}
	version, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse key schema version: %w", err)
	}
	return version, nil
}
