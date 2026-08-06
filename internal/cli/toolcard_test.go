package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestToolCard(t *testing.T) {
	cases := []struct {
		name string
		args string
		want []string
		deny []string
	}{
		{"bash", `{"command":"npm test"}`, []string{"Bash", "npm test"}, nil},
		{"read_file", `{"path":"pkg/a.go"}`, []string{"Read", "pkg/a.go"}, nil},
		{"grep", `{"pattern":"TODO","path":"."}`, []string{"Search", "TODO"}, nil},
		{"wait", `{"job_ids":["bash-1","bash-2"],"timeout_seconds":300}`, []string{"Wait", "bash-1", "bash-2"}, []string{"timeout_seconds", "300", "job_ids"}},
		{"web_fetch", `{"url":"https://x.dev"}`, []string{"Fetch", "https://x.dev"}, nil},
		{"use_capability", `{"action":"call","capability_id":"mcp-tool:github/search_issues","arguments":{"query":"bug"}}`, []string{"MCP", "mcp-tool:github/search_issues"}, []string{`"arguments"`, `"query"`, "bug"}},
		{"use_capability", `{"action":"list"}`, []string{"MCP", "list"}, []string{"action"}},
	}
	for _, c := range cases {
		got := toolCard(c.name, c.args, 120)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q missing %q", c.name, got, w)
			}
		}
		for _, d := range c.deny {
			if strings.Contains(got, d) {
				t.Errorf("%s: %q should not contain raw arg %q", c.name, got, d)
			}
		}
	}
}

func TestToolCardUnknownFallsBackToName(t *testing.T) {
	if got := toolCard("frobnicate", `{}`, 80); !strings.Contains(got, "frobnicate") {
		t.Errorf("unknown tool should show its raw name, got %q", got)
	}

	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	head := toolHead("frobnicate", "{}", 80)
	if !strings.Contains(head, fgSGR(activeCLITheme.accent)) {
		t.Fatalf("unknown tool verb should carry accent SGR, got %q", head)
	}
}

func TestToolCardVerbAndArgColors(t *testing.T) {
	defer restoreThemeForTest(activeColorProfile, activeCLITheme)
	activeColorProfile = colorprofile.ANSI256
	configureCLITheme("dark")

	read := toolHead("grep", "TODO", 120)
	if !strings.Contains(read, fgSGR(activeCLITheme.toolRead)) {
		t.Fatalf("read verb should carry toolRead SGR, got %q", read)
	}
	if !strings.Contains(read, ansiBold) {
		t.Fatalf("read verb should stay bold, got %q", read)
	}
	if !strings.Contains(read, fgSGR(activeCLITheme.toolArg)) {
		t.Fatalf("read arg should carry toolArg SGR, got %q", read)
	}
	exec := toolHead("bash", "npm test", 120)
	if !strings.Contains(exec, fgSGR(activeCLITheme.warn)) {
		t.Fatalf("bash verb should carry warn SGR, got %q", exec)
	}
	write := toolHead("write_file", "a.go", 120)
	if !strings.Contains(write, fgSGR(activeCLITheme.success)) {
		t.Fatalf("write verb should carry success SGR, got %q", write)
	}
	dot := toolDot("wait")
	if !strings.Contains(dot, fgSGR(activeCLITheme.toolProc)) {
		t.Fatalf("wait dot should stay toolProc-colored, got %q", dot)
	}
}

func TestToolArgPairwiseDistinct(t *testing.T) {
	names := []string{"faint", "muted", "toolRead", "success", "warn", "toolProc", "accent"}
	for _, mode := range []string{"dark", "light"} {
		defer restoreThemeForTest(activeColorProfile, activeCLITheme)
		activeColorProfile = colorprofile.ANSI256
		configureCLITheme(mode)

		colors := map[string]cliColor{
			"toolArg":  activeCLITheme.toolArg,
			"faint":    activeCLITheme.faint,
			"muted":    activeCLITheme.muted,
			"toolRead": activeCLITheme.toolRead,
			"success":  activeCLITheme.success,
			"warn":     activeCLITheme.warn,
			"toolProc": activeCLITheme.toolProc,
			"accent":   activeCLITheme.accent,
		}
		toolArgSGR := fgSGR(colors["toolArg"])
		for _, name := range names {
			if fgSGR(colors[name]) == toolArgSGR {
				t.Fatalf("%s theme: toolArg SGR %q collides with %s", mode, toolArgSGR, name)
			}
		}
	}
}
