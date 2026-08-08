package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func isolateUserConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"CORVUS_HOME", "CORVUS_STATE_HOME", "CORVUS_CACHE_HOME"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}
	t.Setenv("CORVUS_CREDENTIALS_STORE", "file")
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	return home
}

// setRuntimeGOOS overrides the package-level runtimeGOOS for one test. The
// t.Setenv call is a guard: it panics if the test also uses t.Parallel, which
// would otherwise race on the shared global.
func setRuntimeGOOS(t *testing.T, goos string) {
	t.Helper()
	t.Setenv("CORVUS_TEST_GOOS", goos)
	old := runtimeGOOS
	runtimeGOOS = goos
	t.Cleanup(func() { runtimeGOOS = old })
}

func expectedDefaultCorvusHome(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming", "corvus")
	}
	return filepath.Join(home, ".corvus")
}

func TestUserConfigDisplayPathCollapsesHome(t *testing.T) {
	home := isolateUserConfigHome(t)
	got := userConfigDisplayPath()
	if !strings.HasPrefix(got, "~/") {
		t.Fatalf("display path = %q, want ~/ prefix", got)
	}
	if !strings.HasSuffix(got, "corvus/config.toml") {
		t.Fatalf("display path = %q, want corvus/config.toml suffix", got)
	}
	if strings.Contains(got, home) {
		t.Fatalf("display path %q must not embed the absolute home", got)
	}
}

func TestUserConfigPathUsesCorvusHome(t *testing.T) {
	home := isolateUserConfigHome(t)
	want := filepath.Join(expectedDefaultCorvusHome(home), "config.toml")
	if got := UserConfigPath(); filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("UserConfigPath() = %q, want %q", got, want)
	}
}

func TestCorvusManagedConfigPathsAreConfigFilesOnly(t *testing.T) {
	home := isolateUserConfigHome(t)
	setRuntimeGOOS(t, "windows")
	oldConfigDir := osUserConfigDir
	osUserConfigDir = func() string { return filepath.Join(home, "AppData", "Roaming") }
	t.Cleanup(func() { osUserConfigDir = oldConfigDir })

	paths := CorvusManagedConfigPaths()
	for _, want := range []string{
		filepath.Join(home, "AppData", "Roaming", "corvus", "config.toml"),
		filepath.Join(home, ".corvus", "config.json"),
	} {
		found := false
		for _, got := range paths {
			if samePath(got, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("managed config paths = %v, want %s", paths, want)
		}
	}
	// The escape hatch is file-level by contract: no directories, and none of
	// the sensitive Corvus-home siblings (credentials, hooks, skills,
	// sessions) may ride along.
	for _, got := range paths {
		if base := filepath.Base(got); base != "config.toml" && base != "config.json" {
			t.Fatalf("managed config path %q is not a known config file (paths must be files, not directories): %v", got, paths)
		}
		for _, forbidden := range []string{
			home,
			CorvusHomeDir(),
			UserCredentialsPath(),
			filepath.Join(CorvusHomeDir(), "settings.json"),
			filepath.Join(CorvusHomeDir(), "skills"),
			filepath.Join(CorvusHomeDir(), "sessions"),
		} {
			if samePath(got, forbidden) {
				t.Fatalf("managed config paths must not include %q: %v", forbidden, paths)
			}
		}
	}
}

func TestUserConfigPathHonorsCorvusHome(t *testing.T) {
	home := isolateUserConfigHome(t)
	custom := filepath.Join(home, "custom-home")
	t.Setenv("CORVUS_HOME", custom)

	want := filepath.Join(custom, "config.toml")
	if got := UserConfigPath(); filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("UserConfigPath() = %q, want %q", got, want)
	}
}

func TestLoadForRootUsesWindowsHomeFallbackWhenConfigDirUnavailable(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	oldGOOS := runtimeGOOS
	oldConfigDir := osUserConfigDir
	oldHomeDir := osUserHomeDir
	runtimeGOOS = "windows"
	osUserConfigDir = func() string { return "" }
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() {
		runtimeGOOS = oldGOOS
		osUserConfigDir = oldConfigDir
		osUserHomeDir = oldHomeDir
	})

	t.Setenv("CORVUS_HOME", "")

	configPath := filepath.Join(home, "AppData", "Roaming", "corvus", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("default_model = \"custom/from-home\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot() error = %v", err)
	}
	if cfg.DefaultModel != "custom/from-home" {
		t.Fatalf("DefaultModel = %q, want %q", cfg.DefaultModel, "custom/from-home")
	}
}

func TestRenderTOMLHeaderShowsResolvedConfigPath(t *testing.T) {
	isolateUserConfigHome(t)
	out := RenderTOML(Default())
	want := "> " + userConfigDisplayPath() + " > built-in defaults."
	if !strings.Contains(out, want) {
		t.Fatalf("rendered header missing resolved config path %q", want)
	}
}

func TestWriteRootsForRootExcludesUserConfigDirByDefault(t *testing.T) {
	isolateUserConfigHome(t)
	project := t.TempDir()
	cfg := Default()

	roots := cfg.WriteRootsForRoot(project)
	want := filepath.Clean(filepath.Dir(UserConfigPath()))
	for _, root := range roots {
		if filepath.Clean(root) == want {
			t.Fatalf("WriteRootsForRoot() = %v, must not include user config dir %q by default", roots, want)
		}
	}
	if got := filepath.Clean(roots[0]); got != filepath.Clean(project) {
		t.Fatalf("first write root = %q, want project %q", got, project)
	}
}

// TestRenderTOMLRoundTrips ensures the annotated TOML we emit parses back into
// an equivalent config — i.e. the wizard never writes a file it can't read.
func TestRenderTOMLRoundTrips(t *testing.T) {
	orig := Default()
	orig.Providers = append(orig.Providers, legacyMimoCustomProvider("mimo-pro"))
	orig.DefaultModel = "mimo-pro"
	orig.Language = "zh"
	orig.UI.Theme = "light"
	orig.UI.ThemeStyle = "glacier"
	orig.UI.ShortcutLayout = "desktop"
	orig.UI.CursorShape = "bar"
	orig.UI.Currency = "CNY"
	orig.UI.ProviderAccess = []string{"deepseek-flash", "mimo-pro"}
	orig.Agent.RecoveryModel = "mimo-pro"
	orig.Agent.RecoveryTemperature = 0.15
	orig.Agent.ReasoningLanguage = "zh"
	orig.Agent.ToolResultSnipRatio = 0.65
	orig.Agent.SubagentModel = "mimo-pro"
	orig.Agent.SubagentModels = map[string]string{"review": "deepseek-pro"}
	orig.Agent.MaxSubagentDepth = 3
	orig.Agent.Keep = []string{"errors", "user_marked"}
	orig.Agent.RecentKeep = 4
	orig.Tools.BashTimeoutSeconds = intPtr(900)
	orig.Tools.BackgroundJobs.StalledWarningSeconds = intPtr(30)
	orig.Tools.Shell.Prefer = "bash"
	orig.Tools.Shell.Path = "/usr/local/bin/bash"
	orig.Permissions = PermissionsConfig{
		Mode:             "deny",
		Deny:             []string{"Bash(rm -rf*)"},
		Allow:            []string{"Bash(go test:*)", "read_file"},
		AllowDynamicBash: true,
	}
	orig.Network = NetworkConfig{
		ProxyMode: "custom",
		NoProxy:   "localhost,127.0.0.1",
		Proxy: NetworkProxyConfig{
			Type:     "socks5",
			Server:   "127.0.0.1",
			Port:     7890,
			Username: "user",
			Password: "${CORVUS_PROXY_PASSWORD}",
		},
	}
	orig.Environment.Enabled = boolPtr(false)
	orig.Environment.Tools = map[string]string{"go": "/opt/homebrew/bin/go", "python3": "~/.pyenv/shims/python3"}
	orig.Skills.Paths = []string{"~/my-skills", "../shared/skills"}
	orig.Skills.ExcludedPaths = []string{"~/.agents/skills"}
	orig.Skills.DisabledSkills = []string{"review", "explore"}
	orig.Skills.MaxDepth = 2
	orig.LSP = LSPConfig{
		Enabled: true,
		Servers: map[string]LSPServer{
			"lua": {
				Command:     "lua-language-server",
				Args:        []string{"--stdio"},
				Env:         map[string]string{"LUA_PATH": "./?.lua"},
				LanguageID:  "lua",
				Extensions:  []string{".lua", ".script", ".gui_script"},
				InstallHint: "install lua-language-server",
			},
		},
	}
	orig.Plugins = []PluginEntry{
		{Name: "example", Command: "corvus-plugin-example"},
		{Name: "stripe", Type: "http", URL: "https://mcp.stripe.com", Headers: map[string]string{"Authorization": "Bearer x"}, AutoStart: boolPtr(false), Tier: "background"},
	}
	mm, _ := orig.Provider("mimo-pro")
	mm.BaseURL = "http://localhost:8000/v1"
	mm.ChatURL = "http://localhost:8000/v1/chat/completions"
	mm.ModelsURL = "http://localhost:8000/v1/models"
	mm.ReasoningProtocol = "openai"
	mm.PresetID = "mimo-api"
	mm.PresetVersion = ProviderPresetVersion
	ds, _ := orig.Provider("deepseek-flash")
	ds.Effort = "max"

	rendered := RenderTOML(orig)

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n---\n%s", err, rendered)
	}

	if got.DefaultModel != "mimo-pro" {
		t.Errorf("default_model = %q, want mimo-pro", got.DefaultModel)
	}
	if got.ConfigVersion != 5 {
		t.Errorf("config_version = %d, want 5", got.ConfigVersion)
	}
	if got.Language != "zh" {
		t.Errorf("language = %q, want zh", got.Language)
	}
	if got.UI.Theme != "light" {
		t.Errorf("ui.theme = %q, want light", got.UI.Theme)
	}
	if got.UI.ThemeStyle != "glacier" {
		t.Errorf("ui.theme_style = %q, want glacier", got.UI.ThemeStyle)
	}
	if got.UI.ShortcutLayout != "desktop" {
		t.Errorf("ui.shortcut_layout = %q, want desktop", got.UI.ShortcutLayout)
	}
	if got.UICursorShape() != "bar" {
		t.Errorf("ui.cursor_shape = %q, want bar (explicit config preserved)", got.UICursorShape())
	}
	if got.PricingCurrency() != "CNY" {
		t.Errorf("ui.currency = %q, want CNY", got.PricingCurrency())
	}
	if want := []string{"deepseek-flash", "mimo-pro"}; !reflect.DeepEqual(got.UI.ProviderAccess, want) {
		t.Errorf("ui.provider_access = %v, want %v", got.UI.ProviderAccess, want)
	}
	if got.Agent.RecoveryModel != "mimo-pro" || got.Agent.RecoveryTemperature != 0 {
		t.Errorf("agent recovery settings not preserved: %+v", got.Agent)
	}
	if got.Agent.MaxSteps != orig.Agent.MaxSteps {
		t.Errorf("max_steps = %d, want %d", got.Agent.MaxSteps, orig.Agent.MaxSteps)
	}
	if got.Agent.PlannerMaxSteps != orig.Agent.PlannerMaxSteps {
		t.Errorf("planner_max_steps = %d, want %d", got.Agent.PlannerMaxSteps, orig.Agent.PlannerMaxSteps)
	}
	if got.Agent.Temperature != orig.Agent.Temperature {
		t.Errorf("temperature = %v, want %v", got.Agent.Temperature, orig.Agent.Temperature)
	}
	if got.Agent.ReasoningLanguage != "zh" {
		t.Errorf("reasoning_language = %q, want zh", got.Agent.ReasoningLanguage)
	}
	if got.Agent.SoftCompactRatio != orig.Agent.SoftCompactRatio {
		t.Errorf("soft_compact_ratio = %v, want %v", got.Agent.SoftCompactRatio, orig.Agent.SoftCompactRatio)
	}
	if got.Agent.ToolResultSnipRatio != orig.Agent.ToolResultSnipRatio {
		t.Errorf("tool_result_snip_ratio = %v, want %v", got.Agent.ToolResultSnipRatio, orig.Agent.ToolResultSnipRatio)
	}
	if got.Agent.CompactRatio != orig.Agent.CompactRatio {
		t.Errorf("compact_ratio = %v, want %v", got.Agent.CompactRatio, orig.Agent.CompactRatio)
	}
	if got.Agent.CompactForceRatio != orig.Agent.CompactForceRatio {
		t.Errorf("compact_force_ratio = %v, want %v", got.Agent.CompactForceRatio, orig.Agent.CompactForceRatio)
	}
	if strings.Join(got.Agent.Keep, ",") != strings.Join(orig.Agent.Keep, ",") {
		t.Errorf("keep = %v, want %v", got.Agent.Keep, orig.Agent.Keep)
	}
	if got.Agent.RecentKeep != orig.Agent.RecentKeep {
		t.Errorf("recent_keep = %d, want %d", got.Agent.RecentKeep, orig.Agent.RecentKeep)
	}
	if got.Agent.SystemPrompt != orig.Agent.SystemPrompt {
		t.Errorf("system_prompt mismatch:\n got %q\nwant %q", got.Agent.SystemPrompt, orig.Agent.SystemPrompt)
	}
	if !got.LSP.Enabled {
		t.Error("lsp.enabled = false, want true")
	}
	if got.Environment.Enabled == nil || *got.Environment.Enabled {
		t.Errorf("environment.enabled = %+v, want false", got.Environment.Enabled)
	}
	if !reflect.DeepEqual(got.Environment.Tools, orig.Environment.Tools) {
		t.Errorf("environment.tools = %v, want %v", got.Environment.Tools, orig.Environment.Tools)
	}
	lua := got.LSP.Servers["lua"]
	if lua.Command != "lua-language-server" || lua.LanguageID != "lua" || lua.InstallHint != "install lua-language-server" {
		t.Errorf("lsp.servers.lua scalar fields not preserved: %+v", lua)
	}
	if len(lua.Args) != 1 || lua.Args[0] != "--stdio" {
		t.Errorf("lsp.servers.lua.args = %v, want [--stdio]", lua.Args)
	}
	if lua.Env["LUA_PATH"] != "./?.lua" {
		t.Errorf("lsp.servers.lua.env = %v, want LUA_PATH", lua.Env)
	}
	if len(lua.Extensions) != 3 || lua.Extensions[2] != ".gui_script" {
		t.Errorf("lsp.servers.lua.extensions = %v", lua.Extensions)
	}
	if got.Agent.SubagentModel != "mimo-pro" {
		t.Errorf("subagent_model = %q, want mimo-pro", got.Agent.SubagentModel)
	}
	if got.Agent.SubagentModels["review"] != "deepseek-pro" {
		t.Errorf("subagent_models.review = %q, want deepseek-pro", got.Agent.SubagentModels["review"])
	}
	if got.Agent.MaxSubagentDepth != 3 {
		t.Errorf("max_subagent_depth = %d, want 3", got.Agent.MaxSubagentDepth)
	}
	if got.Tools.BashTimeoutSeconds == nil || *got.Tools.BashTimeoutSeconds != 900 {
		t.Errorf("tools.bash_timeout_seconds = %v, want 900", got.Tools.BashTimeoutSeconds)
	}
	if got.Tools.BackgroundJobs.StalledWarningSeconds == nil || *got.Tools.BackgroundJobs.StalledWarningSeconds != 30 {
		t.Errorf("tools.background_jobs.stalled_warning_seconds = %v, want 30", got.Tools.BackgroundJobs.StalledWarningSeconds)
	}
	if got.Tools.Shell.Prefer != "bash" {
		t.Errorf("tools.shell.prefer = %q, want bash", got.Tools.Shell.Prefer)
	}
	if got.Tools.Shell.Path != "/usr/local/bin/bash" {
		t.Errorf("tools.shell.path = %q, want /usr/local/bin/bash", got.Tools.Shell.Path)
	}
	if g, _ := got.Provider("mimo-pro"); g == nil || g.BaseURL != "http://localhost:8000/v1" || g.ChatURL != "http://localhost:8000/v1/chat/completions" || g.ModelsURL != "http://localhost:8000/v1/models" || g.ReasoningProtocol != "openai" {
		t.Errorf("mimo-pro endpoint fields not preserved: %+v", g)
	}
	if g, _ := got.Provider("mimo-pro"); g == nil || g.PresetID != "mimo-api" || g.PresetVersion != ProviderPresetVersion {
		t.Errorf("mimo-pro preset metadata not preserved: %+v", g)
	}
	if g, _ := got.Provider("deepseek-flash"); g == nil || g.Effort != "max" {
		t.Errorf("deepseek-flash effort not preserved: %+v", g)
	}
	if len(got.Providers) != len(orig.Providers) {
		t.Errorf("providers count = %d, want %d", len(got.Providers), len(orig.Providers))
	}
	if got.Permissions.Mode != "deny" {
		t.Errorf("permissions.mode = %q, want deny", got.Permissions.Mode)
	}
	if len(got.Permissions.Deny) != 1 || got.Permissions.Deny[0] != "Bash(rm -rf*)" {
		t.Errorf("permissions.deny = %v, want [Bash(rm -rf*)]", got.Permissions.Deny)
	}
	if len(got.Permissions.Allow) != 2 {
		t.Errorf("permissions.allow = %v, want 2 entries", got.Permissions.Allow)
	}
	if got.Network.ProxyMode != "custom" || got.Network.Proxy.Type != "socks5" || got.Network.Proxy.Port != 7890 {
		t.Errorf("network proxy not preserved: %+v", got.Network)
	}
	if len(got.Skills.Paths) != 2 || got.Skills.Paths[0] != "~/my-skills" {
		t.Errorf("skills.paths = %v", got.Skills.Paths)
	}
	if len(got.Skills.ExcludedPaths) != 1 || got.Skills.ExcludedPaths[0] != "~/.agents/skills" {
		t.Errorf("skills.excluded_paths = %v", got.Skills.ExcludedPaths)
	}
	if len(got.Skills.DisabledSkills) != 2 || got.Skills.DisabledSkills[0] != "review" || got.Skills.DisabledSkills[1] != "explore" {
		t.Errorf("skills.disabled_skills = %v", got.Skills.DisabledSkills)
	}
	if got.SkillMaxDepth() != 2 {
		t.Errorf("skills.max_depth = %d, want 2", got.SkillMaxDepth())
	}
	if len(got.Plugins) != 2 {
		t.Fatalf("plugins count = %d, want 2", len(got.Plugins))
	}
	stripe := got.Plugins[1]
	if stripe.Name != "stripe" || stripe.Type != "http" || stripe.URL != "https://mcp.stripe.com" {
		t.Errorf("http plugin not preserved: %+v", stripe)
	}
	if stripe.Headers["Authorization"] != "Bearer x" {
		t.Errorf("plugin headers not preserved: %v", stripe.Headers)
	}
	if strings.Contains(rendered, "trusted_read_only_tools") {
		t.Errorf("removed plugin reader setting survived render: entry=%+v\n%s", stripe, rendered)
	}
	if stripe.AutoStart == nil || *stripe.AutoStart {
		t.Errorf("auto_start should render and parse as false, got %+v", stripe.AutoStart)
	}
	if stripe.Tier != "" {
		t.Errorf("plugin tier should be omitted from new config, got %q", stripe.Tier)
	}
	if strings.Contains(rendered, "\ntier") {
		t.Errorf("rendered config should not contain MCP tier fields:\n%s", rendered)
	}
}

func TestRenderTOMLDocumentsPlanModeReadOnlyCommands(t *testing.T) {
	cfg := Default()
	cfg.Agent.PlanModeReadOnlyCommands = []string{"gh issue view"}

	rendered := RenderTOML(cfg)
	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n%s", err, rendered)
	}
	if !strings.Contains(rendered, `plan_mode_read_only_commands = ["gh issue view"]`) {
		t.Fatalf("rendered config should preserve plan_mode_read_only_commands:\n%s", rendered)
	}
	if !strings.Contains(rendered, "legacy compatibility only") || !strings.Contains(rendered, "Plan bash uses Permissions") {
		t.Fatalf("rendered config should document legacy plan_mode_read_only_commands semantics:\n%s", rendered)
	}
	if !reflect.DeepEqual(got.Agent.PlanModeReadOnlyCommands, cfg.Agent.PlanModeReadOnlyCommands) {
		t.Fatalf("PlanModeReadOnlyCommands round trip = %v, want %v", got.Agent.PlanModeReadOnlyCommands, cfg.Agent.PlanModeReadOnlyCommands)
	}
}

func TestRenderTOMLDropsRetiredMCPPolicyFields(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`[[plugins]]
name = "github"
command = "github-mcp"
trusted_read_only_tools = ["issue_read", "pull_request_read"]
default_tools_approval_mode = "writes"
approvals_reviewer = "auto_review"

[plugins.tools.wipe]
approval_mode = "prompt"
`, &cfg); err != nil {
		t.Fatalf("legacy config should still decode: %v", err)
	}

	rendered := RenderTOML(&cfg)
	for _, retired := range []string{"trusted_read_only_tools", "default_tools_approval_mode", "approvals_reviewer", "\napproval_mode ="} {
		if strings.Contains(rendered, retired) {
			t.Fatalf("rendered config retained retired MCP field %q:\n%s", retired, rendered)
		}
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n%s", err, rendered)
	}
}

func TestRenderTOMLPreservesMCPTimeouts(t *testing.T) {
	cfg := Default()
	cfg.Tools.MCPCallTimeoutSeconds = intPtr(450)
	cfg.Tools.MCPStartupTimeoutSeconds = intPtr(45)
	cfg.Plugins = []PluginEntry{{
		Name:                  "maker",
		Command:               "maker-mcp",
		StartupTimeoutSeconds: 60,
		CallTimeoutSeconds:    600,
		ToolTimeoutSeconds: map[string]int{
			"generate/video": 1800,
			"search":         120,
		},
	}}

	rendered := RenderTOML(cfg)
	for _, want := range []string{
		"mcp_call_timeout_seconds = 450",
		"mcp_startup_timeout_seconds = 45",
		"startup_timeout_seconds = 60",
		"call_timeout_seconds = 600",
		`tool_timeout_seconds = { "generate/video" = 1800, "search" = 120 }`,
		"Raw MCP tool names",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, rendered)
		}
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n%s", err, rendered)
	}
	if got.Tools.MCPCallTimeoutSeconds == nil || *got.Tools.MCPCallTimeoutSeconds != 450 {
		t.Fatalf("MCPCallTimeoutSeconds round trip = %v, want 450", got.Tools.MCPCallTimeoutSeconds)
	}
	if got.Tools.MCPStartupTimeoutSeconds == nil || *got.Tools.MCPStartupTimeoutSeconds != 45 {
		t.Fatalf("MCPStartupTimeoutSeconds round trip = %v, want 45", got.Tools.MCPStartupTimeoutSeconds)
	}
	if got.Plugins[0].StartupTimeoutSeconds != 60 {
		t.Fatalf("StartupTimeoutSeconds round trip = %d, want 60", got.Plugins[0].StartupTimeoutSeconds)
	}
	if got.Plugins[0].CallTimeoutSeconds != 600 {
		t.Fatalf("CallTimeoutSeconds round trip = %d, want 600", got.Plugins[0].CallTimeoutSeconds)
	}
	if !reflect.DeepEqual(got.Plugins[0].ToolTimeoutSeconds, cfg.Plugins[0].ToolTimeoutSeconds) {
		t.Fatalf("ToolTimeoutSeconds round trip = %v, want %v", got.Plugins[0].ToolTimeoutSeconds, cfg.Plugins[0].ToolTimeoutSeconds)
	}
}

func TestScopedRenderPreservesLSPConfig(t *testing.T) {
	const src = `
config_version = 4
default_model = "mimo"

[lsp]
enabled = true

[lsp.servers.lua]
command = "lua-language-server"
args = ["--stdio"]
env = { LUA_PATH = "./?.lua" }
language_id = "lua"
extensions = [".lua", ".script", ".gui_script"]
install_hint = "install lua-language-server"

[lsp.servers."c++"]
command = "clangd"
extensions = [".cc", ".cpp", ".hpp"]
`

	var cfg Config
	if _, err := toml.Decode(src, &cfg); err != nil {
		t.Fatalf("decode source TOML: %v", err)
	}

	for _, scope := range []RenderScope{RenderScopeFull, RenderScopeUser, RenderScopeProject} {
		t.Run(string(scope), func(t *testing.T) {
			rendered := RenderTOMLForScope(&cfg, scope)
			if !strings.Contains(rendered, "[lsp]") {
				t.Fatalf("render missing [lsp]:\n%s", rendered)
			}
			if !strings.Contains(rendered, "[lsp.servers.lua]") {
				t.Fatalf("render missing [lsp.servers.lua]:\n%s", rendered)
			}
			if !strings.Contains(rendered, `[lsp.servers."c++"]`) {
				t.Fatalf("render missing quoted c++ server key:\n%s", rendered)
			}

			var got Config
			if _, err := toml.Decode(rendered, &got); err != nil {
				t.Fatalf("decode rendered TOML: %v\n---\n%s", err, rendered)
			}
			if !got.LSP.Enabled {
				t.Fatalf("lsp.enabled = false, want true")
			}
			lua, ok := got.LSP.Servers["lua"]
			if !ok {
				t.Fatalf("lsp.servers.lua missing after round-trip: %+v", got.LSP.Servers)
			}
			if lua.Command != "lua-language-server" || lua.LanguageID != "lua" || lua.InstallHint != "install lua-language-server" {
				t.Fatalf("lsp.servers.lua scalar fields not preserved: %+v", lua)
			}
			if len(lua.Args) != 1 || lua.Args[0] != "--stdio" {
				t.Fatalf("lsp.servers.lua.args = %v, want [--stdio]", lua.Args)
			}
			if lua.Env["LUA_PATH"] != "./?.lua" {
				t.Fatalf("lsp.servers.lua.env = %v, want LUA_PATH", lua.Env)
			}
			if len(lua.Extensions) != 3 || lua.Extensions[0] != ".lua" || lua.Extensions[2] != ".gui_script" {
				t.Fatalf("lsp.servers.lua.extensions = %v", lua.Extensions)
			}
			cpp, ok := got.LSP.Servers["c++"]
			if !ok {
				t.Fatalf("lsp.servers.c++ missing after round-trip: %+v", got.LSP.Servers)
			}
			if cpp.Command != "clangd" || len(cpp.Extensions) != 3 || cpp.Extensions[1] != ".cpp" {
				t.Fatalf("lsp.servers.c++ not preserved: %+v", cpp)
			}
		})
	}
}

func BenchmarkRenderTOMLWithLSPServers(b *testing.B) {
	cfg := Default()
	cfg.LSP.Servers = make(map[string]LSPServer, 64)
	for i := 0; i < 64; i++ {
		lang := "lang" + strconv.Itoa(i)
		cfg.LSP.Servers[lang] = LSPServer{
			Command:     "server-" + strconv.Itoa(i),
			Args:        []string{"--stdio", "--flag"},
			Env:         map[string]string{"SERVER_MODE": "stdio", "SERVER_ROOT": "."},
			LanguageID:  lang,
			Extensions:  []string{"." + lang, "." + lang + "x"},
			InstallHint: "install server-" + strconv.Itoa(i),
		}
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rendered := RenderTOML(cfg)
		if len(rendered) == 0 {
			b.Fatal("empty render")
		}
	}
}

func TestScopedRenderSeparatesUserAndProjectConfig(t *testing.T) {
	c := Default()
	c.Language = "zh"
	c.UI.Currency = "CNY"
	c.UI.Theme = "dark"
	c.UI.ThemeStyle = "graphite"
	c.Agent.RecoveryModel = "deepseek-pro"
	c.Agent.RecoveryTemperature = 0.2

	user := RenderTOMLForScope(c, RenderScopeUser)
	for _, want := range []string{"config_version = 5", "[ui]", `currency = "CNY"`, `theme = "dark"`, `recovery_model = "deepseek-pro"`, "[tools.shell]"} {
		if !strings.Contains(user, want) {
			t.Fatalf("user render missing %q:\n%s", want, user)
		}
	}
	project := RenderTOMLForScope(c, RenderScopeProject)
	for _, forbidden := range []string{"default_auto_recovery_checkpoint =", "check_updates =", "max_steps", "planner_max_steps"} {
		if strings.Contains(project, forbidden) {
			t.Fatalf("project render should not contain %q:\n%s", forbidden, project)
		}
	}
	var projectConfig Config
	if _, err := toml.Decode(project, &projectConfig); err != nil {
		t.Fatalf("decode project render: %v\n%s", err, project)
	}
	if got := projectConfig.PricingCurrency(); got != "" {
		t.Fatalf("project render pricing currency = %q, want user-level setting omitted", got)
	}
	for _, retired := range []string{"default_auto_recovery_checkpoint", "auto_recovery_checkpoint"} {
		if strings.Contains(user, retired) || strings.Contains(project, retired) {
			t.Fatalf("retired Auto Guard key %q must not be rendered:\nuser:\n%s\nproject:\n%s", retired, user, project)
		}
	}
	if strings.Contains(project, "\nsystem_prompt = \"\"\"") {
		t.Fatalf("project render should not pin the built-in system prompt:\n%s", project)
	}
	if !strings.Contains(project, "# system_prompt =") {
		t.Fatalf("project render should leave a system prompt hint:\n%s", project)
	}
	for _, want := range []string{`recovery_model = "deepseek-pro"`} {
		if !strings.Contains(project, want) {
			t.Fatalf("project render missing %q:\n%s", want, project)
		}
	}
	if strings.Contains(user, "auto_plan") || strings.Contains(project, "auto_plan") {
		t.Fatalf("retired auto-plan keys must not be rendered:\nuser:\n%s\nproject:\n%s", user, project)
	}
	if strings.Contains(user, "recovery_temperature") || strings.Contains(project, "recovery_temperature") {
		t.Fatalf("deprecated recovery_temperature must not be rendered:\nuser:\n%s\nproject:\n%s", user, project)
	}
}

func TestScopedRenderKeepsPluginsInTheirOwningConfig(t *testing.T) {
	cfg := Default()
	cfg.Plugins = []PluginEntry{
		{Name: "unknown", Command: "unknown-mcp"},
		{Name: "user", Command: "user-mcp", Source: MCPSourceUserConfig},
		{Name: "project", Command: "project-mcp", Source: MCPSourceProjectConfig},
		{Name: "mcp-json", Command: "json-mcp", Source: MCPSourceProjectMCPJSON},
		{Name: "legacy", Command: "legacy-mcp", Source: MCPSourceLegacyUser},
		{Name: "package", Command: "package-mcp", Source: MCPSourcePluginPackage},
	}

	tests := []struct {
		name  string
		body  string
		want  []string
		avoid []string
	}{
		{name: "full", body: RenderTOMLForScope(cfg, RenderScopeFull), want: []string{"unknown", "user", "project", "mcp-json", "legacy", "package"}},
		{name: "user", body: RenderTOMLForScope(cfg, RenderScopeUser), want: []string{"unknown", "user"}, avoid: []string{"project", "mcp-json", "legacy", "package"}},
		{name: "project", body: RenderTOMLForScope(cfg, RenderScopeProject), want: []string{"unknown", "project"}, avoid: []string{"user", "mcp-json", "legacy", "package"}},
		{name: "project delta", body: RenderTOMLProjectDelta(cfg), want: []string{"unknown", "project"}, avoid: []string{"user", "mcp-json", "legacy", "package"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range tt.want {
				if !strings.Contains(tt.body, `name    = "`+name+`"`) {
					t.Fatalf("render missing plugin %q:\n%s", name, tt.body)
				}
			}
			for _, name := range tt.avoid {
				if strings.Contains(tt.body, `name    = "`+name+`"`) {
					t.Fatalf("render leaked plugin %q:\n%s", name, tt.body)
				}
			}
		})
	}
}

func TestProjectDeltaRendersRecoveryReviewerOverride(t *testing.T) {
	c := Default()
	c.Agent.RecoveryModel = "deepseek-pro"
	c.Agent.RecoveryTemperature = 0.2

	delta := RenderTOMLProjectDelta(c)
	for _, want := range []string{"[agent]", `recovery_model = "deepseek-pro"`} {
		if !strings.Contains(delta, want) {
			t.Fatalf("project delta missing %q:\n%s", want, delta)
		}
	}
	if strings.Contains(delta, "recovery_temperature") {
		t.Fatalf("deprecated recovery_temperature rendered:\n%s", delta)
	}
}

func TestProjectDeltaRendersToolsShellOverrides(t *testing.T) {
	c := Default()
	c.Tools.Shell.Prefer = "bash"
	c.Tools.Shell.Path = "/usr/local/bin/bash"

	delta := RenderTOMLProjectDelta(c)
	for _, want := range []string{"[tools.shell]", `prefer = "bash"`, `path = "/usr/local/bin/bash"`} {
		if !strings.Contains(delta, want) {
			t.Fatalf("project delta missing %q:\n%s", want, delta)
		}
	}
	if strings.Contains(delta, "[tools]\n\n") {
		t.Fatalf("project delta should not emit an empty [tools] block:\n%s", delta)
	}

	got := Default()
	if _, err := toml.Decode(delta, got); err != nil {
		t.Fatalf("decode project delta: %v\n%s", err, delta)
	}
	if got.Tools.Shell.Prefer != "bash" || got.Tools.Shell.Path != "/usr/local/bin/bash" {
		t.Fatalf("tools.shell = %+v, want bash with path", got.Tools.Shell)
	}
}

func TestResponsesProviderModeRoundTripsInUserAndProjectRender(t *testing.T) {
	legacyFalse := false
	cfg := Default()
	cfg.Providers = append(cfg.Providers, ProviderEntry{
		Name: "responses-test", Kind: "responses", BaseURL: "https://example.com/v1",
		Model: "model", APIKeyEnv: "RESPONSES_API_KEY",
		ResponsesMode: "stateful", ResponsesStateful: &legacyFalse,
	})

	for _, rendered := range []string{RenderTOMLForScope(cfg, RenderScopeUser), RenderTOMLProjectDelta(cfg)} {
		if !strings.Contains(rendered, `responses_mode = "stateful"`) || !strings.Contains(rendered, "responses_stateful = false") {
			t.Fatalf("responses settings missing from render:\n%s", rendered)
		}
		var decoded Config
		if _, err := toml.Decode(rendered, &decoded); err != nil {
			t.Fatalf("decode responses config: %v\n%s", err, rendered)
		}
		entry, ok := decoded.Provider("responses-test")
		if !ok || entry.ResponsesMode != "stateful" || entry.ResponsesStateful == nil || *entry.ResponsesStateful {
			t.Fatalf("responses settings did not round-trip: %+v, found=%v", entry, ok)
		}
	}
}

func TestProjectDeltaRendersUICursorShape(t *testing.T) {
	c := Default()
	c.UI.CursorShape = "block"

	delta := RenderTOMLProjectDelta(c)
	for _, want := range []string{"[ui]", `cursor_shape = "block"`} {
		if !strings.Contains(delta, want) {
			t.Fatalf("project delta missing %q:\n%s", want, delta)
		}
	}

	got := Default()
	if _, err := toml.Decode(delta, got); err != nil {
		t.Fatalf("decode project delta: %v\n%s", err, delta)
	}
	if got.UICursorShape() != "block" {
		t.Fatalf("ui.cursor_shape = %q, want block", got.UICursorShape())
	}
}

func TestProjectRenderIncludesNonDefaultUIAndNetworkSections(t *testing.T) {
	c := Default()
	c.UI.Theme = "light"
	c.Network.ProxyMode = "custom"
	c.Network.Proxy.Server = "127.0.0.1"
	c.Network.Proxy.Port = 7890

	project := RenderTOMLForScope(c, RenderScopeProject)
	for _, want := range []string{"[ui]", `theme = "light"`, "[network]", `proxy_mode = "custom"`, `server = "127.0.0.1"`} {
		if !strings.Contains(project, want) {
			t.Fatalf("project render missing legacy/non-default %q:\n%s", want, project)
		}
	}
}

func TestRenderTOMLRoundTripsPerModelPrices(t *testing.T) {
	orig := Default()
	orig.Providers = []ProviderEntry{{
		Name:      "deepseek",
		Kind:      "openai",
		BaseURL:   "https://api.deepseek.com",
		Models:    []string{"deepseek-v4-flash", "deepseek-v4-pro"},
		Default:   "deepseek-v4-flash",
		APIKeyEnv: "DEEPSEEK_API_KEY",
		Prices:    DeepSeekV4PricesForCurrency("CNY"),
	}}

	var got Config
	if _, err := toml.Decode(RenderTOML(orig), &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	p, ok := got.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider missing after round trip")
	}
	if p.Prices["deepseek-v4-flash"].Input != 1 || p.Prices["deepseek-v4-pro"].Output != 6 {
		t.Fatalf("prices after round trip = %+v", p.Prices)
	}
}

func TestRenderTOMLRoundTripsVisionModels(t *testing.T) {
	orig := Default()
	orig.Providers = []ProviderEntry{
		{
			Name:         "custom",
			Kind:         "openai",
			BaseURL:      "https://proxy.example.com/v1",
			Models:       []string{"text-only", "qwen-vl-plus"},
			Default:      "text-only",
			APIKeyEnv:    "CUSTOM_API_KEY",
			VisionModels: []string{"qwen-vl-plus"},
			VisionDetail: "low",
		},
		{
			Name:         "disabled-vision",
			Kind:         "openai",
			BaseURL:      "https://proxy.example.com/v1",
			Models:       []string{"qwen-vl-plus"},
			Default:      "qwen-vl-plus",
			APIKeyEnv:    "CUSTOM_API_KEY",
			VisionModels: []string{},
		},
	}

	rendered := RenderTOML(orig)
	if !strings.Contains(rendered, `vision_models = ["qwen-vl-plus"]`) {
		t.Fatalf("rendered TOML missing vision_models:\n%s", rendered)
	}
	if !strings.Contains(rendered, `vision_models = []`) {
		t.Fatalf("rendered TOML missing explicit empty vision_models:\n%s", rendered)
	}
	if !strings.Contains(rendered, `vision_detail = "low"`) {
		t.Fatalf("rendered TOML missing vision_detail:\n%s", rendered)
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v", err)
	}
	p, ok := got.Provider("custom")
	if !ok {
		t.Fatal("custom provider missing after round trip")
	}
	if !reflect.DeepEqual(p.VisionModels, []string{"qwen-vl-plus"}) {
		t.Fatalf("vision_models after round trip = %v, want [qwen-vl-plus]", p.VisionModels)
	}
	if p.VisionDetail != "low" {
		t.Fatalf("vision_detail after round trip = %q, want low", p.VisionDetail)
	}
	disabled, ok := got.Provider("disabled-vision")
	if !ok {
		t.Fatal("disabled-vision provider missing after round trip")
	}
	if disabled.VisionModels == nil || len(disabled.VisionModels) != 0 {
		t.Fatalf("disabled-vision vision_models after round trip = %#v, want explicit empty list", disabled.VisionModels)
	}
}

func TestRenderTOMLRoundTripsProviderHeadersAndModelOverrides(t *testing.T) {
	orig := Default()
	orig.Providers = []ProviderEntry{{
		Name:      "gateway",
		Kind:      "openai",
		BaseURL:   "https://gateway.example/v1",
		Models:    []string{"deepseek-v4-flash", "plain-chat"},
		Default:   "plain-chat",
		APIKeyEnv: "GATEWAY_API_KEY",
		Headers: map[string]string{
			"HTTP-Referer": "https://app.example",
			"X-Title":      "Corvus",
		},
		ExtraBody: map[string]any{
			"enable_thinking": true,
			"top_p":           0.8,
			"metadata": map[string]any{
				"mode": "fast",
			},
		},
		AuthHeader:      true,
		MaxOutputTokens: 16_384,
		ModelOverrides: map[string]ProviderModelOverride{
			"deepseek-v4-flash": {
				ReasoningProtocol: ReasoningProtocolDeepSeek,
				SupportedEfforts:  []string{"high", "max"},
				DefaultEffort:     "high",
				Vision:            boolPtr(false),
				ContextWindow:     262_144,
				MaxOutputTokens:   32_768,
			},
		},
	}}

	rendered := RenderTOML(orig)
	if !strings.Contains(rendered, `headers     = { HTTP-Referer = "https://app.example", X-Title = "Corvus" }`) {
		t.Fatalf("rendered TOML missing headers:\n%s", rendered)
	}
	if !strings.Contains(rendered, `extra_body`) || !strings.Contains(rendered, `"enable_thinking" = true`) {
		t.Fatalf("rendered TOML missing extra_body:\n%s", rendered)
	}
	if !strings.Contains(rendered, `auth_header = true`) {
		t.Fatalf("rendered TOML missing auth_header:\n%s", rendered)
	}
	if !strings.Contains(rendered, `max_output_tokens = 16384`) || !strings.Contains(rendered, `model_overrides`) || !strings.Contains(rendered, `reasoning_protocol = "deepseek"`) || !strings.Contains(rendered, `context_window = 262144`) || !strings.Contains(rendered, `max_output_tokens = 32768`) {
		t.Fatalf("rendered TOML missing model overrides:\n%s", rendered)
	}

	var got Config
	if _, err := toml.Decode(rendered, &got); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n%s", err, rendered)
	}
	p, ok := got.Provider("gateway")
	if !ok {
		t.Fatal("gateway provider missing after round trip")
	}
	if p.Headers["HTTP-Referer"] != "https://app.example" || p.Headers["X-Title"] != "Corvus" {
		t.Fatalf("headers after round trip = %+v", p.Headers)
	}
	if p.ExtraBody["enable_thinking"] != true || p.ExtraBody["top_p"] != 0.8 {
		t.Fatalf("extra_body after round trip = %+v", p.ExtraBody)
	}
	if !p.AuthHeader {
		t.Fatal("auth_header after round trip = false, want true")
	}
	if p.MaxOutputTokens != 16_384 {
		t.Fatalf("provider max_output_tokens after round trip = %d, want 16384", p.MaxOutputTokens)
	}
	metadata, ok := p.ExtraBody["metadata"].(map[string]any)
	if !ok || metadata["mode"] != "fast" {
		t.Fatalf("extra_body metadata after round trip = %+v", p.ExtraBody["metadata"])
	}
	ov := p.ModelOverrides["deepseek-v4-flash"]
	if ov.ReasoningProtocol != ReasoningProtocolDeepSeek || !reflect.DeepEqual(ov.SupportedEfforts, []string{"high", "max"}) || ov.DefaultEffort != "high" || ov.Vision == nil || *ov.Vision || ov.ContextWindow != 262_144 || ov.MaxOutputTokens != 32_768 {
		t.Fatalf("model override after round trip = %+v", ov)
	}

	// Older releases do not know context_window/max_output_tokens inside model
	// overrides, but their TOML decoder must still accept this release's config.
	type legacyModelOverride struct {
		ReasoningProtocol string   `toml:"reasoning_protocol"`
		SupportedEfforts  []string `toml:"supported_efforts"`
		DefaultEffort     string   `toml:"default_effort"`
		Vision            *bool    `toml:"vision"`
	}
	type legacyProvider struct {
		ModelOverrides map[string]legacyModelOverride `toml:"model_overrides"`
	}
	var legacy struct {
		Providers []legacyProvider `toml:"providers"`
	}
	if _, err := toml.Decode(rendered, &legacy); err != nil {
		t.Fatalf("legacy config shape cannot read per-model context window: %v", err)
	}
}

func TestRenderStringMapQuotesNonBareTOMLKeys(t *testing.T) {
	rendered := renderStringMap(map[string]string{
		"github:gh-fix-ci": "deepseek-pro",
		"review":           "deepseek-flash",
	})
	if !strings.Contains(rendered, `"github:gh-fix-ci" = "deepseek-pro"`) {
		t.Fatalf("non-bare key was not quoted: %s", rendered)
	}
	var got struct {
		M map[string]string `toml:"m"`
	}
	if _, err := toml.Decode("m = "+rendered, &got); err != nil {
		t.Fatalf("rendered inline map does not parse: %v (%s)", err, rendered)
	}
	if got.M["github:gh-fix-ci"] != "deepseek-pro" || got.M["review"] != "deepseek-flash" {
		t.Fatalf("decoded map = %+v", got.M)
	}
}

func TestRenderTOMLTablePathQuotesEachSegment(t *testing.T) {
	got := renderTOMLTablePath("lsp", "servers", "c++", "github:gh-fix-ci")
	want := `lsp.servers."c++"."github:gh-fix-ci"`
	if got != want {
		t.Fatalf("renderTOMLTablePath = %q, want %q", got, want)
	}
}

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }

func TestRenderTOMLDefaultStepsOmitted(t *testing.T) {
	isolateUserConfigHome(t)
	out := RenderTOML(Default())
	agentLines := extractSectionLines(out, "[agent]")
	for _, line := range agentLines {
		if strings.Contains(line, "max_steps") || strings.Contains(line, "planner_max_steps") {
			t.Errorf("default step limits should be hidden from generated config, got: %s", line)
		}
	}
}

func TestRenderTOMLWindowsSandboxDefaultAndExplicitEnforceDisabled(t *testing.T) {
	isolateUserConfigHome(t)
	setRuntimeGOOS(t, "windows")

	defaultRendered := RenderTOMLForScope(Default(), RenderScopeUser)
	if !strings.Contains(defaultRendered, `bash    = "off"`) {
		t.Fatalf("Windows default user config should render bash off:\n%s", defaultRendered)
	}

	cfg := Default()
	cfg.Sandbox.Bash = "enforce"
	delta := RenderTOMLProjectDelta(cfg)
	if strings.Contains(delta, `[sandbox]`) || strings.Contains(delta, `bash = `) {
		t.Fatalf("Windows explicit enforce should not render as an effective project delta:\n%s", delta)
	}
}

func extractSectionLines(toml, section string) []string {
	var lines []string
	inSection := false
	for _, line := range strings.Split(toml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, section) {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[") {
			break
		}
		if inSection {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func TestRenderTOMLOmitsDeprecatedAgentStepLimits(t *testing.T) {
	isolateUserConfigHome(t)
	c := Default()
	c.Agent.MaxSteps = 5
	c.Agent.PlannerMaxSteps = 7
	out := RenderTOML(c)
	for _, line := range extractSectionLines(out, "[agent]") {
		if strings.Contains(line, "max_steps") || strings.Contains(line, "planner_max_steps") {
			t.Fatalf("deprecated step limit should never be rendered, got: %s", line)
		}
	}
}

func TestLoadForEditIgnoresAndDropsDeprecatedAgentStepLimitsOnSave(t *testing.T) {
	isolateUserConfigHome(t)
	path := UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[agent]\nplanner_max_steps = 9\nmax_steps = 100\ntemperature = 0.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadForEdit(path)
	if cfg.Agent.MaxSteps != 0 || cfg.Agent.PlannerMaxSteps != 0 {
		t.Fatalf("deprecated limits should normalize to zero, got max=%d planner=%d", cfg.Agent.MaxSteps, cfg.Agent.PlannerMaxSteps)
	}
	if cfg.Agent.Temperature != 0.4 {
		t.Fatalf("unrelated agent setting changed: temperature=%v", cfg.Agent.Temperature)
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, changed := stripLegacyAgentStepLimitLines(string(raw)); changed {
		t.Fatalf("saved config retained deprecated step limits:\n%s", raw)
	}
}

func TestIsolatedHomeDirEmptyByDefault(t *testing.T) {
	t.Setenv("CORVUS_HOME", "")
	if got := IsolatedHomeDir(); got != "" {
		t.Fatalf("IsolatedHomeDir() = %q, want empty", got)
	}
}

func TestIsolatedHomeDirReturnsCleanPath(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "isolated-corvus")
	t.Setenv("CORVUS_HOME", raw)
	got := IsolatedHomeDir()
	if filepath.Clean(got) != filepath.Clean(raw) {
		t.Fatalf("IsolatedHomeDir() = %q, want %q", got, raw)
	}
}

func TestLegacyOSSupportDirEmptyWhenIsolated(t *testing.T) {
	isolateUserConfigHome(t)
	t.Setenv("CORVUS_HOME", filepath.Join(t.TempDir(), "isolated-home"))
	if got := legacyOSSupportDir(); got != "" {
		t.Fatalf("legacyOSSupportDir() = %q, want empty when isolated", got)
	}
}

func TestLegacyXDGConfigPathsEmptyWhenIsolated(t *testing.T) {
	isolateUserConfigHome(t)
	t.Setenv("CORVUS_HOME", filepath.Join(t.TempDir(), "isolated-home"))
	if got := legacyXDGConfigPaths(); got != nil {
		t.Fatalf("legacyXDGConfigPaths() = %v, want nil when isolated", got)
	}
}

func TestCacheDirHonorsCorvusHome(t *testing.T) {
	home := t.TempDir()
	isolated := filepath.Join(home, "isolated-home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CORVUS_HOME", isolated)

	got := CacheDir()
	want := filepath.Join(isolated, "cache")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("CacheDir() = %q, want %q", got, want)
	}
}

func TestCacheDirHonorsCorvusCacheHomeOverCorvusHome(t *testing.T) {
	home := t.TempDir()
	cacheHome := filepath.Join(home, "custom-cache")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CORVUS_HOME", filepath.Join(home, "isolated-home"))
	t.Setenv("CORVUS_CACHE_HOME", cacheHome)

	got := CacheDir()
	want := cacheHome
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("CacheDir() = %q, want %q (CORVUS_CACHE_HOME must win)", got, want)
	}
}

func TestUserConfigLoadPathNoLegacyFallbackWhenIsolated(t *testing.T) {
	home := isolateUserConfigHome(t)
	isolated := filepath.Join(home, "isolated-home")
	t.Setenv("CORVUS_HOME", isolated)

	// Create a legacy config at the OS production path — it must not be loaded.
	productionHome := expectedDefaultCorvusHome(home)
	if err := os.MkdirAll(productionHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productionHome, "config.toml"), []byte("default_model = \"production/model\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The primary config under isolated home does not exist yet.
	got := userConfigLoadPath()
	want := filepath.Join(isolated, "config.toml")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("userConfigLoadPath() = %q, want %q (must not fall back to production legacy config)", got, want)
	}
}

func TestCredentialSourceCandidatesSkipHomeEnvWhenIsolated(t *testing.T) {
	isolateUserConfigHome(t)
	t.Setenv("CORVUS_HOME", filepath.Join(t.TempDir(), "isolated-home"))

	// Write a key into the production home .env — it must not appear as a source.
	if home, err := os.UserHomeDir(); err == nil {
		if err := os.WriteFile(filepath.Join(home, ".env"), []byte("LEAKED_KEY=leaked-value\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	candidates := credentialSourceCandidates(".")
	for _, c := range candidates {
		if c.Kind == CredentialSourceHomeEnv {
			t.Fatalf("credentialSourceCandidates includes CredentialSourceHomeEnv when isolated: %v", c)
		}
	}
}

func TestMigrateLegacyIfNeededSkipsWhenIsolated(t *testing.T) {
	home := isolateUserConfigHome(t)
	isolated := filepath.Join(home, "isolated-home")
	t.Setenv("CORVUS_HOME", isolated)

	// Create a legacy config.json in production home — migration must skip it.
	legacyDir := filepath.Join(home, ".corvus")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "config.json"), []byte(`{"model":"production-model","apiKey":"sk-legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := MigrateLegacyIfNeeded()
	if err != nil {
		t.Fatalf("MigrateLegacyIfNeeded() error = %v", err)
	}
	if res != nil {
		t.Fatalf("MigrateLegacyIfNeeded() = %+v, want nil when isolated", res)
	}
}

// TestProjectConfigCannotOverrideSecrets pins [secrets] as a user-global
// security control: a cloned repository's corvus.toml must not be able to
// opt the user into subprocess env stripping or sensitive-path hiding.
func TestProjectConfigCannotOverrideSecrets(t *testing.T) {
	isolateUserConfigHome(t)
	t.Setenv("CORVUS_HOME", "")
	globalDir := filepath.Dir(UserConfigPath())
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalTOML := "[secrets]\nfilter_subprocess_env = false\nprotect_sensitive_files = false\n"
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"), []byte(globalTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	project := t.TempDir()
	projectTOML := "[secrets]\nfilter_subprocess_env = true\nprotect_sensitive_files = true\n"
	if err := os.WriteFile(filepath.Join(project, "corvus.toml"), []byte(projectTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot() error = %v", err)
	}
	if cfg.Secrets.FilterSubprocessEnv {
		t.Error("project corvus.toml enabled filter_subprocess_env; [secrets] must stay user-global")
	}
	if cfg.Secrets.ProtectSensitiveFiles {
		t.Error("project corvus.toml enabled protect_sensitive_files; [secrets] must stay user-global")
	}
}

// TestRenderTOMLPersistsSecretsSection pins config-save round-tripping: the
func TestAgentPromptCacheKeyRoundTrip(t *testing.T) {
	var loaded Config
	if _, err := toml.Decode(`
[agent]
prompt_cache_key = "off"
prompt_cache_key_value = "x"
`, &loaded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if loaded.Agent.PromptCacheKey != "off" {
		t.Fatalf("PromptCacheKey = %q, want off", loaded.Agent.PromptCacheKey)
	}
	if loaded.Agent.PromptCacheKeyValue != "x" {
		t.Fatalf("PromptCacheKeyValue = %q, want x", loaded.Agent.PromptCacheKeyValue)
	}

	// Empty mode is the auto default and must not be forced into defaults.
	if Default().Agent.PromptCacheKey != "" || Default().Agent.PromptCacheKeyValue != "" {
		t.Fatalf("defaults should leave prompt cache key empty (auto), got mode=%q value=%q",
			Default().Agent.PromptCacheKey, Default().Agent.PromptCacheKeyValue)
	}

	cfg := Default()
	cfg.Agent.PromptCacheKey = "off"
	cfg.Agent.PromptCacheKeyValue = "x"
	rendered := RenderTOML(cfg)
	for _, want := range []string{
		`prompt_cache_key = "off"`,
		`prompt_cache_key_value = "x"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("full render missing %q:\n%s", want, rendered)
		}
	}
	// auto/empty must stay commented, not written as a live key.
	autoOnly := Default()
	autoRendered := RenderTOML(autoOnly)
	for _, line := range strings.Split(autoRendered, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") || trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "prompt_cache_key ") || strings.HasPrefix(trim, "prompt_cache_key=") {
			t.Fatalf("default auto mode must not emit live prompt_cache_key:\n%s", autoRendered)
		}
		if strings.HasPrefix(trim, "prompt_cache_key_value ") || strings.HasPrefix(trim, "prompt_cache_key_value=") {
			t.Fatalf("default empty value must not emit live prompt_cache_key_value:\n%s", autoRendered)
		}
	}

	var round Config
	if _, err := toml.Decode(rendered, &round); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n---\n%s", err, rendered)
	}
	if round.Agent.PromptCacheKey != "off" || round.Agent.PromptCacheKeyValue != "x" {
		t.Fatalf("round-trip lost fields: mode=%q value=%q", round.Agent.PromptCacheKey, round.Agent.PromptCacheKeyValue)
	}

	// Project delta render should also persist non-defaults.
	delta := RenderTOMLProjectDelta(cfg)
	for _, want := range []string{
		`prompt_cache_key = "off"`,
		`prompt_cache_key_value = "x"`,
	} {
		if !strings.Contains(delta, want) {
			t.Fatalf("project delta missing %q:\n%s", want, delta)
		}
	}
}

// renderer must emit [secrets] for the user scope or every WriteFile would
// silently drop the user's security toggles.
func TestRenderTOMLPersistsSecretsSection(t *testing.T) {
	cfg := Default()
	cfg.Secrets.FilterSubprocessEnv = true
	cfg.Secrets.ProtectSensitiveFiles = true

	out := RenderTOMLForScope(cfg, RenderScopeUser)
	for _, want := range []string{"[secrets]", "filter_subprocess_env = true", "protect_sensitive_files = true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("user-scope render missing %q:\n%s", want, out)
		}
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	back := Default()
	if err := mergeFile(back, path); err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}
	if !back.Secrets.FilterSubprocessEnv || !back.Secrets.ProtectSensitiveFiles {
		t.Fatalf("secrets toggles lost in render round-trip: %+v", back.Secrets)
	}

	// Project scope must not render the section — LoadForRoot ignores it there.
	if proj := RenderTOMLForScope(cfg, RenderScopeProject); strings.Contains(proj, "[secrets]") {
		t.Fatalf("project scope rendered [secrets]:\n%s", proj)
	}
	if strings.Contains(out, "redact_tool_output") {
		t.Fatalf("user-scope render still exposes removed live-redaction setting:\n%s", out)
	}
}
