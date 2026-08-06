// Command reasonix is a config- and plugin-driven coding agent CLI.
package main

import (
	"os"

	"reasonix/internal/cli"

	// Blank imports wire compile-time built-ins into their registries.
	_ "reasonix/internal/provider/anthropic"
	_ "reasonix/internal/provider/openai"
	_ "reasonix/internal/provider/responses"
	_ "reasonix/internal/tool/builtin"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

var runCLI = cli.Run

func main() {
	os.Exit(runCLI(os.Args[1:], version))
}
