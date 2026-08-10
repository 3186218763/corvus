//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestKillTreeNilAndUnstartedNoOp(t *testing.T) {
	KillTree(nil) // must not panic
	KillTree(exec.Command("true"))
	KillTree(&exec.Cmd{})
}

func TestSetProcessGroupKillPreservesExistingSysProcAttr(t *testing.T) {
	cmd := &exec.Cmd{SysProcAttr: &syscall.SysProcAttr{Setpgid: true}}
	SetProcessGroupKill(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if !cmd.SysProcAttr.Setsid {
		t.Error("Setsid = false, want true")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Error("existing SysProcAttr fields were not preserved")
	}
}

func TestStartTrackedRequired(t *testing.T) {
	cmd := exec.Command("true")
	job, err := StartTrackedRequired(cmd)
	if err != nil {
		t.Fatalf("StartTrackedRequired: %v", err)
	}
	if job != 0 {
		t.Fatalf("job = %d, want 0 off Windows", job)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("StartTrackedRequired should detach the child into a new session")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestFinishTrackedNoOp(t *testing.T) {
	FinishTracked(0)
	FinishTracked(12345)
}

func TestPrepareShellPATHProbeSetsid(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo $PATH")
	PrepareShellPATHProbe(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("PrepareShellPATHProbe should detach the probe into a new session")
	}
	// Existing attributes are preserved.
	cmd2 := &exec.Cmd{SysProcAttr: &syscall.SysProcAttr{Setpgid: true}}
	PrepareShellPATHProbe(cmd2)
	if !cmd2.SysProcAttr.Setsid || !cmd2.SysProcAttr.Setpgid {
		t.Fatal("PrepareShellPATHProbe clobbered existing SysProcAttr fields")
	}
}

func TestLowPriorityNoOpOffWindows(t *testing.T) {
	cmd := exec.Command("true")
	LowPriority(cmd)
	if cmd.SysProcAttr != nil {
		t.Fatal("LowPriority should not touch SysProcAttr off Windows")
	}
}

func TestLowPriorityStarted(t *testing.T) {
	LowPriorityStarted(&exec.Cmd{}) // nil Process: must not panic
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	LowPriorityStarted(cmd) // renice of our own child must not fail loudly
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}
