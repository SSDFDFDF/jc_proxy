package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

type cancelResponseBody struct{ cancel context.CancelFunc }

func (b cancelResponseBody) Read([]byte) (int, error) {
	b.cancel()
	return 0, context.Canceled
}
func (b cancelResponseBody) Close() error { return nil }

type timeoutEOFBody struct{ blockingReadCloser }

func (b *timeoutEOFBody) Read([]byte) (int, error) {
	<-b.closed
	return 0, io.EOF
}

func TestAttemptOutcomesPersistAfterUpload(t *testing.T) {
	for _, tc := range []struct {
		name, mode                   string
		upstream, downstream         int
		success, failed, interrupted int
	}{
		{"success", "normal", 200, 200, 1, 0, 0},
		{"502", "normal", 502, 502, 0, 1, 0},
		{"504", "normal", 504, 504, 0, 1, 0},
		{"empty-inference-response", "empty", 200, 502, 0, 1, 0},
		{"no-content", "empty", 204, 204, 1, 0, 0},
		{"body-timeout", "stall", 200, 502, 0, 1, 0},
		{"body-timeout-returning-eof", "stall-eof", 200, 502, 0, 1, 0},
		{"body-truncated", "truncated", 200, 502, 0, 1, 0},
		{"timeout-before-headers", "network-timeout", 0, 502, 0, 1, 0},
		{"client-cancel-before-headers", "cancel", 0, 0, 0, 0, 1},
		{"client-deadline-after-upload", "deadline", 0, 0, 0, 0, 1},
		{"client-cancel-during-body", "body-cancel", 200, 0, 0, 0, 1},
		{"known-502-before-cancel", "body-cancel", 502, 0, 0, 1, 0},
		{"known-504-before-body-timeout", "stall", 504, 502, 0, 1, 0},
		{"downstream-write-error", "write-fail", 200, 0, 0, 0, 1},
		{"known-502-before-write-error", "write-fail", 502, 0, 0, 1, 0},
		{"read-error-and-write-error", "read-and-write-fail", 200, 0, 0, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "keys.json")
			store, err := keystore.NewFileStore(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Append("vid_openai", []string{"test-key"}); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Vendors: config.VendorsFromMap(map[string]config.VendorConfig{
				"openai": {Upstream: config.UpstreamConfig{BaseURL: "http://unused.invalid", BodyTimeout: 20 * time.Millisecond}, LoadBalance: "adaptive"},
			})}
			rt, err := NewRuntime(cfg, store)
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			p, err := NewRuntimeStatsPersister(rt, store, RuntimeStatsPersisterOptions{FlushInterval: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.mode == "deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer deadlineCancel()
			}
			uploaded, calls := 0, 0
			rt.Snapshot().vendors["openai"].client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				payload, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				uploaded = len(payload)
				switch tc.mode {
				case "network-timeout":
					return nil, context.DeadlineExceeded
				case "deadline":
					<-req.Context().Done()
					return nil, req.Context().Err()
				case "cancel":
					cancel()
					return nil, context.Canceled
				}
				var body io.ReadCloser = io.NopCloser(strings.NewReader("result"))
				switch tc.mode {
				case "empty":
					body = http.NoBody
				case "stall":
					body = &blockingReadCloser{closed: make(chan struct{})}
				case "stall-eof":
					body = &timeoutEOFBody{blockingReadCloser{closed: make(chan struct{})}}
				case "truncated":
					body = timingErrorBody{}
				case "body-cancel":
					body = cancelResponseBody{cancel}
				case "read-and-write-fail":
					body = &bytesAndErrorBody{}
				}
				return &http.Response{StatusCode: tc.upstream, Header: make(http.Header), Body: body}, nil
			})
			req := httptest.NewRequest("POST", "/openai/v1/chat/completions", strings.NewReader("uploaded")).WithContext(ctx)
			w := httptest.NewRecorder()
			var writer http.ResponseWriter = w
			if tc.mode == "write-fail" || tc.mode == "read-and-write-fail" {
				writer = &brokenPipeWriter{}
			}
			rt.ServeHTTP(writer, req)
			if uploaded != len("uploaded") || calls != 1 {
				t.Fatalf("uploaded=%d calls=%d", uploaded, calls)
			}
			if tc.downstream != 0 && w.Code != tc.downstream {
				t.Fatalf("status=%d, want %d", w.Code, tc.downstream)
			}
			state := rt.Snapshot().vendors["openai"].pool.Snapshot()[0]
			s := state.RuntimeStats
			if state.Inflight != 0 || s.TotalRequests != 1 || s.SuccessCount != tc.success || s.OtherErrorCount != tc.failed || s.InterruptedCount() != tc.interrupted {
				t.Fatalf("accounting=%+v inflight=%d", s, state.Inflight)
			}
			if s.RecentRequests != 1-tc.interrupted || s.HeaderSamples != s.RecentRequests || s.ResponseSamples != s.RecentRequests || s.RecentSuccessCount != tc.success {
				t.Fatalf("samples=%+v", s.RecentStats)
			}
			if tc.failed > 0 && (s.AvgHeaderMS != 999000 || s.AvgResponseMS != 999000 || s.LastError == "") {
				t.Fatalf("missing penalty/diagnostics: %+v", s)
			}
			if tc.upstream >= 400 && s.LastStatus != tc.upstream {
				t.Fatalf("lost known HTTP status: %+v", s)
			}
			if tc.interrupted > 0 && (state.Failures != 0 || !state.CooldownUntil.IsZero() || s.LastError != "") {
				t.Fatalf("client interruption poisoned health: %+v", state)
			}
			for range 2 {
				if err := p.Flush(); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := keystore.NewFileStore(path)
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := NewRuntime(cfg, reopened)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if got := restarted.Snapshot().vendors["openai"].pool.Snapshot()[0].RuntimeStats; got != s {
				t.Fatalf("restart=%+v, want %+v", got, s)
			}
		})
	}
}

type bytesAndErrorBody struct{}

func (*bytesAndErrorBody) Read(p []byte) (int, error) {
	return copy(p, "partial"), io.ErrUnexpectedEOF
}
func (*bytesAndErrorBody) Close() error { return nil }

func TestBodyTimeoutCannotBecomeEOFOrClientCancellation(t *testing.T) {
	body := &timeoutEOFBody{blockingReadCloser{closed: make(chan struct{})}}
	r := &idleTimeoutReadCloser{body: body, timeout: time.Second, reading: true, deadline: time.Now().Add(-time.Second)}
	r.expire() // deterministic expiry of an active read
	_, err := r.Read(make([]byte, 1))
	if !errors.Is(err, errUpstreamBodyTimeout) || errors.Is(err, io.EOF) {
		t.Fatalf("timeout became a successful EOF: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if isCanceledUpstreamError(ctx, err) {
		t.Fatal("client cancellation erased a known upstream timeout")
	}
}

func TestExpectsUpstreamResponseBody(t *testing.T) {
	for _, tc := range []struct {
		method, path, contentType string
		status                    int
		want                      bool
	}{
		{"POST", "/v1/chat/completions", "", 200, true},
		{"POST", "/v1/responses", "", 200, true},
		{"POST", "/v1/messages", "", 200, true},
		{"POST", "/v1/embeddings", "", 200, true},
		{"POST", "/v1beta/models/gemini:generateContent", "", 200, true},
		{"POST", "/v1beta/models/gemini:streamGenerateContent", "", 200, true},
		{"GET", "/models", "application/json; charset=utf-8", 200, true},
		{"GET", "/stream", "text/event-stream", 200, true},
		{"GET", "/value", "application/problem+json", 200, true},
		{"HEAD", "/models", "application/json", 200, false},
		{"OPTIONS", "/models", "application/json", 200, false},
		{"POST", "/v1/chat/completions", "application/json", 204, false},
		{"POST", "/v1/chat/completions", "application/json", 205, false},
		{"GET", "/models", "application/json", 304, false},
		{"GET", "/models", "application/json", 302, false},
		{"POST", "/v1/chat/completions", "application/json", 502, false},
		{"POST", "/ack", "", 200, false},
		{"POST", "/ack", "", 202, false},
	} {
		t.Run(tc.method+tc.path+"/"+http.StatusText(tc.status), func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			resp := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.contentType}}}
			if got := expectsUpstreamResponseBody(req, resp); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
