package boot

import (
	"fmt"
	"strings"

	"corvus/internal/config"
	"corvus/internal/event"
	"corvus/internal/stats"
)

// buildSinkAndNotices wraps the frontend sink (usage recorder) and emits the
// one-time boot notices: config migration outcomes, ignored legacy settings,
// and the missing-API-key warning.
func buildSinkAndNotices(opts Options, cfg *config.Config, entry *config.ProviderEntry, modelName string, migs legacyMigrations) (event.Sink, error) {
	// Serialize the frontend's sink once: background jobs (below) emit from their
	// own goroutines, which can overlap a running turn's emission, so every emitter
	// shares this synchronized sink. The job manager is session-scoped — its jobs
	// outlive a turn and are cancelled by Controller.Close.
	sink := event.Sync(opts.Sink)

	// Record billable usage for the "usage statistics" panel. Wrapping here —
	// outside the per-agent sinks — covers every agent (executor, planner,
	// sub-agents, guardian) with one recorder, and each record is labelled with
	// this frontend's StatsSource so the panel can split totals by entry point.
	if source := strings.TrimSpace(opts.StatsSource); source != "" {
		sink = stats.NewRecorder(sink, config.StatsDir(), source)
	}

	if migs.stepLimits.changed || cfg.IgnoredLegacyAgentStepLimits() {
		level := event.LevelInfo
		text := "Deprecated agent step limits were removed."
		detail := "[agent].max_steps and planner_max_steps are no longer used; Corvus now manages interactive progress automatically. " +
			"Use the CLI --max-steps flag for a one-off run or [bot].max_steps for unattended bot sessions."
		if migs.stepLimits.err != nil {
			level = event.LevelWarn
			text = "Deprecated agent step limits were ignored."
			detail += " The old keys were ignored but could not be removed: " + migs.stepLimits.err.Error()
		}
		sink.Emit(event.Event{
			Kind:   event.Notice,
			Level:  level,
			Text:   text,
			Detail: detail,
		})
	} else if migs.stepLimits.err != nil {
		sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "Deprecated agent step-limit migration did not complete.", Detail: migs.stepLimits.err.Error()})
	}
	if migs.redactToolOutput.changed || migs.redactToolOutput.err != nil {
		level := event.LevelInfo
		text := "Deprecated redact_tool_output setting was removed."
		detail := "[secrets].redact_tool_output no longer has any effect: ordinary model/tool content and local session/job artifacts now preserve their original text. Explicit diagnostics and corvus doctor redact-sessions still redact credential values."
		if migs.redactToolOutput.err != nil {
			level = event.LevelWarn
			text = "Deprecated redact_tool_output setting was ignored."
			detail += " The old key could not be removed: " + migs.redactToolOutput.err.Error()
		}
		sink.Emit(event.Event{Kind: event.Notice, Level: level, Text: text, Detail: detail})
	}
	if migs.memoryCompiler.changed || migs.memoryCompiler.err != nil {
		level := event.LevelInfo
		text := "Deprecated memory_compiler setting was removed."
		detail := "The Memory v5 execution compiler has been removed from Corvus: [agent].memory_compiler no longer has any effect, user turns are never replaced by compiled execution contracts, and no compiler state is written. Old transcripts containing compiled turns still display normally."
		if migs.memoryCompiler.err != nil {
			level = event.LevelWarn
			text = "Deprecated memory_compiler setting was ignored."
			detail += " The old key could not be removed: " + migs.memoryCompiler.err.Error()
		}
		sink.Emit(event.Event{Kind: event.Notice, Level: level, Text: text, Detail: detail})
	}
	if ignored := cfg.IgnoredProjectDefaultModel(); ignored != "" {
		sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "Ignored the project config's default_model.", Detail: fmt.Sprintf("project .corvus/config.toml sets default_model = %q but no configured provider serves it; using %q from your user config instead. Edit or remove that default_model line to silence this notice.", ignored, cfg.DefaultModel)})
	}

	// A resolvable model whose API key env is unset would otherwise build fine
	// (RequireKey is false so the UI stays reachable) and then fail silently on the
	// first request, showing as an empty/dead model. Surface the cause up front.
	if !opts.RequireKey && entry.RequiresAPIKey() && entry.EffectiveAPIKey() == "" {
		sink.Emit(event.Event{Kind: event.Notice, Text: "Selected model is missing its API key.", Detail: fmt.Sprintf("model %q is selected but its API key is not set — add api_key to its [[providers]] entry in .corvus/config.toml (user config first), or set env %s for the legacy api_key_env path", modelName, entry.APIKeyEnv)})
	}

	return sink, nil
}
