package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"jc_proxy/internal/config"
)

func TestTargetURLFastAndComponentPathsAgree(t *testing.T) {
	for _, base := range []string{"https://example.invalid/v1", "https://example.invalid/base%2Fid/", "https://example.invalid/base%2F", "https://example.invalid/base%2f?fixed=1", "https://example.invalid/v1?next=/a/", "https://example.invalid/v1?", "https://example.invalid/v1?x=base"} {
		for _, path := range []string{"/models", "/中文/a b", "/literal%text"} {
			u, err := url.Parse(base)
			if err != nil {
				t.Fatal(err)
			}
			v := &vendorGateway{baseURL: u, baseURLPrefix: u.String()}
			fast, err := v.buildTargetURL(path, "x=client&v=a%2Fb", "key")
			if err != nil {
				t.Fatal(err)
			}
			component, err := buildTargetURLFromBase(u, path, "x=client&v=a%2Fb", "key", nil)
			if err != nil {
				t.Fatal(err)
			}
			if fast != component {
				t.Fatalf("base=%s path=%s fast=%s component=%s", base, path, fast, component)
			}
		}
	}
}

func TestRouterPreservesEncodedSlashAtURLJoin(t *testing.T) {
	for _, tc := range []struct {
		base, incoming, want string
	}{
		{"/base%2F", "/files/a%2Fb", "/base%2F/files/a%2Fb"},
		{"/base%2f?fixed=1", "/files/a%2fb?x=2", "/base%2f/files/a%2fb?fixed=1&x=2"},
		{"/base%2F/", "/files/a%2Fb", "/base%2F/files/a%2Fb"},
		{"/base%2F", "/%2Ffiles/a%2Fb", "/base%2F/%2Ffiles/a%2Fb"},
		{"/base/", "/%2Ffiles/a%2Fb", "/base/%2Ffiles/a%2Fb"},
	} {
		t.Run(tc.base+tc.incoming, func(t *testing.T) {
			r, _ := routerWithMasking(t, "http://unused.invalid"+tc.base, []string{"key"}, nil)
			var target string
			r.vendors["openai"].client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				target = req.URL.String()
				return &http.Response{StatusCode: 204, Header: make(http.Header), Body: http.NoBody}, nil
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/openai"+tc.incoming, nil))
			if want := "http://unused.invalid" + tc.want; w.Code != 204 || target != want {
				t.Fatalf("status=%d target=%q, want %q", w.Code, target, want)
			}
		})
	}
}

func TestAggregateRetryKeepsOriginalEscapedPath(t *testing.T) {
	cfg := newAggregateRetryTestConfig(config.AggregateRetryConfig{MaxAttempts: 2})
	mutateTestVendor(t, cfg, "child_a", func(v *config.VendorConfig) { v.PathRewrites = map[string]string{"/v1/*": "/a/*"} })
	mutateTestVendor(t, cfg, "child_b", func(v *config.VendorConfig) { v.PathRewrites = map[string]string{"/v1/*": "/b/*"} })
	if err := cfg.PrepareAndValidate(); err != nil {
		t.Fatal(err)
	}
	r, err := newTestRouter(cfg, aggregateTestSeed())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.EscapedPath())
		status := 429
		if len(paths) > 1 {
			status = 200
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("result"))}, nil
	})
	r.vendors["child_a"].client.Transport = transport
	r.vendors["child_b"].client.Transport = transport
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/agg/v1/files/a%2Fb", nil))
	if w.Code != 200 || !reflect.DeepEqual(paths, []string{"/a/files/a%2Fb", "/b/files/a%2Fb"}) {
		t.Fatalf("status=%d paths=%v", w.Code, paths)
	}
}

func TestVendorDiagnosticsPreserveQueryAndEscaping(t *testing.T) {
	r, cfg := routerWithMasking(t, "http://unused.invalid/base%2Fid?next=/a/", []string{"key"}, nil)
	var target *url.URL
	r.vendors["openai"].client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		target = req.URL
		return &http.Response{StatusCode: 204, Header: make(http.Header), Body: http.NoBody}, nil
	})
	_, err := r.ExecuteVendorTest(context.Background(), vendorID(t, cfg, "openai"), VendorTestRequest{Endpoint: "files/x%2Fy?q=1"})
	if err != nil {
		t.Fatal(err)
	}
	if target.EscapedPath() != "/base%2Fid/files/x%2Fy" || target.RawQuery != "next=/a/&q=1" {
		t.Fatalf("target=%s", target)
	}
}
