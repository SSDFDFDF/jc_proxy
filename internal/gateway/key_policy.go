package gateway

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"jc_proxy/internal/config"
)

type keyAction string

const (
	keyActionNone     keyAction = "none"
	keyActionSuccess  keyAction = "success"
	keyActionObserve  keyAction = "observe"
	keyActionCooldown keyAction = "cooldown"
	keyActionDisable  keyAction = "disable"
)

type keyDecision struct {
	action     keyAction
	statusCode int
	cooldown   time.Duration
	reason     string
	failover   bool
}

type responsePreview struct {
	limit int
	buf   bytes.Buffer
}

func newResponsePreview(limit int) *responsePreview {
	return &responsePreview{limit: limit}
}

func (p *responsePreview) Write(b []byte) (int, error) {
	if p.limit <= 0 || p.buf.Len() >= p.limit {
		return len(b), nil
	}
	remain := p.limit - p.buf.Len()
	if remain > len(b) {
		remain = len(b)
	}
	_, _ = p.buf.Write(b[:remain])
	return len(b), nil
}

func (p *responsePreview) Bytes() []byte {
	return append([]byte(nil), p.buf.Bytes()...)
}

func classifyRequestError(provider string, policy config.ErrorPolicyConfig, message string) keyDecision {
	reason := compactReason("request error", message)
	return cooldownDecision(0, reason, policy.Cooldown.RequestError, boolOrDefault(policy.Failover.RequestError, true), 0, false)
}

func classifyResponse(provider string, policy config.ErrorPolicyConfig, statusCode int, headers http.Header, preview []byte) keyDecision {
	if statusCode < http.StatusBadRequest {
		return keyDecision{action: keyActionSuccess, statusCode: statusCode}
	}

	body := strings.ToLower(string(preview))
	retryAfter := parseRetryAfter(headers.Get("Retry-After"))
	reason := compactReason(fmt.Sprintf("HTTP %d", statusCode), string(preview))

	switch statusCode {
	case http.StatusUnauthorized:
		if isInvalidKey(provider, body) {
			if boolOrDefault(policy.AutoDisable.InvalidKey, true) {
				return disableDecision(statusCode, compactReason("auto disabled: invalid key", string(preview)), boolOrDefault(policy.Failover.Unauthorized, true))
			}
		}
		return cooldownDecision(statusCode, reason, policy.Cooldown.Unauthorized, boolOrDefault(policy.Failover.Unauthorized, true), 0, false)
	case http.StatusPaymentRequired:
		if boolOrDefault(policy.AutoDisable.PaymentRequired, true) {
			return disableDecision(statusCode, compactReason("auto disabled: billing or quota exhausted", string(preview)), boolOrDefault(policy.Failover.PaymentRequired, true))
		}
		return cooldownDecision(statusCode, reason, policy.Cooldown.PaymentRequired, boolOrDefault(policy.Failover.PaymentRequired, true), 0, false)
	case http.StatusForbidden:
		return cooldownDecision(statusCode, reason, policy.Cooldown.Forbidden, boolOrDefault(policy.Failover.Forbidden, true), 0, false)
	case http.StatusTooManyRequests:
		if isQuotaExhausted(provider, body) {
			if boolOrDefault(policy.AutoDisable.QuotaExhausted, true) {
				return disableDecision(statusCode, compactReason("auto disabled: quota exhausted", string(preview)), boolOrDefault(policy.Failover.RateLimit, true))
			}
		}
		return cooldownDecision(statusCode, reason, policy.Cooldown.RateLimit, boolOrDefault(policy.Failover.RateLimit, true), retryAfter, false)
	case 529, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		if provider == "openai" && strings.Contains(body, "slow down") {
			return cooldownDecision(statusCode, reason, policy.Cooldown.OpenAISlowDown, boolOrDefault(policy.Failover.ServerError, true), retryAfter, true)
		}
		return cooldownDecision(statusCode, reason, policy.Cooldown.ServerError, boolOrDefault(policy.Failover.ServerError, true), retryAfter, false)
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		return keyDecision{action: keyActionObserve, statusCode: statusCode, reason: reason}
	default:
		return keyDecision{action: keyActionObserve, statusCode: statusCode, reason: reason}
	}
}

func disableDecision(statusCode int, reason string, failover bool) keyDecision {
	return keyDecision{
		action:     keyActionDisable,
		statusCode: statusCode,
		reason:     reason,
		failover:   failover,
	}
}

func cooldownDecision(statusCode int, reason string, rule config.ErrorCooldownRule, failover bool, retryAfter time.Duration, useMaxRetryAfter bool) keyDecision {
	if !boolOrDefault(rule.Enabled, true) {
		return keyDecision{
			action:     keyActionObserve,
			statusCode: statusCode,
			reason:     reason,
		}
	}

	duration := rule.Duration
	if retryAfter > 0 {
		if useMaxRetryAfter {
			duration = maxDuration(retryAfter, duration)
		} else {
			duration = retryAfter
		}
	}

	return keyDecision{
		action:     keyActionCooldown,
		statusCode: statusCode,
		cooldown:   duration,
		reason:     reason,
		failover:   failover,
	}
}

func boolOrDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func isInvalidKey(provider, body string) bool {
	if body == "" {
		return false
	}
	common := []string{
		"incorrect_api_key",
		"invalid_api_key",
		"invalid api key",
		"api key not valid",
		"authentication_error",
		"invalid authentication",
		"invalid x-api-key",
		"invalid subscription key",
		"key has been disabled",
		"leaked",
		"revoked",
	}
	switch provider {
	case "anthropic":
		return containsAny(body, append(common, "authentication error", "invalid x-api-key")...)
	case "gemini":
		return containsAny(body, append(common, "api_key_invalid", "expired api key")...)
	case "azure_openai":
		return containsAny(body, append(common, "access denied due to invalid subscription key")...)
	default:
		return containsAny(body, common...)
	}
}

func isQuotaExhausted(provider, body string) bool {
	if body == "" {
		return false
	}
	common := []string{
		"insufficient_quota",
		"billing_error",
		"insufficient balance",
		"quota exhausted",
		"quota has been exhausted",
		"credit balance is too low",
		"余额不足",
	}
	switch provider {
	case "deepseek":
		return containsAny(body, append(common, "insufficient balance", "balance is not enough")...)
	default:
		return containsAny(body, common...)
	}
}

func cooldownForRateLimit(level int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	switch level {
	case 0, 1:
		return 5 * time.Second
	case 2:
		return 15 * time.Second
	case 3:
		return 1 * time.Minute
	default:
		return 5 * time.Minute
	}
}

func cooldownForTransient(level int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	switch level {
	case 0, 1:
		return 2 * time.Second
	case 2:
		return 5 * time.Second
	case 3:
		return 15 * time.Second
	default:
		return 1 * time.Minute
	}
}

func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

func compactReason(prefix, body string) string {
	prefix = strings.TrimSpace(prefix)
	body = strings.TrimSpace(body)
	body = strings.ReplaceAll(body, "\n", " ")
	body = strings.ReplaceAll(body, "\r", " ")
	body = strings.Join(strings.Fields(body), " ")
	if body == "" {
		return prefix
	}
	if len(body) > 160 {
		body = body[:160]
	}
	if prefix == "" {
		return body
	}
	return prefix + ": " + body
}

func containsAny(body string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(body, pattern) {
			return true
		}
	}
	return false
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
