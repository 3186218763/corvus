package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"corvus/internal/event"
)

func TestSplitEditorCommandUsesStaticShellWords(t *testing.T) {
	got, err := splitEditorCommand(`code --goto "dir/file name.go:12"`)
	if err != nil {
		t.Fatalf("splitEditorCommand: %v", err)
	}
	want := []string{"code", "--goto", "dir/file name.go:12"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestSplitEditorCommandRejectsShellControl(t *testing.T) {
	if _, err := splitEditorCommand(`vim file; rm -rf tmp`); err == nil {
		t.Fatal("splitEditorCommand accepted shell control syntax")
	}
}

func TestClearMCPAuthenticationUsesControllerWorkspace(t *testing.T) {
	isolateCLIConfigHome(t)
	controllerRoot := t.TempDir()
	cwdRoot := t.TempDir()
	const pluginConfig = `
[[plugins]]
name = "dida"
type = "http"
url = "https://example.test/mcp?access_token=TOKEN&workspace=main"
auto_start = false
`
	writeConfig := func(root, token string) {
		t.Helper()
		raw := minimalTestModelTOML + strings.ReplaceAll(pluginConfig, "TOKEN", token)
		if err := os.MkdirAll(filepath.Join(root, ".corvus"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".corvus", "config.toml"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(controllerRoot, "controller-token")
	writeConfig(cwdRoot, "cwd-token")
	t.Chdir(cwdRoot)

	ctrl, err := setupProfile(context.Background(), "", 0, false, event.Discard, "", controllerRoot)
	if err != nil {
		t.Fatalf("setupProfile: %v", err)
	}
	defer ctrl.Close()

	pending := []string{}
	model := chatTUI{ctrl: ctrl, pendingCommit: &pending}
	model.clearMCPAuthentication(mcpServerView{Name: "dida"})

	controllerRaw, err := os.ReadFile(filepath.Join(controllerRoot, ".corvus", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(controllerRaw), "controller-token") ||
		!strings.Contains(string(controllerRaw), "workspace=main") {
		t.Fatalf("controller config authentication was not cleared:\n%s", controllerRaw)
	}
	cwdRaw, err := os.ReadFile(filepath.Join(cwdRoot, ".corvus", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cwdRaw), "cwd-token") {
		t.Fatalf("cwd config was unexpectedly modified:\n%s", cwdRaw)
	}
}
