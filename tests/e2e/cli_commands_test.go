//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/storage"
)

func TestCLICargoUninstall(t *testing.T) {
	f := newCLICargoFixture(t)
	f.setup(t)
	assertCLICargoLaunches(t, f)
	historyPath := storage.ExecutionLogPath(f.config.Storage.JSONFile)
	history := readCLIFile(t, historyPath)
	preview := f.cli(t, "manage", "--uninstall", "ripgrep", "--tool", "cargo", "--dry-run")
	assertCLIExit(t, preview, 0)
	assertCLIMissing(t, filepath.Join(f.home, "cargo-uninstall-args"))
	writeCLIFile(t, filepath.Join(f.home, "cargo-uninstall-failed"), "", 0o600)
	assertCLIExit(t, f.cli(t, "manage", "--uninstall", "ripgrep", "--tool", "cargo", "--yes"), 7)
	assertCLIRecordedToolUse(t, f, "cargo", "ripgrep", 1)
	if err := os.Remove(filepath.Join(f.home, "cargo-uninstall-failed")); err != nil {
		t.Fatal(err)
	}
	assertCLIExit(t, f.cli(t, "manage", "--uninstall", "ripgrep", "--tool", "cargo", "--yes"), 0)
	assertCLICargoUninstalled(t, f)
	assertCLIFile(t, historyPath, history)
}

func assertCLICargoUninstalled(t *testing.T, f *cliFixture) {
	t.Helper()
	root := filepath.Join(f.home, "cargo-root")
	want := "uninstall\n--root\n" + root + "\nripgrep@14.1.1\n"
	assertCLIFile(t, filepath.Join(f.home, "cargo-uninstall-args"), want)
	assertCLIMissing(t, filepath.Join(root, "bin", "rg"))
	assertCLIExit(t, f.cli(t, "scan"), 0)
	result := f.cli(t, "packages", "--tool", "cargo", "--format", "json")
	assertCLIExit(t, result, 0)
	hasRemainingCrate := !strings.Contains(result.stdout, "ripgrep") && strings.Contains(result.stdout, "cli-tools")
	if !hasRemainingCrate {
		t.Fatalf("remaining inventory = %s", result.stdout)
	}
}

func TestCLICargoUninstallPreservesOtherRoot(t *testing.T) {
	f := multipleCargoRootsFixture(t)
	f.setup(t)
	result := f.cli(t, "manage", "--uninstall", "ripgrep", "--tool", "cargo", "--yes")
	assertCLIExit(t, result, 0)
	root := filepath.Join(f.home, "cargo-root")
	want := "uninstall\n--root\n" + root + "\nripgrep@14.1.1\n"
	assertCLIFile(t, filepath.Join(f.home, "cargo-uninstall-args"), want)
	assertCLIMissing(t, filepath.Join(root, "bin", "rg"))
	assertCLIFile(t, filepath.Join(f.home, ".cargo", "bin", "rg"), "other installation\n")
}

func multipleCargoRootsFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := newCLICargoFixture(t)
	first, second := filepath.Join(f.home, "cargo-root"), filepath.Join(f.home, ".cargo")
	f.env = append(f.env, "CARGO_INSTALL_ROOT=")
	writeCLIFile(t, filepath.Join(first, "inventory"), "ripgrep v14.1.1:\n    rg\n", 0o600)
	writeCLIFile(t, filepath.Join(second, "inventory"), "ripgrep v14.1.0:\n    rg\n", 0o600)
	writeCLIFile(t, filepath.Join(second, "bin", "rg"), "other installation\n", 0o700)
	for _, root := range []string{first, second} {
		writeCLIFile(t, filepath.Join(root, ".crates.toml"), "[v1]\n", 0o600)
	}
	writeCLIFile(t, filepath.Join(f.bin, "cargo"), multipleCargoRootsScript, 0o700)
	return f
}

const multipleCargoRootsScript = `#!/bin/sh
set -eu
root=${CARGO_INSTALL_ROOT:-$CARGO_HOME}
case "$1" in
    install) /bin/cat "$root/inventory" ;;
    uninstall)
        printf '%s\n' "$@" > "$HOME/cargo-uninstall-args"
        [ "$2" = --root ] && [ "$4" = ripgrep@14.1.1 ]
        /bin/rm "$3/bin/rg"
        : > "$3/inventory"
        ;;
    --version) printf 'cargo fixture\n' ;;
    *) exit 2 ;;
esac
`

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
