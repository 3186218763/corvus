package boot

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"corvus/internal/agent"
	"corvus/internal/config"
	"corvus/internal/control"
	"corvus/internal/event"
	"corvus/internal/jobs"
	"corvus/internal/netclient"
	"corvus/internal/provider"
	"corvus/internal/sandbox"
	"corvus/internal/workspacelease"
)

type jobsResult struct {
	workspaceLease *workspacelease.Owner
	jm             *jobs.Manager
	sessionDir     string
	proxySpec      netclient.ProxySpec
	balanceClient  *http.Client
	execProv       provider.Provider
	shell          sandbox.Shell
}

// buildJobsAndProviders wires the session-scoped job manager, the Delivery
// workspace lease, the network client, the executor provider, and the shell.
func buildJobsAndProviders(opts Options, sink event.Sink, cfg *config.Config, root string, stderr io.Writer, modelRef string, tokenDelivery bool) (res *jobsResult, err error) {
	var workspaceLease *workspacelease.Owner
	jobOptions := []jobs.Option{
		jobs.WithStalledWarningAfter(time.Duration(cfg.BackgroundJobStalledWarningSeconds()) * time.Second),
		jobs.WithSessionOwnershipProbe(agent.SessionLeaseHeldByCurrentRuntime),
	}
	if tokenDelivery {
		workspaceLease, err = workspacelease.New(root, config.WorkspaceLeaseDir(), func() {
			sink.Emit(event.Event{
				Kind:   event.Notice,
				Level:  event.LevelInfo,
				Code:   event.NoticeCodeWorkspaceLease,
				Text:   "Another Delivery session is writing to this workspace; this session will continue automatically when it is safe.",
				Detail: "workspace write lease is busy; read-only work remains concurrent",
			})
		})
		if err != nil {
			return nil, fmt.Errorf("initialize Delivery workspace lease: %w", err)
		}
		jobOptions = append(jobOptions, jobs.WithJobStartObserver(workspaceLease.RetainUntil))
	}
	jm := jobs.NewManager(sink, jobOptions...)
	sessionDir := opts.SessionDir
	if sessionDir == "" {
		sessionDir = config.SessionDir()
	}
	reconcileCleanupPending := opts.CleanupPendingReconciler
	if reconcileCleanupPending == nil {
		reconcileCleanupPending = control.ReconcileCleanupPending
	}
	if err := reconcileCleanupPending(sessionDir); err != nil {
		sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "cleanup-pending reconciliation failed: " + err.Error()})
	}

	proxySpec := cfg.NetworkProxySpec()
	if err := netclient.Validate(proxySpec); err != nil {
		return nil, err
	}
	// The 12s cap is billing's bounded-query contract (internal/billing): a
	// slow endpoint must not hang the status line (ADR-0004).
	balanceClient, err := netclient.NewHTTPClient(proxySpec, netclient.TransportOptions{Timeout: 12 * time.Second})
	if err != nil {
		return nil, err
	}

	execProv, err := resolveProvider(opts, cfg, proxySpec, provider.Selection{Ref: modelRef, Effort: opts.EffortOverride})
	if err != nil {
		return nil, err
	}
	shell := sandbox.ResolveShell(cfg.Tools.Shell.Prefer, cfg.Tools.Shell.Path, stderr)

	return &jobsResult{
		workspaceLease: workspaceLease,
		jm:             jm,
		sessionDir:     sessionDir,
		proxySpec:      proxySpec,
		balanceClient:  balanceClient,
		execProv:       execProv,
		shell:          shell,
	}, nil
}

// NewProvider builds a provider.Provider from a configured entry. Exported so
// custom assemblers (e.g. the ACP per-session factory) can reuse it without
// going through the full Build.
func NewProvider(e *config.ProviderEntry) (provider.Provider, error) {
	return NewProviderWithProxy(e, netclient.ProxySpec{Mode: netclient.ModeAuto})
}

// NewCompactionProviderWithProxy builds a provider client for one-off summary
// requests. It is a separate client from the executor, and Responses entries
// are forced into stateless mode so a summary can never replace the executor's
// previous_response_id continuation state.
func NewCompactionProviderWithProxy(e *config.ProviderEntry, proxy netclient.ProxySpec, suppressServerWebSearch ...bool) (provider.Provider, error) {
	if e == nil {
		return nil, fmt.Errorf("compaction provider entry is nil")
	}
	copy := *e
	if strings.EqualFold(strings.TrimSpace(copy.Kind), "responses") || strings.EqualFold(strings.TrimSpace(copy.Kind), "dashscope-responses") {
		copy.ResponsesMode = "stateless"
		stateful := false
		copy.ResponsesStateful = &stateful
	}
	return NewProviderWithProxy(&copy, proxy, suppressServerWebSearch...)
}

// NewProviderWithProxy builds a provider.Provider with the configured ordinary
// network proxy settings.

// serverWebSearchSuppressed reports whether a local [web_search] engine is
// active and therefore the provider-side server web_search tool must not be
// emitted (the model would otherwise see two "web_search" tools).
func serverWebSearchSuppressed(flag []bool) bool {
	return len(flag) > 0 && flag[0]
}

// NewProviderWithProxy builds a provider.Provider with the configured ordinary
// network proxy settings.
func NewProviderWithProxy(e *config.ProviderEntry, proxy netclient.ProxySpec, suppressServerWebSearch ...bool) (provider.Provider, error) {
	return provider.New(e.Kind, provider.Config{
		Name:    e.Name,
		BaseURL: e.BaseURL,
		Model:   e.Model,
		APIKey:  e.EffectiveAPIKey(),
		// Pass the key's env var so auth failures can name where to fix it, plus
		// provider-kind-specific knobs. EffectiveEffort applies a configured
		// default_effort when the user has not explicitly selected /effort.
		Extra: map[string]any{
			"api_key_env":           e.APIKeyEnv,
			"api_key_source":        e.APIKeySourceLabel(),
			"thinking":              e.Thinking,
			"effort":                config.EffectiveEffort(e),
			"supported_efforts":     e.SupportedEfforts,
			"reasoning_protocol":    config.ReasoningProtocolForEntry(e),
			"max_output_tokens":     e.MaxOutputTokens,
			"chat_url":              e.ChatURL,
			"headers":               e.Headers,
			"extra_body":            e.ExtraBody,
			"auth_header":           e.AuthHeader,
			"proxy_spec":            proxy,
			"vision":                config.EffectiveVision(e),
			"vision_model_explicit": config.ExplicitModelVision(e),
			"vision_detail":         e.VisionDetail,
			"web_search":            e.WebSearch && !serverWebSearchSuppressed(suppressServerWebSearch),
			"mode":                  e.ResponsesMode,
			// Keep nil as nil so the responses provider can vendor-detect its
			// default instead of accidentally treating every endpoint as stateful.
			"stateful": e.ResponsesStateful,
		},
	})
}

// addBuiltins adds enabled built-in tools to reg. An empty list means all of
// them. writeRoots confines the file-writing built-ins to the workspace: after
// the (unconfined) defaults are added, each enabled writer is replaced by an
// instance bound to writeRoots (preserving registry order).
// forbidReadRoots confines the read/list/search built-ins so they cannot peek at
// the listed directories.
// When workDir is non-empty, tools resolve relative paths against it instead of
// the process cwd, enabling concurrent multi-project sessions.
// sessionGuard blocks writer-tool targets inside Corvus's own session stores
// and makes bash warn when a command references them. managedConfig names the
// Corvus-owned config files writable outside writeRoots after a fresh
// per-write human approval.

func providerNames(cfg *config.Config) string {
	names := make([]string, len(cfg.Providers))
	for i, p := range cfg.Providers {
		names[i] = p.Name
	}
	return strings.Join(names, "/")
}
