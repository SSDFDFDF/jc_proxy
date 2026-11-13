package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"jc_proxy/internal/config"
	"jc_proxy/internal/gateway"
)

func TestAdminPluginStats(t *testing.T) {
	h := makeHandlerForTest(t)
	cfg, err := h.service.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	id := cfg.Vendors[0].ID
	cfg.Vendors[0].Plugins = []config.PluginConfig{{Name: "history", Builtin: "codex-history-filter"}}
	cfg.Vendors[0].ClientAuth.Enabled = new(false)
	cfg.Vendors[0].ClientAuth.Keys = []string{"client-key"}
	if err := h.service.runtime.Update(cfg); err != nil {
		t.Fatal(err)
	}
	// Reject locally, so this integration test needs no upstream network.
	w := httptest.NewRecorder()
	h.service.runtime.Snapshot().ServeHTTP(w, httptest.NewRequest("POST", "/openai/responses", strings.NewReader(`{"previous_response_id":"private"}`)))
	if w.Code != 400 {
		t.Fatalf("request: %d %s", w.Code, w.Body)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	token := loginAdminToken(t, mux)
	for _, tc := range []struct {
		token, query string
		status       int
		count        int
	}{
		{"", "", 401, 0},
		{token, "", 200, 1},
		{token, "?vendor_id=" + id + "&page=100&q=does-not-match-a-key", 200, 1},
		{token, "?vendor_id=missing", 200, 0},
	} {
		req := makeLoopbackRequest("GET", "/admin/stats"+tc.query, nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("stats: %d %s", w.Code, w.Body)
		}
		if tc.status != 200 {
			continue
		}
		var response struct {
			Plugins map[string][]gateway.PluginStats `json:"plugins"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Plugins) != tc.count || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("stats filtering/data exposure: %s", w.Body)
		}
		if tc.count == 1 {
			rows := response.Plugins[id]
			if len(rows) != 1 || rows[0].Name != "history" || rows[0].Rejected != 1 || rows[0].Last == nil || rows[0].Last.Status != "rejected" {
				t.Fatalf("missing plugin outcome: %+v", rows)
			}
		}
	}
}
