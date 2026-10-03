package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/gateway"
	"jc_proxy/internal/keystore"
)

func TestShutdownWaitsForCanceledAttemptsBeforeFinalFlush(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		name := "http1"
		if h2 {
			name = "http2"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				_, _ = io.Copy(io.Discard, req.Body)
				close(entered)
				<-req.Context().Done()
			}))
			defer upstream.Close()
			cfg := &config.Config{Vendors: config.VendorsFromMap(map[string]config.VendorConfig{"openai": {Upstream: config.UpstreamConfig{BaseURL: upstream.URL}}})}
			store, err := keystore.NewFileStore(filepath.Join(t.TempDir(), "keys.json"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err := store.Append("vid_openai", []string{"key"}); err != nil {
				t.Fatal(err)
			}
			rt, err := gateway.NewRuntime(cfg, store)
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			p, err := gateway.NewRuntimeStatsPersister(rt, store, gateway.RuntimeStatsPersisterOptions{FlushInterval: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			handler := &drainingHandler{next: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if h2 != (req.ProtoMajor == 2) {
					t.Errorf("unexpected downstream protocol: %s (HTTP/2=%v)", req.Proto, h2)
				}
				rt.ServeHTTP(w, req)
			})}
			server := httptest.NewUnstartedServer(handler)
			server.EnableHTTP2 = h2
			if h2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			requestDone := make(chan struct{})
			client := server.Client()
			client.Timeout = 3 * time.Second
			go func() {
				defer close(requestDone)
				resp, _ := client.Get(server.URL + "/openai/test")
				if resp != nil {
					resp.Body.Close()
				}
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("upstream not reached")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if err := shutdownHTTPServer(ctx, server.Config, handler); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("shutdown=%v", err)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			<-requestDone
			records, err := store.List("vid_openai")
			if err != nil {
				t.Fatal(err)
			}
			s := records[0].RuntimeStats
			if s.TotalRequests != 1 || s.InterruptedCount() != 1 || s.OtherErrorCount != 0 || s.RecentRequests != 0 {
				t.Fatalf("final flush lost canceled attempt: %+v", s)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", "/openai/test", nil))
			if w.Code != 503 {
				t.Fatal("accepted a request after drain")
			}
		})
	}
}

func TestGracefulShutdownDoesNotAbortCompletedRequest(t *testing.T) {
	handler := &drainingHandler{next: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(204) })}
	server := httptest.NewServer(handler)
	defer server.Close()
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdownHTTPServer(ctx, server.Config, handler); err != nil {
		t.Fatal(err)
	}
}
