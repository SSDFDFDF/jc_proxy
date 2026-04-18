package keystore

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreApplyRuntimeStatsDeltasDoesNotDoubleCountAfterSaveFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upstream_keys.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("init file store failed: %v", err)
	}
	defer store.Close()

	if _, err := store.Append("openai", []string{"k1"}); err != nil {
		t.Fatalf("append key failed: %v", err)
	}

	deltas := map[string][]RuntimeStatsDelta{
		"openai": {
			{
				Key: "k1",
				RuntimeStats: RuntimeStats{
					TotalRequests: 1,
					SuccessCount:  1,
					LastStatus:    http.StatusOK,
				},
			},
		},
	}

	tmpDir := path + ".tmp"
	if err := os.Mkdir(tmpDir, 0o755); err != nil {
		t.Fatalf("create blocking tmp dir failed: %v", err)
	}
	if err := store.ApplyRuntimeStatsDeltas(deltas); err == nil {
		t.Fatal("expected apply runtime stats delta to fail")
	}

	records, err := store.List("openai")
	if err != nil {
		t.Fatalf("list after failed apply failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count after failed apply = %d, want 1", len(records))
	}
	if got := records[0].TotalRequests; got != 0 {
		t.Fatalf("total_requests after failed apply = %d, want 0", got)
	}
	if got := records[0].SuccessCount; got != 0 {
		t.Fatalf("success_count after failed apply = %d, want 0", got)
	}

	if err := os.Remove(tmpDir); err != nil {
		t.Fatalf("remove blocking tmp dir failed: %v", err)
	}
	if err := store.ApplyRuntimeStatsDeltas(deltas); err != nil {
		t.Fatalf("retry apply runtime stats delta failed: %v", err)
	}

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload file store failed: %v", err)
	}
	defer reloaded.Close()

	records, err = reloaded.List("openai")
	if err != nil {
		t.Fatalf("list after retry failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count after retry = %d, want 1", len(records))
	}
	if got := records[0].TotalRequests; got != 1 {
		t.Fatalf("total_requests after retry = %d, want 1", got)
	}
	if got := records[0].SuccessCount; got != 1 {
		t.Fatalf("success_count after retry = %d, want 1", got)
	}
}
