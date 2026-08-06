package provider

import (
	"errors"
	"net/url"
	"strings"
	"sync"
)

// promptCacheKeyDisables is a process-local fail-open map: once a (kind, baseURL)
// fingerprint is recorded, ResolvePromptCacheKey omits the sticky key for the
// rest of the process lifetime (or until ClearPromptCacheKeyDisablesForTest).
var promptCacheKeyDisables sync.Map // fingerprint string → struct{}

// NormalizePromptCacheKeyMode maps user/config mode strings to the canonical
// set: auto|on|off|custom. Empty and unknown values become "auto".
func NormalizePromptCacheKeyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on":
		return "on"
	case "off":
		return "off"
	case "custom":
		return "custom"
	default:
		return "auto"
	}
}

// FormatSessionPromptCacheKey builds the session-namespaced sticky key.
// Empty sessionID yields "". Non-empty subID appends ":sub:<subID>".
func FormatSessionPromptCacheKey(sessionID, subID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	key := "reasonix:session:" + sessionID
	if sub := strings.TrimSpace(subID); sub != "" {
		key += ":sub:" + sub
	}
	return key
}

// IsDeepSeekShaped uses host rules aligned with openai.IsDeepSeek /
// responses.DetectVendor. Host matching lives here (not via openai import)
// to avoid provider ↔ openai import cycles.
func IsDeepSeekShaped(kind, baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "api.deepseek.com" || strings.HasSuffix(host, ".deepseek.com") {
		return true
	}
	_ = kind
	return false
}

// ProviderFingerprint returns a stable identity for a (kind, baseURL) pair used
// by the fail-open disable map. Kind aliases that share a wire adapter (e.g.
// dashscope-responses → responses) and baseURL forms that differ only by
// trailing slash or host case collapse to the same fingerprint so adapter
// Disable and agent Resolve always agree.
func ProviderFingerprint(kind, baseURL string) string {
	kind = normalizeFingerprintKind(kind)
	baseURL = normalizeFingerprintBaseURL(baseURL)
	if kind == "" && baseURL == "" {
		return ""
	}
	return kind + "|" + baseURL
}

// normalizeFingerprintKind lowercases kind and maps registered adapter aliases
// onto their canonical fingerprint kind.
func normalizeFingerprintKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "dashscope-responses":
		// Same wire adapter as "responses"; fail-open must stick across both.
		return "responses"
	default:
		return kind
	}
}

// normalizeFingerprintBaseURL trims whitespace/trailing slash and lowercases
// scheme+host when the URL parses, so client-stored (TrimRight) and raw
// entry.BaseURL forms match.
func normalizeFingerprintBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return strings.TrimRight(baseURL, "/")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

// promptCacheKeyRejectSubstrings are lowercased body markers that indicate the
// gateway rejected the prompt_cache_key field (unknown/extra parameter).
var promptCacheKeyRejectSubstrings = []string{
	"prompt_cache_key",
	"unknown field",
	"unknown parameter",
	"unrecognized",
	"extra inputs are not permitted",
	"additional properties",
}

// PromptCacheKeyRejected reports whether err is an API rejection of the sticky
// prompt_cache_key field. True only for *APIError with status 400 or 422 whose
// body matches a clear reject heuristic substring. Callers must also verify the
// request actually included a non-empty key before disabling.
func PromptCacheKeyRejected(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		return false
	}
	if apiErr.Status != 400 && apiErr.Status != 422 {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	for _, sub := range promptCacheKeyRejectSubstrings {
		if strings.Contains(body, sub) {
			return true
		}
	}
	return false
}

// DisablePromptCacheKey records a process-local fail-open disable for fingerprint.
// Empty fingerprints are ignored.
func DisablePromptCacheKey(fingerprint string) {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return
	}
	promptCacheKeyDisables.Store(fingerprint, struct{}{})
}

// IsPromptCacheKeyDisabled reports whether sticky keys are fail-open disabled
// for the given fingerprint.
func IsPromptCacheKeyDisabled(fingerprint string) bool {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return false
	}
	_, ok := promptCacheKeyDisables.Load(fingerprint)
	return ok
}

// ClearPromptCacheKeyDisablesForTest clears the process-local disable map.
// Intended for tests only.
func ClearPromptCacheKeyDisablesForTest() {
	promptCacheKeyDisables.Range(func(key, _ any) bool {
		promptCacheKeyDisables.Delete(key)
		return true
	})
}

// ResolvePromptCacheKey decides the wire-ready sticky prompt cache key (or
// empty to omit). Anthropic always omits; DeepSeek-shaped hosts always omit;
// modes: off → omit, custom → customValue (trimmed; empty omits), on/auto →
// session-formatted key for openai/responses/dashscope-responses kinds.
func ResolvePromptCacheKey(mode, customValue, kind, baseURL, sessionID, subID string) string {
	mode = NormalizePromptCacheKeyMode(mode)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "anthropic" {
		return ""
	}
	if IsDeepSeekShaped(kind, baseURL) {
		return ""
	}
	if mode == "off" {
		return ""
	}
	if IsPromptCacheKeyDisabled(ProviderFingerprint(kind, baseURL)) {
		return ""
	}
	switch mode {
	case "custom":
		return strings.TrimSpace(customValue)
	case "on", "auto":
		if kind == "openai" || kind == "responses" || kind == "dashscope-responses" || kind == "" {
			return FormatSessionPromptCacheKey(sessionID, subID)
		}
		return ""
	default:
		return ""
	}
}
