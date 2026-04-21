package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"jc_proxy/internal/config"
	"jc_proxy/internal/resin"
)

const vendorTestMaxResponseBytes = 256 * 1024

type VendorTestPreset struct {
	Label    string `json:"label"`
	Method   string `json:"method"`
	Endpoint string `json:"endpoint"`
	Body     string `json:"body,omitempty"`
}

type VendorTestMeta struct {
	Vendor         string             `json:"vendor"`
	Provider       string             `json:"provider"`
	BaseURL        string             `json:"base_url"`
	ModelEndpoints []string           `json:"model_endpoints"`
	RequestPresets []VendorTestPreset `json:"request_presets"`
}

type VendorTestRequest struct {
	BaseURL  string            `json:"base_url,omitempty"`
	Method   string            `json:"method,omitempty"`
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Key      string            `json:"key,omitempty"`
}

type VendorTestResult struct {
	Provider    string              `json:"provider"`
	BaseURL     string              `json:"base_url"`
	Endpoint    string              `json:"endpoint"`
	ResolvedURL string              `json:"resolved_url"`
	Method      string              `json:"method"`
	StatusCode  int                 `json:"status_code"`
	Headers     map[string][]string `json:"headers"`
	Body        string              `json:"body"`
	Truncated   bool                `json:"truncated"`
	DurationMS  int64               `json:"duration_ms"`
}

func (rt *Runtime) VendorTestMeta(vendor string) (VendorTestMeta, error) {
	rt.mu.RLock()
	router := rt.router
	rt.mu.RUnlock()
	if router == nil {
		return VendorTestMeta{}, errors.New("runtime router unavailable")
	}
	return router.VendorTestMeta(vendor)
}

func (rt *Runtime) ExecuteVendorTest(ctx context.Context, vendor string, req VendorTestRequest) (*VendorTestResult, error) {
	rt.mu.RLock()
	router := rt.router
	rt.mu.RUnlock()
	if router == nil {
		return nil, errors.New("runtime router unavailable")
	}
	return router.ExecuteVendorTest(ctx, vendor, req)
}

func (r *Router) VendorTestMeta(vendor string) (VendorTestMeta, error) {
	vg, err := r.lookupVendor(vendor)
	if err != nil {
		return VendorTestMeta{}, err
	}

	return VendorTestMeta{
		Vendor:         vg.name,
		Provider:       vg.provider,
		BaseURL:        vg.baseURL.String(),
		ModelEndpoints: suggestedModelEndpoints(vg.provider),
		RequestPresets: suggestedRequestPresets(vg.provider),
	}, nil
}

func (r *Router) ExecuteVendorTest(ctx context.Context, vendor string, req VendorTestRequest) (*VendorTestResult, error) {
	vg, err := r.lookupVendor(vendor)
	if err != nil {
		return nil, err
	}
	return vg.executeTest(ctx, req)
}

func (r *Router) lookupVendor(vendor string) (*vendorGateway, error) {
	name := strings.TrimSpace(vendor)
	if name == "" {
		return nil, errors.New("vendor is required")
	}
	vg := r.vendors[name]
	if vg == nil {
		return nil, fmt.Errorf("vendor %q not found", name)
	}
	return vg, nil
}

func suggestedModelEndpoints(provider string) []string {
	switch provider {
	case "gemini":
		return []string{"/v1beta/models", "/v1/models"}
	default:
		return []string{"/v1/models", "/models"}
	}
}

func suggestedRequestPresets(provider string) []VendorTestPreset {
	switch provider {
	case "anthropic":
		return []VendorTestPreset{
			{
				Label:    "Messages",
				Method:   http.MethodPost,
				Endpoint: "/v1/messages",
				Body:     "{\n  \"model\": \"{model}\",\n  \"max_tokens\": 64,\n  \"messages\": [\n    {\n      \"role\": \"user\",\n      \"content\": \"ping\"\n    }\n  ]\n}",
			},
		}
	case "gemini":
		return []VendorTestPreset{
			{
				Label:    "Generate Content",
				Method:   http.MethodPost,
				Endpoint: "/v1beta/models/{model}:generateContent",
				Body:     "{\n  \"contents\": [\n    {\n      \"role\": \"user\",\n      \"parts\": [\n        {\n          \"text\": \"ping\"\n        }\n      ]\n    }\n  ]\n}",
			},
		}
	default:
		return []VendorTestPreset{
			{
				Label:    "Chat Completions",
				Method:   http.MethodPost,
				Endpoint: "/v1/chat/completions",
				Body:     "{\n  \"model\": \"{model}\",\n  \"messages\": [\n    {\n      \"role\": \"user\",\n      \"content\": \"ping\"\n    }\n  ],\n  \"stream\": false\n}",
			},
		}
	}
}

func (v *vendorGateway) executeTest(ctx context.Context, req VendorTestRequest) (*VendorTestResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	baseURL, err := resolveVendorTestBaseURL(req.BaseURL, v.baseURL)
	if err != nil {
		return nil, err
	}

	endpointPath, rawQuery, originalEndpoint, err := resolveVendorTestEndpoint(req.Endpoint)
	if err != nil {
		return nil, err
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		if strings.TrimSpace(req.Body) != "" {
			method = http.MethodPost
		} else {
			method = http.MethodGet
		}
	}

	resolvedPath := v.rewrites.Apply(config.NormalizePath(endpointPath))
	targetURL, err := buildTargetURLFromBase(baseURL, resolvedPath, rawQuery, strings.TrimSpace(req.Key), v.resinRuntime)
	if err != nil {
		return nil, err
	}

	bodyBytes := []byte(req.Body)
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	upReq, err := http.NewRequestWithContext(reqCtx, method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build upstream request failed: %w", err)
	}
	upReq.Header = v.buildTestHeaders(req.Headers, strings.TrimSpace(req.Key), len(bodyBytes) > 0)
	upReq.Host = upReq.URL.Host
	if len(bodyBytes) > 0 {
		upReq.ContentLength = int64(len(bodyBytes))
	}

	startedAt := time.Now()
	resp, err := v.client.Do(upReq)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()

	body, truncated, err := readVendorTestBody(resp.Body, vendorTestMaxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read upstream response failed: %w", err)
	}

	return &VendorTestResult{
		Provider:    v.provider,
		BaseURL:     baseURL.String(),
		Endpoint:    originalEndpoint,
		ResolvedURL: targetURL,
		Method:      method,
		StatusCode:  resp.StatusCode,
		Headers:     cloneHeaderMap(resp.Header),
		Body:        body,
		Truncated:   truncated,
		DurationMS:  time.Since(startedAt).Milliseconds(),
	}, nil
}

func (v *vendorGateway) buildTestHeaders(extra map[string]string, selectedKey string, hasBody bool) http.Header {
	headers := make(http.Header, len(extra)+4)
	for key, value := range extra {
		canon := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(key))
		if canon == "" || isHopHeader(canon) {
			continue
		}
		headers.Set(canon, strings.TrimSpace(value))
	}

	if hasBody && headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", "application/json")
	}
	if headers.Get("Accept") == "" {
		headers.Set("Accept", "application/json")
	}
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", "JCProxy-Admin-Test")
	}

	for key, value := range v.injectHeaders {
		replaced := strings.ReplaceAll(value, "{{vendor}}", v.name)
		replaced = strings.ReplaceAll(replaced, "{{upstream_key}}", selectedKey)
		headers.Set(key, replaced)
	}

	switch v.upstreamAuth.Mode {
	case "bearer", "header":
		if selectedKey != "" {
			headers.Set(v.upstreamAuth.Header, v.upstreamAuth.Prefix+selectedKey)
		}
	case "passthrough":
	}

	if v.resinRuntime != nil && selectedKey != "" {
		headers.Set(resin.AccountHeader, resin.BuildAccount(v.provider, selectedKey))
	}

	headers.Del("Host")
	return headers
}

func resolveVendorTestBaseURL(raw string, fallback *url.URL) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		if fallback == nil {
			return nil, errors.New("base_url is required")
		}
		cloned := *fallback
		return &cloned, nil
	}

	baseURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil {
		return nil, fmt.Errorf("parse base_url: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, errors.New("invalid base_url")
	}
	return baseURL, nil
}

func resolveVendorTestEndpoint(raw string) (path string, rawQuery string, original string, err error) {
	original = strings.TrimSpace(raw)
	if original == "" {
		return "", "", "", errors.New("endpoint is required")
	}

	parsed, err := url.Parse(original)
	if err != nil {
		return "", "", "", fmt.Errorf("parse endpoint: %w", err)
	}
	if parsed.IsAbs() {
		return "", "", "", errors.New("endpoint must be a relative path")
	}

	path = parsed.Path
	if path == "" {
		path = original
	}
	return config.NormalizePath(path), parsed.RawQuery, original, nil
}

func readVendorTestBody(body io.Reader, maxBytes int64) (string, bool, error) {
	if body == nil {
		return "", false, nil
	}

	limited := &io.LimitedReader{R: body, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", false, err
	}
	if int64(len(data)) > maxBytes {
		return string(data[:maxBytes]), true, nil
	}
	return string(data), false, nil
}

func cloneHeaderMap(src http.Header) map[string][]string {
	out := make(map[string][]string, len(src))
	for key, values := range src {
		out[key] = append([]string(nil), values...)
	}
	return out
}
