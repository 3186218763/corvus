package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newFakeManager returns a manager rooted at its own temp workspace plus that
// root, so tests can write files inside the workspace (rel() then yields the
// short relative path the formatters print).
func newFakeManager(t *testing.T, mode, enc string) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	m := NewManager(root, map[string]ServerSpec{"fake": fakeServerSpec(t, mode, enc)})
	t.Cleanup(m.Close)
	return m, root
}

func writeFakeFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestManagerQueriesWithFakeServer drives Definition/References/Hover/
// Diagnostics end-to-end through a real subprocess LSP server.
func TestManagerQueriesWithFakeServer(t *testing.T) {
	m, root := newFakeManager(t, "", "utf-8")
	path := writeFakeFile(t, root, "a.fake", "func alpha() {}\n\nalpha()\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	def, err := m.Definition(ctx, path, 1, "alpha")
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if !strings.Contains(def, "a.fake:1") || !strings.Contains(def, "func alpha() {}") {
		t.Fatalf("Definition = %q, want a.fake:1 with snippet", def)
	}

	refs, err := m.References(ctx, path, 3, "alpha")
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if !strings.Contains(refs, "2 reference(s):") ||
		!strings.Contains(refs, "a.fake:1") || !strings.Contains(refs, "a.fake:3") {
		t.Fatalf("References = %q", refs)
	}

	hov, err := m.Hover(ctx, path, 1, "alpha")
	if err != nil {
		t.Fatalf("Hover: %v", err)
	}
	if hov != "**fake hover**" {
		t.Fatalf("Hover = %q, want **fake hover**", hov)
	}

	diag, err := m.Diagnostics(ctx, path)
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if !strings.Contains(diag, "1 diagnostic(s) in a.fake:") ||
		!strings.Contains(diag, "1:1 error fake diagnostic") {
		t.Fatalf("Diagnostics = %q", diag)
	}
}

func TestManagerDefinitionRetriesContentModified(t *testing.T) {
	m, root := newFakeManager(t, "retry-once", "utf-8")
	path := writeFakeFile(t, root, "a.fake", "func alpha() {}\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	def, err := m.Definition(ctx, path, 1, "alpha")
	if err != nil {
		t.Fatalf("Definition after ContentModified retry: %v", err)
	}
	if !strings.Contains(def, "definition(s):") {
		t.Fatalf("Definition = %q, want resolved result", def)
	}
}

func TestManagerUnconfiguredExtension(t *testing.T) {
	m, _ := newFakeManager(t, "", "utf-8")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.Definition(ctx, filepath.Join(t.TempDir(), "x.go"), 1, "x")
	if err == nil || !strings.Contains(err.Error(), "no language server") {
		t.Fatalf("Definition(.go) error = %v, want no-language-server", err)
	}
}

func TestManagerPrepareMissingFile(t *testing.T) {
	m, _ := newFakeManager(t, "", "utf-8")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.Definition(ctx, "missing.fake", 1, "alpha")
	if err == nil {
		t.Fatal("Definition(missing file) = nil error, want failure")
	}
	if _, _, _, err := m.prepare(ctx, "missing.fake", 1, "alpha"); err == nil {
		t.Fatal("prepare(missing file) = nil error, want failure")
	}
}

func TestManagerPrepareLineOutOfRange(t *testing.T) {
	m, root := newFakeManager(t, "", "utf-8")
	path := writeFakeFile(t, root, "a.fake", "one line\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _, err := m.prepare(ctx, path, 99, "alpha")
	if err == nil || !strings.Contains(err.Error(), "line 99 out of range") {
		t.Fatalf("prepare(line 99) error = %v, want out-of-range", err)
	}
}

func TestManagerPrepareSymbolNotFound(t *testing.T) {
	m, root := newFakeManager(t, "", "utf-8")
	path := writeFakeFile(t, root, "a.fake", "func alpha() {}\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _, err := m.prepare(ctx, path, 1, "nosuchsymbol")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("prepare(missing symbol) error = %v, want not-found", err)
	}
}

// TestManagerConcurrentResolve spawns one server per language even under
// parallel first-use: all resolvers share a single client.
func TestManagerConcurrentResolve(t *testing.T) {
	countFile := filepath.Join(t.TempDir(), "count")
	root := t.TempDir()
	spec := fakeServerSpec(t, "", "utf-8")
	spec.Env[fakeCountFileEnv] = countFile
	m := NewManager(root, map[string]ServerSpec{"fake": spec})
	defer m.Close()

	var wg sync.WaitGroup
	clients := make([]*client, 8)
	errs := make([]error, 8)
	for i := range clients {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clients[i], errs[i] = m.resolve(filepath.Join(root, "a.fake"))
		}(i)
	}
	wg.Wait()

	for i := range clients {
		if errs[i] != nil {
			t.Fatalf("resolve %d: %v", i, errs[i])
		}
		if clients[i] == nil {
			t.Fatalf("resolve %d returned nil client", i)
		}
		if clients[i] != clients[0] {
			t.Fatalf("resolve %d returned a different client than resolve 0", i)
		}
	}
	// Exactly one initialize handshake hit the fake server.
	data, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read count file: %v", err)
	}
	if got := strings.Count(string(data), "initialize"); got != 1 {
		t.Fatalf("fake server initialized %d times, want 1", got)
	}
}

func TestManagerCloseIdempotent(t *testing.T) {
	m, root := newFakeManager(t, "", "utf-8")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.Hover(ctx, writeFakeFile(t, root, "a.fake", "x\n"), 1, "x"); err != nil {
		t.Fatalf("Hover before close: %v", err)
	}
	m.Close()
	m.Close() // second close must not panic or double-wait
}

func TestManagerAbs(t *testing.T) {
	m := NewManager("/ws/root", nil)
	if got := m.abs("a.fake"); got != filepath.Join("/ws/root", "a.fake") {
		t.Errorf("abs(relative) = %q", got)
	}
	if got := m.abs("/abs/path.fake"); got != "/abs/path.fake" {
		t.Errorf("abs(absolute) = %q", got)
	}
	m.Close()
}

func TestManagerRel(t *testing.T) {
	m := NewManager("/ws/root", nil)
	defer m.Close()
	if got := m.rel("/ws/root/a/b.fake"); got != "a/b.fake" {
		t.Errorf("rel inside root = %q", got)
	}
	if got := m.rel("/outside/x.fake"); got != "/outside/x.fake" {
		t.Errorf("rel outside root = %q", got)
	}
}

func TestFormatLocations(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, nil)
	defer m.Close()
	if got := m.formatLocations("definition", nil); got != "no definition found" {
		t.Errorf("empty = %q", got)
	}
	if got := m.formatLocations("reference", nil); got != "no reference found" {
		t.Errorf("empty = %q", got)
	}

	path := writeFakeFile(t, root, "a.fake", "first\nsecond\n")
	uri := pathToURI(path)
	locs := []Location{
		{URI: uri, Range: Range{Start: Position{Line: 1, Character: 0}, End: Position{Line: 1, Character: 2}}},
		{URI: uri, Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 1}}},
	}
	got := m.formatLocations("definition", locs)
	want := "2 definition(s):\n" + filepath.Base(path) + ":1  first\n" + filepath.Base(path) + ":2  second"
	if got != want {
		t.Errorf("formatLocations = %q, want %q", got, want)
	}

	// Sorting: different URIs sort by URI, then by line.
	other := writeFakeFile(t, root, "b.fake", "zzz\n")
	otherURI := pathToURI(other)
	mixed := []Location{
		{URI: otherURI, Range: Range{Start: Position{Line: 0, Character: 0}}},
		{URI: uri, Range: Range{Start: Position{Line: 2, Character: 0}}},
		{URI: uri, Range: Range{Start: Position{Line: 1, Character: 0}}},
	}
	got = m.formatLocations("reference", mixed)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], filepath.Base(path)+":2") ||
		!strings.HasPrefix(lines[2], filepath.Base(path)+":3") ||
		!strings.HasPrefix(lines[3], filepath.Base(other)+":1") {
		t.Errorf("mixed sort output:\n%s", got)
	}
}

func TestReadLine(t *testing.T) {
	if got := readLine(filepath.Join(t.TempDir(), "missing"), 0); got != "" {
		t.Errorf("readLine(missing) = %q, want empty", got)
	}
	path := writeFakeFile(t, t.TempDir(), "a.fake", "  padded  \n")
	if got := readLine(path, 0); got != "padded" {
		t.Errorf("readLine = %q, want trimmed padded", got)
	}
	if got := readLine(path, 5); got != "" {
		t.Errorf("readLine(out of range) = %q, want empty", got)
	}
}

func TestFormatDiagnostics(t *testing.T) {
	if got := formatDiagnostics("a.fake", nil); got != "no diagnostics for a.fake" {
		t.Errorf("empty = %q", got)
	}
	diags := []Diagnostic{
		{Range: Range{Start: Position{Line: 0, Character: 3}}, Severity: 1, Source: "gopls", Message: "  err  "},
		{Range: Range{Start: Position{Line: 4, Character: 0}}, Severity: 2, Message: "warn"},
		{Range: Range{Start: Position{Line: 8, Character: 0}}, Severity: 3, Message: "info"},
		{Range: Range{Start: Position{Line: 9, Character: 0}}, Severity: 4, Message: "hint"},
		{Range: Range{Start: Position{Line: 10, Character: 0}}, Severity: 99, Message: "unknown"},
	}
	got := formatDiagnostics("a.fake", diags)
	want := "5 diagnostic(s) in a.fake:\n" +
		"1:4 error [gopls] err\n" +
		"5:1 warning warn\n" +
		"9:1 info info\n" +
		"10:1 hint hint\n" +
		"11:1 error unknown"
	if got != want {
		t.Errorf("formatDiagnostics = %q, want %q", got, want)
	}
}

func TestIndexingOr(t *testing.T) {
	msg, err := indexingOr(&rpcError{Code: -32801, Message: "content modified"})
	if err != nil || !strings.Contains(msg, "still indexing") {
		t.Fatalf("indexingOr(content modified) = (%q, %v), want retry-shortly", msg, err)
	}
	plain := errors.New("boom")
	if _, err := indexingOr(plain); !errors.Is(err, plain) {
		t.Fatalf("indexingOr(plain) = %v, want passthrough", err)
	}
}

func TestToolsAdapter(t *testing.T) {
	m, root := newFakeManager(t, "", "utf-8")
	tools := Tools(m)
	if len(tools) != 4 {
		t.Fatalf("Tools() = %d tools, want 4", len(tools))
	}
	for _, tool := range tools {
		if !tool.ReadOnly() {
			t.Errorf("%s must be read-only", tool.Name())
		}
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name()] = true
	}
	for _, want := range []string{"lsp_definition", "lsp_references", "lsp_hover", "lsp_diagnostics"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := writeFakeFile(t, root, "a.fake", "func alpha() {}\n")

	// diagTool.Execute routes through the manager.
	res, err := tools[3].Execute(ctx, []byte(`{"file":"`+path+`"}`))
	if err != nil || !strings.Contains(res, "fake diagnostic") {
		t.Fatalf("diagnostics tool = (%q, %v)", res, err)
	}

	// posTool.Execute validates args before calling the manager.
	for _, bad := range []string{`{"line":1,"symbol":"x"}`, `{"file":"x","symbol":""}`, `{"file":"x","line":0,"symbol":"x"}`, `not json`} {
		if _, err := tools[0].Execute(ctx, []byte(bad)); err == nil {
			t.Fatalf("posTool.Execute(%s) = nil error, want validation failure", bad)
		}
	}
	pos, err := tools[0].Execute(ctx, []byte(`{"file":"`+path+`","line":1,"symbol":"alpha"}`))
	if err != nil || !strings.Contains(pos, "definition(s):") {
		t.Fatalf("posTool.Execute = (%q, %v)", pos, err)
	}
}

func TestManagerDiagnosticsReturnsPublished(t *testing.T) {
	// Diagnostics returns the server-published problems for the synced
	// version, not a deadline artifact.
	m, root := newFakeManager(t, "", "utf-8")
	path := writeFakeFile(t, root, "a.fake", "x\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	diag, err := m.Diagnostics(ctx, path)
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if !strings.Contains(diag, "fake diagnostic") {
		t.Fatalf("Diagnostics = %q, want published diagnostic", diag)
	}
}
