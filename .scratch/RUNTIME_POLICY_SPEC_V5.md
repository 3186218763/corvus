# Runtime Policy Specification V5

> Status: **Authoritative design** (2026-08-18). This document supersedes
> `SCAFFOLDING_DESIGN_V4_FINAL.md`, `IMPLEMENTATION_PLAN_RUNTIME_MODE.md`, and
> `runtime-mode-refactor-plan.md`. Implementers must follow V5 when a draft
> conflicts with it.

## 1. Purpose

Corvus currently uses `TokenMode` (`full`, `economy`, `delivery`) as a compact
input for unrelated behavior. V5 introduces a small, pure runtime-policy
resolver and keeps `TokenMode` as a compatibility preset. The resolver makes
three user-visible axes explicit while preserving existing provider-visible
behavior during migration.

The design is deliberately host-oriented: the host decides what the model may
do, what counts as completion, and which capabilities are initially available.
Guidance is only a model-visible hint and must not silently weaken host safety.

## 2. Goals and non-goals

### Goals

- Resolve a deterministic policy from explicit axis requests, legacy presets,
  model capability metadata, and effective provider effort.
- Preserve the current full/economy/delivery behavior as compatibility cases.
- Separate cognitive guidance, completion evidence, tool exposure, permissions,
  planner selection, and capability routing.
- Make policy persistent and reconstructable on resume and fork.
- Give implementers testable seams and explicit error behavior.

### Non-goals

- Replacing the existing permission or sandbox systems.
- Automatically classifying task risk from free-form strings.
- Introducing a new planner or changing the meaning of
  `agent.planner_model`.
- Exposing future values such as `minimal` exposure or `paranoid` assurance
  before their behavior exists.
- Inferring a model tier from a model ID or provider name.

## 3. Canonical model

The resolved runtime policy has three independent axes. Request types are
separate because `inherit` and `auto` are meaningful selections but are never
valid resolved values:

```go
type Guidance string   // resolved: off | light | structured
type Completion string // resolved: standard | verified
type Exposure string   // resolved: eager | deferred

type GuidanceSelection string   // inherit | auto | off | light | structured
type CompletionSelection string // inherit | auto | standard | verified
type ExposureSelection string   // inherit | auto | eager | deferred

type Request struct {
    Preset     Preset // full | economy | delivery
    Guidance   GuidanceSelection
    Completion CompletionSelection
    Exposure   ExposureSelection
}

type Policy struct {
    Guidance   Guidance   // resolved: off | light | structured
    Completion Completion // resolved: standard | verified
    Exposure   Exposure   // resolved: eager | deferred

    // Derived compatibility decisions; not additional user axes.
    LegacyPreset       Preset
    LegacySkillProfile string
    PlannerEligible    bool
    CapabilityFrontend string
    WorkspaceLease     bool
}
```

The boot adapter fills omitted selection fields with `inherit` before calling
the resolver. `inherit` uses the selected preset value; `auto` deliberately
uses the automatic default (`Guidance` uses the matrix in section 5,
`Completion` becomes `standard`, and `Exposure` becomes `eager`). This
distinction lets a user select automatic guidance while retaining a legacy
preset as metadata. A resolved `Policy` never contains `inherit`, `auto`, or an
empty enum. Unknown values are errors, not silent fallbacks.

The implementation belongs in a deep module, preferably
`internal/runtimepolicy`. Its public seam is intentionally small:

```go
func Resolve(Input) (Policy, error)
```

The resolver is pure and deterministic: no filesystem, network, mutable global,
tool invocation, or model-ID heuristic. Boot code adapts existing services to
the resolved policy and remains loop glue rather than policy ownership.

## 4. Resolution inputs

`Input` must contain the normalized request, model capability metadata, and the
effective effort already selected by `config.EffectiveEffort(entry)`. It may
also contain the legacy preset and feature facts needed for derived behavior,
but it must not inspect tool names or parse shell text.

Resolution order for each axis is normative:

1. An explicit concrete selection wins.
2. `auto` uses the axis's automatic default.
3. `inherit` uses the selected preset mapping in section 8.

The compatibility adapter maps an absent `TokenMode` to `full` with all axes
set to `inherit`, preserving today's default behavior. Selecting `/work-mode`
sets the chosen preset and resets all axis selections to `inherit`. Changing an
advanced axis updates only that selection and preserves the preset metadata.

### 4.1 Capability tier

The only accepted capability values are `auto`, `strong`, `standard`, and
`lite`. Empty/`auto` resolves conservatively to `standard`. A configured value
that is not one of these values produces a clear configuration error (or an
equivalent surfaced warning before resolution); it must not be silently mapped
to `standard`.

Capability metadata is explicit configuration, not runtime inference:

```go
type ProviderEntry struct {
    // existing fields...
    ModelCapability ModelCapabilityTier `toml:"model_capability,omitempty"`
    ModelOverrides  map[string]ProviderModelOverride `toml:"model_overrides,omitempty"`
}

type ProviderModelOverride struct {
    ModelCapability ModelCapabilityTier `toml:"model_capability,omitempty"`
}
```

The exact field placement may follow the existing config shape, but both
provider-level metadata and per-model overrides must be supported. The current
configuration type is a map, so examples and implementation must use map TOML
syntax, for example:

```toml
[[providers]]
name = "anthropic"
kind = "anthropic"
model_capability = "standard"
model_overrides = { "claude-opus-5" = { model_capability = "strong" } }
```

Curated provider presets may populate metadata. When an override exists it
wins over the provider default; otherwise provider metadata is used; absent or
`auto` metadata becomes `standard`.

### 4.2 Session API and frontend vocabulary

Add one typed, versioned selection value to `boot.Options` and session metadata;
keep `Options.TokenMode` as the deprecated compatibility input. When both are
present, a non-empty typed selection is authoritative. Frontends must use the
same axis names and enum values: `guidance`, `completion`, and `exposure`.

The CLI/headless flag vocabulary is `--guidance`, `--completion`, and
`--tool-exposure`. The interactive command vocabulary is:

```text
/runtime-policy
/runtime-policy guidance inherit|auto|off|light|structured
/runtime-policy completion inherit|auto|standard|verified
/runtime-policy exposure inherit|auto|eager|deferred
```

Calling `/runtime-policy` without arguments displays the preset, selections,
and resolved axes. Other frontends may use native controls but must send the
same typed selection. This command surface is additive; `/work-mode` and its
`/profile` alias remain supported.

### 4.3 Effort

Effort is provider-specific. Reuse the existing effort capability and
`config.EffectiveEffort(entry)` logic. Normalize only against the levels the
selected provider advertises (`unknown`, `disabled`, `low`, `medium`, `high`,
`xhigh`, `max` as applicable). Invalid effort values must return a clear error
or configuration warning. Effort may influence automatic guidance, but it can
never change the inherent capability tier.

## 5. Automatic guidance

An explicit `guidance=off|light|structured` always wins, including an explicit
`off` for standard or lite models. The following table is the normative default
for `guidance=auto`; `guidance=inherit` uses the preset instead. Columns are
effort bands after provider normalization.

| Capability | unknown | disabled | low | medium | high/xhigh | max |
| --- | --- | --- | --- | --- | --- | --- |
| strong | light | structured | light | light | off | off |
| standard | light | structured | structured | light | light | light |
| lite | structured | structured | structured | structured | structured | light |

Providers without an effort signal use `unknown`. The rule “only strong can be
free” applies to automatic resolution only; an explicit user override is an
intentional choice and is not rewritten.

Guidance prompt fragments are exactly:

- `off`: no fragment.
- `light`: inspect relevant context and choose a short plan before acting.
- `structured`: inspect context, state small steps, work one step at a time,
  and revisit the plan when evidence changes.

These fragments must not duplicate completion verification requirements.

## 6. Completion policy

`standard` keeps the existing ordinary turn behavior. `verified` keeps the
current Delivery evidence contract: delivery criteria, runtime marker,
evidence/checkpoint gates, completion review, and delivery workspace write
lease. During migration the existing `tokenDeliveryPrompt` text is preserved
byte-for-byte; only its typed selection changes.

Completion is not permission. A verified task can still be denied by the
existing permission policy, and standard completion does not grant approval.
Do not map an assurance value directly to allow/ask/deny.

`verified` may require todo/completion tools even when exposure is deferred.
Required completion capabilities must be described through an optional
`internal/tool.Tool` capability interface (for example, a pure
completion-requirement marker), not by hardcoded scheduler tool names.

## 7. Exposure policy

`eager` keeps the current full tool surface and immediate MCP/plugin startup.
`deferred` keeps the current economy behavior: core built-ins first, deferred
tool sources represented by the existing connector, and MCP/skill/catalog
sources loaded on demand. During migration the existing `tokenEconomyPrompt`
text is preserved byte-for-byte.

Exposure controls visibility and startup timing only. It does not alter
permissions, sandboxing, tool argument validation, or completion evidence.

The prompt fragment order is deterministic and must be:

1. Guidance fragment (if any)
2. Completion fragment (if any)
3. Exposure fragment (if any)

The combined `verified + deferred` case is supported. Deferred selection must
still expose the minimum capabilities required to satisfy verified completion.

## 8. Legacy preset adapter

`TokenMode` remains accepted by configuration, CLI, TUI, headless, desktop, and
ACP frontends. It is normalized by the compatibility adapter and then passed to
the resolver as preset input.

| Legacy preset | Guidance | Completion | Exposure |
| --- | --- | --- | --- |
| `full` / `balanced` | off | standard | eager |
| `economy` | off | standard | deferred |
| `delivery` | off | verified | eager |

`balanced` is an alias of `full`; the canonical persisted preset is `full`.
Existing aliases continue to normalize through `NormalizeTokenMode`.

Derived compatibility behavior must remain intact:

- economy core-tool filtering, deferred-source connector, MCP startup delay,
  and economy skill profile;
- economy planner suppression through the existing
  `effectivePlannerModel` path;
- delivery evidence/runtime marker and workspace write lease;
- delivery capability proxy and any existing delivery routing behavior.

Planner eligibility is not a completion consequence. A planner is selected only
when `agent.planner_model` is configured and the existing planner rules allow
it; economy may continue to suppress it for compatibility.

Selecting a legacy `/work-mode` preset clears advanced axis overrides by setting
all selections to `inherit`. Changing an advanced axis preserves the selected
preset as metadata but records the selection, so the effective policy is
predictable and inspectable.

## 9. Permissions, risk, and concurrency

Use the existing `internal/permission.Policy`, `permission.Subject`, Bash
decomposition, `tool.Tool.ReadOnly()`, MCP destructive metadata, and
`agent.Gate` interface. V5 explicitly rejects a string-matching `RiskDetector`:
it hardcodes tool names, mishandles shell syntax and JSON errors, has undefined
sticky-state lifetime, and risks races under concurrent calls while duplicating
structured permission data already in the codebase.

The resolver must be safe for concurrent calls because frontends may rebuild
policy while reads occur. It owns no mutable state. Session/run-loop mutation
continues to obey the existing Agent/session locking and single-writer
contracts.

## 10. Persistence and resume

`BranchMeta` currently stores legacy `TokenMode`. Add a versioned runtime-policy
selection or equivalent host metadata while retaining `TokenMode` for migration.
Persist both the request (including explicit axis overrides) and the resolved
policy, or enough versioned data to deterministically re-resolve it.

Required behavior:

- Old sessions with no new fields derive policy from `TokenMode`.
- Normal resume preserves the resolved policy and explicit overrides.
- `/model`, `/effort`, `/work-mode`, and axis changes re-resolve and update
  metadata before the next model request.
- Fork copies policy metadata unless the fork command explicitly changes it.
- Anything model-visible remains reconstructable from the append/replace event
  log; policy metadata must not be the sole source of a prompt fragment.
- Version migrations are deterministic and covered by round-trip tests.

`internal/store` remains the sole owner of persistence layout. Do not construct
session or sidecar paths in boot or runtime-policy code.

## 11. Error and compatibility handling

- Normalize case and surrounding whitespace for enum inputs.
- Reject unknown non-empty enum values with field-specific errors.
- Resolve typed selections ahead of legacy input. Concrete/`auto` selections
  win per axis; `inherit` takes the preset value; `/work-mode` intentionally
  resets all selections to `inherit`.
- Never silently enable an unimplemented policy value.
- Keep the old prompt bytes and tool surfaces unchanged for the three legacy
  presets until the characterization tests are green.

## 12. Acceptance contract

The implementation is complete only when focused tests prove:

- resolver purity, matrix values, explicit override precedence, and invalid
  value errors;
- provider/model-override TOML apply, render, clone, backfill, and round-trip;
- byte-stable legacy full/economy/delivery prompts;
- built-in tool names/order, skills, MCP startup, deferred connector, and
  delivery capability proxy;
- planner behavior remains controlled by `agent.planner_model`;
- completion-required tools are available in `verified + deferred`;
- BranchMeta/session resume, fork, and old-session migration;
- race-free concurrent resolver and frontend rebuild behavior.

The handoff verification commands are listed in
`.scratch/IMPLEMENTATION_PLAN_RUNTIME_POLICY_V5.md`.

## 13. Rejected approaches

- Keeping `TokenMode` as the semantic core: it preserves the current
  conflation and makes future combinations ambiguous.
- Heuristic capability detection from model IDs: provider naming changes and
  aliases make it non-deterministic and unreviewable.
- A free-form `RiskDetector`: it duplicates structured permission metadata and
  cannot safely parse arbitrary shell/tool payloads.
- Treating completion as approval or planner enablement: these are separate
  host responsibilities with different contracts.
- Big-bang removal of `TokenMode`: it needlessly breaks persisted sessions and
  frontends; migrate behind a compatibility adapter instead.
