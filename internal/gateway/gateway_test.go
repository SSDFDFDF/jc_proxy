package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
	"jc_proxy/internal/ui"
)

type testKeyController struct {
	records    map[string][]keystore.Record
	lastVendor string
	lastKey    string
	lastStatus string
	lastReason string
	lastActor  string
}

type flushCountWriter struct {
	header     http.Header
	statusCode int
	body       bytes.Buffer
	flushCount int
}

func (w *flushCountWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *flushCountWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *flushCountWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.body.Write(p)
}

func (w *flushCountWriter) Flush() {
	w.flushCount++
}

type brokenPipeWriter struct {
	header     http.Header
	statusCode int
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

type trackingReadCloser struct {
	readCalls int
	err       error
}

func (w *brokenPipeWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *brokenPipeWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *brokenPipeWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return 0, syscall.EPIPE
}

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (r *trackingReadCloser) Read(p []byte) (int, error) {
	r.readCalls++
	if r.err != nil {
		return 0, r.err
	}
	return 0, io.EOF
}

func (r *trackingReadCloser) Close() error {
	return nil
}

func (t *testKeyController) ListAll() (map[string][]keystore.Record, error) {
	return t.records, nil
}

func (t *testKeyController) SetStatus(vendor, key, status, reason, actor string) error {
	t.lastVendor = vendor
	t.lastKey = key
	t.lastStatus = status
	t.lastReason = reason
	t.lastActor = actor
	return nil
}

func TestSplitVendorPath(t *testing.T) {
	tests := []struct {
		in     string
		vendor string
		rest   string
		ok     bool
	}{
		{in: "/openai/v1/chat/completions", vendor: "openai", rest: "/v1/chat/completions", ok: true},
		{in: "/openai", vendor: "openai", rest: "/", ok: true},
		{in: "/", ok: false},
		{in: "", ok: false},
	}

	for _, tc := range tests {
		v, r, ok := splitVendorPath(tc.in)
		if v != tc.vendor || r != tc.rest || ok != tc.ok {
			t.Fatalf("splitVendorPath(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.in, v, r, ok, tc.vendor, tc.rest, tc.ok)
		}
	}
}

func TestRewriteMatcherApply(t *testing.T) {
	matcher := newRewriteMatcher(map[string]string{
		"/v1/chat/completions": "/v1/fim/completions",
		"/v1/files/*":          "/v1/storage/*",
	})

	if got := matcher.Apply("/v1/chat/completions"); got != "/v1/fim/completions" {
		t.Fatalf("exact rewrite mismatch: %s", got)
	}
	if got := matcher.Apply("/v1/files/abc"); got != "/v1/storage/abc" {
		t.Fatalf("prefix rewrite mismatch: %s", got)
	}
	if got := matcher.Apply("/v1/other"); got != "/v1/other" {
		t.Fatalf("unexpected rewrite: %s", got)
	}
}

func TestConsoleRoute(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Admin: config.AdminConfig{
			Enabled:      true,
			PasswordHash: "pbkdf2$120000$/8TGko7UMwVqb8htEpczLA$b7031a7a7ec82193cdcba4822bdfd18dbae2196528ba102b0bf26b85d67c8ec0",
		},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: "https://api.openai.com",
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	// /console should redirect to /console/
	req := httptest.NewRequest(http.MethodGet, "/console", nil)
	req.RemoteAddr = "127.0.0.1:10001"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected redirect, got %d", w.Code)
	}

	// /console/ should serve index.html (200)
	req2 := httptest.NewRequest(http.MethodGet, "/console/", nil)
	req2.RemoteAddr = "127.0.0.1:10001"
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	// /consolex should not be treated as console static route
	req3 := httptest.NewRequest(http.MethodGet, "/consolex", nil)
	req3.RemoteAddr = "127.0.0.1:10001"
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)
	if w3.Code == http.StatusOK {
		t.Fatalf("unexpected console match for /consolex")
	}

	// static asset under /console/assets should be served
	sub, err := fs.Sub(ui.DistFS, "dist/assets")
	if err != nil {
		t.Fatalf("load dist assets fs failed: %v", err)
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		t.Fatalf("read dist assets dir failed: %v", err)
	}
	assetName := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".css") || strings.HasSuffix(name, ".js") {
			assetName = name
			break
		}
	}
	if assetName == "" {
		t.Fatal("no css/js asset found in dist/assets")
	}
	req4 := httptest.NewRequest(http.MethodGet, "/console/assets/"+assetName, nil)
	req4.RemoteAddr = "127.0.0.1:10001"
	w4 := httptest.NewRecorder()
	router.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("expected asset 200, got %d", w4.Code)
	}
}

func TestConsoleRouteHiddenForRemoteAddr(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Admin: config.AdminConfig{
			Enabled:      true,
			PasswordHash: "pbkdf2$120000$/8TGko7UMwVqb8htEpczLA$b7031a7a7ec82193cdcba4822bdfd18dbae2196528ba102b0bf26b85d67c8ec0",
			AllowedCIDRs: []string{"127.0.0.1/32"},
		},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: "https://api.openai.com",
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/console/", nil)
	req.RemoteAddr = "203.0.113.9:10001"

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("remote console should be hidden, got %d", w.Code)
	}
}

func TestConsoleRouteAllowsRemoteAddrWhenCIDRsEmpty(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Admin: config.AdminConfig{
			Enabled:      true,
			PasswordHash: "pbkdf2$120000$/8TGko7UMwVqb8htEpczLA$b7031a7a7ec82193cdcba4822bdfd18dbae2196528ba102b0bf26b85d67c8ec0",
		},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: "https://api.openai.com",
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/console/", nil)
	req.RemoteAddr = "203.0.113.9:10001"

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("remote console should be allowed when cidrs empty, got %d", w.Code)
	}
}

func TestConsoleRouteAllowsTrustedProxyForwardedAddr(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Admin: config.AdminConfig{
			Enabled:           true,
			PasswordHash:      "pbkdf2$120000$/8TGko7UMwVqb8htEpczLA$b7031a7a7ec82193cdcba4822bdfd18dbae2196528ba102b0bf26b85d67c8ec0",
			AllowedCIDRs:      []string{"203.0.113.8/32"},
			TrustedProxyCIDRs: []string{"10.0.0.0/8"},
		},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: "https://api.openai.com",
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/console/", nil)
	req.RemoteAddr = "10.1.2.3:10001"
	req.Header.Set("X-Forwarded-For", "203.0.113.8")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("trusted proxy console request should be allowed, got %d", w.Code)
	}
}

func TestForwardHeadersDropAuthByDefault(t *testing.T) {
	v := &vendorGateway{
		allowlist:     map[string]struct{}{},
		injectHeaders: map[string]string{},
		upstreamAuth: config.UpstreamAuthConfig{
			Mode:   "header",
			Header: "x-api-key",
			Prefix: "",
		},
	}
	src := http.Header{}
	src.Set("Authorization", "Bearer client-token")
	src.Set("X-API-Key", "client-x-key")
	src.Set("Content-Type", "application/json")

	out := v.forwardHeaders(src, "upstream-key")
	if got := out.Get("Authorization"); got != "" {
		t.Fatalf("authorization should be dropped, got: %s", got)
	}
	if got := out.Get("X-Api-Key"); got != "upstream-key" {
		t.Fatalf("x-api-key should be upstream key, got: %s", got)
	}
}

func TestForwardHeadersDropConnectionScopedHeaders(t *testing.T) {
	v := &vendorGateway{
		allowlist: map[string]struct{}{},
		upstreamAuth: config.UpstreamAuthConfig{
			Mode: "passthrough",
		},
	}
	src := http.Header{}
	src.Set("Connection", "X-Remove-Me, Trailer")
	src.Set("X-Remove-Me", "secret")
	src.Set("Trailer", "X-Upstream-Trailer")
	src.Set("X-Upstream-Trailer", "value")
	src.Set("Content-Type", "application/json")

	out := v.forwardHeaders(src, "")
	if got := out.Get("X-Remove-Me"); got != "" {
		t.Fatalf("connection scoped header should be dropped, got %q", got)
	}
	if got := out.Get("Trailer"); got != "" {
		t.Fatalf("trailer header should be dropped, got %q", got)
	}
	if got := out.Get("X-Upstream-Trailer"); got != "" {
		t.Fatalf("trailer payload header should be dropped, got %q", got)
	}
	if got := out.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type should remain, got %q", got)
	}
}

func TestCopyResponseHeadersDropConnectionScopedHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Connection", "X-Response-Only")
	src.Set("X-Response-Only", "hidden")
	src.Set("Trailer", "X-Server-Trailer")
	src.Set("X-Server-Trailer", "value")
	src.Set("Content-Type", "application/json")

	dst := make(http.Header)
	copyResponseHeaders(dst, src)

	if got := dst.Get("X-Response-Only"); got != "" {
		t.Fatalf("connection scoped response header should be dropped, got %q", got)
	}
	if got := dst.Get("Trailer"); got != "" {
		t.Fatalf("trailer response header should be dropped, got %q", got)
	}
	if got := dst.Get("X-Server-Trailer"); got != "" {
		t.Fatalf("trailer payload response header should be dropped, got %q", got)
	}
	if got := dst.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type should remain, got %q", got)
	}
}

func TestPrepareRequestBodySkipsPrefetchForUnknownLength(t *testing.T) {
	body := &trackingReadCloser{err: errors.New("unexpected eager read")}
	req := httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", body)
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")

	src, err := prepareRequestBody(req)
	if err != nil {
		t.Fatalf("prepareRequestBody returned error for unknown-length body: %v", err)
	}
	if src.replayable {
		t.Fatal("unknown-length body should remain single-use")
	}
	if body.readCalls != 0 {
		t.Fatalf("prepareRequestBody should not pre-read unknown-length body, got %d reads", body.readCalls)
	}

	reader, err := src.Body()
	if err != nil {
		t.Fatalf("Body() returned error: %v", err)
	}
	_, readErr := io.ReadAll(reader)
	if !errors.Is(readErr, body.err) {
		t.Fatalf("expected deferred body read error %v, got %v", body.err, readErr)
	}
	if body.readCalls == 0 {
		t.Fatal("expected body to be read only after forwarding starts")
	}
}

func TestResponseCopyBufferSkipsPoolForStreaming(t *testing.T) {
	newCalls := 0
	router := &Router{
		bufPool: sync.Pool{
			New: func() any {
				newCalls++
				buf := make([]byte, pooledResponseCopyBufferBytes)
				return &buf
			},
		},
	}

	buf, release := router.responseCopyBuffer(true)
	defer release()

	if newCalls != 0 {
		t.Fatalf("streaming response should not borrow pooled buffer, got %d pool allocations", newCalls)
	}
	if len(buf) != streamingResponseCopyBufferBytes {
		t.Fatalf("streaming buffer len = %d, want %d", len(buf), streamingResponseCopyBufferBytes)
	}
}

func TestRouterSkipsClientAuthWhenDisabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
				ClientAuth: config.ClientAuthConfig{
					Enabled: false,
					Keys:    []string{"client-key-a"},
				},
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected request to pass without client auth, got %d", w.Code)
	}
}

func TestRouterTracksUpstreamStatusStats(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
				Backoff: config.BackoffConfig{
					Threshold: 3,
					Duration:  time.Hour,
				},
			},
		},
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", strings.NewReader(`{"ok":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected upstream status passthrough, got %d", w.Code)
	}

	stats := router.VendorStats()["openai"]
	if len(stats) != 1 {
		t.Fatalf("expected one key stats entry, got %d", len(stats))
	}
	if got := stats[0]["rate_limit_count"]; got != 1 {
		t.Fatalf("unexpected rate limit count: %#v", got)
	}
	if got := stats[0]["total_requests"]; got != 1 {
		t.Fatalf("unexpected total request count: %#v", got)
	}
	if got := stats[0]["success_count"]; got != 0 {
		t.Fatalf("unexpected success count: %#v", got)
	}
	if got := stats[0]["failures"]; got != 1 {
		t.Fatalf("unexpected failure count: %#v", got)
	}
	lastError, _ := stats[0]["last_error"].(string)
	if !strings.Contains(lastError, "429") {
		t.Fatalf("expected last error to mention 429, got %q", lastError)
	}
}

func TestRouterAutoDisablesInvalidKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"incorrect_api_key"}}`))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
				},
				LoadBalance: "round_robin",
			},
		},
	}
	ctrl := &testKeyController{
		records: map[string][]keystore.Record{
			"openai": {
				{Key: "k1", Status: keystore.KeyStatusActive},
			},
		},
	}

	router, err := NewWithUpstreamKeyRecords(cfg, ctrl.records, ctrl)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", strings.NewReader(`{"ok":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected upstream status passthrough, got %d", w.Code)
	}
	if ctrl.lastStatus != keystore.KeyStatusDisabledAuto {
		t.Fatalf("expected auto disable status, got %q", ctrl.lastStatus)
	}
	if ctrl.lastVendor != "openai" || ctrl.lastKey != "k1" {
		t.Fatalf("unexpected disabled key target: %s %s", ctrl.lastVendor, ctrl.lastKey)
	}

	stats := router.VendorStats()["openai"]
	if len(stats) != 1 {
		t.Fatalf("expected one key stats entry, got %d", len(stats))
	}
	if got := stats[0]["status"]; got != keystore.KeyStatusDisabledAuto {
		t.Fatalf("unexpected runtime key status: %#v", got)
	}
}

func TestRouterFlushesStreamingResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: hello\n\n"))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/chat/completions?stream=true", nil)
	req.Header.Set("Accept", "text/event-stream")
	w := &flushCountWriter{}
	router.ServeHTTP(w, req)

	if w.statusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.statusCode)
	}
	if w.flushCount == 0 {
		t.Fatal("expected at least one flush for streaming response")
	}
	if got := w.body.String(); got != "data: hello\n\n" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestRouterDoesNotCooldownHealthyKeyOnClientDisconnect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	w := &brokenPipeWriter{}
	router.ServeHTTP(w, req)

	stats := router.VendorStats()["openai"]
	if len(stats) != 1 {
		t.Fatalf("expected one key stats entry, got %d", len(stats))
	}
	if got := stats[0]["failures"]; got != 0 {
		t.Fatalf("healthy key should not enter failure path, got %#v", got)
	}
	if got := stats[0]["other_error_count"]; got != 0 {
		t.Fatalf("healthy key should not record downstream write error, got %#v", got)
	}
	if got := stats[0]["inflight"]; got != 0 {
		t.Fatalf("inflight should be released, got %#v", got)
	}
}

func TestRouterStillClassifiesErrorResponseOnClientDisconnect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"incorrect_api_key"}}`))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
				},
				LoadBalance: "round_robin",
			},
		},
	}
	ctrl := &testKeyController{
		records: map[string][]keystore.Record{
			"openai": {
				{Key: "k1", Status: keystore.KeyStatusActive},
			},
		},
	}

	router, err := NewWithUpstreamKeyRecords(cfg, ctrl.records, ctrl)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	w := &brokenPipeWriter{}
	router.ServeHTTP(w, req)

	stats := router.VendorStats()["openai"]
	if got := stats[0]["status"]; got != keystore.KeyStatusDisabledAuto {
		t.Fatalf("expected key to remain auto disabled on upstream 401, got %#v", got)
	}
	if ctrl.lastStatus != keystore.KeyStatusDisabledAuto {
		t.Fatalf("expected controller to persist auto disable, got %q", ctrl.lastStatus)
	}
}

func TestRouterFailsOverBufferedPostOnRateLimit(t *testing.T) {
	attempts := make([]string, 0, 2)
	bodies := make([]string, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		attempts = append(attempts, auth)
		payload, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(payload))

		if auth == "Bearer k1" {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok from k2"))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1", "k2"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", strings.NewReader(`{"model":"gpt","stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected failover success, got %d", w.Code)
	}
	if got := w.Body.String(); got != "ok from k2" {
		t.Fatalf("unexpected body: %q", got)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected two upstream attempts, got %d", len(attempts))
	}
	if attempts[0] != "Bearer k1" || attempts[1] != "Bearer k2" {
		t.Fatalf("unexpected auth attempt order: %#v", attempts)
	}
	if len(bodies) != 2 || bodies[0] != `{"model":"gpt","stream":false}` || bodies[1] != `{"model":"gpt","stream":false}` {
		t.Fatalf("request body should be replayed verbatim, got %#v", bodies)
	}

	stats := router.VendorStats()["openai"]
	if len(stats) != 2 {
		t.Fatalf("expected two key stats entries, got %d", len(stats))
	}
	if got := stats[0]["rate_limit_count"]; got != 1 {
		t.Fatalf("first key should record rate limit, got %#v", got)
	}
	if got := stats[1]["success_count"]; got != 1 {
		t.Fatalf("second key should record success, got %#v", got)
	}
}

func TestRouterDoesNotRetryMultipartPost(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.Header.Get("Authorization") == "Bearer k1" {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("unexpected"))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1", "k2"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	body := "--abc\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\nContent-Type: text/plain\r\n\r\nhello\r\n--abc--\r\n"
	req := httptest.NewRequest(http.MethodPost, "/openai/v1/files", strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=abc")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected original upstream status without retry, got %d", w.Code)
	}
	if attempts != 1 {
		t.Fatalf("multipart request should not retry, got %d attempts", attempts)
	}
}

func TestRouterRetriesSafeGetOnRequestError(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: "https://example.test",
					Keys:    []string{"k1", "k2"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	attempts := make([]string, 0, 2)
	router.vendors["openai"].client = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			auth := r.Header.Get("Authorization")
			attempts = append(attempts, auth)
			if auth == "Bearer k1" {
				return nil, syscall.ECONNRESET
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ok from get retry")),
			}, nil
		}),
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected failover success for safe GET, got %d", w.Code)
	}
	if got := w.Body.String(); got != "ok from get retry" {
		t.Fatalf("unexpected body: %q", got)
	}
	if len(attempts) != 2 || attempts[0] != "Bearer k1" || attempts[1] != "Bearer k2" {
		t.Fatalf("unexpected attempt order: %#v", attempts)
	}
}

func TestRouterRespectsDisabledRateLimitFailoverAndCooldown(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1", "k2"},
				},
				LoadBalance: "round_robin",
				ErrorPolicy: config.ErrorPolicyConfig{
					Cooldown: config.ErrorCooldownConfig{
						RateLimit: config.ErrorCooldownRule{Enabled: configBoolPtr(false)},
					},
					Failover: config.ErrorFailoverConfig{
						RateLimit: configBoolPtr(false),
					},
				},
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected original 429 response, got %d", w.Code)
	}
	if attempts != 1 {
		t.Fatalf("rate limit failover disabled should only attempt once, got %d", attempts)
	}

	stats := router.VendorStats()["openai"]
	if got := stats[0]["failures"]; got != 0 {
		t.Fatalf("rate limit cooldown disabled should not record failures, got %#v", got)
	}
	if got := stats[0]["backoff_remaining_seconds"]; got != 0 {
		t.Fatalf("rate limit cooldown disabled should not back off key, got %#v", got)
	}
}

func TestRouterRespectsDisabledInvalidKeyAutoDisable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"incorrect_api_key"}}`))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Provider: "openai",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
				},
				LoadBalance: "round_robin",
				ErrorPolicy: config.ErrorPolicyConfig{
					AutoDisable: config.ErrorAutoDisableConfig{
						InvalidKey: configBoolPtr(false),
					},
					Cooldown: config.ErrorCooldownConfig{
						Unauthorized: config.ErrorCooldownRule{Enabled: configBoolPtr(false)},
					},
					Failover: config.ErrorFailoverConfig{
						Unauthorized: configBoolPtr(false),
					},
				},
			},
		},
	}
	ctrl := &testKeyController{
		records: map[string][]keystore.Record{
			"openai": {
				{Key: "k1", Status: keystore.KeyStatusActive},
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := NewWithUpstreamKeyRecords(cfg, ctrl.records, ctrl)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected upstream 401 response, got %d", w.Code)
	}
	if ctrl.lastStatus != "" {
		t.Fatalf("invalid key auto disable disabled should not persist disable status, got %q", ctrl.lastStatus)
	}

	stats := router.VendorStats()["openai"]
	if got := stats[0]["status"]; got != keystore.KeyStatusActive {
		t.Fatalf("key should remain active, got %#v", got)
	}
	if got := stats[0]["failures"]; got != 0 {
		t.Fatalf("unauthorized cooldown disabled should not record failures, got %#v", got)
	}
}

func TestRouterDoesNotCooldownKeyOnCanceledRequestContext(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: "https://example.test",
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	router.vendors["openai"].client = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(w, req)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServeHTTP did not return after context cancellation")
	}

	stats := router.VendorStats()["openai"]
	if len(stats) != 1 {
		t.Fatalf("expected one key stats entry, got %d", len(stats))
	}
	if got := stats[0]["failures"]; got != 0 {
		t.Fatalf("canceled request should not record failures, got %#v", got)
	}
	if got := stats[0]["other_error_count"]; got != 0 {
		t.Fatalf("canceled request should not record upstream errors, got %#v", got)
	}
	if got := stats[0]["inflight"]; got != 0 {
		t.Fatalf("inflight should be released, got %#v", got)
	}
}

func TestRouterPassthroughModeDoesNotRequireManagedKeys(t *testing.T) {
	gotAuth := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"generic": {
				Provider: "generic",
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
				},
				LoadBalance: "round_robin",
				UpstreamAuth: config.UpstreamAuthConfig{
					Mode: "passthrough",
				},
				ClientHeaders: config.ClientHeadersConfig{
					Allowlist: []string{"Content-Type"},
				},
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/generic/v1/models", nil)
	req.Header.Set("Authorization", "Bearer client-token")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected passthrough request to succeed without managed keys, got %d", w.Code)
	}
	if gotAuth != "Bearer client-token" {
		t.Fatalf("authorization header not forwarded in passthrough mode: %q", gotAuth)
	}

	stats := router.VendorStats()["generic"]
	if len(stats) != 0 {
		t.Fatalf("passthrough mode should not create managed key stats, got %d entries", len(stats))
	}
}

func TestRouterDoesNotFollowUpstreamRedirects(t *testing.T) {
	attempts := make([]string, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, r.URL.Path)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("followed"))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Listen: ":8092"},
		Vendors: map[string]config.VendorConfig{
			"openai": {
				Upstream: config.UpstreamConfig{
					BaseURL: upstream.URL,
					Keys:    []string{"k1"},
				},
				LoadBalance: "round_robin",
			},
		},
	}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatalf("prepare config failed: %v", err)
	}

	router, err := New(cfg)
	if err != nil {
		t.Fatalf("init router failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/openai/redirect", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected upstream redirect to pass through, got %d", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/final" {
		t.Fatalf("unexpected redirect location: %q", got)
	}
	if len(attempts) != 1 || attempts[0] != "/redirect" {
		t.Fatalf("redirect should not be followed upstream, got %#v", attempts)
	}
}

func configBoolPtr(v bool) *bool {
	b := v
	return &b
}
