package cli

// Stable reason ids for CacheInvalidationNotice. Kept as constants so call
// sites and tests share one spelling.
const (
	CacheInvalidationReasonModel     = "model"
	CacheInvalidationReasonTokenMode = "token_mode"
	CacheInvalidationReasonTools     = "tools"
)

// CacheInvalidationNotice returns pre-action Notice copy when a user action
// may reset the provider prompt-cache prefix. Phase 1 is Notice-only (no
// confirm dialog). Unknown reasons return empty so callers can no-op safely.
//
// reason values:
//   - model: /model switch (and model picker)
//   - token_mode / work_mode: /work-mode or /profile switch
//   - tools: tool definitions / schema surface growth (when a single hook exists)
func CacheInvalidationNotice(reason string) string {
	switch reason {
	case CacheInvalidationReasonModel:
		return "Switching models may reset the provider prompt-cache prefix for this session."
	case CacheInvalidationReasonTokenMode, "work_mode":
		return "Switching work/token mode changes the tools surface and may reset the prompt-cache prefix."
	case CacheInvalidationReasonTools:
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
	if note := CacheInvalidationNotice(reason); note != "" {
		m.notice(note)
	}
}
