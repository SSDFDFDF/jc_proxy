package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUploadDeadlineUnblocksSlowClient(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		name := "http1"
		if h2 {
			name = "http2"
		}
		t.Run(name, func(t *testing.T) {
			prefix, peerEntered, releasePeer := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/peer" {
					close(peerEntered)
					select {
					case <-releasePeer:
						_, _ = io.WriteString(w, "peer survived")
					case <-req.Context().Done():
					}
					return
				}
				if _, err := io.CopyN(io.Discard, req.Body, 32<<10); err == nil {
					close(prefix)
				}
				_, _ = io.Copy(io.Discard, req.Body)
			}))
			defer upstream.Close()

			r, _ := routerWithMasking(t, upstream.URL, []string{"key"}, nil)
			v := r.vendors["openai"]
			v.interimInterval = 0
			v.upstreamUploadTimeout = 200 * time.Millisecond
			settled := make(chan struct{})
			addresses := make(chan string, 2)
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if h2 != (req.ProtoMajor == 2) {
					t.Errorf("unexpected downstream protocol: %s", req.Proto)
				}
				addresses <- req.RemoteAddr
				if req.URL.Path == "/openai/upload" {
					defer close(settled)
				}
				r.ServeHTTP(w, req)
			}))
			// The watchdog must settle well before this independent server limit.
			proxy.Config.ReadTimeout = 5 * time.Second
			proxy.EnableHTTP2 = h2
			if h2 {
				proxy.StartTLS()
			} else {
				proxy.Start()
			}
			defer proxy.Close()
			client := proxy.Client()
			client.Timeout = 5 * time.Second
			client.Transport.(*http.Transport).MaxConnsPerHost = 1

			body, input := io.Pipe()
			defer body.Close()
			defer input.Close()
			req, err := http.NewRequest("POST", proxy.URL+"/openai/upload", body)
			if err != nil {
				t.Fatal(err)
			}
			req.ContentLength = 16 << 20
			type outcome struct {
				status int
				body   string
				err    error
			}
			uploadDone := make(chan outcome, 1)
			go func() {
				resp, err := client.Do(req)
				if err != nil {
					uploadDone <- outcome{err: err}
					return
				}
				defer resp.Body.Close()
				text, err := io.ReadAll(resp.Body)
				uploadDone <- outcome{status: resp.StatusCode, body: string(text), err: err}
			}()
			go func() {
				// Keep the pipe open after the prefix; neither a completed body nor
				// a client disconnect may be responsible for ending the attempt.
				_, _ = io.WriteString(input, strings.Repeat("x", 64<<10))
			}()
			select {
			case <-prefix:
			case <-time.After(time.Second):
				t.Fatal("upstream did not receive the streaming prefix")
			}
			uploadAddress := <-addresses

			peerDone := make(chan outcome, 1)
			if h2 {
				go func() {
					resp, err := client.Get(proxy.URL + "/openai/peer")
					if err != nil {
						peerDone <- outcome{err: err}
						return
					}
					defer resp.Body.Close()
					text, err := io.ReadAll(resp.Body)
					peerDone <- outcome{status: resp.StatusCode, body: string(text), err: err}
				}()
				// Release the sibling even if an assertion fails.
				defer close(releasePeer)
				select {
				case <-peerEntered:
				case <-time.After(time.Second):
					t.Fatal("HTTP/2 sibling did not start")
				}
				if peerAddress := <-addresses; peerAddress != uploadAddress {
					t.Fatalf("requests did not share an HTTP/2 connection: %s != %s", peerAddress, uploadAddress)
				}
			}

			select {
			case <-settled:
			case <-time.After(time.Second):
				t.Fatal("upload watchdog did not unblock the client body read")
			}
			select {
			case result := <-uploadDone:
				if result.err != nil || result.status != http.StatusBadRequest {
					t.Fatalf("slow upload response: %+v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("slow upload received no final response")
			}
			s := v.pool.Snapshot()[0]
			wantInflight := 0
			if h2 {
				wantInflight = 1 // sibling still running on the same connection
			}
			if s.TotalRequests != 1 || s.InterruptedCount() != 1 || s.OtherErrorCount != 0 || s.RecentRequests != 0 || s.Failures != 0 || !s.CooldownUntil.IsZero() || s.Inflight != wantInflight {
				t.Fatalf("slow client poisoned key or leaked an attempt: %+v", s)
			}
			if h2 {
				select {
				case result := <-peerDone:
					t.Fatalf("upload timeout terminated sibling stream: %+v", result)
				default:
				}
				// A token releases the peer now; the deferred close handles failures.
				releasePeer <- struct{}{}
				select {
				case result := <-peerDone:
					if result.err != nil || result.status != 200 || result.body != "peer survived" {
						t.Fatalf("HTTP/2 sibling failed: %+v", result)
					}
				case <-time.After(time.Second):
					t.Fatal("HTTP/2 sibling did not finish")
				}
			}
		})
	}
}
