package gateway

import (
	"bytes"
	"context"
	"encoding/json"

	"jc_proxy/internal/config"
)

// Add native implementations here. Factories run once per router build;
// plugins are immutable and shared by concurrent requests.
var builtinRequestPlugins = map[string]func(config.PluginConfig) RequestPlugin{
	"codex-history-filter": func(cfg config.PluginConfig) RequestPlugin {
		return &codexHistoryPlugin{pluginMatcher{cfg: cfg}}
	},
}

type codexHistoryPlugin struct{ pluginMatcher }

// Transform implements the portable history rules from AgentRouter-Codex-Filter
// (MIT; see plugins/LICENSE.AgentRouter-Codex-Filter). RawMessage keeps untouched
// values, including large integers and nested resource IDs, lossless.
func (p *codexHistoryPlugin) Transform(parent context.Context, req PluginRequest) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, p.cfg.ExecutionTimeout())
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil || body == nil {
		return nil, rejectHistory("Responses request must be a JSON object.")
	}
	if historyReference(body["previous_response_id"]) || historyReference(body["conversation"]) {
		return nil, rejectHistory("History filtering requires complete input history instead of previous_response_id or conversation.")
	}
	if raw, ok := body["context_management"]; ok {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil {
			for _, item := range items {
				var fields map[string]json.RawMessage
				if json.Unmarshal(item, &fields) == nil && historyString(fields["type"]) == "compaction" {
					return nil, rejectHistory("History filtering cannot transfer compaction context. Start a session with a text summary.")
				}
			}
		}
	}
	changed := !bytes.Equal(bytes.TrimSpace(body["store"]), []byte("false"))
	body["store"] = json.RawMessage("false")
	if raw, ok := body["include"]; ok {
		var include []json.RawMessage
		if err := json.Unmarshal(raw, &include); err != nil || include == nil {
			return nil, rejectHistory("include must be an array.")
		}
		filtered := include[:0]
		for _, value := range include {
			if historyString(value) != "reasoning.encrypted_content" {
				filtered = append(filtered, value)
			}
		}
		if len(filtered) != len(include) {
			changed = true
			body["include"] = appendHistoryArray(nil, filtered)
		}
	}
	if raw, ok := body["input"]; ok {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || (raw[0] != '[' && raw[0] != '"') {
			return nil, rejectHistory("input must be a string or an array.")
		}
		if raw[0] == '[' {
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, rejectHistory("input must be a string or an array.")
			}
			filtered := items[:0]
			inputChanged := false
			for i, item := range items {
				if i%64 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(item, &fields) != nil || fields == nil {
					filtered = append(filtered, item)
					continue
				}
				kind := historyString(fields["type"])
				if kind == "reasoning" {
					inputChanged = true
					continue
				}
				if kind == "compaction" || kind == "item_reference" || historyReference(fields["encrypted_content"]) {
					return nil, rejectHistory("History filtering cannot transfer encrypted context or item references. Supply complete text history.")
				}
				portable := false
				switch kind {
				case "message", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output":
					portable = true
				default:
					_, hasType := fields["type"]
					_, hasContent := fields["content"]
					var role string
					portable = !hasType && hasContent && json.Unmarshal(fields["role"], &role) == nil && !bytes.Equal(fields["role"], []byte("null"))
				}
				if _, hasID := fields["id"]; portable && hasID {
					delete(fields, "id")
					item = appendHistoryObject(make([]byte, 0, len(item)), fields)
					inputChanged = true
				}
				filtered = append(filtered, item)
			}
			if inputChanged {
				body["input"] = appendHistoryArray(make([]byte, 0, len(raw)), filtered)
				changed = true
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !changed {
		return req.Body, nil
	}
	return appendHistoryObject(make([]byte, 0, len(req.Body)+16), body), nil
}

// Values were validated by json.Unmarshal. Append them directly to avoid
// revalidating/escaping large message contents at every enclosing JSON level.
func appendHistoryObject(dst []byte, fields map[string]json.RawMessage) []byte {
	dst = append(dst, '{')
	first := true
	for key, value := range fields {
		if !first {
			dst = append(dst, ',')
		}
		first = false
		encodedKey, _ := json.Marshal(key)
		dst = append(dst, encodedKey...)
		dst = append(dst, ':')
		dst = append(dst, value...)
	}
	return append(dst, '}')
}

func appendHistoryArray(dst []byte, items []json.RawMessage) []byte {
	dst = append(dst, '[')
	for i, item := range items {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, item...)
	}
	return append(dst, ']')
}

func rejectHistory(message string) error {
	return &PluginRejectError{StatusCode: 400, Message: message}
}

func historyString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func historyReference(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", `""`, "false", "0":
		return false
	default:
		return true
	}
}
