# Prompt Cache Hit Optimization — Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship session-stable `prompt_cache_key` on OpenAI-compatible and non-DeepSeek Responses requests so multi-replica gateways can sticky-route, without regressing DeepSeek automatic prefix cache or Anthropic breakpoints.

**Architecture:** Agent resolves a namespaced sticky key from config + `BranchID(sessionPath)` (+ optional subagent `Ref`) into `provider.Request.PromptCacheKey`. OpenAI and Responses adapters emit snake_case `prompt_cache_key` only when non-empty and not DeepSeek-shaped. Process-local fail-open disables the key after a clear 400 reject. Anthropic is untouched (existing `cache_control` only).

**Tech Stack:** Go, existing `internal/provider/{openai,responses,anthropic}`, `internal/agent`, `internal/config`, `internal/boot`, `internal/control`, `go test`.

**Spec:** `docs/superpowers/specs/2026-08-06-prompt-cache-hit-optimization-design.md` (Phase 1 only).  
**Research:** `docs/research/prompt-cache-wire-compatibility.md`, `docs/research/agent-prompt-caching-survey.md`.

## Global Constraints

- Field name on the wire: **`prompt_cache_key`** (snake_case only). Never default-send `promptCacheKey`.
- DeepSeek-shaped endpoints (**openai**, **responses**, **anthropic** kinds): **hard-omit** sticky key (and keep Anthropic breakpoints off for DeepSeek).
- Detection reuses existing host helpers: `openai.IsDeepSeek`, `responses.DetectVendor == "deepseek"`, anthropic `deepseek` flag — not model-id guessing.
- Session id: `agent.BranchID(sessionPath)` (filepath stem). Key format: `corvus:session:<BranchID>` or `corvus:session:<BranchID>:sub:<SubagentMeta.Ref>`.
- Config: `[agent].prompt_cache_key = auto|on|off|custom` + optional `[agent].prompt_cache_key_value` (no `custom:` prefix grammar).
- Fail-open is **in Phase 1** (process-local fingerprint map). Prefer next turn omits key; do **not** invent double-request auto-retry that re-bills.
- ExtraBody rules apply to **openai** only; responses has no ExtraBody channel — set field from `Request` only.
- Under `auto`, non-DeepSeek openai/responses send key (including unknown base URLs); fail-open handles strict gateways. No separate “known good hosts” allowlist required.
- Phase 2 (runtime-update tail) and Phase 3 (deferred MCP) are **out of this plan** — separate plans after Phase 1 is green.
- YAGNI: no `prompt_cache_retention`, no server compact, no Bedrock/Copilot field variants.
- TDD: write failing test → run → implement → run → commit per task.

## File map

| File | Responsibility |
|------|----------------|
| `internal/provider/provider.go` | `Request.PromptCacheKey string` |
| `internal/provider/prompt_cache_key.go` | Mode normalize, resolve key, format key, DeepSeek-shaped check by kind+baseURL, fail-open map, reject heuristic |
| `internal/provider/prompt_cache_key_test.go` | Policy + fail-open unit tests |
| `internal/provider/openai/openai.go` | Marshal `prompt_cache_key`; hard-omit DeepSeek; reserve ExtraBody field; after-merge canonical win |
| `internal/provider/openai/openai_test.go` | Wire JSON tests |
| `internal/provider/responses/responses.go` | Body field for non-DeepSeek; hard-omit DeepSeek vendor |
| `internal/provider/responses/responses_test.go` | Wire body tests |
| `internal/config/config.go` | `AgentConfig.PromptCacheKey`, `PromptCacheKeyValue` |
| `internal/config/render.go` (+ load defaults if needed) | Persist non-default values |
| `internal/config/*_test.go` | Load/render round-trip |
| `internal/agent/agent.go` | Options fields; `stream` sets `Request.PromptCacheKey` |
| `internal/agent/prompt_cache_key_test.go` or extend existing | Agent attaches key |
| `internal/boot/boot.go` | Wire config + entry kind/baseURL into `agent.Options` |
| `internal/control/controller.go` (if needed) | Keep session cache id in sync with `SessionPath` / subagent path |
| `internal/event/event.go` | Optional Notice codes for fail-open + prefix invalidation |
| `internal/cli/...` | Notice on model / token-mode switch (Phase 1 UX) |
| `.env.example` or config samples | Document keys if project documents agent TOML there |

---

### Task 1: Policy helper — resolve + format + DeepSeek omit

**Files:**
- Create: `internal/provider/prompt_cache_key.go`
- Create: `internal/provider/prompt_cache_key_test.go`

**Interfaces:**
- Produces:
  - `func NormalizePromptCacheKeyMode(mode string) string` → `"auto"|"on"|"off"|"custom"`
  - `func FormatSessionPromptCacheKey(sessionID, subID string) string`
  - `func IsDeepSeekShaped(kind, baseURL string) bool`
  - `func ResolvePromptCacheKey(mode, customValue, kind, baseURL, sessionID, subID string) string`
  - Fail-open helpers used in Task 4 (stubs OK here if tested in Task 4)

- [ ] **Step 1: Write the failing tests**

```go
package provider_test

import (
	"testing"

	"corvus/internal/provider"
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
	if got := provider.FormatSessionPromptCacheKey("abc123", ""); got != "corvus:session:abc123" {
		t.Fatalf("got %q", got)
	}
	if got := provider.FormatSessionPromptCacheKey("abc123", "sa_deadbeef"); got != "corvus:session:abc123:sub:sa_deadbeef" {
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
	if got != "corvus:session:sess1" {
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
	if got != "corvus:session:sess1" {
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
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/provider/ -run 'TestNormalizePromptCacheKeyMode|TestFormatSessionPromptCacheKey|TestResolvePromptCacheKey' -count=1
```

Expected: FAIL (undefined symbols or missing package symbols).

- [ ] **Step 3: Minimal implementation**

```go
// internal/provider/prompt_cache_key.go
package provider

import (
	"net/url"
	"strings"
)

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

func FormatSessionPromptCacheKey(sessionID, subID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	key := "corvus:session:" + sessionID
	if sub := strings.TrimSpace(subID); sub != "" {
		key += ":sub:" + sub
	}
	return key
}

// IsDeepSeekShaped uses host rules aligned with openai.IsDeepSeek / responses.DetectVendor.
// Keep host matching here (or call shared helpers carefully to avoid import cycles).
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

func ResolvePromptCacheKey(mode, customValue, kind, baseURL, sessionID, subID string) string {
	mode = NormalizePromptCacheKeyMode(mode)
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "anthropic" {
		return ""
	}
	if kind != "openai" && kind != "responses" && kind != "dashscope-responses" {
		// Unknown kinds: omit unless later extended.
		if kind != "" && kind != "openai" {
			// treat empty kind as openai-compatible only when callers pass "openai"
		}
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
```

Implement `ProviderFingerprint`, `IsPromptCacheKeyDisabled` as no-op stubs returning false until Task 4, **or** implement the map in this task if easier (tests for fail-open still in Task 4).

**Import cycle note:** Prefer pure host matching in `provider` package (duplicate the small DeepSeek host rule already in `openai.IsDeepSeek` / `responses.DetectVendor`) rather than importing `openai` from `provider`.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/provider/ -run 'TestNormalizePromptCacheKeyMode|TestFormatSessionPromptCacheKey|TestResolvePromptCacheKey' -count=1
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/prompt_cache_key.go internal/provider/prompt_cache_key_test.go
git commit -m "$(cat <<'EOF'
feat(provider): add prompt_cache_key resolve policy

Session-namespaced sticky keys for OpenAI/Responses; DeepSeek and Anthropic omit.
EOF
)"
```

---

### Task 2: `provider.Request` + OpenAI wire field

**Files:**
- Modify: `internal/provider/provider.go` (`Request` struct ~158)
- Modify: `internal/provider/openai/openai.go` (`buildRequest`, `chatRequest`, `MarshalJSON`, `reservedExtraBodyField`)
- Modify: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: `Request.PromptCacheKey`
- Produces: JSON body field `prompt_cache_key` when non-empty and client is not DeepSeek

- [ ] **Step 1: Write the failing tests**

```go
func TestBuildRequestIncludesPromptCacheKey(t *testing.T) {
	c := &client{model: "gpt-test", baseURL: "https://api.openai.com", deepseek: false}
	req := c.buildRequest(provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		PromptCacheKey: "corvus:session:abc",
	})
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["prompt_cache_key"] != "corvus:session:abc" {
		t.Fatalf("body=%s", body)
	}
}

func TestBuildRequestOmitsEmptyPromptCacheKey(t *testing.T) {
	c := &client{model: "gpt-test", deepseek: false}
	body, _ := json.Marshal(c.buildRequest(provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}}))
	if strings.Contains(string(body), "prompt_cache_key") {
		t.Fatalf("unexpected field: %s", body)
	}
}

func TestBuildRequestDeepSeekHardOmitsPromptCacheKey(t *testing.T) {
	c := &client{model: "deepseek-v4", deepseek: true, baseURL: "https://api.deepseek.com"}
	body, _ := json.Marshal(c.buildRequest(provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		PromptCacheKey: "corvus:session:abc",
	}))
	if strings.Contains(string(body), "prompt_cache_key") {
		t.Fatalf("DeepSeek must omit: %s", body)
	}
}

func TestExtraBodyCannotInjectPromptCacheKeyOnDeepSeek(t *testing.T) {
	c := &client{
		model: "deepseek-v4", deepseek: true,
		extraBody: map[string]any{"prompt_cache_key": "evil"},
	}
	body, _ := json.Marshal(c.buildRequest(provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}))
	if strings.Contains(string(body), "prompt_cache_key") {
		t.Fatalf("ExtraBody must not inject on DeepSeek: %s", body)
	}
}

func TestPromptCacheKeyWinsOverExtraBody(t *testing.T) {
	c := &client{
		model: "gpt", deepseek: false,
		extraBody: map[string]any{"prompt_cache_key": "from-extra"},
	}
	body, _ := json.Marshal(c.buildRequest(provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		PromptCacheKey: "from-request",
	}))
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if m["prompt_cache_key"] != "from-request" {
		t.Fatalf("canonical Request must win: %s", body)
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

```bash
go test ./internal/provider/openai/ -run 'TestBuildRequestIncludesPromptCacheKey|TestBuildRequestOmitsEmptyPromptCacheKey|TestBuildRequestDeepSeekHardOmitsPromptCacheKey|TestExtraBodyCannotInjectPromptCacheKeyOnDeepSeek|TestPromptCacheKeyWinsOverExtraBody' -count=1
```

- [ ] **Step 3: Implement**

1. Add to `provider.Request`:

```go
// PromptCacheKey is an OpenAI-family sticky routing key. Empty means omit.
// Adapters hard-omit on DeepSeek-shaped endpoints even when set.
PromptCacheKey string
```

2. Add to `chatRequest`:

```go
PromptCacheKey string `json:"prompt_cache_key,omitempty"`
```

3. In `buildRequest`, after constructing `out`:

```go
if !c.deepseek {
	out.PromptCacheKey = strings.TrimSpace(req.PromptCacheKey)
}
```

4. In `reservedExtraBodyField`, add `"prompt_cache_key"` so ExtraBody cannot set it (canonical path only). **Also** after ExtraBody merge in `MarshalJSON`, if `r.PromptCacheKey != ""` force `body["prompt_cache_key"] = r.PromptCacheKey`; if DeepSeek / empty, `delete(body, "prompt_cache_key")`.

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./internal/provider/openai/ -run 'TestBuildRequestIncludesPromptCacheKey|TestBuildRequestOmitsEmpty|TestBuildRequestDeepSeek|TestExtraBody|TestPromptCacheKeyWins' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/provider/provider.go internal/provider/openai/openai.go internal/provider/openai/openai_test.go
git commit -m "$(cat <<'EOF'
feat(openai): send prompt_cache_key on non-DeepSeek requests

Canonical Request field wins over ExtraBody; DeepSeek hard-omits.
EOF
)"
```

---

### Task 3: Responses wire field

**Files:**
- Modify: `internal/provider/responses/responses.go` (`buildRequestBody`)
- Modify: `internal/provider/responses/responses_test.go`

**Interfaces:**
- Consumes: `req.PromptCacheKey`, `c.vendor` / `DetectVendor`
- Produces: `body["prompt_cache_key"]` when non-DeepSeek and key non-empty

- [ ] **Step 1: Write the failing tests**

```go
func TestBuildRequestBodyPromptCacheKeyNonDeepSeek(t *testing.T) {
	c := New(Config{Name: "oai", BaseURL: "https://api.openai.com", Model: "gpt-test"}).(*client)
	body, _, _ := c.buildRequestBody(provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		PromptCacheKey: "corvus:session:abc",
	})
	if body["prompt_cache_key"] != "corvus:session:abc" {
		t.Fatalf("body=%#v", body)
	}
}

func TestBuildRequestBodyPromptCacheKeyDeepSeekOmits(t *testing.T) {
	c := New(Config{Name: "deepseek", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash"}).(*client)
	body, _, _ := c.buildRequestBody(provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		PromptCacheKey: "corvus:session:abc",
	})
	if _, ok := body["prompt_cache_key"]; ok {
		t.Fatalf("DeepSeek Responses must omit key: %#v", body)
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
go test ./internal/provider/responses/ -run 'TestBuildRequestBodyPromptCacheKey' -count=1
```

- [ ] **Step 3: Implement** in `buildRequestBody` before return:

```go
if c.vendor != "deepseek" {
	if key := strings.TrimSpace(req.PromptCacheKey); key != "" {
		body["prompt_cache_key"] = key
	}
}
```

(Use whatever field holds vendor on `client` — already set from `DetectVendor` in `New`.)

- [ ] **Step 4: Run — expect PASS**

```bash
go test ./internal/provider/responses/ -run 'TestBuildRequestBodyPromptCacheKey' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/provider/responses/responses.go internal/provider/responses/responses_test.go
git commit -m "$(cat <<'EOF'
feat(responses): send prompt_cache_key except DeepSeek vendor

Aligns with Codex for non-DeepSeek; preserves DeepSeek Responses wire shape.
EOF
)"
```

---

### Task 4: Fail-open fingerprint map + reject heuristic

**Files:**
- Modify: `internal/provider/prompt_cache_key.go`
- Modify: `internal/provider/prompt_cache_key_test.go`
- Modify: `internal/provider/openai/openai.go` (on stream/complete error path after 400)
- Modify: `internal/provider/responses/responses.go` (same)

**Interfaces:**
- Produces:
  - `func ProviderFingerprint(kind, baseURL string) string`
  - `func PromptCacheKeyRejected(err error) bool`
  - `func DisablePromptCacheKey(fingerprint string)`
  - `func IsPromptCacheKeyDisabled(fingerprint string) bool`
  - `func ClearPromptCacheKeyDisablesForTest()` (test-only)

**Heuristic (pin this):**

`PromptCacheKeyRejected` returns true only when:

1. Error is `*provider.APIError` with `Status == 400` (or 422 if used), **and**
2. Lowercased body matches any of: `prompt_cache_key`, `unknown field`, `unknown parameter`, `unrecognized`, `extra inputs are not permitted`, `additional properties`

Callers should only invoke disable when the **request actually included** a non-empty key.

- [ ] **Step 1: Write failing unit tests for heuristic + map**

```go
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
	fp := provider.ProviderFingerprint("openai", "https://gateway.example/v1")
	provider.DisablePromptCacheKey(fp)
	got := provider.ResolvePromptCacheKey("auto", "", "openai", "https://gateway.example/v1", "sess", "")
	if got != "" {
		t.Fatalf("disabled fingerprint still resolved key %q", got)
	}
}
```

- [ ] **Step 2: Run — expect FAIL / implement map**

Use `sync.Map` or mutex+map process-global in `prompt_cache_key.go`. Wire `ResolvePromptCacheKey` to check disable (already sketched in Task 1).

- [ ] **Step 3: Adapter hooks**

Where openai/responses turn HTTP 400 into `APIError` and return from Stream:

```go
if keyWasSent && provider.PromptCacheKeyRejected(err) {
	provider.DisablePromptCacheKey(provider.ProviderFingerprint("openai", c.baseURL))
	// optional: attach a sentinel or let agent Notice on next resolve empty after disable
}
```

**Do not** automatically retry the same turn without the key in Phase 1 (avoids surprise double billing). Next request omits via resolver.

Agent Notice for fail-open: either  
- adapters cannot emit agent Notices easily → return a wrapped error type `PromptCacheKeyDisabledError` once, **or**  
- agent checks before stream if previous disable just happened is hard; simpler: agent compares “wanted key non-empty but Resolve returned empty due to disable” requires `WasPromptCacheKeyDisabled` API.

Minimal Phase 1 UX: when `Resolve` returns empty **and** `IsPromptCacheKeyDisabled(fp)`, agent emits one Notice per fingerprint per process:

```go
// Notice text
"Disabled prompt_cache_key for this provider endpoint after the API rejected the field. Set [agent].prompt_cache_key = \"off\" to silence, or upgrade/fix the gateway."
```

Track emitted fingerprints on Agent with a small set.

- [ ] **Step 4: Tests pass**

```bash
go test ./internal/provider/ -run 'TestPromptCacheKeyRejected|TestFailOpen' -count=1
go test ./internal/provider/openai/ ./internal/provider/responses/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/provider/prompt_cache_key.go internal/provider/prompt_cache_key_test.go internal/provider/openai/openai.go internal/provider/responses/responses.go
git commit -m "$(cat <<'EOF'
feat(provider): fail-open when gateways reject prompt_cache_key

Process-local fingerprint disable after clear 400; no same-turn rebill retry.
EOF
)"
```

---

### Task 5: Config surface

**Files:**
- Modify: `internal/config/config.go` (`AgentConfig`)
- Modify: `internal/config/render.go` (emit non-default)
- Modify: `internal/config/config.go` defaults if needed (`PromptCacheKey` default `""` → treat as auto)
- Test: `internal/config/render_test.go` or new small test

**Interfaces:**
- Produces: `AgentConfig.PromptCacheKey string \`toml:"prompt_cache_key"\``  
  `AgentConfig.PromptCacheKeyValue string \`toml:"prompt_cache_key_value"\``

- [ ] **Step 1: Write failing load/render test**

```go
func TestAgentPromptCacheKeyRoundTrip(t *testing.T) {
	// Write TOML with prompt_cache_key = "off" and prompt_cache_key_value = "x"
	// Load via existing Load helpers used in render_test
	// Assert cfg.Agent.PromptCacheKey == "off" and Value == "x"
	// Render and assert lines present
}
```

Follow patterns in `internal/config/render_test.go` for `[agent]` fields.

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement fields + render when ≠ default**

Default empty mode ⇒ `auto`. Render only when mode is non-empty and not `auto`, or when custom value set.

- [ ] **Step 4: PASS + commit**

```bash
go test ./internal/config/ -run 'TestAgentPromptCacheKey' -count=1
git add internal/config/
git commit -m "$(cat <<'EOF'
feat(config): agent prompt_cache_key and prompt_cache_key_value

auto|on|off|custom with separate value field for sticky cache keys.
EOF
)"
```

---

### Task 6: Agent attaches key on stream

**Files:**
- Modify: `internal/agent/agent.go` (`Options`, `Agent` fields, `New`, `stream`)
- Create or modify: `internal/agent/prompt_cache_test.go`

**Interfaces:**
- Consumes: `Options.PromptCacheKeyMode`, `PromptCacheKeyValue`, `ProviderKind`, `ProviderBaseURL`, `SessionCacheID`, `SubagentCacheID`
- Produces: `Request.PromptCacheKey` on every `a.prov.Stream` call

- [ ] **Step 1: Write failing test** (mirror `userInputCaptureProvider`)

```go
func TestStreamAttachesPromptCacheKey(t *testing.T) {
	prov := &userInputCaptureProvider{}
	sess := NewSession("system")
	a := New(prov, tool.NewRegistry(), sess, Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.openai.com/v1",
		SessionCacheID:     "abc123",
	}, event.Discard)
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if prov.request.PromptCacheKey != "corvus:session:abc123" {
		t.Fatalf("got %q", prov.request.PromptCacheKey)
	}
}

func TestStreamOmitsPromptCacheKeyForDeepSeek(t *testing.T) {
	prov := &userInputCaptureProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("system"), Options{
		PromptCacheKeyMode: "on",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.deepseek.com",
		SessionCacheID:     "abc123",
	}, event.Discard)
	_ = a.Run(context.Background(), "hi")
	if prov.request.PromptCacheKey != "" {
		t.Fatalf("got %q", prov.request.PromptCacheKey)
	}
}

func TestStreamSubagentPromptCacheKey(t *testing.T) {
	prov := &userInputCaptureProvider{}
	a := New(prov, tool.NewRegistry(), NewSession("system"), Options{
		PromptCacheKeyMode: "auto",
		ProviderKind:       "openai",
		ProviderBaseURL:    "https://api.openai.com/v1",
		SessionCacheID:     "parent1",
		SubagentCacheID:    "sa_ref1",
	}, event.Discard)
	_ = a.Run(context.Background(), "hi")
	if prov.request.PromptCacheKey != "corvus:session:parent1:sub:sa_ref1" {
		t.Fatalf("got %q", prov.request.PromptCacheKey)
	}
}
```

- [ ] **Step 2: Run — FAIL**

```bash
go test ./internal/agent/ -run 'TestStreamAttachesPromptCacheKey|TestStreamOmitsPromptCacheKeyForDeepSeek|TestStreamSubagentPromptCacheKey' -count=1
```

- [ ] **Step 3: Implement**

Add to `Options` and copy onto `Agent` in `New`:

```go
PromptCacheKeyMode  string
PromptCacheKeyValue string
ProviderKind        string
ProviderBaseURL     string
SessionCacheID      string // BranchID; updated when session path changes if needed
SubagentCacheID     string // SubagentMeta.Ref; empty for main agent
```

In `stream` when building `provider.Request`:

```go
key := provider.ResolvePromptCacheKey(
	a.promptCacheKeyMode, a.promptCacheKeyValue,
	a.providerKind, a.providerBaseURL,
	a.sessionCacheID, a.subagentCacheID,
)
// if key == "" && disabled → emit fail-open Notice once
ch, err := a.prov.Stream(ctx, provider.Request{
	Messages: requestMessages,
	Tools: a.tools.Schemas(),
	MaxTokens: a.maxOutputTokens,
	Temperature: provider.OptionalTemperature(a.temperature),
	PromptCacheKey: key,
})
```

Add `SetSessionCacheID(id string)` if Controller must update after resume/new session without rebuilding Agent.

- [ ] **Step 4: PASS + commit**

```bash
go test ./internal/agent/ -run 'TestStreamAttachesPromptCacheKey|TestStreamOmits|TestStreamSubagent' -count=1
git add internal/agent/
git commit -m "$(cat <<'EOF'
feat(agent): attach resolved prompt_cache_key on each stream

Uses BranchID session cache id and optional subagent Ref suffix.
EOF
)"
```

---

### Task 7: Boot + Controller wiring

**Files:**
- Modify: `internal/boot/boot.go` (executor/planner/subagent `agent.Options`)
- Modify: `internal/control/controller.go` if session path changes must refresh `SessionCacheID`
- Modify: task/subagent construction paths that call `agent.New` / `RunReadOnlySubAgent*`

**Rules:**
- `SessionCacheID: agent.BranchID(sessionPath)` for main session (from controller opts / boot session path).
- `ProviderKind: entry.Kind`, `ProviderBaseURL: entry.BaseURL`.
- `PromptCacheKeyMode: cfg.Agent.PromptCacheKey`, value from `cfg.Agent.PromptCacheKeyValue`.
- Subagent: `SubagentCacheID: meta.Ref`; `SessionCacheID` = parent BranchID (from parent session path / ParentSession stem).
- Ephemeral subagent with empty Ref: leave `SubagentCacheID` empty **and** if no stable parent id, omit key (`SessionCacheID` empty → Resolve returns "").

- [ ] **Step 1: Write a boot-level or control-level test** if cheap; otherwise unit-test a small helper:

```go
// e.g. boot.promptCacheOptions(cfg, entry, sessionPath, subRef) agent options fields
```

Prefer extracting a pure helper in boot to keep tests free of full `boot.Build`.

- [ ] **Step 2: Implement wiring at every `agent.New` / Options factory in boot** (executor, planner, subagent skill options).

- [ ] **Step 3: On `Controller.SetSessionPath` / NewSession / Resume**, if Agent exposes `SetSessionCacheID`, update to `BranchID(newPath)`.

- [ ] **Step 4: Run focused tests**

```bash
go test ./internal/boot/ -count=1 -timeout 120s
go test ./internal/control/ -count=1 -timeout 120s
go test ./internal/agent/ -run 'TestStream' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/boot/ internal/control/ internal/agent/
git commit -m "$(cat <<'EOF'
feat(boot): wire prompt cache key policy into agent options

Session BranchID and provider kind/baseURL drive sticky key resolution.
EOF
)"
```

---

### Task 8: Invalidation Notice (model / token-mode / tools)

**Files:**
- Modify: `internal/event/event.go` (optional codes)
- Modify: CLI model switch + work/token mode switch paths (`internal/cli/chat_tui.go`, `internal/cli/work_mode.go`, etc.)
- Optionally: when `CacheDiagnostics.PrefixChanged` already prints receipt — ensure model/token switches emit a **pre-action** Notice

**Copy (English, match existing Notice tone):**

- Model switch: `Switching models may reset the provider prompt-cache prefix for this session.`
- Token mode switch: `Switching work/token mode changes the tools surface and may reset the prompt-cache prefix.`
- Tools schema growth (if a single hook exists): `Tool definitions changed; the prompt-cache tools prefix may miss on the next turn.`

Phase 1 is Notice-only (no confirm dialog).

- [ ] **Step 1: Locate switch handlers; write CLI/unit test that captures sink Notice after switch if testable; else table-test a pure `CacheInvalidationNotice(reason string) string` helper.**

- [ ] **Step 2: Implement emits**

- [ ] **Step 3: `go test` for touched packages**

- [ ] **Step 4: Commit**

```bash
git commit -m "$(cat <<'EOF'
feat(cli): notice when user actions may invalidate prompt cache

Model and token-mode switches warn about prefix reset cost.
EOF
)"
```

---

### Task 9: Anthropic regression + full Phase 1 verification

**Files:**
- Touch only if a test is missing: `internal/provider/anthropic/anthropic_test.go` (already has cache_control + DeepSeek omit)

- [ ] **Step 1: Run Anthropic breakpoint tests (must stay green)**

```bash
go test ./internal/provider/anthropic/ -count=1
```

- [ ] **Step 2: Run OpenAI + Responses + provider policy + agent cache tests**

```bash
go test ./internal/provider/ ./internal/provider/openai/ ./internal/provider/responses/ ./internal/provider/anthropic/ -count=1
go test ./internal/agent/ -run 'Cache|PromptCache|StreamAttaches|cachehit' -count=1
go test ./internal/config/ -run 'PromptCache' -count=1
```

- [ ] **Step 3: Run broader safety net**

```bash
go test ./internal/agent/ ./internal/boot/ ./internal/control/ -count=1 -timeout 180s
```

- [ ] **Step 4: Optional live probe (not CI)** — document in commit message only; do not gate merge:

```bash
# existing patterns
go test -tags live ./internal/provider/openai/ -run TestRealDeepSeekCacheProbe -count=1
# future: OpenAI turn-2 cached_tokens probe if credentials present
```

- [ ] **Step 5: Final commit only if docs/examples updated** (e.g. `.env.example` or config sample):

```bash
git add -A
git commit -m "$(cat <<'EOF'
docs: note agent prompt_cache_key settings for Phase 1
EOF
)"
```

---

## Spec coverage (self-review)

| Spec Phase 1 item | Task |
|-------------------|------|
| `Request.PromptCacheKey` | T2 |
| Agent stream sets key | T6 |
| openai marshal + DeepSeek omit | T2 |
| responses marshal + DeepSeek omit | T3 |
| Config auto/on/off/custom + value | T5 |
| Policy helper | T1 |
| Anthropic keep + tests | T9 |
| Fail-open Phase 1 | T4 |
| ExtraBody reserved / canonical win | T2 |
| Session BranchID + sub Ref | T6–T7 |
| Invalidation Notice | T8 |
| Soft live probe optional | T9 |
| Phase 2/3 | **Deferred** — new plans after Phase 1 green |

## Placeholder scan

No TBD steps; fail-open heuristic strings pinned; subID = `SubagentMeta.Ref`; ExtraBody openai-only documented in Global Constraints.

## Follow-on plans (do not implement in this plan)

1. **Phase 2** — `<runtime-update>` for non-tools knobs; token mode remains invalidating.  
2. **Phase 3** — Full/Balanced deferred MCP schemas; pin `use_capability` as sole default proxy; eager opt-in; update boot contracts.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-06-prompt-cache-hit-phase1.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks (`superpowers:subagent-driven-development`)
2. **Inline Execution** — execute tasks in this session with checkpoints (`superpowers:executing-plans`)

**Which approach?**
