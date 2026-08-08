package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestBashToolCardHighlightsAndContinues(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	card := toolCard("bash", `{"command":"go build ./...\ngo test ./..."}`, 60)
	lines := strings.Split(card, "\n")
	if len(lines) != 2 {
		t.Fatalf("want header + one continuation row, got %d: %q", len(lines), card)
	}
	plain0 := ansi.Strip(lines[0])
	if !strings.Contains(plain0, "Ran") || !strings.Contains(plain0, "go build ./...") {
		t.Fatalf("header should be • Ran + first command line, got %q", lines[0])
	}
	if !strings.Contains(plain0, "•") {
		t.Fatalf("header should use • marker, got %q", plain0)
	}
	if !strings.Contains(lines[0], "\033[") {
		t.Fatalf("command should be syntax-highlighted, got %q", lines[0])
	}
	plain1 := ansi.Strip(lines[1])
	if !strings.Contains(plain1, "└") || !strings.Contains(plain1, "go test ./...") {
		t.Fatalf("continuation should use the └ gutter, got %q", lines[1])
	}
}

func TestBashToolCardEmptyCommand(t *testing.T) {
	card := toolCard("bash", `{}`, 60)
	if !strings.Contains(ansi.Strip(card), "Ran") {
		t.Fatalf("empty command should still name Ran, got %q", card)
	}
}

func TestBashToolCardSingleLineStaysOneRow(t *testing.T) {
	card := toolCard("bash", `{"command":"git status"}`, 60)
	if strings.Contains(card, "\n") {
		t.Fatalf("single-line command should stay one row, got %q", card)
	}
	if !strings.Contains(card, "git status") {
		t.Fatalf("command missing from card, got %q", card)
	}
}

func TestBashToolCardNarrowNoPanic(t *testing.T) {
	for _, w := range []int{1, 2, 3, 5, 8, 20} {
		_ = toolCard("bash", `{"command":"go test ./... 你好 long command"}`, w)
	}
}

func TestEditedCard(t *testing.T) {
	plain := ansi.Strip(toolCard("edit_file", `{"path":"status_footer.go"}`, 80))
	if !strings.Contains(plain, "•") || !strings.Contains(plain, "Edited") || !strings.Contains(plain, "status_footer.go") {
		t.Fatalf("want • Edited path, got %q", plain)
	}
	if strings.Contains(plain, "●") {
		t.Fatalf("must not use category ●, got %q", plain)
	}
}

func TestExploredCardSingleRead(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	card := toolCard("read_file", `{"path":"toolcard.go"}`, 80)
	plain := ansi.Strip(card)
	if !strings.Contains(plain, "Explored") {
		t.Fatalf("read tools render as Explored, got %q", plain)
	}
	if !strings.Contains(plain, "Read") || !strings.Contains(plain, "toolcard.go") {
		t.Fatalf("want Read path leaf, got %q", plain)
	}
	if !strings.Contains(plain, "└") {
		t.Fatalf("want tree gutter, got %q", plain)
	}
	// Tree verb should be cyan (info), not bare default only.
	if !strings.Contains(card, "\033[") {
		t.Fatalf("tree verb should be colored, got %q", card)
	}
}

func TestExploredCardCoalescesReads(t *testing.T) {
	leaves := []exploreLeaf{
		{Verb: "Search", Arg: "foo"},
		{Verb: "Read", Arg: "a.go"},
		{Verb: "Read", Arg: "b.go"},
		{Verb: "Read", Arg: "c.go"},
	}
	plain := ansi.Strip(exploredCard(leaves, 80))
	if strings.Count(plain, "\n") < 2 {
		t.Fatalf("want multi-line tree, got %q", plain)
	}
	if !strings.Contains(plain, "Search") || !strings.Contains(plain, "foo") {
		t.Fatalf("search leaf missing: %q", plain)
	}
	// Consecutive reads merge to one leaf.
	if strings.Count(plain, "Read") != 1 {
		t.Fatalf("consecutive Reads should merge, got %q", plain)
	}
	if !strings.Contains(plain, "a.go, b.go, c.go") {
		t.Fatalf("merged read names, got %q", plain)
	}
	// Codex hang: one └ on first leaf, no sibling ├ tree.
	if strings.Contains(plain, "├") {
		t.Fatalf("must not use sibling ├ tree, got %q", plain)
	}
	if !strings.Contains(plain, "└") {
		t.Fatalf("want hanging └ on first leaf, got %q", plain)
	}
	lines := strings.Split(plain, "\n")
	if len(lines) < 2 || !strings.Contains(lines[1], "└") {
		t.Fatalf("first leaf under └, got %q", plain)
	}
	for _, line := range lines[2:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, "└") || strings.Contains(line, "├") {
			t.Fatalf("continuation leaves must not re-branch, got %q", line)
		}
		if !strings.HasPrefix(line, "    ") {
			t.Fatalf("continuation should use 4-space indent, got %q", line)
		}
	}
}

func TestExploredCardTreeHierarchy(t *testing.T) {
	plain := ansi.Strip(exploredCard([]exploreLeaf{
		{Verb: "Read", Arg: "config.go"},
		{Verb: "Search", Arg: "Bash|Sandbox|enforce|off"},
	}, 80))
	lines := strings.Split(plain, "\n")
	if len(lines) != 3 {
		t.Fatalf("want head + 2 leaves, got %d: %q", len(lines), plain)
	}
	if strings.Contains(plain, "├") {
		t.Fatalf("must not use ├, got %q", plain)
	}
	if !strings.Contains(lines[1], "└") || !strings.Contains(lines[1], "Read") {
		t.Fatalf("first leaf should be └ Read, got %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "    ") || !strings.Contains(lines[2], "Search") {
		t.Fatalf("second leaf should be indented Search, got %q", lines[2])
	}
}

func TestExploredCardSingleHangingBranch(t *testing.T) {
	leaves := []exploreLeaf{
		{Verb: "Search", Arg: "foo"},
		{Verb: "Read", Arg: "a.go"},
		{Verb: "Read", Arg: "b.go"},
	}
	plain := ansi.Strip(exploredCard(leaves, 80))
	if strings.Contains(plain, "├") {
		t.Fatalf("must not use sibling ├ tree, got %q", plain)
	}
	if !strings.Contains(plain, "a.go, b.go") {
		t.Fatalf("merged reads missing: %q", plain)
	}
	if strings.Count(plain, "└") != 1 {
		t.Fatalf("want exactly one └, got %q", plain)
	}
}

func TestExploredCardMaxLeaves(t *testing.T) {
	leaves := make([]exploreLeaf, 0, 8)
	for i := 0; i < 8; i++ {
		leaves = append(leaves, exploreLeaf{Verb: "Search", Arg: "q" + string(rune('a'+i))})
	}
	plain := ansi.Strip(exploredCard(leaves, 80))
	if !strings.Contains(plain, "+3 more") {
		t.Fatalf("want +N more for overflow, got %q", plain)
	}
}

func TestIsExploreCoalesceTool(t *testing.T) {
	for _, name := range []string{"read_file", "ls", "glob", "grep", "web_fetch", "web_search"} {
		if !isExploreCoalesceTool(name) {
			t.Errorf("%s should coalesce", name)
		}
	}
	for _, name := range []string{"bash", "bash_output", "edit_file", "wait"} {
		if isExploreCoalesceTool(name) {
			t.Errorf("%s must not coalesce", name)
		}
	}
}
