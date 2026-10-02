package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/gateway"
	"jc_proxy/internal/keystore"
)

func statsResetTestHandler(t *testing.T) (*Handler, *http.ServeMux, string, string) {
	t.Helper()
	h := makeHandlerForTest(t)
	s := h.service
	id := vendorIDFromService(t, s, "openai")
	if _, err := s.keyStore.Append("orphan", []string{"old-key"}); err != nil {
		t.Fatal(err)
	}
	statsStore := s.keyStore.(keystore.RuntimeStatsStore)
	if err := statsStore.ApplyRuntimeStatsDeltas(map[string][]keystore.RuntimeStatsDelta{
		id:       {{Key: "k1", RuntimeStats: keystore.RuntimeStats{TotalRequests: 10, SuccessCount: 10}}},
		"orphan": {{Key: "old-key", RuntimeStats: keystore.RuntimeStats{TotalRequests: 20, SuccessCount: 20}}},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	rt, err := gateway.NewRuntime(cfg, s.keyStore)
	if err != nil {
		t.Fatal(err)
	}
	p, err := gateway.NewRuntimeStatsPersister(rt, statsStore, gateway.RuntimeStatsPersisterOptions{FlushInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	s.runtime, s.statsPersister = rt, p
	t.Cleanup(func() { _ = p.Close(); _ = rt.Close() })
	mux := http.NewServeMux()
	h.Register(mux)
	return h, mux, loginAdminToken(t, mux), id
}

func sendStatsReset(mux *http.ServeMux, token, method, body string) *httptest.ResponseRecorder {
	r := makeLoopbackRequest(method, "/admin/stats/reset", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Admin-User", "forged-user")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestStatsResetEndpointValidationAndAuth(t *testing.T) {
	h, mux, token, id := statsResetTestHandler(t)
	body := `{"scope":"vendor","vendor_id":"` + id + `","confirmation":"RESET"}`
	for _, tt := range []struct {
		name, token, method, body string
		want                      int
	}{
		{"unauthenticated", "", "POST", body, 401},
		{"invalid token", "invalid", "POST", body, 401},
		{"read only", token, "GET", body, 405},
		{"no scope", token, "POST", `{"confirmation":"RESET"}`, 400},
		{"no confirm", token, "POST", `{"scope":"all"}`, 400},
		{"wrong confirm", token, "POST", `{"scope":"all","confirmation":"reset"}`, 400},
		{"no vendor", token, "POST", `{"scope":"vendor","confirmation":"RESET"}`, 400},
		{"ambiguous scope", token, "POST", `{"scope":"all","vendor_id":"x","confirmation":"RESET"}`, 400},
		{"unknown vendor", token, "POST", `{"scope":"vendor","vendor_id":"missing","confirmation":"RESET"}`, 400},
		{"vendor name is not id", token, "POST", `{"scope":"vendor","vendor_id":"openai","confirmation":"RESET"}`, 400},
		{"unknown field", token, "POST", `{"scope":"all","confirmation":"RESET","typo":1}`, 400},
		{"trailing json", token, "POST", body + `{}`, 400},
		{"invalid json", token, "POST", `{`, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := sendStatsReset(mux, tt.token, tt.method, tt.body)
			if w.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
	records, _ := h.service.keyStore.List(id)
	if records[0].TotalRequests != 10 {
		t.Fatal("rejected requests changed statistics")
	}
}

func TestStatsResetEndpointVendorAllAndAudit(t *testing.T) {
	h, mux, token, id := statsResetTestHandler(t)
	body := `{"scope":"vendor","vendor_id":"` + id + `","confirmation":"RESET"}`
	for i := 0; i < 2; i++ {
		w := sendStatsReset(mux, token, "POST", body)
		if w.Code != http.StatusOK {
			t.Fatalf("reset: %d %s", w.Code, w.Body.String())
		}
		var response struct{ Count int }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Count != 1 {
			t.Fatalf("response = %s, %v", w.Body.String(), err)
		}
	}
	s := h.service
	records, _ := s.keyStore.List(id)
	orphan, _ := s.keyStore.List("orphan")
	if records[0].RuntimeStats != (keystore.RuntimeStats{}) || orphan[0].TotalRequests != 20 {
		t.Fatal("vendor scope was not respected")
	}
	if s.runtime.Snapshot().VendorStateSnapshots()[id][0].TotalRequests != 0 {
		t.Fatal("endpoint reset storage but not runtime")
	}
	w := sendStatsReset(mux, token, "POST", `{"scope":"vendor","vendor_id":"orphan","confirmation":"RESET"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("orphan reset: %d %s", w.Code, w.Body.String())
	}
	w = sendStatsReset(mux, token, "POST", `{"scope":"all","confirmation":"RESET"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("all reset: %d %s", w.Code, w.Body.String())
	}
	if err := s.statsPersister.Flush(); err != nil {
		t.Fatal(err)
	}
	all, _ := s.keyStore.ListAll()
	for _, records := range all {
		if records[0].RuntimeStats != (keystore.RuntimeStats{}) {
			t.Fatal("old statistics returned after flush")
		}
	}
	data, err := os.ReadFile(s.audit.path)
	if err != nil {
		t.Fatal(err)
	}
	var resets []AuditEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event AuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Action == "runtime_stats.reset" {
			resets = append(resets, event)
			if event.Actor != "admin" {
				t.Fatal("audit trusted spoofed actor")
			}
		}
	}
	if len(resets) != 4 || resets[0].Detail["vendor_id"] != id || resets[3].Detail["scope"] != "all" || resets[3].Detail["count"] != float64(2) {
		t.Fatalf("unexpected audit records: %#v", resets)
	}
	if strings.Contains(string(data), "old-key") || strings.Contains(string(data), "k1") {
		t.Fatal("audit leaked keys")
	}
}

func TestStatsResetEndpointFailure(t *testing.T) {
	h, mux, token, id := statsResetTestHandler(t)
	blocker := h.service.keyStore.Info().FilePath + ".tmp"
	if err := os.Mkdir(blocker, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(blocker)
	w := sendStatsReset(mux, token, "POST", `{"scope":"all","confirmation":"RESET"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failure status: %d %s", w.Code, w.Body.String())
	}
	if h.service.runtime.Snapshot().VendorStateSnapshots()[id][0].TotalRequests != 10 {
		t.Fatal("failed endpoint reset runtime")
	}
	data, err := os.ReadFile(h.service.audit.path)
	if err != nil || !strings.Contains(string(data), "runtime_stats.reset_failed") {
		t.Fatalf("missing failure audit: %v", err)
	}
}
