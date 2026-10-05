package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jc_proxy/internal/balancer"
)

// Exercise Readers that return the final bytes and EOF in the same call as
// well as strings.Reader, which reports EOF on the following Read.
type finalChunkEOFBody struct{ remaining string }

func (b *finalChunkEOFBody) Read(p []byte) (int, error) {
	n := copy(p, b.remaining)
	b.remaining = b.remaining[n:]
	if b.remaining == "" {
		return n, io.EOF
	}
	return n, nil
}
func (*finalChunkEOFBody) Close() error { return nil }

type cancelAtEOFBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelAtEOFBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.cancel()
	}
	return n, err
}

// Buffered downstream writes/flushes can succeed even after cancellation.
// Cancellation, not a returned I/O error, must exclude the pending timings.
type cancelAtCompletionWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	stage  string
}

func (w *cancelAtCompletionWriter) WriteHeader(status int) {
	w.ResponseRecorder.WriteHeader(status)
	if w.stage == "headers" {
		w.cancel()
	}
}
func (w *cancelAtCompletionWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if w.stage == "write" {
		w.cancel()
	}
	return n, err
}
func (w *cancelAtCompletionWriter) FlushError() error {
	w.ResponseRecorder.Flush()
	if w.stage == "flush" {
		w.cancel()
	}
	return nil
}

func TestCancellationAtResponseCompletionExcludesTimings(t *testing.T) {
	for _, strategy := range []string{"lowest_latency", "adaptive"} {
		for _, tc := range []struct {
			name, stage string
			streaming   bool
			empty       bool
			noContent   bool
			combinedEOF bool
			interrupted bool
		}{
			{name: "success"},
			{name: "cancel-on-eof", stage: "read", interrupted: true},
			{name: "cancel-with-final-bytes", stage: "read", combinedEOF: true, interrupted: true},
			{name: "cancel-on-buffered-write", stage: "write", interrupted: true},
			{name: "cancel-on-final-write", stage: "write", combinedEOF: true, interrupted: true},
			{name: "cancel-on-stream-flush", stage: "flush", streaming: true, interrupted: true},
			{name: "cancel-on-final-stream-flush", stage: "flush", streaming: true, combinedEOF: true, interrupted: true},
			{name: "cancel-on-empty-json-eof", stage: "read", empty: true, interrupted: true},
			{name: "cancel-on-empty-stream-eof", stage: "read", streaming: true, empty: true, interrupted: true},
			{name: "cancel-on-empty-headers", stage: "headers", empty: true, noContent: true, interrupted: true},
			{name: "cancel-on-empty-flush", stage: "flush", streaming: true, empty: true, noContent: true, interrupted: true},
			{name: "cancel-after-settlement", stage: "settled"},
		} {
			t.Run(strategy+"/"+tc.name, func(t *testing.T) {
				r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, nil)
				v := r.vendors["openai"]
				v.interimInterval = 0
				pool, err := balancer.NewPool(strategy, []string{"key"})
				if err != nil {
					t.Fatal(err)
				}
				v.pool = pool
				// Keep an existing window: cancellation must neither add a
				// partial sample nor erase previously completed timings.
				idx, _, version, ok := pool.AcquireVersioned(nil, nil)
				if !ok {
					t.Fatal("acquire failed")
				}
				pool.CompleteAttempt(idx, version, balancer.AttemptResult{
					Action: balancer.AttemptSuccess, HeaderElapsed: 20 * time.Millisecond, FullElapsed: 100 * time.Millisecond,
				})
				before := pool.Snapshot()[0]

				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				v.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
					var body io.ReadCloser = io.NopCloser(strings.NewReader("result"))
					if tc.combinedEOF {
						body = &finalChunkEOFBody{remaining: "result"}
					}
					if tc.empty {
						body = http.NoBody
					}
					if tc.stage == "read" {
						body = &cancelAtEOFBody{ReadCloser: body, cancel: cancel}
					}
					contentType, status := "application/json", http.StatusOK
					if tc.streaming {
						contentType = "text/event-stream"
					}
					if tc.noContent {
						status = http.StatusNoContent
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: body}, nil
				})
				w := &cancelAtCompletionWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, stage: tc.stage}
				r.ServeHTTP(w, httptest.NewRequest("POST", "/openai/v1/responses", nil).WithContext(ctx))
				if tc.stage == "settled" {
					cancel()
				}
				if tc.stage != "" && ctx.Err() != context.Canceled {
					t.Fatal("fixture did not cancel the downstream request")
				}
				after := pool.Snapshot()[0]
				if after.Inflight != 0 || after.TotalRequests != before.TotalRequests+1 || after.Failures != before.Failures || after.CooldownUntil != before.CooldownUntil {
					t.Fatalf("incorrect settlement: %+v", after)
				}
				if tc.interrupted {
					want := before.RuntimeStats
					want.TotalRequests++
					if after.RuntimeStats != want {
						t.Fatalf("cancellation changed timing/health stats: got %+v, want %+v", after.RuntimeStats, want)
					}
				} else if after.SuccessCount != before.SuccessCount+1 || after.InterruptedCount() != 0 || after.RecentSuccessCount != 2 || after.HeaderSamples != 2 || after.ResponseSamples != 2 {
					t.Fatalf("completed response lost its timings: %+v", after)
				}
			})
		}
	}
}
