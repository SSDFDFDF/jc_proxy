package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
	"jc_proxy/internal/config"
	"jc_proxy/internal/plugins"
	"jc_proxy/internal/plugins/codexhistory"
)

// Factories run once per router build; concrete implementations live outside
// the gateway and are shared by concurrent requests.
var builtinRequestPlugins = map[string]func(config.PluginConfig) plugins.RequestPlugin{
	"codex-history-filter": codexhistory.New,
}

type scriptRequestPlugin struct {
	plugins.Matcher
	program *goja.Program
}

func loadRequestPlugins(configs []config.PluginConfig) ([]*loadedRequestPlugin, error) {
	var out []*loadedRequestPlugin
	for _, cfg := range configs {
		if !cfg.IsEnabled() {
			continue
		}
		if strings.TrimSpace(cfg.Builtin) != "" {
			factory, ok := builtinRequestPlugins[cfg.Builtin]
			if !ok {
				return nil, fmt.Errorf("plugin %q: unknown builtin %q", cfg.Name, cfg.Builtin)
			}
			out = append(out, observeRequestPlugin(factory(cfg), cfg, ""))
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
		p := &scriptRequestPlugin{Matcher: plugins.NewMatcher(cfg), program: program}
		// Reject invalid initialization before a candidate router is persisted.
		if _, err := p.execute(context.Background(), nil); err != nil {
			return nil, fmt.Errorf("plugin %q: initialization failed", cfg.Name)
		}
		out = append(out, observeRequestPlugin(p, cfg, source))
	}
	return out, nil
}

func (p *scriptRequestPlugin) Transform(ctx context.Context, req plugins.Request) (plugins.Result, error) {
	return p.execute(ctx, &req)
}

func (p *scriptRequestPlugin) execute(parent context.Context, req *plugins.Request) (plugins.Result, error) {
	ctx, cancel := context.WithTimeout(parent, p.ExecutionTimeout())
	defer cancel()
	vm := goja.New()
	vm.SetMaxCallStackSize(256)
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()
	if err := ctx.Err(); err != nil {
		return plugins.Result{}, err
	}
	var summary plugins.Summary
	// Optional numeric report: no free text, body/ID retention or extra parse.
	_ = vm.Set("reportHistory", func(call goja.FunctionCall) goja.Value {
		var counts [3]int64
		for i := range counts {
			value := call.Argument(i)
			t := value.ExportType()
			if t != reflect.TypeFor[int64]() && t != reflect.TypeFor[float64]() {
				panic(vm.NewTypeError("reportHistory counts must be non-negative integers"))
			}
			n := value.ToFloat()
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 64<<20 || n != math.Trunc(n) {
				panic(vm.NewTypeError("reportHistory counts must be integers between 0 and 67108864"))
			}
			counts[i] = int64(n)
		}
		if call.Argument(3).ExportType() != reflect.TypeFor[bool]() {
			panic(vm.NewTypeError("reportHistory storeDisabled must be a boolean"))
		}
		summary = plugins.Summary{Reported: true, ReasoningRemoved: counts[0], IDsRemoved: counts[1], IncludesRemoved: counts[2], StoreDisabled: call.Argument(3).ToBoolean()}
		return goja.Undefined()
	})
	var rejection *plugins.RejectError
	_ = vm.Set("reject", func(status int, message string) {
		if status < 400 || status > 499 || message == "" || len(message) > 512 {
			panic(vm.NewTypeError("reject requires a 4xx status and a message of 1-512 bytes"))
		}
		rejection = &plugins.RejectError{StatusCode: status, Message: message}
		panic(vm.NewGoError(rejection))
	})
	hook, err := vm.RunProgram(p.program)
	if err == nil && req != nil {
		fn, ok := goja.AssertFunction(hook)
		if !ok {
			return plugins.Result{}, errors.New("onRequest must be a function")
		}
		// Use a value copy: script mutation cannot affect another plugin/vendor.
		result, callErr := fn(goja.Undefined(), vm.ToValue(map[string]any{
			"body": string(req.Body), "method": req.Method, "path": req.Path,
			"vendor_id": req.VendorID, "vendor": req.Vendor, "provider": req.Provider,
		}))
		err = callErr
		if ctx.Err() != nil {
			return plugins.Result{}, ctx.Err()
		}
		if rejection != nil {
			return plugins.Result{}, rejection
		}
		if err == nil {
			if goja.IsUndefined(result) || goja.IsNull(result) {
				return plugins.Result{Body: req.Body, Summary: summary}, nil
			}
			if value, ok := result.(goja.String); ok {
				raw := value.String()
				if int64(len(raw)) > p.BodyLimit() {
					return plugins.Result{}, errPluginOutputLimit
				}
				return plugins.Result{Body: []byte(raw), Summary: summary}, nil
			}
			return plugins.Result{}, errors.New("onRequest must return a string, null or undefined")
		}
	}
	if ctx.Err() != nil {
		return plugins.Result{}, ctx.Err()
	}
	return plugins.Result{}, err
}

// applyRequestPlugins leaves source holding the original bytes for aggregate
// failover. The returned body belongs only to this vendor and is reused across
// key retries, so non-idempotent transforms run once per vendor dispatch.
func (v *vendorGateway) applyRequestPlugins(req *http.Request, path routedPath, source *requestBodySource) (*requestBodySource, *proxyError) {
	var matchedStorage [16]*loadedRequestPlugin
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
		matched[0].blocked("content_encoding", 0)
		return nil, &proxyError{http.StatusUnsupportedMediaType, "request plugins require identity Content-Encoding"}
	}
	limit := matched[0].BodyLimit()
	if source != nil && source.contentLength > limit {
		matched[0].blocked("input_too_large", int(source.contentLength))
		return nil, &proxyError{http.StatusRequestEntityTooLarge, "request body exceeds plugin limit"}
	}
	body, err := source.Body()
	if err != nil {
		matched[0].blocked("read_failed", 0)
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
			matched[0].blocked("read_failed", 0)
			return nil, &proxyError{http.StatusBadRequest, "read request body failed"}
		}
		if overflow {
			matched[0].blocked("input_too_large", int(limit)+1)
			return nil, &proxyError{http.StatusRequestEntityTooLarge, "request body exceeds plugin limit"}
		}
	}
	if !utf8.Valid(raw) {
		matched[0].blocked("invalid_utf8", len(raw))
		return nil, &proxyError{http.StatusBadRequest, "request plugins require a UTF-8 body"}
	}
	if source != nil && !buffered {
		*source = *newBufferedBodySource(raw, source.safeRetry, false)
	}
	input := plugins.Request{Body: raw, Method: req.Method, Path: path.path, VendorID: v.id, Vendor: v.name, Provider: v.provider}
	changed := false
	for _, plugin := range matched {
		if int64(len(input.Body)) > plugin.BodyLimit() {
			plugin.blocked("input_too_large", len(input.Body))
			return nil, &proxyError{http.StatusRequestEntityTooLarge, "request body exceeds plugin limit"}
		}
		output, pluginChanged, err := plugin.transform(req.Context(), input)
		if err != nil {
			var rejected *plugins.RejectError
			if errors.As(err, &rejected) {
				return nil, &proxyError{rejected.StatusCode, rejected.Message}
			}
			// Do not echo exceptions: scripts may throw request contents.
			return nil, &proxyError{http.StatusInternalServerError, fmt.Sprintf("request plugin %q failed", plugin.Name())}
		}
		input.Body = output.Body
		changed = pluginChanged
	}
	// Reuse the comparison from observation for the common single-plugin path.
	// A chain may undo an earlier transform, so compare its final bytes once.
	if len(matched) > 1 {
		changed = !bytes.Equal(input.Body, raw)
	}
	return newBufferedBodySource(input.Body, isSafeRetryMethod(req.Method), changed), nil
}
