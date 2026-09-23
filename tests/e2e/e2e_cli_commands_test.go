//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

type cliResult struct {
	stdout string
	stderr string
	code   int
}

func (f *cliFixture) command(t *testing.T, executable string, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env, cmd.Dir = f.env, f.home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 250 * time.Millisecond
	return cmd
}

func runCLICommand(t *testing.T, cmd *exec.Cmd, input string) cliResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	err := cmd.Wait()
	var exitErr *exec.ExitError
	hasUnexpectedError := err != nil && !errors.As(err, &exitErr)
	if hasUnexpectedError {
		t.Fatalf("command did not complete: %v; stderr: %s", err, stderr.String())
	}
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: cmd.ProcessState.ExitCode()}
}

func (f *cliFixture) cli(t *testing.T, args ...string) cliResult {
	t.Helper()
	return runCLICommand(t, f.command(t, cliBinary, args...), "")
}

func (f *cliFixture) setup(t *testing.T) {
	t.Helper()
	assertCLIExit(t, f.cli(t, "setup"), 0)
}

func assertCLIExit(t *testing.T, result cliResult, code int) {
	t.Helper()
	if result.code != code {
		t.Fatalf("exit = %d, want %d; stdout=%q stderr=%q", result.code, code, result.stdout, result.stderr)
	}
}

func readCLIFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertCLIFile(t *testing.T, path, want string) {
	t.Helper()
	if got := readCLIFile(t, path); got != want {
		t.Fatalf("%s changed: got %q, want %q", path, got, want)
	}
}

func assertCLIMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent: %v", path, err)
	}
}

func awaitCLI(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true within three seconds")
}
