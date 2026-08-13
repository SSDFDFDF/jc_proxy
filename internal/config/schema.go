package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// CurrentSchemaVersion is the layout version of every persisted artifact:
// the config file, the config row in PostgreSQL, and the upstream key store.
//
//	v1 - vendors was a YAML map keyed by the vendor name, and upstream keys,
//	     aggregate children and runtime stats were all partitioned by that
//	     same mutable name.
//	v2 - vendors is an ordered array of {id, name, ...}. Every internal
//	     reference keys off the immutable id, so a vendor can be renamed
//	     without touching stored data.
//
// The running binary never migrates data in place. It detects the stored
// version and refuses to start on anything other than CurrentSchemaVersion,
// pointing the operator at the standalone upgrade command.
const CurrentSchemaVersion = 2

// LegacySchemaVersion is the version assumed when a stored payload carries no
// explicit schema_version field.
const LegacySchemaVersion = 1

// UpgradeCommandHint is the command an operator must run to migrate stored
// data forward. Kept here so every "wrong version" error reads the same.
const UpgradeCommandHint = "jc_proxy_upgrade -config <path>"

// SchemaVersionError reports stored data whose layout the running binary
// cannot read.
type SchemaVersionError struct {
	Source string // human readable origin, e.g. "config file" or "upstream key store"
	Found  int
	Want   int
}

func (e *SchemaVersionError) Error() string {
	if e.Found > e.Want {
		return fmt.Sprintf(
			"%s schema version %d is newer than this binary supports (%d): upgrade jc_proxy",
			e.Source, e.Found, e.Want,
		)
	}
	return fmt.Sprintf(
		"%s schema version %d is older than required (%d): run %q to migrate, then restart",
		e.Source, e.Found, e.Want, UpgradeCommandHint,
	)
}

// NewSchemaVersionError builds a SchemaVersionError for the current binary.
func NewSchemaVersionError(source string, found int) *SchemaVersionError {
	return &SchemaVersionError{Source: source, Found: found, Want: CurrentSchemaVersion}
}

// DetectSchemaVersion reads only the schema_version field out of a YAML
// payload. It deliberately ignores every other field so that a payload in an
// unreadable layout still yields a precise version number instead of a
// confusing unmarshal error.
//
// An empty payload means "nothing stored yet" and reports CurrentSchemaVersion
// so fresh installs start clean.
func DetectSchemaVersion(payload []byte) (int, error) {
	if len(payload) == 0 {
		return CurrentSchemaVersion, nil
	}
	var probe struct {
		SchemaVersion *int `yaml:"schema_version"`
	}
	if err := yaml.Unmarshal(payload, &probe); err != nil {
		return 0, fmt.Errorf("probe config schema version: %w", err)
	}
	if probe.SchemaVersion == nil {
		return LegacySchemaVersion, nil
	}
	return *probe.SchemaVersion, nil
}

// CheckSchemaVersion verifies a stored YAML payload is readable by this binary.
func CheckSchemaVersion(source string, payload []byte) error {
	found, err := DetectSchemaVersion(payload)
	if err != nil {
		return err
	}
	if found != CurrentSchemaVersion {
		return NewSchemaVersionError(source, found)
	}
	return nil
}
