package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"corvus/internal/provider"
)

type RenderScope string

const (
	RenderScopeFull    RenderScope = "full"
	RenderScopeUser    RenderScope = "user"
	RenderScopeProject RenderScope = "project"
)

// RenderTOML renders the config as annotated TOML in the `corvus setup` house style:
// comments preserved, system_prompt as a multi-line string, helpful hints. The
// output round-trips back through Load (see render_test.go).
func RenderTOML(c *Config) string {
	return RenderTOMLForScope(c, RenderScopeFull)
}

// RenderTOMLForScope renders an annotated TOML file for a specific persistence
// target. User configs can carry account-level preferences; project
// .corvus/config.toml stays focused on project behavior and excludes user-level
// preferences such as pricing currency.
func RenderTOMLForScope(c *Config, scope RenderScope) string {
	if c == nil {
		c = Default()
	}
	switch scope {
	case RenderScopeUser, RenderScopeProject:
	default:
		scope = RenderScopeFull
	}
	if scope == RenderScopeProject {
		c = projectScopedConfigForRender(c)
	}
	defaults := Default()
	var b strings.Builder

	b.WriteString("# Corvus configuration.\n")
	fmt.Fprintf(&b, "# Resolution order: flag > project .corvus/config.toml > %s > built-in defaults.\n", userConfigDisplayPath())
	b.WriteString("# API keys: set api_key on a [[providers]] entry; the user config above wins over the project config.\n")
	b.WriteString("# (Legacy api_key_env still works: keys live in Corvus's global .env instead.)\n\n")

	fmt.Fprintf(&b, "config_version = %d   # schema marker for diagnostics; old versions may ignore it\n", configVersion(c))
	fmt.Fprintf(&b, "default_model = %q\n", c.DefaultModel)
	if c.Language != "" {
		fmt.Fprintf(&b, "language      = %q   # ui/model language; empty = auto-detect from $LANG / $CORVUS_LANG\n", c.Language)
	} else {
		b.WriteString("# language      = \"zh\"   # ui/model language; empty = auto-detect from $LANG / $CORVUS_LANG\n")
	}
	if scope != RenderScopeProject {
		fmt.Fprintf(&b, "credentials_store = %q   # legacy compatibility; provider keys are saved in Corvus's global .env\n", normalizeCredentialsStore(c.CredentialsStore))
	}
	b.WriteString("\n")

	if shouldRenderUI(c, defaults, scope) {
		b.WriteString("[ui]\n")
		fmt.Fprintf(&b, "theme = %q   # auto|dark|light; CLI colors only; CORVUS_THEME can override per run\n", c.UITheme())
		if style := c.UIThemeStyle(); style != "" {
			fmt.Fprintf(&b, "theme_style = %q   # CLI accent palette; CORVUS_THEME_STYLE can override per run\n", style)
		} else {
			b.WriteString("# theme_style = \"graphite\"   # graphite|aurora|slate|carbon|nocturne|amber and legacy aliases\n")
		}
		if layout := c.UIShortcutLayout(); layout != "classic" {
			fmt.Fprintf(&b, "shortcut_layout = %q   # classic|desktop; compatibility setting; Shift+Tab toggles Plan, Ctrl+Y toggles YOLO\n", layout)
		} else {
			b.WriteString("# shortcut_layout = \"desktop\"   # classic|desktop; compatibility setting; Shift+Tab toggles Plan, Ctrl+Y toggles YOLO\n")
		}
		if strings.TrimSpace(c.UI.CursorShape) != "" {
			fmt.Fprintf(&b, "cursor_shape = %q   # block|underline|bar; text input cursor shape\n", c.UICursorShape())
		} else {
			b.WriteString("# cursor_shape = \"block\"   # block|underline|bar; text input cursor shape\n")
		}
		if c.UI.ShowReasoning {
			b.WriteString("show_reasoning = true   # CLI: show thinking text by default; false = collapsed (toggle with Ctrl+O)\n")
		} else {
			b.WriteString("# show_reasoning = true   # CLI: show thinking text by default; false = collapsed (toggle with Ctrl+O)\n")
		}
		if scope != RenderScopeProject {
			if currency := c.PricingCurrency(); currency != "" {
				fmt.Fprintf(&b, "currency = %q   # official pricing currency: CNY|USD; empty/auto follows language\n", currency)
			} else {
				b.WriteString("# currency = \"USD\"   # official pricing currency: CNY|USD; empty/auto follows language\n")
			}
		}
		if c.UI.ProviderAccess != nil {
			fmt.Fprintf(&b, "provider_access = %s   # providers enabled by the TUI setup flow\n", renderStringArray(c.UI.ProviderAccess))
		}
		b.WriteString("\n")
	}

	if shouldRenderNetwork(c, defaults, scope) {
		b.WriteString("[network]\n")
		fmt.Fprintf(&b, "proxy_mode = %q   # auto|env|custom|off; auto currently uses env proxy\n", c.NetworkProxyMode())
		if c.Network.ProxyURL != "" {
			fmt.Fprintf(&b, "proxy_url  = %q   # custom override, e.g. socks5://127.0.0.1:7890\n", c.Network.ProxyURL)
		} else {
			b.WriteString("# proxy_url  = \"socks5://127.0.0.1:7890\"   # optional custom override\n")
		}
		if c.Network.NoProxy != "" {
			fmt.Fprintf(&b, "no_proxy   = %q   # honored for proxy_mode = \"custom\"\n", c.Network.NoProxy)
		} else {
			b.WriteString("# no_proxy   = \"localhost,127.0.0.1,.local\"   # honored for proxy_mode = \"custom\"\n")
		}
		b.WriteString("\n[network.proxy]\n")
		proxyType := c.Network.Proxy.Type
		if proxyType == "" {
			proxyType = "socks5"
		}
		fmt.Fprintf(&b, "type = %q   # http|https|socks5|socks5h\n", proxyType)
		if c.Network.Proxy.Server != "" {
			fmt.Fprintf(&b, "server = %q\n", c.Network.Proxy.Server)
		} else {
			b.WriteString("# server = \"127.0.0.1\"\n")
		}
		if c.Network.Proxy.Port > 0 {
			fmt.Fprintf(&b, "port = %d\n", c.Network.Proxy.Port)
		} else {
			b.WriteString("# port = 7890\n")
		}
		if c.Network.Proxy.Username != "" {
			fmt.Fprintf(&b, "username = %q\n", c.Network.Proxy.Username)
		} else {
			b.WriteString("# username = \"\"\n")
		}
		if c.Network.Proxy.Password != "" {
			fmt.Fprintf(&b, "password = %q   # supports ${VAR} expansion\n", c.Network.Proxy.Password)
		} else {
			b.WriteString("# password = \"${CORVUS_PROXY_PASSWORD}\"   # optional; supports ${VAR} expansion\n")
		}
		b.WriteString("\n")
	}
	if shouldRenderNetworkPolicy(c, defaults, scope) {
		b.WriteString("[network_policy]\n")
		if len(c.NetworkPolicy.Allow) > 0 {
			fmt.Fprintf(&b, "allow   = %s   # hostname globs always permitted; * = one label, ** = any depth\n", renderStringArray(c.NetworkPolicy.Allow))
		} else {
			b.WriteString("# allow   = [\"docs.example.com\", \"**.github.com\"]   # hostname globs always permitted\n")
		}
		if len(c.NetworkPolicy.Deny) > 0 {
			fmt.Fprintf(&b, "deny    = %s   # hostname globs always refused; wins over allow\n", renderStringArray(c.NetworkPolicy.Deny))
		} else {
			b.WriteString("# deny    = [\"*.internal.corp\", \"10.0.0.0\"]   # hostname globs always refused; wins over allow\n")
		}
		policyDefault := strings.TrimSpace(c.NetworkPolicy.Default)
		if policyDefault == "" {
			// NetPolicy maps a blank default to "allow"; render that value so
			// a reloaded config does not churn the file with default = "".
			policyDefault = "allow"
		}
		fmt.Fprintf(&b, "default = %q   # allow (open) | deny (closed) | ask (no approval UI: falls back to allow)\n", policyDefault)
		b.WriteString("\n")
	}
	if shouldRenderEnvironment(c, defaults, scope) {
		renderEnvironmentConfig(&b, c.Environment)
	}

	b.WriteString("[agent]\n")
	if shouldRenderSystemPrompt(c, defaults, scope) {
		b.WriteString("system_prompt = \"\"\"\n")
		b.WriteString(c.Agent.SystemPrompt)
		b.WriteString("\"\"\"\n")
	} else {
		b.WriteString("# system_prompt = \"\"\"...\"\"\"   # omit to use the built-in prompt for this version\n")
	}
	if c.Agent.SystemPromptFile != "" {
		fmt.Fprintf(&b, "system_prompt_file = %q\n", c.Agent.SystemPromptFile)
	} else {
		b.WriteString("# system_prompt_file = \"prompts/system.md\"   # project paths stay in <workspace>; user paths may fall back to <corvus home>\n")
	}
	fmt.Fprintf(&b, "temperature       = %s\n", formatFloat(c.Agent.Temperature))
	if strings.TrimSpace(c.Agent.RecoveryModel) != "" {
		fmt.Fprintf(&b, "recovery_model = %q   # optional independent reviewer for low-risk automatic recovery\n", c.Agent.RecoveryModel)
	} else {
		b.WriteString("# recovery_model = \"deepseek-pro\"   # optional; falls back to guardian then main model\n")
	}
	if lang := c.ReasoningLanguage(); lang != "auto" {
		fmt.Fprintf(&b, "reasoning_language = %q   # visible reasoning language: auto|zh|en\n", lang)
	} else {
		b.WriteString("# reasoning_language = \"zh\"   # visible reasoning language: auto|zh|en\n")
	}
	fmt.Fprintf(&b, "soft_compact_ratio  = %s   # notice only; keeps cache-first prefix intact\n", formatFloat(c.Agent.SoftCompactRatio))
	fmt.Fprintf(&b, "tool_result_snip_ratio = %s   # snip stale tool results at this fraction before summary compaction\n", formatFloat(c.Agent.ToolResultSnipRatio))
	fmt.Fprintf(&b, "compact_ratio       = %s   # try compacting when prompt reaches this fraction\n", formatFloat(c.Agent.CompactRatio))
	fmt.Fprintf(&b, "compact_force_ratio = %s   # force compacting at this high-water mark\n", formatFloat(c.Agent.CompactForceRatio))
	if c.Agent.Keep != nil {
		fmt.Fprintf(&b, "keep                = %s   # compaction keep policy: errors, user_marked\n", renderStringArray(c.Agent.Keep))
	} else {
		b.WriteString("# keep                = [\"errors\"]   # compaction keep policy: errors, user_marked\n")
	}
	if c.Agent.RecentKeep > 0 {
		fmt.Fprintf(&b, "recent_keep         = %d   # minimum recent messages kept verbatim\n", c.Agent.RecentKeep)
	} else {
		b.WriteString("# recent_keep         = 2   # minimum recent messages kept verbatim\n")
	}
	fmt.Fprintf(&b, "cold_resume_prune   = %v   # elide stale tool results when reopening a session past the provider cache window\n", c.ColdResumePruneEnabled())
	if len(c.Agent.PlanModeReadOnlyCommands) > 0 {
		fmt.Fprintf(&b, "plan_mode_read_only_commands = %s   # legacy compatibility only; Plan bash uses Permissions\n", renderStringArray(c.Agent.PlanModeReadOnlyCommands))
	} else {
		b.WriteString("# plan_mode_read_only_commands = [\"gh issue view\"]   # legacy compatibility only; Plan bash uses Permissions\n")
	}
	if c.Agent.PlannerModel != "" {
		fmt.Fprintf(&b, "planner_model = %q   # low-frequency planner (two-model collaboration)\n", c.Agent.PlannerModel)
	} else {
		b.WriteString("# planner_model = \"deepseek-pro\"   # optional: enable two-model collaboration\n")
	}
	if c.Agent.SubagentModel != "" {
		fmt.Fprintf(&b, "subagent_model = %q   # default model for runAs=subagent skills\n", c.Agent.SubagentModel)
	} else {
		b.WriteString("# subagent_model = \"deepseek-pro\"   # optional default for runAs=subagent skills\n")
	}
	if len(c.Agent.SubagentModels) > 0 {
		fmt.Fprintf(&b, "subagent_models = %s   # per-skill overrides\n", renderStringMap(c.Agent.SubagentModels))
	} else {
		b.WriteString("# subagent_models = { review = \"deepseek-pro\", security_review = \"deepseek-pro\" }   # per-skill overrides\n")
	}
	if c.Agent.SubagentEffort != "" {
		fmt.Fprintf(&b, "subagent_effort = %q   # default effort for subagent entry points\n", c.Agent.SubagentEffort)
	} else {
		b.WriteString("# subagent_effort = \"high\"   # optional default effort for subagents\n")
	}
	if len(c.Agent.SubagentEfforts) > 0 {
		fmt.Fprintf(&b, "subagent_efforts = %s   # per-tool/skill effort overrides\n", renderStringMap(c.Agent.SubagentEfforts))
	} else {
		b.WriteString("# subagent_efforts = { review = \"max\", task = \"high\" }   # per-tool/skill effort overrides\n")
	}
	if c.Agent.MaxSubagentDepth != defaults.Agent.MaxSubagentDepth {
		fmt.Fprintf(&b, "max_subagent_depth = %d   # nested subagent delegation depth; 1 restores the old single-layer boundary\n", c.Agent.MaxSubagentDepth)
	} else {
		b.WriteString("# max_subagent_depth = 2   # nested subagent delegation depth; set 1 to disable nested delegation\n")
	}
	// Zero means "unset" for both knobs (NormalizeConcurrencyLimits maps it to
	// 6/3), so rendering an explicit 0 after a reload would only churn the file.
	if v := c.Agent.MaxSubagentConcurrency; v != 0 && v != defaults.Agent.MaxSubagentConcurrency {
		fmt.Fprintf(&b, "max_subagent_concurrency = %d   # session-wide sub-agent concurrency (task/fleet/skills)\n", v)
	} else {
		b.WriteString("# max_subagent_concurrency = 6   # session-wide sub-agent concurrency (task/fleet/skills)\n")
	}
	if v := c.Agent.MaxParallelWriters; v != 0 && v != defaults.Agent.MaxParallelWriters {
		fmt.Fprintf(&b, "max_parallel_writers = %d   # concurrent writers with non-overlapping write_paths\n", v)
	} else {
		b.WriteString("# max_parallel_writers = 3   # concurrent writers with non-overlapping write_paths\n")
	}
	if c.Agent.OutputStyle != "" {
		fmt.Fprintf(&b, "output_style = %q   # persona/tone folded into the prompt\n", c.Agent.OutputStyle)
	} else {
		b.WriteString("# output_style = \"explanatory\"   # explanatory | learning | concise | custom; empty = default\n")
	}
	if mode := strings.TrimSpace(c.Agent.PromptCacheKey); mode != "" && !strings.EqualFold(mode, "auto") {
		fmt.Fprintf(&b, "prompt_cache_key = %q   # sticky prompt cache key: auto|on|off|custom\n", mode)
	} else {
		b.WriteString("# prompt_cache_key = \"auto\"   # sticky prompt cache key: auto|on|off|custom\n")
	}
	if val := strings.TrimSpace(c.Agent.PromptCacheKeyValue); val != "" {
		fmt.Fprintf(&b, "prompt_cache_key_value = %q   # used only when prompt_cache_key = \"custom\"\n", val)
	} else {
		b.WriteString("# prompt_cache_key_value = \"my-stable-id\"   # used only when prompt_cache_key = \"custom\"\n")
	}
	b.WriteString("\n")
	if shouldRenderRuntimePolicy(c) {
		b.WriteString("[runtime_policy]\n")
		writeRuntimePolicyFields(&b, c.RuntimePolicy, true)
		b.WriteString("\n")
	}

	if shouldRenderProviders(c, defaults, scope) {
		for _, p := range c.Providers {
			b.WriteString("[[providers]]\n")
			fmt.Fprintf(&b, "name        = %q\n", p.Name)
			fmt.Fprintf(&b, "kind        = %q\n", p.Kind)
			fmt.Fprintf(&b, "base_url    = %q\n", p.BaseURL)
			if p.ChatURL != "" {
				fmt.Fprintf(&b, "chat_url    = %q   # optional full chat completions URL; disables automatic /chat/completions suffix\n", p.ChatURL)
			}
			if len(p.Models) > 0 {
				fmt.Fprintf(&b, "models      = %s\n", renderStringArray(p.Models))
				if p.Default != "" {
					fmt.Fprintf(&b, "default     = %q\n", p.Default)
				}
			} else if p.Model != "" {
				fmt.Fprintf(&b, "model       = %q\n", p.Model)
			}
			if p.ModelsURL != "" {
				fmt.Fprintf(&b, "models_url  = %q   # auto-fetch models from this URL on startup\n", p.ModelsURL)
			}
			if p.APIKey != "" {
				fmt.Fprintf(&b, "api_key     = %q\n", p.APIKey)
			} else {
				fmt.Fprintf(&b, "api_key_env = %q\n", p.APIKeyEnv)
			}
			if p.PresetID != "" {
				fmt.Fprintf(&b, "preset_id   = %q   # curated preset identity; settings UI uses it to avoid duplicate installs\n", p.PresetID)
			}
			if p.PresetVersion > 0 {
				fmt.Fprintf(&b, "preset_version = %d\n", p.PresetVersion)
			}
			if len(p.Headers) > 0 {
				fmt.Fprintf(&b, "headers     = %s   # extra static request headers; keep secrets in api_key\n", renderStringMap(p.Headers))
			}
			if len(p.ExtraBody) > 0 {
				fmt.Fprintf(&b, "extra_body  = %s   # extra top-level JSON request body fields for compatible gateways\n", renderAnyMap(p.ExtraBody))
			}
			if p.AuthHeader {
				b.WriteString("auth_header = true   # Anthropic-compatible: send Authorization: Bearer <api_key> instead of x-api-key\n")
			}
			if p.ResponsesMode != "" {
				fmt.Fprintf(&b, "responses_mode = %q   # responses provider: stateless|stateful\n", p.ResponsesMode)
			}
			if p.ResponsesStateful != nil {
				fmt.Fprintf(&b, "responses_stateful = %t   # legacy responses mode switch\n", *p.ResponsesStateful)
			}
			if p.BalanceURL != "" {
				fmt.Fprintf(&b, "balance_url = %q   # optional; wallet-balance endpoint shown in the status bar\n", p.BalanceURL)
			}
			if p.ContextWindow > 0 {
				fmt.Fprintf(&b, "context_window = %d   # tokens; compaction triggers near this limit\n", p.ContextWindow)
			}
			if p.MaxOutputTokens != 0 {
				fmt.Fprintf(&b, "max_output_tokens = %d   # total output cap; 0 = provider default, negative = omit when optional\n", p.MaxOutputTokens)
			}
			if p.Price != nil {
				fmt.Fprintf(&b, "price       = %s   # provider-wide fallback, per 1M tokens\n", renderPricingInline(p.Price))
			}
			if len(p.Prices) > 0 {
				fmt.Fprintf(&b, "prices      = %s   # per-model prices, per 1M tokens\n", renderPricingMap(p.Prices))
			}
			if p.Thinking != "" {
				fmt.Fprintf(&b, "thinking    = %q\n", p.Thinking)
			}
			if p.Effort != "" {
				fmt.Fprintf(&b, "effort      = %q\n", p.Effort)
			}
			if p.Vision {
				b.WriteString("vision      = true   # provider accepts image input for all listed models\n")
			}
			if p.VisionModels != nil {
				fmt.Fprintf(&b, "vision_models = %s   # models in this provider that accept image input\n", renderStringArray(p.VisionModels))
			}
			if p.VisionDetail != "" {
				fmt.Fprintf(&b, "vision_detail = %q   # openai image detail hint: low|high; empty = auto\n", p.VisionDetail)
			}
			if p.WebSearch {
				b.WriteString("web_search  = true   # enable server-side web_search tool (Anthropic/DeepSeek API)\n")
			}
			if p.ReasoningProtocol != "" {
				fmt.Fprintf(&b, "reasoning_protocol = %q   # auto|deepseek|glm|openai|none; overrides model/endpoint reasoning detection\n", p.ReasoningProtocol)
			}
			if len(p.SupportedEfforts) > 0 {
				fmt.Fprintf(&b, "supported_efforts = %s   # custom /effort levels exposed by this provider; overrides the built-in Kind/BaseURL default\n", renderStringArray(p.SupportedEfforts))
			}
			if p.DefaultEffort != "" {
				fmt.Fprintf(&b, "default_effort    = %q   # used when /effort is auto or unset; must be one of supported_efforts\n", p.DefaultEffort)
			}
			if len(p.ModelOverrides) > 0 {
				fmt.Fprintf(&b, "model_overrides   = %s   # per-model context/output/reasoning/vision overrides for mixed gateways\n", renderModelOverrides(p.ModelOverrides))
			}
			if p.NoProxy {
				b.WriteString("no_proxy    = true   # reach this base_url directly, never via the proxy\n")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("[tools]\n")
	if len(c.Tools.Enabled) == 0 {
		b.WriteString("enabled = []   # empty = all built-in tools\n")
	} else {
		b.WriteString("enabled = [")
		for i, t := range c.Tools.Enabled {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", t)
		}
		b.WriteString("]\n")
	}
	fmt.Fprintf(&b, "bash_timeout_seconds = %d   # foreground safety cap; set 0 for no tool-local cap\n", c.BashTimeoutSeconds())
	fmt.Fprintf(&b, "mcp_startup_timeout_seconds = %d   # background initialize + tools/list safety cap; per-plugin overrides may raise it\n", c.MCPStartupTimeoutSeconds())
	fmt.Fprintf(&b, "mcp_call_timeout_seconds = %d   # default MCP call safety cap; per-plugin/tool overrides may raise it\n\n", c.MCPCallTimeoutSeconds())

	b.WriteString("[tools.background_jobs]\n")
	fmt.Fprintf(&b, "stalled_warning_seconds = %d   # warn once per background job after this many quiet seconds; 0 disables\n\n", c.BackgroundJobStalledWarningSeconds())

	b.WriteString("[tools.shell]\n")
	if c.Tools.Shell.Prefer != "" {
		fmt.Fprintf(&b, "prefer = %q   # auto|bash|powershell|pwsh; empty/default = auto-detect\n", c.Tools.Shell.Prefer)
	} else {
		b.WriteString("# prefer = \"auto\"   # auto|bash|powershell|pwsh; empty/default = auto-detect\n")
	}
	if c.Tools.Shell.Path != "" {
		fmt.Fprintf(&b, "path   = %q   # absolute path to the shell executable; empty = PATH lookup\n\n", c.Tools.Shell.Path)
	} else {
		b.WriteString("# path   = \"/opt/homebrew/bin/bash\"   # absolute path to the shell executable; empty = PATH lookup\n\n")
	}

	renderLSPConfig(&b, c.LSP)

	b.WriteString("[skills]\n")
	if len(c.Skills.Paths) > 0 {
		fmt.Fprintf(&b, "paths = %s   # extra custom skill roots\n", renderStringArray(c.Skills.Paths))
	} else {
		b.WriteString("# paths = [\"~/my-skills\", \"../shared/skills\"]   # extra custom skill roots\n")
	}
	if len(c.Skills.ExcludedPaths) > 0 {
		fmt.Fprintf(&b, "excluded_paths = %s   # skill roots hidden from discovery\n", renderStringArray(c.Skills.ExcludedPaths))
	} else {
		b.WriteString("# excluded_paths = [\"~/.agents/skills\"]   # hide convention roots without deleting folders\n")
	}
	if c.Skills.MaxDepth != 0 {
		fmt.Fprintf(&b, "max_depth = %d   # nested scan depth; default 3, set 1 for legacy root-only discovery\n", c.SkillMaxDepth())
	} else {
		b.WriteString("# max_depth = 3   # nested scan depth; set 1 for legacy root-only discovery\n")
	}
	if disabled := c.DisabledSkillNames(); len(disabled) > 0 {
		fmt.Fprintf(&b, "disabled_skills = %s   # hidden from the prompt, slash invocation, and skill tools\n\n", renderStringArray(disabled))
	} else {
		b.WriteString("# disabled_skills = [\"review\"]   # hide noisy or unwanted skills\n\n")
	}

	b.WriteString("[permissions]\n")
	b.WriteString("# Per-call gating. mode = writer fallback when no rule matches: ask|allow|deny.\n")
	b.WriteString("# Readers always default to allow. Precedence: deny > ask > allow > fallback.\n")
	b.WriteString("# Rules are \"Tool\" or \"Tool(specifier)\"; e.g. Bash(go test:*), Edit(src/**).\n")
	mode := c.Permissions.Mode
	if mode == "" {
		mode = "ask"
	}
	fmt.Fprintf(&b, "mode  = %q\n", mode)
	if c.Permissions.AllowDynamicBash {
		b.WriteString("allow_dynamic_bash = true   # advanced: let mode=allow cover command substitution and interpreter -c/-e\n")
	} else {
		b.WriteString("# allow_dynamic_bash = false   # advanced opt-in; deny/ask and exact rules still take precedence\n")
	}
	b.WriteString(renderRuleList("deny", c.Permissions.Deny, `["Bash(rm -rf*)", "Bash(git push*)"]   # hard-blocked in every mode`))
	b.WriteString(renderRuleList("allow", c.Permissions.Allow, `["Bash(go test:*)", "Bash(git status:*)"]   # never prompted`))
	b.WriteString(renderRuleList("ask", c.Permissions.Ask, `["Edit(src/**)"]   # force a prompt even if otherwise allowed`))
	b.WriteString("\n")

	b.WriteString("[sandbox]\n")
	b.WriteString("# Confine tool blast radius. File-writers (write_file/edit_file/multi_edit/move_file)\n")
	b.WriteString("# may only write under workspace_root (empty = current dir) and allow_write extras.\n")
	b.WriteString("# bash = \"enforce\" jails each command in an OS sandbox when available;\n")
	b.WriteString("# without one, bash execution is refused. Empty defaults to enforce on macOS/Linux.\n")
	b.WriteString("# Windows has no OS-level Bash sandbox and fixes bash = \"off\".\n")
	b.WriteString("# network allows sandboxed bash egress.\n")
	if c.Sandbox.WorkspaceRoot != "" {
		fmt.Fprintf(&b, "workspace_root = %q\n", c.Sandbox.WorkspaceRoot)
	} else {
		b.WriteString("# workspace_root = \"\"            # default: current working directory\n")
	}
	if len(c.Sandbox.AllowWrite) > 0 {
		fmt.Fprintf(&b, "allow_write = %s\n", renderStringArray(c.Sandbox.AllowWrite))
	} else {
		b.WriteString("# allow_write = [\"/tmp\"]          # extra dirs writers may also modify\n")
	}
	if len(c.Sandbox.ForbidRead) > 0 {
		fmt.Fprintf(&b, "forbid_read = %s\n", renderStringArray(c.Sandbox.ForbidRead))
	} else {
		b.WriteString("# forbid_read = []                  # dirs the agent cannot read or list\n")
	}
	fmt.Fprintf(&b, "bash    = %q\n", c.BashMode())
	fmt.Fprintf(&b, "network = %v\n", c.Sandbox.Network)
	b.WriteString("\n")

	b.WriteString("[statusline]\n")
	b.WriteString("# A custom status line: a command whose first stdout line replaces the built-in\n")
	b.WriteString("# data row. It receives {\"model\",\"contextUsed\",\"contextWindow\",\"cwd\"} as JSON on stdin.\n")
	if c.Statusline.Command != "" {
		fmt.Fprintf(&b, "command = %q\n", c.Statusline.Command)
	} else {
		b.WriteString("# command = \"my-statusline.sh\"\n")
	}
	b.WriteString("\n")

	// [secrets] is user/global only: LoadForRoot discards project values, so
	// the project scope never renders it. Rendering it here is what lets a
	// user's saved toggles survive config rewrites (WriteFile re-renders the
	// whole file from the struct).
	if scope != RenderScopeProject {
		b.WriteString("[secrets]   # credential protection; user/global only, project config cannot override\n")
		if c.Secrets.FilterSubprocessEnv {
			b.WriteString("filter_subprocess_env = true   # strip credential-named env vars from tool/hook/LSP/MCP subprocesses\n")
		} else {
			b.WriteString("# filter_subprocess_env = false   # opt-in; stripping tokens breaks gh, HTTPS git push, npm publish\n")
		}
		if c.Secrets.ProtectSensitiveFiles {
			b.WriteString("protect_sensitive_files = true   # hide .env/.git-credentials/key files/~/.ssh from read tools\n")
		} else {
			b.WriteString("# protect_sensitive_files = false   # opt-in; hiding credential files can break legitimate edit workflows\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("# External MCP servers. type: \"stdio\" (default, a subprocess) | \"http\" | \"sse\".\n")
	b.WriteString("# ${VAR} / ${VAR:-default} are expanded from the environment in command/args/env/url/headers.\n")
	plugins := tomlPluginsForScope(c.Plugins, scope)
	if len(plugins) == 0 {
		b.WriteString("# [[plugins]]\n")
		b.WriteString("# name    = \"example\"\n")
		b.WriteString("# command = \"corvus-plugin-example\"\n")
		b.WriteString("# startup_timeout_seconds = 60    # optional initialize + tools/list cap\n")
		b.WriteString("# call_timeout_seconds = 600       # optional per-server MCP call timeout\n")
		b.WriteString("# tool_timeout_seconds = { \"generate_video\" = 1800 }   # raw MCP tool names\n")
		b.WriteString("# [[plugins]]                                  # a remote server over Streamable HTTP\n")
		b.WriteString("# name    = \"stripe\"\n")
		b.WriteString("# type    = \"http\"\n")
		b.WriteString("# url     = \"https://mcp.stripe.com\"\n")
		b.WriteString("# headers = { Authorization = \"Bearer ${STRIPE_KEY}\" }\n")
	} else {
		for _, pl := range plugins {
			b.WriteString("\n[[plugins]]\n")
			fmt.Fprintf(&b, "name    = %q\n", pl.Name)
			if pl.Type != "" {
				fmt.Fprintf(&b, "type    = %q\n", pl.Type)
			}
			if pl.Command != "" {
				fmt.Fprintf(&b, "command = %q\n", pl.Command)
			}
			if len(pl.Args) > 0 {
				fmt.Fprintf(&b, "args    = %s\n", renderStringArray(pl.Args))
			}
			if pl.URL != "" {
				fmt.Fprintf(&b, "url     = %q\n", pl.URL)
			}
			if len(pl.Headers) > 0 {
				fmt.Fprintf(&b, "headers = %s\n", renderStringMap(pl.Headers))
			}
			if len(pl.Env) > 0 {
				fmt.Fprintf(&b, "env     = %s\n", renderStringMap(pl.Env))
			}
			if pl.StartupTimeoutSeconds > 0 {
				b.WriteString("# Per-server MCP initialize + tools/list timeout; 0 keeps the global/default cap.\n")
				fmt.Fprintf(&b, "startup_timeout_seconds = %d\n", pl.StartupTimeoutSeconds)
			}
			if pl.CallTimeoutSeconds > 0 {
				b.WriteString("# Per-server MCP call timeout; 0 keeps the global/default cap.\n")
				fmt.Fprintf(&b, "call_timeout_seconds = %d\n", pl.CallTimeoutSeconds)
			}
			if hasPositiveIntMap(pl.ToolTimeoutSeconds) {
				b.WriteString("# Raw MCP tool names with per-tool call timeouts.\n")
				fmt.Fprintf(&b, "tool_timeout_seconds = %s\n", renderIntMap(pl.ToolTimeoutSeconds))
			}
			if pl.AutoStart != nil {
				fmt.Fprintf(&b, "auto_start = %v\n", *pl.AutoStart)
			}
		}
	}

	return b.String()
}

// tomlPluginsForScope keeps merged runtime entries in their owning config
// source. Unknown provenance is retained for callers that construct a Config
// directly before saving it to a specific target.
func tomlPluginsForScope(plugins []PluginEntry, scope RenderScope) []PluginEntry {
	if scope == RenderScopeFull {
		return plugins
	}
	out := make([]PluginEntry, 0, len(plugins))
	for _, pl := range plugins {
		switch pl.Source {
		case MCPSourceUnknown:
			out = append(out, pl)
		case MCPSourceUserConfig:
			if scope == RenderScopeUser {
				out = append(out, pl)
			}
		case MCPSourceProjectConfig:
			if scope == RenderScopeProject {
				out = append(out, pl)
			}
		}
	}
	return out
}

// RenderTOMLProjectDelta generates TOML containing only the sections and fields
// that differ from built-in defaults. Unlike RenderTOMLForScope (which renders
// the full config with comments), this emits clean TOML that can be surgically
// merged into an existing project config file via replaceTOMLSection.
func RenderTOMLProjectDelta(c *Config) string {
	if c == nil {
		return ""
	}
	d := Default()
	var b strings.Builder

	// Top-level scalar fields
	if v := configVersion(c); v != d.ConfigVersion {
		fmt.Fprintf(&b, "config_version = %d\n", v)
	}
	if c.DefaultModel != d.DefaultModel {
		fmt.Fprintf(&b, "default_model = %q\n", c.DefaultModel)
	}
	if c.Language != "" && c.Language != d.Language {
		fmt.Fprintf(&b, "language = %q\n", c.Language)
	}

	// [ui] section — whole-section comparison
	if !reflect.DeepEqual(c.UI, d.UI) {
		b.WriteString("[ui]\n")
		if c.UI.Theme != d.UI.Theme {
			fmt.Fprintf(&b, "theme = %q\n", c.UITheme())
		}
		if s := c.UIThemeStyle(); s != "" && s != d.UIThemeStyle() {
			fmt.Fprintf(&b, "theme_style = %q\n", s)
		}
		if l := c.UIShortcutLayout(); l != "classic" {
			fmt.Fprintf(&b, "shortcut_layout = %q\n", l)
		}
		if strings.TrimSpace(c.UI.CursorShape) != "" {
			fmt.Fprintf(&b, "cursor_shape = %q\n", c.UICursorShape())
		}
		if c.UI.ShowReasoning != d.UI.ShowReasoning {
			fmt.Fprintf(&b, "show_reasoning = %v\n", c.UI.ShowReasoning)
		}
		if c.UI.ProviderAccess != nil {
			fmt.Fprintf(&b, "provider_access = %s\n", renderStringArray(c.UI.ProviderAccess))
		}
		b.WriteString("\n")
	}

	// [network] section
	if !reflect.DeepEqual(c.Network, d.Network) {
		b.WriteString("[network]\n")
		if c.Network.ProxyMode != d.Network.ProxyMode {
			fmt.Fprintf(&b, "proxy_mode = %q\n", c.NetworkProxyMode())
		}
		if c.Network.ProxyURL != "" {
			fmt.Fprintf(&b, "proxy_url = %q\n", c.Network.ProxyURL)
		}
		if c.Network.NoProxy != "" {
			fmt.Fprintf(&b, "no_proxy = %q\n", c.Network.NoProxy)
		}
		if c.Network.Proxy.Type != "" || c.Network.Proxy.Server != "" || c.Network.Proxy.Port > 0 || c.Network.Proxy.Username != "" || c.Network.Proxy.Password != "" {
			b.WriteString("[network.proxy]\n")
			pt := c.Network.Proxy.Type
			if pt == "" {
				pt = "socks5"
			}
			fmt.Fprintf(&b, "type = %q\n", pt)
			if c.Network.Proxy.Server != "" {
				fmt.Fprintf(&b, "server = %q\n", c.Network.Proxy.Server)
			}
			if c.Network.Proxy.Port > 0 {
				fmt.Fprintf(&b, "port = %d\n", c.Network.Proxy.Port)
			}
			if c.Network.Proxy.Username != "" {
				fmt.Fprintf(&b, "username = %q\n", c.Network.Proxy.Username)
			}
			if c.Network.Proxy.Password != "" {
				fmt.Fprintf(&b, "password = %q\n", c.Network.Proxy.Password)
			}
		}
		b.WriteString("\n")
	}

	// [agent] section — per-field comparison
	var agentBuf strings.Builder
	anyAgent := false

	if sp := strings.TrimSpace(c.Agent.SystemPrompt); sp != "" && sp != d.Agent.SystemPrompt {
		agentBuf.WriteString("system_prompt = \"\"\"\n")
		agentBuf.WriteString(sp)
		agentBuf.WriteString("\"\"\"\n")
		anyAgent = true
	}
	if c.Agent.SystemPromptFile != "" && c.Agent.SystemPromptFile != d.Agent.SystemPromptFile {
		fmt.Fprintf(&agentBuf, "system_prompt_file = %q\n", c.Agent.SystemPromptFile)
		anyAgent = true
	}
	if c.Agent.Temperature != d.Agent.Temperature {
		fmt.Fprintf(&agentBuf, "temperature = %s\n", formatFloat(c.Agent.Temperature))
		anyAgent = true
	}
	if c.Agent.RecoveryModel != "" && c.Agent.RecoveryModel != d.Agent.RecoveryModel {
		fmt.Fprintf(&agentBuf, "recovery_model = %q\n", c.Agent.RecoveryModel)
		anyAgent = true
	}
	if c.Agent.ReasoningLanguage != d.Agent.ReasoningLanguage {
		if l := c.ReasoningLanguage(); l != "auto" {
			fmt.Fprintf(&agentBuf, "reasoning_language = %q\n", l)
			anyAgent = true
		}
	}
	if c.Agent.SoftCompactRatio != d.Agent.SoftCompactRatio {
		fmt.Fprintf(&agentBuf, "soft_compact_ratio = %s\n", formatFloat(c.Agent.SoftCompactRatio))
		anyAgent = true
	}
	if c.Agent.ToolResultSnipRatio != d.Agent.ToolResultSnipRatio {
		fmt.Fprintf(&agentBuf, "tool_result_snip_ratio = %s\n", formatFloat(c.Agent.ToolResultSnipRatio))
		anyAgent = true
	}
	if c.Agent.CompactRatio != d.Agent.CompactRatio {
		fmt.Fprintf(&agentBuf, "compact_ratio = %s\n", formatFloat(c.Agent.CompactRatio))
		anyAgent = true
	}
	if c.Agent.CompactForceRatio != d.Agent.CompactForceRatio {
		fmt.Fprintf(&agentBuf, "compact_force_ratio = %s\n", formatFloat(c.Agent.CompactForceRatio))
		anyAgent = true
	}
	if c.Agent.Keep != nil && !reflect.DeepEqual(c.Agent.Keep, d.Agent.Keep) {
		fmt.Fprintf(&agentBuf, "keep = %s\n", renderStringArray(c.Agent.Keep))
		anyAgent = true
	}
	if c.Agent.RecentKeep > 0 && c.Agent.RecentKeep != d.Agent.RecentKeep {
		fmt.Fprintf(&agentBuf, "recent_keep = %d\n", c.Agent.RecentKeep)
		anyAgent = true
	}
	if c.Agent.ColdResumePrune != d.Agent.ColdResumePrune {
		fmt.Fprintf(&agentBuf, "cold_resume_prune = %v\n", c.ColdResumePruneEnabled())
		anyAgent = true
	}
	if len(c.Agent.PlanModeReadOnlyCommands) > 0 && !reflect.DeepEqual(c.Agent.PlanModeReadOnlyCommands, d.Agent.PlanModeReadOnlyCommands) {
		fmt.Fprintf(&agentBuf, "plan_mode_read_only_commands = %s\n", renderStringArray(c.Agent.PlanModeReadOnlyCommands))
		anyAgent = true
	}
	if c.Agent.PlannerModel != "" && c.Agent.PlannerModel != d.Agent.PlannerModel {
		fmt.Fprintf(&agentBuf, "planner_model = %q\n", c.Agent.PlannerModel)
		anyAgent = true
	}
	if c.Agent.SubagentModel != "" && c.Agent.SubagentModel != d.Agent.SubagentModel {
		fmt.Fprintf(&agentBuf, "subagent_model = %q\n", c.Agent.SubagentModel)
		anyAgent = true
	}
	if len(c.Agent.SubagentModels) > 0 && !reflect.DeepEqual(c.Agent.SubagentModels, d.Agent.SubagentModels) {
		fmt.Fprintf(&agentBuf, "subagent_models = %s\n", renderStringMap(c.Agent.SubagentModels))
		anyAgent = true
	}
	if c.Agent.SubagentEffort != "" && c.Agent.SubagentEffort != d.Agent.SubagentEffort {
		fmt.Fprintf(&agentBuf, "subagent_effort = %q\n", c.Agent.SubagentEffort)
		anyAgent = true
	}
	if len(c.Agent.SubagentEfforts) > 0 && !reflect.DeepEqual(c.Agent.SubagentEfforts, d.Agent.SubagentEfforts) {
		fmt.Fprintf(&agentBuf, "subagent_efforts = %s\n", renderStringMap(c.Agent.SubagentEfforts))
		anyAgent = true
	}
	if c.Agent.MaxSubagentDepth != d.Agent.MaxSubagentDepth {
		fmt.Fprintf(&agentBuf, "max_subagent_depth = %d\n", c.Agent.MaxSubagentDepth)
		anyAgent = true
	}
	if c.Agent.OutputStyle != "" && c.Agent.OutputStyle != d.Agent.OutputStyle {
		fmt.Fprintf(&agentBuf, "output_style = %q\n", c.Agent.OutputStyle)
		anyAgent = true
	}
	if mode := strings.TrimSpace(c.Agent.PromptCacheKey); mode != "" && !strings.EqualFold(mode, "auto") && mode != d.Agent.PromptCacheKey {
		fmt.Fprintf(&agentBuf, "prompt_cache_key = %q\n", mode)
		anyAgent = true
	}
	if val := strings.TrimSpace(c.Agent.PromptCacheKeyValue); val != "" && val != d.Agent.PromptCacheKeyValue {
		fmt.Fprintf(&agentBuf, "prompt_cache_key_value = %q\n", val)
		anyAgent = true
	}

	if anyAgent {
		b.WriteString("[agent]\n")
		b.WriteString(agentBuf.String())
		b.WriteString("\n")
	}
	if shouldRenderRuntimePolicy(c) {
		b.WriteString("[runtime_policy]\n")
		writeRuntimePolicyFields(&b, c.RuntimePolicy, false)
		b.WriteString("\n")
	}

	// [[providers]] — include user-defined providers that aren't built-in
	proj := projectScopedConfigForRender(c)
	if proj != nil && len(proj.Providers) > 0 && !reflect.DeepEqual(proj.Providers, d.Providers) {
		for _, p := range proj.Providers {
			b.WriteString("[[providers]]\n")
			fmt.Fprintf(&b, "name        = %q\n", p.Name)
			fmt.Fprintf(&b, "kind        = %q\n", p.Kind)
			fmt.Fprintf(&b, "base_url    = %q\n", p.BaseURL)
			if p.ChatURL != "" {
				fmt.Fprintf(&b, "chat_url    = %q\n", p.ChatURL)
			}
			if len(p.Models) > 0 {
				fmt.Fprintf(&b, "models      = %s\n", renderStringArray(p.Models))
				if p.Default != "" {
					fmt.Fprintf(&b, "default     = %q\n", p.Default)
				}
			} else if p.Model != "" {
				fmt.Fprintf(&b, "model       = %q\n", p.Model)
			}
			if p.ModelsURL != "" {
				fmt.Fprintf(&b, "models_url  = %q\n", p.ModelsURL)
			}
			if p.APIKey != "" {
				fmt.Fprintf(&b, "api_key     = %q\n", p.APIKey)
			} else {
				fmt.Fprintf(&b, "api_key_env = %q\n", p.APIKeyEnv)
			}
			if p.PresetID != "" {
				fmt.Fprintf(&b, "preset_id   = %q\n", p.PresetID)
			}
			if p.PresetVersion > 0 {
				fmt.Fprintf(&b, "preset_version = %d\n", p.PresetVersion)
			}
			if len(p.Headers) > 0 {
				fmt.Fprintf(&b, "headers     = %s\n", renderStringMap(p.Headers))
			}
			if len(p.ExtraBody) > 0 {
				fmt.Fprintf(&b, "extra_body  = %s\n", renderAnyMap(p.ExtraBody))
			}
			if p.AuthHeader {
				b.WriteString("auth_header = true\n")
			}
			if p.ResponsesMode != "" {
				fmt.Fprintf(&b, "responses_mode = %q\n", p.ResponsesMode)
			}
			if p.ResponsesStateful != nil {
				fmt.Fprintf(&b, "responses_stateful = %t\n", *p.ResponsesStateful)
			}
			if p.BalanceURL != "" {
				fmt.Fprintf(&b, "balance_url = %q\n", p.BalanceURL)
			}
			if p.ContextWindow > 0 {
				fmt.Fprintf(&b, "context_window = %d\n", p.ContextWindow)
			}
			if p.MaxOutputTokens != 0 {
				fmt.Fprintf(&b, "max_output_tokens = %d\n", p.MaxOutputTokens)
			}
			if p.Price != nil {
				fmt.Fprintf(&b, "price       = %s\n", renderPricingInline(p.Price))
			}
			if len(p.Prices) > 0 {
				fmt.Fprintf(&b, "prices      = %s\n", renderPricingMap(p.Prices))
			}
			if p.Thinking != "" {
				fmt.Fprintf(&b, "thinking    = %q\n", p.Thinking)
			}
			if p.Effort != "" {
				fmt.Fprintf(&b, "effort      = %q\n", p.Effort)
			}
			if p.Vision {
				b.WriteString("vision      = true\n")
			}
			if p.VisionModels != nil {
				fmt.Fprintf(&b, "vision_models = %s\n", renderStringArray(p.VisionModels))
			}
			if p.VisionDetail != "" {
				fmt.Fprintf(&b, "vision_detail = %q\n", p.VisionDetail)
			}
			if p.WebSearch {
				b.WriteString("web_search  = true\n")
			}
			if p.ReasoningProtocol != "" {
				fmt.Fprintf(&b, "reasoning_protocol = %q\n", p.ReasoningProtocol)
			}
			if len(p.SupportedEfforts) > 0 {
				fmt.Fprintf(&b, "supported_efforts = %s\n", renderStringArray(p.SupportedEfforts))
			}
			if p.DefaultEffort != "" {
				fmt.Fprintf(&b, "default_effort    = %q\n", p.DefaultEffort)
			}
			if len(p.ModelOverrides) > 0 {
				fmt.Fprintf(&b, "model_overrides   = %s\n", renderModelOverrides(p.ModelOverrides))
			}
			if p.NoProxy {
				b.WriteString("no_proxy    = true\n")
			}
			b.WriteString("\n")
		}
	}

	// [tools]
	if len(c.Tools.Enabled) > 0 ||
		(c.Tools.BashTimeoutSeconds != nil && *c.Tools.BashTimeoutSeconds != 0) ||
		(c.Tools.MCPStartupTimeoutSeconds != nil && *c.Tools.MCPStartupTimeoutSeconds > 0) ||
		(c.Tools.MCPCallTimeoutSeconds != nil && *c.Tools.MCPCallTimeoutSeconds > 0) {
		b.WriteString("[tools]\n")
		if len(c.Tools.Enabled) > 0 {
			fmt.Fprintf(&b, "enabled = %s\n", renderStringArray(c.Tools.Enabled))
		}
		if c.Tools.BashTimeoutSeconds != nil && *c.Tools.BashTimeoutSeconds != 0 {
			fmt.Fprintf(&b, "bash_timeout_seconds = %d\n", *c.Tools.BashTimeoutSeconds)
		}
		if c.Tools.MCPStartupTimeoutSeconds != nil && *c.Tools.MCPStartupTimeoutSeconds > 0 {
			fmt.Fprintf(&b, "mcp_startup_timeout_seconds = %d\n", *c.Tools.MCPStartupTimeoutSeconds)
		}
		if c.Tools.MCPCallTimeoutSeconds != nil && *c.Tools.MCPCallTimeoutSeconds > 0 {
			fmt.Fprintf(&b, "mcp_call_timeout_seconds = %d\n", *c.Tools.MCPCallTimeoutSeconds)
		}
		b.WriteString("\n")
	}

	// [tools.background_jobs]
	if c.Tools.BackgroundJobs != d.Tools.BackgroundJobs {
		if c.Tools.BackgroundJobs.StalledWarningSeconds != nil && *c.Tools.BackgroundJobs.StalledWarningSeconds > 0 {
			b.WriteString("[tools.background_jobs]\n")
			fmt.Fprintf(&b, "stalled_warning_seconds = %d\n", *c.Tools.BackgroundJobs.StalledWarningSeconds)
			b.WriteString("\n")
		}
	}

	// [tools.shell]
	if !reflect.DeepEqual(c.Tools.Shell, d.Tools.Shell) {
		b.WriteString("[tools.shell]\n")
		if c.Tools.Shell.Prefer != d.Tools.Shell.Prefer {
			fmt.Fprintf(&b, "prefer = %q\n", c.Tools.Shell.Prefer)
		}
		if c.Tools.Shell.Path != d.Tools.Shell.Path {
			fmt.Fprintf(&b, "path = %q\n", c.Tools.Shell.Path)
		}
		b.WriteString("\n")
	}

	// [lsp]
	if !reflect.DeepEqual(c.LSP, d.LSP) {
		renderLSPConfig(&b, c.LSP)
	}

	// [skills]
	if !reflect.DeepEqual(c.Skills, d.Skills) {
		b.WriteString("[skills]\n")
		if len(c.Skills.Paths) > 0 {
			fmt.Fprintf(&b, "paths = %s\n", renderStringArray(c.Skills.Paths))
		}
		if len(c.Skills.ExcludedPaths) > 0 {
			fmt.Fprintf(&b, "excluded_paths = %s\n", renderStringArray(c.Skills.ExcludedPaths))
		}
		if c.Skills.MaxDepth != 0 {
			fmt.Fprintf(&b, "max_depth = %d\n", c.SkillMaxDepth())
		}
		if disabled := c.DisabledSkillNames(); len(disabled) > 0 {
			fmt.Fprintf(&b, "disabled_skills = %s\n\n", renderStringArray(disabled))
		}
	}

	// [permissions]
	if !reflect.DeepEqual(c.Permissions, d.Permissions) {
		b.WriteString("[permissions]\n")
		mode := c.Permissions.Mode
		if mode == "" {
			mode = "ask"
		}
		if mode != "ask" {
			fmt.Fprintf(&b, "mode = %q\n", mode)
		}
		if c.Permissions.AllowDynamicBash {
			b.WriteString("allow_dynamic_bash = true\n")
		}
		if len(c.Permissions.Deny) > 0 {
			fmt.Fprintf(&b, "deny = %s\n", renderStringArray(c.Permissions.Deny))
		}
		if len(c.Permissions.Allow) > 0 {
			fmt.Fprintf(&b, "allow = %s\n", renderStringArray(c.Permissions.Allow))
		}
		if len(c.Permissions.Ask) > 0 {
			fmt.Fprintf(&b, "ask = %s\n", renderStringArray(c.Permissions.Ask))
		}
		b.WriteString("\n")
	}

	// [sandbox]
	if !reflect.DeepEqual(c.Sandbox, d.Sandbox) {
		var sandboxBuf strings.Builder
		if c.Sandbox.WorkspaceRoot != "" {
			fmt.Fprintf(&sandboxBuf, "workspace_root = %q\n", c.Sandbox.WorkspaceRoot)
		}
		if len(c.Sandbox.AllowWrite) > 0 {
			fmt.Fprintf(&sandboxBuf, "allow_write = %s\n", renderStringArray(c.Sandbox.AllowWrite))
		}
		// Only persist a bash mode when its effective value differs from the
		// platform default. On Windows, even explicit "enforce" currently
		// resolves to "off", so project configs should not imply otherwise.
		if strings.TrimSpace(c.Sandbox.Bash) != "" && c.BashMode() != d.BashModeForGOOS(runtimeGOOS) {
			fmt.Fprintf(&sandboxBuf, "bash = %q\n", c.BashMode())
		}
		if c.Sandbox.Network != d.Sandbox.Network {
			fmt.Fprintf(&sandboxBuf, "network = %v\n", c.Sandbox.Network)
		}
		if sandboxBuf.Len() > 0 {
			b.WriteString("[sandbox]\n")
			b.WriteString(sandboxBuf.String())
			b.WriteString("\n")
		}
	}

	// [statusline]
	if !reflect.DeepEqual(c.Statusline, d.Statusline) {
		b.WriteString("[statusline]\n")
		if c.Statusline.Command != "" {
			fmt.Fprintf(&b, "command = %q\n", c.Statusline.Command)
		}
		b.WriteString("\n")
	}

	// [[plugins]] — always include when set; replaces all existing entries
	for _, pl := range tomlPluginsForScope(c.Plugins, RenderScopeProject) {
		b.WriteString("[[plugins]]\n")
		fmt.Fprintf(&b, "name    = %q\n", pl.Name)
		if pl.Type != "" {
			fmt.Fprintf(&b, "type    = %q\n", pl.Type)
		}
		if pl.Command != "" {
			fmt.Fprintf(&b, "command = %q\n", pl.Command)
		}
		if len(pl.Args) > 0 {
			fmt.Fprintf(&b, "args    = %s\n", renderStringArray(pl.Args))
		}
		if pl.URL != "" {
			fmt.Fprintf(&b, "url     = %q\n", pl.URL)
		}
		if len(pl.Headers) > 0 {
			fmt.Fprintf(&b, "headers = %s\n", renderStringMap(pl.Headers))
		}
		if len(pl.Env) > 0 {
			fmt.Fprintf(&b, "env     = %s\n", renderStringMap(pl.Env))
		}
		if pl.StartupTimeoutSeconds > 0 {
			fmt.Fprintf(&b, "startup_timeout_seconds = %d\n", pl.StartupTimeoutSeconds)
		}
		if pl.CallTimeoutSeconds > 0 {
			b.WriteString("# Per-server MCP call timeout; 0 keeps the global/default cap.\n")
			fmt.Fprintf(&b, "call_timeout_seconds = %d\n", pl.CallTimeoutSeconds)
		}
		if hasPositiveIntMap(pl.ToolTimeoutSeconds) {
			b.WriteString("# Raw MCP tool names with per-tool call timeouts.\n")
			fmt.Fprintf(&b, "tool_timeout_seconds = %s\n", renderIntMap(pl.ToolTimeoutSeconds))
		}
		if pl.AutoStart != nil {
			fmt.Fprintf(&b, "auto_start = %v\n", *pl.AutoStart)
		}
		b.WriteString("\n")
	}

	return b.String()
}

func renderPricingInline(p *provider.Pricing) string {
	if p == nil {
		return "{}"
	}
	return fmt.Sprintf("{ cache_hit = %v, input = %v, output = %v, currency = %q }",
		p.CacheHit, p.Input, p.Output, p.Symbol())
}

func renderPricingMap(prices map[string]*provider.Pricing) string {
	if len(prices) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(prices))
	for model := range prices {
		if strings.TrimSpace(model) != "" && prices[model] != nil {
			keys = append(keys, model)
		}
	}
	if len(keys) == 0 {
		return "{}"
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, model := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s = %s", strconv.Quote(model), renderPricingInline(prices[model]))
	}
	b.WriteString(" }")
	return b.String()
}

func configVersion(c *Config) int {
	if c != nil && c.ConfigVersion > 0 {
		return c.ConfigVersion
	}
	return Default().ConfigVersion
}

func shouldRenderUI(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	return !reflect.DeepEqual(c.UI, defaults.UI)
}

func shouldRenderNetwork(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	return !reflect.DeepEqual(c.Network, defaults.Network)
}

func shouldRenderNetworkPolicy(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	// A blank default is the default policy at runtime, so it must not make a
	// defaults-equal policy look project-customized after a reload.
	eq := func(p NetworkPolicyConfig) NetworkPolicyConfig {
		if strings.TrimSpace(p.Default) == "" {
			p.Default = "allow"
		}
		return p
	}
	return !reflect.DeepEqual(eq(c.NetworkPolicy), eq(defaults.NetworkPolicy))
}

func shouldRenderEnvironment(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	return !reflect.DeepEqual(c.Environment, defaults.Environment)
}

func renderEnvironmentConfig(b *strings.Builder, cfg EnvironmentConfig) {
	b.WriteString("[environment]\n")
	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}
	fmt.Fprintf(b, "enabled = %v   # inject a stable startup environment summary into the model prompt\n", enabled)
	if len(cfg.Tools) == 0 {
		b.WriteString("# [environment.tools]\n")
		b.WriteString("# go = \"/opt/homebrew/bin/go\"   # trusted executable path; workspace-local paths are not auto-executed\n\n")
		return
	}
	b.WriteString("\n[environment.tools]\n")
	names := make([]string, 0, len(cfg.Tools))
	for name := range cfg.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(b, "%s = %q\n", renderTOMLKeyPart(name), cfg.Tools[name])
	}
	b.WriteString("\n")
}

func shouldRenderProviders(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	return !reflect.DeepEqual(c.Providers, defaults.Providers)
}

func projectScopedConfigForRender(c *Config) *Config {
	if c == nil || len(c.providerSources) == 0 {
		return c
	}
	cp := *c
	cp.Providers = make([]ProviderEntry, 0, len(c.Providers)+len(c.shadowedProjectProviders))
	for _, p := range c.Providers {
		if c.providerSources[providerMergeKey(p)] == providerSourceUser {
			continue
		}
		cp.Providers = append(cp.Providers, p)
	}
	cp.Providers = append(cp.Providers, c.shadowedProjectProviders...)
	return &cp
}

func shouldRenderSystemPrompt(c, defaults *Config, scope RenderScope) bool {
	if scope == RenderScopeFull {
		return true
	}
	return strings.TrimSpace(c.Agent.SystemPrompt) != "" && c.Agent.SystemPrompt != defaults.Agent.SystemPrompt
}

func renderLSPConfig(b *strings.Builder, cfg LSPConfig) {
	b.WriteString("[lsp]\n")
	fmt.Fprintf(b, "enabled = %v   # language server tools; servers launch lazily when used\n", cfg.Enabled)
	if len(cfg.Servers) == 0 {
		b.WriteString("# [lsp.servers.go]\n")
		b.WriteString("# command = \"gopls\"\n")
		b.WriteString("# args = []\n")
		b.WriteString("# extensions = [\".go\"]\n\n")
		return
	}
	b.WriteString("\n")

	langs := make([]string, 0, len(cfg.Servers))
	for lang := range cfg.Servers {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		srv := cfg.Servers[lang]
		fmt.Fprintf(b, "[%s]\n", renderTOMLTablePath("lsp", "servers", lang))
		if srv.Command != "" {
			fmt.Fprintf(b, "command = %q\n", srv.Command)
		}
		if len(srv.Args) > 0 {
			fmt.Fprintf(b, "args = %s\n", renderStringArray(srv.Args))
		}
		if len(srv.Env) > 0 {
			fmt.Fprintf(b, "env = %s\n", renderStringMap(srv.Env))
		}
		if srv.LanguageID != "" {
			fmt.Fprintf(b, "language_id = %q\n", srv.LanguageID)
		}
		if len(srv.Extensions) > 0 {
			fmt.Fprintf(b, "extensions = %s\n", renderStringArray(srv.Extensions))
		}
		if srv.InstallHint != "" {
			fmt.Fprintf(b, "install_hint = %q\n", srv.InstallHint)
		}
		b.WriteString("\n")
	}
}

func renderTOMLKeyPart(key string) string {
	if isBareTOMLKey(key) {
		return key
	}
	return strconv.Quote(key)
}

func renderTOMLTablePath(parts ...string) string {
	rendered := make([]string, 0, len(parts))
	for _, part := range parts {
		rendered = append(rendered, renderTOMLKeyPart(part))
	}
	return strings.Join(rendered, ".")
}

func isBareTOMLKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// renderStringArray renders a []string as a TOML inline array.
func renderStringArray(ss []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range ss {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String()
}

// renderStringMap renders a map[string]string as a TOML inline table with keys
// in sorted order so output is deterministic (round-trips cleanly).
func renderStringMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s = %q", renderTOMLKeyPart(k), m[k])
	}
	b.WriteString(" }")
	return b.String()
}

func shouldRenderRuntimePolicy(c *Config) bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.RuntimePolicy.Guidance) != "" ||
		strings.TrimSpace(c.RuntimePolicy.Completion) != "" ||
		strings.TrimSpace(c.RuntimePolicy.Exposure) != ""
}

func writeRuntimePolicyFields(b *strings.Builder, cfg RuntimePolicyConfig, annotated bool) {
	if guidance := strings.TrimSpace(cfg.Guidance); guidance != "" {
		if annotated {
			fmt.Fprintf(b, "guidance   = %q   # inherit|auto|off|light|structured\n", guidance)
		} else {
			fmt.Fprintf(b, "guidance = %q\n", guidance)
		}
	}
	if completion := strings.TrimSpace(cfg.Completion); completion != "" {
		if annotated {
			fmt.Fprintf(b, "completion = %q   # inherit|auto|standard|verified\n", completion)
		} else {
			fmt.Fprintf(b, "completion = %q\n", completion)
		}
	}
	if exposure := strings.TrimSpace(cfg.Exposure); exposure != "" {
		if annotated {
			fmt.Fprintf(b, "exposure   = %q   # inherit|auto|eager|deferred\n", exposure)
		} else {
			fmt.Fprintf(b, "exposure = %q\n", exposure)
		}
	}
}

func renderAnyMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if _, ok := renderAnyValue(v); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		value, _ := renderAnyValue(m[k])
		fmt.Fprintf(&b, "%s = %s", strconv.Quote(k), value)
	}
	b.WriteString(" }")
	return b.String()
}

func renderAnyValue(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		return strconv.Quote(x), true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	case int:
		return strconv.Itoa(x), true
	case int8:
		return strconv.FormatInt(int64(x), 10), true
	case int16:
		return strconv.FormatInt(int64(x), 10), true
	case int32:
		return strconv.FormatInt(int64(x), 10), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case uint:
		return strconv.FormatUint(uint64(x), 10), true
	case uint8:
		return strconv.FormatUint(uint64(x), 10), true
	case uint16:
		return strconv.FormatUint(uint64(x), 10), true
	case uint32:
		return strconv.FormatUint(uint64(x), 10), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	case float32:
		return formatFloat(float64(x)), true
	case float64:
		return formatFloat(x), true
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			part, ok := renderAnyValue(item)
			if !ok {
				return "", false
			}
			parts = append(parts, part)
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	case []string:
		return renderStringArray(x), true
	case map[string]any:
		return renderAnyMap(x), true
	case map[string]string:
		return renderStringMap(x), true
	default:
		return "", false
	}
}

func renderModelOverrides(m map[string]ProviderModelOverride) string {
	keys := make([]string, 0, len(m))
	for k, ov := range m {
		if k == "" || modelOverrideEmpty(ov) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q = %s", k, renderModelOverride(m[k]))
	}
	b.WriteString(" }")
	return b.String()
}

func renderModelOverride(ov ProviderModelOverride) string {
	var parts []string
	if ov.ReasoningProtocol != "" {
		parts = append(parts, fmt.Sprintf("reasoning_protocol = %q", ov.ReasoningProtocol))
	}
	if len(ov.SupportedEfforts) > 0 {
		parts = append(parts, "supported_efforts = "+renderStringArray(ov.SupportedEfforts))
	}
	if ov.DefaultEffort != "" {
		parts = append(parts, fmt.Sprintf("default_effort = %q", ov.DefaultEffort))
	}
	if ov.Vision != nil {
		parts = append(parts, fmt.Sprintf("vision = %t", *ov.Vision))
	}
	if ov.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("context_window = %d", ov.ContextWindow))
	}
	if ov.MaxOutputTokens != 0 {
		parts = append(parts, fmt.Sprintf("max_output_tokens = %d", ov.MaxOutputTokens))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func modelOverrideEmpty(ov ProviderModelOverride) bool {
	return ov.ReasoningProtocol == "" && len(ov.SupportedEfforts) == 0 && ov.DefaultEffort == "" && ov.Vision == nil && ov.ContextWindow <= 0 && ov.MaxOutputTokens == 0
}

func hasPositiveIntMap(m map[string]int) bool {
	for k, v := range m {
		if strings.TrimSpace(k) != "" && v > 0 {
			return true
		}
	}
	return false
}

// renderIntMap renders a map[string]int as a TOML inline table with positive
// values only, preserving deterministic key order.
func renderIntMap(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if strings.TrimSpace(k) != "" && v > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q = %d", k, m[k])
	}
	b.WriteString(" }")
	return b.String()
}

// renderRuleList emits a permission rule list. A populated list renders as an
// active TOML array; an empty one renders as a commented example so `corvus setup`
// scaffolds discoverable guidance without imposing surprising rules.
func renderRuleList(key string, rules []string, example string) string {
	if len(rules) == 0 {
		return fmt.Sprintf("# %s = %s\n", key, example)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s = [", key)
	for i, r := range rules {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", r)
	}
	b.WriteString("]\n")
	return b.String()
}

// formatFloat ensures a float renders with a decimal point so TOML types it as a
// float, not an integer (e.g. 0 -> "0.0").
func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
