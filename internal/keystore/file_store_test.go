package keystore

import (
	"path/filepath"
	"testing"
)

func newTestFileStore(t *testing.T) *FileStore {
	t.Helper()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "upstream_keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestFileStoreReplacePreservesDisabledKeysWhenActiveListCleared(t *testing.T) {
	store := newTestFileStore(t)
	if _, err := store.Append("openai", []string{"k1", "k2"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus("openai", "k2", KeyStatusDisabledManual, "manual", "admin"); err != nil {
		t.Fatal(err)
	}

	if err := store.Replace("openai", nil); err != nil {
		t.Fatal(err)
	}

	records, err := store.List("openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 disabled key to remain, got %d", len(records))
	}
	if records[0].Key != "k2" {
		t.Fatalf("expected remaining key to be k2, got %q", records[0].Key)
	}
	if records[0].Status != KeyStatusDisabledManual {
		t.Fatalf("expected remaining key to stay disabled, got %q", records[0].Status)
	}
}

func TestFileStoreReplaceReactivatesDisabledKeyWithoutDuplicates(t *testing.T) {
	store := newTestFileStore(t)
	if _, err := store.Append("openai", []string{"k1", "k2"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus("openai", "k2", KeyStatusDisabledAuto, "quota", "system:auto"); err != nil {
		t.Fatal(err)
	}

	if err := store.Replace("openai", []string{"k2"}); err != nil {
		t.Fatal(err)
	}

	records, err := store.List("openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one active key after replace, got %d", len(records))
	}
	if records[0].Key != "k2" {
		t.Fatalf("expected k2 to be retained, got %q", records[0].Key)
	}
	if records[0].Status != KeyStatusActive {
		t.Fatalf("expected k2 to be reactivated, got %q", records[0].Status)
	}
}

func TestFileStoreReplacePreservesDisabledKeysAlongsideActiveSelection(t *testing.T) {
	store := newTestFileStore(t)
	if _, err := store.Append("openai", []string{"k1", "k2", "k3"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus("openai", "k3", KeyStatusDisabledAuto, "quota", "system:auto"); err != nil {
		t.Fatal(err)
	}

	if err := store.Replace("openai", []string{"k1"}); err != nil {
		t.Fatal(err)
	}

	records, err := store.List("openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected one active and one disabled key, got %d", len(records))
	}
	if records[0].Key != "k1" || records[0].Status != KeyStatusActive {
		t.Fatalf("unexpected first record: %+v", records[0])
	}
	if records[1].Key != "k3" || records[1].Status != KeyStatusDisabledAuto {
		t.Fatalf("unexpected second record: %+v", records[1])
	}
}
