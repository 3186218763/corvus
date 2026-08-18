package cli

// Stable reason ids for cacheInvalidationNotice. Kept as constants so call
// sites and tests share one spelling.
const (
	cacheInvalidationReasonModel         = "model"
	cacheInvalidationReasonTokenMode     = "token_mode"
	cacheInvalidationReasonRuntimePolicy = "runtime_policy"
	cacheInvalidationReasonTools         = "tools"
)

// cacheInvalidationNotice returns pre-action Notice copy when a user action
// may reset the provider prompt-cache prefix. Phase 1 is Notice-only (no
// confirm dialog). Unknown reasons return empty so callers can no-op safely.
//
// reason values:
//   - model: /model switch (and model picker)
//   - token_mode / work_mode: /work-mode or /profile switch
//   - tools: tool definitions / schema surface growth (when a single hook exists)
func cacheInvalidationNotice(reason string) string {
	switch reason {
	case cacheInvalidationReasonModel:
		return "Switching models may reset the provider prompt-cache prefix for this session."
	case cacheInvalidationReasonTokenMode, "work_mode", cacheInvalidationReasonRuntimePolicy:
		return "Switching work/token mode changes the tools surface and may reset the prompt-cache prefix."
	case cacheInvalidationReasonTools:
		return "Tool definitions changed; the prompt-cache tools prefix may miss on the next turn."
	default:
		return ""
	}
}

// noticeCacheInvalidation emits a dim Notice when reason maps to known copy.
func (m *chatTUI) noticeCacheInvalidation(reason string) {
	if m == nil {
		return
	}
	if note := cacheInvalidationNotice(reason); note != "" {
		m.notice(note)
	}
}
