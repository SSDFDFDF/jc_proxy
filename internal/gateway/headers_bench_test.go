package gateway

import (
	"net/http"
	"testing"

	"jc_proxy/internal/config"
)

func newBenchVendorGateway(allowlist, drop []string, inject map[string]string) *vendorGateway {
	return &vendorGateway{
		name:          "openai",
		provider:      "openai",
		allowlist:     headerNameSet(allowlist),
		dropHeaders:   headerNameSet(drop),
		injectHeaders: inject,
		upstreamAuth:  config.UpstreamAuthConfig{Mode: "bearer", Header: "Authorization", Prefix: "Bearer "},
	}
}

func benchHeadersInput() http.Header {
	h := make(http.Header, 16)
	h.Set("Authorization", "Bearer client-supplied")
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("User-Agent", "jc-proxy-bench/1.0")
	h.Set("X-Request-Id", "abcdef0123456789")
	h.Set("X-Forwarded-For", "10.0.0.1")
	h.Set("Accept-Encoding", "gzip, br")
	h.Set("Accept-Language", "en-US,en;q=0.9")
	h.Set("Cache-Control", "no-cache")
	h.Set("Pragma", "no-cache")
	h.Set("Origin", "https://example.com")
	h.Set("Referer", "https://example.com/page")
	h.Set("X-Custom-1", "v1")
	h.Set("X-Custom-2", "v2")
	h.Set("X-Custom-3", "v3")
	return h
}

func BenchmarkForwardHeaders_NoAllowlist(b *testing.B) {
	vg := newBenchVendorGateway(nil, nil, nil)
	src := benchHeadersInput()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = vg.forwardHeaders(src, "sk-test-1234567890")
	}
}

func BenchmarkForwardHeaders_WithAllowlist(b *testing.B) {
	vg := newBenchVendorGateway(
		[]string{"Content-Type", "Accept", "User-Agent", "X-Request-Id", "X-Custom-1"},
		nil, nil,
	)
	src := benchHeadersInput()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = vg.forwardHeaders(src, "sk-test-1234567890")
	}
}

func BenchmarkExtraHopTokens_Empty(b *testing.B) {
	src := benchHeadersInput()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = extraHopTokens(src)
	}
}

func BenchmarkExtraHopTokens_WithConnection(b *testing.B) {
	src := benchHeadersInput()
	src.Set("Connection", "keep-alive, X-Custom-2")
	src.Set("Trailer", "X-Trailer-1")

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = extraHopTokens(src)
	}
}
