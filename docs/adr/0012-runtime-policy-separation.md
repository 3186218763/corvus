# ADR-0012: Resolve runtime policy across orthogonal guidance, completion, and exposure axes

**Date**: 2026-08-18

**Status**: Accepted

## Context

Corvus's `TokenMode` (`full`, `economy`, and `delivery`) is used as a compact
switch for several unrelated behaviors: prompt scaffolding, tool exposure and
startup timing, delivery evidence gates, workspace write coordination, skill
profiles, and some planner/capability routing. This makes combinations hard to
reason about. In particular, model capability and effort cannot be expressed
without accidentally changing completion or permissions.

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
   Completion does not grant approval or enable a planner. Existing planner
   selection through `agent.planner_model` and economy suppression remain
   compatibility behavior.
4. Add explicit model capability metadata (`auto`, `strong`, `standard`,
   `lite`) at provider and model-override levels. Resolve effort with the
   existing provider-specific effort machinery; never infer capability from a
   model ID.
5. Preserve legacy preset behavior with a compatibility adapter:
   `full/balanced = off + standard + eager`, `economy = off + standard +
   deferred`, and `delivery = off + verified + eager`, including their existing
   prompts, tool surfaces, evidence gates, workspace lease, skill filtering,
   deferred sources, and capability proxy.
6. Persist versioned runtime-policy selection/resolution alongside legacy
   `TokenMode` and migrate old sessions deterministically. Keep
   `internal/store` as the persistence-layout owner and the event log as the
   source from which model-visible state can be reconstructed.
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
- Legacy sessions and commands continue to work while migration proceeds.
- The resolver is independently testable and has no side effects or scheduler
  coupling.

Costs and risks:

- Boot temporarily carries an adapter and derived compatibility fields.
- Persisted metadata needs a version and migration tests.
- More combinations require a characterization matrix, especially
  `verified + deferred`.
- Config schemas and frontends must agree on explicit enum errors and override
  precedence.

## Rejected alternatives

- **Keep `TokenMode` as the core model**: leaves independent concerns coupled.
- **Infer capability from model names**: fragile across providers and aliases.
- **Add a string-based `RiskDetector`**: duplicates structured permission and
  tool metadata, with parsing and concurrency hazards.
- **Use completion/assurance as permission or planner control**: violates the
  existing separation of host responsibilities.
- **Remove legacy modes in one release**: breaks persisted sessions and
  non-CLI frontends without improving the migration path.
