package fileutil

import (
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// MatchSlashGlob reports whether the slash-normalized path matches the
// slash-normalized doublestar pattern. A pattern starting with "**/" also
// matches at the root, so "**/*.go" matches "main.go".
func MatchSlashGlob(path, pattern string) bool {
	path = normalizeSlashPath(path)
	pattern = normalizeSlashPath(pattern)
	if matched, _ := doublestar.Match(pattern, path); matched {
		return true
	}
	if strings.HasPrefix(pattern, "**/") {
		matched, _ := doublestar.Match(strings.TrimPrefix(pattern, "**/"), path)
		return matched
	}
	return false
}

func normalizeSlashPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}
