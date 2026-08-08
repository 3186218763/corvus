package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestSelectionLetter(t *testing.T) {
	ch, ok := selectionLetter(0)
	if !ok || ch != 'a' {
		t.Fatalf("0 → a, got %q %v", ch, ok)
	}
	ch, ok = selectionLetter(25)
	if !ok || ch != 'z' {
		t.Fatalf("25 → z, got %q %v", ch, ok)
	}
	if _, ok := selectionLetter(26); ok {
		t.Fatal("26 should have no letter")
	}
}

func TestSelectionIndexKey(t *testing.T) {
	if got := selectionIndexKey("a"); got != 0 {
		t.Fatalf("a → 0, got %d", got)
	}
	if got := selectionIndexKey("B"); got != 1 {
		t.Fatalf("B → 1, got %d", got)
	}
	if got := selectionIndexKey("3"); got != 2 {
		t.Fatalf("3 → 2, got %d", got)
	}
	if got := selectionIndexKey("enter"); got != -1 {
		t.Fatalf("enter → -1, got %d", got)
	}
}

func TestSelectionRowUsesLetters(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	sel := ansi.Strip(selectionRow(true, 0, "", "Yes, just this once", false))
	if !strings.Contains(sel, "a.") || !strings.Contains(sel, "Yes") {
		t.Fatalf("selected row should show a. label, got %q", sel)
	}
	if !strings.Contains(sel, "›") {
		t.Fatalf("selected row should show ›, got %q", sel)
	}
	idle := ansi.Strip(selectionRow(false, 1, "", "No, deny", false))
	if !strings.Contains(idle, "b.") || !strings.Contains(idle, "No") {
		t.Fatalf("idle row should show b. label, got %q", idle)
	}
	if strings.Contains(idle, "1.") || strings.Contains(idle, "2.") {
		t.Fatalf("must not use numeric labels, got %q", idle)
	}
}
