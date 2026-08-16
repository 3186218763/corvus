package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestGenToolsCatalogsEveryBuiltin verifies the generator emits one section
// per registered built-in, so a new built-in surfaces in CI via verify-catalog.
func TestGenToolsCatalogsEveryBuiltin(t *testing.T) {
	var buf bytes.Buffer
	genTools(&buf)
	out := buf.String()
	for _, name := range []string{"bash", "grep", "read_file", "edit_file"} {
		if !strings.Contains(out, "## "+name+"\n") {
			t.Errorf("catalog missing built-in %q", name)
		}
	}
	if !strings.Contains(out, "JSON Schema") {
		t.Error("catalog missing schema sections")
	}
}

// TestGenEventsCatalogsEveryKind verifies the parser picks up the full Kind
// enum, including the wire-stable slots at the tail.
func TestGenEventsCatalogsEveryKind(t *testing.T) {
	var buf bytes.Buffer
	if err := genEvents(&buf, "../../internal/event/event.go"); err != nil {
		t.Fatalf("genEvents: %v", err)
	}
	out := buf.String()
	for _, name := range []string{"TurnStarted", "ToolDispatch", "ToolResult", "TurnDone", "CompactionDone", "ToolProgress", "MCPSurfaceReady"} {
		if !strings.Contains(out, "| "+name+" |") {
			t.Errorf("event map missing kind %q", name)
		}
	}
}
