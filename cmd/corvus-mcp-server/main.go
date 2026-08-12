// Command corvus-mcp-server exposes Corvus's built-in tools to MCP hosts
// (IDEs, editors, and other Model Context Protocol clients) over the stdio
// transport: newline-delimited JSON-RPC 2.0, one message per line. An IDE
// spawns this binary and speaks the 2024-11-05 protocol to it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"corvus/internal/config"
	"corvus/internal/i18n"
	"corvus/internal/mcpserver"
	"corvus/internal/netclient"
	"corvus/internal/permission"
	"corvus/internal/sandbox"
	"corvus/internal/tool"
	"corvus/internal/tool/builtin"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "corvus-mcp-server:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("corvus-mcp-server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", "", "workspace root the tools operate on (default: current directory)")
	allowWrite := fs.Bool("allow-write", false,
		"register the writer tools (write_file, edit_file, multi_edit, move_file, notebook_edit, delete_range, delete_symbol, bash); every call still goes through the permission policy")
	permissionMode := fs.String("permission-mode", "dontAsk",
		"policy fallback for calls not covered by a rule: dontAsk (default, fail closed), ask (headless: same as dontAsk), auto, or yolo")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Printf("%s %s\n", mcpserver.ServerName, mcpserver.ServerVersion)
		return nil
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	root, err := resolveRoot(*dir)
	if err != nil {
		return err
	}

	// Project config is read only (no legacy migrations on disk). A broken
	// config fails startup loudly, matching the rest of Corvus.
	cfg, err := config.LoadForRootReadOnly(root)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	i18n.DetectLanguage(cfg.Language)

	tools, err := buildTools(cfg, root, *allowWrite)
	if err != nil {
		return err
	}
	policyMode, err := mapPermissionMode(*permissionMode)
	if err != nil {
		return err
	}
	policy := permission.New(policyMode, cfg.Permissions.Allow, cfg.Permissions.Ask, cfg.Permissions.Deny)

	// The process runs until stdin closes (the MCP client owns the lifetime).
	return mcpserver.New(tools, policy).Serve(context.Background(), os.Stdin, os.Stdout)
}

func resolveRoot(dir string) (string, error) {
	root := strings.TrimSpace(dir)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
		root = cwd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve --dir: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("--dir %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--dir %s: not a directory", abs)
	}
	return abs, nil
}

// buildTools assembles the served tool set. The default set is read-only
// (fail closed): read/list/search tools bound to the workspace root plus the
// SSRF-guarded web_fetch with the configured proxy and the [network_policy]
// egress rules. With allowWrite the writer tools and a workspace-confined
// bash are added, but every call still passes through policy.Decide (Ask and
// Deny decisions refuse the call).
func buildTools(cfg *config.Config, root string, allowWrite bool) ([]tool.Tool, error) {
	proxySpec := cfg.NetworkProxySpec()
	writeRoots := cfg.WriteRootsForRoot(root)
	guard := builtin.NewSessionDataGuard(config.MemoryUserDir(), cfg.AllowWriteRoots())
	search := builtin.ResolveSearch(cfg.Tools.Search.Engine, cfg.Tools.Search.RgPath, os.Stderr)
	// The egress policy gates web_fetch, bash URL arguments, and web_search on
	// this surface too — an MCP host speaking to this server is exactly the
	// outbound channel the deny rules exist for.
	netPolicy, err := cfg.NetPolicy()
	if err != nil {
		return nil, fmt.Errorf("network policy: %w", err)
	}
	ws := builtin.Workspace{
		Dir:             root,
		WriteRoots:      writeRoots,
		ForbidReadRoots: cfg.Sandbox.ForbidRead,
		Bash: sandbox.Spec{
			Mode:            cfg.BashMode(),
			WriteRoots:      writeRoots,
			ForbidReadRoots: cfg.Sandbox.ForbidRead,
			Network:         cfg.Sandbox.Network,
		},
		BashTimeout:  time.Duration(cfg.BashTimeoutSeconds()) * time.Second,
		Search:       search,
		ProxySpec:    proxySpec,
		NetPolicy:    netPolicy,
		SessionGuard: guard,
	}
	// readOnlyServed is the default fail-closed tool set. Session-coupled
	// tools (complete_step, todo_write, bash_output, wait, kill_shell) report
	// ReadOnly but do nothing useful without a live controller session, so
	// they are excluded from the MCP surface.
	readOnlyServed := []string{"read_file", "ls", "glob", "grep", "code_index"}
	tools := append([]tool.Tool{}, ws.Tools(readOnlyServed...)...)
	// Workspace already binds web_fetch to the configured proxy; the explicit
	// ConfineWebFetch guarantees the read-only set never falls back to the
	// unconfined init-registered instance even if the Workspace set changes.
	// New() deduplicates by name, keeping the Workspace-bound instance.
	tools = append(tools, builtin.ConfineWebFetch(proxySpec, netPolicy))
	// tool_search discovers the served surface on demand; web_search joins it
	// when a [web_search] engine is configured. Both are read-only and fit the
	// fail-closed MCP toolset.
	tools = append(tools, builtin.NewToolSearchTool(toolSearchSnapshot(tools)))
	if cfg.WebSearch.Enabled() {
		client, err := netclient.NewHTTPClient(proxySpec, netclient.TransportOptions{
			DialTimeout:           15 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			// Overall cap including the body: this server is single-threaded,
			// so a stalled search backend must never hang it permanently.
			Timeout: 30 * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("web_search: network client: %w", err)
		}
		wsTool, err := builtin.NewWebSearchTool(cfg.WebSearch.Engine, cfg.WebSearch.BaseURL, cfg.WebSearch.APIKey, cfg.WebSearch.MaxResults, client, netPolicy)
		if err != nil {
			return nil, fmt.Errorf("web_search: %w", err)
		}
		tools = append(tools, wsTool)
	}
	if allowWrite {
		// The writer tools confined to the workspace write roots (the
		// workspace itself also binds them; the explicit ConfineWriters set is
		// the failsafe layer — New() deduplicates by name and the Workspace
		// instances carry the same confinement).
		tools = append(tools, ws.Tools("write_file", "edit_file", "multi_edit", "move_file", "notebook_edit", "delete_range", "delete_symbol")...)
		tools = append(tools, builtin.ConfineBashWithNetPolicy(ws.Bash, guard, netPolicy, ws.BashTimeout))
	}
	return tools, nil
}

// toolSearchSnapshot builds the registry-contract snapshot tool_search searches
// over from a concrete tool slice (the MCP server has no live registry).
func toolSearchSnapshot(tools []tool.Tool) func() []tool.ContractEntry {
	return func() []tool.ContractEntry {
		entries := make([]tool.ContractEntry, 0, len(tools))
		for _, t := range tools {
			entries = append(entries, tool.ContractEntry{
				Name:        t.Name(),
				Description: t.Description(),
				ReadOnly:    t.ReadOnly(),
				Schema:      t.Schema(),
			})
		}
		return entries
	}
}

// mapPermissionMode translates the CLI-style approval flag into the writer
// fallback mode permission.New understands. The mapping mirrors the CLI's
// headless gate: dontAsk and ask both fail closed in this server (there is no
// interactive approver, so Ask decisions are refused), while auto and yolo
// map to Allow. Deny rules still win in every mode.
func mapPermissionMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "dontask", "dont-ask", "dont_ask":
		return "deny", nil
	case "ask":
		return "ask", nil
	case "auto", "yolo":
		return "allow", nil
	default:
		return "", fmt.Errorf("unknown --permission-mode %q (want dontAsk, ask, auto, or yolo)", mode)
	}
}
