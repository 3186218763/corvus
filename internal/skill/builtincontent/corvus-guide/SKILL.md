---
name: corvus-guide
description: "Troubleshoot and configure Corvus capabilities: Skills (project/custom/global/builtin priority, discovery dirs), Commands (override order, /dir:file naming), Hooks (11 events, automatic project loading, matchers, timeouts), MCP (.corvus/config.toml + .mcp.json + plugin packages, auto_start), plugin packages (native/Codex/Claude manifests), and AGENTS.md / instruction docs. Use when the user asks how to configure, debug missing skills/commands/hooks/MCP/plugins, or diagnose capability loading."
runAs: inline
---

# Corvus self-diagnostics guide

This skill is **inlined**. Prefer evidence over guessing.

## First action

The CLI ships no `doctor`/`setup` subcommands — only `help`/`version` plus the
session flags listed by `corvus --help`. Gather evidence directly from the
files the TUI reads:

1. Show session flags and startup behavior:

```bash
corvus --help
```

2. Inside the TUI, list session commands:

```text
/help
```

3. Read the config and skill files that drive capability loading (no network,
   no MCP subprocesses): `.corvus/config.toml` (project) and
   `~/.corvus/config.toml` (user-global), plus the skill/command roots under
   `<workspace>/{.corvus,.agents,.agent,.claude}/` and `<Corvus home>/`.

4. On desktop, open **Settings → Diagnostics** for a runtime report. The
   desktop "include current session runtime" toggle only **reads** the active
   tab Host (connected/failed/deferred/disabled); it does **not** start MCP.

Do not invent auto-fixes. Base conclusions on what is actually configured:
config files, skill/command/hook files on disk, and `/help` output.

---

## Skills

### Config sources and priority

Winner per skill name (highest first):

1. **project** — `<workspace>/{.corvus,.agents,.agent,.claude}/skills/`
2. **custom** — `[skills].paths` (and plugin package skill roots)
3. **global** — `<Corvus home>/skills` and home convention dirs
4. **builtin** — shipped skills (including this guide)

Same name: higher scope wins; lower scopes are **shadowed**. `[skills].disabled_skills` hides a name from List/Read entirely.

Discovery conventions: `.corvus`, `.agents`, `.agent`, `.claude` (see `config.ConventionDirs`). Layouts: `<name>/SKILL.md` or flat `<name>.md` (Claude flat files need skill frontmatter).

### Checks

| Entry | How |
| --- | --- |
| Config | Read `[skills]` in `.corvus/config.toml` and `~/.corvus/config.toml` (disabled_skills, paths) |
| Files | Inspect `<workspace>/{.corvus,.agents,.agent,.claude}/skills/` and `<Corvus home>/skills` |
| Desktop | Settings → Skills; Settings → Diagnostics |
| Agent | `/skill` list, `/corvus-guide`, `run_skill` |

### Symptom → cause → fix

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Skill missing from index | Disabled, shadowed, missing description, wrong root | Check report codes `skill.shadowed`, `skill.missing_description`, disabled list, discovery roots |
| Builtin overridden | Project/global same name | Rename or remove user skill; disable if intentional |
| Flat Claude file ignored | No skill frontmatter under `.claude/skills` | Add `description:` / `runAs:` frontmatter or use `SKILL.md` folder |
| Body never loads | Expected: bodies are on-demand | Invoke via `/name` or `run_skill` |

### Ordered triage

1. Read `[skills]` from `.corvus/config.toml` and `~/.corvus/config.toml`
2. Confirm name not in `disabled_skills`
3. Confirm winner Path/Scope from the discovery dirs; if shadowed, inspect lower-priority roots
4. Missing description: skill may load but index placeholder is weak — add `description:`
5. Reopen session / Refresh Skills after config changes

---

## Commands (slash templates)

### Priority

`config.CommandDirsForRoot`: home convention commands → Corvus home commands → project convention commands. **Later directory overrides earlier** on name clash (`command.Load`).

Name from path: `git/commit.md` → `/git:commit` (slashes → `:`).

### Checks

Invoke `/name` in chat; inspect `commands/` roots on disk; on desktop, Settings → Commands / Diagnostics → Commands.

### Symptom → cause → fix

| Symptom | Cause | Fix |
| --- | --- | --- |
| Wrong body | Shadowed by later dir | Check `command.shadowed` winners |
| Missing command | Wrong dir / extension | Place `*.md` under a scanned `commands/` root |
| Parse fail | Unreadable file | Fix permissions / encoding (`command.read_failed`) |

---

## Hooks

### Events (11)

`PreToolUse`, `PostToolUse`, `PermissionRequest`, `UserPromptSubmit`, `Stop`, `PostLLMCall`, `SessionStart`, `SessionEnd`, `SubagentStop`, `Notification`, `PreCompact`.

**Blocking** (exit 2 can gate the loop): `PreToolUse`, `UserPromptSubmit`. Others warn or contribute context only.

### Sources

- Project: `<workspace>/.corvus/settings.json` — loaded automatically
- Plugin packages: installed enabled packages
- Global: `<Corvus home>/settings.json` (always)

Match field is an **anchored** regex: `file` does **not** match `read_file`; use `.*file` or `*`. Timeout is **milliseconds** (defaults 5s gating / 30s other).

### Checks

`/hooks`, Settings → Hooks, Diagnostics → Hooks.

### Symptom → cause → fix

| Symptom | Cause | Fix |
| --- | --- | --- |
| Project hooks silent | Wrong workspace / restart required | Confirm the project path and restart Corvus after saving |
| Matcher never fires | Non-anchored assumption / bad regex | Fix match (`hook.invalid_matcher`) |
| Command missing | Empty command / missing context file | Fix settings entry |
| Malformed JSON | Invalid settings.json | Repair JSON (file yields no hooks, no crash) |

---

## MCP servers

### Merge order

`config.LoadForRoot` merges:

1. User/project TOML `[[plugins]]` (higher name wins vs later sources when already defined)
2. Project `.mcp.json` servers not already in TOML
3. Enabled **plugin packages** MCP (skipped if name already defined)

Transports: `stdio` (default), `http` / streamable-http, `sse`. `auto_start=false` skips startup; nil/true = automatic. Tier `eager` blocks boot handshake; empty/background connects without blocking chat.

Env/header values may contain secrets — diagnostics list **keys only**.

### Checks

| Mode | Behavior |
| --- | --- |
| Static review | Read `[[plugins]]` in `.corvus/config.toml` + `.mcp.json`: TOML validity, command path / URL shape, start intent — **no** subprocess |
| TUI session | Host via `boot.PluginSpecsForRoot` + `plugin.Start`; auto-start only; concurrency 4; always Close |
| Desktop runtime | Read active tab Host only |

### Symptom → cause → fix

| Symptom | Cause | Fix |
| --- | --- | --- |
| Not connected | `auto_start=false` or failed start | Enable / fix command/URL (`mcp.command_not_found`, `mcp.start_failed`) |
| No tools | Connected but empty tools/list | Server config or permissions (`mcp.no_tools`) |
| Wrong source | Shadowed by TOML vs `.mcp.json` vs package | Inspect report Source / package owner |
| Invalid transport | Bad `type` | Use stdio/http/sse (`mcp.invalid_transport`) |

---

## Plugin packages

### Manifests

- Native: `corvus-plugin.json`
- Codex: `.codex-plugin/plugin.json`
- Claude: `.claude-plugin/plugin.json` (+ limited Claude compatibility paths)

State: `<Corvus home>/plugin-packages.json`. Disabled packages do not contribute skills/hooks/MCP.

Unmapped Claude-only features may appear as compatibility warnings — Corvus does not invent support.

### Checks

Settings → Plugins, Diagnostics → Plugins; inspect `<Corvus home>/plugin-packages.json`
and the manifest files on disk.

### Symptom → cause → fix

| Symptom | Cause | Fix |
| --- | --- | --- |
| Package missing | Bad root path | Reinstall / fix root (`plugin.missing_root`) |
| Invalid manifest | Parse failure | Fix JSON/manifest (`plugin.invalid_manifest`) |
| Skills missing | Disabled package | Enable package |

---

## Instructions (AGENTS.md / CORVUS.md)

### Load order (ascending specificity)

User global docs → ancestor chain → project docs → project-local (`*.local.md`).

Recognized names: `CORVUS.md`, `AGENTS.md`, `CLAUDE.md` (and `*.local.md` variants). Multiple files in one directory can load; symlink identity is deduped.

Instructions fold into the system prompt at session boot (cache-stable prefix);
Hooks remain runtime event handlers loaded from their configured locations.

### Checks

Diagnostics → Instructions; memory Settings; read files on disk.

### Symptom → cause → fix

| Symptom | Cause | Fix |
| --- | --- | --- |
| Guidance ignored | Wrong filename / empty file | Use recognized names under correct dir |
| Wrong scope won | Local override | Check load order in report |

---

## Desktop Diagnostics page

- Static report on open; Refresh re-runs static collect
- Copy redacted JSON
- Optional session runtime merge (read-only Host)
- Jump to Settings for MCP / Skills / Plugins / Hooks when issue `settings_tab` is set
- **Never** auto-edit config, execute hooks, or auto-reconnect from this page

---

## Safety

- Prefer static diagnostics
- Live MCP may run third-party code and network
- Do not print tokens, header values, env values, URL query strings, usernames, or machine-absolute external paths
- Report paths as `<workspace>/…`, `~/…`, or `<external>/…`
