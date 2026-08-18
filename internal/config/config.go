// Package config loads Corvus's runtime configuration from TOML. Resolution order:
// flag > project .corvus/config.toml > user config.toml (in Corvus home,
// ~/.corvus) > built-in defaults.
// API keys are set directly with api_key on a [[providers]] entry; the user's
// ~/.corvus/config.toml wins over the project .corvus/config.toml. The legacy
// api_key_env indirection (project .env, then Corvus's credential store) is
// still honored when api_key is not set.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	fileencoding "corvus/internal/fileutil/encoding"
	"corvus/internal/netclient"
	"corvus/internal/netpolicy"
	"corvus/internal/provider"
)

var validSkillName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// IsValidSkillName reports whether name is a usable skill identifier.
func IsValidSkillName(name string) bool { return validSkillName.MatchString(name) }

// SkillNameKey normalizes a skill identifier for config comparisons.
func SkillNameKey(name string) string {
	name = strings.TrimSpace(name)
	if !IsValidSkillName(name) {
		return ""
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(name)
	}
	return name
}

// Config is Corvus's runtime configuration.
type Config struct {
	ConfigVersion    int                 `toml:"config_version"`
	DefaultModel     string              `toml:"default_model"`
	Language         string              `toml:"language"` // ui/model language tag (e.g. "zh"); empty = auto-detect from $LANG / $CORVUS_LANG
	CredentialsStore string              `toml:"credentials_store"`
	UI               UIConfig            `toml:"ui"`
	Agent            AgentConfig         `toml:"agent"`
	Providers        []ProviderEntry     `toml:"providers"`
	Tools            ToolsConfig         `toml:"tools"`
	Permissions      PermissionsConfig   `toml:"permissions"`
	Sandbox          SandboxConfig       `toml:"sandbox"`
	Network          NetworkConfig       `toml:"network"`
	NetworkPolicy    NetworkPolicyConfig `toml:"network_policy"`
	WebSearch        WebSearchConfig     `toml:"web_search"`
	Environment      EnvironmentConfig   `toml:"environment"`
	Plugins          []PluginEntry       `toml:"plugins"`
	Skills           SkillsConfig        `toml:"skills"`
	Statusline       StatuslineConfig    `toml:"statusline"`
	LSP              LSPConfig           `toml:"lsp"`
	Secrets          SecretsConfig       `toml:"secrets"`
	RuntimePolicy    RuntimePolicyConfig `toml:"runtime_policy"`

	systemPromptFileSource     promptFileSource
	providerSources            map[string]providerSourceScope
	shadowedProjectProviders   []ProviderEntry
	ignoredProjectDefaultModel string
	ignoredLegacyStepLimits    bool
	expansionEnv               map[string]string
	pluginPackageOwners        map[string]string
	pluginPackageSkillOwners   map[string][]string
	pluginPackageAgentOwners   map[string][]string
	editLoadErr                error
	// loadWarnings are non-fatal issues observed while loading config (corrupt
	// user/project files recovered via last-known-good or defaults). They never
	// rewrite the original file; the UI may surface them for doctor repair.
	loadWarnings []string
}

type promptFileSource uint8

const (
	promptFileSourceUnknown promptFileSource = iota
	promptFileSourceUser
	promptFileSourceProject
)

type systemPromptFileError struct {
	configured string
	candidates []string
	errors     []error
	allMissing bool
}

func (e *systemPromptFileError) Error() string {
	detail := "could not be read from any configured location"
	if e.allMissing {
		detail = "not found at any configured location"
	}
	message := fmt.Sprintf("system_prompt_file %q %s: %s", e.configured, detail, strings.Join(e.candidates, ", "))
	if !e.allMissing && len(e.errors) > 0 {
		message += ": " + errors.Join(e.errors...).Error()
	}
	return message
}

func (e *systemPromptFileError) Unwrap() error { return errors.Join(e.errors...) }

// IsMissingSystemPromptFile reports whether every allowed location for a
// configured prompt file was absent. Permission, containment, and other I/O
// failures deliberately return false so callers do not start without an
// explicitly configured prompt.
func IsMissingSystemPromptFile(err error) bool {
	var target *systemPromptFileError
	return errors.As(err, &target) && target.allMissing
}

// LoadWarnings returns non-fatal config load issues (corrupt files recovered in
// memory). The returned slice is a copy.
func (c *Config) LoadWarnings() []string {
	if c == nil || len(c.loadWarnings) == 0 {
		return nil
	}
	out := make([]string, len(c.loadWarnings))
	copy(out, c.loadWarnings)
	return out
}

// HasLoadWarnings reports whether the load used a degraded in-memory fallback.
func (c *Config) HasLoadWarnings() bool {
	return c != nil && len(c.loadWarnings) > 0
}

func (c *Config) addLoadWarning(msg string) {
	if c == nil {
		return
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	c.loadWarnings = append(c.loadWarnings, msg)
}

// IgnoredLegacyAgentStepLimits reports whether this load found and ignored the
// retired [agent].max_steps or planner_max_steps settings. Boot removes standard
// key assignments before loading, while read-only/config-only loads only report
// and normalize them in memory.
func (c *Config) IgnoredLegacyAgentStepLimits() bool {
	return c != nil && c.ignoredLegacyStepLimits
}

// IgnoredProjectDefaultModel returns the project .corvus/config.toml default_model
// that LoadForRoot ignored because no configured provider serves it (see
// restoreUnresolvableProjectDefaultModel), or "" when none was ignored.
func (c *Config) IgnoredProjectDefaultModel() string {
	if c == nil {
		return ""
	}
	return c.ignoredProjectDefaultModel
}

// SecretsConfig controls the credential protection layers. It is a user-global
// setting: project .corvus/config.toml values are ignored (see LoadForRoot), so a
// cloned repository cannot silently opt the user into workflow-breaking
// protections.
type SecretsConfig struct {
	// FilterSubprocessEnv strips credential-like environment variables
	// (*_API_KEY, *TOKEN*, *SECRET*, ...) from tool subprocesses (bash, hooks,
	// LSP, MCP stdio). Default off: it breaks token-based workflows such as
	// `gh`, HTTPS `git push`, and `npm publish`.
	FilterSubprocessEnv bool `toml:"filter_subprocess_env"`
	// ProtectSensitiveFiles makes read/list/search tools treat credential
	// paths (.env, .git-credentials, .netrc, *.pem/*.key/*.p12/*.pfx, ~/.ssh)
	// as invisible. Default off because hiding the files breaks legitimate
	// "edit my .env" workflows.
	ProtectSensitiveFiles bool `toml:"protect_sensitive_files"`
}

type providerSourceScope string

const (
	providerSourceUser    providerSourceScope = "user"
	providerSourceProject providerSourceScope = "project"
)

// UIConfig controls terminal presentation and TUI-only preferences.
type UIConfig struct {
	Theme          string   `toml:"theme"`           // auto|dark|light; empty resolves to auto
	ThemeStyle     string   `toml:"theme_style"`     // codex|codex-light|graphite|ember|aurora|midnight|sandstone|porcelain|linen|glacier plus legacy aliases
	ShortcutLayout string   `toml:"shortcut_layout"` // classic|desktop; accepted for compatibility
	Currency       string   `toml:"currency"`        // auto|CNY|USD pricing preference
	ProviderAccess []string `toml:"provider_access"` // providers visible in the TUI setup flow
	ShowReasoning  bool     `toml:"show_reasoning"`  // Ctrl+O / /verbose: show thinking text in CLI; false = collapsed
	CursorShape    string   `toml:"cursor_shape"`    // block|underline|bar; empty defaults to block
}

// EnvironmentConfig controls the stable startup environment block injected into
// the model-facing prompt. Enabled nil means the default (enabled); Tools maps a
// tool name to an explicit executable path when PATH probing is not enough.
type EnvironmentConfig struct {
	Enabled *bool             `toml:"enabled"`
	Tools   map[string]string `toml:"tools"`
}

// EnvironmentEnabled reports whether startup environment probing should feed the
// cache-stable system prompt.
func (c *Config) EnvironmentEnabled() bool {
	return c == nil || c.Environment.Enabled == nil || *c.Environment.Enabled
}

// UITheme normalizes ui.theme to a supported value.
func (c *Config) UITheme() string {
	switch strings.ToLower(strings.TrimSpace(c.UI.Theme)) {
	case "dark":
		return "dark"
	case "light":
		return "light"
	default:
		return "auto"
	}
}

// UIThemeStyle normalizes ui.theme_style. Empty means "pick the default style
// for the resolved light/dark shell".
func (c *Config) UIThemeStyle() string {
	return normalizeThemeStyle(c.UI.ThemeStyle)
}

// UIShortcutLayout normalizes the legacy CLI shortcut layout setting. It is kept
// for compatibility; Shift+Tab toggles Plan and Ctrl+Y toggles YOLO in both
// layouts.
func (c *Config) UIShortcutLayout() string {
	switch strings.ToLower(strings.TrimSpace(c.UI.ShortcutLayout)) {
	case "desktop", "dual", "dual-axis", "dual_axis":
		return "desktop"
	default:
		return "classic"
	}
}

// UICursorShape normalizes ui.cursor_shape. The default is a full-cell "block"
// caret so the insertion point is large and centered in the cell; "bar" remains
// available for a slim caret that does not cover CJK glyphs. Valid values are
// "block", "underline", and "bar".
func (c *Config) UICursorShape() string {
	switch strings.ToLower(strings.TrimSpace(c.UI.CursorShape)) {
	case "bar":
		return "bar"
	case "underline":
		return "underline"
	default:
		return "block"
	}
}

func normalizeThemeStyle(style string) string {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "codex", "codex-light", "graphite", "aurora", "slate", "carbon", "nocturne", "amber", "ember", "midnight", "sandstone", "porcelain", "linen", "glacier":
		return strings.ToLower(strings.TrimSpace(style))
	default:
		return ""
	}
}

// PricingCurrency returns the explicit user-global pricing currency. Empty
// means the pricing region follows the configured language.
func (c *Config) PricingCurrency() string {
	if c == nil {
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(c.UI.Currency)) {
	case "CNY", "RMB", "CNH":
		return "CNY"
	case "USD":
		return "USD"
	default:
		return ""
	}
}

// ColdResumePruneEnabled reports whether stale tool results are elided when a
// session resumes past the provider cache window. Default true (cheaper cold
// restart); users keep full history by disabling it.
func (c *Config) ColdResumePruneEnabled() bool {
	if c == nil || c.Agent.ColdResumePrune == nil {
		return true
	}
	return *c.Agent.ColdResumePrune
}

// ResponseLanguage normalizes the top-level language preference for final
// answers. Empty means auto: replies follow the current user turn.
func (c *Config) ResponseLanguage() string {
	if c == nil {
		return "auto"
	}
	return NormalizeLanguage(c.Language)
}

// NormalizeLanguage returns one of auto|zh|en for UI/default reply language settings.
func NormalizeLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "auto", "detect", "default":
		return "auto"
	case "zh", "cn", "chinese", "中文":
		return "zh"
	case "en", "english":
		return "en"
	default:
		return "auto"
	}
}

// ReasoningLanguage normalizes agent.reasoning_language. Empty means auto:
// visible reasoning stays English per the stable LanguagePolicy. zh pins
// Chinese thinking text as transient user-turn context. Legacy "default" is
// treated as auto.
func (c *Config) ReasoningLanguage() string {
	if c == nil {
		return "auto"
	}
	return NormalizeReasoningLanguage(c.Agent.ReasoningLanguage)
}

// NormalizeReasoningLanguage returns one of auto|zh|en.
func NormalizeReasoningLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "auto", "follow", "conversation", "detect", "default", "model", "model-default", "model_default", "provider":
		return "auto"
	case "zh", "cn", "chinese", "中文":
		return "zh"
	case "en", "english":
		return "en"
	default:
		return "auto"
	}
}

// LSPConfig governs the optional Language Server Protocol tools (lsp_definition,
// lsp_references, lsp_hover, lsp_diagnostics). Enabled defaults to true; the
// servers themselves are never bundled — each resolves on PATH and the tool
// returns an install hint when it is missing, so the capability is dormant until
// the user installs a server. Servers overrides or extends the built-in language
// → server map, keyed by language id (e.g. "go", "rust", "python").
type LSPConfig struct {
	Enabled bool                 `toml:"enabled"`
	Servers map[string]LSPServer `toml:"servers"`
}

// LSPServer overrides a built-in language's server or, when keyed by a new
// language, adds one. An empty field falls back to the built-in default for that
// language; Extensions is required when adding a language the built-ins don't
// cover (e.g. ".ex" for Elixir) so files route to it.
type LSPServer struct {
	Command     string            `toml:"command"`
	Args        []string          `toml:"args"`
	Env         map[string]string `toml:"env"`
	LanguageID  string            `toml:"language_id"`
	Extensions  []string          `toml:"extensions"`
	InstallHint string            `toml:"install_hint"`
}

// StatuslineConfig configures a custom status line. Command, when set, is run at
// startup and after each turn; its first line of stdout replaces the built-in
// status data row. A JSON payload (model, context tokens, cwd) is fed on stdin.
type StatuslineConfig struct {
	Command string `toml:"command"`
}

// NetworkConfig controls ordinary outbound HTTP traffic such as model providers,
// wallet-balance lookups, updater checks, CodeGraph downloads, and web_fetch.
// web_fetch reuses these proxy settings while keeping its own SSRF-guarded
// dialer.
type NetworkConfig struct {
	// ProxyMode is "auto" (default; environment proxy for now), "env", "custom",
	// or "off". auto leaves room for OS proxy detection later without changing the
	// config shape.
	ProxyMode string `toml:"proxy_mode"`
	// ProxyURL is an advanced custom override such as "socks5://127.0.0.1:7890".
	// When set and proxy_mode = "custom", it wins over the structured proxy table.
	ProxyURL string `toml:"proxy_url"`
	// NoProxy is honored for custom proxies. Env/auto modes use NO_PROXY from the
	// process environment instead.
	NoProxy string             `toml:"no_proxy"`
	Proxy   NetworkProxyConfig `toml:"proxy"`
}

// NetworkProxyConfig is the structured custom-proxy editor shape. Password is
// optional and supports ${VAR} expansion, so users can avoid storing it literally.
type NetworkProxyConfig struct {
	Type     string `toml:"type"` // http|https|socks5|socks5h
	Server   string `toml:"server"`
	Port     int    `toml:"port"`
	Username string `toml:"username"`
	Password string `toml:"password"`
}

// NetworkProxySpec returns the expanded proxy settings used by netclient.
func (c *Config) NetworkProxySpec() netclient.ProxySpec {
	return netclient.ProxySpec{
		Mode:        c.Network.ProxyMode,
		URL:         c.expandVars(c.Network.ProxyURL),
		NoProxy:     c.expandVars(c.Network.NoProxy),
		Type:        c.Network.Proxy.Type,
		Server:      c.expandVars(c.Network.Proxy.Server),
		Port:        c.Network.Proxy.Port,
		Username:    c.expandVars(c.Network.Proxy.Username),
		Password:    c.expandVars(c.Network.Proxy.Password),
		DirectHosts: c.directProxyHosts(),
	}
}

// directProxyHosts collects the base_url hosts of providers marked no_proxy, so
// netclient bypasses the proxy for them without knowing any provider by name.
//
// Only for an auto-detected proxy (auto/env): that proxy is typically a
// GFW-circumvention one not meant for domestic endpoints (e.g. mimo), so keep
// them direct. An explicit proxy_mode = "custom" is the user saying "route
// everything through this" — e.g. a mandatory corporate proxy — so honor it for
// every provider; a custom-proxy user who wants a host direct uses
// network.no_proxy instead (#3635).
func (c *Config) directProxyHosts() []string {
	if c.NetworkProxyMode() == netclient.ModeCustom {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range c.Providers {
		if !p.NoProxy {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(p.BaseURL))
		if err != nil {
			continue
		}
		if h := u.Hostname(); h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// NetworkProxyMode normalizes network.proxy_mode to a known value.
func (c *Config) NetworkProxyMode() string {
	return netclient.NormalizeMode(c.Network.ProxyMode)
}

// NetworkPolicyConfig declares the egress policy for outbound tools such as
// web_fetch (and the bash URL guard), layered over the coarse [sandbox]
// network switch. Rules are hostname globs matched against the URL's bare
// hostname: "example.com" matches exactly that host, "*.example.com" matches
// one level of subdomains, "**.example.com" any depth, and IP literals match
// exactly. Deny rules win over allow rules; Default is the decision when no
// rule matches ("allow" | "deny" | "ask"; default "allow", the fail-open
// status quo — the deny list is the explicit exfiltration guard). An "ask"
// default has no approval UI in this environment and resolves to allow,
// mirroring the permission package's nil-approver behaviour.
type NetworkPolicyConfig struct {
	Allow   []string `toml:"allow"`
	Deny    []string `toml:"deny"`
	Default string   `toml:"default"` // "allow" (default) | "deny" | "ask"
}

// NetPolicy compiles the [network_policy] section into the decision policy
// used by web_fetch and the bash URL guard. An empty default falls back to
// "allow" so an absent section keeps today's unconfined behaviour; blank rule
// entries are dropped. Invalid default values or malformed rules return an
// error so boot can refuse to start with a policy that silently matches
// nothing.
func (c *Config) NetPolicy() (netpolicy.Policy, error) {
	np := c.NetworkPolicy
	def := netpolicy.Allow
	switch strings.ToLower(strings.TrimSpace(np.Default)) {
	case "", "allow":
	case "deny":
		def = netpolicy.Deny
	case "ask":
		def = netpolicy.Ask
	default:
		return netpolicy.Policy{}, fmt.Errorf("network_policy.default %q is invalid; use \"allow\", \"deny\", or \"ask\"", np.Default)
	}
	p := netpolicy.New(nonBlank(np.Allow), nonBlank(np.Deny), def)
	if err := p.Validate(); err != nil {
		return netpolicy.Policy{}, err
	}
	return p, nil
}

// nonBlank returns the non-empty, trimmed entries of ss.
func nonBlank(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SkillsConfig configures skill discovery. Paths adds extra "custom"-scope skill
// roots — each a directory of SKILL.md / <name>.md playbooks — scanned between
// the project roots (.corvus/.agents/.agent/.claude under the workspace) and
// the global roots. ExcludedPaths hides matching discovery roots without deleting
// folders. ~, relative paths, and ${VAR} expansion are supported. DisabledSkills
// hides named skills from the agent prompt, slash invocation, and skill tools
// while keeping them manageable.
type SkillsConfig struct {
	Paths          []string `toml:"paths"`
	ExcludedPaths  []string `toml:"excluded_paths"`
	DisabledSkills []string `toml:"disabled_skills"`
	MaxDepth       int      `toml:"max_depth"`
}

// SkillCustomPaths returns the configured custom skill roots with ${VAR}
// expanded; empty entries are dropped.
func (c *Config) SkillCustomPaths() []string {
	var out []string
	for _, p := range c.Skills.Paths {
		if p = c.expandVars(p); strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// SkillExcludedPaths returns configured skill roots that should be hidden from
// discovery, with ${VAR} expanded and empty entries dropped.
func (c *Config) SkillExcludedPaths() []string {
	var out []string
	for _, p := range c.Skills.ExcludedPaths {
		if p = c.expandVars(p); strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// SkillMaxDepth bounds nested skill discovery. Depth 3 favors bundled skill
// packs while Store keeps nested markdown safe by requiring descriptions.
func (c *Config) SkillMaxDepth() int {
	const (
		defaultDepth = 3
		maxDepth     = 5
	)
	if c == nil || c.Skills.MaxDepth == 0 {
		return defaultDepth
	}
	if c.Skills.MaxDepth < 1 {
		return 1
	}
	if c.Skills.MaxDepth > maxDepth {
		return maxDepth
	}
	return c.Skills.MaxDepth
}

// DisabledSkillNames returns valid disabled skill identifiers, preserving the
// first spelling and dropping duplicates/empty entries.
func (c *Config) DisabledSkillNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range c.Skills.DisabledSkills {
		name = strings.TrimSpace(name)
		if !IsValidSkillName(name) {
			continue
		}
		key := SkillNameKey(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

// IsSkillDisabled reports whether name is configured as disabled.
func (c *Config) IsSkillDisabled(name string) bool {
	key := SkillNameKey(name)
	if key == "" {
		return false
	}
	for _, disabled := range c.DisabledSkillNames() {
		if SkillNameKey(disabled) == key {
			return true
		}
	}
	return false
}

// SandboxConfig bounds the blast radius of tool calls (Phase 0: file-writer
// confinement). WorkspaceRoot is the directory the built-in file writers
// (write_file / edit_file / multi_edit / move_file) may modify; empty means the
// current working directory, so writes stay inside the project by default.
// AllowWrite lists extra directories writers may also touch (e.g. a sibling repo
// or a temp dir). ForbidRead lists files or directories the agent may not read or list
// (e.g. ~/.ssh for secrets). Both support ${VAR} / ${VAR:-default} expansion. Reads are
// unrestricted; confining `bash` is Phase 1 (OS-level sandbox).
type SandboxConfig struct {
	WorkspaceRoot string   `toml:"workspace_root"`
	AllowWrite    []string `toml:"allow_write"`
	ForbidRead    []string `toml:"forbid_read"`
	// Bash is the OS-sandbox mode for the bash tool: "enforce" jails each
	// command when an OS sandbox is available and refuses bash otherwise; "off"
	// runs it unconfined. Empty uses the platform default.
	Bash string `toml:"bash"`
	// Network allows network egress from inside the bash sandbox. Defaults true
	// so module/package downloads keep working; the boundary is then writes.
	Network bool `toml:"network"`
}

// WriteRoots returns the directories file-writer tools may modify: the
// workspace root (defaulting to the current working directory when unset), plus
// any AllowWrite extras, with ${VAR} expanded. The roots are returned as given
// (relative or absolute); the confiner resolves them to absolute, symlink-free
// paths. The result is always non-empty, so confinement is on by default.
func (c *Config) WriteRoots() []string {
	return c.WriteRootsForRoot(".")
}

// WriteRootsForRoot is like WriteRoots but falls back to fallbackRoot when the
// config doesn't explicitly set a workspace_root. Desktop tabs pass their
// project root here so tool confinement is correct without changing cwd.
func (c *Config) WriteRootsForRoot(fallbackRoot string) []string {
	root := c.expandVars(c.Sandbox.WorkspaceRoot)
	if root == "" {
		root = fallbackRoot
		if root == "" || root == "." {
			if wd, err := os.Getwd(); err == nil {
				root = wd
			} else {
				root = "."
			}
		}
	}
	roots := []string{root}
	for _, d := range c.Sandbox.AllowWrite {
		if d = c.expandVars(d); d != "" {
			roots = append(roots, d)
		}
	}
	return roots
}

// AllowWriteRoots returns only the configured [sandbox] allow_write extras with
// ${VAR} expanded — the explicit escape-hatch entries, without the workspace
// root that WriteRoots prepends. The session-data write guard treats these as
// user-sanctioned raw access.
func (c *Config) AllowWriteRoots() []string {
	var roots []string
	for _, d := range c.Sandbox.AllowWrite {
		if d = c.expandVars(d); d != "" {
			roots = append(roots, d)
		}
	}
	return roots
}

// ForbidReadRoots returns the paths the agent is forbidden from reading
// or listing, with ${VAR} expanded. Relative roots are resolved against the
// current working directory; the confiner resolves them to symlink-free paths.
// Empty when no forbid_read entries are configured.
func (c *Config) ForbidReadRoots() []string {
	return c.ForbidReadRootsForRoot(".")
}

// ForbidReadRootsForRoot is like ForbidReadRoots but uses fallbackRoot when
// resolving relative paths (for desktop tabs that pass their project root).
func (c *Config) ForbidReadRootsForRoot(fallbackRoot string) []string {
	root := fallbackRoot
	if root == "" || root == "." {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		} else {
			root = "."
		}
	}
	roots := make([]string, 0, len(c.Sandbox.ForbidRead))
	for _, d := range c.Sandbox.ForbidRead {
		if d = c.expandVars(d); d != "" {
			if !filepath.IsAbs(d) {
				d = filepath.Join(root, d)
			}
			roots = append(roots, d)
		}
	}
	return roots
}

// BashMode normalises the bash-sandbox mode for the current host.
func (c *Config) BashMode() string {
	return c.BashModeForGOOS(runtimeGOOS)
}

// BashModeForGOOS normalises the bash-sandbox mode for tests and cross-platform
// rendering. Windows has no OS-level Bash sandbox and forces the effective mode
// off, even when older configs explicitly requested "enforce". macOS/Linux keep
// the existing explicit-mode behavior.
func (c *Config) BashModeForGOOS(goos string) string {
	if goos == "windows" {
		return "off"
	}
	switch strings.TrimSpace(c.Sandbox.Bash) {
	case "enforce":
		return "enforce"
	case "off":
		return "off"
	case "":
		return "enforce"
	default:
		return "enforce"
	}
}

// AgentConfig configures the harness loop. PlannerModel is optional: when set
// to another provider's name it enables two-model collaboration, where the
// planner handles low-frequency planning in its own session (kept separate so
// each model's prompt prefix stays cache-stable). SubagentModel is the optional
// default for runAs=subagent skills; SubagentModels overrides it per skill name.
type AgentConfig struct {
	SystemPrompt     string `toml:"system_prompt"`
	SystemPromptFile string `toml:"system_prompt_file"`
	// MaxSteps and PlannerMaxSteps are deprecated compatibility fields: old
	// TOML and desktop clients may still send them, but config loading
	// normalizes both to zero and rendering omits them. One-off CLI and
	// unattended bot limits remain separate controls.
	MaxSteps        int `toml:"max_steps"`
	PlannerMaxSteps int `toml:"planner_max_steps"`
	// Temperature is live (main + planner sampling temperature); only the two
	// fields above it are deprecated.
	Temperature         float64 `toml:"temperature"`
	PlannerModel        string  `toml:"planner_model"`
	GuardianModel       string  `toml:"guardian_model"`
	GuardianTemperature float64 `toml:"guardian_temperature"`
	// RecoveryModel optionally names a dedicated model for the independent
	// recovery reviewer. Empty falls back to GuardianModel, then the main model.
	RecoveryModel    string            `toml:"recovery_model"`
	SubagentModel    string            `toml:"subagent_model"`
	SubagentModels   map[string]string `toml:"subagent_models"`
	SubagentEffort   string            `toml:"subagent_effort"`
	SubagentEfforts  map[string]string `toml:"subagent_efforts"`
	MaxSubagentDepth int               `toml:"max_subagent_depth"`
	// MaxSubagentConcurrency bounds how many sub-agents (task, fleet items,
	// profile skills, nested children) may run at once in one session.
	// 0 means the default (6). Values outside 1–32 are clamped on load.
	MaxSubagentConcurrency int `toml:"max_subagent_concurrency"`
	// MaxParallelWriters bounds concurrent writer-capable sub-agents that
	// declare non-overlapping write_paths. 0 means the default (3). Must not
	// exceed MaxSubagentConcurrency after normalization.
	MaxParallelWriters int `toml:"max_parallel_writers"`
	// OutputStyle selects a persona/tone block folded into the system prompt at
	// startup (a built-in like "explanatory"/"learning"/"concise", or a custom
	// .corvus/output-styles/<name>.md). Empty = the unmodified prompt.
	OutputStyle string `toml:"output_style"`
	// PromptCacheKey selects sticky prompt-cache key policy for OpenAI-compatible
	// and non-DeepSeek Responses requests: auto|on|off|custom. Empty means auto.
	PromptCacheKey string `toml:"prompt_cache_key"`
	// PromptCacheKeyValue is the raw sticky key used only when PromptCacheKey is
	// "custom". Empty custom value means omit the wire field.
	PromptCacheKeyValue string `toml:"prompt_cache_key_value"`
	// Deprecated compatibility field. Automatic plan mode was retired in config
	// version 5; old TOML remains readable, but loading normalizes it to "off"
	// and rendering omits it. Plan mode remains available as an explicit user
	// choice.
	AutoPlan string `toml:"auto_plan"`
	// ReasoningLanguage controls the preferred language for visible reasoning
	// text. Empty/auto follows the conversation language. Applied as transient
	// turn context, not the stable prompt.
	ReasoningLanguage string `toml:"reasoning_language"`
	// Deprecated compatibility field paired with AutoPlan. Old TOML remains
	// readable, but loading clears it and rendering omits it.
	AutoPlanClassifier string `toml:"auto_plan_classifier"`
	// Compaction window fractions: soft = notice only, compact = trigger, force = hard ceiling.
	SoftCompactRatio    float64 `toml:"soft_compact_ratio"`
	ToolResultSnipRatio float64 `toml:"tool_result_snip_ratio"`
	CompactRatio        float64 `toml:"compact_ratio"`
	CompactForceRatio   float64 `toml:"compact_force_ratio"`
	// Keep controls which compactable messages stay verbatim beyond the current
	// user-fact/digest floor and recent tail. Empty uses the conservative default
	// of keeping error tool results.
	Keep       []string `toml:"keep"`
	RecentKeep int      `toml:"recent_keep"`
	// ColdResumePrune elides stale tool results when a session reopens past the
	// provider cache window. nil = default enabled.
	ColdResumePrune *bool `toml:"cold_resume_prune"`
	// PlanModeReadOnlyCommands is retained for old config/session round trips. Main
	// Plan bash calls now use the ordinary Permissions classifier and Sandbox.
	PlanModeReadOnlyCommands []string `toml:"plan_mode_read_only_commands"`
}

// RuntimePolicyConfig holds optional default axis selections for new sessions.
// Empty fields inherit the TokenMode / --profile preset.
type RuntimePolicyConfig struct {
	Guidance   string `toml:"guidance"`
	Completion string `toml:"completion"`
	Exposure   string `toml:"exposure"`
}

// ProviderEntry declares a model provider instance. ContextWindow is the model's
// token budget; the harness compacts older history as a turn's prompt approaches
// it (see agent compaction). 0 disables compaction for the instance.
type ProviderEntry struct {
	Name          string            `toml:"name"`
	Kind          string            `toml:"kind"`
	BaseURL       string            `toml:"base_url"`
	ChatURL       string            `toml:"chat_url"`
	Model         string            `toml:"model"`      // a single model (back-compat)
	Models        []string          `toml:"models"`     // a vendor's model list (one base_url/key, many models)
	ModelsURL     string            `toml:"models_url"` // auto-fetch models from this URL on startup
	Default       string            `toml:"default"`    // default model when Models is set (else Models[0])
	APIKey        string            `toml:"api_key"`    // direct key in config.toml; preferred over api_key_env
	APIKeyEnv     string            `toml:"api_key_env"`
	PresetID      string            `toml:"preset_id"`      // curated preset identity; UI-only metadata, not sent to model providers.
	PresetVersion int               `toml:"preset_version"` // curated preset schema version for future migrations.
	Headers       map[string]string `toml:"headers"`        // optional extra HTTP headers for compatible gateways; secrets should stay in api_key_env.
	ExtraBody     map[string]any    `toml:"extra_body"`     // optional extra top-level JSON request body fields for OpenAI-compatible gateways.
	AuthHeader    bool              `toml:"auth_header"`    // for Anthropic-compatible gateways that expect Authorization: Bearer instead of x-api-key.
	// ResponsesMode selects the Responses API context strategy. Empty preserves
	// vendor detection; DeepSeek is stateless while compatible endpoints may use
	// stateful previous_response_id continuation.
	ResponsesMode string `toml:"responses_mode"`
	// ResponsesStateful is the legacy boolean form retained for config
	// compatibility. ResponsesMode wins when both are present.
	ResponsesStateful *bool `toml:"responses_stateful"`
	resolvedAPIKey    string
	resolvedSource    CredentialSource
	BalanceURL        string `toml:"balance_url"` // optional; a provider-specific wallet-balance endpoint (DeepSeek: https://api.deepseek.com/user/balance). Empty = no balance readout.
	ContextWindow     int    `toml:"context_window"`
	// MaxOutputTokens is a protocol-neutral total output budget. Zero lets the
	// provider choose a safe default, a positive value is explicit, and a
	// negative value omits optional wire limits. Anthropic still requires one.
	MaxOutputTokens int                          `toml:"max_output_tokens"`
	Price           *provider.Pricing            `toml:"price"`  // legacy/provider-wide fallback
	Prices          map[string]*provider.Pricing `toml:"prices"` // optional per-model prices; keys are model ids

	persistedOfficialCurrency string

	// Thinking / Effort are provider-kind-specific knobs forwarded to the provider
	// via Config.Extra. The anthropic provider reads Thinking="adaptive" to enable
	// extended thinking and Effort ("low".."max") to tune depth. The
	// openai-compatible provider forwards Effort as reasoning_effort for
	// thinking-capable models; DeepSeek V4 Flash accepts low|high|max while
	// other DeepSeek models retain their model-specific capability mapping.
	// Empty = provider default.
	Thinking string `toml:"thinking"`
	Effort   string `toml:"effort"`
	// Vision marks the model as accepting image input. When set, images the user
	// attaches are embedded in the request (image_url for openai-kind, base64
	// blocks for anthropic). Off by default: text-only models 400 on image input,
	// and image tokens are heavy — gating keeps text-only flows cheap (the prompt
	// prefix is byte-identical with no image, so the cache is unaffected either way).
	Vision bool `toml:"vision"`
	// ModelCapability describes the model's inherent capacity for runtime policy
	// resolution. Valid values: auto|strong|standard|lite. Empty/auto defaults to
	// standard. Invalid values produce a configuration error.
	ModelCapability string `toml:"model_capability"`
	// VisionModels narrows image input support to specific models in a multi-model
	// provider. This lets one provider expose both text-only and multimodal chat
	// models without enabling image payloads for every model.
	VisionModels []string `toml:"vision_models"`
	// VisionDetail sets the openai image_url detail hint (low|high); empty = auto
	// (the field is omitted). "low" caps an image to a fixed ~85 tokens for cheap
	// coarse reads; ignored by providers without the knob (e.g. anthropic).
	VisionDetail string `toml:"vision_detail"`
	// WebSearch enables the server-side web_search tool for the anthropic
	// provider kind. When true, the provider includes {"type":"web_search"} in
	// the tools array, and the API executes searches server-side, returning
	// web_search_tool_result content blocks in the stream. This is the primary
	// way to use DeepSeek's built-in search via its Anthropic-compatible
	// endpoint (https://api.deepseek.com/anthropic). Off by default.
	WebSearch bool `toml:"web_search"`
	// ReasoningProtocol selects the request shape for OpenAI-compatible reasoning
	// models. Empty/auto uses the model capability registry plus endpoint
	// heuristics; glm selects GLM's thinking.type toggle; none disables automatic
	// reasoning controls for this provider.
	ReasoningProtocol string `toml:"reasoning_protocol"`
	// SupportedEfforts lists the /effort levels this provider/model exposes.
	// When non-empty, it overrides the built-in defaults derived from
	// Kind/BaseURL and makes /effort configurable. "auto" is the implicit
	// prefix — always accepted. DefaultEffort resolves it; omit DefaultEffort
	// (or set one outside this list) to fall back to SupportedEfforts[0].
	SupportedEfforts []string `toml:"supported_efforts"`
	// DefaultEffort is the /effort level used when the user picks "auto" or
	// has not set Effort. Ignored when SupportedEfforts is empty.
	DefaultEffort string `toml:"default_effort"`
	// ModelOverrides customizes capability metadata after ResolveModel selects a
	// concrete model from a multi-model provider. Use it when a gateway exposes
	// mixed DeepSeek/OpenAI/no-reasoning or mixed vision/text models under one
	// base_url/key.
	ModelOverrides map[string]ProviderModelOverride `toml:"model_overrides"`
	visionOverride *bool
	// NoProxy reaches this provider's base_url directly, never through the proxy.
	// For China-only endpoints a foreign-exit proxy resets the TLS handshake (#2803).
	NoProxy bool `toml:"no_proxy"`
}

type ProviderModelOverride struct {
	ReasoningProtocol string   `toml:"reasoning_protocol"`
	SupportedEfforts  []string `toml:"supported_efforts"`
	DefaultEffort     string   `toml:"default_effort"`
	Vision            *bool    `toml:"vision"`
	// ContextWindow overrides the provider-wide context budget for this model.
	// Zero inherits ProviderEntry.ContextWindow so existing configurations keep
	// their current compaction behavior.
	ContextWindow int `toml:"context_window"`
	// MaxOutputTokens overrides the provider-wide output budget. Zero inherits;
	// positive values set a cap and negative values omit optional wire limits.
	MaxOutputTokens int `toml:"max_output_tokens"`
	// ModelCapability overrides the provider-wide capability tier. Valid values:
	// auto|strong|standard|lite. Empty inherits provider-level setting.
	ModelCapability string `toml:"model_capability"`
}

// ModelList returns the models this provider exposes: the explicit `models` list,
// or the single `model` as a one-element list (back-compat). Empty if neither set.
func (e *ProviderEntry) ModelList() []string {
	if len(e.Models) > 0 {
		return e.Models
	}
	if e.Model != "" {
		return []string{e.Model}
	}
	return nil
}

// IsLikelyChatModel reports whether a model ID looks like a chat/completion
// model rather than a specialised audio/vision/embedding model. It applies a
// conservative name-based heuristic — the OpenAI-compatible /models API does
// not return capability/modality metadata, so this is the most reliable
// fallback until providers add such fields.
//
// The heuristic works in two passes:
//  1. Multi-word substring check for compound terms that span separators
//     (e.g. "text-embedding", "text-to-speech").
//  2. Token-level check: the model ID is split on common separators (- _ . / :)
//     and each token is compared against a set of known non-chat keywords.
//
// "voice" is intentionally absent from the non-chat set because it is too
// broad — legitimate future chat models may include it in their name.
func IsLikelyChatModel(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	lower := strings.ToLower(model)

	// Pass 1: compound terms that span separator boundaries.
	var compoundNonChat = []string{
		"text-embedding", "text-to-speech", "speech-to-text",
	}
	for _, c := range compoundNonChat {
		if strings.Contains(lower, c) {
			return false
		}
	}

	// Pass 2: token-level check.
	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/' || r == ':'
	})
	var nonChatTokens = map[string]bool{
		"asr": true, "stt": true, "tts": true,
		"whisper": true, "embedding": true,
		"moderation": true, "rerank": true, "dall": true,
		"transcription": true,
	}
	for _, tok := range tokens {
		if nonChatTokens[tok] {
			return false
		}
	}
	return true
}

// ChatModelList returns ModelList filtered to likely chat/completion models.
// Non-chat models (TTS, STT, ASR, embedding, etc.) are excluded so they do
// not appear in the chat model picker. Use ModelList() only when the full
// raw provider model list is needed, such as config serialization, provider
// diagnostics, or model-fetch editing.
func (e *ProviderEntry) ChatModelList() []string {
	raw := e.ModelList()
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		if IsLikelyChatModel(m) {
			out = append(out, m)
		}
	}
	return out
}

// DefaultModel returns the provider's default model: the explicit `default`, else
// the first of ModelList.
func (e *ProviderEntry) DefaultModel() string {
	if e.Default != "" {
		return e.Default
	}
	if l := e.ModelList(); len(l) > 0 {
		return l[0]
	}
	return ""
}

// HasModel reports whether m is one of the provider's models.
func (e *ProviderEntry) HasModel(m string) bool {
	for _, x := range e.ModelList() {
		if x == m {
			return true
		}
	}
	return false
}

// PriceForModel returns the configured per-1M-token price for model. Per-model
// prices win; the legacy provider-wide price is a fallback for older configs.
func (e *ProviderEntry) PriceForModel(model string) *provider.Pricing {
	if e == nil {
		return nil
	}
	if e.Prices != nil {
		if p := e.Prices[strings.TrimSpace(model)]; p != nil {
			return clonePricing(p)
		}
	}
	return clonePricing(e.Price)
}

func (e *ProviderEntry) applyModelPrice() {
	if e == nil {
		return
	}
	e.Price = e.PriceForModel(e.Model)
}

func (e *ProviderEntry) applyModelOverride() {
	if e == nil || len(e.ModelOverrides) == 0 {
		return
	}
	ov, ok := e.modelOverrideForModel(e.Model)
	if !ok {
		return
	}
	if ov.ReasoningProtocol != "" {
		e.ReasoningProtocol = ov.ReasoningProtocol
	}
	if ov.SupportedEfforts != nil {
		e.SupportedEfforts = append([]string(nil), ov.SupportedEfforts...)
	}
	if ov.DefaultEffort != "" || ov.SupportedEfforts != nil {
		e.DefaultEffort = ov.DefaultEffort
	}
	if ov.Vision != nil {
		e.visionOverride = ov.Vision
	}
	if ov.ContextWindow > 0 {
		e.ContextWindow = ov.ContextWindow
	}
	if ov.MaxOutputTokens != 0 {
		e.MaxOutputTokens = ov.MaxOutputTokens
	}
}

func (e *ProviderEntry) modelOverrideForModel(model string) (ProviderModelOverride, bool) {
	model = strings.TrimSpace(model)
	if e == nil || model == "" || len(e.ModelOverrides) == 0 {
		return ProviderModelOverride{}, false
	}
	if ov, ok := e.ModelOverrides[model]; ok {
		return ov, true
	}
	for k, ov := range e.ModelOverrides {
		if strings.EqualFold(strings.TrimSpace(k), model) {
			return ov, true
		}
	}
	return ProviderModelOverride{}, false
}

func clonePricing(p *provider.Pricing) *provider.Pricing {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

// ToolsConfig selects which built-in tools are enabled. Empty means all of them.
// WebSearchConfig configures the local web_search built-in tool. The tool
// queries a search backend directly (independent of the provider), so it works
// with every provider kind. Engine is one of searxng, brave, or tavily; an
// empty engine disables the tool. Brave and tavily require APIKey; searxng
// requires BaseURL (a self-hosted instance, e.g. https://search.example.com).
// When a local engine is configured, provider-side server web_search toggles
// are suppressed so the model sees a single "web_search" tool.
type WebSearchConfig struct {
	Engine     string `toml:"engine"`
	BaseURL    string `toml:"base_url"`
	APIKey     string `toml:"api_key"`
	MaxResults int    `toml:"max_results"`
}

// Enabled reports whether the local web_search tool is configured.
func (w WebSearchConfig) Enabled() bool {
	return strings.TrimSpace(w.Engine) != ""
}

type ToolsConfig struct {
	Enabled                  []string             `toml:"enabled"`
	BashTimeoutSeconds       *int                 `toml:"bash_timeout_seconds"`
	MCPStartupTimeoutSeconds *int                 `toml:"mcp_startup_timeout_seconds"`
	MCPCallTimeoutSeconds    *int                 `toml:"mcp_call_timeout_seconds"`
	BackgroundJobs           BackgroundJobsConfig `toml:"background_jobs"`
	Search                   SearchConfig         `toml:"search"`
	Shell                    ShellConfig          `toml:"shell"`
}

const (
	defaultBashTimeoutSeconds             = 120
	defaultMCPStartupTimeoutSeconds       = 30
	defaultMCPCallTimeoutSeconds          = 300
	defaultBackgroundJobStalledWarningSec = 900
	maxBackgroundJobStalledWarningSec     = 86400
)

// BashTimeoutSeconds returns the foreground bash timeout in seconds. An omitted
// config keeps the historical 120s safety cap, explicit 0 disables the
// tool-local cap, and positive values set a custom cap. Negative values fall
// back to the default so a typo cannot silently remove the safety net.
func (c *Config) BashTimeoutSeconds() int {
	if c.Tools.BashTimeoutSeconds == nil || *c.Tools.BashTimeoutSeconds < 0 {
		return defaultBashTimeoutSeconds
	}
	return *c.Tools.BashTimeoutSeconds
}

// MCPCallTimeoutSeconds returns the default MCP JSON-RPC call timeout in
// seconds. Omitted, zero, and negative values keep the built-in safety cap so a
// hung MCP server cannot block a turn indefinitely.
func (c *Config) MCPCallTimeoutSeconds() int {
	if c.Tools.MCPCallTimeoutSeconds == nil || *c.Tools.MCPCallTimeoutSeconds <= 0 {
		return defaultMCPCallTimeoutSeconds
	}
	return *c.Tools.MCPCallTimeoutSeconds
}

// MCPStartupTimeoutSeconds returns the background initialize + tools/list
// safety cap. Omitted, zero, and negative values keep the built-in default so
// a slow but healthy MCP can outlive the short interactive wait without running
// indefinitely.
func (c *Config) MCPStartupTimeoutSeconds() int {
	if c.Tools.MCPStartupTimeoutSeconds == nil || *c.Tools.MCPStartupTimeoutSeconds <= 0 {
		return defaultMCPStartupTimeoutSeconds
	}
	return *c.Tools.MCPStartupTimeoutSeconds
}

// BackgroundJobsConfig tunes parent-created background jobs.
type BackgroundJobsConfig struct {
	StalledWarningSeconds *int `toml:"stalled_warning_seconds"`
}

// BackgroundJobStalledWarningSeconds returns the stalled warning threshold in
// seconds. Omitted/negative values keep the default, explicit 0 disables the
// notice, and oversized values clamp to one day so a typo cannot become
// effectively invisible.
func (c *Config) BackgroundJobStalledWarningSeconds() int {
	if c.Tools.BackgroundJobs.StalledWarningSeconds == nil || *c.Tools.BackgroundJobs.StalledWarningSeconds < 0 {
		return defaultBackgroundJobStalledWarningSec
	}
	if *c.Tools.BackgroundJobs.StalledWarningSeconds > maxBackgroundJobStalledWarningSec {
		return maxBackgroundJobStalledWarningSec
	}
	return *c.Tools.BackgroundJobs.StalledWarningSeconds
}

// SearchConfig tunes the grep tool's engine. Engine is "auto" (default — use
// ripgrep when it's on PATH, else the native Go scanner), "native" (always Go),
// or "rg" (require ripgrep; warn at startup and fall back to native if absent).
// RgPath optionally points at a specific ripgrep binary instead of a PATH lookup.
type SearchConfig struct {
	Engine string `toml:"engine"`
	RgPath string `toml:"rg_path"`
}

// ShellConfig chooses the interpreter the bash tool runs commands under. Prefer
// is "auto" (default — real bash when present, else PowerShell on Windows),
// "bash", or "powershell"/"pwsh" (force it; warn at startup and fall back to
// auto if absent). Path optionally points at a specific shell executable.
type ShellConfig struct {
	Prefer string `toml:"prefer"`
	Path   string `toml:"path"`
}

// PermissionsConfig declares the per-call permission policy (see
// internal/permission). Mode is the fallback decision for writer tools when no
// rule matches ("ask" | "allow" | "deny"; default "ask"); read-only tools always
// fall back to allow. Allow/Ask/Deny are rule lists of the form "ToolName" or
// "ToolName(glob)". Precedence: deny > ask > allow > fallback.
type PermissionsConfig struct {
	Mode             string   `toml:"mode"`
	Allow            []string `toml:"allow"`
	Ask              []string `toml:"ask"`
	Deny             []string `toml:"deny"`
	AllowDynamicBash bool     `toml:"allow_dynamic_bash"`
}

// MCPConfigSource records where a merged MCP entry came from. It is runtime
// provenance only and is never serialized back into TOML or .mcp.json.
type MCPConfigSource string

const (
	MCPSourceUnknown        MCPConfigSource = ""
	MCPSourceUserConfig     MCPConfigSource = "user_config"
	MCPSourceProjectConfig  MCPConfigSource = "project_config"
	MCPSourceProjectMCPJSON MCPConfigSource = "project_mcp_json"
	MCPSourceLegacyUser     MCPConfigSource = "legacy_user_config"
	MCPSourcePluginPackage  MCPConfigSource = "plugin_package"
)

func (s MCPConfigSource) UserAuthorized() bool {
	switch s {
	case MCPSourceUserConfig, MCPSourceLegacyUser, MCPSourcePluginPackage,
		MCPSourceProjectConfig, MCPSourceProjectMCPJSON:
		return true
	default:
		return false
	}
}

// ProjectScoped reports whether an MCP entry belongs to one workspace. Project
// scope remains useful for provenance, activation, and relative-path handling;
// it no longer implies a separate launch-approval workflow.
func (s MCPConfigSource) ProjectScoped() bool {
	return s == MCPSourceProjectConfig || s == MCPSourceProjectMCPJSON
}

// PluginEntry declares an external MCP server. Type selects the transport:
// "stdio" (default) launches Command/Args/Env as a subprocess; "http"
// (a.k.a. streamable-http) and "sse" connect to a remote URL with optional
// static Headers. String fields support ${VAR} / ${VAR:-default} expansion so
// secrets (bearer tokens, keys) come from the environment, not the file. The
// fields mirror Claude Code's mcpServers spec, so entries can come from either
// .corvus/config.toml's [[plugins]] or a project-root .mcp.json (see loadMCPJSON).
type PluginEntry struct {
	Name    string            `toml:"name"`
	Type    string            `toml:"type"` // "stdio" (default) | "http" | "sse"
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`
	// StartupTimeoutSeconds overrides [tools].mcp_startup_timeout_seconds for
	// initialize + tools/list. Zero keeps the global/default cap.
	StartupTimeoutSeconds int `toml:"startup_timeout_seconds"`
	// CallTimeoutSeconds overrides the default per-call deadline for this MCP
	// server. Zero falls back to [tools].mcp_call_timeout_seconds.
	CallTimeoutSeconds int `toml:"call_timeout_seconds"`
	// ToolTimeoutSeconds overrides the per-call deadline for raw MCP tool names
	// from this server. Keys are server-local tool names, not model-visible
	// mcp__server__tool names.
	ToolTimeoutSeconds map[string]int `toml:"tool_timeout_seconds"`
	// AutoStart controls whether the server connects during session startup.
	// Nil preserves historical behavior: configured servers start automatically.
	AutoStart *bool `toml:"auto_start"`
	// Tier is a legacy compatibility field. New config rendering omits it; enabled
	// MCP servers connect automatically in the background unless auto_start=false.
	// Historical values are accepted for old files:
	//   "eager"      — blocks startup until the handshake completes; required for
	//                  servers whose tools the system prompt depends on.
	//   "lazy"       — legacy alias for background.
	//   "background" — placeholder + spawn fired at boot but not waited on;
	//                  swap happens once the spawn finishes.
	// Empty defaults to "background" so enabled MCPs connect automatically
	// without blocking chat. Unknown non-empty values fall back to "background".
	Tier         string          `toml:"tier"`
	Source       MCPConfigSource `toml:"-" json:"-"`
	expansionEnv map[string]string
}

func (e PluginEntry) ShouldAutoStart() bool {
	return e.AutoStart == nil || *e.AutoStart
}

// ResolvedTier returns the normalized tier ("eager"|"background") with the
// project default applied. Legacy lazy and unknown values fall back to
// background so enabled MCPs are available without manual connection.
//
// Tier no longer changes runtime process start timing; it remains for config
// compatibility and diagnostics only.
func (e PluginEntry) ResolvedTier() string {
	return resolvedMCPTier(e.Tier)
}

func resolvedMCPTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "eager":
		return "eager"
	case "background", "lazy":
		return "background"
	case "":
		return "background"
	default:
		return "background"
	}
}

// EnabledPlugins returns catalog-enabled MCP entries for workspace, consulting
// the activation store when provided.
func (c *Config) EnabledPlugins(workspace string, activation *MCPActivationStore) []PluginEntry {
	if c == nil {
		return nil
	}
	out := make([]PluginEntry, 0, len(c.Plugins))
	for _, p := range c.Plugins {
		enabled := p.ShouldAutoStart()
		if activation != nil {
			if resolved, err := activation.IsEnabled(p, workspace); err == nil {
				enabled = resolved
			}
		}
		if enabled {
			out = append(out, p)
		}
	}
	return out
}

// DefaultSystemPrompt is used when config provides none.
const DefaultSystemPrompt = `You are Corvus, a coding agent. Use the available tools when they help complete the user's request. Keep changes focused and replies concise.`

// UserDecisionPolicy is appended to every system prompt, including user-custom
// prompts, so custom personas cannot accidentally remove the `ask` UI contract.
const UserDecisionPolicy = `Consequential decisions with no safe, obvious default are user-owned: call the ask tool, never ask in prose. Otherwise proceed with a sensible reversible default; in non-interactive runs, state the assumption and take the safest reversible path.`

// LanguagePolicy is the auto fallback appended to the system prompt when no
// concrete UI language is resolved. It splits thinking from replies: reasoning
// is always English; the reply language follows the user's most recent message.
// It is static English text, so it stays part of the cache-stable prefix and
// avoids per-turn language injection.
const LanguagePolicy = `Think in English: all reasoning and thinking text is English, whatever language the conversation is in. Reply in the language of the user's most recent message, switching whenever they switch. Keep code, identifiers, file paths, shell commands, and technical terms untranslated.`

// Default returns the built-in default configuration.
func Default() *Config {
	return &Config{
		ConfigVersion:    5,
		DefaultModel:     "deepseek-flash",
		CredentialsStore: CredentialsStoreAuto,
		UI:               UIConfig{Theme: "auto"},
		Agent: AgentConfig{
			SystemPrompt: DefaultSystemPrompt,
			// Normal interactive execution has no configurable total round cap. It
			// is bounded by adaptive progress guards and context compaction instead.
			MaxSteps:               0,
			PlannerMaxSteps:        0,
			AutoPlan:               "off",
			SoftCompactRatio:       0.5,
			ToolResultSnipRatio:    0.6,
			CompactRatio:           0.8,
			CompactForceRatio:      0.9,
			MaxSubagentDepth:       2,
			MaxSubagentConcurrency: 6,
			MaxParallelWriters:     3,
		},
		// Mode "ask" with no rules keeps `corvus run` autonomous (no TTY → ask
		// resolves to allow) while `corvus` prompts before writers. Users add
		// deny/allow rules to harden or quiet specific tools.
		Permissions: PermissionsConfig{Mode: "ask"},
		// Sandbox uses platform defaults: macOS/Linux jail bash by default;
		// Windows has no OS-level Bash sandbox and always forces bash off.
		// Network=true here so an absent [sandbox] in a user's file keeps egress
		// (zero value would wrongly deny it).
		Sandbox: SandboxConfig{Network: true},
		// LSP tools on by default, but dormant until a language server is on PATH;
		// a missing server yields an install hint rather than an error.
		LSP:           LSPConfig{Enabled: true},
		Network:       NetworkConfig{ProxyMode: netclient.ModeAuto},
		NetworkPolicy: NetworkPolicyConfig{Default: "allow"},
		Providers: []ProviderEntry{
			{Name: "deepseek-flash", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash", APIKeyEnv: "DEEPSEEK_API_KEY", BalanceURL: "https://api.deepseek.com/user/balance", ContextWindow: 1_000_000, Price: deepSeekV4FlashPriceUSD()},
			{Name: "deepseek-pro", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro", APIKeyEnv: "DEEPSEEK_API_KEY", BalanceURL: "https://api.deepseek.com/user/balance", ContextWindow: 1_000_000, Price: deepSeekV4ProPriceUSD()},
		},
	}
}

// WriteFile writes the configuration to path as annotated TOML. The write is
// atomic + fsynced so an interrupted write or power loss can never truncate the
// main config into an unparseable state that leaves the app with no usable
// models (#4615, #4708).
func (c *Config) WriteFile(path string) error {
	return atomicWriteToConfigFile(path, RenderTOMLForScope(c, renderScopeForPath(path)), configFilePerm(path))
}

// Provider returns the named provider entry.
func (c *Config) Provider(name string) (*ProviderEntry, bool) {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i], true
		}
	}
	return nil, false
}

// ResolveModel resolves a model reference to a provider entry whose Model is the
// selected model string (a copy, so the config's lists stay intact). It accepts:
//   - "provider/model" — that exact model under that provider;
//   - a provider name   — the provider's default model;
//   - a bare model name — the (first) provider that lists it.
//
// The returned entry is ready to build a provider from (NewProvider reads .Model),
// so a single "vendor with many models" entry yields one instance per model
// without duplicating base_url/api_key_env. Single-`model` entries still resolve
// by provider name, keeping older configs working unchanged.
func (c *Config) ResolveModel(ref string) (*ProviderEntry, bool) {
	if ref == "" {
		return nil, false
	}
	if access := uiProviderAccessMap(c.UI.ProviderAccess); len(access) > 0 {
		ref = retargetOfficialProviderRef(ref, access)
	}
	// "provider/model"
	if prov, model, ok := strings.Cut(ref, "/"); ok {
		if e, found := c.Provider(prov); found && e.HasModel(model) {
			cp := *e
			cp.Model = model
			cp.applyModelPrice()
			cp.applyModelOverride()
			return &cp, true
		}
	}
	// a provider name → its default model
	if e, found := c.Provider(ref); found {
		cp := *e
		cp.Model = e.DefaultModel()
		cp.applyModelPrice()
		cp.applyModelOverride()
		return &cp, true
	}
	// a bare model name → the provider that lists it
	for i := range c.Providers {
		if c.Providers[i].HasModel(ref) {
			cp := c.Providers[i]
			cp.Model = ref
			cp.applyModelPrice()
			cp.applyModelOverride()
			return &cp, true
		}
	}
	return nil, false
}

// ResolveModelWithFallback resolves a model reference to the canonical
// "provider/model" form used by the desktop runtime. If ref is stale or empty,
// it tries the user's configured default_model before falling back to the first
// configured provider — so preference isn't overwritten by iteration order.
func (c *Config) ResolveModelWithFallback(ref string) (resolvedRef string, fallback bool, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		if e, found := c.ResolveModel(ref); found {
			return e.Name + "/" + e.Model, false, true
		}
	}
	// Before falling back to the first configured provider (which may not be the
	// user's preferred choice), try the configured default_model.  Skip when ref
	// already WAS the DefaultModel (it already failed above, so retrying won't
	// help) or when the default provider has no API key configured.
	if ref != c.DefaultModel && c.DefaultModel != "" {
		if e, found := c.ResolveModel(c.DefaultModel); found && e.Configured() {
			return e.Name + "/" + e.Model, true, true
		}
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		// Skip providers with no models or no API key: falling back onto a keyless
		// provider just boots the tab onto something that fails on first use. Mirrors
		// the Configured() gate the provider-removal/selection paths already apply.
		if len(p.ModelList()) == 0 || !p.Configured() {
			continue
		}
		return p.Name + "/" + p.DefaultModel(), true, true
	}
	return "", false, false
}

// ResolveNewSessionChatModel selects the model for a newly-created chat
// session. Configured candidates win; if every chat candidate is keyless, the
// valid default (or first chat model) is preserved so callers can surface their
// existing missing-key recovery UI. An unknown default is also preserved for
// the CLI's actionable configuration error. Provider order is otherwise stable.
func (c *Config) ResolveNewSessionChatModel() (resolvedRef string, fallback bool, ok bool) {
	return c.resolveNewSessionChatModel(nil, true)
}

func (c *Config) resolveNewSessionChatModel(providerAllowed func(string) bool, preserveUnknownDefault bool) (resolvedRef string, fallback bool, ok bool) {
	if c == nil {
		return "", false, false
	}
	if providerAllowed == nil {
		providerAllowed = func(string) bool { return true }
	}

	def := strings.TrimSpace(c.DefaultModel)
	keylessDefault := ""
	if def != "" {
		if entry, found := c.ResolveModel(def); found {
			if providerAllowed(entry.Name) && IsLikelyChatModel(entry.Model) {
				if entry.Configured() {
					return def, false, true
				}
				keylessDefault = def
			}
		} else if preserveUnknownDefault {
			// CLI/boot callers need the stale value intact so their existing
			// unknown-model error can name it and explain the providers that
			// replaced it. Desktop uses its recovery UI and does not preserve it.
			return def, false, true
		}
	}

	keylessFallback := ""
	for i := range c.Providers {
		p := &c.Providers[i]
		if !providerAllowed(p.Name) {
			continue
		}
		chatModels := p.ChatModelList()
		if len(chatModels) == 0 {
			continue
		}
		model := chatModels[0]
		for _, candidate := range chatModels {
			if candidate == p.DefaultModel() {
				model = candidate
				break
			}
		}
		resolved := p.Name + "/" + model
		if p.Configured() {
			return resolved, true, true
		}
		if keylessFallback == "" {
			keylessFallback = resolved
		}
	}
	if keylessDefault != "" {
		return keylessDefault, false, true
	}
	if keylessFallback != "" {
		return keylessFallback, true, true
	}
	return "", false, false
}

// EffectiveAPIKey resolves the entry's API key. A key configured directly via api_key in
// .corvus/config.toml (user config first, then project) wins; otherwise the
// value resolved at load time from the legacy api_key_env path is returned,
// falling back to project .env (cwd) then Corvus's persistent credentials store.
func (e *ProviderEntry) EffectiveAPIKey() string {
	if e == nil {
		return ""
	}
	if e.resolvedAPIKey != "" {
		return e.resolvedAPIKey
	}
	if v := strings.TrimSpace(e.APIKey); v != "" {
		return v
	}
	if e.APIKeyEnv == "" {
		return ""
	}
	return ResolveCredentialForRootGlobalFirst(".", e.APIKeyEnv).Value
}

// ResolveAPIKeyFromProcessEnvForProbe pins a setup-time, user-entered key onto
// this entry for an immediate connectivity probe. Normal runtime resolution does
// not call this; loaded provider entries resolve from project .env first, then
// Corvus's persistent credentials store.
func (e *ProviderEntry) ResolveAPIKeyFromProcessEnvForProbe() {
	if e == nil {
		return
	}
	key := strings.TrimSpace(e.APIKeyEnv)
	if key == "" {
		return
	}
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return
	}
	e.resolvedAPIKey = value
	e.resolvedSource = CredentialSource{Kind: CredentialSourceEnvironment, Label: "setup prompt"}
}

func (e *ProviderEntry) APIKeySourceLabel() string {
	if e == nil {
		return ""
	}
	if e.resolvedAPIKey != "" {
		return credentialSourceLabel(e.resolvedSource)
	}
	if v := strings.TrimSpace(e.APIKey); v != "" {
		return credentialSourceLabel(CredentialSource{Kind: CredentialSourceConfigFile})
	}
	if strings.TrimSpace(e.APIKeyEnv) == "" {
		return ""
	}
	return ResolveCredentialForRootGlobalFirst(".", e.APIKeyEnv).Source.Label
}

// RequiresAPIKey reports whether this provider should be hidden/validated when
// neither api_key nor api_key_env is set. A blank pair means the provider is
// intentionally no-auth. Local OpenAI-compatible gateways often keep a legacy
// api_key_env in config even though they accept unauthenticated requests, so
// loopback/private endpoints are also allowed to run without a resolved key.
func (e *ProviderEntry) RequiresAPIKey() bool {
	if e == nil {
		return false
	}
	if strings.TrimSpace(e.APIKeyEnv) == "" && strings.TrimSpace(e.APIKey) == "" {
		return providerBaseURLRequiresAPIKey(e.BaseURL)
	}
	return !providerBaseURLAllowsMissingAPIKey(e.BaseURL)
}

func providerBaseURLRequiresAPIKey(raw string) bool {
	switch officialProviderHost(raw) {
	case "api.deepseek.com", "api.xiaomimimo.com", "token-plan-cn.xiaomimimo.com", "api.minimaxi.com", "api.openai.com":
		return true
	default:
		return false
	}
}

func providerBaseURLAllowsMissingAPIKey(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.Trim(strings.ToLower(u.Hostname()), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast()
}

// Configured reports whether the provider is selectable. Providers that do not
// require an API key are configured by definition; providers that name an env var
// require that variable to resolve unless their endpoint is local/private.
func (e *ProviderEntry) Configured() bool {
	return e != nil && (!e.RequiresAPIKey() || e.EffectiveAPIKey() != "")
}

// ResolveSystemPrompt returns the system prompt, reading system_prompt_file if set.
func (c *Config) ResolveSystemPrompt() (string, error) {
	return c.ResolveSystemPromptForRoot(".")
}

// ResolveSystemPromptForRoot is like ResolveSystemPrompt but resolves a relative
// system_prompt_file against root. Desktop tabs pass their workspace root here so
// prompt files are project-scoped even when the process cwd is elsewhere. A path
// inherited from user config may fall back to Corvus home, while a path chosen
// by project config is confined to the workspace and never probes user files.
func (c *Config) ResolveSystemPromptForRoot(root string) (string, error) {
	path := c.Agent.SystemPromptFile
	if path == "" {
		return c.InlineSystemPrompt(), nil
	}

	if c.systemPromptFileSource == promptFileSourceProject {
		if filepath.IsAbs(path) || !filepath.IsLocal(filepath.Clean(path)) {
			return "", fmt.Errorf("project system_prompt_file %q must be a relative path within the workspace", path)
		}
		candidate := filepath.Join(resolveRoot(root), path)
		b, err := readProjectSystemPromptFile(root, path)
		if err != nil {
			return "", newSystemPromptFileError(path, []string{candidate}, []error{err})
		}
		return strings.TrimSpace(string(b)), nil
	}

	if filepath.IsAbs(path) {
		b, err := fileencoding.ReadFileUTF8(path)
		if err != nil {
			return "", newSystemPromptFileError(path, []string{path}, []error{err})
		}
		return strings.TrimSpace(string(b)), nil
	}

	candidates := []string{filepath.Join(resolveRoot(root), path)}
	if home := CorvusHomeDir(); home != "" {
		homeCandidate := filepath.Join(home, path)
		if filepath.Clean(homeCandidate) != filepath.Clean(candidates[0]) {
			candidates = append(candidates, homeCandidate)
		}
	}
	readErrors := make([]error, 0, len(candidates))
	for _, candidate := range candidates {
		b, err := fileencoding.ReadFileUTF8(candidate)
		if err == nil {
			return strings.TrimSpace(string(b)), nil
		}
		readErrors = append(readErrors, fmt.Errorf("%s: %w", candidate, err))
	}
	return "", newSystemPromptFileError(path, candidates, readErrors)
}

func readProjectSystemPromptFile(root, path string) ([]byte, error) {
	workspace, err := filepath.Abs(resolveRoot(root))
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	rootHandle, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, fmt.Errorf("open workspace root %q: %w", workspace, err)
	}
	defer rootHandle.Close()
	f, err := rootHandle.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return fileencoding.DecodeToUTF8(b), nil
}

func newSystemPromptFileError(configured string, candidates []string, readErrors []error) error {
	allMissing := len(readErrors) > 0
	for _, err := range readErrors {
		if !errors.Is(err, fs.ErrNotExist) {
			allMissing = false
			break
		}
	}
	return &systemPromptFileError{
		configured: configured,
		candidates: append([]string(nil), candidates...),
		errors:     append([]error(nil), readErrors...),
		allMissing: allMissing,
	}
}

// InlineSystemPrompt returns the configured system_prompt, or DefaultSystemPrompt
// when unset. It is the fallback when system_prompt_file cannot be read.
func (c *Config) InlineSystemPrompt() string {
	if strings.TrimSpace(c.Agent.SystemPrompt) == "" {
		return DefaultSystemPrompt
	}
	return c.Agent.SystemPrompt
}

// Validate checks that the selected model's provider is usable.
func (c *Config) Validate(model string) error {
	e, ok := c.ResolveModel(model)
	if !ok {
		return fmt.Errorf("unknown model %q (configured: %s)", model, c.providerNames())
	}
	if e.Kind == "" {
		return fmt.Errorf("provider %q: kind is required", model)
	}
	if e.BaseURL == "" {
		return fmt.Errorf("provider %q: base_url is required", model)
	}
	if strings.TrimSpace(e.APIKeyEnv) != "" && !IsValidCredentialKey(e.APIKeyEnv) {
		return fmt.Errorf("provider %q: api_key_env %q is invalid; use letters, numbers, and underscores, not a model name", model, e.APIKeyEnv)
	}
	if e.RequiresAPIKey() && e.EffectiveAPIKey() == "" {
		if strings.TrimSpace(e.APIKeyEnv) != "" {
			return fmt.Errorf("provider %q: missing API key (set api_key in .corvus/config.toml or provide env %s)", model, e.APIKeyEnv)
		}
		return fmt.Errorf("provider %q: missing API key (set api_key in .corvus/config.toml)", model)
	}
	return nil
}

func (c *Config) providerNames() string {
	names := make([]string, len(c.Providers))
	for i, p := range c.Providers {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}
