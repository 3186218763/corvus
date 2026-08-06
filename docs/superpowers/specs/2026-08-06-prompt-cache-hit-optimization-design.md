# Prompt Cache Hit Optimization — Design

**Date:** 2026-08-06  
**Status:** Draft for implementation planning (revised after design review)  
**Inputs:**  
- `docs/research/agent-prompt-caching-survey.md`  
- `docs/research/prompt-cache-wire-compatibility.md`  
- Brainstorming: full P0–P2 as one program, phased delivery; success metric = OpenAI/gateway hit rate; DeepSeek must not regress  
- Design review: Responses×DeepSeek omit, fail-open in Phase 1, Phase 2/3 boundary tightening  

---

## 1. Problem

Reasonix already keeps **append-only history** and a **boot-stable system prefix** (good for DeepSeek automatic caching). Gaps vs Codex / Claude Code / Agents SDK / OpenCode:

| Gap | Effect |
|-----|--------|
| OpenAI/Responses never send sticky cache key | Gateway multi-replica → near-zero hits |
| Hot changes expand tools/system when avoidable | Prefix churn (`tools` / rebuild) |
| Full mode dumps MCP schemas into tools | Large, unstable tools prefix |
| Invalidation is silent | Users flip model/mode without knowing cost |

Anthropic **already** places `cache_control` breakpoints (disabled for DeepSeek). Do not reimplement; only keep and test. (Survey comparison tables that once said “no breakpoints” are outdated — see research fix.)

---

## 2. Goals and non-goals

### Goals

1. **OpenAI-family hit rate:** session-stable `prompt_cache_key` on the wire where the protocol supports it.  
2. **No DeepSeek regression:** no extra cache fields that change wire shape or break APIs (openai **and** responses **and** anthropic kinds).  
3. **Hot updates without rewriting the prefix:** prefer append-only runtime deltas for knobs that do **not** require a tools-surface change.  
4. **Smaller tools prefix where safe:** defer MCP schemas behind a stable proxy by default in Full/Balanced (Phase 3).  
5. **Honest UX:** notice when a user action will reset the cacheable prefix.

### Non-goals (this program)

- Server-side `/responses/compact` or encrypted compaction  
- Rewriting local compact algorithm  
- Bedrock-native `cachePoint` / Copilot-specific fields (no client for them today)  
- Cross-session or cross-user shared cache keys  
- Vercel AI SDK `promptCacheKey` camelCase (Reasonix speaks HTTP REST)  
- `prompt_cache_retention` / GPT-5.x `prompt_cache_options` / explicit OpenAI breakpoints (record as deferred levers; not Phase 1–3)  
- Unifying every tail tag into a single `<system-reminder>` channel (nice-to-have; Phase 2 may introduce `<runtime-update>` without forcing a global rename)

---

## 3. Design principles (clean rules)

1. **Two mechanisms, never mixed**  
   - OpenAI / Responses (non-DeepSeek) → sticky **key** on the request body  
   - Anthropic Messages (non-DeepSeek) → **breakpoints** on content (existing)  
   - DeepSeek (openai **or** responses **or** anthropic) → **neither** extra field  

2. **HTTP field names match REST, not AI SDK**  
   - Always `prompt_cache_key` (snake_case) for OpenAI-compatible JSON  
   - Never default-send `promptCacheKey`  

3. **Adapters own wire mapping**  
   - Agent/Controller only supply a stable session id + policy  
   - `openai` / `responses` / `anthropic` packages decide what to emit  
   - Adapter hard-omit always wins over a non-empty `Request.PromptCacheKey` when the endpoint is DeepSeek-shaped  

4. **Append beats rewrite**  
   - Runtime policy changes that do not require a tools-surface change go to the **user-turn tail**, not system rewrite  
   - Token-mode / tools-set changes are **invalidating by nature**; do not pretend a tail message preserves `ToolsHash`  

5. **Phased delivery, one architecture**  
   - Ship Phase 1 without waiting for Phase 3  

---

## 4. Architecture

```
Controller / Agent
  · stable session id = BranchID(sessionPath)  // filepath stem; resume-safe
  · CacheKeyMode from config (auto|on|off|custom)
  · optional runtime-update tail (Phase 2; non-tools knobs only)
  · tools surface policy (Phase 3)
           │
           ▼
provider.Request
  Messages, Tools, Temperature, MaxTokens
  + PromptCacheKey string   // empty = omit; adapters may still force-omit
           │
     ┌─────┴─────────────────────────────┐
     ▼                                   ▼
openai / responses                 anthropic
  if key != "" && !DeepSeek:         if !deepseek:
    body["prompt_cache_key"]=key       cache_control on system tail
  DeepSeek host/vendor: force omit     + last message block (existing)
```

No new top-level package. Small helpers may live next to provider or agent (`cache_key.go`-sized), not a framework.

### Session id and key scope

| Case | Key |
|------|-----|
| Main session | `reasonix:session:<BranchID>` where `BranchID` = `filepath.Base(sessionPath)` without extension |
| Resume same file | Same key (path stem unchanged; title rename must not change stem) |
| Subagent / parallel task | **Separate** key: `reasonix:session:<parentBranchID>:sub:<subID>` so distinct prefixes do not share one OpenAI ~15 RPM routing bucket |
| Custom config | Raw string from `prompt_cache_key_value` when mode is `custom` |

### ExtraBody precedence

OpenAI client already merges `ExtraBody`. Rules for Phase 1:

1. `Request.PromptCacheKey` is the **canonical** agent path.  
2. If adapter would send a key, set body field from `Request.PromptCacheKey` **after** ExtraBody merge so the canonical value wins.  
3. Treat `prompt_cache_key` as a reserved ExtraBody key (strip or ignore user ExtraBody copies) so the field is not double-serialized with conflicting values.  
4. Config `off` or DeepSeek hard-omit → field never appears, even if ExtraBody tried to set it.

---

## 5. Wire compatibility matrix

Source of truth for open-source mapping: `docs/research/prompt-cache-wire-compatibility.md` (must match this table).

**DeepSeek-shaped** means: official `*.deepseek.com` host detection already used by `openai.IsDeepSeek` / responses `DetectVendor` / anthropic `deepseek` flag. Non-official gateways that only *look* OpenAI-compatible are **not** auto-classified as DeepSeek; use `off` or fail-open if they reject the field. If a gateway is already detected as DeepSeek wire shape by existing host/vendor helpers, hard-omit applies.

| Condition | Sticky key | Breakpoints |
|-----------|------------|-------------|
| `kind=anthropic`, not DeepSeek | omit | **keep existing** `cache_control` |
| `kind=anthropic`, DeepSeek | omit | omit (existing) |
| `kind=openai`, DeepSeek-shaped | omit | omit |
| `kind=openai`, OpenAI / Azure / known OpenAI-compatible | send `prompt_cache_key` | omit |
| `kind=openai`, unknown base URL | see **auto policy** below | omit |
| `kind=responses`, DeepSeek-shaped | **omit** (not Codex; DeepSeek uses Responses today) | omit |
| `kind=responses`, non-DeepSeek | send `prompt_cache_key` (Codex-aligned) | omit |
| config `off` | never | Anthropic breakpoints unchanged |
| config `on` | try on openai/responses non-DeepSeek only; DeepSeek hard-omit still wins | — |
| config `custom` | same as `on`, value from `prompt_cache_key_value` | — |

### auto policy

Gateway hit rate is a product goal, but strict proxies often 400 on unknown fields (Agents SDK only auto-injects for official OpenAI; OpenCode uses allowlist + force flag).

| `auto` target | Behavior |
|---------------|----------|
| Official OpenAI / Azure / known good OpenAI-compatible hosts | send key |
| `kind=responses` non-DeepSeek | send key |
| DeepSeek-shaped (any kind) | omit |
| Unknown openai-compatible base URL | **send key** (gateway stickiness preferred) **and** enable fail-open |

### Fail-open (required in Phase 1, not deferred)

If a provider returns **400** (or equivalent client error) and the error text/body clearly rejects `prompt_cache_key` / unknown field:

1. Disable sticky key for that **provider fingerprint** (kind + baseURL host, process-local) for the rest of the process lifetime.  
2. Emit a Notice: key disabled for this endpoint; set `prompt_cache_key = off` or fix the gateway.  
3. Do **not** retry the failed turn with a second automatic request unless the existing retry layer already does safe retries for 400s; prefer “next turn omits key” to avoid double-billing surprises. Document if a single safe retry-without-key is added later.

Config `on` / `custom` still hard-omit DeepSeek, but do **not** re-enable a fingerprint that fail-open disabled until process restart (or explicit config change).

### Key value

- Default: `reasonix:session:<BranchID>` (see §4)  
- Namespace avoids colliding with other clients on shared gateways  
- Custom: raw string from config (no `custom:` prefix parsing)

---

## 6. Phases

### Phase 1 — Protocol (hit rate)

**Work**

1. Extend `provider.Request` with `PromptCacheKey string` (empty means omit).  
2. Agent `stream` (and any other Stream entry points) set key from session + config policy.  
3. `openai` client: marshal `prompt_cache_key` when non-empty; **hard-omit** for DeepSeek-shaped.  
4. `responses` client: same field on create body for non-DeepSeek; **hard-omit** for DeepSeek Responses.  
5. Config:

   ```toml
   [agent]
   # auto | on | off | custom
   prompt_cache_key = "auto"
   # used only when prompt_cache_key = "custom"
   # prompt_cache_key_value = "my-stable-id"
   ```

6. Policy helper: `(kind, baseURL, mode, customValue, sessionID, subagentID?) → key or ""`.  
7. Anthropic: no behavior change; extend tests documenting breakpoints + DeepSeek omit.  
8. Fail-open fingerprint map + Notice (see §5).  
9. ExtraBody reserved-field handling (see §4).  
10. UX: when model switch / token-mode switch / tools schema set changes, emit Notice that cache prefix may reset (reuse or extend `CacheDiagnostics` / Notice codes). Phase 1 is Notice-only (no confirm dialog).  

**Acceptance**

- Unit: OpenAI request JSON contains `prompt_cache_key` with namespaced session id when policy on.  
- Unit: DeepSeek **openai** path never contains the field.  
- Unit: DeepSeek **responses** path never contains the field.  
- Unit: Anthropic still has `cache_control`; DeepSeek-anthropic still does not.  
- Unit: fail-open disables key after a simulated 400 reject.  
- Unit: ExtraBody cannot override hard-omit or fight canonical key.  
- Existing `cachehit_e2e` still passes (prefix stability).  
- Responses non-DeepSeek path includes key when configured.  
- Soft / opt-in: live probe (like existing realcache tests) may assert `cached_tokens > 0` on turn 2 for official OpenAI or a known gateway; not required for CI.

### Phase 2 — Hot updates (less rigid)

**Scope — knobs that must NOT change tools schema**

Prefer tail injection (`<runtime-update>…</runtime-update>` on the **next user turn**, same idea as memory-update / background-jobs) for:

- permission / tool-approval mode (when not encoded into tools)  
- plan mode (gate-only; already mostly out of system)  
- additional dirs / cwd-style environment facts (when today risk rewriting early context)  
- similar host policy that is **text-only** for the model  

**Out of Phase 2 SystemHash stability (invalidating; Notice only)**

- **Token mode** (Full / Economy / Delivery): changes tools set and/or system contracts → `ToolsHash` and often `SystemHash` change by design  
- Model switch  
- System prompt file change (still new session / rebuild)  
- Eager MCP connect that mutates the tools array (Phase 3 reduces this; Economy may still churn until aligned)

**Work**

1. Single tail channel for host runtime deltas listed above.  
2. Do **not** rewrite the system message mid-session for those knobs.  
3. Document the invalidating set explicitly in UX copy and tests.  

**Acceptance**

- Changing **in-scope** knobs mid-session does not change `SystemHash` or `ToolsHash` in `PrefixShape`.  
- Model still sees the new policy via tail content.  
- Token-mode switch **may** change hashes; must emit invalidation Notice.  
- Tests for compose/tail injection.  

### Phase 3 — Tools surface (MCP)

**Product reality today (must be updated by this phase)**

- Full: large tool surface; **must not** expose `use_capability` (existing tests).  
- Delivery: Full surface **plus** `use_capability`.  
- Economy: `connect_tool_source`; MCP connect **mutates** tools (intentional churn).  

Phase 3 **changes** the Full/Balanced contract for MCP schemas.

**Work**

1. Full/Balanced default: **do not** dump every MCP tool schema into `tools` at boot.  
2. Discovery path (pick one primary; document it): stable proxy + catalog — extend **`use_capability`** (and/or a small catalog tool already present) so the model can reach MCP without full schemas in the prefix. This **will** require Full to expose the proxy (updates today’s “Full must not expose use_capability” tests).  
3. Eager opt-in: config flag (e.g. per-server `alwaysLoad` / global `mcp.eager_schemas = true`) restores old “all MCP schemas in tools” behavior.  
4. Economy: keep `connect_tool_source`; prefer aligning connect so that enabling MCP **does not** reshuffle core builtin order; longer-term prefer proxy-first so connect does not grow the tools array (if not feasible in this phase, document residual churn + Notice).  
5. Delivery: remains “execution profile”; tools-prefix policy should stay consistent with Full’s deferred MCP default (not Full-schemas + proxy).  
6. When tools schema **must** grow, keep registration order stable; emit Notice on tools-prefix change.  

**Acceptance**

- Fresh Full session (default config): provider `tools` list **excludes** full MCP tool schemas.  
- MCP still reachable via the documented proxy/catalog path.  
- Eager opt-in restores previous schema dump.  
- Connecting MCP does not reshuffle core builtin order.  
- Boot tests updated for the new Full/Delivery/Economy contracts.  

---

## 7. Config surface (minimal)

| Key | Values | Default |
|-----|--------|---------|
| `[agent].prompt_cache_key` | `auto` \| `on` \| `off` \| `custom` | `auto` |
| `[agent].prompt_cache_key_value` | string | empty; **required** when mode is `custom` |

No `custom:<embedded-string>` grammar (colon ambiguity).  
No per-provider TOML explosion in v1. Detection uses kind + base URL inside `auto`.

Optional later (not required for Phase 1): `prompt_cache_key_format = namespaced | raw`.

---

## 8. Data flow (Phase 1)

```
boot.Build → Controller with sessionPath
  sessionID = BranchID(sessionPath)
Agent.Run → stream:
  key = ResolvePromptCacheKey(cfg, prov, sessionID, subID)
  prov.Stream(Request{..., PromptCacheKey: key})
openai / responses body:
  if DeepSeek-shaped { omit }
  else if key != "" { body.prompt_cache_key = key }
  on 400 reject of field → fail-open fingerprint + Notice
usage path unchanged (already normalises hit/miss)
CacheDiagnostics compares prefix; UI shows cached/new as today
```

---

## 9. Testing strategy

| Layer | Cases |
|-------|--------|
| Policy unit | DeepSeek openai omit; DeepSeek responses omit; OpenAI on; responses non-DeepSeek on; off; custom value; subagent key suffix |
| OpenAI marshal | JSON has snake_case field only when set; ExtraBody cannot force field on DeepSeek |
| Anthropic buildRequest | existing breakpoint tests stay green |
| Fail-open | simulated 400 → subsequent requests omit key |
| Agent | stream attaches key from BranchID |
| E2E | `cachehit_e2e` prefix stability unchanged |
| Phase 2 | SystemHash **and** ToolsHash stable across **in-scope** toggles + tail present; token-mode Notice |
| Phase 3 | Full boot tools list excludes MCP schemas by default; eager flag; proxy path |

No live network required for CI; optional live probe remains opt-in.

---

## 10. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Gateway 400 on unknown field | DeepSeek hard-omit; `off` config; **Phase 1 fail-open** |
| Key changes every resume | Bind to BranchID(sessionPath), not random per process |
| Subagents thrash one routing bucket | Separate sub keys under parent namespace |
| Phase 2 over-promises token mode | Token mode explicitly invalidating; not in SystemHash acceptance |
| Phase 3 breaks MCP / Full contract | Default deferred + eager opt-in; update boot tests; proxy path tested |
| Field present but still 0% hits | Soft live probe; keep prefix discipline (Phases 2–3); document retention as later lever |
| Scope creep | Phases ship independently; this doc is the contract |

---

## 11. Implementation order

1. Phase 1 only until green on CI  
2. Phase 2  
3. Phase 3  

Implementation plans may be one file with three milestones or three sequential plans; behavior must match this design.

---

## 12. References

- Survey: `docs/research/agent-prompt-caching-survey.md`  
- Wire compat: `docs/research/prompt-cache-wire-compatibility.md`  
- Reasonix today: `internal/provider/anthropic/anthropic.go` (breakpoints), `internal/agent/cache_shape.go`, `internal/agent/compact.go`, `internal/agent/branch.go` (`BranchID`), `internal/provider/responses` (DeepSeek Responses)  
- Upstream patterns: OpenCode `transform.ts`; Agents SDK `prompt_cache_key.py`; Codex `client.rs`  
- OpenAI: Prompt Caching guide + Prompt Caching 201 (routing stickiness, ~15 RPM/key, retention)  

---

## 13. Decision log

| Decision | Choice |
|----------|--------|
| Overall approach | Protocol + hot-update discipline (not a new framework package) |
| Program shape | One design, phased milestones |
| Success metric | OpenAI/gateway hits first; DeepSeek no regression |
| OpenAI field name | `prompt_cache_key` snake_case only |
| Anthropic | Keep existing breakpoints (already implemented) |
| DeepSeek | Hard-omit key **and** breakpoints on **all** kinds (openai, responses, anthropic) |
| Responses + DeepSeek | Omit key (Codex parity only for non-DeepSeek Responses) |
| auto + unknown host | Send key + **Phase 1 fail-open** (not deferred) |
| Session id | `BranchID(sessionPath)`; namespaced `reasonix:session:…` |
| Subagent keys | Separate `…:sub:<id>` under parent |
| ExtraBody | Canonical `Request.PromptCacheKey` wins; field reserved |
| Config custom | Separate `prompt_cache_key` + `prompt_cache_key_value` (no `custom:` prefix) |
| Phase 2 token mode | **Invalidating**; not part of SystemHash-stable acceptance |
| MCP Full | Deferred schemas by default in Phase 3; eager opt-in; Full gains proxy |
| Server compact / retention / OpenAI explicit breakpoints | Out of scope (deferred levers) |
