package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
	"jc_proxy/internal/resin"
)

type rewriteRule struct {
	from string
	to   string
}

type rewriteMatcher struct {
	exact    map[string]string
	prefixes []rewriteRule
}

func (v *vendorGateway) authorizeClient(req *http.Request) error {
	if len(v.clientAuth) == 0 {
		return nil
	}
	auth := headerCredential(req.Header)
	if auth == "" {
		return errors.New("missing client api key")
	}
	if _, ok := v.clientAuth[auth]; !ok {
		return errors.New("invalid client api key")
	}
	return nil
}

func (v *vendorGateway) newAttempt(ctx context.Context, req *http.Request, path string, bodySource *requestBodySource, excluded map[int]struct{}) (*upstreamAttempt, *proxyError) {
	idx := -1
	selectedVersion := int64(0)
	selectedKey := v.passthroughUpstreamKey(req)
	if v.usesManagedUpstreamKeys() {
		var keyOK bool
		idx, selectedKey, keyOK = v.pool.AcquireExcept(excluded)
		if !keyOK {
			return nil, &proxyError{statusCode: http.StatusServiceUnavailable, message: "all vendor keys in cooldown or disabled"}
		}
		selectedVersion = v.pool.Version(idx)
	}

	targetURL, err := v.buildTargetURL(path, req.URL.RawQuery, selectedKey)
	if err != nil {
		if v.usesManagedUpstreamKeys() {
			v.pool.Release(idx)
		}
		return nil, &proxyError{statusCode: http.StatusBadGateway, message: err.Error()}
	}

	upReq, err := v.newUpstreamRequest(ctx, req.Method, targetURL, req.Header, bodySource, selectedKey)
	if err != nil {
		if v.usesManagedUpstreamKeys() {
			v.pool.Release(idx)
		}
		if isClientDisconnectError(err) {
			return nil, nil
		}
		return nil, &proxyError{statusCode: http.StatusInternalServerError, message: "build upstream request failed"}
	}

	return &upstreamAttempt{
		idx:             idx,
		selectedVersion: selectedVersion,
		selectedKey:     selectedKey,
		request:         upReq,
	}, nil
}

func (v *vendorGateway) buildTargetURL(path, rawQuery, selectedKey string) (string, error) {
	return buildTargetURLFromBase(v.baseURL, path, rawQuery, selectedKey, v.resinRuntime)
}

func (v *vendorGateway) newUpstreamRequest(ctx context.Context, method, targetURL string, headers http.Header, bodySource *requestBodySource, selectedKey string) (*http.Request, error) {
	body, err := bodySource.Body()
	if err != nil {
		return nil, err
	}

	upReq, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		_ = body.Close()
		return nil, err
	}
	upReq.Header = v.forwardHeaders(headers, selectedKey)
	upReq.Host = upReq.URL.Host
	if bodySource != nil {
		upReq.ContentLength = bodySource.contentLength
	}
	return upReq, nil
}

func (v *vendorGateway) hasAvailableKey(excluded map[int]struct{}) bool {
	if !v.usesManagedUpstreamKeys() || v.pool == nil {
		return false
	}
	now := time.Now()
	for idx, state := range v.pool.Snapshot() {
		if _, skip := excluded[idx]; skip {
			continue
		}
		if !keystore.IsActiveStatus(state.Status) {
			continue
		}
		if state.CooldownUntil.After(now) {
			continue
		}
		return true
	}
	return false
}

func (v *vendorGateway) shouldBufferRequestBody(method string) bool {
	if v == nil || !v.usesManagedUpstreamKeys() || v.managedKeyCount < 2 {
		return false
	}

	if isSafeRetryMethod(method) {
		if boolOrDefault(v.errorPolicy.Failover.RequestError, true) {
			return true
		}
		if len(v.errorPolicy.Failover.ResponseStatusCodes) > 0 {
			return true
		}
		for _, statusCode := range []int{
			http.StatusUnauthorized,
			http.StatusPaymentRequired,
			http.StatusForbidden,
			http.StatusTooManyRequests,
			529,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout,
		} {
			if shouldFailoverResponse(statusCode, v.errorPolicy) {
				return true
			}
		}
		return false
	}

	for _, statusCode := range []int{
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusTooManyRequests,
	} {
		if shouldFailoverResponse(statusCode, v.errorPolicy) {
			return true
		}
	}
	return false
}

func (v *vendorGateway) applyDecision(idx int, key string, version int64, decision keyDecision) {
	if !v.usesManagedUpstreamKeys() || v.pool == nil || idx < 0 {
		return
	}
	switch decision.action {
	case keyActionSuccess:
		v.pool.ReleaseSuccess(idx)
	case keyActionObserve:
		v.pool.Observe(idx, decision.statusCode, decision.reason)
	case keyActionCooldown:
		v.pool.Cooldown(idx, decision.statusCode, decision.reason, decision.cooldown)
	case keyActionDisable:
		v.pool.Disable(idx, decision.statusCode, decision.reason, "system:auto")
		v.persistDisabledKeyAsync(key, version, decision.reason)
	default:
		v.pool.Release(idx)
	}
}

func (v *vendorGateway) persistDisabledKeyAsync(key string, version int64, reason string) {
	if v.keyCtrl == nil {
		return
	}
	go func(ctrl UpstreamKeyController, vendor, key string, version int64, reason string) {
		if versioned, ok := ctrl.(keystore.ConditionalStatusStore); ok {
			_ = versioned.SetStatusIfVersion(vendor, key, version, keystore.KeyStatusDisabledAuto, reason, "system:auto")
			return
		}
		_ = ctrl.SetStatus(vendor, key, keystore.KeyStatusDisabledAuto, reason, "system:auto")
	}(v.keyCtrl, v.name, key, version, reason)
}

func (v *vendorGateway) usesManagedUpstreamKeys() bool {
	return v.upstreamAuth.Mode != "passthrough"
}

func (v *vendorGateway) passthroughUpstreamKey(req *http.Request) string {
	if req == nil || v.usesManagedUpstreamKeys() {
		return ""
	}
	return headerCredential(req.Header)
}

func (v *vendorGateway) isPassthroughAuthHeader(name string) bool {
	if v.usesManagedUpstreamKeys() {
		return false
	}
	switch textproto.CanonicalMIMEHeaderKey(name) {
	case "Authorization", "X-Api-Key":
		return true
	default:
		return false
	}
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}

func buildTargetURLFromBase(baseURL *url.URL, path, rawQuery, selectedKey string, resinRuntime *resin.RuntimeConfig) (string, error) {
	if baseURL == nil {
		return "", errors.New("upstream base_url is empty")
	}

	target := *baseURL
	target.Path = singleJoiningSlash(baseURL.Path, path)
	target.RawQuery = rawQuery
	resolved := target.String()

	if resinRuntime == nil {
		return resolved, nil
	}
	urlText, err := resin.BuildReverseURL(resolved, *resinRuntime)
	if err != nil {
		return "", err
	}
	_ = selectedKey
	return urlText, nil
}

func newRewriteMatcher(m map[string]string) rewriteMatcher {
	rm := rewriteMatcher{exact: make(map[string]string, len(m))}
	prefixes := make([]rewriteRule, 0)
	for from, to := range m {
		from = config.NormalizePath(from)
		to = config.NormalizePath(to)
		if strings.HasSuffix(from, "*") {
			base := strings.TrimSuffix(from, "*")
			prefixes = append(prefixes, rewriteRule{from: base, to: strings.TrimSuffix(to, "*")})
			continue
		}
		rm.exact[from] = to
	}
	sort.Slice(prefixes, func(i, j int) bool {
		return len(prefixes[i].from) > len(prefixes[j].from)
	})
	rm.prefixes = prefixes
	return rm
}

func (r rewriteMatcher) Apply(path string) string {
	if to, ok := r.exact[path]; ok {
		return to
	}
	for _, rule := range r.prefixes {
		if strings.HasPrefix(path, rule.from) {
			tail := strings.TrimPrefix(path, rule.from)
			if !strings.HasPrefix(tail, "/") && tail != "" {
				tail = "/" + tail
			}
			return strings.TrimRight(rule.to, "/") + tail
		}
	}
	return path
}
