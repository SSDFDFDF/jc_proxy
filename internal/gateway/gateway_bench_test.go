package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/config"
)

func newBenchRouter(b *testing.B, upstreamURL string) *Router {
	b.Helper()
	zero := time.Duration(0)
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL:                 upstreamURL,
					Keys:                    []string{"k1", "k2", "k3"},
					ResponseHeaderTimeout:   &zero,
					InterimResponseInterval: &zero,
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		b.Fatalf("prepare config failed: %v", err)
	}
	router, err := New(cfg)
	if err != nil {
		b.Fatalf("init router failed: %v", err)
	}
	return router
}

func BenchmarkRouter_ServeHTTP_NoBody(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	router := newBenchRouter(b, upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "jc-proxy-bench/1.0")

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			b.Fatalf("unexpected status %d", w.Code)
		}
	}
}

func BenchmarkRouter_ServeHTTP_PostBody(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	router := newBenchRouter(b, upstream.URL)
	body := strings.Repeat("a", 1024)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("unexpected status %d", w.Code)
		}
	}
}

func BenchmarkBuildTargetURL(b *testing.B) {
	baseURL, err := url.Parse("https://api.openai.com/v1")
	if err != nil {
		b.Fatalf("parse: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := buildTargetURLFromBase(baseURL, "/chat/completions", "stream=true&n=1", "sk-test", nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVendorBuildTargetURL_FastPath(b *testing.B) {
	baseURL, err := url.Parse("https://api.openai.com/v1")
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	vg := &vendorGateway{baseURL: baseURL, baseURLPrefix: baseURL.String()}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := vg.buildTargetURL("/chat/completions", "stream=true&n=1", "sk-test")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildTargetURL_FastPath(b *testing.B) {
	prefix := "https://api.openai.com/v1"
	path := "/chat/completions"
	rawQuery := "stream=true&n=1"

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var sb strings.Builder
		sb.Grow(len(prefix) + len(path) + len(rawQuery) + 2)
		sb.WriteString(prefix)
		sb.WriteString(path)
		if rawQuery != "" {
			sb.WriteByte('?')
			sb.WriteString(rawQuery)
		}
		_ = sb.String()
	}
}
