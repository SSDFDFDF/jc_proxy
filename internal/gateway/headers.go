package gateway

import (
	"net/http"
	"net/textproto"
	"strings"

	"jc_proxy/internal/resin"
)

var hopHeaders = map[string]struct{}{
	"connection":          {},
	"proxy-connection":    {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"trailers":            {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

func (v *vendorGateway) forwardHeaders(src http.Header, selectedKey string) http.Header {
	headers := make(http.Header, len(src)+4)
	connectionHeaders := extraHopTokens(src)
	if len(v.allowlist) == 0 {
		for k, vals := range src {
			canon := textproto.CanonicalMIMEHeaderKey(k)
			if v.shouldDropClientHeader(canon) || isConnectionScopedHopHeader(canon, connectionHeaders) {
				continue
			}
			for _, val := range vals {
				headers.Add(canon, val)
			}
		}
	} else {
		for k, vals := range src {
			canon := textproto.CanonicalMIMEHeaderKey(k)
			if v.shouldDropClientHeader(canon) || isConnectionScopedHopHeader(canon, connectionHeaders) {
				continue
			}
			if _, ok := v.allowlist[canon]; !ok && !v.isPassthroughAuthHeader(canon) {
				continue
			}
			for _, val := range vals {
				headers.Add(canon, val)
			}
		}
	}

	if v.upstreamAuth.Mode != "passthrough" {
		headers.Del("Authorization")
		headers.Del("X-Api-Key")
	}

	for k, val := range v.injectHeaders {
		replaced := strings.ReplaceAll(val, "{{vendor}}", v.name)
		replaced = strings.ReplaceAll(replaced, "{{upstream_key}}", selectedKey)
		headers.Set(k, replaced)
	}

	switch v.upstreamAuth.Mode {
	case "bearer", "header":
		headers.Set(v.upstreamAuth.Header, v.upstreamAuth.Prefix+selectedKey)
	case "passthrough":
	}

	if v.resinRuntime != nil && selectedKey != "" {
		headers.Set(resin.AccountHeader, resin.BuildAccount(v.provider, selectedKey))
	}

	headers.Del("Host")
	return headers
}

func (v *vendorGateway) shouldDropClientHeader(name string) bool {
	if len(v.dropHeaders) == 0 {
		return false
	}
	_, ok := v.dropHeaders[textproto.CanonicalMIMEHeaderKey(name)]
	return ok
}

func copyResponseHeaders(dst, src http.Header) {
	connectionHeaders := extraHopTokens(src)
	for k, vv := range src {
		canon := textproto.CanonicalMIMEHeaderKey(k)
		if isConnectionScopedHopHeader(canon, connectionHeaders) {
			continue
		}
		for _, v := range vv {
			dst.Add(canon, v)
		}
	}
}

func isHopHeader(name string) bool {
	_, ok := hopHeaders[strings.ToLower(name)]
	return ok
}

func extraHopTokens(headers http.Header) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, raw := range headers.Values("Connection") {
		for _, part := range strings.Split(raw, ",") {
			token := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(part))
			if token == "" {
				continue
			}
			tokens[token] = struct{}{}
		}
	}
	for _, raw := range headers.Values("Trailer") {
		for _, part := range strings.Split(raw, ",") {
			token := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(part))
			if token == "" {
				continue
			}
			tokens[token] = struct{}{}
		}
	}
	return tokens
}

func isConnectionScopedHopHeader(name string, connectionHeaders map[string]struct{}) bool {
	if isHopHeader(name) {
		return true
	}
	_, ok := connectionHeaders[textproto.CanonicalMIMEHeaderKey(name)]
	return ok
}

func headerCredential(headers http.Header) string {
	if headers == nil {
		return ""
	}
	auth := strings.TrimSpace(headers.Get("Authorization"))
	if auth != "" {
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			if token := strings.TrimSpace(auth[7:]); token != "" {
				return token
			}
		} else {
			return auth
		}
	}
	return strings.TrimSpace(headers.Get("X-API-Key"))
}

func headerNameSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		canon := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(value))
		if canon == "" {
			continue
		}
		out[canon] = struct{}{}
	}
	return out
}
