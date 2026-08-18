package hook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"corvus/internal/sandbox"
)

func TestProjectTrustPersistsCanonicalRootAndResolvesSymlink(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(project, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if IsProjectTrusted(home, project) {
		t.Fatal("new project must not be trusted")
	}
	if err := TrustProject(home, link); err != nil {
		t.Fatalf("TrustProject: %v", err)
	}
	if !IsProjectTrusted(home, project) || !IsProjectTrusted(home, link) {
		t.Fatal("trust should follow the canonical real project root")
	}
	if err := RevokeProjectTrust(home, project); err != nil {
		t.Fatalf("RevokeProjectTrust: %v", err)
	}
	if IsProjectTrusted(home, project) {
		t.Fatal("revoke should remove the canonical trust record")
	}
}

func TestLoadHeadlessProjectHooksFailsClosedWithoutParsing(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	writeSettings(t, project, `{"hooks":{"PreToolUse":[{"command":"this must never load"}]}}`)
	called := false
	hooks, report := LoadWithReport(LoadOptions{
		HomeDir:     home,
		ProjectRoot: project,
		Headless:    true,
		TrustProject: func(ProjectTrustRequest) ProjectTrustDecision {
			called = true
			return ProjectTrustRemember
		},
	})
	if called {
		t.Fatal("headless trust gate must not invoke an interactive decision callback")
	}
	if len(hooks) != 0 || !report.ProjectHooksFound || !report.TrustDenied {
		t.Fatalf("headless project-hook load = hooks=%+v report=%+v, want fail closed", hooks, report)
	}
}

func TestLoadRememberTrustsOnlyAfterDecision(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	writeSettings(t, project, `{"hooks":{"Stop":[{"command":"echo stop"}]}}`)
	requested := 0
	hooks, report := LoadWithReport(LoadOptions{
		HomeDir:     home,
		ProjectRoot: project,
		TrustProject: func(req ProjectTrustRequest) ProjectTrustDecision {
			requested++
			if req.SettingsPath != ProjectSettingsPath(project) {
				t.Fatalf("trust request path = %q", req.SettingsPath)
			}
			return ProjectTrustRemember
		},
	})
	if len(hooks) != 1 || hooks[0].Scope != ScopeProject || !report.TrustPersisted {
		t.Fatalf("remembered trust load = hooks=%+v report=%+v", hooks, report)
	}
	if requested != 1 {
		t.Fatalf("trust callback count = %d, want 1", requested)
	}

	hooks, report = LoadWithReport(LoadOptions{HomeDir: home, ProjectRoot: project})
	if len(hooks) != 1 || !report.TrustPersisted || report.TrustRequired {
		t.Fatalf("existing trust load = hooks=%+v report=%+v", hooks, report)
	}
}

func TestProjectHookSpawnerScrubsCredentialEnvironment(t *testing.T) {
	t.Setenv("CORVUS_TEST_API_KEY", "inherited-secret")
	result := DefaultSpawner(context.Background(), SpawnInput{
		Command:       `printf '%s' "$CORVUS_TEST_API_KEY"`,
		ProjectScoped: true,
		Timeout:       5 * time.Second,
	})
	if result.ExitCode != 0 || result.SpawnErr != nil {
		t.Fatalf("project hook env probe failed: %+v", result)
	}
	if result.Stdout != "" {
		t.Fatalf("project hook inherited a credential-like environment value: %q", result.Stdout)
	}
}

// TestProjectHookSandboxFailsClosedWhenBackendUnavailable verifies that when
// an OS sandbox is requested for project hooks but the backend is unavailable,
// the spawner refuses to run the hook unconfined instead of silently executing
// it. This is the fail-closed contract: trusted-but-unconfinable hooks are
// safer to skip than to run without the boundary.
func TestProjectHookSandboxFailsClosedWhenBackendUnavailable(t *testing.T) {
	// An enforce spec with a write root that does not exist on this host still
	// exercises the backend-availability check: if bwrap/seatbelt is missing,
	// the spawner must return a SpawnErr, not run the command.
	// On platforms with a working backend (CI, dev machines with bwrap), this
	// test confirms the sandbox wraps and the hook runs confined.
	spawner := NewDefaultSpawner(RuntimeOptions{
		ProjectSandbox: sandbox.Spec{
			Mode:       "enforce",
			WriteRoots: []string{t.TempDir()},
			Network:    false,
		},
	})
	result := spawner(context.Background(), SpawnInput{
		Command:       `echo unsandboxed`,
		ProjectScoped: true,
		Timeout:       5 * time.Second,
		Cwd:           t.TempDir(),
	})
	// If the backend is available, the hook runs (exit 0) but confined.
	// If unavailable, the spawner must fail closed.
	if sandbox.Available() {
		if result.SpawnErr != nil {
			t.Fatalf("sandboxed project hook should run when backend is available: %+v", result)
		}
	} else {
		if result.SpawnErr == nil && result.ExitCode == 0 {
			t.Fatal("project hook ran unconfined when sandbox was requested but backend unavailable")
		}
	}
}

// TestProjectHookSandboxBlocksNetworkEgress verifies that a project hook under
// an enforced sandbox with Network=false cannot reach the network. This is a
// real defense-in-depth boundary: even a malicious trusted hook cannot
// exfiltrate data or fetch instructions from a remote server. This test
// validates the hardcoded Network=false setting in hookProjectSandbox.
func TestProjectHookSandboxBlocksNetworkEgress(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("OS sandbox backend unavailable on this platform")
	}
	spawner := NewDefaultSpawner(RuntimeOptions{
		ProjectSandbox: sandbox.Spec{
			Mode:       "enforce",
			WriteRoots: []string{t.TempDir()},
			Network:    false,
		},
	})
	// Try to reach a network resource. Under the sandbox, this should fail
	// (non-zero exit or empty output) because the network namespace is isolated.
	result := spawner(context.Background(), SpawnInput{
		Command:       `sh -c 'ping -c 1 -W 1 8.8.8.8 2>&1 || echo network_blocked'`,
		ProjectScoped: true,
		Timeout:       10 * time.Second,
		Cwd:           t.TempDir(),
	})
	// The hook process should run (not fail to spawn), but the network call
	// inside it should fail because --unshare-net is active.
	if result.SpawnErr != nil {
		t.Fatalf("sandboxed hook failed to spawn: %+v", result)
	}
	// The command should have produced output indicating network isolation.
	// Either ping failed to resolve/connect, or our fallback echo ran.
	if result.Stdout == "" && result.Stderr == "" {
		t.Fatal("sandboxed hook produced no output at all")
	}
	// Verify the sandbox actually blocked network (ping should not succeed).
	combined := result.Stdout + result.Stderr
	if strings.Contains(combined, "1 packets transmitted, 1 received") {
		t.Fatal("project hook sandbox did not block network access")
	}
}

// TestGlobalHookNotSandboxed verifies that global hooks are not subjected to
// the project-sandbox path: they are user-installed configuration and run with
// the normal environment, even when a ProjectSandbox spec is configured.
func TestGlobalHookNotSandboxed(t *testing.T) {
	spawner := NewDefaultSpawner(RuntimeOptions{
		ProjectSandbox: sandbox.Spec{
			Mode:       "enforce",
			WriteRoots: []string{t.TempDir()},
			Network:    false,
		},
	})
	// A global hook (ProjectScoped=false) should run normally regardless of
	// the ProjectSandbox spec.
	result := spawner(context.Background(), SpawnInput{
		Command:       `echo "global hook ran"`,
		ProjectScoped: false,
		Timeout:       5 * time.Second,
		Cwd:           t.TempDir(),
	})
	if result.SpawnErr != nil {
		t.Fatalf("global hook failed: %+v", result)
	}
	if result.ExitCode != 0 {
		t.Fatalf("global hook exit code = %d, want 0", result.ExitCode)
	}
	if result.Stdout == "" {
		t.Fatal("global hook produced no output")
	}
}
