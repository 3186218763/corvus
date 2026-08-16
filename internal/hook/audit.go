package hook

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"corvus/internal/fileutil"
)

// The hook audit sidecar records every hook invocation as one JSONL record —
// a log-only trail (never model-visible, never rendered as a session event) so
// replay and post-hoc review can answer "which hooks ran, and what did they
// decide". It follows the ADR-0006 diagnostic tier: best-effort appends with
// the shared torn-tail guard, 0600, errors swallowed — audit loss can never
// change a hook verdict.
//
// Modeled on deepseek-harness's log-only hook/invoked session records.
type auditRecord struct {
	Time        string `json:"time"`
	SessionID   string `json:"session_id,omitempty"`
	Event       Event  `json:"event"`
	Scope       string `json:"scope,omitempty"`
	Command     string `json:"command,omitempty"`
	Decision    string `json:"decision"`
	ExitCode    int    `json:"exit_code,omitempty"`
	StdoutBytes int    `json:"stdout_bytes"`
	TimedOut    bool   `json:"timed_out,omitempty"`
}

// auditMu serialises appends across potentially concurrent hook events
// (foreground turns and background-job notifications).
var auditMu sync.Mutex

// SetAuditLog points the runner at a session hook-audit sidecar. Empty
// disables auditing. Nil-safe.
func (r *Runner) SetAuditLog(path string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.auditPath = path
	r.mu.Unlock()
}

// audit appends one record per outcome. Best-effort by design.
func (r *Runner) audit(rep Report) {
	if r == nil {
		return
	}
	r.mu.RLock()
	path := r.auditPath
	id := r.sessionID
	r.mu.RUnlock()
	if path == "" || len(rep.Outcomes) == 0 {
		return
	}
	auditMu.Lock()
	defer auditMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if err := fileutil.EnsureTrailingNewline(f); err != nil {
		return
	}
	enc := json.NewEncoder(f)
	for _, o := range rep.Outcomes {
		rec := auditRecord{
			Time:        time.Now().UTC().Format(time.RFC3339Nano),
			SessionID:   id,
			Event:       rep.Event,
			Scope:       string(o.Hook.Scope),
			Command:     clipRunes(o.Hook.Command, 60),
			Decision:    string(o.Decision),
			ExitCode:    o.ExitCode,
			StdoutBytes: len(o.Stdout),
			TimedOut:    o.TimedOut,
		}
		if err := enc.Encode(rec); err != nil {
			return
		}
	}
}
