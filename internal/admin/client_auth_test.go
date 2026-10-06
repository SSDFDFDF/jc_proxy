package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"jc_proxy/internal/config"
)

func TestClientKeyLifecycleFailsClosed(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	for _, aggregate := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "aggregate"}[aggregate], func(t *testing.T) {
			s := makeHandlerForTest(t).service
			defer s.runtime.Close()
			cfg, err := s.GetConfigRaw()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Vendors[0].Upstream.BaseURL = upstream.URL
			if err := s.UpdateConfig("admin", cfg); err != nil {
				t.Fatal(err)
			}
			id := cfg.Vendors[0].ID
			path := "/openai/v1/models"
			if aggregate {
				id, err = s.CreateVendor("admin", "combined", config.VendorConfig{
					Provider:  "aggregate",
					Aggregate: config.AggregateConfig{Children: []config.AggregateChild{{VendorID: id, Weight: 1}}},
				})
				if err != nil {
					t.Fatal(err)
				}
				path = "/combined/v1/models"
			}
			request := func(token string, wantStatus int) {
				t.Helper()
				before := calls.Load()
				r := httptest.NewRequest(http.MethodGet, path, nil)
				if token != "" {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				s.runtime.ServeHTTP(w, r)
				if w.Code != wantStatus {
					t.Fatalf("status=%d, want %d: %s", w.Code, wantStatus, w.Body)
				}
				if wantStatus == http.StatusUnauthorized && calls.Load() != before {
					t.Fatal("unauthorized request reached upstream")
				}
			}
			request("", http.StatusUnauthorized)
			if err := s.AddClientKey("admin", id, "client-lifecycle-secret"); err != nil {
				t.Fatal(err)
			}
			request("", http.StatusUnauthorized)
			request("client-lifecycle-secret", http.StatusNoContent)

			cfg, err = s.GetConfigRaw()
			if err != nil {
				t.Fatal(err)
			}
			disabled := false
			cfg.Vendors[cfg.VendorIndexByID(id)].ClientAuth.Enabled = &disabled
			if err := s.UpdateConfig("admin", cfg); err != nil {
				t.Fatal(err)
			}
			request("", http.StatusNoContent)
			masked, err := s.store.SnapshotJSON(true)
			if err != nil || strings.Contains(string(masked), "client-lifecycle-secret") {
				t.Fatalf("disabled client key was not masked: %v", err)
			}

			if err := s.DeleteClientKey("admin", id, "client-lifecycle-secret"); err != nil {
				t.Fatal(err)
			}
			request("", http.StatusUnauthorized)
			request("client-lifecycle-secret", http.StatusUnauthorized)
			cfg, err = s.GetConfigRaw()
			if err != nil {
				t.Fatal(err)
			}
			auth := cfg.Vendors[cfg.VendorIndexByID(id)].ClientAuth
			if !auth.IsEnabled() || len(auth.Keys) != 0 {
				t.Fatalf("deleting last key did not restore safe defaults: %+v", auth)
			}
			if err := s.runtime.Update(cfg); err != nil {
				t.Fatal(err)
			}
			request("", http.StatusUnauthorized)
			if err := s.AddClientKey("admin", id, "replacement-key"); err != nil {
				t.Fatal(err)
			}
			request("", http.StatusUnauthorized)
			request("replacement-key", http.StatusNoContent)
		})
	}
}
