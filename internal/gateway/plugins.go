package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
	"jc_proxy/internal/config"
)

// RequestPlugin is the request-body extension point. Implementations must be
// safe for concurrent use and must treat Body as immutable. A transform never
// receives credentials or an upstream response and cannot change routing,
// authentication or retry policy.
type RequestPlugin interface {
	Name() string
	Matches(method, path string) bool
	BodyLimit() int64
	Transform(context.Context, PluginRequest) ([]byte, error)
}

type PluginRequest struct {
	Body     []byte `json:"body"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	VendorID string `json:"vendor_id"`
	Vendor   string `json:"vendor"`
	Provider string `json:"provider"`
}

// PluginRejectError is an intentional client rejection, separate from script
// execution failures. Neither kind is an upstream attempt or a key failure.
type PluginRejectError struct {
	StatusCode int
	Message    string
}

func (e *PluginRejectError) Error() string { return e.Message }

type scriptRequestPlugin struct {
	pluginMatcher
	program *goja.Program
}

type pluginMatcher struct{ cfg config.PluginConfig }

func (p pluginMatcher) Name() string     { return p.cfg.Name }
func (p pluginMatcher) BodyLimit() int64 { return p.cfg.BodyLimit() }
func (p pluginMatcher) Matches(method, path string) bool {
	methodOK := len(p.cfg.Methods) == 0
	for _, m := range p.cfg.Methods {
		if method == m {
			methodOK = true
			break
		}
	}
	if !methodOK {
		return false
	}
	if len(p.cfg.Paths) == 0 {
		return true
	}
	for _, pattern := range p.cfg.Paths {
		if pattern == path || (strings.HasSuffix(pattern, "*") && strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))) {
			return true
		}
	}
	return false
}

func loadRequestPlugins(configs []config.PluginConfig) ([]RequestPlugin, error) {
	var out []RequestPlugin
	for _, cfg := range configs {
		if !cfg.IsEnabled() {
			continue
		}
		if strings.TrimSpace(cfg.Builtin) != "" {
			factory, ok := builtinRequestPlugins[cfg.Builtin]
			if !ok {
				return nil, fmt.Errorf("plugin %q: unknown builtin %q", cfg.Name, cfg.Builtin)
			}
			out = append(out, factory(cfg))
			continue
		}
		source := cfg.Source
		if strings.TrimSpace(cfg.Script) != "" {
			info, err := os.Stat(cfg.Script)
			if err != nil || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("plugin %q: script must be an existing regular file", cfg.Name)
			}
			f, err := os.Open(cfg.Script)
			if err != nil {
				return nil, fmt.Errorf("plugin %q: open script: %w", cfg.Name, err)
			}
			info, err = f.Stat()
			if err != nil || !info.Mode().IsRegular() {
				_ = f.Close()
				return nil, fmt.Errorf("plugin %q: script must be a regular file", cfg.Name)
			}
			data, err := io.ReadAll(io.LimitReader(f, config.MaxPluginScriptBytes+1))
			_ = f.Close()
			if err != nil {
				return nil, fmt.Errorf("plugin %q: read script: %w", cfg.Name, err)
			}
			source = string(data)
		}
		if len(source) > config.MaxPluginScriptBytes {
			return nil, fmt.Errorf("plugin %q: script exceeds 1 MiB", cfg.Name)
		}
		// Returning the hook from an IIFE supports both declarations and const
		// functions, without leaking the script's globals into the host API.
		program, err := goja.Compile(cfg.Name, "(function() {\n\"use strict\";\n"+source+"\nif (typeof onRequest !== 'function') throw new Error('onRequest must be a function');\nreturn onRequest;\n})()", true)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: compile failed", cfg.Name)
		}
		p := &scriptRequestPlugin{pluginMatcher: pluginMatcher{cfg: cfg}, program: program}
		// Reject invalid initialization before a candidate router is persisted.
		if _, err := p.execute(context.Background(), nil); err != nil {
			return nil, fmt.Errorf("plugin %q: initialization failed", cfg.Name)
		}
		out = append(out, p)
	}
	return out, nil
}

func (p *scriptRequestPlugin) Transform(ctx context.Context, req PluginRequest) ([]byte, error) {
	return p.execute(ctx, &req)
}

func (p *scriptRequestPlugin) execute(parent context.Context, req *PluginRequest) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, p.cfg.ExecutionTimeout())
	defer cancel()
	vm := goja.New()
	vm.SetMaxCallStackSize(256)
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rejection *PluginRejectError
	_ = vm.Set("reject", func(status int, message string) {
		if status < 400 || status > 499 || message == "" || len(message) > 512 {
			panic(vm.NewTypeError("reject requires a 4xx status and a message of 1-512 bytes"))
		}
		rejection = &PluginRejectError{StatusCode: status, Message: message}
		panic(vm.NewGoError(rejection))
	})
	hook, err := vm.RunProgram(p.program)
	if err == nil && req != nil {
		fn, ok := goja.AssertFunction(hook)
		if !ok {
			return nil, errors.New("onRequest must be a function")
		}
		// Use a value copy: script mutation cannot affect another plugin/vendor.
		result, callErr := fn(goja.Undefined(), vm.ToValue(map[string]any{
			"body": string(req.Body), "method": req.Method, "path": req.Path,
			"vendor_id": req.VendorID, "vendor": req.Vendor, "provider": req.Provider,
		}))
		err = callErr
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if rejection != nil {
			return nil, rejection
		}
		if err == nil {
			if goja.IsUndefined(result) || goja.IsNull(result) {
				return req.Body, nil
			}
			if value, ok := result.(goja.String); ok {
				raw := value.String()
				if int64(len(raw)) > p.BodyLimit() {
					return nil, errors.New("plugin output exceeds body limit")
				}
				return []byte(raw), nil
			}
			return nil, errors.New("onRequest must return a string, null or undefined")
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, err
}

// applyRequestPlugins leaves source holding the original bytes for aggregate
// failover. The returned body belongs only to this vendor and is reused across
// key retries, so non-idempotent transforms run once per vendor dispatch.
func (v *vendorGateway) applyRequestPlugins(req *http.Request, path routedPath, source *requestBodySource) (*requestBodySource, *proxyError) {
	var matchedStorage [16]RequestPlugin
	matched := matchedStorage[:0]
	for _, plugin := range v.plugins {
		if plugin.Matches(req.Method, path.path) {
			matched = append(matched, plugin)
		}
	}
	if len(matched) == 0 {
		return source, nil
	}
	encoding := strings.TrimSpace(req.Header.Get("Content-Encoding"))
	if encoding != "" && !strings.EqualFold(encoding, "identity") {
		return nil, &proxyError{http.StatusUnsupportedMediaType, "request plugins require identity Content-Encoding"}
	}
	limit := matched[0].BodyLimit()
	if source != nil && source.contentLength > limit {
		return nil, &proxyError{http.StatusRequestEntityTooLarge, "request body exceeds plugin limit"}
	}
	body, err := source.Body()
	if err != nil {
		return nil, &proxyError{http.StatusBadRequest, "read request body failed"}
	}
	if body != nil {
		defer body.Close()
	}
	var raw []byte
	buffered := false
	if reader, ok := body.(*requestBodyReader); ok {
		if cached, ok := reader.ReadCloser.(*bufferedRequestBody); ok {
			raw, buffered = cached.raw, true
		}
	}
	if !buffered {
		var overflow bool
		raw, overflow, err = readReplayableRequestBody(body, limit)
		if err != nil {
			return nil, &proxyError{http.StatusBadRequest, "read request body failed"}
		}
		if overflow {
			return nil, &proxyError{http.StatusRequestEntityTooLarge, "request body exceeds plugin limit"}
		}
	}
	if !utf8.Valid(raw) {
		return nil, &proxyError{http.StatusBadRequest, "request plugins require a UTF-8 body"}
	}
	if source != nil && !buffered {
		*source = *newBufferedBodySource(raw, source.safeRetry, false)
	}
	input := PluginRequest{Body: raw, Method: req.Method, Path: path.path, VendorID: v.id, Vendor: v.name, Provider: v.provider}
	for _, plugin := range matched {
		if int64(len(input.Body)) > plugin.BodyLimit() {
			return nil, &proxyError{http.StatusRequestEntityTooLarge, "request body exceeds plugin limit"}
		}
		output, err := plugin.Transform(req.Context(), input)
		if err != nil {
			var rejected *PluginRejectError
			if errors.As(err, &rejected) {
				return nil, &proxyError{rejected.StatusCode, rejected.Message}
			}
			// Do not echo exceptions: scripts may throw request contents.
			return nil, &proxyError{http.StatusInternalServerError, fmt.Sprintf("request plugin %q failed", plugin.Name())}
		}
		input.Body = output
		if int64(len(output)) > plugin.BodyLimit() {
			return nil, &proxyError{http.StatusInternalServerError, fmt.Sprintf("request plugin %q output exceeds body limit", plugin.Name())}
		}
	}
	return newBufferedBodySource(input.Body, isSafeRetryMethod(req.Method), !bytes.Equal(input.Body, raw)), nil
}
