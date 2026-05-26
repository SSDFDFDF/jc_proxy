package gateway

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"jc_proxy/internal/config"
	"jc_proxy/internal/ui"
)

type preparedProxyRequest struct {
	vendor     *vendorGateway
	path       string
	bodySource *requestBodySource
}

type upstreamAttempt struct {
	idx             int
	selectedVersion int64
	selectedKey     string
	request         *http.Request
}

type proxyError struct {
	statusCode int
	message    string
}

type aggregateRetryHook func(statusCode int, err error, bodySource *requestBodySource) bool

type vendorRequestOutcome int

const (
	vendorRequestDone vendorRequestOutcome = iota
	vendorRequestRetryAggregateChild
)

type upstreamResponseOutcome int

const (
	upstreamResponseDone upstreamResponseOutcome = iota
	upstreamResponseRetryVendorKey
	upstreamResponseRetryAggregateChild
)

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.serveInternalRoute(w, req) {
		return
	}

	vendorName, upstreamPath, ok := splitVendorPath(req.URL.Path)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	vg, exists := r.vendors[vendorName]
	if !exists {
		http.Error(w, "unknown vendor", http.StatusNotFound)
		return
	}

	if vg.isAggregate {
		r.serveAggregateRequest(w, req, vg, upstreamPath)
		return
	}

	prepared, proxyErr := r.prepareNonAggregateRequest(req, vg, upstreamPath)
	if proxyErr != nil {
		http.Error(w, proxyErr.message, proxyErr.statusCode)
		return
	}
	r.serveVendorRequest(w, req, prepared)
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

func (r *Router) serveInternalRoute(w http.ResponseWriter, req *http.Request) bool {
	if req.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return true
	}
	if req.URL.Path == "/console" || strings.HasPrefix(req.URL.Path, "/console/") {
		if !r.consoleEnabled || !config.RequestAddrAllowed(req.RemoteAddr, req.Header, r.adminCIDRs, r.adminTrustedProxyCIDRs) {
			http.NotFound(w, req)
			return true
		}
		r.serveConsole(w, req)
		return true
	}
	return false
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

func (r *Router) prepareNonAggregateRequest(req *http.Request, vg *vendorGateway, upstreamPath string) (*preparedProxyRequest, *proxyError) {
	if err := vg.authorizeClient(req); err != nil {
		return nil, &proxyError{statusCode: http.StatusUnauthorized, message: err.Error()}
	}

	bodySource, err := prepareRequestBody(req, vg.shouldBufferRequestBody(req.Method))
	if err != nil {
		if isClientDisconnectError(err) {
			return nil, nil
		}
		return nil, &proxyError{statusCode: http.StatusBadRequest, message: "read request body failed"}
	}

	return &preparedProxyRequest{
		vendor:     vg,
		path:       vg.rewrites.Apply(config.NormalizePath(upstreamPath)),
		bodySource: bodySource,
	}, nil
}

func (r *Router) prepareAggregateRequest(req *http.Request, agg *vendorGateway, path string, exclude map[string]struct{}, bodySource *requestBodySource) (*preparedProxyRequest, *proxyError) {
	child := agg.aggPool.PickAvailable(aggregateChildAvailable, exclude)
	if child == nil {
		return nil, &proxyError{statusCode: http.StatusServiceUnavailable, message: "no available child vendor"}
	}

	if bodySource == nil {
		var err error
		allowReplay := child.vendor.shouldBufferRequestBody(req.Method)
		if boolOrDefault(agg.aggRetry.Enabled, true) {
			allowReplay = true
		}
		bodySource, err = prepareRequestBody(req, allowReplay)
		if err != nil {
			if isClientDisconnectError(err) {
				return nil, nil
			}
			return nil, &proxyError{statusCode: http.StatusBadRequest, message: "read request body failed"}
		}
	}

	return &preparedProxyRequest{
		vendor:     child.vendor,
		path:       child.vendor.rewrites.Apply(config.NormalizePath(path)),
		bodySource: bodySource,
	}, nil
}

func aggregateChildAvailable(e *aggregateChildEntry) bool {
	if e == nil || e.vendor == nil {
		return false
	}
	if !e.vendor.usesManagedUpstreamKeys() {
		return true
	}
	return e.vendor.hasAvailableKey(nil)
}

func (r *Router) serveAggregateRequest(w http.ResponseWriter, req *http.Request, agg *vendorGateway, path string) {
	if err := agg.authorizeClient(req); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	retry := agg.aggRetry
	if !boolOrDefault(retry.Enabled, true) {
		// Retry disabled — behave like a single-attempt aggregate.
		prepared, proxyErr := r.prepareAggregateRequest(req, agg, path, nil, nil)
		if proxyErr != nil {
			http.Error(w, proxyErr.message, proxyErr.statusCode)
			return
		}
		r.serveVendorRequest(w, req, prepared)
		return
	}

	maxAttempts := retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 2
	}

	exclude := make(map[string]struct{})
	var bodySource *requestBodySource

	for attempt := 0; attempt < maxAttempts; attempt++ {
		prepared, proxyErr := r.prepareAggregateRequest(req, agg, path, exclude, bodySource)
		if proxyErr != nil {
			http.Error(w, proxyErr.message, proxyErr.statusCode)
			return
		}
		if prepared == nil {
			return
		}

		// Capture body source from first attempt for replay.
		if bodySource == nil {
			bodySource = prepared.bodySource
		}

		shouldRetryAggregate := func(statusCode int, err error, source *requestBodySource) bool {
			if attempt >= maxAttempts-1 || source == nil {
				return false
			}
			if !aggregateRetryable(statusCode, err, retry) {
				return false
			}
			if !source.canRetryAggregate(statusCode, err) {
				return false
			}
			nextExclude := make(map[string]struct{}, len(exclude)+1)
			for name := range exclude {
				nextExclude[name] = struct{}{}
			}
			nextExclude[prepared.vendor.name] = struct{}{}
			return agg.aggPool.HasAvailable(aggregateChildAvailable, nextExclude)
		}
		if r.serveVendorRequestWithAggregateHook(w, req, prepared, shouldRetryAggregate) == vendorRequestRetryAggregateChild {
			exclude[prepared.vendor.name] = struct{}{}
			continue
		}

		return
	}
}

func (r *Router) serveVendorRequest(w http.ResponseWriter, req *http.Request, prepared *preparedProxyRequest) {
	_ = r.serveVendorRequestWithAggregateHook(w, req, prepared, nil)
}

func (r *Router) serveVendorRequestWithAggregateHook(w http.ResponseWriter, req *http.Request, prepared *preparedProxyRequest, aggregateHook aggregateRetryHook) vendorRequestOutcome {
	if prepared == nil || prepared.vendor == nil {
		return vendorRequestDone
	}

	vg := prepared.vendor
	interim := newInterimResponseSender(w, vg.interimInterval)
	defer interim.stop()

	triedManagedKeyIdx := make(map[int]struct{})
	maxAttempts := vg.errorPolicy.Failover.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	attempts := 0

	for {
		attempts++
		if attempts > maxAttempts {
			writeHTTPError(w, interim, "exceeded maximum failover attempts", http.StatusBadGateway)
			return vendorRequestDone
		}

		attempt, proxyErr := vg.newAttempt(req.Context(), req, prepared.path, prepared.bodySource, triedManagedKeyIdx)
		if proxyErr != nil {
			writeHTTPError(w, interim, proxyErr.message, proxyErr.statusCode)
			return vendorRequestDone
		}
		if attempt == nil {
			return vendorRequestDone
		}

		resp, err := vg.client.Do(attempt.request)
		if err != nil {
			if isCanceledUpstreamError(req.Context(), err) {
				if vg.usesManagedUpstreamKeys() {
					vg.pool.Release(attempt.idx)
				}
				return vendorRequestDone
			}
			decision := classifyRequestError(vg.provider, vg.errorPolicy, fmt.Sprintf("upstream request failed: %v", err))
			vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
			if prepared.bodySource.canRetryRequestError(decision) && vg.usesManagedUpstreamKeys() {
				triedManagedKeyIdx[attempt.idx] = struct{}{}
			}
			if prepared.bodySource.canRetryRequestError(decision) && vg.hasAvailableKey(triedManagedKeyIdx) {
				if attempts >= maxAttempts {
					if aggregateHook != nil && aggregateHook(0, err, prepared.bodySource) {
						return vendorRequestRetryAggregateChild
					}
					writeHTTPError(w, interim, "exceeded maximum failover attempts", http.StatusBadGateway)
					return vendorRequestDone
				}
				continue
			}
			if aggregateHook != nil && aggregateHook(0, err, prepared.bodySource) {
				return vendorRequestRetryAggregateChild
			}
			writeHTTPError(w, interim, "upstream request failed", http.StatusBadGateway)
			return vendorRequestDone
		}

		if vg.upstreamBodyTimeout > 0 {
			resp.Body = newIdleTimeoutReadCloser(resp.Body, vg.upstreamBodyTimeout)
		}

		switch r.handleUpstreamResponse(w, req, resp, vg, attempt, prepared.bodySource, triedManagedKeyIdx, interim, aggregateHook, attempts < maxAttempts) {
		case upstreamResponseRetryVendorKey:
			continue
		case upstreamResponseRetryAggregateChild:
			return vendorRequestRetryAggregateChild
		default:
			return vendorRequestDone
		}
	}
}

func (r *Router) handleUpstreamResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, vg *vendorGateway, attempt *upstreamAttempt, bodySource *requestBodySource, triedManagedKeyIdx map[int]struct{}, interim *interimResponseSender, aggregateHook aggregateRetryHook, vendorRetryRemaining bool) upstreamResponseOutcome {
	if resp.StatusCode >= http.StatusBadRequest {
		preview, bodyReader, err := captureResponsePreview(resp.Body, 2048, resp.Header)
		if err != nil {
			_ = resp.Body.Close()
			if isCanceledUpstreamError(req.Context(), err) {
				if vg.usesManagedUpstreamKeys() {
					vg.pool.Release(attempt.idx)
				}
				return upstreamResponseDone
			}
			decision := classifyRequestError(vg.provider, vg.errorPolicy, fmt.Sprintf("read upstream response failed: %v", err))
			vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
			if bodySource.canRetryRequestError(decision) && vg.usesManagedUpstreamKeys() {
				triedManagedKeyIdx[attempt.idx] = struct{}{}
			}
			if bodySource.canRetryRequestError(decision) && vg.hasAvailableKey(triedManagedKeyIdx) {
				if !vendorRetryRemaining {
					if aggregateHook != nil && aggregateHook(0, err, bodySource) {
						return upstreamResponseRetryAggregateChild
					}
					writeHTTPError(w, interim, "exceeded maximum failover attempts", http.StatusBadGateway)
					return upstreamResponseDone
				}
				return upstreamResponseRetryVendorKey
			}
			if aggregateHook != nil && aggregateHook(0, err, bodySource) {
				return upstreamResponseRetryAggregateChild
			}
			writeHTTPError(w, interim, "upstream request failed", http.StatusBadGateway)
			return upstreamResponseDone
		}

		decision := classifyResponse(vg.provider, vg.errorPolicy, resp.StatusCode, resp.Header, preview)
		if bodySource.canRetryResponse(resp.StatusCode, decision) {
			vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
			if vg.usesManagedUpstreamKeys() {
				triedManagedKeyIdx[attempt.idx] = struct{}{}
			}
			if vg.hasAvailableKey(triedManagedKeyIdx) {
				if !vendorRetryRemaining {
					if aggregateHook != nil && aggregateHook(resp.StatusCode, nil, bodySource) {
						_ = resp.Body.Close()
						return upstreamResponseRetryAggregateChild
					}
					_ = resp.Body.Close()
					writeHTTPError(w, interim, "exceeded maximum failover attempts", http.StatusBadGateway)
					return upstreamResponseDone
				}
				_ = resp.Body.Close()
				return upstreamResponseRetryVendorKey
			}
			if aggregateHook != nil && aggregateHook(resp.StatusCode, nil, bodySource) {
				_ = resp.Body.Close()
				return upstreamResponseRetryAggregateChild
			}
			if err := r.writeUpstreamResponse(w, req, resp, bodyReader, vg, attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision, true, interim); errors.Is(err, errAbortDownstreamResponse) {
				panic(http.ErrAbortHandler)
			}
			return upstreamResponseDone
		}

		if aggregateHook != nil && aggregateHook(resp.StatusCode, nil, bodySource) {
			vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
			_ = resp.Body.Close()
			return upstreamResponseRetryAggregateChild
		}
		if err := r.writeUpstreamResponse(w, req, resp, bodyReader, vg, attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision, false, interim); errors.Is(err, errAbortDownstreamResponse) {
			panic(http.ErrAbortHandler)
		}
		return upstreamResponseDone
	}

	decision := classifyResponse(vg.provider, vg.errorPolicy, resp.StatusCode, resp.Header, nil)
	if aggregateHook != nil && aggregateHook(resp.StatusCode, nil, bodySource) {
		vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
		_ = resp.Body.Close()
		return upstreamResponseRetryAggregateChild
	}
	if err := r.writeUpstreamResponse(w, req, resp, resp.Body, vg, attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision, false, interim); errors.Is(err, errAbortDownstreamResponse) {
		panic(http.ErrAbortHandler)
	}
	return upstreamResponseDone
}
