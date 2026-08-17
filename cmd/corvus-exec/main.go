// Command corvus-exec is a headless, one-shot execution frontend for Corvus —
// the codex-exec analogue. The implementation lives in internal/headless so
// the main corvus binary can expose the same behavior through --headless.
package main

import (
	"os"

	"corvus/internal/headless"

	// Blank imports wire compile-time built-ins into their registries.
	_ "corvus/internal/provider/anthropic"
	_ "corvus/internal/provider/openai"
	_ "corvus/internal/provider/responses"
	_ "corvus/internal/tool/builtin"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(headless.Run(os.Args[1:], version))
}
