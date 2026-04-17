package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"jc_proxy/internal/balancer"
	"jc_proxy/internal/config"
	"jc_proxy/internal/keystore"
	"jc_proxy/internal/resin"
	"jc_proxy/internal/ui"
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

type Router struct {
	vendors                map[string]*vendorGateway
	bufPool                sync.Pool
	uiFS                   http.Handler
	uiRead                 fs.FS
	consoleEnabled         bool
	adminCIDRs             []netip.Prefix
	adminTrustedProxyCIDRs []netip.Prefix
}

type vendorGateway struct {
	name          string
	provider      string
	baseURL       *url.URL
	pool          *balancer.Pool
	client        *http.Client
	clientAuth    map[string]struct{}
	allowlist     map[string]struct{}
	injectHeaders map[string]string
	upstreamAuth  config.UpstreamAuthConfig
	errorPolicy   config.ErrorPolicyConfig
	rewrites      rewriteMatcher
	resinRuntime  *resin.RuntimeConfig
	keyCtrl       UpstreamKeyController
}

type rewriteRule struct {
	from string
	to   string
}

type rewriteMatcher struct {
	exact    map[string]string
	prefixes []rewriteRule
}

const maxReplayableRequestBodyBytes = 4 << 20

const (
	pooledResponseCopyBufferBytes    = 32 << 10
	streamingResponseCopyBufferBytes = 4 << 10
)

type flushWriter struct {
	writer  io.Writer
	flusher http.Flusher
}

type requestBodySource struct {
	replayable    bool
	safeRetry     bool
	contentLength int64
	newBody       func() (io.ReadCloser, error)
}

func (w *flushWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		w.flusher.Flush()
	}
	return n, err
}

func (s *requestBodySource) Body() (io.ReadCloser, error) {
	if s == nil || s.newBody == nil {
		return http.NoBody, nil
	}
	return s.newBody()
}

func (s *requestBodySource) canRetryRequestError(decision keyDecision) bool {
	if s == nil || !s.replayable || !s.safeRetry {
		return false
	}
	return shouldRetryDecision(decision)
}

func (s *requestBodySource) canRetryResponse(statusCode int, decision keyDecision) bool {
	if s == nil || !s.replayable || !shouldRetryDecision(decision) {
		return false
	}
	if s.safeRetry {
		return true
	}
	switch statusCode {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

func New(cfg *config.Config) (*Router, error) {
	return NewWithUpstreamKeyRecords(cfg, nil, nil)
}

func NewWithUpstreamKeyRecords(cfg *config.Config, upstreamKeys map[string][]keystore.Record, keyCtrl UpstreamKeyController) (*Router, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	adminCIDRs, err := config.ParseAdminAllowedCIDRs(cfg.Admin.AllowedCIDRs)
	if err != nil {
		return nil, fmt.Errorf("parse admin allowed cidrs: %w", err)
	}
	adminTrustedProxyCIDRs, err := config.ParseAdminTrustedProxyCIDRs(cfg.Admin.TrustedProxyCIDRs)
	if err != nil {
		return nil, fmt.Errorf("parse admin trusted proxy cidrs: %w", err)
	}

	vendors := make(map[string]*vendorGateway, len(cfg.Vendors))
	for name, vendor := range cfg.Vendors {
		baseURL, err := url.Parse(strings.TrimRight(vendor.Upstream.BaseURL, "/"))
		if err != nil {
			return nil, fmt.Errorf("vendor %s parse upstream base_url: %w", name, err)
		}
		if baseURL.Scheme == "" || baseURL.Host == "" {
			return nil, fmt.Errorf("vendor %s invalid upstream base_url", name)
		}

		keyConfigs := make([]balancer.KeyConfig, 0)
		if upstreamKeys != nil {
			for _, record := range upstreamKeys[name] {
				keyConfigs = append(keyConfigs, balancer.KeyConfig{
					Key:           record.Key,
					Status:        record.Status,
					DisableReason: record.DisableReason,
					DisabledAt:    record.DisabledAt,
					DisabledBy:    record.DisabledBy,
				})
			}
		}
		if len(keyConfigs) == 0 {
			for _, key := range vendor.Upstream.Keys {
				keyConfigs = append(keyConfigs, balancer.KeyConfig{
					Key:    key,
					Status: keystore.KeyStatusActive,
				})
			}
		}

		pool, err := balancer.NewPoolWithConfigs(vendor.LoadBalance, keyConfigs, vendor.Backoff.Threshold, vendor.Backoff.Duration)
		if err != nil {
			return nil, fmt.Errorf("vendor %s init key pool: %w", name, err)
		}

		transport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          512,
			MaxIdleConnsPerHost:   128,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
		}
		client := &http.Client{
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		clientAuthSet := make(map[string]struct{}, len(vendor.ClientAuth.Keys))
		if vendor.ClientAuth.Enabled {
			for _, key := range vendor.ClientAuth.Keys {
				clientAuthSet[key] = struct{}{}
			}
		}

		allowlist := make(map[string]struct{}, len(vendor.ClientHeaders.Allowlist))
		for _, h := range vendor.ClientHeaders.Allowlist {
			allowlist[textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(h))] = struct{}{}
		}

		var rr *resin.RuntimeConfig
		if vendor.Resin.Enabled {
			rc, err := resin.ParseRuntime(resin.Config{
				URL:      vendor.Resin.URL,
				Platform: vendor.Resin.Platform,
				Mode:     vendor.Resin.Mode,
			})
			if err != nil {
				return nil, fmt.Errorf("vendor %s parse resin: %w", name, err)
			}
			rr = rc
		}

		vendors[name] = &vendorGateway{
			name:          name,
			provider:      config.NormalizeProvider(vendor.Provider, name),
			baseURL:       baseURL,
			pool:          pool,
			client:        client,
			clientAuth:    clientAuthSet,
			allowlist:     allowlist,
			injectHeaders: vendor.InjectedHeader,
			upstreamAuth:  vendor.UpstreamAuth,
			errorPolicy:   vendor.ErrorPolicy,
			rewrites:      newRewriteMatcher(vendor.PathRewrites),
			resinRuntime:  rr,
			keyCtrl:       keyCtrl,
		}
	}

	return &Router{
		vendors: vendors,
		bufPool: sync.Pool{
			New: func() any {
				buf := make([]byte, pooledResponseCopyBufferBytes)
				return &buf
			},
		},
		uiFS:                   newUIHandler(),
		uiRead:                 newUIReadFS(),
		consoleEnabled:         cfg.Admin.Enabled,
		adminCIDRs:             adminCIDRs,
		adminTrustedProxyCIDRs: adminTrustedProxyCIDRs,
	}, nil
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	if req.URL.Path == "/console" || strings.HasPrefix(req.URL.Path, "/console/") {
		if !r.consoleEnabled || !config.RequestAddrAllowed(req.RemoteAddr, req.Header, r.adminCIDRs, r.adminTrustedProxyCIDRs) {
			http.NotFound(w, req)
			return
		}
		r.serveConsole(w, req)
		return
	}

	vendorName, upstreamPath, ok := splitVendorPath(req.URL.Path)
	if !ok {
		http.NotFound(w, req)
		return
	}

	vg, exists := r.vendors[vendorName]
	if !exists {
		http.Error(w, "unknown vendor", http.StatusNotFound)
		return
	}

	if err := vg.authorizeClient(req); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	path := vg.rewrites.Apply(config.NormalizePath(upstreamPath))

	bodySource, err := prepareRequestBody(req)
	if err != nil {
		if isClientDisconnectError(err) {
			return
		}
		http.Error(w, "read request body failed", http.StatusBadRequest)
		return
	}

	useManagedKey := vg.usesManagedUpstreamKeys()
	for {
		idx := -1
		selectedKey := vg.passthroughUpstreamKey(req)
		if useManagedKey {
			var keyOK bool
			idx, selectedKey, keyOK = vg.pool.Acquire()
			if !keyOK {
				http.Error(w, "all vendor keys in cooldown or disabled", http.StatusServiceUnavailable)
				return
			}
		}

		targetURL, err := vg.buildTargetURL(path, req.URL.RawQuery, selectedKey)
		if err != nil {
			if useManagedKey {
				vg.pool.Release(idx)
			}
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		upReq, err := vg.newUpstreamRequest(req.Context(), req.Method, targetURL, req.Header, bodySource, selectedKey)
		if err != nil {
			if useManagedKey {
				vg.pool.Release(idx)
			}
			if isClientDisconnectError(err) {
				return
			}
			http.Error(w, "build upstream request failed", http.StatusInternalServerError)
			return
		}

		resp, err := vg.client.Do(upReq)
		if err != nil {
			if isCanceledUpstreamError(req.Context(), err) {
				if useManagedKey {
					vg.pool.Release(idx)
				}
				return
			}
			decision := classifyRequestError(vg.provider, vg.errorPolicy, fmt.Sprintf("upstream request failed: %v", err))
			vg.applyDecision(idx, selectedKey, decision)
			if bodySource.canRetryRequestError(decision) && vg.hasAvailableKey() {
				continue
			}
			http.Error(w, "upstream request failed", http.StatusBadGateway)
			return
		}

		if resp.StatusCode >= http.StatusBadRequest {
			preview, bodyReader, err := captureResponsePreview(resp.Body, 2048)
			if err != nil {
				_ = resp.Body.Close()
				if isCanceledUpstreamError(req.Context(), err) {
					if useManagedKey {
						vg.pool.Release(idx)
					}
					return
				}
				decision := classifyRequestError(vg.provider, vg.errorPolicy, fmt.Sprintf("read upstream response failed: %v", err))
				vg.applyDecision(idx, selectedKey, decision)
				if bodySource.canRetryRequestError(decision) && vg.hasAvailableKey() {
					continue
				}
				http.Error(w, "upstream request failed", http.StatusBadGateway)
				return
			}

			decision := classifyResponse(vg.provider, vg.errorPolicy, resp.StatusCode, resp.Header, preview)
			if bodySource.canRetryResponse(resp.StatusCode, decision) {
				vg.applyDecision(idx, selectedKey, decision)
				if vg.hasAvailableKey() {
					_ = resp.Body.Close()
					continue
				}
				r.writeUpstreamResponse(w, req, resp, bodyReader, vg, idx, selectedKey, decision, true)
				return
			}

			r.writeUpstreamResponse(w, req, resp, bodyReader, vg, idx, selectedKey, decision, false)
			return
		}

		decision := classifyResponse(vg.provider, vg.errorPolicy, resp.StatusCode, resp.Header, nil)
		r.writeUpstreamResponse(w, req, resp, resp.Body, vg, idx, selectedKey, decision, false)
		return
	}
}

func (r *Router) VendorStats() map[string][]map[string]any {
	out := make(map[string][]map[string]any, len(r.vendors))
	for name, v := range r.vendors {
		out[name] = v.pool.Stats()
	}
	return out
}

func newUIHandler() http.Handler {
	sub, err := fs.Sub(ui.DistFS, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}

func newUIReadFS() fs.FS {
	sub, err := fs.Sub(ui.DistFS, "dist")
	if err != nil {
		return nil
	}
	return sub
}

func (r *Router) serveConsole(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/console" {
		http.Redirect(w, req, "/console/", http.StatusTemporaryRedirect)
		return
	}
	trimmed := strings.TrimPrefix(req.URL.Path, "/console")
	if trimmed == "" {
		trimmed = "/"
	}
	clean := strings.TrimPrefix(trimmed, "/")
	if r.uiRead != nil && clean != "" {
		if f, err := r.uiRead.Open(clean); err == nil {
			_ = f.Close()
			req2 := req.Clone(req.Context())
			req2.URL.Path = trimmed
			r.uiFS.ServeHTTP(w, req2)
			return
		}
	}
	if strings.HasPrefix(trimmed, "/assets/") {
		req2 := req.Clone(req.Context())
		req2.URL.Path = trimmed
		r.uiFS.ServeHTTP(w, req2)
		return
	}
	req2 := req.Clone(req.Context())
	req2.URL.Path = "/"
	r.uiFS.ServeHTTP(w, req2)
}

func splitVendorPath(path string) (vendor, rest string, ok bool) {
	clean := strings.TrimPrefix(path, "/")
	if clean == "" {
		return "", "", false
	}
	parts := strings.SplitN(clean, "/", 2)
	vendor = parts[0]
	if vendor == "" {
		return "", "", false
	}
	if len(parts) == 1 {
		return vendor, "/", true
	}
	return vendor, "/" + parts[1], true
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

func (v *vendorGateway) buildTargetURL(path, rawQuery, selectedKey string) (string, error) {
	target := *v.baseURL
	target.Path = singleJoiningSlash(v.baseURL.Path, path)
	target.RawQuery = rawQuery
	resolved := target.String()

	if v.resinRuntime == nil {
		return resolved, nil
	}
	urlText, err := resin.BuildReverseURL(resolved, *v.resinRuntime)
	if err != nil {
		return "", err
	}
	_ = selectedKey
	return urlText, nil
}

func (v *vendorGateway) forwardHeaders(src http.Header, selectedKey string) http.Header {
	headers := make(http.Header, len(src)+4)
	connectionHeaders := extraHopTokens(src)
	if len(v.allowlist) == 0 {
		for k, vals := range src {
			canon := textproto.CanonicalMIMEHeaderKey(k)
			if isConnectionScopedHopHeader(canon, connectionHeaders) {
				continue
			}
			for _, val := range vals {
				headers.Add(canon, val)
			}
		}
	} else {
		for k, vals := range src {
			canon := textproto.CanonicalMIMEHeaderKey(k)
			if _, ok := v.allowlist[canon]; !ok && !v.isPassthroughAuthHeader(canon) {
				continue
			}
			if isConnectionScopedHopHeader(canon, connectionHeaders) {
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

func (v *vendorGateway) hasAvailableKey() bool {
	if !v.usesManagedUpstreamKeys() || v.pool == nil {
		return false
	}
	now := time.Now()
	for _, state := range v.pool.Snapshot() {
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

func (v *vendorGateway) applyDecision(idx int, key string, decision keyDecision) {
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
		if v.keyCtrl != nil {
			_ = v.keyCtrl.SetStatus(v.name, key, keystore.KeyStatusDisabledAuto, decision.reason, "system:auto")
		}
	default:
		v.pool.Release(idx)
	}
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

func shouldFlushResponse(req *http.Request, resp *http.Response) bool {
	if resp != nil {
		contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
		if strings.HasPrefix(contentType, "text/event-stream") {
			return true
		}
	}
	if req == nil {
		return false
	}
	stream := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("stream")))
	if stream == "true" || stream == "1" || stream == "yes" {
		return true
	}
	return strings.Contains(strings.ToLower(req.Header.Get("Accept")), "text/event-stream")
}

func isClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "use of closed network connection")
}

func isCanceledUpstreamError(ctx context.Context, err error) bool {
	if ctx == nil || err == nil || ctx.Err() == nil {
		return false
	}
	if errors.Is(err, ctx.Err()) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if isClientDisconnectError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "request canceled") ||
		strings.Contains(msg, "operation was canceled")
}

func prepareRequestBody(req *http.Request) (*requestBodySource, error) {
	safeRetry := isSafeRetryMethod(req.Method)

	if req == nil || req.Body == nil || req.Body == http.NoBody || req.ContentLength == 0 {
		return &requestBodySource{
			replayable:    true,
			safeRetry:     safeRetry,
			contentLength: 0,
			newBody: func() (io.ReadCloser, error) {
				return http.NoBody, nil
			},
		}, nil
	}

	if req.GetBody != nil {
		return &requestBodySource{
			replayable:    true,
			safeRetry:     safeRetry,
			contentLength: req.ContentLength,
			newBody:       req.GetBody,
		}, nil
	}

	// Unknown-length request bodies are commonly chunked/streamed uploads.
	// Avoid pre-reading them into memory before the first upstream attempt.
	if req.ContentLength < 0 {
		return newSingleUseBodySource(req.Body, req.ContentLength, safeRetry), nil
	}

	if !shouldBufferRequestBody(req.Header.Get("Content-Type")) {
		return newSingleUseBodySource(req.Body, req.ContentLength, safeRetry), nil
	}
	if req.ContentLength > maxReplayableRequestBodyBytes {
		return newSingleUseBodySource(req.Body, req.ContentLength, safeRetry), nil
	}

	buf, overflow, err := readReplayableRequestBody(req.Body, maxReplayableRequestBodyBytes)
	if err != nil {
		return nil, err
	}
	if overflow {
		reader := io.MultiReader(bytes.NewReader(buf), req.Body)
		return newSingleUseBodySource(io.NopCloser(reader), req.ContentLength, safeRetry), nil
	}

	raw := append([]byte(nil), buf...)
	return &requestBodySource{
		replayable:    true,
		safeRetry:     safeRetry,
		contentLength: int64(len(raw)),
		newBody: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(raw)), nil
		},
	}, nil
}

func newSingleUseBodySource(body io.ReadCloser, contentLength int64, safeRetry bool) *requestBodySource {
	used := false
	return &requestBodySource{
		replayable:    false,
		safeRetry:     safeRetry,
		contentLength: contentLength,
		newBody: func() (io.ReadCloser, error) {
			if used {
				return nil, errors.New("request body is not replayable")
			}
			used = true
			return body, nil
		},
	}
}

func readReplayableRequestBody(body io.ReadCloser, limit int64) ([]byte, bool, error) {
	if body == nil || body == http.NoBody {
		return nil, false, nil
	}
	lr := &io.LimitedReader{R: body, N: limit + 1}
	buf, err := io.ReadAll(lr)
	if err != nil {
		return nil, false, err
	}
	return buf, int64(len(buf)) > limit, nil
}

func shouldBufferRequestBody(contentType string) bool {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = contentType
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return !strings.HasPrefix(mediaType, "multipart/")
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

func isSafeRetryMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func shouldRetryDecision(decision keyDecision) bool {
	if !decision.failover {
		return false
	}
	return decision.action == keyActionCooldown || decision.action == keyActionDisable
}

func captureResponsePreview(body io.ReadCloser, limit int) ([]byte, io.Reader, error) {
	if body == nil || body == http.NoBody || limit <= 0 {
		return nil, http.NoBody, nil
	}
	preview, err := io.ReadAll(io.LimitReader(body, int64(limit)))
	if err != nil {
		return nil, nil, err
	}
	return preview, io.MultiReader(bytes.NewReader(preview), body), nil
}

func (r *Router) responseCopyBuffer(streaming bool) ([]byte, func()) {
	if streaming {
		return make([]byte, streamingResponseCopyBufferBytes), func() {}
	}

	buf := r.bufPool.Get().(*[]byte)
	return *buf, func() {
		r.bufPool.Put(buf)
	}
}

func (r *Router) writeUpstreamResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, body io.Reader, vg *vendorGateway, idx int, selectedKey string, decision keyDecision, decisionApplied bool) {
	defer resp.Body.Close()

	if body == nil {
		body = http.NoBody
	}

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	streaming := shouldFlushResponse(req, resp)
	buf, releaseBuf := r.responseCopyBuffer(streaming)
	defer releaseBuf()

	writer := io.Writer(w)
	if streaming {
		if flusher, ok := w.(http.Flusher); ok {
			writer = &flushWriter{writer: w, flusher: flusher}
			flusher.Flush()
		}
	}

	_, copyErr := io.CopyBuffer(writer, body, buf)
	if copyErr != nil {
		if !decisionApplied {
			if resp.StatusCode >= http.StatusBadRequest {
				vg.applyDecision(idx, selectedKey, decision)
			} else {
				vg.applyDecision(idx, selectedKey, keyDecision{action: keyActionNone})
			}
		}
		return
	}

	if !decisionApplied {
		vg.applyDecision(idx, selectedKey, decision)
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
