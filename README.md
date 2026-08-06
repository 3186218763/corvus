# Corvus

Corvus is a local terminal UI for an AI coding agent. This repository ships
one executable and one interaction surface: the Bubble Tea TUI.

## Build

```sh
go build -o corvus ./cmd/corvus
```

## Configure

Copy the example configuration, then set a provider API key. Resolution order is
project `.env` first, then Corvus's credentials store (`corvus setup` /
user config dir). The first TUI launch can also guide provider setup when no
usable model is configured.

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

## Development

```sh
make fmt
make test
make build
```
