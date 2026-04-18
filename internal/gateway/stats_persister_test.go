package gateway

import (
	"net/http"
	"testing"

	"jc_proxy/internal/keystore"
)

func TestBuildRuntimeStatsDeltasPreservesMissingBaselineEntries(t *testing.T) {
	initial := keystore.RuntimeStats{
		TotalRequests: 1,
		SuccessCount:  1,
		LastStatus:    http.StatusOK,
	}
	previous := map[string]map[string]keystore.RuntimeStats{
		"openai": {
			"k1": initial,
		},
	}

	deltas, nextBaseline := buildRuntimeStatsDeltas(map[string]map[string]keystore.RuntimeStats{}, previous)
	if len(deltas) != 0 {
		t.Fatalf("deltas after removal = %#v, want empty", deltas)
	}
	if got := nextBaseline["openai"]["k1"]; !got.Equal(initial) {
		t.Fatalf("baseline after removal = %#v, want %#v", got, initial)
	}

	deltas, nextBaseline = buildRuntimeStatsDeltas(map[string]map[string]keystore.RuntimeStats{
		"openai": {
			"k1": initial,
		},
	}, nextBaseline)
	if len(deltas) != 0 {
		t.Fatalf("deltas after re-add without traffic = %#v, want empty", deltas)
	}

	updated := keystore.RuntimeStats{
		TotalRequests: 2,
		SuccessCount:  2,
		LastStatus:    http.StatusOK,
	}
	deltas, _ = buildRuntimeStatsDeltas(map[string]map[string]keystore.RuntimeStats{
		"openai": {
			"k1": updated,
		},
	}, nextBaseline)

	records := deltas["openai"]
	if len(records) != 1 {
		t.Fatalf("delta records = %#v, want one record", records)
	}
	want := keystore.RuntimeStats{
		TotalRequests: 1,
		SuccessCount:  1,
		LastStatus:    http.StatusOK,
	}
	if records[0].Key != "k1" {
		t.Fatalf("delta key = %q, want k1", records[0].Key)
	}
	if !records[0].RuntimeStats.Equal(want) {
		t.Fatalf("delta stats = %#v, want %#v", records[0].RuntimeStats, want)
	}
}
