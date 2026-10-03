package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/keystore"
)

func TestRouterPreservesQueryAndEscapedPath(t *testing.T) {
	for _, tc := range []struct{ name, base, incoming, wantPath, rewrite string }{
		{"base-query", "http://unused.invalid/v1?api-version=2025-01-01", "/openai/models?stream=true", "/v1/models", ""},
		{"escaped-slash", "http://unused.invalid", "/openai/files/a%2Fb/content", "/files/a%2Fb/content", ""},
		{"encoded-base", "http://unused.invalid/base%2Fid/", "/openai/files/x%2Fy", "/base%2Fid/files/x%2Fy", ""},
		{"prefix-rewrite", "http://unused.invalid", "/openai/v1/files/a%2Fb", "/v2/files/a%2Fb", "prefix"},
		{"exact-rewrite", "http://unused.invalid", "/openai/v1/files/a%2Fb", "/replacement", "exact"},
		{"unicode", "http://unused.invalid", "/openai/v1/%E4%B8%AD%25text", "/v2/%E4%B8%AD%25text", "prefix"},
		{"encoded-vendor", "http://unused.invalid", "/open%61i/files/a%2fb", "/files/a%2fb", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := routerWithMasking(t, tc.base, []string{"key"}, nil)
			v := r.vendors["openai"]
			if tc.rewrite == "prefix" {
				v.rewrites = newRewriteMatcher(map[string]string{"/v1/*": "/v2/*"})
			}
			if tc.rewrite == "exact" {
				v.rewrites = newRewriteMatcher(map[string]string{"/v1/files/a/b": "/replacement"})
			}
			var got *url.URL
			v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				got = req.URL
				return &http.Response{StatusCode: 204, Header: make(http.Header), Body: http.NoBody}, nil
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", tc.incoming, nil))
			if got == nil || got.EscapedPath() != tc.wantPath {
				t.Fatalf("target=%v, want path %q", got, tc.wantPath)
			}
			if tc.name == "base-query" && (got.Query().Get("api-version") != "2025-01-01" || got.Query().Get("stream") != "true") {
				t.Fatalf("query corrupted: %s", got.RawQuery)
			}
		})
	}
}

func TestDefaultDisableIgnoresReflectedModelName(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": fmt.Sprintf("The model %q does not exist", body.Model), "type": "invalid_request_error", "code": "model_not_found"}})
	}))
	defer upstream.Close()
	r, _ := routerWithMasking(t, upstream.URL, []string{"key1", "key2"}, nil)
	for range 3 {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/openai/v1/chat/completions", strings.NewReader(`{"model":"invalid_api_key"}`)))
		if w.Code != 404 {
			t.Fatalf("model error became %d", w.Code)
		}
	}
	if calls != 3 {
		t.Fatalf("upstream calls=%d", calls)
	}
	for _, s := range r.vendors["openai"].pool.Snapshot() {
		if s.Status != keystore.KeyStatusActive {
			t.Fatalf("reflected input disabled valid key: %+v", s)
		}
	}
}

func TestDefaultCredentialKeywordsRequireErrorCode(t *testing.T) {
	r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, nil)
	policy := r.vendors["openai"].errorPolicy
	for _, tc := range []struct {
		body    string
		disable bool
	}{
		{`{"error":{"code":"invalid_api_key"}}`, true},
		{`{"error":{"type":"incorrect_api_key"}}`, true},
		{`{"error":"invalid_api_key"}`, true},
		{`{"code":"INVALID_API_KEY"}`, true},
		{`{"error":{"message":"incorrect_api_key","code":"model_not_found"}}`, false},
		{`{"error":{"param":"invalid_api_key"}}`, false},
		{`{"input":{"code":"invalid_api_key"},"error":{"type":"invalid_request_error"}}`, false},
		{`invalid_api_key`, false},
		{`{"error":{"code":"invalid_api_key`, false},
	} {
		d := classifyResponse("openai", policy, 400, http.Header{"Content-Type": {"application/json"}}, []byte(tc.body))
		if (d.action == keyActionDisable) != tc.disable {
			t.Errorf("body=%s decision=%+v", tc.body, d)
		}
	}
	policy.AutoDisable.Keywords = []string{"quota exhausted"}
	if d := classifyResponse("openai", policy, 400, nil, []byte("quota exhausted")); d.action != keyActionDisable {
		t.Fatal("custom text policy changed")
	}
}

func TestUploadDeadlineStopsBlockedUpstreamWrite(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	r, _ := routerWithMasking(t, "http://"+listener.Addr().String(), []string{"key"}, nil)
	v := r.vendors["openai"]
	v.interimInterval = 0
	v.upstreamUploadTimeout = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wrote := make(chan httptrace.WroteRequestInfo, 1)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) { wrote <- info }})
	req := httptest.NewRequest("POST", "/openai/test", strings.NewReader(strings.Repeat("x", 16<<20))).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); r.ServeHTTP(w, req) }()
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-ctx.Done():
		t.Fatal("no upstream connection")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("upload was not bounded")
	}
	if info := <-wrote; info.Err == nil {
		t.Fatal("request unexpectedly finished uploading")
	}
	s := v.pool.Snapshot()[0]
	if w.Code != 502 || s.Inflight != 0 || s.TotalRequests != 1 || s.OtherErrorCount != 1 || s.InterruptedCount() != 0 || s.AvgResponseMS != 999000 || !strings.Contains(s.LastError, "upstream upload timeout") {
		t.Fatalf("status=%d stats=%+v", w.Code, s)
	}
}

func TestClientInterruptedUploadDoesNotPenalizeUpstream(t *testing.T) {
	readPrefix := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, err := io.CopyN(io.Discard, req.Body, 32<<10)
		if err == nil {
			readPrefix <- struct{}{}
		}
		_, _ = io.Copy(io.Discard, req.Body)
		w.WriteHeader(204)
	}))
	defer upstream.Close()
	r, _ := routerWithMasking(t, upstream.URL, []string{"key"}, nil)
	r.vendors["openai"].interimInterval = 0
	done := make(chan struct{}, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { r.ServeHTTP(w, req); done <- struct{}{} }))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)
	for range 20 {
		conn, err := net.Dial("tcp", u.Host)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fmt.Fprintf(conn, "POST /openai/v1/messages HTTP/1.1\r\nHost: proxy\r\nContent-Length: 16777216\r\nContent-Type: application/octet-stream\r\n\r\n%s", strings.Repeat("x", 64<<10))
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		select {
		case <-readPrefix:
		case <-time.After(time.Second):
			conn.Close()
			t.Fatal("upstream did not receive prefix")
		}
		conn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("proxy did not settle interrupted upload")
		}
	}
	s := r.vendors["openai"].pool.Snapshot()[0]
	if s.TotalRequests != 20 || s.InterruptedCount() != 20 || s.OtherErrorCount != 0 || s.RecentRequests != 0 || s.Failures != 0 || !s.CooldownUntil.IsZero() || s.Inflight != 0 {
		t.Fatalf("client upload failures poisoned key: %+v", s)
	}
}

func TestRequestBodyErrorOriginIsNotUpstreamEOF(t *testing.T) {
	for _, fromClient := range []bool{false, true} {
		r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, nil)
		v := r.vendors["openai"]
		v.client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if fromClient {
				_, err := io.ReadAll(req.Body)
				return nil, fmt.Errorf("write request: %w", err)
			}
			return nil, io.ErrUnexpectedEOF
		})
		req := httptest.NewRequest("POST", "/openai/test", nil)
		req.Body, req.ContentLength = timingErrorBody{}, 100
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		s := v.pool.Snapshot()[0]
		if fromClient {
			if w.Code != 400 || s.InterruptedCount() != 1 || s.RecentRequests != 0 {
				t.Fatalf("client error: %+v", s)
			}
		} else if w.Code != 502 || s.OtherErrorCount != 1 || s.RecentRequests != 1 {
			t.Fatalf("upstream EOF was hidden: %+v", s)
		}
	}
}
