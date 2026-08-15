package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolatedHome points HOME / config-dir resolution at a fresh temp tree and
// returns the v1+ dest config path.
func isolatedHome(t *testing.T) (dest, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CORVUS_CREDENTIALS_STORE", "file")
	t.Setenv("USERPROFILE", home)                               // os.UserHomeDir on Windows
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config")) // os.UserConfigDir on Linux
	t.Setenv("AppData", filepath.Join(home, "AppData"))         // os.UserConfigDir on Windows
	return userConfigPath(), home
}

func TestMigrateMCPToUserConfigOnUpgradeCollectsKnownSources(t *testing.T) {
	dest, _ := isolatedHome(t)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(`
[[plugins]]
name = "global"
command = "global-bin"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The legacy OS-support TOML location still contributes, but never wins a
	// name collision against the already-present global entry.
	legacyTOML := legacyUserConfigPath()
	if legacyTOML != "" && !samePath(legacyTOML, dest) {
		if err := os.MkdirAll(filepath.Dir(legacyTOML), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(legacyTOML, []byte(`
[[plugins]]
name = "global"
command = "legacy-should-not-win"

[[plugins]]
name = "legacy-toml"
command = "legacy-toml-bin"
`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	projectTOML := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectTOML, ".corvus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectTOML, ".corvus", "config.toml"), []byte(`
[[plugins]]
name = "project-toml"
command = "project-toml-bin"

[[plugins]]
name = "global"
command = "project-should-not-win"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	projectJSON := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectJSON, ".mcp.json"), []byte(`{
		"mcpServers": {
			"project-json": {"command": "project-json-bin"}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := MigrateMCPToUserConfigOnUpgrade([]string{projectTOML, projectJSON, projectTOML})
	if err != nil {
		t.Fatalf("MigrateMCPToUserConfigOnUpgrade: %v", err)
	}
	want := 2
	if legacyTOML != "" && !samePath(legacyTOML, dest) {
		want = 3
	}
	if res == nil || res.Added != want {
		t.Fatalf("migration result = %+v, want %d added", res, want)
	}
	cfg := LoadForEdit(dest)
	byName := map[string]PluginEntry{}
	for _, p := range cfg.Plugins {
		byName[p.Name] = p
	}
	for name, command := range map[string]string{
		"global":       "global-bin",
		"legacy-toml":  "legacy-toml-bin",
		"project-toml": "project-toml-bin",
		"project-json": "project-json-bin",
	} {
		if byName[name].Command != command {
			t.Fatalf("%s command = %q, want %q; plugins=%+v", name, byName[name].Command, command, cfg.Plugins)
		}
	}
	if _, err := os.Stat(mcpGlobalMigrationMarkerPath()); err != nil {
		t.Fatalf("migration marker missing: %v", err)
	}

	lateProject := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lateProject, ".corvus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lateProject, ".corvus", "config.toml"), []byte(`
[[plugins]]
name = "late"
command = "late-bin"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = MigrateMCPToUserConfigOnUpgrade([]string{lateProject})
	if err != nil {
		t.Fatalf("second migration: %v", err)
	}
	if res != nil {
		t.Fatalf("second migration result = %+v, want nil due marker", res)
	}
	if got := LoadForEdit(dest); len(got.Plugins) != len(cfg.Plugins) {
		t.Fatalf("second migration changed plugins: %+v", got.Plugins)
	}
}

func TestMigrateMCPToUserConfigOnUpgradeDoesNotMarkEmptyScan(t *testing.T) {
	_, _ = isolatedHome(t)
	res, err := MigrateMCPToUserConfigOnUpgrade(nil)
	if err != nil {
		t.Fatalf("MigrateMCPToUserConfigOnUpgrade: %v", err)
	}
	if res != nil {
		t.Fatalf("result = %+v, want nil", res)
	}
	if _, statErr := os.Stat(mcpGlobalMigrationMarkerPath()); !os.IsNotExist(statErr) {
		t.Fatalf("empty scan must not write marker, stat err=%v", statErr)
	}
}

func TestMigrateMCPToUserConfigOnUpgradeRefusesMalformedGlobalConfig(t *testing.T) {
	dest, _ := isolatedHome(t)
	const malformed = "[[plugins]\nname = \"broken\"\n"
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := MigrateMCPToUserConfigOnUpgrade(nil)
	if err == nil {
		t.Fatal("expected malformed global config to abort MCP migration")
	}
	if res != nil {
		t.Fatalf("result = %+v, want nil", res)
	}
	if got, readErr := os.ReadFile(dest); readErr != nil {
		t.Fatalf("read dest: %v", readErr)
	} else if string(got) != malformed {
		t.Fatalf("malformed config was overwritten:\n%s", got)
	}
	if _, statErr := os.Stat(mcpGlobalMigrationMarkerPath()); !os.IsNotExist(statErr) {
		t.Fatalf("failed migration must not write marker, stat err=%v", statErr)
	}
}

func TestMigrateMCPToUserConfigOnUpgradePreservesConfigVersion(t *testing.T) {
	dest, _ := isolatedHome(t)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("config_version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".corvus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".corvus", "config.toml"), []byte(`
[[plugins]]
name = "project"
command = "project-bin"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := MigrateMCPToUserConfigOnUpgrade([]string{project})
	if err != nil {
		t.Fatalf("MigrateMCPToUserConfigOnUpgrade: %v", err)
	}
	if res == nil || res.Added != 1 {
		t.Fatalf("result = %+v, want 1 added", res)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !strings.Contains(string(got), "config_version = 1") {
		t.Fatalf("MCP migration should not advance config_version:\n%s", got)
	}
}

func TestLoadFallsBackToLegacyOSConfigWhenPrimaryMissing(t *testing.T) {
	dest, _ := isolatedHome(t)
	legacy := legacyUserConfigPath()
	if legacy == "" {
		t.Skip("legacy OS config path matches primary path on this platform")
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`default_model = "legacy-provider/legacy-model"`), 0o644); err != nil {
		t.Fatal(err)
	}

	if source := SourcePath(); source != legacy {
		t.Fatalf("SourcePath() = %q, want legacy path %q", source, legacy)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultModel != "legacy-provider/legacy-model" {
		t.Fatalf("DefaultModel = %q, want legacy value", cfg.DefaultModel)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("Load fallback should not create primary config, stat err=%v", err)
	}
}

func TestLoadPrefersPrimaryConfigOverLegacyOSConfig(t *testing.T) {
	dest, _ := isolatedHome(t)
	legacy := legacyUserConfigPath()
	if legacy == "" {
		t.Skip("legacy OS config path matches primary path on this platform")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(`default_model = "primary-provider/primary-model"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`default_model = "legacy-provider/legacy-model"`), 0o644); err != nil {
		t.Fatal(err)
	}

	if source := SourcePath(); source != dest {
		t.Fatalf("SourcePath() = %q, want primary path %q", source, dest)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultModel != "primary-provider/primary-model" {
		t.Fatalf("DefaultModel = %q, want primary value", cfg.DefaultModel)
	}
}
