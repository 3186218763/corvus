package fileutil

import "testing"

func TestMatchSlashGlobMatchesRootWithRecursivePrefix(t *testing.T) {
	if !MatchSlashGlob("main.go", "**/*.go") {
		t.Fatal("**/*.go should match root-level main.go")
	}
}

func TestMatchSlashGlobNested(t *testing.T) {
	if !MatchSlashGlob("internal/cli/box.go", "**/*.go") {
		t.Fatal("**/*.go should match nested .go file")
	}
	if MatchSlashGlob("internal/cli/box.go", "*.go") {
		t.Fatal("*.go should not match a nested path")
	}
}
