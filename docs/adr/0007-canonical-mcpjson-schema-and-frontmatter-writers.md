# ADR-0007: Canonical .mcp.json wire schema (internal/mcpjson); one frontmatter writer

- Date: 2026-08-16
- Status: Accepted

## Context

The audit (`.scratch/dep-audit/FINDINGS.md` B6) found five (later six) parsers
of an `mcpServers` map, each defining its own anonymous struct with a drifting
field set:

- `internal/config/mcpjson.go` (`loadMCPJSON`): dropped `tier` on the floor —
  invisible to the main config reader.
- `internal/installsource/mcp.go` (`parseMCPJSON`): full field set, own copies
  of the tier/transport alias tables.
- `internal/pluginpkg/pluginpkg.go` (`MCPServer`): **no timeout fields at all**.
- `internal/pluginpkg/claude_compat.go`: decoded 7 fields, then **forced
  `autoStart = false`** and discarded any timeouts a Claude-format manifest
  carried.
- `internal/config/plugin_packages.go`: copied 9 fields from `MCPServer` into
  `PluginEntry` — the drop point where a package manifest's timeouts became
  zeros and the boot chain silently fell back to 30s/300s defaults.
- `internal/config/ccswitch.go`: its own `mcpServerSpec` usage.

Net effect: a user who set `startup_timeout_seconds: 5` in a plugin package
manifest got 30s, and `auto_start: true` was ignored on Claude-format imports —
each parser was "correct" in isolation.

Separately, frontmatter had one reader (`frontmatter.Split`, permissive),
one typed decoder (`Decode`, one caller, which then re-parsed with `Split`),
a second private fence scanner in `internal/skill/skill.go`, and three
hand-rolled writers (memory `render`, `RenderSkillFile`, skill stub) each
building `---` fences by string concatenation.

## Decision

1. **`internal/mcpjson` is the one wire schema.** `ServerSpec` carries the
   Claude field set plus the Corvus policy extensions (three timeouts, tier,
   auto_start, title/description); `Document`/`Parse` decode a document;
   `SortedNames` gives a stable order; `NormalizeType`/`NormalizeTier` are the
   single alias tables (streamable-http→http, lazy→background).
2. **Every mcpServers reader decodes through it.** config, installsource,
   pluginpkg (native manifests embed `ServerSpec` in `MCPServer`), and
   claude_compat all parse via `mcpjson.Parse` (or embed the struct) and map
   into their own internal types. Consumers keep their own policy — strict
   transport rejection stays in installsource, tolerance stays in config.
3. **The formerly dropped fields now flow.** Package imports copy the three
   timeout fields into `PluginEntry`; Claude-format imports honor an explicit
   `auto_start` (absent still means false for imported third-party servers);
   tier decodes everywhere. Tier remains a retired user-facing setting:
   `normalizeLegacyMCPTiers` erases it from all sources in exactly one place
   at load (load.go) — parsers decode faithfully, policy lives in one site.
4. **One frontmatter writer.** `frontmatter.Encode(frontmatter, body)` renders
   the `---` fence + YAML (indent 2) + right-trimmed body; memory `render`,
   `RenderSkillFile`, and the `/skill new` stub all use it. Output is
   byte-identical to the previous writers for their flat structs. `Raw`
   exports the CRLF-normalizing fence scan (replacing skill.go's private
   copy, which missed CRLF files); `ParseError` gives strict YAML validation
   (replacing `Decode`'s one use in installsource, which re-parsed anyway).

## Consequences

- Adding a field to the wire format is now a one-struct change; a reader can
  no longer silently drop it.
- `ServerSpec` fields are `omitempty` so embedding it in `MCPServer` keeps
  marshaled manifests compact; decoding is unaffected.
- `claudeMCPIdentity` (dedup key for imported servers) intentionally excludes
  timeouts/tier — they are policy, not identity.
- RenderSkillFile golden tests pass unchanged: its struct is flat (scalars +
  one flow sequence), so the previous default-indent-4 marshal and the new
  indent-2 encode produce identical bytes.
