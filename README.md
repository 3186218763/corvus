# Corvus

Corvus is a local terminal UI for an AI coding agent. This repository ships
one executable and one interaction surface: the Bubble Tea TUI.

## Build

```sh
go build -o corvus ./cmd/corvus
```

## Configure

Configuration lives in `.corvus/config.toml`:

- User/global: `~/.corvus/config.toml` (highest priority)
- Project: `<project root>/.corvus/config.toml` (the old `./corvus.toml` layout was removed)

Set the API key directly with `api_key` on a `[[providers]]` entry (see
`corvus.example.toml`) instead of environment variables; the user
`~/.corvus/config.toml` wins over the project config. The first TUI launch can
also guide provider setup when no usable model is configured.

```sh
corvus
```

The TUI supports session recovery, model selection, local workspace tools,
permissions, MCP servers, skills, and project memory. Run `corvus --help` in
an interactive terminal to view its session flags.

TUI environment variables:
- `CORVUS_REDUCE_MOTION=1` — disable decorative animation (spinner motion,
  smooth scroll, tool frame cycling). Elapsed counters still tick.
- `CORVUS_TUI_SCROLL_REPAINT=1` — legacy full-screen repaint on every scroll;
  only for terminals that strand stale rows under the cell-diff renderer
  (disables smooth scroll).

## MCP server

`cmd/corvus-mcp-server` exposes Corvus's built-in tools to MCP hosts (IDEs,
editors, and other Model Context Protocol clients) over the stdio transport
(newline-delimited JSON-RPC 2.0, protocol version 2024-11-05). Point an MCP
client at the binary with:

```sh
go build -o corvus-mcp-server ./cmd/corvus-mcp-server
# MCP client command:
corvus-mcp-server --dir /path/to/project
```

Flags: `--dir` (workspace root; defaults to the current directory), `--allow-write`
(register the writer tools and a workspace-confined `bash`; every call still goes
through the permission policy), `--permission-mode dontAsk|ask|auto|yolo`
(default `dontAsk`), `--version`, `--help`. The default tool set is read-only and
fail-closed: read/list/search tools bound to the workspace plus SSRF-guarded
`web_fetch`. `ask` degrades to `dontAsk` in this headless server (no interactive
approver), and deny rules always win, including in `yolo` mode.

## Development

```sh
make fmt
make test
make build
```
