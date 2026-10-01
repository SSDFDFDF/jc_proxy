package keystore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileMutationsPublishOnlyAfterSuccessfulSave(t *testing.T) {
	for _, operation := range []string{"append", "replace", "clear", "delete", "delete-vendor", "status", "conditional-status", "remark"} {
		t.Run(operation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "keys.json")
			store, err := NewFileStore(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Append("v", []string{"key"}); err != nil {
				t.Fatal(err)
			}
			before, _ := store.ListAll()
			version := before["v"][0].Version
			mutate := func() error {
				switch operation {
				case "append":
					_, err := store.Append("v", []string{"new"})
					return err
				case "replace":
					return store.Replace("v", []string{"new"})
				case "clear":
					return store.Replace("v", nil)
				case "delete":
					_, err := store.Delete("v", []string{"key"})
					return err
				case "delete-vendor":
					return store.DeleteVendor("v")
				case "status":
					return store.SetStatus("v", "key", KeyStatusDisabledManual, "test", "admin")
				case "conditional-status":
					return store.SetStatusIfVersion("v", "key", version, KeyStatusDisabledAuto, "test", "auto")
				default:
					return store.SetRemark("v", "key", "remark")
				}
			}
			if err := os.Mkdir(path+".tmp", 0700); err != nil {
				t.Fatal(err)
			}
			if err := mutate(); err == nil {
				t.Fatal("expected injected file write failure")
			}
			after, _ := store.ListAll()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed save mutated memory/version")
			}
			disk, err := NewFileStore(path)
			if err != nil {
				t.Fatal(err)
			}
			diskRecords, _ := disk.ListAll()
			if !reflect.DeepEqual(before, diskRecords) {
				t.Fatal("failed save mutated disk")
			}
			if err := os.Remove(path + ".tmp"); err != nil {
				t.Fatal(err)
			}
			if err := mutate(); err != nil {
				t.Fatalf("retry failed: %v", err)
			}
		})
	}
}
