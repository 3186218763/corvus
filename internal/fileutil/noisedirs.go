package fileutil

// noiseDirNames are the directory names every recursive walk in Corvus prunes:
// VCS internals, dependency trees, and language caches that dominate walk time
// but almost never hold wanted results (node_modules alone can be 100k+ files).
// This is the single shared table (ADR-0003) — tools with stricter needs layer
// their own entries on top (code_index also skips build outputs; fileref also
// skips build/dist); none narrow it. Build outputs (dist, target, ...) are
// deliberately NOT here: grep mirrors ripgrep, which searches build trees that
// are not git-ignored.
var noiseDirNames = map[string]bool{
	".git": true, ".svn": true, ".hg": true, ".jj": true,
	"node_modules": true, "vendor": true, ".venv": true,
	".npm": true, ".pnpm-store": true,
	"__pycache__": true, ".mypy_cache": true, ".pytest_cache": true,
}

// IsNoiseDir reports whether name is a directory name recursive walks prune
// before descending (see noiseDirNames).
func IsNoiseDir(name string) bool {
	return noiseDirNames[name]
}
