package keystore

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type auditTransientStatusStore struct {
	*controlledConditionalStore
	fail atomic.Bool
}

func (s *auditTransientStatusStore) SetStatusIfVersion(vendor, key string, version int64, status, reason, actor string) error {
	if s.fail.Load() {
		return errors.New("temporary storage failure")
	}
	return s.controlledConditionalStore.SetStatusIfVersion(vendor, key, version, status, reason, actor)
}
func TestDuplicateAppendPreservesPendingDisable(t *testing.T) {
	for _, keys := range [][]string{{"k1"}, {"k1", "new-key"}} {
		t.Run(keys[len(keys)-1], func(t *testing.T) {
			base := &auditTransientStatusStore{controlledConditionalStore: newControlledConditionalStore()}
			close(base.release)
			base.fail.Store(true)
			failed := make(chan struct{}, 1)
			store, err := NewAsyncStatusStore(base, AsyncStatusStoreOptions{ErrorHandler: func(error) {
				select {
				case failed <- struct{}{}:
				default:
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.SetStatusIfVersion("openai", "k1", 1, KeyStatusDisabledAuto, "invalid key", "system:auto"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-failed:
			case <-time.After(time.Second):
				t.Fatal("worker did not try persistence")
			}
			before := len(store.pendingSnapshot())
			added, err := store.Append("openai", keys)
			if err != nil {
				t.Fatal(err)
			}
			after := len(store.pendingSnapshot())
			if added != len(keys)-1 || before != 1 || after != 1 {
				t.Errorf("added=%d pending=%d -> %d, want added=%d and pending=1 -> 1", added, before, after, len(keys)-1)
			}
			base.fail.Store(false)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			records, _ := base.List("openai")
			t.Logf("input=%v added=%d pending=%d -> %d final=%s", keys, added, before, after, records[0].Status)
			if records[0].Status != KeyStatusDisabledAuto {
				t.Error("append silently discarded auto-disable intent without persistence")
			}
		})
	}
}
