package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/balancer"
	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
)

type timingErrorBody struct{}

func (timingErrorBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (timingErrorBody) Close() error             { return nil }

func TestAttemptTimingCompletionPaths(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		status                  int
		read, broken, cancel    bool
		requests, full, success int
	}{
		{"success", 200, true, false, false, 1, 1, 1},
		{"no-content", 204, true, false, false, 1, 1, 1},
		{"error-forwarded", 429, true, false, false, 1, 1, 0},
		{"retry-discard", 429, false, false, false, 1, 0, 0},
		{"truncated", 200, true, true, false, 1, 0, 0},
		{"downstream-write-error", 200, false, false, false, 0, 0, 0},
		{"cancelled", 200, true, false, true, 0, 0, 0},
		{"cancelled-error", 500, true, true, true, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, _ := balancer.NewPool("adaptive", []string{"key"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &upstreamAttempt{idx: 0, vendor: &vendorGateway{pool: pool}, request: httptest.NewRequest("GET", "/", nil).WithContext(ctx), body: io.NopCloser(strings.NewReader("body")), responseStatus: tc.status, started: time.Now().Add(-100 * time.Millisecond), headerElapsed: 20 * time.Millisecond}
			if tc.broken {
				a.body = timingErrorBody{}
			}
			if tc.read {
				_, _ = io.Copy(io.Discard, a)
			}
			if tc.cancel {
				cancel()
			}
			_ = a.Close()
			_ = a.Close() // exactly once even with redundant cleanup
			s := pool.Snapshot()[0].RecentStats
			if s.RecentRequests != tc.requests || s.ResponseSamples != tc.full || s.RecentSuccessCount != tc.success {
				t.Fatalf("summary = %+v", s)
			}
			if tc.requests > 0 && (s.HeaderSamples != 1 || s.AvgHeaderMS != 20) {
				t.Fatalf("header timing = %+v", s)
			}
			if tc.full > 0 && s.AvgResponseMS < 100 {
				t.Fatalf("full timing = %+v", s)
			}
		})
	}
}

type countingTimingStore struct {
	*keystore.FileStore
	calls int
	fail  bool
}

func (s *countingTimingStore) ApplyRuntimeStatsDeltas(d map[string][]keystore.RuntimeStatsDelta) error {
	s.calls++
	if s.fail {
		return errors.New("test persistence failure")
	}
	return s.FileStore.ApplyRuntimeStatsDeltas(d)
}

func TestTimingBatchPersistenceReloadAndRetry(t *testing.T) {
	file, err := keystore.NewFileStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	store := &countingTimingStore{FileStore: file}
	if _, err := store.Append("vid_openai", []string{"k"}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Vendors: config.VendorsFromMap(map[string]config.VendorConfig{"openai": {Upstream: config.UpstreamConfig{BaseURL: "http://unused.invalid"}, LoadBalance: "adaptive"}})}
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
	rt.Snapshot().vendors["openai"].client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: ok\n\n"))}, nil
	})
	for range 7 {
		w := httptest.NewRecorder()
		rt.ServeHTTP(w, httptest.NewRequest("GET", "/openai/test", nil))
		if w.Code != 200 || w.Body.String() != "data: ok\n\n" {
			t.Fatalf("response = %d %s", w.Code, w.Body)
		}
	}
	if store.calls != 0 {
		t.Fatal("request path performed database writes")
	}
	before := rt.Snapshot().vendors["openai"].pool.Snapshot()[0].RuntimeStats
	if before.RecentRequests != 5 || before.ResponseSamples != 5 || before.AvgResponseMS < before.AvgHeaderMS {
		t.Fatalf("timings = %+v", before)
	}
	if err := rt.RefreshKeys(); err != nil {
		t.Fatal(err)
	}
	after := rt.Snapshot().vendors["openai"].pool.Snapshot()[0].RuntimeStats
	if after != before {
		t.Fatal("reload lost live window")
	}
	store.fail = true
	if err := p.Flush(); err == nil {
		t.Fatal("expected flush failure")
	}
	store.fail = false
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	if store.calls != 2 {
		t.Fatalf("unchanged data written again: %d", store.calls)
	}
	records, err := store.List("vid_openai")
	if err != nil {
		t.Fatal(err)
	}
	if records[0].RuntimeStats != before {
		t.Fatalf("stored = %+v, want %+v", records[0].RuntimeStats, before)
	}
	restarted, err := NewRuntime(cfg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.Snapshot().vendors["openai"].pool.Snapshot()[0].RuntimeStats != before {
		t.Fatal("restart lost summary")
	}
}

func TestTimingNetworkFailoverRecordsEachKey(t *testing.T) {
	cfg := &config.Config{Vendors: config.VendorsFromMap(map[string]config.VendorConfig{"openai": {Upstream: config.UpstreamConfig{BaseURL: "http://unused.invalid"}}})}
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatal(err)
	}
	r, err := newTestRouter(cfg, map[string][]string{"openai": {"k1", "k2"}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.vendors["openai"].client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("dial failed")
		}
		return &http.Response{StatusCode: 204, Header: make(http.Header), Body: http.NoBody}, nil
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/openai/test", nil))
	s := r.vendors["openai"].pool.Snapshot()
	if w.Code != 204 || calls != 2 || s[0].RecentRequests != 1 || s[0].HeaderSamples != 0 || s[1].RecentSuccessCount != 1 || s[1].ResponseSamples != 1 {
		t.Fatalf("calls=%d, code=%d, stats=%+v", calls, w.Code, s)
	}
}

func TestDecompressedErrorPreviewIsBounded(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write(bytes.Repeat([]byte("x"), 1<<20))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	preview := maybeDecompressPreview(compressed.Bytes(), http.Header{"Content-Encoding": {"gzip"}})
	if len(preview) != 64<<10 {
		t.Fatalf("decoded preview length = %d", len(preview))
	}
}

func BenchmarkResponseCopyBufferStreaming(b *testing.B) {
	r := newBenchRouter(b, "http://unused.invalid")
	b.ReportAllocs()
	for b.Loop() {
		buf, release := r.responseCopyBuffer(true)
		if len(buf) != 4096 {
			b.Fatal(len(buf))
		}
		release()
	}
}
