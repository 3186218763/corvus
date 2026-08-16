package plugin

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"corvus/internal/tool"
)

// buildExamplePlugin compiles cmd/corvus-plugin-example into a temp binary and
// returns its path. Building from inside the module lets `go build` resolve the
// import path regardless of the test's working directory.
func buildExamplePlugin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "corvus-plugin-example")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, "corvus/cmd/corvus-plugin-example").CombinedOutput()
	if err != nil {
		t.Fatalf("build example plugin: %v\n%s", err, out)
	}
	return bin
}

// TestExamplePluginEndToEnd builds the real reference plugin and drives it
// through StartAll over actual stdio pipes — the genuine end-to-end contract,
// not a mock. It also asserts the readOnlyHint annotation flows through to
// ReadOnly(), which is what lets plugin tools join parallel batches and the
// permission reader-default.
func TestExamplePluginEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a subprocess; skipped under -short")
	}
	bin := buildExamplePlugin(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	host, tools, err := connectSpecs(ctx, []Spec{{Name: "example", Command: bin}}, true)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer host.Close()

	byName := map[string]tool.Tool{}
	for _, tl := range tools {
		byName[tl.Name()] = tl
	}
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d (%v)", len(tools), byName)
	}

	echo, ok := byName["mcp__example__echo"]
	if !ok {
		t.Fatal("mcp__example__echo not listed")
	}
	wc, ok := byName["mcp__example__wordcount"]
	if !ok {
		t.Fatal("mcp__example__wordcount not listed")
	}

	// readOnlyHint: true must surface as ReadOnly() == true.
	if !echo.ReadOnly() || !wc.ReadOnly() {
		t.Errorf("read-only annotation not honoured: echo=%v wordcount=%v", echo.ReadOnly(), wc.ReadOnly())
	}

	// echo round-trips its argument.
	if got, err := echo.Execute(ctx, json.RawMessage(`{"text":"hi there"}`)); err != nil || got != "hi there" {
		t.Errorf("echo = (%q, %v), want (\"hi there\", nil)", got, err)
	}

	// wordcount produces a structured count.
	got, err := wc.Execute(ctx, json.RawMessage(`{"text":"alpha beta gamma"}`))
	if err != nil {
		t.Fatalf("wordcount: %v", err)
	}
	if !strings.Contains(got, "words: 3") {
		t.Errorf("wordcount = %q, want it to contain \"words: 3\"", got)
	}

	// A handler-level failure (wrong arg type) comes back as an isError result,
	// which the adapter surfaces as a Go error the model can read.
	if _, err := echo.Execute(ctx, json.RawMessage(`{"text":123}`)); err == nil {
		t.Error("echo with non-string text should return an error (isError result)")
	}

	// Prompts and resources stream in on a background goroutine once the
	// handshake advertises them; poll until both surfaces land.
	deadline := time.Now().Add(5 * time.Second)
	for len(host.Prompts()) == 0 || len(host.Resources()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("prompts/resources did not arrive in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Prompts: the server advertises the capability, so the host discovers the
	// "review" prompt and can render it with arguments.
	prompts := host.Prompts()
	if len(prompts) != 1 || prompts[0].Name != "mcp__example__review" {
		t.Fatalf("prompts = %+v, want one mcp__example__review", prompts)
	}
	if len(prompts[0].Args) != 1 || prompts[0].Args[0].Name != "path" {
		t.Errorf("prompt args = %+v, want one 'path'", prompts[0].Args)
	}
	rendered, err := prompts[0].Get(ctx, map[string]string{"path": "main.go"})
	if err != nil {
		t.Fatalf("prompt Get: %v", err)
	}
	if !strings.Contains(rendered, "main.go") {
		t.Errorf("rendered prompt = %q, want it to mention main.go", rendered)
	}

	// Resources: discovered via the advertised capability, read by uri.
	res := host.Resources()
	if len(res) != 1 || res[0].URI != "doc://style-guide" {
		t.Fatalf("resources = %+v, want one doc://style-guide", res)
	}
	content, err := host.ReadResource(ctx, "example", "doc://style-guide")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if !strings.Contains(content, "style") {
		t.Errorf("resource content = %q, want it to mention style", content)
	}
	if _, err := host.ReadResource(ctx, "example", "doc://missing"); err == nil {
		t.Error("reading an unknown resource uri should error")
	}
}
