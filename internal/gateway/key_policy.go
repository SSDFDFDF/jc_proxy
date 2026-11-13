package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"jc_proxy/internal/balancer"
	"jc_proxy/internal/config"
)

type keyAction = balancer.AttemptAction

const (
	keyActionInterrupted = balancer.AttemptInterrupted
	keyActionSuccess     = balancer.AttemptSuccess
	keyActionObserve     = balancer.AttemptObserve
	keyActionCooldown    = balancer.AttemptCooldown
	keyActionDisable     = balancer.AttemptDisable
)

type keyDecision struct {
	action     keyAction
	statusCode int
	cooldown   time.Duration
	reason     string
	failover   bool
}

// Only health settings advance the pool's policy generation. Key refreshes,
// retry edits and presentation-only masking edits keep in-flight health valid.
func keyHealthPolicyRevision(policy config.ErrorPolicyConfig) string {
	type maskingHealthRule struct {
		StatusCodes []int
		Keywords    []string
		Cooldown    time.Duration
	}
	maskingHealthEnabled := false
	if errorMaskingEnabled(policy.Masking) {
		for _, rule := range policy.Masking.Rules {
			maskingHealthEnabled = maskingHealthEnabled || rule.Cooldown > 0
		}
	}
	rules := make([]maskingHealthRule, 0)
	if maskingHealthEnabled {
		for _, rule := range policy.Masking.Rules {
			// Keep zero-cooldown rules too: an earlier presentation rule can
			// shadow a later rule that would extend cooldown.
			rules = append(rules, maskingHealthRule{rule.StatusCodes, rule.Keywords, rule.Cooldown})
		}
	}
	revision, _ := json.Marshal(struct {
		AutoDisable    config.ErrorAutoDisableConfig
		Cooldown       config.ErrorCooldownConfig
		MaskingEnabled bool
		MaskingRules   []maskingHealthRule
	}{policy.AutoDisable, policy.Cooldown, maskingHealthEnabled, rules})
	return string(revision)
}

// analyzeErrorResponse shares one parsed preview between health classification
// and masking. Both continue to use the real upstream status and body.
func analyzeErrorResponse(provider string, policy config.ErrorPolicyConfig, statusCode int, headers http.Header, preview []byte, responseMasking ...config.ErrorMaskingConfig) (keyDecision, config.ErrorMaskingRule, bool) {
	body, reason := summarizeResponsePreview(headers, preview)
	decision := classifyErrorResponse(provider, policy, statusCode, headers, body, reason)
	rule, matched := matchUpstreamErrorMask(policy, statusCode, body)
	if matched {
		decision = extendDecisionCooldown(decision, rule)
	}
	if len(responseMasking) > 0 {
		rule, matched = matchUpstreamErrorMask(config.ErrorPolicyConfig{Masking: responseMasking[0]}, statusCode, body)
	}
	return decision, rule, matched
}

func classifyRequestError(_ string, policy config.ErrorPolicyConfig, message string) keyDecision {
	decision := keyDecision{action: keyActionObserve, reason: compactReason("request error", message), failover: boolOrDefault(policy.Failover.RequestError, false)}
	rule := policy.Cooldown.RequestError
	if boolOrDefault(rule.Enabled, rule.Duration > 0) && rule.Duration > 0 {
		decision.action = keyActionCooldown
		decision.cooldown = rule.Duration
	}
	return decision
}

func classifyResponse(provider string, policy config.ErrorPolicyConfig, statusCode int, headers http.Header, preview []byte) keyDecision {
	if statusCode < http.StatusBadRequest {
		return keyDecision{action: keyActionSuccess, statusCode: statusCode}
	}

	body, reasonBody := summarizeResponsePreview(headers, preview)
	return classifyErrorResponse(provider, policy, statusCode, headers, body, reasonBody)
}

func classifyErrorResponse(_ string, policy config.ErrorPolicyConfig, statusCode int, headers http.Header, body, reasonBody string) keyDecision {
	reason := compactReason(fmt.Sprintf("HTTP %d", statusCode), reasonBody)
	failover := shouldFailoverResponse(statusCode, policy)
	if disable, match := classifyAutoDisable(policy.AutoDisable, statusCode, body); disable {
		return disableDecision(statusCode, compactReason("auto-disable rule matched: "+match, reasonBody), failover)
	}
	if rule, ok := matchResponseCooldownRule(statusCode, body, policy.Cooldown.ResponseRules); ok {
		return responseCooldownDecision(statusCode, reason, rule, failover, parseRetryAfter(headers.Get("Retry-After")))
	}
	return keyDecision{action: keyActionObserve, statusCode: statusCode, reason: reason, failover: failover}
}

func disableDecision(statusCode int, reason string, failover bool) keyDecision {
	return keyDecision{
		action:     keyActionDisable,
		statusCode: statusCode,
		reason:     reason,
		failover:   failover,
	}
}

func responseCooldownDecision(statusCode int, reason string, rule config.ErrorResponseCooldownRule, failover bool, retryAfter time.Duration) keyDecision {
	duration := rule.Duration
	switch retryAfterMode(rule.RetryAfter) {
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

func autoDisableEnabled(auto config.ErrorAutoDisableConfig) bool {
	return boolOrDefault(auto.Enabled, true) && (len(auto.StatusCodes) > 0 || hasNonEmptyPattern(auto.Keywords))
}

func classifyAutoDisable(auto config.ErrorAutoDisableConfig, statusCode int, body string) (bool, string) {
	if !autoDisableEnabled(auto) {
		return false, ""
	}
	if containsStatusCode(auto.StatusCodes, statusCode) {
		return true, fmt.Sprintf("HTTP %d", statusCode)
	}
	if keyword, ok := matchKeyword(body, auto.Keywords); ok {
		return true, "keyword " + keyword
	}
	return false, ""
}

func shouldFailoverResponse(statusCode int, policy config.ErrorPolicyConfig) bool {
	return statusCode >= http.StatusBadRequest && containsStatusCode(policy.Failover.ResponseStatusCodes, statusCode)
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

func retryAfterMode(raw string) string {
	if mode := strings.ToLower(strings.TrimSpace(raw)); mode != "" {
		return mode
	}
	return "ignore"
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
		return string(preview) + " " + strings.Join(parts, " "), reasonText, true
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
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
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

func matchKeyword(body string, patterns []string) (string, bool) {
	if body == "" {
		return "", false
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if strings.Contains(body, pattern) {
			return pattern, true
		}
	}
	return "", false
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
