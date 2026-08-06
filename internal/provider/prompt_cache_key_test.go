package provider_test

import (
	"testing"

	"reasonix/internal/provider"
)

func TestNormalizePromptCacheKeyMode(t *testing.T) {
	cases := map[string]string{
		"": "auto", "AUTO": "auto", "on": "on", "off": "off", "custom": "custom", "nope": "auto",
	}
	for in, want := range cases {
		if got := provider.NormalizePromptCacheKeyMode(in); got != want {
			t.Fatalf("NormalizePromptCacheKeyMode(%q)=%q want %q", in, got, want)
		}
	}
}

func TestFormatSessionPromptCacheKey(t *testing.T) {
	if got := provider.FormatSessionPromptCacheKey("abc123", ""); got != "reasonix:session:abc123" {
		t.Fatalf("got %q", got)
	}
	if got := provider.FormatSessionPromptCacheKey("abc123", "sa_deadbeef"); got != "reasonix:session:abc123:sub:sa_deadbeef" {
		t.Fatalf("got %q", got)
	}
}

func TestResolvePromptCacheKeyDeepSeekOmits(t *testing.T) {
	for _, kind := range []string{"openai", "responses", "anthropic"} {
		got := provider.ResolvePromptCacheKey("auto", "", kind, "https://api.deepseek.com", "sess1", "")
		if got != "" {
			t.Fatalf("kind=%s DeepSeek should omit key, got %q", kind, got)
		}
	}
}

func TestResolvePromptCacheKeyOpenAIAutoSends(t *testing.T) {
	got := provider.ResolvePromptCacheKey("auto", "", "openai", "https://api.openai.com/v1", "sess1", "")
	if got != "reasonix:session:sess1" {
		t.Fatalf("got %q", got)
	}
}

func TestResolvePromptCacheKeyResponsesDeepSeekOmits(t *testing.T) {
	got := provider.ResolvePromptCacheKey("on", "", "responses", "https://api.deepseek.com", "sess1", "")
	if got != "" {
		t.Fatalf("DeepSeek responses must omit even on, got %q", got)
	}
}

func TestResolvePromptCacheKeyResponsesNonDeepSeekSends(t *testing.T) {
	got := provider.ResolvePromptCacheKey("auto", "", "responses", "https://api.openai.com/v1", "sess1", "")
	if got != "reasonix:session:sess1" {
		t.Fatalf("got %q", got)
	}
}

func TestResolvePromptCacheKeyOff(t *testing.T) {
	got := provider.ResolvePromptCacheKey("off", "", "openai", "https://api.openai.com/v1", "sess1", "")
	if got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestResolvePromptCacheKeyCustom(t *testing.T) {
	got := provider.ResolvePromptCacheKey("custom", "my-key", "openai", "https://gateway.example/v1", "sess1", "")
	if got != "my-key" {
		t.Fatalf("got %q", got)
	}
	if got := provider.ResolvePromptCacheKey("custom", "", "openai", "https://gateway.example/v1", "sess1", ""); got != "" {
		t.Fatalf("empty custom value must omit, got %q", got)
	}
}

func TestResolvePromptCacheKeyAnthropicNeverSendsStickyKey(t *testing.T) {
	got := provider.ResolvePromptCacheKey("on", "x", "anthropic", "https://api.anthropic.com", "sess1", "")
	if got != "" {
		t.Fatalf("anthropic must never get sticky key from resolver, got %q", got)
	}
}

func TestPromptCacheKeyRejected(t *testing.T) {
	err := &provider.APIError{Status: 400, Body: `Unknown field: prompt_cache_key`}
	if !provider.PromptCacheKeyRejected(err) {
		t.Fatal("expected reject")
	}
	if provider.PromptCacheKeyRejected(&provider.APIError{Status: 400, Body: "context length exceeded"}) {
		t.Fatal("unrelated 400 must not disable")
	}
	if provider.PromptCacheKeyRejected(&provider.APIError{Status: 500, Body: "prompt_cache_key"}) {
		t.Fatal("non-400 must not disable")
	}
}

func TestFailOpenDisablesFingerprint(t *testing.T) {
	provider.ClearPromptCacheKeyDisablesForTest()
	t.Cleanup(provider.ClearPromptCacheKeyDisablesForTest)
	fp := provider.ProviderFingerprint("openai", "https://gateway.example/v1")
	provider.DisablePromptCacheKey(fp)
	got := provider.ResolvePromptCacheKey("auto", "", "openai", "https://gateway.example/v1", "sess", "")
	if got != "" {
		t.Fatalf("disabled fingerprint still resolved key %q", got)
	}
}

func TestProviderFingerprintKindAliasDashscopeResponses(t *testing.T) {
	// Adapter fail-open uses hard-coded "responses"; agent Resolve uses entry.Kind
	// "dashscope-responses". Both must share one fingerprint so disable sticks.
	base := "https://dashscope.aliyuncs.com/compatible-mode/v1"
	fpResponses := provider.ProviderFingerprint("responses", base)
	fpDashscope := provider.ProviderFingerprint("dashscope-responses", base)
	if fpResponses == "" || fpDashscope == "" {
		t.Fatalf("empty fingerprint: responses=%q dashscope=%q", fpResponses, fpDashscope)
	}
	if fpResponses != fpDashscope {
		t.Fatalf("kind alias mismatch: responses=%q dashscope-responses=%q", fpResponses, fpDashscope)
	}
}

func TestProviderFingerprintBaseURLTrailingSlash(t *testing.T) {
	// Clients store TrimRight(baseURL, "/"); agent fingerprints raw entry.BaseURL.
	// Trailing slash must not change the fingerprint.
	with := "https://gateway.example/v1/"
	without := "https://gateway.example/v1"
	fpWith := provider.ProviderFingerprint("openai", with)
	fpWithout := provider.ProviderFingerprint("openai", without)
	if fpWith != fpWithout {
		t.Fatalf("trailing slash mismatch: with=%q without=%q", fpWith, fpWithout)
	}
	// Host case should also be stable after URL parse + lower host.
	mixed := "https://Gateway.Example/v1"
	if got := provider.ProviderFingerprint("openai", mixed); got != fpWithout {
		t.Fatalf("host case mismatch: mixed=%q want %q", got, fpWithout)
	}
}

func TestDisableViaResponsesIsDisabledForDashscopeResponses(t *testing.T) {
	provider.ClearPromptCacheKeyDisablesForTest()
	t.Cleanup(provider.ClearPromptCacheKeyDisablesForTest)

	base := "https://dashscope.aliyuncs.com/compatible-mode/v1"
	// Adapter path: Disable with hard-coded "responses" + client-normalized baseURL (no slash).
	provider.DisablePromptCacheKey(provider.ProviderFingerprint("responses", base))
	// Agent path: IsPromptCacheKeyDisabled / Resolve with entry.Kind and possibly trailing slash.
	fpAgent := provider.ProviderFingerprint("dashscope-responses", base+"/")
	if !provider.IsPromptCacheKeyDisabled(fpAgent) {
		t.Fatal("IsPromptCacheKeyDisabled must be true for dashscope-responses after disable via responses")
	}
	got := provider.ResolvePromptCacheKey("auto", "", "dashscope-responses", base+"/", "sess", "")
	if got != "" {
		t.Fatalf("Resolve with dashscope-responses must omit after fail-open, got %q", got)
	}
}

func TestDisableViaOneBaseURLFormDisablesOther(t *testing.T) {
	provider.ClearPromptCacheKeyDisablesForTest()
	t.Cleanup(provider.ClearPromptCacheKeyDisablesForTest)

	provider.DisablePromptCacheKey(provider.ProviderFingerprint("openai", "https://gateway.example/v1/"))
	if !provider.IsPromptCacheKeyDisabled(provider.ProviderFingerprint("openai", "https://gateway.example/v1")) {
		t.Fatal("disable with trailing slash must stick for baseURL without slash")
	}
	if !provider.IsPromptCacheKeyDisabled(provider.ProviderFingerprint("openai", "https://Gateway.Example/v1")) {
		t.Fatal("disable must stick across host case differences")
	}
}
