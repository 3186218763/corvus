// Command corvus is a config- and plugin-driven coding agent CLI.
package main

import (
	"os"

	"corvus/internal/cli"

	// Blank imports wire compile-time built-ins into their registries.
	_ "corvus/internal/provider/anthropic"
	_ "corvus/internal/provider/openai"
	_ "corvus/internal/provider/responses"
	_ "corvus/internal/tool/builtin"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

var runCLI = cli.Run

func main() {
	os.Exit(runCLI(os.Args[1:], version))
}
