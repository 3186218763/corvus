package boot

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"corvus/internal/agent"
	"corvus/internal/capability"
	"corvus/internal/config"
	"corvus/internal/event"
	"corvus/internal/mcplaunch"
	"corvus/internal/netclient"
	"corvus/internal/plugin"
	"corvus/internal/sandbox"
	"corvus/internal/skill"
	"corvus/internal/tool"
)

type pluginResult struct {
	pluginSpecOptions PluginSpecOptions
	enabledMCPNames   map[string]bool
	onDemandMCPSpecs  map[string]plugin.Spec
	onDemandMCPNames  []string
}

// buildMCPPlugins resolves enabled MCP entries into specs, applies launch
// isolation and timeouts, connects host-session extra plugins, registers
// catalog placeholders, and emits demotion notices.
func buildMCPPlugins(ctx context.Context, opts Options, sink event.Sink, cfg *config.Config, root string, reg *tool.Registry, pluginHost *plugin.Host, writeRoots []string, networkEnabled bool, forbidReadRoots []string, tokenEconomy bool) (*pluginResult, error) {
	// Enabled MCP servers enter the tool catalog at boot. Cached schemas
	// register placeholders without starting processes; cache-miss servers get
	// a single background catalog discovery. First real tool call uses
	// EnsureConnected so parent/child/tab runtimes share one process.
	pluginSpecOptions := PluginSpecOptions{
		DefaultStartupTimeout: time.Duration(cfg.MCPStartupTimeoutSeconds()) * time.Second,
		DefaultCallTimeout:    time.Duration(cfg.MCPCallTimeoutSeconds()) * time.Second,
		LaunchManager:         mcplaunch.ForWorkspace(config.CorvusHomeDir(), root),
		ConfigSource:          "workspace_config",
		StateHome:             config.CorvusHomeDir(),
		WriterRoots:           writeRoots,
		ForbidReadRoots:       forbidReadRoots,
		Network:               networkEnabled,
		Proxy:                 cfg.NetworkProxySpec(),
		PackageOwners:         pluginPackageOwners(cfg),
	}
	autoStartEntries := cfg.EnabledPlugins(root, config.DefaultMCPActivationStore())
	enabledMCPNames := make(map[string]bool, len(autoStartEntries))
	for _, enabled := range autoStartEntries {
		if name := strings.TrimSpace(enabled.Name); name != "" {
			enabledMCPNames[name] = true
		}
	}
	// Legacy eager/background tiers are still parsed for config compatibility
	// but no longer change process start timing. Keep the partition only so
	// demotion notices remain meaningful for chronically slow eager configs.
	eagerEntries, bgEntries := partitionByTier(autoStartEntries)
	extraSpecs := applyDefaultMCPStartupTimeout(
		applyDefaultMCPCallTimeout(
			applyKnownPluginOverrides(opts.ExtraPlugins, root),
			pluginSpecOptions.DefaultCallTimeout,
		),
		pluginSpecOptions.DefaultStartupTimeout,
	)
	for i := range extraSpecs {
		if strings.TrimSpace(extraSpecs[i].WorkspaceRoot) == "" {
			extraSpecs[i].WorkspaceRoot = root
		}
		if extraSpecs[i].LaunchManager == nil {
			extraSpecs[i].LaunchManager = pluginSpecOptions.LaunchManager
		}
		if strings.TrimSpace(extraSpecs[i].ConfigSource) == "" {
			extraSpecs[i].ConfigSource = "host_session"
		}
		if !extraSpecs[i].RequireLaunchApproval {
			// Session-scoped MCP specs arrive through an explicit host/user action
			// (for example ACP session/new), so they follow installed-server
			// authorization without another per-tool or per-session prompt.
			extraSpecs[i].Authorized = true
		}
		applyMCPIsolation(&extraSpecs[i], root, pluginSpecOptions)
	}
	onDemandMCPSpecs := map[string]plugin.Spec{}
	onDemandMCPNames := []string{}
	if tokenEconomy {
		for _, spec := range append(PluginSpecsForRootWithOptions(autoStartEntries, root, pluginSpecOptions), extraSpecs...) {
			name := strings.TrimSpace(spec.Name)
			if name == "" {
				continue
			}
			if _, exists := onDemandMCPSpecs[name]; !exists {
				onDemandMCPNames = append(onDemandMCPNames, name)
			}
			onDemandMCPSpecs[name] = spec
		}
		eagerEntries, bgEntries = nil, nil
	}
	// Auto-demote: any eager plugin that has been chronically slow (recent
	// samples repeatedly hit the blocking startup budget) drops to background
	// for this session. The user keeps eager intent, just doesn't pay for it
	// on a server that's been misbehaving. A notice surfaces the demotion.
	var demoteMessages []string
	budget := plugin.DefaultStartupBudget()
	kept := eagerEntries[:0]
	for _, e := range eagerEntries {
		rec := plugin.Recommend(e.Name, budget, 0)
		if rec.Demote {
			demoteMessages = append(demoteMessages, rec.Reason)
			bgEntries = append(bgEntries, e)
			continue
		}
		kept = append(kept, e)
	}
	eagerEntries = kept

	eagerSpecs := PluginSpecsForRootWithOptions(eagerEntries, root, pluginSpecOptions)
	bgSpecs := PluginSpecsForRootWithOptions(bgEntries, root, pluginSpecOptions)

	if !tokenEconomy {
		eagerSpecs = append(eagerSpecs, extraSpecs...)
	}

	// Apply caller-supplied stderr override to every spec across tiers.
	if opts.Stderr != nil {
		for i := range eagerSpecs {
			eagerSpecs[i].Stderr = opts.Stderr
		}
		for i := range bgSpecs {
			bgSpecs[i].Stderr = opts.Stderr
		}
	}

	// Host-session ExtraPlugins (for example ACP session servers) are explicit
	// for this controller and still take a short readiness probe so recovery and
	// session-scoped servers are deterministic. User/project config MCP stays
	// catalog-first and process-idle until first real tool call.
	if len(extraSpecs) > 0 && !tokenEconomy {
		for _, s := range extraSpecs {
			if pluginHost.HasClient(s.Name) {
				if tools, err := pluginHost.ToolsFor(ctx, s.Name); err == nil {
					for _, t := range tools {
						reg.Add(t)
					}
					continue
				}
			}
			// Project-scoped MCP servers (repo config / .mcp.json) must not
			// start or connect until the user authorizes the exact identity;
			// a denial records a RequiresLaunchApproval failure and skips the
			// connection entirely.
			resolved, ok := resolveEagerMCPLaunchApproval(ctx, pluginHost, opts.MCPLaunchApprover, s)
			if !ok {
				continue
			}
			s = resolved
			addCtx, addCancel := context.WithTimeout(ctx, 5*time.Second)
			tools, err := pluginHost.EnsureConnectedWithLifecycle(ctx, addCtx, s, 0)
			addCancel()
			if err != nil {
				if plugin.IsServerAlreadyConnected(err) {
					if tools, err2 := pluginHost.ToolsFor(ctx, s.Name); err2 == nil {
						for _, t := range tools {
							reg.Add(t)
						}
						continue
					}
				}
				// Leave a catalog entry for diagnostics; failures surface in /mcp.
				cs, _ := plugin.LoadCachedSchemaForSpec(s)
				for _, t := range plugin.LazyToolset(s, cs, pluginHost, reg, ctx, false) {
					reg.Add(t)
				}
				sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
					Text: "An MCP server failed to start.", Detail: fmt.Sprintf("mcp %s: %v", s.Name, err)})
				continue
			}
			for _, t := range tools {
				reg.Add(t)
			}
		}
	}

	// Configured enabled MCP: cache-hit placeholders without starting processes;
	// cache-miss servers get one background catalog discovery.
	registerEnabledMCP := func(specs []plugin.Spec) {
		for _, s := range specs {
			if pluginHost.HasClient(s.Name) {
				tools, err := pluginHost.ToolsFor(ctx, s.Name)
				if err == nil {
					for _, t := range tools {
						reg.Add(t)
					}
					continue
				}
			}
			cs, _ := plugin.LoadCachedSchemaForSpec(s)
			// Only kick a process for catalog discovery when no usable schema is
			// cached. Cache-hit sessions stay process-idle until first tool call.
			kick := cs == nil || len(cs.Tools) == 0
			for _, t := range plugin.LazyToolset(s, cs, pluginHost, reg, ctx, kick) {
				reg.Add(t)
			}
		}
	}
	// eagerSpecs already includes extraSpecs when !tokenEconomy; avoid double
	// registration of host-session servers that connected above.
	configSpecs := append(append([]plugin.Spec{}, eagerSpecs...), bgSpecs...)
	if len(extraSpecs) > 0 && !tokenEconomy {
		extraNames := map[string]bool{}
		for _, s := range extraSpecs {
			extraNames[s.Name] = true
		}
		filtered := configSpecs[:0]
		for _, s := range configSpecs {
			if extraNames[s.Name] {
				continue
			}
			filtered = append(filtered, s)
		}
		configSpecs = filtered
	}
	registerEnabledMCP(configSpecs)

	for _, msg := range demoteMessages {
		sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: msg})
	}

	return &pluginResult{
		pluginSpecOptions: pluginSpecOptions,
		enabledMCPNames:   enabledMCPNames,
		onDemandMCPSpecs:  onDemandMCPSpecs,
		onDemandMCPNames:  onDemandMCPNames,
	}, nil
}

type capabilityResult struct {
	capSpecs         []plugin.Spec
	capLedger        *capability.Ledger
	capAudit         *capability.Audit
	capRuntime       *agent.MCPCapabilityRuntime
	dualModelPlanner bool
}

// buildCapabilityRuntime loads the MCP capability specs and cache, builds the
// session-shared MCP runtime and the Delivery/dual-model capability frontends,
// and installs the skill invocation dependency checker.
func buildCapabilityRuntime(ctx context.Context, cfg *config.Config, opts Options, root string, reg *tool.Registry, skillStore *skill.Store, pluginHost *plugin.Host, pluginSpecOptions PluginSpecOptions, enabledMCPNames map[string]bool, runtimeProfile capability.Profile, tokenEconomy, tokenDelivery bool, entry *config.ProviderEntry, capRuntimeGet func() *agent.MCPCapabilityRuntime, capRuntimeSet func(*agent.MCPCapabilityRuntime)) (*capabilityResult, error) {
	// Session-shared MCP runtime: Host, specs, and connection snapshots. Each
	// agent gets its own use_capability frontend (ledger/audit isolation) while
	// reusing processes. Delivery puts a frontend on the executor registry;
	// dual-model Planner and all task/fleet sub-agents get their own frontends
	// without inheriting dynamic mcp__* schemas.
	var capLedger *capability.Ledger
	var capAudit *capability.Audit
	capSpecs := PluginSpecsForRootWithOptions(cfg.Plugins, root, pluginSpecOptions)
	cachedTools, cacheKeyOK := capability.LoadCachedToolsForSpecs(capSpecs)
	skillStore.ConfigureToolBindings(func(sk skill.Skill) []tool.MCPBinding {
		return skillMCPBindings(sk, reg, capSpecs, cachedTools, cacheKeyOK)
	})
	// Detect dual-model planner early so Balanced can attach the same stable
	// use_capability surface to both Planner and Executor. Their frontends keep
	// independent ledgers/audits while sharing the session MCP runtime.
	dualModelPlanner := false
	if pm := effectivePlannerModel(cfg, opts, tokenEconomy); pm != "" {
		if pe, ok := resolveOptionalEntry(opts, cfg, pm); ok && pe.Model != entry.Model {
			dualModelPlanner = true
		}
	}
	profile := capability.ProfileBalanced
	if tokenDelivery {
		profile = capability.ProfileDelivery
	} else if tokenEconomy {
		profile = capability.ProfileEconomy
	}
	var capProxy *agent.UseCapabilityTool
	// Catalog closes over capRuntime so proxy-connected tools stay routable.
	catalogFn := func() capability.Catalog {
		conn := map[string]bool{}
		failedNow := map[string]string{}
		if pluginHost != nil {
			for _, n := range pluginHost.ServerNames() {
				conn[n] = true
			}
			for _, failure := range pluginHost.Failures() {
				failedNow[failure.Name] = failure.Error
			}
		}
		catOpts := capability.CatalogOptions{
			Tools:       reg.ContractEntries(),
			Skills:      skillStore.List(),
			Plugins:     cfg.Plugins,
			Profile:     profile,
			Connected:   conn,
			Failed:      failedNow,
			CachedTools: cachedTools,
			CacheKeyOK:  cacheKeyOK,
		}
		if rt := capRuntimeGet(); rt != nil {
			catOpts.Plugins, catOpts.CachedTools, catOpts.CacheKeyOK, catOpts.Disabled, catOpts.ProxyTools = rt.CapabilityCatalogState()
		}
		return capability.BuildCatalog(catOpts)
	}
	// Always build the runtime when a plugin host exists so task/fleet children
	// can use the stable proxy even in Balanced/Economy without Delivery.
	if pluginHost != nil || len(capSpecs) > 0 || tokenDelivery || dualModelPlanner {
		capRuntimeSet(agent.NewMCPCapabilityRuntime(ctx, pluginHost, capSpecs, reg, catalogFn))
		capRuntimeGet().ConfigureServers(cfg.Plugins, capSpecs, enabledMCPNames)
	}
	if tokenDelivery || dualModelPlanner {
		capLedger = capability.NewLedger()
		capAudit = &capability.Audit{}
		if rt := capRuntimeGet(); rt != nil {
			capProxy = rt.NewFrontend(capLedger, capAudit)
			reg.Add(capProxy)
		}
	}
	skillStore.ConfigureInvocationPolicy(string(runtimeProfile), func(requires []string) []string {
		connected := map[string]bool{}
		failedNow := map[string]string{}
		if pluginHost != nil {
			for _, name := range pluginHost.ServerNames() {
				connected[name] = true
			}
			for _, failure := range pluginHost.Failures() {
				failedNow[failure.Name] = failure.Error
			}
		}
		catOpts := capability.CatalogOptions{
			Tools:       reg.ContractEntries(),
			Skills:      skillStore.List(),
			Plugins:     cfg.Plugins,
			Profile:     runtimeProfile,
			Connected:   connected,
			Failed:      failedNow,
			CachedTools: cachedTools,
			CacheKeyOK:  cacheKeyOK,
		}
		if rt := capRuntimeGet(); rt != nil {
			catOpts.Plugins, catOpts.CachedTools, catOpts.CacheKeyOK, catOpts.Disabled, catOpts.ProxyTools = rt.CapabilityCatalogState()
		}
		catalog := capability.BuildCatalog(catOpts)
		_, missing := catalog.RequiresReady(requires)
		return missing
	})

	return &capabilityResult{
		capSpecs:         capSpecs,
		capLedger:        capLedger,
		capAudit:         capAudit,
		capRuntime:       capRuntimeGet(),
		dualModelPlanner: dualModelPlanner,
	}, nil
}

// defaultMCPLaunchApprover is the fail-closed approval fallback used when
// Options.MCPLaunchApprover is nil. Build runs before any interactive frontend
// takes over the terminal (stdin is still in cooked mode), so a one-line
// prompt on os.Stdout and a single y/N answer on os.Stdin is safe. Non-TTY
// stdin (headless runs, CI, servers) denies without starting anything.
func defaultMCPLaunchApprover(ctx context.Context, spec plugin.Spec) (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, nil
	}
	fmt.Fprintf(os.Stdout, "\nProject MCP server %q wants to start:\n  %s\nAuthorize? [y/N] ", spec.Name, mcpLaunchSummary(spec))
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// mcpLaunchSummary renders one line describing what approving the server would
// start or connect to: the transport plus command/args (stdio) or URL
// (http/sse).

// mcpLaunchSummary renders one line describing what approving the server would
// start or connect to: the transport plus command/args (stdio) or URL
// (http/sse).
func mcpLaunchSummary(spec plugin.Spec) string {
	transport := strings.ToLower(strings.TrimSpace(spec.Type))
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "http", "sse":
		return fmt.Sprintf("%s %s", transport, spec.URL)
	default:
		if len(spec.Args) == 0 {
			return fmt.Sprintf("stdio %s", spec.Command)
		}
		return fmt.Sprintf("stdio %s %s", spec.Command, strings.Join(spec.Args, " "))
	}
}

// resolveEagerMCPLaunchApproval gates one eagerly connected MCP spec behind the
// project launch-approval contract. A spec that does not require approval (or
// already carries a durable exact-identity grant) passes through untouched.
// Otherwise the approver (injected via Options.MCPLaunchApprover, or the
// fail-closed default) decides: approval is persisted with
// plugin.AuthorizeProjectSpecLaunch and the re-resolved authorized spec is
// returned; a denial or error records a RequiresLaunchApproval failure on the
// host and returns ok=false so the caller never starts the process or opens a
// network connection.

// resolveEagerMCPLaunchApproval gates one eagerly connected MCP spec behind the
// project launch-approval contract. A spec that does not require approval (or
// already carries a durable exact-identity grant) passes through untouched.
// Otherwise the approver (injected via Options.MCPLaunchApprover, or the
// fail-closed default) decides: approval is persisted with
// plugin.AuthorizeProjectSpecLaunch and the re-resolved authorized spec is
// returned; a denial or error records a RequiresLaunchApproval failure on the
// host and returns ok=false so the caller never starts the process or opens a
// network connection.
func resolveEagerMCPLaunchApproval(ctx context.Context, host *plugin.Host, approver func(context.Context, plugin.Spec) (bool, error), s plugin.Spec) (plugin.Spec, bool) {
	if !s.RequireLaunchApproval {
		return s, true
	}
	resolved := plugin.ResolveStoredAuthorization(ctx, s)
	if resolved.ServerAuthorized() {
		return resolved, true
	}
	if approver == nil {
		approver = defaultMCPLaunchApprover
	}
	approved, err := approver(ctx, resolved)
	if err != nil {
		host.RecordFailure(resolved, err)
		return resolved, false
	}
	if !approved {
		host.RecordFailure(resolved, plugin.NewLaunchApprovalError(resolved.Name, false))
		return resolved, false
	}
	if err := plugin.AuthorizeProjectSpecLaunch(ctx, resolved); err != nil {
		host.RecordFailure(resolved, err)
		return resolved, false
	}
	// Re-resolve so the freshly recorded grant marks the spec Authorized before
	// the caller connects; start() re-verifies the exact identity anyway.
	resolved = plugin.ResolveStoredAuthorization(ctx, resolved)
	if !resolved.ServerAuthorized() {
		host.RecordFailure(resolved, plugin.NewLaunchApprovalError(resolved.Name, false))
		return resolved, false
	}
	return resolved, true
}

// Build loads config, resolves the model(s), and returns a Controller wrapping a
// single Agent, or a two-model Coordinator when agent.planner_model is set. The
// returned controller owns plugin subprocesses; call Close (via Controller.Close)
// to release them.

// partitionByTier splits configured plugin entries into eager (block boot until
// ready) and background (placeholder + start spawn now). Entries with an empty,
// legacy lazy, or unrecognised tier land in background.
func partitionByTier(entries []config.PluginEntry) (eager, bg []config.PluginEntry) {
	for _, e := range entries {
		switch e.ResolvedTier() {
		case "eager":
			eager = append(eager, e)
		default:
			bg = append(bg, e)
		}
	}
	return eager, bg
}

// PluginSpecsForRoot maps configured plugin entries to plugin.Spec and applies
// workspace-aware compatibility overrides for known cwd-sensitive servers.

// PluginSpecsForRoot maps configured plugin entries to plugin.Spec and applies
// workspace-aware compatibility overrides for known cwd-sensitive servers.
func PluginSpecsForRoot(entries []config.PluginEntry, workspaceRoot string) []plugin.Spec {
	return PluginSpecsForRootWithOptions(entries, workspaceRoot, PluginSpecOptions{})
}

// PluginSpecOptions carries runtime policy that is not stored on each plugin
// entry but still needs to reach plugin.Spec.

// PluginSpecOptions carries runtime policy that is not stored on each plugin
// entry but still needs to reach plugin.Spec.
type PluginSpecOptions struct {
	DefaultStartupTimeout time.Duration
	DefaultCallTimeout    time.Duration
	LaunchManager         *mcplaunch.Manager
	ConfigSource          string
	StateHome             string
	WriterRoots           []string
	ForbidReadRoots       []string
	Network               bool
	PackageOwners         map[string]string
	// Proxy routes remote HTTP/SSE MCP server connections through the user's
	// proxy settings (ADR-0004).
	Proxy netclient.ProxySpec
}

// PluginSpecsForRootWithOptions maps configured plugin entries to plugin.Spec
// and injects runtime policy such as the global MCP call timeout.

// PluginSpecsForRootWithOptions maps configured plugin entries to plugin.Spec
// and injects runtime policy such as the global MCP call timeout.
func PluginSpecsForRootWithOptions(entries []config.PluginEntry, workspaceRoot string, opts PluginSpecOptions) []plugin.Spec {
	specs := make([]plugin.Spec, len(entries))
	for i, e := range entries {
		specs[i] = pluginSpecFromEntryWithOptions(e, workspaceRoot, opts)
	}
	return specs
}

func pluginSpecFromEntryWithOptions(e config.PluginEntry, workspaceRoot string, opts PluginSpecOptions) plugin.Spec {
	e = e.ExpandedPlugin() // resolve ${VAR} / ${VAR:-default} from the environment
	configSource := strings.TrimSpace(string(e.Source))
	if configSource == "" {
		configSource = opts.ConfigSource
	}
	// Repository-declared MCP servers (project config / project .mcp.json) are
	// untrusted project code: they must not start or connect until the user
	// explicitly authorizes the exact command/endpoint (persisted via
	// plugin.AuthorizeProjectSpecLaunch). User-level config and installed
	// plugin packages are the user's own explicit installs and stay authorized.
	projectScoped := e.Source.ProjectScoped()
	spec := plugin.ApplyKnownOverrides(plugin.Spec{
		Name:                  e.Name,
		Package:               strings.TrimSpace(opts.PackageOwners[e.Name]),
		Type:                  e.Type,
		Command:               e.Command,
		Args:                  e.Args,
		Env:                   e.Env,
		URL:                   e.URL,
		Headers:               e.Headers,
		Proxy:                 opts.Proxy,
		DefaultStartupTimeout: opts.DefaultStartupTimeout,
		StartupTimeout:        secondsDuration(e.StartupTimeoutSeconds),
		DefaultCallTimeout:    opts.DefaultCallTimeout,
		CallTimeout:           secondsDuration(e.CallTimeoutSeconds),
		ToolTimeouts:          toolTimeoutDurations(e.ToolTimeoutSeconds),
		WorkspaceRoot:         strings.TrimSpace(workspaceRoot),
		LaunchManager:         opts.LaunchManager,
		ConfigSource:          configSource,
		Authorized:            e.Source.UserAuthorized() && !projectScoped,
		RequireLaunchApproval: projectScoped,
	}, workspaceRoot)
	if projectScoped && strings.TrimSpace(spec.Dir) == "" {
		spec.Dir = workspaceRoot
	}
	applyMCPIsolation(&spec, workspaceRoot, opts)
	return spec
}

func pluginPackageOwners(cfg *config.Config) map[string]string {
	out := map[string]string{}
	if cfg == nil {
		return out
	}
	for _, configured := range cfg.Plugins {
		if owner, ok := cfg.PluginPackageOwner(configured.Name); ok {
			out[configured.Name] = owner
		}
	}
	return out
}

func skillMCPBindings(sk skill.Skill, reg *tool.Registry, specs []plugin.Spec, cachedTools map[string][]plugin.CachedTool, cacheKeyOK map[string]bool) []tool.MCPBinding {
	var out []tool.MCPBinding
	liveServers := map[string]bool{}
	if reg != nil {
		bindings := reg.MCPBindings()
		out = make([]tool.MCPBinding, 0, len(bindings))
		for _, binding := range bindings {
			liveServers[binding.Server] = true
			if binding.Package == sk.Plugin {
				out = append(out, binding)
			}
		}
	}
	// A valid cached schema also supplies stable bindings for an on-demand
	// package server before it is connected. The skill can then route through
	// use_capability without inventing Corvus's canonical name.
	for _, spec := range specs {
		if spec.Package != sk.Plugin || liveServers[spec.Name] || !cacheKeyOK[spec.Name] {
			continue
		}
		for _, cached := range cachedTools[spec.Name] {
			visible := cached.Name
			if spec.StripRawPrefix != "" {
				visible = strings.TrimPrefix(visible, spec.StripRawPrefix)
			}
			out = append(out, tool.MCPBinding{
				Package:      spec.Package,
				Server:       spec.Name,
				RawName:      cached.Name,
				VisibleName:  visible,
				CallableName: plugin.ModelToolName(spec.Name, visible),
				CapabilityID: "mcp-tool:" + spec.Name + "/" + cached.Name,
			})
		}
	}
	return out
}

func applyMCPIsolation(spec *plugin.Spec, workspaceRoot string, opts PluginSpecOptions) {
	if spec == nil {
		return
	}
	// Authorized user MCP defaults to trusted host process mode. Confined mode
	// is opt-in for internal managed deployments/tests and is never selected by
	// ordinary install paths.
	if spec.ProcessMode == "" {
		spec.ProcessMode = plugin.MCPProcessHost
	}
	if strings.TrimSpace(opts.StateHome) == "" {
		return
	}
	stateDir := plugin.MCPStateDir(opts.StateHome, workspaceRoot, spec.Name)
	spec.StateDir = stateDir
	if spec.ResolvedProcessMode() != plugin.MCPProcessConfined {
		// Host mode still gets a private state/cache/temp tree; only the OS
		// command sandbox is omitted so local app integrations keep working.
		return
	}
	writerRoots := appendUniquePaths([]string{stateDir}, opts.WriterRoots...)
	readerRoots := []string{workspaceRoot}
	if home, err := os.UserHomeDir(); err == nil {
		readerRoots = appendUniquePaths(readerRoots, home)
	}
	spec.Sandbox = sandbox.Spec{
		Mode: "enforce", WriteRoots: writerRoots,
		ReadRoots:              readerRoots,
		AppContainerWriteRoots: append([]string(nil), writerRoots...),
		ForbidReadRoots:        append([]string(nil), opts.ForbidReadRoots...),
		Network:                opts.Network, MinimalWrites: true,
	}
}

func secondsDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func toolTimeoutDurations(seconds map[string]int) map[string]time.Duration {
	if len(seconds) == 0 {
		return nil
	}
	out := make(map[string]time.Duration, len(seconds))
	for name, sec := range seconds {
		name = strings.TrimSpace(name)
		if name == "" || sec <= 0 {
			continue
		}
		out[name] = time.Duration(sec) * time.Second
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func applyKnownPluginOverrides(specs []plugin.Spec, workspaceRoot string) []plugin.Spec {
	out := make([]plugin.Spec, len(specs))
	for i, spec := range specs {
		out[i] = plugin.ApplyKnownOverrides(spec, workspaceRoot)
	}
	return out
}

func applyDefaultMCPCallTimeout(specs []plugin.Spec, timeout time.Duration) []plugin.Spec {
	if len(specs) == 0 || timeout <= 0 {
		return specs
	}
	out := make([]plugin.Spec, len(specs))
	for i, spec := range specs {
		out[i] = spec
		if out[i].DefaultCallTimeout <= 0 {
			out[i].DefaultCallTimeout = timeout
		}
	}
	return out
}

func applyDefaultMCPStartupTimeout(specs []plugin.Spec, timeout time.Duration) []plugin.Spec {
	if len(specs) == 0 || timeout <= 0 {
		return specs
	}
	out := make([]plugin.Spec, len(specs))
	for i, spec := range specs {
		out[i] = spec
		if out[i].DefaultStartupTimeout <= 0 {
			out[i].DefaultStartupTimeout = timeout
		}
	}
	return out
}

// autoShellPrefer reports whether [tools.shell] left the interpreter to
// auto-detection, so the "fell back to PowerShell" hint is suppressed once the
// user has explicitly chosen a shell.

// autoShellPrefer reports whether [tools.shell] left the interpreter to
// auto-detection, so the "fell back to PowerShell" hint is suppressed once the
// user has explicitly chosen a shell.
func autoShellPrefer(prefer string) bool {
	p := strings.ToLower(strings.TrimSpace(prefer))
	return p == "" || p == "auto"
}

// LSPSpecs returns the language → server map: the built-in defaults overlaid with
// any user overrides. A user entry may set only the fields it wants to change;
// empty fields keep the default for that language.
