package keystore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileStoreResetRuntimeStats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initial := RuntimeStats{
		TotalRequests: 10, SuccessCount: 6, LastStatus: 429, LastError: "limited",
		UnauthorizedCount: 1, ForbiddenCount: 1, RateLimitCount: 1, OtherErrorCount: 1,
		RecentStats: RecentStats{RecentRequests: 5, RecentSuccessCount: 3,
			HeaderSamples: 5, AvgHeaderMS: 12, ResponseSamples: 4, AvgResponseMS: 50},
	}
	for _, id := range []string{"a", "b", "orphan"} {
		if _, err := store.Append(id, []string{"key"}); err != nil {
			t.Fatal(err)
		}
		if err := store.ApplyRuntimeStatsDeltas(map[string][]RuntimeStatsDelta{id: {{Key: "key", RuntimeStats: initial}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetRemark("a", "key", "keep this"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus("a", "key", KeyStatusDisabledManual, "do not enable", "admin"); err != nil {
		t.Fatal(err)
	}
	before, _ := store.ListAll()

	// A failed atomic file replacement must not publish the reset in memory.
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResetRuntimeStats("a"); err == nil {
		t.Fatal("reset unexpectedly succeeded")
	}
	after, _ := store.ListAll()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed reset changed records")
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		count, err := store.ResetRuntimeStats(" a ")
		if err != nil || count != 1 {
			t.Fatalf("reset = %d, %v", count, err)
		}
	}
	after, _ = store.ListAll()
	want := before["a"][0]
	want.RuntimeStats = RuntimeStats{}
	want.UpdatedAt = after["a"][0].UpdatedAt
	if !reflect.DeepEqual(want, after["a"][0]) {
		t.Fatalf("reset changed key metadata: %#v", after["a"][0])
	}
	if !reflect.DeepEqual(before["b"], after["b"]) || !reflect.DeepEqual(before["orphan"], after["orphan"]) {
		t.Fatal("vendor reset touched other partitions")
	}
	if count, err := store.ResetRuntimeStats("missing"); err != nil || count != 0 {
		t.Fatalf("missing partition = %d, %v", count, err)
	}
	if count, err := store.ResetRuntimeStats(""); err != nil || count != 3 {
		t.Fatalf("global reset = %d, %v", count, err)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	all, _ := reopened.ListAll()
	for id, records := range all {
		if records[0].RuntimeStats != (RuntimeStats{}) {
			t.Fatalf("%s restored old stats", id)
		}
	}
	if err := reopened.ApplyRuntimeStatsDeltas(map[string][]RuntimeStatsDelta{
		"a": {{Key: "key", RuntimeStats: RuntimeStats{TotalRequests: 1, SuccessCount: 1}}},
	}); err != nil {
		t.Fatal(err)
	}
	records, _ := reopened.List("a")
	if records[0].TotalRequests != 1 || records[0].SuccessCount != 1 || records[0].RecentRequests != 0 {
		t.Fatal("new period did not start at zero")
	}
}

func TestAsyncStatusStoreForwardsStatsReset(t *testing.T) {
	base, err := NewFileStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.Append("v", []string{"key"}); err != nil {
		t.Fatal(err)
	}
	store, err := NewAsyncStatusStore(base, AsyncStatusStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.ApplyRuntimeStatsDeltas(map[string][]RuntimeStatsDelta{
		"v": {{Key: "key", RuntimeStats: RuntimeStats{TotalRequests: 1, SuccessCount: 1}}},
	}); err != nil {
		t.Fatal(err)
	}
	if count, err := store.ResetRuntimeStats("v"); err != nil || count != 1 {
		t.Fatalf("reset = %d, %v", count, err)
	}
	records, _ := store.List("v")
	if records[0].RuntimeStats != (RuntimeStats{}) {
		t.Fatal("wrapper did not reset base store")
	}
}
