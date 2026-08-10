package proc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const helperEnv = "CORVUS_PROC_HELPER_MODE"

// TestHelperProcess is the helper-process entry point for lifecycle tests. It
// only acts when CORVUS_PROC_HELPER_MODE is set; the parent test kills it, so
// it never kills the test process itself.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv(helperEnv) {
	case "block":
		fmt.Println("ready")
		select {} // stay alive until the parent kills the tree
	case "sleep":
		time.Sleep(time.Hour)
	case "exit":
		os.Exit(0)
	}
}

func helperProc(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("abs test binary: %v", err)
	}
	cmd := exec.Command(bin, "-test.run=TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	return cmd
}

// waitForReady blocks until the helper prints "ready" on its stdout, with a
// hard deadline so a wedged child fails the test instead of hanging it.
func waitForReady(t *testing.T, stdout *bufio.Reader) {
	t.Helper()
	line := make(chan string, 1)
	go func() {
		l, _ := stdout.ReadString('\n')
		line <- l
	}()
	select {
	case l := <-line:
		if strings.TrimSpace(l) != "ready" {
			t.Fatalf("helper output = %q, want ready", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper never became ready")
	}
}

func TestNormalizeRunOptionsDefaults(t *testing.T) {
	opts := normalizeRunOptions(RunOptions{})
	if opts.CancelWaitGrace != time.Second {
		t.Errorf("CancelWaitGrace = %v, want 1s", opts.CancelWaitGrace)
	}
	if opts.CancelRetryInterval != defaultCancelRetryInterval {
		t.Errorf("CancelRetryInterval = %v, want %v", opts.CancelRetryInterval, defaultCancelRetryInterval)
	}
	if opts.CancelRetryFor != defaultCancelRetryFor {
		t.Errorf("CancelRetryFor = %v, want %v", opts.CancelRetryFor, defaultCancelRetryFor)
	}
	opts = normalizeRunOptions(RunOptions{
		CancelWaitGrace:     250 * time.Millisecond,
		CancelRetryInterval: 30 * time.Millisecond,
		CancelRetryFor:      90 * time.Millisecond,
	})
	if opts.CancelWaitGrace != 250*time.Millisecond ||
		opts.CancelRetryInterval != 30*time.Millisecond ||
		opts.CancelRetryFor != 90*time.Millisecond {
		t.Errorf("explicit durations not preserved: %+v", opts)
	}
}

func TestCanceledWaitError(t *testing.T) {
	cause := context.Canceled
	waitErr := errors.New("wait failed")
	e := CanceledWaitError{Cause: cause, WaitErr: waitErr}
	if e.Error() != context.Canceled.Error() {
		t.Errorf("Error() = %q, want %q", e.Error(), context.Canceled.Error())
	}
	if !errors.Is(e, cause) || !errors.Is(e, waitErr) {
		t.Errorf("errors.Is failed for %+v", e)
	}
	var target *os.PathError
	if errors.As(e, &target) {
		t.Error("errors.As should not match unrelated error types")
	}
	if e.Unwrap()[0] != cause || e.Unwrap()[1] != waitErr {
		t.Errorf("Unwrap = %v, want [cause waitErr]", e.Unwrap())
	}
	causeOnly := CanceledWaitError{Cause: cause}
	if causeOnly.Error() != context.Canceled.Error() || len(causeOnly.Unwrap()) != 1 {
		t.Errorf("cause-only: %q unwrap %v", causeOnly.Error(), causeOnly.Unwrap())
	}
	waitOnly := CanceledWaitError{WaitErr: waitErr}
	if waitOnly.Error() != waitErr.Error() || len(waitOnly.Unwrap()) != 1 {
		t.Errorf("wait-only: %q unwrap %v", waitOnly.Error(), waitOnly.Unwrap())
	}
	empty := CanceledWaitError{}
	if empty.Error() != "command wait canceled" || empty.Unwrap() != nil {
		t.Errorf("empty: %q unwrap %v", empty.Error(), empty.Unwrap())
	}
}

func TestSetCancelKillsTreeWiresCancel(t *testing.T) {
	SetCancelKillsTree(nil) // must not panic
	cmd := exec.Command("true")
	SetCancelKillsTree(cmd)
	if cmd.Cancel == nil {
		t.Fatal("SetCancelKillsTree did not install cmd.Cancel")
	}
	if runtime.GOOS != "windows" {
		if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
			t.Fatal("SetCancelKillsTree should detach the child into a new session off Windows")
		}
	}
	// The installed Cancel must reap the running child. SetCancelKillsTree
	// requires a CommandContext-created cmd (os/exec refuses a non-nil Cancel
	// otherwise).
	block := exec.CommandContext(context.Background(), helperProc(t, "block").Path, "-test.run=TestHelperProcess$")
	block.Env = helperProc(t, "block").Env
	stdout, err := block.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	SetCancelKillsTree(block)
	if err := block.Start(); err != nil {
		t.Fatal(err)
	}
	waitForReady(t, bufio.NewReader(stdout))
	if err := block.Cancel(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("cmd.Cancel = %v, want context.Canceled", err)
	}
	done := make(chan error, 1)
	go func() { done <- block.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("child survived cmd.Cancel")
	}
}

func TestRunCommandTrackedCancellationKillsTree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, helperProc(t, "block").Path, "-test.run=TestHelperProcess$")
	cmd.Env = helperProc(t, "block").Env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var tracked *TrackedCommand
	go func() {
		var runErr error
		tracked, runErr = RunCommand(ctx, cmd, RunOptions{
			Track:               true,
			Source:              "test-source",
			ShellKind:           "sh",
			ShellPath:           "/bin/sh",
			CommandPreview:      "block",
			CancelWaitGrace:     50 * time.Millisecond,
			CancelRetryInterval: time.Millisecond,
			CancelRetryFor:      10 * time.Millisecond,
		})
		done <- runErr
	}()

	waitForReady(t, bufio.NewReader(stdout))
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunCommand error = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunCommand did not return after cancellation")
	}
	// RunCommand has returned, so cmd.Process is stable.
	rootPID := cmd.Process.Pid

	diag := tracked.Diagnostics()
	if diag.KillCalls == 0 {
		t.Error("tracked command was not killed")
	}
	if !diag.Tracked {
		t.Error("Tracked = false, want true")
	}
	if diag.RootPID != rootPID {
		t.Errorf("RootPID = %d, want %d", diag.RootPID, rootPID)
	}
	if diag.Source != "test-source" || diag.ShellKind != "sh" || diag.ShellPath != "/bin/sh" {
		t.Errorf("metadata not recorded: %+v", diag)
	}
	// TreeTrackerStarted is not asserted here: setTree races with cancellation
	// (TestTrackedCommandDiagnosticsLifecycle covers the deterministic path).
}

func TestRunCommandTrackedNormalExit(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), helperProc(t, "exit").Path, "-test.run=TestHelperProcess$")
	cmd.Env = helperProc(t, "exit").Env
	tracked, err := RunCommand(context.Background(), cmd, RunOptions{
		Track: true, CancelWaitGrace: time.Second,
	})
	if err != nil {
		t.Fatalf("RunCommand = %v, want nil", err)
	}
	if tracked == nil {
		t.Fatal("tracked = nil, want non-nil")
	}
	if tracked.Diagnostics().Tracked != true {
		t.Error("Tracked = false after normal exit")
	}
}

func TestRunCommandUntrackedNormalExit(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), helperProc(t, "exit").Path, "-test.run=TestHelperProcess$")
	cmd.Env = helperProc(t, "exit").Env
	tracked, err := RunCommand(context.Background(), cmd, RunOptions{})
	if err != nil {
		t.Fatalf("RunCommand = %v, want nil", err)
	}
	if tracked != nil {
		t.Fatalf("untracked RunCommand returned %+v, want nil", tracked)
	}
}

func TestRunCommandUntrackedCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := helperProc(t, "block")
	cmdCtx := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	cmdCtx.Env = cmd.Env
	stdout, err := cmdCtx.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := RunCommand(ctx, cmdCtx, RunOptions{})
		done <- err
	}()
	waitForReady(t, bufio.NewReader(stdout))
	cancel()
	select {
	case err := <-done:
		// Untracked cancellation reports exec's wait result (e.g. "signal:
		// killed") rather than wrapping it as context.Canceled; the contract is
		// that the process tree is dead and RunCommand returns.
		if err == nil {
			t.Fatal("RunCommand = nil, want cancellation/wait error")
		}
		if cmdCtx.ProcessState == nil || cmdCtx.ProcessState.Success() {
			t.Errorf("child not terminated after cancellation (state %v)", cmdCtx.ProcessState)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("untracked RunCommand did not return after cancellation")
	}
}

// TestRunCommandRequiresCommandContext locks the os/exec contract: proc wires
// cmd.Cancel, which Go only permits on CommandContext-created commands. A
// caller that starts from exec.Command fails closed at Start instead of
// silently losing tree-kill semantics.
func TestRunCommandRequiresCommandContext(t *testing.T) {
	cmd := exec.Command("true") // not CommandContext
	tracked, err := RunCommand(context.Background(), cmd, RunOptions{Track: true})
	if err == nil || !strings.Contains(err.Error(), "CommandContext") {
		t.Fatalf("Track=true with exec.Command error = %v, want CommandContext failure", err)
	}
	if tracked == nil {
		t.Fatal("tracked must be non-nil even when Start fails")
	}
}

func TestRunCommandStartErrors(t *testing.T) {
	missing := exec.Command("corvus-test-no-such-binary-xyz")
	tracked, err := RunCommand(context.Background(), missing, RunOptions{Track: true})
	if err == nil {
		t.Fatal("Track=true with missing binary: want error")
	}
	if tracked == nil {
		t.Fatal("Track=true with missing binary: tracked must be non-nil")
	}

	missing = exec.Command("corvus-test-no-such-binary-xyz")
	if tracked, err := RunCommand(context.Background(), missing, RunOptions{}); err == nil || tracked != nil {
		t.Fatalf("Track=false with missing binary: err=%v tracked=%v, want error + nil", err, tracked)
	}
}

func TestWaitForTrackedCommandWaitFirst(t *testing.T) {
	sentinel := errors.New("wait sentinel")
	tracked := &TrackedCommand{}
	if err := waitForTrackedCommand(context.Background(), tracked,
		func() error { return sentinel }, time.Second, time.Millisecond, time.Millisecond); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want sentinel", err)
	}
	if tracked.Diagnostics().KillCalls != 0 {
		t.Error("kill must not be invoked when wait completes first")
	}
	tracked2 := &TrackedCommand{}
	if err := waitForTrackedCommand(context.Background(), tracked2,
		func() error { return nil }, time.Second, time.Millisecond, time.Millisecond); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}

func TestTrackedCommandDiagnosticsLifecycle(t *testing.T) {
	tracked := &TrackedCommand{}
	tracked.setMetadata(RunOptions{Source: "s", ShellKind: "k", ShellPath: "p", CommandPreview: "c"})
	tracked.setStarted(0)
	diag := tracked.Diagnostics()
	if diag.Source != "s" || diag.ShellKind != "k" || diag.ShellPath != "p" || diag.CommandPreview != "c" {
		t.Errorf("metadata: %+v", diag)
	}
	if !diag.Tracked || diag.JobObjectCreated || diag.RootPID != 0 {
		t.Errorf("started state: %+v", diag)
	}

	tracked.setTree(&TreeTracker{})
	diag = tracked.Diagnostics()
	if !diag.TreeTrackerStarted {
		t.Error("TreeTrackerStarted = false after setTree")
	}

	tracked.Kill()
	tracked.Kill()
	diag = tracked.Diagnostics()
	if diag.KillCalls != 2 {
		t.Errorf("KillCalls = %d, want 2", diag.KillCalls)
	}

	tracked.StopTracking()
	if tracked.Diagnostics().KillCalls != 2 {
		t.Error("StopTracking must not kill")
	}

	tracked.markGraceExpired(2 * time.Second)
	diag = tracked.Diagnostics()
	if !diag.CancelWaitGraceExpired || diag.CancelRetryWindowMillis != 2000 {
		t.Errorf("grace expiry state: %+v", diag)
	}
}

func TestTrackedCommandSetTreeAfterKill(t *testing.T) {
	tracked := &TrackedCommand{}
	tracked.Kill() // kill before the tree is attached (start race)
	tracked.setTree(&TreeTracker{})
	diag := tracked.Diagnostics()
	if diag.KillCalls != 1 {
		t.Errorf("KillCalls = %d, want 1", diag.KillCalls)
	}
	if diag.TreeTrackerStarted {
		t.Error("tree must not be tracked once killed")
	}
}

func TestTrackedCommandNilReceivers(t *testing.T) {
	var nilCmd *TrackedCommand
	nilCmd.Kill()
	nilCmd.StopTracking()
	nilCmd.setMetadata(RunOptions{})
	nilCmd.setStarted(0)
	nilCmd.setTree(&TreeTracker{})
	nilCmd.markGraceExpired(time.Second)
	if got := nilCmd.Diagnostics(); got != (RunDiagnostics{}) {
		t.Errorf("nil Diagnostics = %+v, want zero value", got)
	}
}

func TestRetryKillUntilWaitDeadline(t *testing.T) {
	tracked := &TrackedCommand{}
	tracked.markGraceExpired(time.Millisecond)
	waitCh := make(chan error)
	done := make(chan struct{})
	go func() {
		tracked.retryKillUntilWait(waitCh, time.Millisecond, 5*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retryKillUntilWait did not stop at the retry deadline")
	}
	if tracked.Diagnostics().RetryKillCalls == 0 {
		t.Error("retryKillUntilWait never retried the kill")
	}
}

func TestRetryKillUntilWaitStopsOnWait(t *testing.T) {
	tracked := &TrackedCommand{}
	waitCh := make(chan error, 1)
	waitCh <- errors.New("wait done")
	done := make(chan struct{})
	go func() {
		tracked.retryKillUntilWait(waitCh, time.Millisecond, 10*time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retryKillUntilWait did not stop when wait completed")
	}
	if tracked.Diagnostics().RetryKillCalls != 0 {
		t.Error("retryKillUntilWait killed after wait completed")
	}
}

// TestTreeTrackerNoOpsOnNilAndUnstarted documents the platform-independent
// contract of tree_other.go's empty implementation (and the windows
// implementation's nil guards): TrackTree never panics and returns nil for a
// nil or unstarted command; Stop is a no-op; Kill reports zero kills.
func TestTreeTrackerNoOpsOnNilAndUnstarted(t *testing.T) {
	if tt := TrackTree(nil); tt != nil {
		t.Fatalf("TrackTree(nil) = %v, want nil", tt)
	}
	if tt := TrackTree(&exec.Cmd{}); tt != nil {
		t.Fatalf("TrackTree(unstarted) = %v, want nil", tt)
	}
	var tt *TreeTracker
	tt.Stop() // must not panic
	if n := tt.Kill(); n != 0 {
		t.Fatalf("nil TreeTracker.Kill() = %d, want 0", n)
	}
	// A concrete empty tracker is likewise inert.
	empty := &TreeTracker{}
	empty.Stop()
	if n := empty.Kill(); n != 0 {
		t.Fatalf("empty TreeTracker.Kill() = %d, want 0", n)
	}
}
