package keystore

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type controlledConditionalStore struct {
	mu      sync.Mutex
	records map[string][]Record
	started chan struct{}
	release chan struct{}
}

func newControlledConditionalStore() *controlledConditionalStore {
	return &controlledConditionalStore{
		records: map[string][]Record{
			"openai": {
				{Key: "k1", Status: KeyStatusActive, Version: 1},
			},
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *controlledConditionalStore) Info() Info {
	return Info{Driver: "test"}
}

func (s *controlledConditionalStore) ListAll() (map[string][]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRecordMap(s.records), nil
}

func (s *controlledConditionalStore) List(vendor string) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records[normalizeVendor(vendor)]...), nil
}

func (s *controlledConditionalStore) KeyMap() (map[string][]string, error) {
	all, err := s.ListAll()
	if err != nil {
		return nil, err
	}
	return toKeyMap(all), nil
}

func (s *controlledConditionalStore) Replace(vendor string, keys []string) error {
	return nil
}

func (s *controlledConditionalStore) Append(vendor string, keys []string) (int, error) {
	return 0, nil
}

func (s *controlledConditionalStore) Delete(vendor string, keys []string) (int, error) {
	return 0, nil
}

func (s *controlledConditionalStore) SetStatus(vendor, key, status, reason, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return updateControlledRecord(s.records, vendor, key, -1, false, status, reason, actor)
}

func (s *controlledConditionalStore) SetStatusIfVersion(vendor, key string, expectedVersion int64, status, reason, actor string) error {
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	<-s.release

	s.mu.Lock()
	defer s.mu.Unlock()
	return updateControlledRecord(s.records, vendor, key, expectedVersion, true, status, reason, actor)
}

func (s *controlledConditionalStore) Close() error {
	return nil
}

func (s *controlledConditionalStore) DeleteVendor(vendor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, normalizeVendor(vendor))
	return nil
}

func updateControlledRecord(records map[string][]Record, vendor, key string, expectedVersion int64, checkVersion bool, status, reason, actor string) error {
	vendor = normalizeVendor(vendor)
	key = strings.TrimSpace(key)
	current := records[vendor]
	for i := range current {
		if current[i].Key != key {
			continue
		}
		if checkVersion && current[i].Version != expectedVersion {
			return ErrVersionMismatch
		}
		now := time.Now().UTC()
		current[i].Status = NormalizeStatus(status)
		current[i].Version = nextRecordVersion(current[i].Version)
		current[i].UpdatedAt = now
		if current[i].Status == KeyStatusActive {
			current[i].DisableReason = ""
			current[i].DisabledAt = nil
			current[i].DisabledBy = ""
		} else {
			current[i].DisableReason = strings.TrimSpace(reason)
			current[i].DisabledBy = strings.TrimSpace(actor)
			current[i].DisabledAt = &now
		}
		records[vendor] = current
		return nil
	}
	return ErrKeyNotFound
}

func TestAsyncStatusStoreListsPendingAutoDisable(t *testing.T) {
	base := newControlledConditionalStore()
	var releaseOnce sync.Once
	store, err := NewAsyncStatusStore(base, AsyncStatusStoreOptions{
		SetStatusTimeout: time.Second,
		ErrorHandler:     func(error) {},
	})
	if err != nil {
		t.Fatalf("init async status store failed: %v", err)
	}
	defer func() {
		releaseOnce.Do(func() { close(base.release) })
		_ = store.Close()
	}()

	if err := store.SetStatusIfVersion("openai", "k1", 1, KeyStatusDisabledAuto, "quota", "system:auto"); err != nil {
		t.Fatalf("enqueue async disable failed: %v", err)
	}

	select {
	case <-base.started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected async worker to start")
	}

	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("list all failed: %v", err)
	}
	record := all["openai"][0]
	if record.Status != KeyStatusDisabledAuto {
		t.Fatalf("pending status = %q, want %q", record.Status, KeyStatusDisabledAuto)
	}
	if record.Version != 2 {
		t.Fatalf("pending version = %d, want 2", record.Version)
	}
}

func TestAsyncStatusStoreSyncStatusClearsPendingAndPreservesNewerVersion(t *testing.T) {
	base := newControlledConditionalStore()
	var releaseOnce sync.Once
	store, err := NewAsyncStatusStore(base, AsyncStatusStoreOptions{
		SetStatusTimeout: time.Second,
		ErrorHandler:     func(error) {},
	})
	if err != nil {
		t.Fatalf("init async status store failed: %v", err)
	}
	defer func() {
		releaseOnce.Do(func() { close(base.release) })
		_ = store.Close()
	}()

	if err := store.SetStatusIfVersion("openai", "k1", 1, KeyStatusDisabledAuto, "quota", "system:auto"); err != nil {
		t.Fatalf("enqueue async disable failed: %v", err)
	}

	select {
	case <-base.started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected async worker to start")
	}

	if err := store.SetStatus("openai", "k1", KeyStatusActive, "", "admin"); err != nil {
		t.Fatalf("sync enable failed: %v", err)
	}

	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("list all after sync enable failed: %v", err)
	}
	record := all["openai"][0]
	if record.Status != KeyStatusActive {
		t.Fatalf("status after sync enable = %q, want %q", record.Status, KeyStatusActive)
	}
	if record.Version != 2 {
		t.Fatalf("version after sync enable = %d, want 2", record.Version)
	}

	releaseOnce.Do(func() { close(base.release) })

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		baseAll, err := base.ListAll()
		if err != nil {
			t.Fatalf("list base store failed: %v", err)
		}
		baseRecord := baseAll["openai"][0]
		if baseRecord.Status == KeyStatusActive && baseRecord.Version == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stale async update overwrote newer sync state")
}

func TestAsyncStatusStoreListSkipsStalePendingWhenBaseVersionIsNewer(t *testing.T) {
	base := newControlledConditionalStore()
	store, err := NewAsyncStatusStore(base, AsyncStatusStoreOptions{
		SetStatusTimeout: time.Second,
		ErrorHandler:     func(error) {},
	})
	if err != nil {
		t.Fatalf("init async status store failed: %v", err)
	}
	defer func() {
		close(base.release)
		_ = store.Close()
	}()

	if err := store.SetStatusIfVersion("openai", "k1", 1, KeyStatusDisabledAuto, "quota", "system:auto"); err != nil {
		t.Fatalf("enqueue async disable failed: %v", err)
	}

	select {
	case <-base.started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected async worker to start")
	}

	base.mu.Lock()
	base.records["openai"][0].Status = KeyStatusActive
	base.records["openai"][0].Version = 3
	base.mu.Unlock()

	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("list all failed: %v", err)
	}
	record := all["openai"][0]
	if record.Status != KeyStatusActive {
		t.Fatalf("stale pending should not override newer base status, got %q", record.Status)
	}
	if record.Version != 3 {
		t.Fatalf("stale pending should not lower base version, got %d", record.Version)
	}
}

func TestAsyncStatusStoreListDoesNotRecreateMissingKeyFromPending(t *testing.T) {
	base := newControlledConditionalStore()
	store, err := NewAsyncStatusStore(base, AsyncStatusStoreOptions{
		SetStatusTimeout: time.Second,
		ErrorHandler:     func(error) {},
	})
	if err != nil {
		t.Fatalf("init async status store failed: %v", err)
	}
	defer func() {
		close(base.release)
		_ = store.Close()
	}()

	if err := store.SetStatusIfVersion("openai", "k1", 1, KeyStatusDisabledAuto, "quota", "system:auto"); err != nil {
		t.Fatalf("enqueue async disable failed: %v", err)
	}

	select {
	case <-base.started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected async worker to start")
	}

	base.mu.Lock()
	delete(base.records, "openai")
	base.mu.Unlock()

	all, err := store.ListAll()
	if err != nil {
		t.Fatalf("list all failed: %v", err)
	}
	if _, ok := all["openai"]; ok {
		t.Fatal("stale pending should not recreate deleted key")
	}
}
