package keystore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type FileStore struct {
	path string
	mu   sync.RWMutex
	data map[string][]Record
}

type fileSnapshot struct {
	Vendors map[string][]Record `json:"vendors"`
}

func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("upstream key file path required")
	}
	fs := &FileStore{
		path: path,
		data: map[string][]Record{},
	}
	if err := fs.load(); err != nil {
		return nil, err
	}
	return fs, nil
}

func (s *FileStore) Info() Info {
	return Info{Driver: "file", FilePath: s.path}
}

func (s *FileStore) ListAll() (map[string][]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRecordMap(s.data), nil
}

func (s *FileStore) List(vendor string) ([]Record, error) {
	vendor = normalizeVendor(vendor)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]Record(nil), s.data[vendor]...)
	for i := range out {
		out[i] = NormalizeRecord(out[i])
	}
	return out, nil
}

func (s *FileStore) KeyMap() (map[string][]string, error) {
	all, err := s.ListAll()
	if err != nil {
		return nil, err
	}
	return toKeyMap(all), nil
}

func (s *FileStore) Replace(vendor string, keys []string) error {
	vendor = normalizeVendor(vendor)
	if vendor == "" {
		return errors.New("vendor is required")
	}
	keys = NormalizeKeys(keys)
	now := time.Now().UTC()
	selected := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		selected[key] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existingIndex := make(map[string]Record, len(s.data[vendor]))
	for _, record := range s.data[vendor] {
		existingIndex[record.Key] = record
	}

	if len(keys) == 0 {
		disabled := make([]Record, 0, len(s.data[vendor]))
		for _, record := range s.data[vendor] {
			record = NormalizeRecord(record)
			if !IsActiveStatus(record.Status) {
				disabled = append(disabled, record)
			}
		}
		if len(disabled) == 0 {
			delete(s.data, vendor)
		} else {
			sortRecords(disabled)
			s.data[vendor] = disabled
		}
		return s.saveLocked()
	}

	next := make([]Record, 0, len(keys))
	for _, key := range keys {
		if record, ok := existingIndex[key]; ok {
			record.Status = KeyStatusActive
			record.DisableReason = ""
			record.DisabledAt = nil
			record.DisabledBy = ""
			record.Version = nextRecordVersion(record.Version)
			record.UpdatedAt = now
			next = append(next, record)
			continue
		}
		next = append(next, Record{
			Key:       key,
			Status:    KeyStatusActive,
			Version:   1,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	for _, record := range s.data[vendor] {
		if _, ok := selected[record.Key]; ok {
			continue
		}
		if !IsActiveStatus(record.Status) {
			next = append(next, NormalizeRecord(record))
		}
	}
	sortRecords(next)
	s.data[vendor] = next
	return s.saveLocked()
}

func (s *FileStore) Append(vendor string, keys []string) (int, error) {
	vendor = normalizeVendor(vendor)
	if vendor == "" {
		return 0, errors.New("vendor is required")
	}
	keys = NormalizeKeys(keys)
	if len(keys) == 0 {
		return 0, nil
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := make(map[string]struct{}, len(s.data[vendor]))
	for _, record := range s.data[vendor] {
		existing[record.Key] = struct{}{}
	}

	added := 0
	next := append([]Record(nil), s.data[vendor]...)
	for _, key := range keys {
		if _, ok := existing[key]; ok {
			continue
		}
		existing[key] = struct{}{}
		next = append(next, Record{
			Key:       key,
			Status:    KeyStatusActive,
			Version:   1,
			CreatedAt: now,
			UpdatedAt: now,
		})
		added++
	}
	if added == 0 {
		return 0, nil
	}
	sortRecords(next)
	s.data[vendor] = next
	return added, s.saveLocked()
}

func (s *FileStore) Delete(vendor string, keys []string) (int, error) {
	vendor = normalizeVendor(vendor)
	if vendor == "" {
		return 0, errors.New("vendor is required")
	}
	keys = NormalizeKeys(keys)
	if len(keys) == 0 {
		return 0, nil
	}

	removeSet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		removeSet[key] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.data[vendor]
	if len(current) == 0 {
		return 0, nil
	}
	next := make([]Record, 0, len(current))
	removed := 0
	for _, record := range current {
		if _, ok := removeSet[record.Key]; ok {
			removed++
			continue
		}
		next = append(next, record)
	}
	if removed == 0 {
		return 0, nil
	}
	if len(next) == 0 {
		delete(s.data, vendor)
	} else {
		s.data[vendor] = next
	}
	return removed, s.saveLocked()
}

func (s *FileStore) SetStatus(vendor, key, status, reason, actor string) error {
	return s.setStatus(vendor, key, -1, false, status, reason, actor)
}

func (s *FileStore) SetStatusIfVersion(vendor, key string, expectedVersion int64, status, reason, actor string) error {
	return s.setStatus(vendor, key, expectedVersion, true, status, reason, actor)
}

func (s *FileStore) setStatus(vendor, key string, expectedVersion int64, checkVersion bool, status, reason, actor string) error {
	vendor = normalizeVendor(vendor)
	key = strings.TrimSpace(key)
	if vendor == "" {
		return errors.New("vendor is required")
	}
	if key == "" {
		return errors.New("key is required")
	}

	status = NormalizeStatus(status)
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.data[vendor]
	if len(current) == 0 {
		return ErrKeyNotFound
	}
	found := false
	for i := range current {
		if current[i].Key != key {
			continue
		}
		found = true
		if checkVersion && current[i].Version != expectedVersion {
			return ErrVersionMismatch
		}
		current[i].Status = status
		current[i].Version = nextRecordVersion(current[i].Version)
		current[i].UpdatedAt = now
		if status == KeyStatusActive {
			current[i].DisableReason = ""
			current[i].DisabledAt = nil
			current[i].DisabledBy = ""
		} else {
			current[i].DisableReason = strings.TrimSpace(reason)
			current[i].DisabledAt = &now
			current[i].DisabledBy = strings.TrimSpace(actor)
		}
		current[i] = NormalizeRecord(current[i])
		break
	}
	if !found {
		return ErrKeyNotFound
	}
	s.data[vendor] = current
	return s.saveLocked()
}

func (s *FileStore) DeleteVendor(vendor string) error {
	vendor = normalizeVendor(vendor)
	if vendor == "" {
		return errors.New("vendor is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, vendor)
	return s.saveLocked()
}

func (s *FileStore) Close() error {
	return nil
}

func (s *FileStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read upstream key file: %w", err)
	}

	var snap fileSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("parse upstream key file: %w", err)
	}
	if snap.Vendors == nil {
		s.data = map[string][]Record{}
		return nil
	}
	for vendor, records := range snap.Vendors {
		for i := range records {
			records[i] = NormalizeRecord(records[i])
		}
		sortRecords(records)
		snap.Vendors[vendor] = records
	}
	s.data = snap.Vendors
	return nil
}

func (s *FileStore) saveLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir upstream key dir: %w", err)
	}

	snap := fileSnapshot{Vendors: cloneRecordMap(s.data)}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal upstream key file: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write upstream key tmp file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace upstream key file: %w", err)
	}
	return nil
}
