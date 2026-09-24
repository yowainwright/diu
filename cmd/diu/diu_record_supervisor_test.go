package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestSupervisedRecorderCommandUsesBoundedDetachedProcess(t *testing.T) {
	lock, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	cmd, err := supervisedRecorderCommand(context.Background(), lock, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertSupervisedRecorderCommand(t, cmd, lock)
}

func assertSupervisedRecorderCommand(t *testing.T, cmd *exec.Cmd, lock *os.File) {
	t.Helper()
	assertRecorderWorkerTarget(t, cmd)
	assertRecorderWaitBound(t, cmd)
	assertRecorderInputs(t, cmd, lock)
	assertRecorderProcessDetached(t, cmd)
}

func assertRecorderWorkerTarget(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	wantExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	usesExecutable := cmd.Path == wantExecutable
	argsHaveWorker := len(cmd.Args) == 3
	workerArgument := argsHaveWorker && cmd.Args[1] == "record-worker"
	slotArgument := argsHaveWorker && cmd.Args[2] == "2"
	if !usesExecutable {
		t.Fatal("supervisor command uses the wrong executable")
	}
	if !workerArgument {
		t.Fatal("supervisor command does not target the recorder worker")
	}
	if !slotArgument {
		t.Fatal("supervisor command does not target the expected worker")
	}
}

func assertRecorderWaitBound(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	usesBounds := cmd.Cancel != nil && cmd.WaitDelay == time.Second
	if !usesBounds {
		t.Fatal("supervisor command has no wait bound")
	}
}

func assertRecorderInputs(t *testing.T, cmd *exec.Cmd, lock *os.File) {
	t.Helper()
	if cmd.Stdin != os.Stdin {
		t.Fatal("supervisor command did not pass stdin")
	}
	passesOneFile := len(cmd.ExtraFiles) == 1
	passesLock := passesOneFile && cmd.ExtraFiles[0] == lock
	if !passesLock {
		t.Fatal("supervisor command did not pass the slot lock")
	}
}

func assertRecorderProcessDetached(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	detached := cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid
	if !detached {
		t.Fatal("supervisor command is not detached")
	}
}

func TestKillRecorderGroupIgnoresMissingProcess(t *testing.T) {
	if err := killRecorderGroup(2147483647); err != nil {
		t.Fatalf("missing recorder process group returned error: %v", err)
	}
}

func TestRecorderDeadlineRequiresPrivateProcessGroup(t *testing.T) {
	stop, err := recorderDeadline()
	if err == nil {
		stop()
		return
	}
	if err.Error() != "recorder requires a private process group" {
		t.Fatalf("deadline error = %v", err)
	}
}

func TestRecorderDeadlineHelper(t *testing.T) {
	if os.Getenv("DIU_TEST_RECORDER_DEADLINE") != "1" {
		return
	}
	stop, err := recorderDeadline()
	if err != nil {
		t.Fatal(err)
	}
	stop()
}

func TestRecorderDeadlineAcceptsPrivateProcessGroup(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRecorderDeadlineHelper$")
	cmd.Env = append(os.Environ(), "DIU_TEST_RECORDER_DEADLINE=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("private group deadline failed: %v\n%s", err, output)
	}
}
