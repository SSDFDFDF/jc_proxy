package keystore

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"jc_proxy/internal/config"
)

type Info struct {
	Driver   string `json:"driver"`
	FilePath string `json:"file_path,omitempty"`
	Table    string `json:"table,omitempty"`
}

const (
	KeyStatusActive         = "active"
	KeyStatusDisabledManual = "disabled_manual"
	KeyStatusDisabledAuto   = "disabled_auto"
)

var (
	ErrKeyNotFound     = errors.New("key not found")
	ErrVersionMismatch = errors.New("key version mismatch")
)

type ConditionalStatusStore interface {
	SetStatusIfVersion(vendor, key string, expectedVersion int64, status, reason, actor string) error
}

type Record struct {
	Key           string     `json:"key"`
	Status        string     `json:"status"`
	DisableReason string     `json:"disable_reason,omitempty"`
	DisabledAt    *time.Time `json:"disabled_at,omitempty"`
	DisabledBy    string     `json:"disabled_by,omitempty"`
	Version       int64      `json:"version"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type Store interface {
	Info() Info
	ListAll() (map[string][]Record, error)
	List(vendor string) ([]Record, error)
	KeyMap() (map[string][]string, error)
	Replace(vendor string, keys []string) error
	Append(vendor string, keys []string) (int, error)
	Delete(vendor string, keys []string) (int, error)
	SetStatus(vendor, key, status, reason, actor string) error
	DeleteVendor(vendor string) error
	Close() error
}

func New(cfg config.UpstreamKeyStoreConfig) (Store, error) {
	switch cfg.Driver {
	case "file":
		return NewFileStore(cfg.FilePath)
	case "pgsql":
		return NewPGStore(cfg.PGSQL)
	default:
		return nil, fmt.Errorf("unsupported upstream key store driver: %s", cfg.Driver)
	}
}

func BootstrapLegacyKeys(store Store, cfgs ...*config.Config) (int, error) {
	if store == nil {
		return 0, errors.New("store is nil")
	}
	current, err := store.KeyMap()
	if err != nil {
		return 0, err
	}

	imported := 0
	for _, cfg := range cfgs {
		if cfg == nil {
			continue
		}
		for vendor, vc := range cfg.Vendors {
			keys := NormalizeKeys(vc.Upstream.Keys)
			if len(keys) == 0 {
				continue
			}
			if len(current[vendor]) > 0 {
				continue
			}
			n, err := store.Append(vendor, keys)
			if err != nil {
				return imported, err
			}
			if n > 0 {
				current[vendor] = append([]string(nil), keys...)
				imported += n
			}
		}
	}
	return imported, nil
}

func NormalizeKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func NormalizeStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "", KeyStatusActive:
		return KeyStatusActive
	case KeyStatusDisabledManual:
		return KeyStatusDisabledManual
	case KeyStatusDisabledAuto:
		return KeyStatusDisabledAuto
	default:
		return KeyStatusActive
	}
}

func IsActiveStatus(status string) bool {
	return NormalizeStatus(status) == KeyStatusActive
}

func NormalizeRecord(record Record) Record {
	record.Key = strings.TrimSpace(record.Key)
	record.Status = NormalizeStatus(record.Status)
	if record.Version < 0 {
		record.Version = 0
	}
	if record.Status == KeyStatusActive {
		record.DisableReason = ""
		record.DisabledAt = nil
		record.DisabledBy = ""
	}
	return record
}

func normalizeVendor(vendor string) string {
	return strings.TrimSpace(vendor)
}

func cloneRecordMap(src map[string][]Record) map[string][]Record {
	dst := make(map[string][]Record, len(src))
	for vendor, records := range src {
		next := append([]Record(nil), records...)
		for i := range next {
			next[i] = NormalizeRecord(next[i])
		}
		sortRecords(next)
		dst[vendor] = next
	}
	return dst
}

func toKeyMap(src map[string][]Record) map[string][]string {
	out := make(map[string][]string, len(src))
	for vendor, records := range src {
		keys := make([]string, 0, len(records))
		for _, record := range records {
			keys = append(keys, record.Key)
		}
		out[vendor] = keys
	}
	return out
}

func sortRecords(records []Record) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].Key < records[j].Key
	})
}

func nextRecordVersion(current int64) int64 {
	if current < 0 {
		current = 0
	}
	return current + 1
}
