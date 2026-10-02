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

func TestAttemptFinishExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		action  keyAction
		cancel  bool
		recent  int
		success int
	}{
		{"success", keyActionSuccess, false, 1, 1},
		{"known-success-before-cancellation", keyActionSuccess, true, 1, 1},
		{"error", keyActionObserve, false, 1, 0},
		{"known-error-before-cancellation", keyActionObserve, true, 1, 0},
		{"client-cancelled", keyActionInterrupted, true, 0, 0},
		{"downstream-write-error", keyActionInterrupted, false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, _ := balancer.NewPool("adaptive", []string{"key"})
			idx, key, version, _ := pool.AcquireVersioned(nil, nil)
			v := &vendorGateway{pool: pool}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &upstreamAttempt{idx: idx, selectedKey: key, selectedVersion: version, request: httptest.NewRequest("GET", "/", nil).WithContext(ctx), headerElapsed: 20 * time.Millisecond}
			if tc.cancel {
				cancel()
			}
			a.finish(v, keyDecision{action: tc.action, statusCode: 502, reason: "test error"}, 100*time.Millisecond)
			a.finish(v, keyDecision{action: keyActionInterrupted}, -1)
			a.finish(v, keyDecision{action: keyActionSuccess}, time.Second)
			s := pool.Snapshot()[0]
			if s.TotalRequests != 1 || s.Inflight != 0 || s.RecentRequests != tc.recent || s.HeaderSamples != tc.recent || s.ResponseSamples != tc.recent || s.RecentSuccessCount != tc.success {
				t.Fatalf("summary = %+v", s)
			}
			if tc.success > 0 && (s.AvgHeaderMS != 20 || s.AvgResponseMS != 100) {
				t.Fatalf("success timing = %+v", s)
			}
			if tc.recent > tc.success && (s.AvgHeaderMS != 999000 || s.AvgResponseMS != 999000 || s.OtherErrorCount != 1) {
				t.Fatalf("failure penalty = %+v", s)
			}
			if tc.recent == 0 && (s.InterruptedCount() != 1 || s.OtherErrorCount != 0 || s.LastError != "") {
				t.Fatalf("interrupted attempt = %+v", s)
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
	if w.Code != 204 || calls != 2 || s[0].RecentRequests != 1 || s[0].HeaderSamples != 1 || s[0].AvgHeaderMS != 999000 || s[0].AvgResponseMS != 999000 || s[1].RecentSuccessCount != 1 || s[1].ResponseSamples != 1 {
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
