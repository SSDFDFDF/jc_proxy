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

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.serveInternalRoute(w, req) {
		return
	}

	prepared, proxyErr := r.prepareProxyRequest(req)
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

func (r *Router) prepareProxyRequest(req *http.Request) (*preparedProxyRequest, *proxyError) {
	vendorName, upstreamPath, ok := splitVendorPath(req.URL.Path)
	if !ok {
		return nil, &proxyError{statusCode: http.StatusNotFound, message: "not found"}
	}

	vg, exists := r.vendors[vendorName]
	if !exists {
		return nil, &proxyError{statusCode: http.StatusNotFound, message: "unknown vendor"}
	}
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

func (r *Router) serveVendorRequest(w http.ResponseWriter, req *http.Request, prepared *preparedProxyRequest) {
	if prepared == nil || prepared.vendor == nil {
		return
	}

	vg := prepared.vendor
	triedManagedKeyIdx := make(map[int]struct{})
	for {
		attempt, proxyErr := vg.newAttempt(req.Context(), req, prepared.path, prepared.bodySource, triedManagedKeyIdx)
		if proxyErr != nil {
			http.Error(w, proxyErr.message, proxyErr.statusCode)
			return
		}
		if attempt == nil {
			return
		}

		resp, err := vg.client.Do(attempt.request)
		if err != nil {
			if isCanceledUpstreamError(req.Context(), err) {
				if vg.usesManagedUpstreamKeys() {
					vg.pool.Release(attempt.idx)
				}
				return
			}
			decision := classifyRequestError(vg.provider, vg.errorPolicy, fmt.Sprintf("upstream request failed: %v", err))
			vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
			if prepared.bodySource.canRetryRequestError(decision) && vg.usesManagedUpstreamKeys() {
				triedManagedKeyIdx[attempt.idx] = struct{}{}
			}
			if prepared.bodySource.canRetryRequestError(decision) && vg.hasAvailableKey(triedManagedKeyIdx) {
				continue
			}
			http.Error(w, "upstream request failed", http.StatusBadGateway)
			return
		}

		if vg.upstreamBodyTimeout > 0 {
			resp.Body = newIdleTimeoutReadCloser(resp.Body, vg.upstreamBodyTimeout)
		}

		if r.handleUpstreamResponse(w, req, resp, vg, attempt, prepared.bodySource, triedManagedKeyIdx) {
			return
		}
	}
}

func (r *Router) handleUpstreamResponse(w http.ResponseWriter, req *http.Request, resp *http.Response, vg *vendorGateway, attempt *upstreamAttempt, bodySource *requestBodySource, triedManagedKeyIdx map[int]struct{}) bool {
	if resp.StatusCode >= http.StatusBadRequest {
		preview, bodyReader, err := captureResponsePreview(resp.Body, 2048, resp.Header)
		if err != nil {
			_ = resp.Body.Close()
			if isCanceledUpstreamError(req.Context(), err) {
				if vg.usesManagedUpstreamKeys() {
					vg.pool.Release(attempt.idx)
				}
				return true
			}
			decision := classifyRequestError(vg.provider, vg.errorPolicy, fmt.Sprintf("read upstream response failed: %v", err))
			vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
			if bodySource.canRetryRequestError(decision) && vg.usesManagedUpstreamKeys() {
				triedManagedKeyIdx[attempt.idx] = struct{}{}
			}
			if bodySource.canRetryRequestError(decision) && vg.hasAvailableKey(triedManagedKeyIdx) {
				return false
			}
			http.Error(w, "upstream request failed", http.StatusBadGateway)
			return true
		}

		decision := classifyResponse(vg.provider, vg.errorPolicy, resp.StatusCode, resp.Header, preview)
		if bodySource.canRetryResponse(resp.StatusCode, decision) {
			vg.applyDecision(attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision)
			if vg.usesManagedUpstreamKeys() {
				triedManagedKeyIdx[attempt.idx] = struct{}{}
			}
			if vg.hasAvailableKey(triedManagedKeyIdx) {
				_ = resp.Body.Close()
				return false
			}
			if err := r.writeUpstreamResponse(w, req, resp, bodyReader, vg, attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision, true); errors.Is(err, errAbortDownstreamResponse) {
				panic(http.ErrAbortHandler)
			}
			return true
		}

		if err := r.writeUpstreamResponse(w, req, resp, bodyReader, vg, attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision, false); errors.Is(err, errAbortDownstreamResponse) {
			panic(http.ErrAbortHandler)
		}
		return true
	}

	decision := classifyResponse(vg.provider, vg.errorPolicy, resp.StatusCode, resp.Header, nil)
	if err := r.writeUpstreamResponse(w, req, resp, resp.Body, vg, attempt.idx, attempt.selectedKey, attempt.selectedVersion, decision, false); errors.Is(err, errAbortDownstreamResponse) {
		panic(http.ErrAbortHandler)
	}
	return true
}
