# ADR-0003: One walk-pruning semantics for grep/glob/ls; one shared noise table

- Date: 2026-08-16
- Status: Accepted

## Context

The file-facing tools each decided for themselves what a recursive walk skips:

- grep pruned via `walkIgnorer` (ripgrep-style: hidden entries, a vendor-dir
  table, and all applicable git ignore rules).
- glob pruned via its own `skipWalkDir` (vendor table only — hidden and
  git-ignored files were returned).
- `ls -R` pruned via a third, shorter hardcoded list.
- code_index and the fileref @-menu kept two more private tables, mutually
  inconsistent; fileref's also carried three paths (`desktop/frontend/wailsjs`,
  `site/.astro`, `npm/.stage`) inherited verbatim from the original project's
  layout in the initial commit.
- `fileutil.GlobSet` advertised itself as the centralization layer and had zero
  non-test callers.

The inconsistency was also an exposure: `glob **/*.env*` surfaces files the
user git-ignored, while grep deliberately does not.

## Decision

1. grep, glob (`**` walks), and `ls -R` prune identically, via the one
   `walkIgnorer`: hidden entries, the shared noise-dir table, forbid-read
   roots, and git ignore rules (every applicable .gitignore plus
   .git/info/exclude and the global excludes file). A walk rooted directly at
   a hidden or ignored directory searches it in full — ripgrep's
   explicitly-named-path semantics.
2. The noise-dir table lives once, as `fileutil.IsNoiseDir` (VCS internals,
   dependency trees, language caches). Tools layer stricter entries on top
   (code_index adds build outputs/IDE dirs; fileref adds build/dist); none
   narrow it. Build outputs stay out of the shared table because grep
   deliberately mirrors ripgrep, which searches non-ignored build trees.
3. Non-`**` glob patterns (exact paths like `dir/file.*`) are explicit paths
   and are not ignore-filtered, consistent with rule 1.
4. `permission.matchGlob`, skill/task `path.Match` are **not** part of this
   family and are not unified with doublestar: they match command prefixes
   and tool *names*, where `*` crossing `/` (permission) or not (names) is
   the documented contract. Boundary comments mark each site.
5. `GlobSet` is deleted; `fileutil.MatchSlashGlob` is the surviving helper.

## Consequences

- `glob **/…` and `ls -R` stop returning hidden and git-ignored entries — a
  visible behavior change (e.g. agent scratch dirs like `.scratch/` need an
  explicit pattern). Tool descriptions state this.
- The three foreign-project paths in fileref are gone; nothing in this repo
  or its origin layout is referenced.
- Adding a new skip rule to "all tools" means editing `noiseDirNames` once;
  per-tool additions must be justified locally in a comment.
