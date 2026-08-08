package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"corvus/internal/event"
)

// TestDiffSourceReflowsWithoutWrappingBars is the non-fullscreen regression:
// a diff committed at a wide width must re-paint to a narrower viewport so
// each +/- row stays one visual line at content width (full-line bg intact).
// Fixed commitLine rows used to lipgloss-wrap mid-bar and look broken.
func TestDiffSourceReflowsWithoutWrappingBars(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.TrueColor
	configureCLITheme("dark")

	m := newTestChatTUI()
	m.width = 100
	m.nativeScrollback = false
	m.diffMaxLines = 40

	diff := event.FileDiff{
		Diff:    "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old line\n+new line that is short\n",
		Added:   1,
		Removed: 1,
	}
	m.commitTranscriptSource(transcriptSource{
		kind:     transcriptSourceDiff,
		raw:      "edit_file",
		aux:      `{"path":"x.go"}`,
		maxLines: 40,
		fileDiff: diff,
	})
	if len(m.transcript) != 1 {
		t.Fatalf("want one reflowable diff block, got %d", len(m.transcript))
	}

	// Narrow "not fullscreen" viewport (alt-screen content width = termW-1).
	m.width = 50
	m.reflowTranscript(m.width)
	contentW := transcriptContentWidth(m.width, m.nativeScrollback)

	lines := strings.Split(m.transcript[0], "\n")
	if len(lines) < 3 {
		t.Fatalf("expected header + diff rows, got %d:\n%s", len(lines), m.transcript[0])
	}
	for i, line := range lines {
		// Skip dim hunk markers / fold footers without full-line paint.
		plain := ansi.Strip(line)
		if strings.TrimSpace(plain) == "⋮" || strings.Contains(plain, "more") {
			continue
		}
		// +/- body rows must be single visual lines at content width.
		if strings.Contains(plain, "+") || strings.Contains(plain, "-") || strings.Contains(plain, "old") || strings.Contains(plain, "new") {
			// Prefer rows that carry the bar (have NBSP pad or open bg).
			if strings.Contains(line, completionPadCell) || strings.Contains(line, bgSGR(activeCLITheme.diffAddBG)) || strings.Contains(line, bgSGR(activeCLITheme.diffDelBG)) {
				if w := visibleWidth(line); w != contentW {
					t.Errorf("row %d vis width = %d, want content width %d\nplain=%q", i, w, contentW, plain)
				}
				if !strings.HasSuffix(line, ansiReset) {
					t.Errorf("row %d must end with reset (wrap must not strip it): %q", i, line[max(0, len(line)-30):])
				}
				// Must not have been lipgloss-split: the block is one string with \n
				// separators, each logical row is one of those lines.
			}
		}
	}

	// wrapBlock at content width must keep each +/- bar as one line.
	for _, line := range lines {
		if !strings.Contains(line, completionPadCell) {
			continue
		}
		wrapped := wrapBlock(line, contentW)
		if len(wrapped) != 1 {
			t.Fatalf("bar at content width must not wrap, got %d lines for %q", len(wrapped), ansi.Strip(line))
		}
	}
}

// TestDiffDispatchCommitsReflowableSource ensures ToolDispatch path stores
// transcriptSourceDiff (not fixed lines) so resize can re-paint.
func TestDiffDispatchCommitsReflowableSource(t *testing.T) {
	m := newTestChatTUI()
	m.width = 80
	m.ingestEvent(event.Event{
		Kind: event.ToolDispatch,
		Tool: event.Tool{
			Name: "edit_file",
			Args: `{"path":"pkg/x.go"}`,
			FileDiff: event.FileDiff{
				Diff:    "@@ -1 +1 @@\n-a\n+b\n",
				Added:   1,
				Removed: 1,
			},
		},
	})
	if len(m.transcriptSources) == 0 {
		t.Fatal("expected transcript source")
	}
	// Last non-empty source should be the diff.
	var found bool
	for _, s := range m.transcriptSources {
		if s.kind == transcriptSourceDiff {
			found = true
			if s.fileDiff.Diff == "" {
				t.Fatal("diff source missing FileDiff payload")
			}
			if s.raw != "edit_file" {
				t.Fatalf("raw tool name = %q", s.raw)
			}
		}
	}
	if !found {
		t.Fatalf("ToolDispatch with FileDiff must commit transcriptSourceDiff, sources=%v", m.transcriptSources)
	}
}
