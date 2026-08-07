package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLockUserConfigEditsSerializesRMW drives concurrent load-modify-save
// cycles through the edit lock and checks no writer's change is dropped.
// Without the lock, two editors load the same base config, each append their
// own MCP server, and the second save silently erases the first one's entry.
func TestLockUserConfigEditsSerializesRMW(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	path := UserConfigPath()
	if path == "" {
		t.Fatal("UserConfigPath is empty with CORVUS_HOME set")
	}

	const writers = 8
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			unlock := LockUserConfigEdits()
			defer unlock()
			cfg := LoadForEdit(path)
			cfg.Plugins = append(cfg.Plugins, PluginEntry{Name: fmt.Sprintf("server-%d", n), Command: "mcp-server"})
			if err := cfg.SaveTo(path); err != nil {
				t.Errorf("save: %v", err)
			}
		}(i)
	}
	wg.Wait()

	cfg := LoadForEdit(path)
	if got := len(cfg.Plugins); got != writers {
		t.Fatalf("plugins = %d, want %d (concurrent read-modify-write dropped updates)", got, writers)
	}
}

// TestConcurrentMCPAndSettingsWritersKeepBothFields checks that MCP and agent
// settings updates can share the user config without losing either field.
// Each writer also checks under the lock that its previous round survived, so
// a lost update fails before the final assertion.
func TestConcurrentMCPAndSettingsWritersKeepBothFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	path := UserConfigPath()
	if path == "" {
		t.Fatal("UserConfigPath is empty with CORVUS_HOME set")
	}

	const rounds = 40
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	// MCP settings writer replaces one server entry on each round.
	go func() {
		defer wg.Done()
		<-start
		for i := 1; i <= rounds; i++ {
			unlock := LockUserConfigEdits()
			cfg := LoadForEdit(path)
			if i > 1 {
				wantName := fmt.Sprintf("server-%d", i-1)
				if len(cfg.Plugins) != 1 || cfg.Plugins[0].Name != wantName {
					unlock()
					t.Errorf("round %d: previous MCP update lost: got %+v, want single server %q", i, cfg.Plugins, wantName)
					return
				}
			}
			cfg.Plugins = []PluginEntry{{Name: fmt.Sprintf("server-%d", i), Command: "mcp-server"}}
			err := cfg.SaveTo(path)
			unlock()
			if err != nil {
				t.Errorf("MCP writer save: %v", err)
				return
			}
		}
	}()

	// Agent settings writer bumps a TUI-supported runtime field.
	go func() {
		defer wg.Done()
		<-start
		for i := 1; i <= rounds; i++ {
			unlock := LockUserConfigEdits()
			cfg := LoadForEdit(path)
			if i > 1 && cfg.Agent.Temperature != float64(i-1) {
				unlock()
				t.Errorf("round %d: previous settings update lost: Temperature = %v, want %d", i, cfg.Agent.Temperature, i-1)
				return
			}
			cfg.Agent.Temperature = float64(i)
			err := cfg.SaveTo(path)
			unlock()
			if err != nil {
				t.Errorf("settings writer save: %v", err)
				return
			}
		}
	}()

	close(start)
	wg.Wait()
	if t.Failed() {
		return
	}

	final := LoadForEdit(path)
	wantName := fmt.Sprintf("server-%d", rounds)
	if len(final.Plugins) != 1 || final.Plugins[0].Name != wantName {
		t.Fatalf("MCP writer's last update lost: got %+v, want single server %q", final.Plugins, wantName)
	}
	if final.Agent.Temperature != rounds {
		t.Fatalf("settings writer's last update lost: Temperature = %v, want %d", final.Agent.Temperature, rounds)
	}
}

func TestLockUserConfigEditsSerializesAcrossProcessesWithDifferentTempDirs(t *testing.T) {
	home := t.TempDir()
	assertUserConfigLockSerializesAcrossProcesses(
		t,
		home,
		home,
		filepath.Join(t.TempDir(), "tmp-a"),
		filepath.Join(t.TempDir(), "tmp-b"),
	)
}

func TestLockUserConfigEditsSerializesDarwinCaseAliasesAcrossProcesses(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin path aliases only")
	}
	parent := t.TempDir()
	home := filepath.Join(parent, "MiXeDHome")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := strings.ToUpper(home)
	homeInfo, homeErr := os.Stat(home)
	aliasInfo, aliasErr := os.Stat(alias)
	if homeErr != nil || aliasErr != nil || !os.SameFile(homeInfo, aliasInfo) {
		t.Skip("test volume is case-sensitive")
	}
	assertUserConfigLockSerializesAcrossProcesses(t, home, alias, t.TempDir(), t.TempDir())
}

func assertUserConfigLockSerializesAcrossProcesses(t *testing.T, firstHome, secondHome, firstTmp, secondTmp string) {
	t.Helper()
	if err := os.MkdirAll(firstTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secondTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	home := firstHome
	t.Setenv("CORVUS_HOME", home)
	path := UserConfigPath()
	if err := Default().SaveTo(path); err != nil {
		t.Fatal(err)
	}

	signals := t.TempDir()
	aStarted := filepath.Join(signals, "a-started")
	aAcquired := filepath.Join(signals, "a-acquired")
	aRelease := filepath.Join(signals, "a-release")
	bStarted := filepath.Join(signals, "b-started")
	bAcquired := filepath.Join(signals, "b-acquired")

	startHelper := func(mode, processHome, processTmp, started, acquired, release string) (*exec.Cmd, *bytes.Buffer) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestLockUserConfigEditsHelperProcess$")
		cmd.Env = testEnvWithOverrides(map[string]string{
			"TMPDIR":                      processTmp,
			"CORVUS_HOME":                 processHome,
			"CORVUS_CONFIG_LOCK_HELPER":   "1",
			"CORVUS_CONFIG_LOCK_MODE":     mode,
			"CORVUS_CONFIG_LOCK_STARTED":  started,
			"CORVUS_CONFIG_LOCK_ACQUIRED": acquired,
			"CORVUS_CONFIG_LOCK_RELEASE":  release,
		})
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			t.Fatalf("start %s helper: %v", mode, err)
		}
		return cmd, &output
	}
	waitForFile := func(path string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", path)
	}

	first, firstOutput := startHelper("mcp", firstHome, firstTmp, aStarted, aAcquired, aRelease)
	waitForFile(aAcquired)
	second, secondOutput := startHelper("agent", secondHome, secondTmp, bStarted, bAcquired, "")
	waitForFile(bStarted)
	time.Sleep(150 * time.Millisecond)
	if _, err := os.Stat(bAcquired); err == nil {
		firstLock, _ := os.ReadFile(aAcquired)
		secondLock, _ := os.ReadFile(bAcquired)
		t.Fatalf(
			"second process acquired the user config lock before the first released it: first=%q second=%q",
			strings.TrimSpace(string(firstLock)),
			strings.TrimSpace(string(secondLock)),
		)
	}
	if err := os.WriteFile(aRelease, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(); err != nil {
		t.Fatalf("first helper: %v\n%s", err, firstOutput.String())
	}
	if err := second.Wait(); err != nil {
		t.Fatalf("second helper: %v\n%s", err, secondOutput.String())
	}

	final, err := LoadForEditReadOnlyStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Plugins) != 1 || final.Plugins[0].Name != "cross-process" {
		t.Fatalf("MCP update was lost: %+v", final.Plugins)
	}
	if final.Agent.Temperature != 0.75 {
		t.Fatalf("agent update was lost: temperature = %v, want 0.75", final.Agent.Temperature)
	}
}

func testEnvWithOverrides(overrides map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, overridden := overrides[key]; overridden {
				continue
			}
		}
		env = append(env, entry)
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func TestLockUserConfigEditsFailsClosedWhenFileLockTimesOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CORVUS_HOME", home)
	path := UserConfigPath()
	if err := Default().SaveTo(path); err != nil {
		t.Fatal(err)
	}

	release, err := acquireConfigFileEditLockWithTimeout(path, time.Second)
	if err != nil {
		t.Fatalf("hold config file lock: %v", err)
	}
	defer release()

	previousTimeout := userConfigEditLockTimeout
	userConfigEditLockTimeout = 30 * time.Millisecond
	t.Cleanup(func() { userConfigEditLockTimeout = previousTimeout })

	unlock := LockUserConfigEdits()
	defer unlock()
	if err := currentUserConfigEditLockError(); err == nil {
		t.Fatal("LockUserConfigEdits did not report the file-lock timeout")
	}

	cfg := LoadForEdit(path)
	cfg.Agent.Temperature = 0.75
	if err := cfg.SaveTo(path); err == nil {
		t.Fatal("SaveTo wrote user config after the cross-process lock failed")
	}
}

func TestLockUserConfigEditsHelperProcess(t *testing.T) {
	if os.Getenv("CORVUS_CONFIG_LOCK_HELPER") != "1" {
		return
	}
	started := os.Getenv("CORVUS_CONFIG_LOCK_STARTED")
	acquired := os.Getenv("CORVUS_CONFIG_LOCK_ACQUIRED")
	release := os.Getenv("CORVUS_CONFIG_LOCK_RELEASE")
	if err := os.WriteFile(started, []byte("started\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	unlock := LockUserConfigEdits()
	defer unlock()
	if err := currentUserConfigEditLockError(); err != nil {
		t.Fatalf("acquire user config file lock: %v", err)
	}
	lockPath, err := configFileEditLockPath(UserConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(acquired, []byte(lockPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if release != "" {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(release); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for release signal")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	path := UserConfigPath()
	cfg, err := LoadForEditReadOnlyStrict(path)
	if err != nil {
		t.Fatal(err)
	}
	switch os.Getenv("CORVUS_CONFIG_LOCK_MODE") {
	case "mcp":
		cfg.Plugins = []PluginEntry{{Name: "cross-process", Command: "mcp-server"}}
	case "agent":
		cfg.Agent.Temperature = 0.75
	default:
		t.Fatal("unknown helper mode")
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
}

func TestConfigEditLockCanonicalizesAliasesAndIgnoresCacheOverrides(t *testing.T) {
	dir := t.TempDir()

	target := filepath.Join(dir, "target.toml")
	link := filepath.Join(dir, "corvus.toml")
	if err := os.WriteFile(target, []byte("[agent]\ntemperature = 0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	aliasLock, err := configFileEditLockPath(link)
	if err != nil {
		t.Fatal(err)
	}
	targetLock, err := configFileEditLockPath(target)
	if err != nil {
		t.Fatal(err)
	}
	if aliasLock != targetLock {
		t.Fatalf("alias lock = %q, target lock = %q", aliasLock, targetLock)
	}

	t.Setenv("CORVUS_CACHE_HOME", filepath.Join(dir, "cache-a"))
	first, err := configFileEditLockPath(link)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORVUS_CACHE_HOME", filepath.Join(dir, "cache-b"))
	second, err := configFileEditLockPath(link)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("cache override split config lock: %q != %q", first, second)
	}
	t.Setenv("HOME", filepath.Join(dir, "isolated-home"))
	t.Setenv("CORVUS_HOME", filepath.Join(dir, "corvus-home"))
	t.Setenv("TMPDIR", filepath.Join(dir, "tmp-a"))
	third, err := configFileEditLockPath(link)
	if err != nil {
		t.Fatal(err)
	}
	if first != third {
		t.Fatalf("HOME/profile/TMPDIR override split config lock: %q != %q", first, third)
	}
	wantDir, err := configEditLockRegistryDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != wantDir {
		t.Fatalf("lock dir = %q, want OS-user registry %q", filepath.Dir(first), wantDir)
	}
}

func TestAcquireConfigEditLockRejectsSymlinkRegistry(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(dir, "locks")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if unlock, err := acquireConfigEditLockPath(ctx, filepath.Join(linkDir, "config.lock")); err == nil {
		unlock()
		t.Fatal("symlinked lock registry was accepted")
	}
}

func TestAcquireConfigEditLockSecuresRegistryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows protects the per-user lock registry through inherited ACLs, not Unix permission bits")
	}
	dir := filepath.Join(t.TempDir(), "locks")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := acquireConfigEditLockPath(ctx, filepath.Join(dir, "config.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("lock registry mode = %04o, want 0700", got)
	}
}

func TestConfigEditTransactionPinsSymlinkTarget(t *testing.T) {
	dir := t.TempDir()

	first := filepath.Join(dir, "first.toml")
	second := filepath.Join(dir, "second.toml")
	link := filepath.Join(dir, "corvus.toml")
	if err := os.WriteFile(first, []byte("[agent]\ntemperature = 0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const secondBody = "[agent]\ntemperature = 0.9\n"
	if err := os.WriteFile(second, []byte(secondBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	unlock, err := LockConfigFileEdits(link)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForEditReadOnlyStrict(link)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Temperature != 0.1 {
		t.Fatalf("transaction followed retargeted link: temperature = %v, want 0.1", cfg.Agent.Temperature)
	}
	cfg.Agent.Temperature = 0.2
	if err := cfg.SaveTo(link); err != nil {
		t.Fatal(err)
	}

	gotFirst, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotFirst), "temperature = 0.2") {
		t.Fatalf("pinned target was not updated:\n%s", gotFirst)
	}
	gotSecond, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotSecond) != secondBody {
		t.Fatalf("retargeted destination was modified: %q", gotSecond)
	}
}

func TestLoadForEditMalformedConfigCannotBeSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corvus.toml")
	const malformed = "[agent\ntemperature = 0.4\n"
	if err := os.WriteFile(path, []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadForEdit(path)
	cfg.Agent.Temperature = 0.7
	if err := cfg.SaveTo(path); err == nil {
		t.Fatal("SaveTo accepted defaults returned after a malformed edit load")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != malformed {
		t.Fatalf("malformed config was overwritten: %q", got)
	}
}
