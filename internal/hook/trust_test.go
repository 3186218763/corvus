package hook

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
