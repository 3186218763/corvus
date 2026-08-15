# ADR-0001: Static single-binary build; code indexing stays pure Go

- Date: 2026-08-15
- Status: Accepted

## Context

Corvus ships one static executable, built with `CGO_ENABLED=0` (Makefile, CI, and
the released 25 MB binaries all assume this; dev machines may not even have a C
toolchain). Since the initial commit, a tree-sitter-backed symbol indexer lived
in `internal/tool/builtin/codeindex_treesitter.go` behind the
`//go:build treesitter && cgo` tag, alongside a stub for default builds. No
build entry ever set the tag: the tagged code (and its tagged tests) was never
compiled, never tested in CI, and never present in any artifact — while
`go.mod` permanently carried `go-tree-sitter` plus four language grammars (the
heaviest dependency family in the tree) and their upgrade churn.

The always-active path is `code_index` in `codeindex.go`: `go/parser` AST
extraction for Go files, regex-based extraction for everything else, positioned
as the local fallback below `lsp_*` tools and external code-graph MCP servers.

## Decision

1. `CGO_ENABLED=0` static single-binary distribution is a hard product
   constraint.
2. Code indexing stays pure Go. The tree-sitter files and their five direct
   dependencies are removed. Shipped behavior is unchanged (the stub was the
   live path).

## Consequences

- Any future cgo-bearing dependency (sqlite, tree-sitter, ...) must reopen this
  ADR and argue against constraint 1 first.
- Recovering the tree-sitter work is a `git revert` of the removal commit.

## Alternatives considered

- **Wire tree-sitter into the build** — rejected: requires a C toolchain on
  every dev/CI machine and forfeits static cross-compilation, for a feature the
  layered `code_index` / `lsp_*` / MCP story already covers.
- **Keep it dormant for later** — rejected: permanent `go.mod`/tidy/upgrade tax
  for code with zero lifetime executions.
