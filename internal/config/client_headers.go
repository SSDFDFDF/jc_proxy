package config

import (
	"net/textproto"
	"sort"
	"strings"
)

var defaultClientHeaderDropList = []string{
	"Cdn-Loop",
	"Cf-Connecting-Ip",
	"Cf-Ipcountry",
	"Cf-Ray",
	"Cf-Visitor",
	"Fastly-Client-Ip",
	"Fly-Client-Ip",
	"Forwarded",
	"True-Client-Ip",
	"Via",
	"X-Amzn-Trace-Id",
	"X-Correlation-Id",
	"X-Envoy-Decorator-Operation",
	"X-Envoy-External-Address",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Port",
	"X-Forwarded-Proto",
	"X-Forwarded-Scheme",
	"X-Forwarded-Server",
	"X-Original-Forwarded-For",
	"X-Original-Host",
	"X-Real-Ip",
	"X-Request-Id",
	"X-Request-Start",
	"X-Rewrite-Url",
}

var clientHeaderAllowlistPresets = map[string][]string{
	"anthropic": {
		"Accept",
		"Accept-Encoding",
		"Anthropic-Beta",
		"Anthropic-Version",
		"Content-Type",
		"User-Agent",
	},
	"gemini": {
		"Accept",
		"Accept-Encoding",
		"Content-Type",
		"User-Agent",
		"X-Goog-Api-Client",
	},
	"generic_ai": {
		"Accept",
		"Accept-Encoding",
		"Cache-Control",
		"Content-Type",
		"Idempotency-Key",
		"User-Agent",
	},
	"openai": {
		"Accept",
		"Accept-Encoding",
		"Cache-Control",
		"Content-Type",
		"Idempotency-Key",
		"Openai-Beta",
		"Openai-Organization",
		"Openai-Project",
		"User-Agent",
	},
	"openai_compatible": {
		"Accept",
		"Accept-Encoding",
		"Cache-Control",
		"Content-Type",
		"Idempotency-Key",
		"Openai-Beta",
		"Openai-Organization",
		"Openai-Project",
		"User-Agent",
		"X-Title",
	},
}

func DefaultClientHeaderDropList() []string {
	return append([]string(nil), defaultClientHeaderDropList...)
}

func ClientHeaderAllowlistPreset(name string) ([]string, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	headers, ok := clientHeaderAllowlistPresets[key]
	if !ok {
		return nil, false
	}
	return append([]string(nil), headers...), true
}

func ResolveClientHeaderAllowlist(cfg ClientHeadersConfig) []string {
	values := make([]string, 0, len(cfg.Allowlist)+8)
	if preset, ok := ClientHeaderAllowlistPreset(cfg.Preset); ok {
		values = append(values, preset...)
	}
	values = append(values, cfg.Allowlist...)
	return normalizeCanonicalHeaderNames(values)
}

func ResolveClientHeaderDropList(cfg ClientHeadersConfig) []string {
	values := append(DefaultClientHeaderDropList(), cfg.Drop...)
	return normalizeCanonicalHeaderNames(values)
}

func normalizeCanonicalHeaderNames(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		canon := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(value))
		if canon == "" {
			continue
		}
		if _, ok := seen[canon]; ok {
			continue
		}
		seen[canon] = struct{}{}
		out = append(out, canon)
	}
	sort.Strings(out)
	return out
}
