# Runtime Policy Specification V5

> Status: **Authoritative design** (2026-08-18). This document supersedes
> `SCAFFOLDING_DESIGN_V4_FINAL.md`, `IMPLEMENTATION_PLAN_RUNTIME_MODE.md`, and
> `runtime-mode-refactor-plan.md`. Implementers must follow V5 when a draft
> conflicts with it.

## 1. Purpose

Corvus now resolves runtime behavior from three independent axes. V5 introduces
a small, pure runtime-policy resolver; the old `TokenMode`/work-mode values are
accepted only when reading older sessions and are never the semantic source for
a new session.

The design is deliberately host-oriented: the host decides what the model may
do, what counts as completion, and which capabilities are initially available.
Guidance is only a model-visible hint and must not silently weaken host safety.

## 2. Goals and non-goals

### Goals

- Resolve a deterministic policy from explicit axis requests, model capability
  metadata, and effective provider effort.
- Read old work-mode metadata deterministically without carrying it into new
  runtime selections.
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
    Preset     Preset // deprecated, migration-only metadata
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

The boot adapter fills omitted selections with the new-session defaults:
`guidance=auto`, `completion=standard`, and `exposure=eager`. `inherit` is
accepted only when replaying migration metadata. A resolved `Policy` never
contains `inherit`, `auto`, or an empty enum. Unknown values are errors, not
silent fallbacks.

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
effective effort already selected by `config.EffectiveEffort(entry)`. A legacy
preset may be present only while reading migration metadata; the resolver must
not inspect tool names or parse shell text.

Resolution order for each axis is normative:

1. An explicit concrete selection wins.
2. `auto` uses the axis's automatic default.
3. `inherit` uses migration metadata when present; otherwise it is equivalent
   to the new-session default for that axis.

There is no default work-mode preset. Frontends expose only the three axis
selections. An old `TokenMode` value may be read to reconstruct a legacy
session, then is written back only as a migration field.

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

Calling `/runtime-policy` without arguments displays the selections and
resolved axes. `/work-mode`, `/profile`, and `--profile` are removed; there is
no runtime compatibility command. Compatibility is limited to persisted
session metadata migration.

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
for `guidance=auto`; `guidance=inherit` uses migration metadata when present.
Columns are
effort bands after provider normalization.

| Capability | unknown | disabled | low | medium | high/xhigh | max |
| --- | --- | --- | --- | --- | --- | --- |
| strong | light | structured | off | off | off | off |
| standard | light | structured | light | light | light | light |
| lite | structured | structured | structured | structured | structured | structured |

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

`eager` starts the configured tool surface normally. `deferred` keeps the core
built-ins first, represents optional sources through the existing connector,
and loads MCP/skill/catalog sources on demand. This is an exposure choice, not
an economy work mode.

Exposure controls visibility and startup timing only. It does not alter
permissions, sandboxing, tool argument validation, or completion evidence.

The prompt fragment order is deterministic and must be:

1. Guidance fragment (if any)
2. Completion fragment (if any)
3. Exposure fragment (if any)

The combined `verified + deferred` case is supported. Deferred selection must
still expose the minimum capabilities required to satisfy verified completion.

## 8. Migration metadata

`TokenMode` and the old `full`, `balanced`, `economy`, and `delivery` strings
are not runtime policy values anymore. The persistence reader accepts them so
old sessions remain loadable. Migration maps only the behavior that cannot be
represented otherwise: legacy `delivery` becomes `completion=verified` and
legacy `economy` becomes `exposure=deferred`. Legacy `full`/`balanced` become
the ordinary new defaults.

Planner selection remains controlled only by `agent.planner_model`. Completion
does not enable a planner, and exposure does not change permissions. New
sessions never persist a preset; they persist the typed axis selections.

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

`BranchMeta` may still contain legacy `TokenMode` while it is being read, but
the versioned runtime-policy record is authoritative for new sessions and the
legacy field is cleared on the first canonical save.
Persist both the request (including explicit axis overrides) and the resolved
policy, or enough versioned data to deterministically re-resolve it.

Required behavior:

- Old sessions with no new fields translate `TokenMode` once using the migration
  mapping above.
- Normal resume preserves the resolved policy and explicit overrides.
- `/model`, `/effort`, and axis changes re-resolve and update
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
  win per axis; `inherit` uses migration metadata only when present.
- Never silently enable an unimplemented policy value.
- Keep old session files readable and deterministic; new sessions must not
  acquire work-mode-specific prompt or tool behavior.

## 12. Acceptance contract

The implementation is complete only when focused tests prove:

- resolver purity, matrix values, explicit override precedence, and invalid
  value errors;
- provider/model-override TOML apply, render, clone, backfill, and round-trip;
- old-session migration for legacy work-mode metadata;
- built-in tool names/order, skills, MCP startup, deferred connector, and
  delivery capability proxy;
- planner behavior remains controlled by `agent.planner_model`;
- completion-required tools are available in `verified + deferred`;
- BranchMeta/session resume, fork, and old-session migration;
- race-free concurrent resolver and frontend rebuild behavior.

The handoff verification commands are listed in
`.scratch/IMPLEMENTATION_PLAN_RUNTIME_POLICY_V5.md`.

## 13. Rejected approaches

- Keeping work modes as the semantic core: it preserves the current conflation
  and makes future combinations ambiguous.
- Heuristic capability detection from model IDs: provider naming changes and
  aliases make it non-deterministic and unreviewable.
- A free-form `RiskDetector`: it duplicates structured permission metadata and
  cannot safely parse arbitrary shell/tool payloads.
- Treating completion as approval or planner enablement: these are separate
  host responsibilities with different contracts.
- Deleting the migration reader: it would make persisted sessions unreadable;
  retain the field as data compatibility, not runtime semantics.
