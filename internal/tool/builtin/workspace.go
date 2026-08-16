package builtin

import (
	"path/filepath"
	"time"

	"corvus/internal/netclient"
	"corvus/internal/netpolicy"
	"corvus/internal/sandbox"
	"corvus/internal/tool"
)

// Workspace builds a built-in tool set bound to a working directory, so several
// agents can run concurrently with independent path roots — a desktop front-end
// opening one tab per project, say. The process working directory is global and
// cannot be made per-agent (os.Chdir is process-wide), so each tool instead
// resolves relative paths against this directory and bash runs in it.
//
// Dir is that directory (empty yields process-cwd tools, byte-identical to the
// compile-time built-ins). WriteRoots confines the file-writers (as
// ConfineWriters); when empty and Dir is set, Dir itself becomes the sole write
// root, so writes stay inside the project by default. ForbidReadRoots confines
// the read/list/search built-ins so they cannot peek at the listed directories.
// Bash is the OS-sandbox spec for the bash tool (as ConfineBash). SessionGuard
// rejects writer-tool targets inside Corvus's own session stores and makes
// bash warn when a command references them (see SessionDataGuard).
type Workspace struct {
	Dir             string
	WriteRoots      []string
	ForbidReadRoots []string
	Bash            sandbox.Spec
	BashTimeout     time.Duration
	Search          SearchSpec
	ProxySpec       netclient.ProxySpec
	// NetPolicy gates outbound URLs (web_fetch fetch and bash curl/wget
	// arguments) against the [network_policy] allow/deny rules; the zero
	// policy is unconfined.
	NetPolicy    netpolicy.Policy
	SessionGuard SessionDataGuard
	// ManagedConfig names the Corvus-owned config files the file-writers may
	// touch outside WriteRoots after a fresh per-write human approval (see
	// ManagedConfigPaths). The zero value disables the escape hatch.
	ManagedConfig ManagedConfigPaths
	// FileOverlay, when non-nil, serves read_file/write_file content through the
	// host transport (unsaved editor buffers) with disk fallback; Terminal, when
	// non-nil, runs foreground bash in a host-owned terminal when the local OS
	// sandbox is not enforcing. Both are nil outside host transports like ACP.
	FileOverlay FileOverlay
	Terminal    TerminalRunner
}

// Tools returns the built-in tools bound to the workspace, ready to Add to a
// per-run tool.Registry. An empty enabled list yields every built-in; otherwise
// only the named ones are returned (unknown names are ignored). This is the
// per-workspace analogue of the cli's process-cwd assembly — a desktop driver
// calls it once per agent instead of relying on the global working directory.
func (w Workspace) Tools(enabled ...string) []tool.Tool {
	writeRoots := w.WriteRoots
	if len(writeRoots) == 0 && w.Dir != "" {
		writeRoots = []string{w.Dir}
	}
	roots := realRoots(writeRoots)
	forbidRoots := realRoots(w.ForbidReadRoots)

	overrides := map[string]tool.Tool{
		"read_file":     readFile{workDir: w.Dir, forbidRoots: forbidRoots, overlay: w.FileOverlay},
		"write_file":    writeFile{workDir: w.Dir, roots: roots, guard: w.SessionGuard, managed: w.ManagedConfig, overlay: w.FileOverlay},
		"edit_file":     editFile{workDir: w.Dir, roots: roots, guard: w.SessionGuard, managed: w.ManagedConfig},
		"multi_edit":    multiEdit{workDir: w.Dir, roots: roots, guard: w.SessionGuard, managed: w.ManagedConfig},
		"move_file":     moveFile{workDir: w.Dir, roots: roots, guard: w.SessionGuard, managed: w.ManagedConfig},
		"notebook_edit": notebookEdit{workDir: w.Dir, roots: roots, guard: w.SessionGuard, managed: w.ManagedConfig},
		"delete_range":  deleteRange{workDir: w.Dir, roots: roots, guard: w.SessionGuard, managed: w.ManagedConfig},
		"delete_symbol": deleteSymbol{workDir: w.Dir, roots: roots, guard: w.SessionGuard, managed: w.ManagedConfig},
		"code_index":    codeIndex{workDir: w.Dir, forbidRoots: forbidRoots},
		"bash":          bash{workDir: w.Dir, sb: w.Bash, timeout: w.BashTimeout, guard: w.SessionGuard, terminal: w.Terminal, netPolicy: w.NetPolicy},
		"ls":            listDir{workDir: w.Dir, forbidRoots: forbidRoots},
		"glob":          globTool{workDir: w.Dir, forbidRoots: forbidRoots},
		"grep":          grepTool{workDir: w.Dir, rg: w.Search.RgPath, forbidRoots: forbidRoots, sb: w.Bash},
		"web_fetch":     webFetch{proxySpec: w.ProxySpec, policy: w.NetPolicy},
	}
	all := tool.Builtins()
	if len(enabled) == 0 {
		for i, t := range all {
			if bound, ok := overrides[t.Name()]; ok {
				all[i] = bound
			}
		}
		return all
	}
	want := make(map[string]bool, len(enabled))
	for _, n := range enabled {
		want[n] = true
	}
	out := make([]tool.Tool, 0, len(enabled))
	for _, t := range all {
		if want[t.Name()] {
			if bound, ok := overrides[t.Name()]; ok {
				t = bound
			}
			out = append(out, t)
		}
	}
	return out
}

// resolveIn maps a tool's path/pattern argument into a working directory. With
// an empty workDir it returns p unchanged — the process-cwd behavior the
// compile-time built-ins have always had, so existing callers are unaffected.
// Otherwise a relative p is joined onto workDir; an absolute p is returned as-is
// (an explicit absolute path is honored verbatim — the write-confiner, not this,
// enforces the workspace boundary). An empty p resolves to workDir itself, so a
// defaulted "." (ls/grep) targets the workspace root.
func resolveIn(workDir, p string) string {
	if workDir == "" {
		return p
	}
	if p == "" || p == "." {
		return workDir
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}

// Recursive walks (grep, glob, ls) prune directories via walkIgnorer, which
// layers the shared noise-dir table (fileutil.IsNoiseDir, ADR-0003) with the
// repository's ignore rules; see gitignore.go.

// skipForbidDir reports whether a directory should be pruned from a recursive
// walk because it is within any forbid-read root. forbidRoots are pre-resolved
// absolute paths; empty means unconfined.
func skipForbidDir(path string, forbidRoots []string) bool {
	return confineRead(forbidRoots, path)
}
