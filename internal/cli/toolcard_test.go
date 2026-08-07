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
	if !strings.Contains(plain0, "Bash") || !strings.Contains(plain0, "go build ./...") {
		t.Fatalf("header should carry the first command line, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "\033[") {
		t.Fatalf("command should be syntax-highlighted, got %q", lines[0])
	}
	plain1 := ansi.Strip(lines[1])
	if !strings.Contains(plain1, "⎿") || !strings.Contains(plain1, "go test ./...") {
		t.Fatalf("continuation should use the ⎿ gutter, got %q", lines[1])
	}
}

func TestBashToolCardEmptyCommand(t *testing.T) {
	card := toolCard("bash", `{}`, 60)
	if !strings.Contains(card, "Bash") {
		t.Fatalf("empty command should still name the tool, got %q", card)
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
