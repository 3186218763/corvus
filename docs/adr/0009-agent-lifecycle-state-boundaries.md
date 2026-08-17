# ADR-0009: Separate Agent runtime, session, and delivery-scope state

## Status

Accepted (2026-08-17).

## Context

`Agent` is the sequential run-loop spine, but it also serves as the construction
boundary for provider configuration, the owner of a session, and the host-side
Delivery contract. Those concerns have different lifetimes:

- provider, tool, sandbox, and compaction wiring are immutable runtime state;
- the session, canonical todo state, and cache identity belong to one
  conversation/session and may survive a turn;
- acceptance-criteria expectations, evidence preservation, capability-route
  outcomes, review warnings, recovery readiness, and the Delivery checkpoint
  belong to one Delivery scope or one user turn;
- Controller/frontend state owns admission, prompts, mode changes, and Goal
  sidecar persistence.

Keeping all of these fields directly on `Agent` made it easy for a new run to
inherit a stale turn flag or for controller-facing checkpoint state to be
treated like ordinary execution state. It also made the run loop an implicit
policy owner instead of a small state-machine spine.

## Decision

1. `Agent` remains the owner of provider execution and the only writer of its
   active `Session` during a run. Frontends read session state through the
   existing snapshot boundary; controller admission and Goal persistence stay
   outside the run loop.
2. Construction-time runtime wiring stays on `Agent` and is not copied into
   turn state. This includes the provider, tool registry, permission/recovery
   hooks, workspace lease, mutation observer, job manager, and the isolated
   compaction summarizer.
3. `deliverySupervisor` is the single internal owner for Delivery-scope and
   turn-scoped mutable state: scope identity/activation, checkpoint, per-turn
   expectations, one-shot evidence preservation, recovery-pending state,
   review warnings, and capability ledger/audit/gate markers. `beginRunTurn`
   reinitializes the per-turn portion through the supervisor before classifying
   the current task.
4. Agent methods may expose narrow lifecycle operations such as
   `DeliveryCheckpoint`, `RestoreDeliveryCheckpoint`, and
   `PrepareDeliveryRecovery`, but callers do not construct or mutate delivery
   paths themselves. The supervisor is internal; it is not a second public
   controller API.
5. Delivery state is host state, not provider-visible prompt state and not
   session event-log content. Evidence and model-visible facts continue to be
   recorded through the existing event/evidence mechanisms, while only the
   compact, persistence-safe checkpoint crosses the Controller/Goal boundary.
6. Do not introduce SQLite, a daemon, or a general-purpose state store as part
   of this split. Extract another seam only when its lifetime and write owner
   are clear (for example, a future delivery supervisor); keep the run loop
   sequential and preserve the existing concurrency contracts.

## Consequences

- A turn cannot accidentally retain delivery expectations, capability gate
  markers, or review warnings merely because an unrelated `Agent` field was
  not reset.
- Goal continuation still preserves only the compact checkpoint and the
  explicitly requested recovery evidence; ordinary follow-up turns reset
  evidence as before.
- The supervisor currently owns state and lifecycle transitions while Agent
  still performs evidence recording and tool execution. Further extraction is
  allowed, but must preserve the run loop's single-writer rule and the stable
  resolved-call boundary.
- Tests that need to seed host state should use the narrow Agent lifecycle
  methods (or package-local supervisor access for focused unit tests), rather
  than recreating removed Agent fields.
