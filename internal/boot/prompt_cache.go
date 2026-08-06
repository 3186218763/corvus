package boot

import (
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// promptCacheFields are the sticky prompt_cache_key Options fields shared by
// executor, planner, and sub-agent construction. Pure helper for unit tests
// without full boot.Build.
type promptCacheFields struct {
	PromptCacheKeyMode  string
	PromptCacheKeyValue string
	ProviderKind        string
	ProviderBaseURL     string
	SessionCacheID      string
	SubagentCacheID     string
}

// promptCacheOptions builds sticky-key policy fields from config + provider entry.
// sessionCacheID is the BranchID-derived session sticky id (main agent) or the
// parent BranchID (sub-agents). subRef is SubagentMeta.Ref; empty for the main
// agent and for ephemeral sub-agents without a transcript ref.
//
// Empty sessionCacheID leaves SessionCacheID empty so Resolve omits the key
// (headless runs / no stable parent).
func promptCacheOptions(cfg *config.Config, entry *config.ProviderEntry, sessionCacheID, subRef string) promptCacheFields {
	var f promptCacheFields
	if cfg != nil {
		f.PromptCacheKeyMode = cfg.Agent.PromptCacheKey
		f.PromptCacheKeyValue = cfg.Agent.PromptCacheKeyValue
	}
	if entry != nil {
		f.ProviderKind = entry.Kind
		f.ProviderBaseURL = entry.BaseURL
	}
	f.SessionCacheID = strings.TrimSpace(sessionCacheID)
	f.SubagentCacheID = strings.TrimSpace(subRef)
	return f
}

// apply copies sticky-key fields onto agent.Options.
func (f promptCacheFields) apply(opts *agent.Options) {
	if opts == nil {
		return
	}
	opts.PromptCacheKeyMode = f.PromptCacheKeyMode
	opts.PromptCacheKeyValue = f.PromptCacheKeyValue
	opts.ProviderKind = f.ProviderKind
	opts.ProviderBaseURL = f.ProviderBaseURL
	opts.SessionCacheID = f.SessionCacheID
	opts.SubagentCacheID = f.SubagentCacheID
}
