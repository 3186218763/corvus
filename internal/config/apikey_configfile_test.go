package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAPIConfigTestFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func apiProviderByName(t *testing.T, cfg *Config, name string) *ProviderEntry {
	t.Helper()
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == name {
			return &cfg.Providers[i]
		}
	}
	t.Fatalf("provider %q not found in %v", name, cfg.Providers)
	return nil
}

// api_key can be written directly on a [[providers]] entry in the project
// .corvus/config.toml; no environment variable is needed.
func TestProviderAPIKeyDirectInProjectCorvusConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	project := t.TempDir()
	writeAPIConfigTestFile(t, project, filepath.Join(".corvus", "config.toml"), `
[[providers]]
name     = "deepseek"
kind     = "openai"
base_url = "https://api.deepseek.com"
model    = "deepseek-v4-flash"
api_key  = "sk-project-direct"
`)

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	p := apiProviderByName(t, cfg, "deepseek")
	if got := p.EffectiveAPIKey(); got != "sk-project-direct" {
		t.Fatalf("EffectiveAPIKey = %q, want %q", got, "sk-project-direct")
	}
	if got := p.APIKeySourceLabel(); !strings.Contains(got, "config.toml") {
		t.Fatalf("APIKeySourceLabel = %q, want config.toml", got)
	}
}

// The user's ~/.corvus/config.toml wins over the project .corvus/config.toml.
func TestProviderAPIKeyUserWinsOverProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	writeAPIConfigTestFile(t, home, "config.toml", `
[[providers]]
name     = "deepseek"
kind     = "openai"
base_url = "https://api.deepseek.com"
model    = "deepseek-v4-flash"
api_key  = "sk-home-key"
`)
	project := t.TempDir()
	writeAPIConfigTestFile(t, project, filepath.Join(".corvus", "config.toml"), `
[[providers]]
name     = "deepseek"
kind     = "openai"
base_url = "https://api.deepseek.com"
model    = "deepseek-v4-flash"
api_key  = "sk-project-key"
`)

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	p := apiProviderByName(t, cfg, "deepseek")
	if got := p.EffectiveAPIKey(); got != "sk-home-key" {
		t.Fatalf("EffectiveAPIKey = %q, want user key %q", got, "sk-home-key")
	}
}

// A partial user entry (api_key only) keeps the project entry's endpoint/model
// fields while its key wins.
func TestProviderAPIKeyPartialHomeOverrideKeepsProjectFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	writeAPIConfigTestFile(t, home, "config.toml", `
[[providers]]
name    = "deepseek"
api_key = "sk-home-partial"
`)
	project := t.TempDir()
	writeAPIConfigTestFile(t, project, filepath.Join(".corvus", "config.toml"), `
[[providers]]
name     = "deepseek"
kind     = "openai"
base_url = "https://project.example.com/v1"
model    = "deepseek-v4-flash"
api_key  = "sk-project-key"
`)

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	p := apiProviderByName(t, cfg, "deepseek")
	if got := p.EffectiveAPIKey(); got != "sk-home-partial" {
		t.Fatalf("EffectiveAPIKey = %q, want home key %q", got, "sk-home-partial")
	}
	if p.BaseURL != "https://project.example.com/v1" {
		t.Fatalf("BaseURL = %q, want project endpoint kept", p.BaseURL)
	}
	if p.Kind != "openai" || p.Model != "deepseek-v4-flash" {
		t.Fatalf("kind/model not kept from project entry: %+v", p)
	}
}

// An api_key-only entry can target a built-in provider without re-declaring
// its endpoint or model.
func TestProviderAPIKeyPartialOverrideBuiltinDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	writeAPIConfigTestFile(t, home, "config.toml", `
default_model = "deepseek-flash"

[[providers]]
name    = "deepseek-flash"
api_key = "sk-builtin-key"
`)
	project := t.TempDir()

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	p := apiProviderByName(t, cfg, "deepseek-flash")
	if got := p.EffectiveAPIKey(); got != "sk-builtin-key" {
		t.Fatalf("EffectiveAPIKey = %q, want %q", got, "sk-builtin-key")
	}
	if p.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("BaseURL = %q, want built-in endpoint filled", p.BaseURL)
	}
	if p.Model != "deepseek-v4-flash" {
		t.Fatalf("Model = %q, want built-in model filled", p.Model)
	}
	if err := cfg.Validate("deepseek-flash"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// A legacy ./corvus.toml at the project root is no longer read: only
// .corvus/config.toml is.
func TestProjectConfigIgnoresLegacyCorvusTOML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	project := t.TempDir()
	writeAPIConfigTestFile(t, project, "corvus.toml", `
default_model = "legacy-model"

[[providers]]
name     = "legacy"
kind     = "openai"
base_url = "https://legacy.example.com/v1"
model    = "legacy-model"
api_key  = "sk-legacy"
`)
	writeAPIConfigTestFile(t, project, filepath.Join(".corvus", "config.toml"), `
default_model = "canonical-model"

[[providers]]
name     = "canonical"
kind     = "openai"
base_url = "https://canonical.example.com/v1"
model    = "canonical-model"
api_key  = "sk-canonical"
`)

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if cfg.DefaultModel != "canonical-model" {
		t.Fatalf("DefaultModel = %q, want .corvus/config.toml to win", cfg.DefaultModel)
	}
	if _, ok := cfg.Provider("legacy"); ok {
		t.Fatalf("legacy corvus.toml provider should be ignored: %v", cfg.Providers)
	}
	if got := SourcePathForRoot(project); !strings.HasSuffix(got, filepath.Join(".corvus", "config.toml")) {
		t.Fatalf("SourcePathForRoot = %q, want .corvus/config.toml", got)
	}
}

// ProjectConfigPathForRoot always points at .corvus/config.toml.
func TestProjectConfigPathForRootAlwaysCanonical(t *testing.T) {
	project := t.TempDir()
	writeAPIConfigTestFile(t, project, "corvus.toml", "")
	if got := ProjectConfigPathForRoot(project); got != filepath.Join(project, ".corvus", "config.toml") {
		t.Fatalf("ProjectConfigPathForRoot = %q, want .corvus/config.toml", got)
	}
	empty := t.TempDir()
	if got := ProjectConfigPathForRoot(empty); got != filepath.Join(empty, ".corvus", "config.toml") {
		t.Fatalf("ProjectConfigPathForRoot = %q, want .corvus/config.toml", got)
	}
}
