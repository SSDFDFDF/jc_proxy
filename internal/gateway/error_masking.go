package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"jc_proxy/internal/config"
)

// Only incomplete masked bodies need best-effort draining. Bound both bytes
// and total time: trickling data must not hold a synthetic response indefinitely.
const (
	maxMaskedBodyDrainBytes = 64 << 10
	maskedBodyDrainTimeout  = 50 * time.Millisecond
)

// maskedResponse is the uniform replacement a masking rule prescribes for a
// matched error. Upstream headers are deliberately dropped: masking exists to
// hide upstream error detail from the client.
type maskedResponse struct {
	statusCode  int
	contentType string
	body        []byte
	retryAfter  string // rendered Retry-After header value; "" = omit the header
}

// errorMaskingEnabled reports whether masking is switched on for a policy.
// Absent configuration means enabled (masking is opt-in per rule, not per
// flag).
func errorMaskingEnabled(masking config.ErrorMaskingConfig) bool {
	return boolOrDefault(masking.Enabled, true)
}

// matchErrorMaskRule applies the same matching semantics as cooldown
// response_rules: a rule matches when the status code is listed (or the rule
// lists none) and the body contains one of the keywords (or the rule lists
// none); at least one of the two must be configured. The first matching rule
// wins.
func matchErrorMaskRule(statusCode int, body string, rules []config.ErrorMaskingRule) (config.ErrorMaskingRule, bool) {
	for _, rule := range rules {
		if len(rule.StatusCodes) > 0 && !containsStatusCode(rule.StatusCodes, statusCode) {
			continue
		}
		if hasNonEmptyPattern(rule.Keywords) && !containsAnyKeyword(body, rule.Keywords) {
			continue
		}
		if len(rule.StatusCodes) == 0 && !hasNonEmptyPattern(rule.Keywords) {
			continue
		}
		return rule, true
	}
	return config.ErrorMaskingRule{}, false
}

// matchUpstreamErrorMask checks masking rules against an upstream error
// response. body is the already-normalized preview shared with classification.
// Only error responses (status >= 400) can be masked.
func matchUpstreamErrorMask(policy config.ErrorPolicyConfig, statusCode int, body string) (config.ErrorMaskingRule, bool) {
	if len(policy.Masking.Rules) == 0 || !errorMaskingEnabled(policy.Masking) || statusCode < http.StatusBadRequest {
		return config.ErrorMaskingRule{}, false
	}
	return matchErrorMaskRule(statusCode, body, policy.Masking.Rules)
}

// matchGatewayErrorMask checks masking rules against a gateway-synthesized
// error (the 502 "upstream request failed" response, the 503 "all vendor keys
// in cooldown or disabled" response, ...). Keywords are matched against the
// gateway error message.
func matchGatewayErrorMask(policy config.ErrorPolicyConfig, statusCode int, message string) (config.ErrorMaskingRule, bool) {
	if !errorMaskingEnabled(policy.Masking) {
		return config.ErrorMaskingRule{}, false
	}
	return matchErrorMaskRule(statusCode, message, policy.Masking.Rules)
}

// extendDecisionCooldown folds a masking rule's backoff extension into the
// classification decision. It only ever extends: the longer of the classified
// cooldown and the rule cooldown wins, and a disable decision (the most severe
// outcome) is never downgraded.
func extendDecisionCooldown(decision keyDecision, rule config.ErrorMaskingRule) keyDecision {
	if rule.Cooldown <= 0 || decision.action == keyActionDisable {
		return decision
	}
	if decision.action == keyActionCooldown && decision.cooldown >= rule.Cooldown {
		return decision
	}
	decision.action = keyActionCooldown
	decision.cooldown = rule.Cooldown
	return decision
}

// buildMaskedResponse renders the uniform client-visible replacement for a
// matched rule. upstreamRetryAfter is the raw Retry-After header value of the
// response being masked (empty for gateway-synthesized errors).
func buildMaskedResponse(rule config.ErrorMaskingRule, upstreamRetryAfter string) maskedResponse {
	contentType := strings.TrimSpace(rule.ContentType)
	if contentType == "" {
		contentType = "application/json"
	}

	retryAfter := ""
	switch maskedRetryAfterMode(rule.RetryAfter) {
	case "ignore":
		// never set the header
	case "set":
		if d, err := time.ParseDuration(strings.TrimSpace(rule.RetryAfter)); err == nil {
			retryAfter = strconv.Itoa(int(d / time.Second))
		}
	default: // preserve
		retryAfter = strings.TrimSpace(upstreamRetryAfter)
	}

	return maskedResponse{
		statusCode:  rule.StatusCode,
		contentType: contentType,
		body:        buildMaskedResponseBody(rule),
		retryAfter:  retryAfter,
	}
}

// maskedRetryAfterMode normalizes a rule's retry_after setting into preserve /
// ignore / set.
func maskedRetryAfterMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "preserve":
		return "preserve"
	case "ignore":
		return "ignore"
	default:
		return "set"
	}
}

// buildMaskedResponseBody builds the masked body: the exact configured body
// when given, otherwise a compact JSON error whose message comes from the rule
// or falls back to the standard status text.
func buildMaskedResponseBody(rule config.ErrorMaskingRule) []byte {
	if body := rule.Body; strings.TrimSpace(body) != "" {
		return []byte(body)
	}
	message := strings.TrimSpace(rule.Message)
	if message == "" {
		message = http.StatusText(rule.StatusCode)
	}
	if message == "" {
		message = "upstream error masked by gateway"
	}
	payload, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "gateway_error",
		},
	})
	if err != nil {
		return []byte(`{"error":{"message":"upstream error masked by gateway","type":"gateway_error"}}`)
	}
	return payload
}

// writeMaskedResponse replaces the error visible to the client with the
// uniform masked response. It must be called before anything of the upstream
// response has been written downstream, and it is interim-aware.
func writeMaskedResponse(w http.ResponseWriter, interim *interimResponseSender, mask maskedResponse) {
	commitFinalResponse(interim, func() {
		h := w.Header()
		h.Set("Content-Type", mask.contentType)
		if mask.retryAfter != "" {
			h.Set("Retry-After", mask.retryAfter)
		}
		w.WriteHeader(mask.statusCode)
		_, _ = w.Write(mask.body)
	})
}

// drainMaskedUpstreamBody reads only the remainder, not the replayed preview.
// Response.Body must support Close concurrently with Read (net/http's contract).
// This timer exists only for incomplete, discarded error bodies, never for each
// stream chunk. A timeout here is cleanup, not another upstream failure sample.
func drainMaskedUpstreamBody(body io.ReadCloser) {
	if body == nil || body == http.NoBody {
		return
	}
	timer := time.AfterFunc(maskedBodyDrainTimeout, func() { _ = body.Close() })
	defer timer.Stop()
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxMaskedBodyDrainBytes))
}

// writeMaskableGatewayError writes a gateway-synthesized error response,
// replacing it with the masked uniform response when a masking rule matches
// the status code and/or the error message.
func writeMaskableGatewayError(w http.ResponseWriter, interim *interimResponseSender, vg *vendorGateway, message string, statusCode int) {
	if rule, ok := matchGatewayErrorMask(vg.errorPolicy, statusCode, message); ok {
		writeMaskedResponse(w, interim, buildMaskedResponse(rule, ""))
		return
	}
	writeHTTPError(w, interim, message, statusCode)
}
