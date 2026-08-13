package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// vendorIDPrefix marks generated vendor identifiers. Hand written identifiers
// are not required to use it.
const vendorIDPrefix = "v_"

// reservedVendorNames cannot be used as a vendor name. The gateway resolves the
// first path segment of an incoming request to a vendor name, and these
// segments are already claimed by internal routes (see gateway.Router).
var reservedVendorNames = map[string]struct{}{
	"healthz": {},
	"console": {},
	"admin":   {},
}

// VendorEntry is one configured vendor.
//
// ID is immutable and is what every stored reference uses: upstream key
// partitions, aggregate children and runtime statistics. Name is mutable and
// is only a display label plus the request path segment clients call, so
// renaming a vendor changes its public URL and nothing else.
type VendorEntry struct {
	ID           string `yaml:"id" json:"id"`
	Name         string `yaml:"name" json:"name"`
	VendorConfig `yaml:",inline"`
}

// NewVendorID returns a fresh opaque vendor identifier. It is deliberately not
// derived from the name, otherwise renaming would change the identifier and
// defeat the whole point of having one.
func NewVendorID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate vendor id: %w", err)
	}
	return vendorIDPrefix + hex.EncodeToString(buf), nil
}

// ValidateVendorID rejects identifiers that cannot be embedded in an admin API
// path or used as a storage partition key.
func ValidateVendorID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("vendor id is required")
	}
	if len(id) > 64 {
		return errors.New("vendor id must be at most 64 characters")
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return fmt.Errorf("vendor id %q may only contain letters, digits, '_' and '-'", id)
		}
	}
	return nil
}

// ValidateVendorName rejects names that cannot serve as a single request path
// segment. The rule set is deliberately permissive about character sets so that
// existing deployments keep working after the v2 upgrade; it only refuses what
// actually breaks routing.
func ValidateVendorName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errors.New("vendor name is required")
	}
	if trimmed != name {
		return fmt.Errorf("vendor name %q must not have leading or trailing whitespace", name)
	}
	if len(name) > 128 {
		return errors.New("vendor name must be at most 128 characters")
	}
	if strings.ContainsAny(name, "/?#%\\") {
		return fmt.Errorf("vendor name %q must not contain any of / ? # %% \\", name)
	}
	if strings.ContainsAny(name, " \t\r\n") {
		return fmt.Errorf("vendor name %q must not contain whitespace", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("vendor name %q is not a valid path segment", name)
	}
	if _, reserved := reservedVendorNames[strings.ToLower(name)]; reserved {
		return fmt.Errorf("vendor name %q is reserved by an internal route", name)
	}
	return nil
}

// ReservedVendorNames lists the path segments a vendor name cannot use.
func ReservedVendorNames() []string {
	out := make([]string, 0, len(reservedVendorNames))
	for name := range reservedVendorNames {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// VendorIndexByID returns the slice index of a vendor, or -1.
func (c *Config) VendorIndexByID(id string) int {
	if c == nil {
		return -1
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return -1
	}
	for i := range c.Vendors {
		if c.Vendors[i].ID == id {
			return i
		}
	}
	return -1
}

// VendorIndexByName returns the slice index of a vendor, or -1.
func (c *Config) VendorIndexByName(name string) int {
	if c == nil {
		return -1
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return -1
	}
	for i := range c.Vendors {
		if c.Vendors[i].Name == name {
			return i
		}
	}
	return -1
}

// VendorByID returns a copy of the vendor with the given id.
func (c *Config) VendorByID(id string) (VendorEntry, bool) {
	idx := c.VendorIndexByID(id)
	if idx < 0 {
		return VendorEntry{}, false
	}
	return c.Vendors[idx], true
}

// VendorByName returns a copy of the vendor with the given name.
func (c *Config) VendorByName(name string) (VendorEntry, bool) {
	idx := c.VendorIndexByName(name)
	if idx < 0 {
		return VendorEntry{}, false
	}
	return c.Vendors[idx], true
}

// SetVendor replaces the entry with a matching id, or appends it.
func (c *Config) SetVendor(entry VendorEntry) {
	if c == nil {
		return
	}
	if idx := c.VendorIndexByID(entry.ID); idx >= 0 {
		c.Vendors[idx] = entry
		return
	}
	c.Vendors = append(c.Vendors, entry)
}

// DeleteVendorByID removes a vendor, preserving the order of the rest.
func (c *Config) DeleteVendorByID(id string) bool {
	idx := c.VendorIndexByID(id)
	if idx < 0 {
		return false
	}
	c.Vendors = append(c.Vendors[:idx], c.Vendors[idx+1:]...)
	return true
}

// VendorIDs returns every configured vendor id in configuration order.
func (c *Config) VendorIDs() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Vendors))
	for i := range c.Vendors {
		out = append(out, c.Vendors[i].ID)
	}
	return out
}

// VendorNameByID resolves an id to its current display/route name.
func (c *Config) VendorNameByID(id string) (string, bool) {
	idx := c.VendorIndexByID(id)
	if idx < 0 {
		return "", false
	}
	return c.Vendors[idx].Name, true
}

// VendorsFromMap builds an ordered vendor list out of a name-keyed map. It is
// a test-only convenience: the deterministic `vid_<name>` ids intentionally
// differ from the name so tests that confuse the two fail loudly, but they are
// not safe for real data — a name containing dots or non-ASCII characters
// yields an id that ValidateVendorID rejects. Production code and the upgrade
// command both mint opaque ids with NewVendorID.
func VendorsFromMap(vendors map[string]VendorConfig) []VendorEntry {
	names := make([]string, 0, len(vendors))
	for name := range vendors {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]VendorEntry, 0, len(names))
	for _, name := range names {
		out = append(out, VendorEntry{
			ID:           "vid_" + name,
			Name:         name,
			VendorConfig: vendors[name],
		})
	}
	return out
}
