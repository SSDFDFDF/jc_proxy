package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

	body, reasonBody := summarizeResponsePreview(headers, preview)
	retryAfter := parseRetryAfter(headers.Get("Retry-After"))
	reason := compactReason(fmt.Sprintf("HTTP %d", statusCode), reasonBody)
	responseFailover := shouldFailoverResponse(statusCode, policy)

	if shouldAutoDisableInvalidKey(provider, policy.AutoDisable, statusCode, body) {
		return disableDecision(statusCode, compactReason("auto disabled: invalid key", reasonBody), responseFailover)
	}

	switch statusCode {
	case http.StatusPaymentRequired:
		if boolOrDefault(policy.AutoDisable.PaymentRequired, true) {
			return disableDecision(statusCode, compactReason("auto disabled: billing or quota exhausted", reasonBody), responseFailover)
		}
	case http.StatusTooManyRequests:
		if isQuotaExhausted(provider, body) && boolOrDefault(policy.AutoDisable.QuotaExhausted, true) {
			return disableDecision(statusCode, compactReason("auto disabled: quota exhausted", reasonBody), responseFailover)
		}
	}

	if rule, ok := matchResponseCooldownRule(statusCode, body, policy.Cooldown.ResponseRules); ok {
		return responseCooldownDecision(statusCode, reason, rule, responseFailover, retryAfter)
	}

	switch statusCode {
	case http.StatusUnauthorized:
		return cooldownDecision(statusCode, reason, policy.Cooldown.Unauthorized, responseFailover, 0, false)
	case http.StatusPaymentRequired:
		return cooldownDecision(statusCode, reason, policy.Cooldown.PaymentRequired, responseFailover, 0, false)
	case http.StatusForbidden:
		return cooldownDecision(statusCode, reason, policy.Cooldown.Forbidden, responseFailover, 0, false)
	case http.StatusTooManyRequests:
		return cooldownDecision(statusCode, reason, policy.Cooldown.RateLimit, responseFailover, retryAfter, false)
	case 529, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		if provider == "openai" && strings.Contains(body, "slow down") {
			return cooldownDecision(statusCode, reason, policy.Cooldown.OpenAISlowDown, responseFailover, retryAfter, true)
		}
		return cooldownDecision(statusCode, reason, policy.Cooldown.ServerError, responseFailover, retryAfter, false)
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		if responseFailover {
			return keyDecision{action: keyActionObserve, statusCode: statusCode, reason: reason, failover: true}
		}
		return keyDecision{action: keyActionObserve, statusCode: statusCode, reason: reason}
	default:
		if responseFailover {
			return keyDecision{action: keyActionObserve, statusCode: statusCode, reason: reason, failover: true}
		}
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

func responseCooldownDecision(statusCode int, reason string, rule config.ErrorResponseCooldownRule, failover bool, retryAfter time.Duration) keyDecision {
	duration := rule.Duration
	switch retryAfterMode(rule.RetryAfter, statusCode) {
	case "override":
		if retryAfter > 0 {
			duration = retryAfter
		}
	case "max":
		if retryAfter > 0 {
			duration = maxDuration(retryAfter, duration)
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

func shouldAutoDisableInvalidKey(provider string, auto config.ErrorAutoDisableConfig, statusCode int, body string) bool {
	if !boolOrDefault(auto.InvalidKey, true) {
		return false
	}
	if len(auto.InvalidKeyStatusCodes) > 0 || hasNonEmptyPattern(auto.InvalidKeyKeywords) {
		return containsStatusCode(auto.InvalidKeyStatusCodes, statusCode) || containsAnyKeyword(body, auto.InvalidKeyKeywords)
	}
	return statusCode == http.StatusUnauthorized && isInvalidKey(provider, body)
}

func shouldFailoverResponse(statusCode int, policy config.ErrorPolicyConfig) bool {
	if len(policy.Failover.ResponseStatusCodes) > 0 {
		return containsStatusCode(policy.Failover.ResponseStatusCodes, statusCode)
	}
	switch statusCode {
	case http.StatusUnauthorized:
		return boolOrDefault(policy.Failover.Unauthorized, true)
	case http.StatusPaymentRequired:
		return boolOrDefault(policy.Failover.PaymentRequired, true)
	case http.StatusForbidden:
		return boolOrDefault(policy.Failover.Forbidden, true)
	case http.StatusTooManyRequests:
		return boolOrDefault(policy.Failover.RateLimit, true)
	case 529, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return boolOrDefault(policy.Failover.ServerError, true)
	default:
		return false
	}
}

func matchResponseCooldownRule(statusCode int, body string, rules []config.ErrorResponseCooldownRule) (config.ErrorResponseCooldownRule, bool) {
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
	return config.ErrorResponseCooldownRule{}, false
}

func retryAfterMode(raw string, statusCode int) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode != "" {
		return mode
	}
	if statusCode == http.StatusTooManyRequests {
		return "override"
	}
	return "ignore"
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

func summarizeResponsePreview(headers http.Header, preview []byte) (string, string) {
	preview = bytes.TrimSpace(preview)
	if len(preview) == 0 {
		return "", ""
	}

	if matchText, reasonText, ok := summarizeJSONPreview(preview); ok {
		return strings.ToLower(matchText), reasonText
	}

	if isLikelyTextPreview(headers, preview) {
		text := strings.TrimSpace(string(preview))
		return strings.ToLower(text), text
	}

	return "", describeNonTextPreview(headers, len(preview))
}

func summarizeJSONPreview(preview []byte) (string, string, bool) {
	if !looksLikeJSON(preview) {
		return "", "", false
	}

	var payload any
	if err := json.Unmarshal(preview, &payload); err != nil {
		return "", "", false
	}

	parts := collectJSONPreviewStrings(payload)
	if len(parts) > 0 {
		reasonText := strings.Join(parts, " | ")
		return strings.Join(parts, " "), reasonText, true
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, preview); err != nil {
		return "", "", false
	}
	text := compact.String()
	return text, text, true
}

func collectJSONPreviewStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		return []string{text}
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, collectJSONPreviewStrings(item)...)
		}
		return dedupeStrings(out)
	case map[string]any:
		var out []string
		for _, key := range []string{"message", "error_description", "description", "detail", "error", "type", "code", "param"} {
			if child, ok := typed[key]; ok {
				out = append(out, collectJSONPreviewStrings(child)...)
			}
		}
		return dedupeStrings(out)
	default:
		return nil
	}
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func looksLikeJSON(preview []byte) bool {
	if len(preview) == 0 {
		return false
	}
	switch preview[0] {
	case '{', '[':
		return true
	default:
		return false
	}
}

func isLikelyTextPreview(headers http.Header, preview []byte) bool {
	if len(preview) == 0 || !utf8.Valid(preview) {
		return false
	}

	badRunes := 0
	totalRunes := 0
	for _, r := range string(preview) {
		totalRunes++
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			continue
		}
		badRunes++
	}

	if totalRunes == 0 {
		return false
	}
	if badRunes == 0 {
		return true
	}

	contentType := normalizedContentType(headers)
	if contentType == "" {
		return badRunes*10 <= totalRunes
	}
	if isTextualContentType(contentType) {
		return badRunes*5 <= totalRunes
	}
	return badRunes*10 <= totalRunes
}

func describeNonTextPreview(headers http.Header, length int) string {
	contentType := normalizedContentType(headers)
	if contentType == "" {
		return fmt.Sprintf("<non-text response body: %d bytes>", length)
	}
	return fmt.Sprintf("<non-text response body: %d bytes; content-type=%s>", length, contentType)
}

func normalizedContentType(headers http.Header) string {
	if headers == nil {
		return ""
	}
	contentType := strings.TrimSpace(headers.Get("Content-Type"))
	if contentType == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.ToLower(contentType)
	}
	return strings.ToLower(mediaType)
}

func isTextualContentType(contentType string) bool {
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	switch contentType {
	case "application/json", "application/problem+json", "application/ld+json", "application/xml", "application/javascript", "application/x-www-form-urlencoded":
		return true
	default:
		return strings.HasSuffix(contentType, "+json") || strings.HasSuffix(contentType, "+xml")
	}
}

func containsAny(body string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(body, pattern) {
			return true
		}
	}
	return false
}

func containsAnyKeyword(body string, patterns []string) bool {
	if body == "" {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if strings.Contains(body, pattern) {
			return true
		}
	}
	return false
}

func containsStatusCode(statusCodes []int, statusCode int) bool {
	for _, candidate := range statusCodes {
		if candidate == statusCode {
			return true
		}
	}
	return false
}

func hasNonEmptyPattern(patterns []string) bool {
	for _, pattern := range patterns {
		if strings.TrimSpace(pattern) != "" {
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
