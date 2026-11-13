package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"jc_proxy/internal/config"
)

func TestInterimPreservesFinalStatusOnRealHTTP(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		name := "http1"
		if h2 {
			name = "http2"
		}
		for _, status := range []int{200, 502, 504} {
			t.Run(name+"/"+http.StatusText(status), func(t *testing.T) {
				release := make(chan struct{})
				var once sync.Once
				r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, nil)
				r.vendors["openai"].interimInterval = 5 * time.Millisecond
				r.vendors["openai"].client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					select {
					case <-release:
					case <-req.Context().Done():
						return nil, req.Context().Err()
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"result":"test"}`))}, nil
				})
				ts := httptest.NewUnstartedServer(r)
				if h2 {
					ts.EnableHTTP2 = true
					ts.StartTLS()
				} else {
					ts.Start()
				}
				defer ts.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
					if code == 102 {
						once.Do(func() { close(release) })
					}
					return nil
				}}
				req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", ts.URL+"/openai/models", nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := ts.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				wantProto := 1
				if h2 {
					wantProto = 2
				}
				if resp.ProtoMajor != wantProto || resp.StatusCode != status || string(body) != `{"result":"test"}` {
					t.Fatalf("protocol=%s status=%d body=%s", resp.Proto, resp.StatusCode, body)
				}
				select {
				case <-release:
				default:
					t.Fatal("did not receive the informational response")
				}
			})
		}
	}
}

func TestDisabledInterimIsNilAndSafe(t *testing.T) {
	s := newInterimResponseSender(httptest.NewRecorder(), 0)
	if s != nil {
		t.Fatal("disabled interim sender should not allocate")
	}
	calls := 0
	s.commitFinal(func() { calls++ })
	s.stop()
	if calls != 1 {
		t.Fatal("nil sender lost final response")
	}
}

type responseEventWriter struct {
	*httptest.ResponseRecorder
	events []string
}

func (w *responseEventWriter) WriteHeader(code int) {
	w.events = append(w.events, "header")
	w.ResponseRecorder.WriteHeader(code)
}
func (w *responseEventWriter) Write(p []byte) (int, error) {
	w.events = append(w.events, "write")
	return w.ResponseRecorder.Write(p)
}
func (w *responseEventWriter) Flush() {
	w.events = append(w.events, "flush")
	w.ResponseRecorder.Flush()
}

func TestStreamingFlushesEachChunkOnceAndEmptyResponse(t *testing.T) {
	for _, empty := range []bool{false, true} {
		name := "chunks"
		if empty {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, nil)
			r.vendors["openai"].interimInterval = 0
			r.vendors["openai"].client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				var body io.ReadCloser = &stagedReadCloser{chunks: [][]byte{[]byte("data: first\n\n"), []byte("data: second\n\n")}}
				status := 200
				if empty {
					body, status = http.NoBody, 204
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
			})
			w := &responseEventWriter{ResponseRecorder: httptest.NewRecorder()}
			r.ServeHTTP(w, httptest.NewRequest("GET", "/openai/stream", nil))
			want := []string{"header", "write", "flush", "write", "flush"}
			if empty {
				want = []string{"header", "flush"}
			}
			if !reflect.DeepEqual(w.events, want) {
				t.Fatalf("events=%v, want %v", w.events, want)
			}
		})
	}
}

// After the immediate preview, optionally trickle bytes. Close always unblocks
// Read, as required of http.Response.Body. A total drain deadline must work even
// when progress continually resets the ordinary upstream read timeout.
type discardedErrorBody struct {
	prefix *strings.Reader
	ticker *time.Ticker
	closed chan struct{}
	once   sync.Once
}

func (b *discardedErrorBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	var tick <-chan time.Time
	if b.ticker != nil {
		tick = b.ticker.C
	}
	select {
	case <-b.closed:
		return 0, io.ErrClosedPipe
	case <-tick:
		p[0] = 'x'
		return 1, nil
	}
}
func (b *discardedErrorBody) Close() error {
	b.once.Do(func() {
		if b.ticker != nil {
			b.ticker.Stop()
		}
		close(b.closed)
	})
	return nil
}

func TestMaskedBodyDrainHasTotalTimeBudget(t *testing.T) {
	for _, trickle := range []bool{false, true} {
		name := "stalled"
		if trickle {
			name = "trickling"
		}
		t.Run(name, func(t *testing.T) {
			body := &discardedErrorBody{prefix: strings.NewReader(strings.Repeat("x", 2048)), closed: make(chan struct{})}
			if trickle {
				body.ticker = time.NewTicker(5 * time.Millisecond)
			}
			defer body.Close()
			r, _ := routerWithMasking(t, "http://unused.invalid", []string{"key"}, func(p *config.ErrorPolicyConfig) {
				p.Masking.Rules = []config.ErrorMaskingRule{{StatusCodes: []int{502}, StatusCode: 500}}
			})
			r.vendors["openai"].interimInterval = 0
			r.vendors["openai"].upstreamBodyTimeout = 2 * time.Second
			r.vendors["openai"].client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 502, Header: make(http.Header), Body: body}, nil
			})
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { r.ServeHTTP(w, httptest.NewRequest("POST", "/openai/test", nil)); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				_ = body.Close()
				<-done
				t.Fatal("discarded body held the response beyond its drain budget")
			}
			if w.Code != 500 {
				t.Fatalf("masked status=%d", w.Code)
			}
			s := r.vendors["openai"].pool.Snapshot()[0]
			if s.TotalRequests != 1 || s.OtherErrorCount != 1 || s.LastStatus != 502 || s.RecentRequests != 1 || s.AvgHeaderMS != 999000 || s.InterruptedCount() != 0 {
				t.Fatalf("cleanup changed or duplicated the original outcome: %+v", s)
			}
		})
	}
}

func TestMaskedDrainClosesRealUpstreamBody(t *testing.T) {
	upstreamDone := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(502)
		_, _ = io.WriteString(w, strings.Repeat("x", 2048))
		w.(http.Flusher).Flush()
		<-req.Context().Done() // no EOF: the proxy must abandon the connection
		close(upstreamDone)
	}))
	defer upstream.Close()
	r, _ := routerWithMasking(t, upstream.URL, []string{"key"}, func(p *config.ErrorPolicyConfig) {
		p.Masking.Rules = []config.ErrorMaskingRule{{StatusCodes: []int{502}, StatusCode: 500}}
	})
	r.vendors["openai"].interimInterval = 0
	proxy := httptest.NewServer(r)
	defer proxy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", proxy.URL+"/openai/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := proxy.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 500 || !strings.Contains(string(body), "gateway_error") {
		t.Fatalf("response=%d %q, error=%v", resp.StatusCode, body, err)
	}
	select {
	case <-upstreamDone:
	case <-time.After(time.Second):
		t.Fatal("masked upstream connection was not closed")
	}
}

func TestCapturedPreviewCompletionAndReplay(t *testing.T) {
	for _, size := range []int{0, 8, 2048, 4096} {
		payload := strings.Repeat("x", size)
		body := io.NopCloser(strings.NewReader(payload))
		p, err := captureResponsePreview(body, 2048, nil)
		if err != nil {
			t.Fatal(err)
		}
		if p.complete != (size < 2048) {
			t.Fatalf("size=%d complete=%v", size, p.complete)
		}
		got, err := io.ReadAll(p.replay(body))
		if err != nil || string(got) != payload {
			t.Fatalf("size=%d replay mismatch: %v", size, err)
		}
	}
}

func TestSharedErrorSummaryPreservesClassificationAndMaskPriority(t *testing.T) {
	policy := config.ErrorPolicyConfig{
		AutoDisable: config.ErrorAutoDisableConfig{StatusCodes: []int{401}, Keywords: []string{"invalid_api_key"}},
		Masking: config.ErrorMaskingConfig{Rules: []config.ErrorMaskingRule{
			{StatusCodes: []int{429}, Keywords: []string{"quota"}, StatusCode: 503, Cooldown: time.Minute},
			{Keywords: []string{"quota"}, StatusCode: 500},
			{StatusCodes: []int{502}, StatusCode: 500},
		}},
	}
	for _, status := range []int{400, 401, 429, 502, 503} {
		for _, text := range []string{`{"error":{"message":"quota exceeded","type":"invalid_api_key"}}`, "slow down", "quota exceeded", ""} {
			headers := http.Header{"Retry-After": {"20"}}
			preview := []byte(text)
			wantDecision := classifyResponse("openai", policy, status, headers, preview)
			body, _ := summarizeResponsePreview(headers, preview)
			wantRule, wantMatched := matchUpstreamErrorMask(policy, status, body)
			if wantMatched {
				wantDecision = extendDecisionCooldown(wantDecision, wantRule)
			}
			decision, rule, matched := analyzeErrorResponse("openai", policy, status, headers, preview)
			if decision != wantDecision || matched != wantMatched || !reflect.DeepEqual(rule, wantRule) {
				t.Fatalf("shared analysis changed semantics for status=%d text=%q", status, text)
			}
		}
	}
}
