# ADR-0012: Resolve runtime policy across orthogonal guidance, completion, and exposure axes

**Date**: 2026-08-18

**Status**: Accepted

## Context

Corvus used `TokenMode` work modes (`full`, `balanced`, `economy`, and
`delivery`) as a compact switch for unrelated behaviors: prompt scaffolding,
tool exposure and startup timing, delivery evidence gates, workspace write
coordination, skill profiles, and planner/capability routing. This made
combinations hard to reason about and prevented model capability/effort from
being expressed independently.

The repository already has structured permission, planner, tool capability, and
session event-log contracts. A new runtime abstraction must preserve those
contracts and remain safe under the Agent's concurrency model.

## Decision

1. Add a deep `internal/runtimepolicy` module with a pure deterministic
   `Resolve(Input) (Policy, error)` seam. Boot and frontends adapt resolved
   policy to existing services; they do not own policy semantics.
2. Make the user-selectable axes explicit:
   - `Guidance`: `off`, `light`, `structured`;
   - `Completion`: `standard`, `verified`;
   - `Exposure`: `eager`, `deferred`.
   Request-time `auto` is allowed, but resolved policy values are never
   ambiguous.
3. Keep permissions, planner selection, and capability routing independent.
   Completion does not grant approval or enable a planner. Planner selection is
   controlled only by `agent.planner_model`; exposure does not suppress it.
4. Add explicit model capability metadata (`auto`, `strong`, `standard`,
   `lite`) at provider and model-override levels. Resolve effort with the
   existing provider-specific effort machinery; never infer capability from a
   model ID.
5. Remove work-mode semantics. New sessions have no preset and default to
   `guidance=auto`, `completion=standard`, and `exposure=eager`. `delivery` is
   represented by `completion=verified`; deferred loading is represented by
   `exposure=deferred`.
6. Persist versioned runtime-policy selections. Keep the old `TokenMode` field
   only while reading/migrating old sessions so they remain loadable; clear it
   on the first canonical save and never expose a work-mode command or flag.
   Keep `internal/store` as the persistence-layout owner and the event log as
   the source from which model-visible state can be reconstructed.
7. Represent completion-required tools with optional `internal/tool.Tool`
   capability interfaces. Do not introduce a string-matching risk detector or
   hardcode tool names in the scheduler.

The normative details and acceptance contract live in
[`.scratch/RUNTIME_POLICY_SPEC_V5.md`](../../.scratch/RUNTIME_POLICY_SPEC_V5.md).

## Consequences

Positive:

- Policy combinations are explicit, testable, and explainable to users and
  frontends.
- Strong/standard/lite models can receive appropriate guidance without changing
  host safety or completion guarantees.
- Legacy session metadata remains readable while new combinations are explicit.
- The resolver is independently testable and has no side effects or scheduler
  coupling.

Costs and risks:

- Boot temporarily carries a migration adapter and derived compatibility fields.
- Persisted metadata needs a version and migration tests.
- More combinations require a characterization matrix, especially
  `verified + deferred`.
- Config schemas and frontends must agree on explicit enum errors and override
  precedence.

## Rejected alternatives

- **Keep work modes as the core model**: leaves independent concerns coupled.
- **Infer capability from model names**: fragile across providers and aliases.
- **Add a string-based `RiskDetector`**: duplicates structured permission and
  tool metadata, with parsing and concurrency hazards.
- **Use completion/assurance as permission or planner control**: violates the
  existing separation of host responsibilities.
- **Delete the migration reader**: breaks persisted sessions without improving
  the new runtime model.
