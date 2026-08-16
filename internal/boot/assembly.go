package boot

import (
	"context"

	"corvus/internal/event"
)

// assembly is the stage bus Build threads through the late builders
// (buildSkillTools, buildToolSourceConnector, buildExecutorAndPlanner,
// buildController). Each stage result embeds wholesale, so its fields promote
// to a.<field> — the builders read exactly the identifiers they used to
// receive positionally, without the transposition-prone 25-45 argument lists
// that threading them required (a bool landing in the wrong slot compiled
// fine). Stages populate their result once; later builders read earlier
// results, never their own.
type assembly struct {
	ctx  context.Context
	opts Options
	sink event.Sink

	*configResult
	*toolResult
	*jobsResult
	*promptResult
	*pluginResult
	*lspResult
	*hookResult
	*subagentResult
	*sessionMemoryResult
	*skillToolsResult
	*capabilityResult
	*runnerResult
}
