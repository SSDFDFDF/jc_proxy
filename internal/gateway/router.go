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
	vendor           *vendorGateway
	path             string
	bodySource       *requestBodySource
	allowedKeyIdxs   []int
	aggregateChildID string
}

// requestExecution owns state that must live for the entire downstream
// request, including all aggregate child switches. Keeping this state outside
// the per-vendor loop prevents a key from being retried after control moves to
// another aggregate child and provides one hard upstream-attempt budget.
type requestExecution struct {
	maxAttempts  int
	attempts     int
	tried        map[string]struct{}
	triedIndexes map[string]map[int]struct{} // vendorID → tried key indexes
}

func newRequestExecution(maxAttempts int) *requestExecution {
	if maxAttempts <= 0 {
		maxAttempts = config.DefaultMaxUpstreamAttempts
	}
	return &requestExecution{
		maxAttempts:  maxAttempts,
		tried:        make(map[string]struct{}),
		triedIndexes: make(map[string]map[int]struct{}),
	}
}

func (e *requestExecution) consumeAttempt() bool {
	if e == nil {
		return true
	}
	if e.attempts >= e.maxAttempts {
		return false
	}
	e.attempts++
	return true
}

// hasBudget reports whether one more upstream attempt is still allowed. Retry
// decisions consult this so an exhausted budget makes the gateway deliver the
// upstream response it already holds instead of discarding it for a synthetic
// gateway error.
func (e *requestExecution) hasBudget() bool {
	if e == nil {
		return true
	}
	return e.attempts < e.maxAttempts
}

func executionTargetKey(v *vendorGateway, selectedKey string) string {
	if v == nil {
		return ""
	}
	return v.id + "\x00" + selectedKey
}

func (e *requestExecution) hasTried(v *vendorGateway, selectedKey string) bool {
	if e == nil {
		return false
	}
	_, ok := e.tried[executionTargetKey(v, selectedKey)]
	return ok
}

func (e *requestExecution) triedKeyIndexes(v *vendorGateway) map[int]struct{} {
	if e == nil || v == nil {
		return nil
	}
	return e.triedIndexes[v.id]
}

// getOrCreateTriedKeyIndexes returns (or creates) the live set of tried key
// indexes for vendor v. The caller may hold the returned map as a local
// variable; subsequent markTried calls write to the same map.
func (e *requestExecution) getOrCreateTriedKeyIndexes(v *vendorGateway) map[int]struct{} {
	if e == nil || v == nil {
		return nil
	}
	idxs := e.triedIndexes[v.id]
	if idxs == nil {
		idxs = make(map[int]struct{})
		e.triedIndexes[v.id] = idxs
	}
	return idxs
}

func (e *requestExecution) markTried(v *vendorGateway, selectedKey string, idx int) {
	if e == nil {
		return
	}
	if key := executionTargetKey(v, selectedKey); key != "" {
		e.tried[key] = struct{}{}
	}
	if idx >= 0 && v != nil && v.usesManagedUpstreamKeys() {
		idxs := e.triedIndexes[v.id]
		if idxs == nil {
			idxs = make(map[int]struct{})
			e.triedIndexes[v.id] = idxs
		}
		idxs[idx] = struct{}{}
	}
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

func (r *Router) prepareAggregateRequest(req *http.Request, agg *vendorGateway, path string, exclude map[string]struct{}, bodySource *requestBodySource, execution *requestExecution) (*preparedProxyRequest, *proxyError) {
	child := agg.aggPool.PickAvailable(aggregateChildAvailableForExecution(req, execution), exclude)
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
		vendor:           child.vendor,
		path:             child.vendor.rewrites.Apply(config.NormalizePath(path)),
		bodySource:       bodySource,
		allowedKeyIdxs:   child.keyIdxs,
		aggregateChildID: child.id,
	}, nil
}

func aggregateChildAvailable(e *aggregateChildEntry) bool {
	if e == nil || e.vendor == nil {
		return false
	}
	if !e.vendor.usesManagedUpstreamKeys() {
		return true
	}
	return e.vendor.hasAvailableKey(nil, e.keyIdxs)
}

func aggregateChildAvailableForExecution(req *http.Request, execution *requestExecution) func(*aggregateChildEntry) bool {
	return func(child *aggregateChildEntry) bool {
		if child == nil || child.vendor == nil {
			return false
		}
		if !child.vendor.usesManagedUpstreamKeys() {
			if execution != nil {
				return !execution.hasTried(child.vendor, child.vendor.passthroughUpstreamKey(req))
			}
			return true
		}
		// For managed keys the execution-aware check (which excludes
		// already-tried indexes) is strictly more restrictive than the
		// base aggregateChildAvailable check, so skip straight to it.
		if execution != nil {
			return child.vendor.hasAvailableKey(execution.triedKeyIndexes(child.vendor), child.keyIdxs)
		}
		return child.vendor.hasAvailableKey(nil, child.keyIdxs)
	}
}

func (r *Router) serveAggregateRequest(w http.ResponseWriter, req *http.Request, agg *vendorGateway, path string) {
	if err := agg.authorizeClient(req); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	retry := agg.aggRetry
	if !boolOrDefault(retry.Enabled, true) {
		// Retry disabled — behave like a single-attempt aggregate.
		execution := newRequestExecution(agg.maxUpstreamAttempts())
		prepared, proxyErr := r.prepareAggregateRequest(req, agg, path, nil, nil, execution)
		if proxyErr != nil {
			http.Error(w, proxyErr.message, proxyErr.statusCode)
			return
		}
		_ = r.serveVendorRequestWithAggregateHook(w, req, prepared, nil, execution)
		return
	}

	maxAttempts := retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 2
	}
	execution := newRequestExecution(agg.maxUpstreamAttempts())

	exclude := make(map[string]struct{})
	var bodySource *requestBodySource

	for attempt := 0; attempt < maxAttempts; attempt++ {
		prepared, proxyErr := r.prepareAggregateRequest(req, agg, path, exclude, bodySource, execution)
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
			// Switching children still costs an upstream attempt, so refuse
			// once the request-level budget is spent. Returning false here lets
			// the caller deliver the upstream response instead.
			if !execution.hasBudget() {
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
			nextExclude[prepared.aggregateChildID] = struct{}{}
			return agg.aggPool.HasAvailable(aggregateChildAvailableForExecution(req, execution), nextExclude)
		}
		if r.serveVendorRequestWithAggregateHook(w, req, prepared, shouldRetryAggregate, execution) == vendorRequestRetryAggregateChild {
			exclude[prepared.aggregateChildID] = struct{}{}
			continue
		}

		return
	}
}

func (r *Router) serveVendorRequest(w http.ResponseWriter, req *http.Request, prepared *preparedProxyRequest) {
	// prepareNonAggregateRequest reports a client disconnect as (nil, nil), so
	// prepared can legitimately be nil here. Guard before touching it: the
	// budget argument is evaluated at the call site, ahead of the callee's own
	// nil check.
	if prepared == nil || prepared.vendor == nil {
		return
	}
	_ = r.serveVendorRequestWithAggregateHook(w, req, prepared, nil, newRequestExecution(prepared.vendor.maxUpstreamAttempts()))
}

func (r *Router) serveVendorRequestWithAggregateHook(w http.ResponseWriter, req *http.Request, prepared *preparedProxyRequest, aggregateHook aggregateRetryHook, execution *requestExecution) vendorRequestOutcome {
	if prepared == nil || prepared.vendor == nil {
		return vendorRequestDone
	}

	vg := prepared.vendor
	interim := newInterimResponseSender(w, vg.interimInterval)
	defer interim.stop()

	triedManagedKeyIdx := execution.getOrCreateTriedKeyIndexes(vg)
	maxAttempts := vg.errorPolicy.Failover.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	attempts := 0

	for {
		attempts++
		if attempts > maxAttempts {
			// Unreachable: every retry site below is gated on retryRemaining.
			// Kept as a guard so a future edit cannot spin this loop.
			writeHTTPError(w, interim, "exceeded maximum failover attempts", http.StatusBadGateway)
			return vendorRequestDone
		}
		attempt, proxyErr := vg.newAttempt(req.Context(), req, prepared.path, prepared.bodySource, triedManagedKeyIdx, prepared.allowedKeyIdxs)
		if proxyErr != nil {
			writeMaskableGatewayError(w, interim, vg, proxyErr.message, proxyErr.statusCode)
			return vendorRequestDone
		}
		if attempt == nil {
			return vendorRequestDone
		}
		if !execution.consumeAttempt() {
			// Also unreachable: retryRemaining and the aggregate hook both
			// require remaining budget before another attempt is started.
			if vg.usesManagedUpstreamKeys() {
				vg.pool.Release(attempt.idx)
			}
			writeHTTPError(w, interim, "exceeded maximum upstream attempts", http.StatusBadGateway)
			return vendorRequestDone
		}
		execution.markTried(vg, attempt.selectedKey, attempt.idx)
		// Budget is consumed, so this reflects whether attempt N+1 is allowed.
		retryRemaining := attempts < maxAttempts && execution.hasBudget()

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
			canRetry := prepared.bodySource.canRetryRequestError(decision)
			if canRetry && retryRemaining && vg.hasAvailableKey(triedManagedKeyIdx, prepared.allowedKeyIdxs) {
				continue
			}
			if aggregateHook != nil && aggregateHook(0, err, prepared.bodySource) {
				return vendorRequestRetryAggregateChild
			}
			// No upstream response exists to forward, so a gateway error is the
			// only honest answer here. Masking rules can still normalize what the
			// client sees (e.g. 502 -> 500).
			writeMaskableGatewayError(w, interim, vg, "upstream request failed", http.StatusBadGateway)
			return vendorRequestDone
		}

		if vg.upstreamBodyTimeout > 0 {
			resp.Body = newIdleTimeoutReadCloser(resp.Body, vg.upstreamBodyTimeout)
		}

		switch r.handleUpstreamResponse(w, req, resp, vg, attempt, prepared.bodySource, triedManagedKeyIdx, prepared.allowedKeyIdxs, interim, aggregateHook, retryRemaining) {
		case upstreamResponseRetryVendorKey:
			continue
		case upstreamResponseRetryAggregateChild:
			return vendorRequestRetryAggregateChild
		default:
			return vendorRequestDone
		}
	}
}

func (r *Router) handleUpstreamResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, vg *vendorGateway, attempt *upstreamAttempt, bodySource *requestBodySource, triedManagedKeyIdx map[int]struct{}, allowedKeyIdxs []int, interim *interimResponseSender, aggregateHook aggregateRetryHook, vendorRetryRemaining bool) upstreamResponseOutcome {
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
			canRetry := bodySource.canRetryRequestError(decision)
			if canRetry && vendorRetryRemaining && vg.hasAvailableKey(triedManagedKeyIdx, allowedKeyIdxs) {
				return upstreamResponseRetryVendorKey
			}
			if aggregateHook != nil && aggregateHook(0, err, bodySource) {
				return upstreamResponseRetryAggregateChild
			}
			// The response body could not be read, so there is nothing to
			// forward downstream.
			writeMaskableGatewayError(w, interim, vg, "upstream request failed", http.StatusBadGateway)
			return upstreamResponseDone
		}

		decision := classifyResponse(vg.provider, vg.errorPolicy, resp.StatusCode, resp.Header, preview)
		// Masking is evaluated on the real upstream response but only applied
		// at delivery time: failover still runs first, and key health decisions
		// still see the true status code and body.
		maskRule, maskMatched := matchUpstreamErrorMask(vg.errorPolicy, resp.StatusCode, resp.Header, preview)
		if maskMatched {
			decision = extendDecisionCooldown(decision, maskRule)
		}
		deliverMasked := func() {
			drainMaskedUpstreamBody(bodyReader)
			_ = resp.Body.Close()
			writeMaskedResponse(w, interim, buildMaskedResponse(maskRule, resp.Header.Get("Retry-After")))
		}
		if bodySource.canRetryResponse(resp.StatusCode, decision) {
			vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
			if vendorRetryRemaining && vg.hasAvailableKey(triedManagedKeyIdx, allowedKeyIdxs) {
				_ = resp.Body.Close()
				return upstreamResponseRetryVendorKey
			}
			if aggregateHook != nil && aggregateHook(resp.StatusCode, nil, bodySource) {
				_ = resp.Body.Close()
				return upstreamResponseRetryAggregateChild
			}
			// Out of keys, attempts or budget: deliver the outcome we already
			// hold. A masking rule replaces the client-visible error with a
			// uniform response; otherwise the upstream response is forwarded
			// verbatim because its real status and Retry-After are exactly the
			// signal a rate-limited client needs.
			if maskMatched {
				deliverMasked()
				return upstreamResponseDone
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
		if maskMatched {
			// The masked body is synthesized, so the key decision is applied
			// here; writeUpstreamResponse would otherwise apply it once the
			// real body finished relaying.
			vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
			deliverMasked()
			return upstreamResponseDone
		}
		if err := r.writeUpstreamResponse(w, req, resp, bodyReader, vg, attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision, false, interim); errors.Is(err, errAbortDownstreamResponse) {
			panic(http.ErrAbortHandler)
		}
		return upstreamResponseDone
	}

	decision := classifyResponse(vg.provider, vg.errorPolicy, resp.StatusCode, resp.Header, nil)
	if err := r.writeUpstreamResponse(w, req, resp, resp.Body, vg, attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision, false, interim); errors.Is(err, errAbortDownstreamResponse) {
		panic(http.ErrAbortHandler)
	}
	return upstreamResponseDone
}
