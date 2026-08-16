package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"corvus/internal/fileutil"
	"corvus/internal/secrets"
	"corvus/internal/tool"
)

func init() { tool.RegisterBuiltin(globTool{}) }

// globTool matches files by pattern. workDir, when non-empty, is the directory
// a relative pattern resolves against (see resolveIn). forbidRoots lists
// directories the tool may not search inside.
type globTool struct {
	workDir     string
	forbidRoots []string
}

func (globTool) Name() string { return "glob" }

func (globTool) Description() string {
	return "Find files matching a glob pattern (e.g. \"*.go\", \"internal/*/*.go\", \"**/*.test.ts\"). Supports shell metacharacters * ? [] and the recursive ** pattern. Like grep, results skip hidden, vendor, and git-ignored entries — point the pattern straight at a directory (e.g. \".github/**\") to search it in full."
}

func (globTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern (supports ** for recursive matching)"}},"required":["pattern"]}`)
}

func (globTool) ReadOnly() bool { return true }

// SnipHint keeps a long head and short tail like grep: the first paths matter
// most, the tail confirms how many more there were.
func (globTool) SnipHint() tool.SnipHint {
	return tool.SnipHint{Head: 80, Tail: 8, HeadChars: 10000, TailChars: 1000}
}

const globMaxResults = 1000

func (g globTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Pattern string `json:"pattern"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	if p.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	// Save the original pattern before resolveIn prepends workDir, so the
	// simple-filename recursive-fallback check below works on the raw input
	// — not the already-joined absolute path that always contains separators.
	rawPattern := p.Pattern
	p.Pattern = resolveIn(g.workDir, p.Pattern)
	p.Pattern = filepath.FromSlash(p.Pattern) // models emit "/" (see Description); WalkDir/Match compare OS-native paths
	displayPattern := p.Pattern

	// If the pattern contains **, use recursive matching via doublestar semantics
	// while retaining Corvus's cancellation and read-forbid pruning.
	if strings.Contains(p.Pattern, "**") {
		return g.globRecursive(ctx, p.Pattern, displayPattern)
	}

	// For patterns without **, try filepath.Glob first. If no matches are
	// found and the pattern is a simple filename (no path separator), retry
	// with a recursive walk (equivalent to "**/<pattern>") so the tool finds
	// files anywhere in the tree — the common case where the model only knows
	// a filename but not its exact location. Uses the raw pattern (before
	// resolveIn) so a workspace root doesn't mask a simple "*.go".
	matches, err := filepath.Glob(p.Pattern)
	if err != nil {
		return "", fmt.Errorf("glob %q: %w", displayPattern, err)
	}
	matches = filterForbidMatches(matches, g.forbidRoots)
	if len(matches) == 0 && !strings.ContainsAny(rawPattern, "/\\") {
		fallback := filepath.Join(g.workDir, "**", rawPattern)
		return g.globRecursive(ctx, fallback, fallback)
	}
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	if len(matches) > globMaxResults {
		matches = matches[:globMaxResults]
		return strings.Join(matches, "\n") + fmt.Sprintf("\n... (truncated at %d results)", globMaxResults), nil
	}
	return strings.Join(matches, "\n"), nil
}

func filterForbidMatches(matches, forbidRoots []string) []string {
	if len(matches) == 0 || (len(forbidRoots) == 0 && !secrets.ProtectSensitiveFiles()) {
		return matches
	}
	out := matches[:0]
	for _, match := range matches {
		if !confineRead(forbidRoots, match) {
			out = append(out, match)
		}
	}
	return out
}

// globRecursive handles patterns containing ** by walking the stable non-meta
// prefix and matching relative paths with doublestar. Accepts a context so the
// walk can be interrupted on cancellation.
func (g globTool) globRecursive(ctx context.Context, pattern, displayPattern string) (string, error) {
	rootSlash, relPattern := doublestar.SplitPattern(filepath.ToSlash(filepath.Clean(pattern)))
	root := filepath.FromSlash(rootSlash)
	if relPattern == "" {
		relPattern = "**"
	}

	// Check root exists.
	if info, err := os.Stat(root); err != nil {
		return "", fmt.Errorf("glob %q: %w", displayPattern, err)
	} else if !info.IsDir() {
		return "(no matches)", nil
	}

	// A forbidden read root stays invisible even when the walk points straight
	// at it, mirroring the non-recursive path's match filtering.
	if skipForbidDir(root, g.forbidRoots) {
		return "(no matches)", nil
	}

	// Prune exactly like grep's walk (ADR-0003): hidden entries, the shared
	// noise-dir table, and the repository's ignore rules are skipped — unless
	// the walk root is itself hidden or ignored, which searches it in full.
	ig := newWalkIgnorer(root, g.forbidRoots)

	var matches []string
	truncated := false

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err() // abort promptly on cancel — a huge tree is interruptible
		}
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if ig.skip(path, d.Name(), true) {
				return filepath.SkipDir
			}
			ig.enter(path)
			return nil
		}
		if ig.skip(path, d.Name(), false) {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		if matchGlobPattern(filepath.ToSlash(rel), relPattern) {
			matches = append(matches, path)
		}
		if len(matches) >= globMaxResults {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("glob %q: %w", displayPattern, err)
	}

	if len(matches) == 0 {
		return "(no matches)", nil
	}
	sort.Strings(matches)
	result := strings.Join(matches, "\n")
	if truncated {
		result += fmt.Sprintf("\n... (truncated at %d results)", globMaxResults)
	}
	return result, nil
}

func matchGlobPattern(path, pattern string) bool {
	return fileutil.MatchSlashGlob(path, pattern)
}
