package recovery

// Fixed product defaults for Auto recovery budgets. These are not configurable
// via UI, CLI, or config files — they define the host-owned Auto safety envelope.
const (
	// MaxOperationFailures is how many times one exact operation may fail inside
	// one Episode before that operation alone is stopped.
	MaxOperationFailures = 3
	// MaxEpisodeFailures is how many qualifying execution failures one Task may
	// accumulate inside one Episode since the last real progress before the
	// Episode stops further mutation/verification. Parameter, command, or target
	// changes do not reset it; host-proven read-only diagnosis remains available.
	MaxEpisodeFailures = 6
	// MaxReviewRejects is how many cumulative reviewer rejections one Task may
	// accumulate inside one Episode before the turn stops. Different candidates
	// share this budget.
	MaxReviewRejects = 3
	// MaxStoppedOperationRetries is how many times an already-stopped operation
	// may be re-proposed before the turn escalates to a hard Episode stop.
	MaxStoppedOperationRetries = 3
)

// StopReason identifies why an Episode-level stop was raised. Values are
// internal; user-facing copy never exposes them.
type StopReason string

const (
	StopReasonNone              StopReason = ""
	StopReasonEpisodeFailures   StopReason = "episode_failures"
	StopReasonReviewRejects     StopReason = "review_rejects"
	StopReasonStoppedOpRetries  StopReason = "stopped_op_retries"
	StopReasonOperationFailures StopReason = "operation_failures"
)
