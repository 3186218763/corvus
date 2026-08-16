# ADR-0008: Provider `base_url` is user-owned configuration

## Status

Accepted (2026-08-16). Supersedes the commented-out rewrite in
`internal/config/load.go` (`normalizeLegacyStepFunBaseURLs`).

## Context

StepFun historically served both `api.stepfun.ai` (global) and
`api.stepfun.com` (China) as official endpoints. Earlier Corvus builds carried a
loader that inferred the "right" region and rewrote a provider's `base_url`
during load or during an unrelated settings save. The rewrite was later
disabled by a function whose body was a literal `return false` plus a comment
explaining why — leaving four constants, two pipeline call sites, and tests
carrying a decision that lived only in that comment.

## Decision

A provider's `base_url` is user-owned configuration. Neither runtime loading
nor an unrelated settings save may infer a region or otherwise rewrite it. Both
StepFun domains are official endpoints; whichever the user configured is the
one Corvus must keep using, byte-for-byte, through load, edit, and save.

## Consequences

- The no-op `normalizeLegacyStepFunBaseURLs` and its four constants are
  deleted; the two legacy-URL constants survive only as fixtures in the
  round-trip test that pins this decision
  (`TestLoadAndSavePreserveStepFunRegionalBaseURLs`).
- New provider migrations that want to touch `base_url` need an explicit user
  consent flow, not a silent normalize step.
