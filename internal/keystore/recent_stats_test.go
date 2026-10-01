package keystore

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRecentStatsDeltaIsReplacementAndTimingOnlyPreservesStatus(t *testing.T) {
	prev := RuntimeStats{TotalRequests: 10, SuccessCount: 10, LastStatus: 200, RecentStats: RecentStats{RecentRequests: 5, RecentSuccessCount: 5, HeaderSamples: 5, AvgHeaderMS: 100}}
	next := prev
	next.RecentSuccessCount = 4
	next.AvgHeaderMS = 20
	delta, ok := next.DeltaSince(prev)
	if !ok || delta.IsZero() || delta.TotalRequests != 0 || delta.RecentRequests != 5 {
		t.Fatalf("delta = %+v", delta)
	}
	prev.ApplyDelta(delta)
	if prev != next {
		t.Fatalf("apply = %+v, want %+v", prev, next)
	}
	if delta, ok := next.DeltaSince(prev); !ok || !delta.IsZero() {
		t.Fatalf("unchanged = %+v", delta)
	}
}

func TestRecentStatsFilePersistenceAndReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("vid_test", []string{"k"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		delta := RuntimeStats{TotalRequests: 1, SuccessCount: 1, LastStatus: 200, RecentStats: RecentStats{RecentRequests: 5, RecentSuccessCount: 4, HeaderSamples: 5, AvgHeaderMS: float64(i * 10)}}
		if err := store.ApplyRuntimeStatsDeltas(map[string][]RuntimeStatsDelta{"vid_test": {{Key: "k", RuntimeStats: delta}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Replace("vid_test", []string{"k"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	records, err := reopened.List("vid_test")
	if err != nil {
		t.Fatal(err)
	}
	r := records[0]
	if r.TotalRequests != 2 || r.RecentRequests != 5 || r.AvgHeaderMS != 20 {
		t.Fatalf("stored = %+v", r)
	}
	value, err := r.RecentStats.Value()
	if err != nil {
		t.Fatal(err)
	}
	var decoded RecentStats
	if err := decoded.Scan(value); err != nil {
		t.Fatal(err)
	}
	if decoded != r.RecentStats {
		t.Fatalf("sql codec = %+v", decoded)
	}
	data, err := json.Marshal(r.RuntimeStats)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip RuntimeStats
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip != r.RuntimeStats {
		t.Fatalf("JSON round trip = %+v", roundTrip)
	}
}
