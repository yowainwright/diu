//go:build e2e

package e2e

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type cliDaemon struct {
	cmd    *exec.Cmd
	output bytes.Buffer
	done   chan struct{}
	err    error
}

func startCLIDaemon(t *testing.T, f *cliFixture) *cliDaemon {
	t.Helper()
	cmd := f.command(t, cliBinary, "daemon", "start")
	cmd.Env = append(cmd.Env, "DIU_DAEMON_FOREGROUND=1")
	d := &cliDaemon{cmd: cmd, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = &d.output, &d.output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { d.err = cmd.Wait(); close(d.done) }()
	t.Cleanup(func() { stopCLIDaemon(t, d) })
	awaitCLI(t, func() bool {
		result := f.cli(t, "daemon", "status")
		return strings.Contains(result.stdout, "DIU daemon is running")
	})
	return d
}

func stopCLIDaemon(t *testing.T, d *cliDaemon) {
	t.Helper()
	_ = syscall.Kill(-d.cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-d.done:
	case <-time.After(time.Second):
		t.Error("container daemon did not exit after cleanup")
	}
}

func assertCLIDaemonStopped(t *testing.T, d *cliDaemon) {
	t.Helper()
	select {
	case <-d.done:
		if d.err != nil {
			t.Fatalf("daemon stopped with error: %v; log: %s", d.err, d.output.String())
		}
	case <-time.After(time.Second):
		t.Fatal("uninstall returned while recorder was still running")
	}
}

func TestCLIUninstallStopsRealRecorderAndRemovesRuntimeFiles(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	d := startCLIDaemon(t, f)
	assertCLICommandContract(t, f, "bash", "probe")
	waitCLIRecords(t, f, 1)
	assertCLIExit(t, f.cli(t, "uninstall"), 0)
	assertCLIDaemonStopped(t, d)
	assertCLIMissing(t, f.config.Daemon.PIDFile)
	assertCLIMissing(t, f.config.Daemon.SocketPath)
	assertCLICleanup(t, f)
	assertCLICommandContract(t, f, "bash", "probe")
}

func TestCLIUninstallCleansWrappersWhenRecorderCannotStop(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	d := startCLIDaemon(t, f)
	if err := d.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	result := f.cli(t, "uninstall")
	assertCLIExit(t, result, 1)
	if !strings.Contains(result.stderr, "timed out") {
		t.Fatalf("missing recorder timeout error: %#v", result)
	}
	assertCLICleanup(t, f)
	assertCLIMissing(t, filepath.Join(f.wrappers, ".diu-delegates"))
}
