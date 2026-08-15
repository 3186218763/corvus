package config

import (
	"os"
	"path/filepath"
	"testing"

	"corvus/internal/pluginpkg"
)

func TestLoadMergesInstalledPluginSkillRootsAndMCP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	root := filepath.Join(home, "plugins", "superpowers")
	writeConfigTestFile(t, filepath.Join(root, pluginpkg.NativeManifest), `{
  "name": "superpowers",
  "version": "1.0.0",
  "skills": "skills",
  "mcpServers": {
    "helper": { "command": "bin/helper" }
  }
}`)
	if err := pluginpkg.Upsert(home, pluginpkg.InstalledPlugin{
		Name:         "superpowers",
		Root:         "plugins/superpowers",
		Version:      "1.0.0",
		ManifestKind: "corvus",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Skills.Paths) == 0 || cfg.Skills.Paths[len(cfg.Skills.Paths)-1] != filepath.Join(root, "skills") {
		t.Fatalf("skills paths = %#v", cfg.Skills.Paths)
	}
	owners := cfg.PluginPackageSkillOwners()[CanonicalSkillPath(filepath.Join(root, "skills"))]
	if len(owners) != 1 || owners[0] != "superpowers" {
		t.Fatalf("plugin skill owners = %#v, want superpowers", owners)
	}
	var found bool
	for _, p := range cfg.Plugins {
		if p.Name == "helper" {
			found = true
			if p.Command != filepath.Join(root, "bin", "helper") {
				t.Fatalf("plugin command = %q", p.Command)
			}
			if p.Env["CORVUS_PLUGIN_NAME"] != "superpowers" {
				t.Fatalf("plugin env = %#v", p.Env)
			}
		}
	}
	if !found {
		t.Fatalf("plugin MCP server missing: %#v", cfg.Plugins)
	}
	if owner, ok := cfg.PluginPackageOwner("helper"); !ok || owner != "superpowers" {
		t.Fatalf("plugin MCP owner = %q, %v; want superpowers, true", owner, ok)
	}
}

func TestClaudePackageMCPExpandsRootAndDoesNotAutoStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	root := filepath.Join(home, "plugins", "claude-mcp")
	writeConfigTestFile(t, filepath.Join(root, pluginpkg.ClaudeManifest), `{"name":"claude-mcp"}`)
	writeConfigTestFile(t, filepath.Join(root, ".mcp.json"), `{
  "mcpServers": {
    "Local Search": {
      "command": "${CLAUDE_PLUGIN_ROOT}/bin/server",
      "args": ["--root", "${CLAUDE_PLUGIN_ROOT}/data", "--workspace", "${CLAUDE_PROJECT_DIR}"],
      "env": {"DATA_DIR": "${CLAUDE_PLUGIN_ROOT}/data"}
    }
  }
}`)
	if err := pluginpkg.Upsert(home, pluginpkg.InstalledPlugin{Name: "claude-mcp", Root: "plugins/claude-mcp", ManifestKind: "claude", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	cfg, err := LoadForRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins) != 1 {
		t.Fatalf("plugins = %#v", cfg.Plugins)
	}
	got := cfg.Plugins[0]
	if got.Command != filepath.Join(root, "bin", "server") || got.Args[1] != filepath.Join(root, "data") || got.Env["DATA_DIR"] != filepath.Join(root, "data") {
		t.Fatalf("Claude root was not expanded: %#v", got)
	}
	if got.Env["CLAUDE_PLUGIN_ROOT"] != root {
		t.Fatalf("CLAUDE_PLUGIN_ROOT = %q", got.Env["CLAUDE_PLUGIN_ROOT"])
	}
	if got.Args[3] != workspace || got.Env["CLAUDE_PROJECT_DIR"] != workspace {
		t.Fatalf("workspace expansion = %#v", got)
	}
	if got.ShouldAutoStart() {
		t.Fatal("imported Claude MCP must require an explicit connection")
	}
	if len(cfg.AutoStartPlugins()) != 0 {
		t.Fatalf("auto-start plugins = %#v", cfg.AutoStartPlugins())
	}
}

func TestClaudePackageMCPDeduplicatesSameConnectionAcrossPackages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	for i, name := range []string{"legal-one", "legal-two"} {
		root := filepath.Join(home, "plugins", name)
		writeConfigTestFile(t, filepath.Join(root, pluginpkg.ClaudeManifest), `{"name":"`+name+`"}`)
		writeConfigTestFile(t, filepath.Join(root, ".mcp.json"), `{
  "mcpServers":{"飞书":{"type":"http","url":"https://open.feishu.cn/mcp","description":"package `+string(rune('A'+i))+` description"}}
}`)
		if err := pluginpkg.Upsert(home, pluginpkg.InstalledPlugin{Name: name, Root: "plugins/" + name, ManifestKind: "claude", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins) != 1 || cfg.Plugins[0].URL != "https://open.feishu.cn/mcp" {
		t.Fatalf("deduplicated plugins = %#v", cfg.Plugins)
	}
}

func writeConfigTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCommandDirsIncludePluginPackageCommands pins the plugin-commands wiring:
// an enabled plugin package's command roots join command discovery at the
// before user/project entries, retaining package ownership, while a disabled
// package contributes nothing.
func TestCommandDirsIncludePluginPackageCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	root := filepath.Join(home, "plugins", "pwf")
	writeConfigTestFile(t, filepath.Join(root, pluginpkg.ClaudeManifest), `{"name": "pwf"}`)
	writeConfigTestFile(t, filepath.Join(root, "skills", "planner", "SKILL.md"), "---\ndescription: p\n---\nbody")
	writeConfigTestFile(t, filepath.Join(root, "commands", "plan.md"), "---\ndescription: plan\n---\nPlan: $ARGUMENTS")
	if err := pluginpkg.Upsert(home, pluginpkg.InstalledPlugin{
		Name:         "pwf",
		Root:         "plugins/pwf",
		ManifestKind: "claude",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	dirs := CommandDirsForRoot(t.TempDir())
	want := filepath.Join(root, "commands")
	if len(dirs) == 0 || dirs[0] != want {
		t.Fatalf("CommandDirsForRoot = %#v, want plugin commands dir first (lowest priority): %s", dirs, want)
	}
	roots := CommandRootsForRoot(t.TempDir())
	if len(roots) == 0 || roots[0].Path != want || roots[0].Plugin != "pwf" {
		t.Fatalf("CommandRootsForRoot = %#v, want plugin ownership on first root", roots)
	}

	if err := pluginpkg.SetEnabled(home, "pwf", false); err != nil {
		t.Fatal(err)
	}
	for _, dir := range CommandDirsForRoot(t.TempDir()) {
		if dir == want {
			t.Fatalf("disabled plugin's commands dir must not join discovery: %#v", dir)
		}
	}
}

// TestCommandDirsWithoutPluginState keeps the no-plugin fast path intact.
func TestCommandDirsWithoutPluginState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	if dirs := CommandDirsForRoot(t.TempDir()); len(dirs) == 0 {
		t.Fatal("CommandDirsForRoot must still return the conventional dirs")
	}
}

// The three MCP timeout fields were dropped when a package manifest was copied
// into config (ADR-0007): the plugin layer never carried them, so the boot
// chain saw zeros and fell back to defaults regardless of the manifest.
func TestPluginPackageMCPTimeoutsAndTierSurviveImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	root := filepath.Join(home, "plugins", "timed")
	writeConfigTestFile(t, filepath.Join(root, pluginpkg.NativeManifest), `{
  "name": "timed",
  "version": "1.0.0",
  "mcpServers": {
    "slow-helper": {
      "command": "bin/helper",
      "startup_timeout_seconds": 5,
      "call_timeout_seconds": 60,
      "tool_timeout_seconds": {"deep-research": 900},
      "tier": "eager"
    }
  }
}`)
	if err := pluginpkg.Upsert(home, pluginpkg.InstalledPlugin{
		Name: "timed", Root: "plugins/timed", Version: "1.0.0",
		ManifestKind: "corvus", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var entry *PluginEntry
	for i := range cfg.Plugins {
		if cfg.Plugins[i].Name == "slow-helper" {
			entry = &cfg.Plugins[i]
		}
	}
	if entry == nil {
		t.Fatalf("plugin MCP server missing: %#v", cfg.Plugins)
	}
	if entry.StartupTimeoutSeconds != 5 || entry.CallTimeoutSeconds != 60 || entry.ToolTimeoutSeconds["deep-research"] != 900 {
		t.Fatalf("timeouts dropped on import: startup=%d call=%d tool=%v",
			entry.StartupTimeoutSeconds, entry.CallTimeoutSeconds, entry.ToolTimeoutSeconds)
	}
	// Tier is a retired user-facing setting: normalizeLegacyMCPTiers erases it
	// from every entry (user TOML, .mcp.json, and packages alike) at load, so
	// the manifest's eager tier must NOT survive either — same policy for all
	// sources, applied in exactly one place.
	if entry.Tier != "" || entry.ResolvedTier() != "background" {
		t.Fatalf("package tier escaped the load-time normalization: tier=%q resolved=%q", entry.Tier, entry.ResolvedTier())
	}
}

// Claude-format imports forced autoStart=false and had no timeouts at all; the
// canonical schema carries both, and auto_start is now honored when the
// manifest states it explicitly (absent stays false — see
// TestClaudePackageMCPExpandsRootAndDoesNotAutoStart).
func TestClaudeImportHonorsAutoStartAndTimeouts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	root := filepath.Join(home, "plugins", "claude-timed")
	writeConfigTestFile(t, filepath.Join(root, pluginpkg.ClaudeManifest), `{"name":"claude-timed"}`)
	writeConfigTestFile(t, filepath.Join(root, ".mcp.json"), `{
  "mcpServers": {
    "pinned": {
      "command": "bin/server",
      "auto_start": true,
      "startup_timeout_seconds": 15,
      "call_timeout_seconds": 120,
      "tool_timeout_seconds": {"crawl": 600}
    }
  }
}`)
	if err := pluginpkg.Upsert(home, pluginpkg.InstalledPlugin{Name: "claude-timed", Root: "plugins/claude-timed", ManifestKind: "claude", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins) != 1 {
		t.Fatalf("plugins = %#v", cfg.Plugins)
	}
	got := cfg.Plugins[0]
	if !got.ShouldAutoStart() {
		t.Fatal("explicit auto_start: true was not honored on Claude import")
	}
	if got.StartupTimeoutSeconds != 15 || got.CallTimeoutSeconds != 120 || got.ToolTimeoutSeconds["crawl"] != 600 {
		t.Fatalf("timeouts dropped on Claude import: startup=%d call=%d tool=%v",
			got.StartupTimeoutSeconds, got.CallTimeoutSeconds, got.ToolTimeoutSeconds)
	}
}
