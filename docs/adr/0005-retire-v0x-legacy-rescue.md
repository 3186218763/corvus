# ADR-0005: Retire the v0.x (TypeScript-era) legacy rescue paths

- Date: 2026-08-16
- Status: Accepted

## Context

Corvus grew from a TypeScript-era product ("v0.x", config in
`~/.corvus/config.json`, credentials partly in the OS keyring, sessions in a
different event-log layout). The Go rewrite carried a rescue layer so an
upgrading v0.x install kept working: boot-time one-shot imports
(`config.MigrateLegacyIfNeeded*`), a `/migrate` rescue command
(`internal/migration`, 872 lines), v0.x session/memory importers
(`agent/migrate.go`, 992 lines), keyring credential reads
(`zalando/go-keyring`, read-only), and a lowest-priority read of the legacy
config.json on **every** config load.

The rescue layer was also a drift source: the same legacy mcpServers were
parsed by two different structs with different field handling — `loadLegacyMCP`
kept the Corvus timeout fields but ignored `transport`/`disabled`, while
`legacyPlugins` honored transport/disabled and dropped every timeout.

The premise for retirement: the v0.x population is the maintainer's own
machines, all of which already run the current layout (`~/.corvus/config.toml`
present, no `~/.corvus/config.json`, no keyring-stored credentials — and the
keyring path was build-tag-excluded on Linux anyway). No other users hold
v0.x data that still needs rescuing at boot.

## Decision

1. Delete the rescue layer wholesale: `internal/migration`, the `/migrate`
   command (TUI + control + i18n strings), `agent/migrate.go`'s v0.x session
   importers, the keyring credential lookup (and with it the
   `zalando/go-keyring` dependency plus its indirect `wincred`/`dbus`), the
   v0.x `mcp`/`mcpServers`/`mcpEnv`/`mcpDisabled` parsing, and the
   `MigrateLegacyIfNeeded*` one-shot config import.
2. The v0.x config.json is no longer read, written, locked, or listed as a
   Corvus-managed config file. Removing a plugin no longer writes a
   compatibility `mcpDisabled` marker into it.
3. Kept, because they are current-generation behavior, not v0.x rescue:
   - Reading a config.toml from the older OS-support/XDG locations when the
     primary config is absent (`userConfigLoadPath` fallback) — read-only
     compatibility, no copying.
   - The v1.9.1 MCP backfill (`MigrateMCPToUserConfigOnUpgrade`), minus its
     v0.x config.json source: it lifts servers from legacy TOML locations and
     project roots into the user-global config, gated by its marker file.
   - Current-config housekeeping migrations (retired-key removal such as
     step limits / redact_tool_output / memory_compiler).
   - `parseLegacyMCPSpec`: the `name=cmd args` string format is still current
     UX for adding plugins, independent of the v0.x file.
4. `MCPSourceLegacyUser` stays as a plugin source value: TOML written by past
     migrations can still carry it; it just no longer resolves to a live file
     (source→path lookup returns "").

## Consequences

- go.mod loses one direct dependency (go-keyring) and two indirect
  (wincred, godbus/dbus).
- A machine that still had un-migrated v0.x data and upgrades past this point
  keeps that data untouched on disk but unread: recovery is manual. That is
  the accepted trade for deleting ~2,000 lines of dead rescue code and the
  legacy double-parse drift.
- The config-load path no longer stats the legacy file on every boot.
