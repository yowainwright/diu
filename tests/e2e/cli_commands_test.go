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

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

func TestCLIPipxUninstallPreservesOtherInstallAndHistory(t *testing.T) {
	f := newCLIPipxFixture(t)
	f.setup(t)
	assertCLIPipxLaunches(t, f)
	historyPath := storage.ExecutionLogPath(f.config.Storage.JSONFile)
	history := readCLIFile(t, historyPath)
	assertCLIPipxUninstallPreviewAndFailure(t, f)
	result := f.cli(t, "manage", "--uninstall", "px-tools@dev", "--tool", "pipx", "--yes")
	assertCLIExit(t, result, 0)
	assertCLIFile(t, filepath.Join(f.home, "pipx-uninstall-args"), "uninstall\npx-tools@dev\n")
	assertCLIPipxUninstalled(t, f)
	assertCLIFile(t, historyPath, history)
}

func assertCLIPipxUninstallPreviewAndFailure(t *testing.T, f *cliFixture) {
	t.Helper()
	preview := f.cli(t, "manage", "--uninstall", "px-tools@dev", "--tool", "pipx", "--dry-run")
	assertCLIExit(t, preview, 0)
	if !strings.Contains(preview.stdout, "pipx uninstall px-tools@dev") {
		t.Fatalf("unexpected pipx preview: %q", preview.stdout)
	}
	assertCLIMissing(t, filepath.Join(f.home, "pipx-uninstall-args"))
	failed := filepath.Join(f.home, "pipx-uninstall-failed")
	writeCLIFile(t, failed, "", 0o600)
	assertCLIExit(t, f.cli(t, "manage", "--uninstall", "px-tools@dev", "--tool", "pipx", "--yes"), 7)
	assertCLIRecordedToolUse(t, f, "pipx", "px-tools@dev", 1)
	if err := os.Remove(failed); err != nil {
		t.Fatal(err)
	}
}

func assertCLIPipxUninstalled(t *testing.T, f *cliFixture) {
	t.Helper()
	root := filepath.Join(f.home, "pipx root", "venvs")
	assertCLIMissing(t, filepath.Join(root, "px-tools@dev", "bin", "px-one"))
	assertCLIFile(t, filepath.Join(root, "px-tools", "bin", "px-one"), "#!/bin/sh\nexit 0\n")
	assertCLIExit(t, f.cli(t, "scan"), 0)
	remaining := f.cli(t, "packages", "--tool", "pipx", "--format", "json")
	assertCLIExit(t, remaining, 0)
	hasSuffix := strings.Contains(remaining.stdout, "px-tools@dev")
	hasOtherInstall := strings.Contains(remaining.stdout, "px-tools")
	isExpected := !hasSuffix && hasOtherInstall
	if !isExpected {
		t.Fatalf("unexpected remaining pipx inventory: %s", remaining.stdout)
	}
}

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

func TestCLIRubyGemsUninstallPreservesOtherVersionsAndHistory(t *testing.T) {
	f := newCLIGemFixture(t)
	f.setup(t)
	assertCLIGemLaunches(t, f)
	historyPath := storage.ExecutionLogPath(f.config.Storage.JSONFile)
	history := readCLIFile(t, historyPath)
	preview := f.cli(t, "manage", "--uninstall", "cli-tools", "--tool", "gem", "--dry-run")
	assertCLIExit(t, preview, 0)
	assertCLIMissing(t, filepath.Join(f.home, "gem-uninstall-args"))
	assertCLIGemBlockedRemoval(t, f)
	assertCLIExit(t, f.cli(t, "manage", "--uninstall", "cli-tools", "--tool", "gem", "--yes"), 0)
	assertCLIGemUninstalled(t, f)
	assertCLIFile(t, historyPath, history)
	assertCLIGemRemainingLaunch(t, f)
}

func assertCLIGemBlockedRemoval(t *testing.T, f *cliFixture) {
	t.Helper()
	for marker, code := range map[string]int{"gem-dependent": 1, "gem-uninstall-failed": 7} {
		path := filepath.Join(f.home, marker)
		writeCLIFile(t, path, "", 0o600)
		assertCLIExit(t, f.cli(t, "manage", "--uninstall", "cli-tools", "--tool", "gem", "--yes"), code)
		assertCLIRecordedToolUse(t, f, core.ToolGem, "cli-tools", 2)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func assertCLIGemUninstalled(t *testing.T, f *cliFixture) {
	t.Helper()
	root := filepath.Join(f.home, "gem home")
	args := "uninstall\ncli-tools\n--version\n2.0.0\n--install-dir\n" + root + "\n--platform\nruby\n--executables\n--abort-on-dependent\n--no-user-install\n--no-force\n--no-ignore-dependencies\n"
	assertCLIFile(t, filepath.Join(f.home, "gem-uninstall-args"), args)
	assertCLIMissing(t, filepath.Join(root, "gems", "cli-tools-2.0.0", "bin", "gem-one"))
	assertCLIFile(t, filepath.Join(root, "gems", "cli-tools-1.0.0", "bin", "gem-one"), "older gem version\n")
	assertCLIExit(t, f.cli(t, "scan"), 0)
	assertCLIGemRemainingVersion(t, f, root)
}

func assertCLIGemRemainingVersion(t *testing.T, f *cliFixture, root string) {
	t.Helper()
	store, err := storage.NewJSONStorage(f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	pkg, err := store.GetPackage(core.ToolGem, "cli-tools")
	if err != nil {
		t.Fatal(err)
	}
	hasVersion := pkg.Version == "1.0.0" && pkg.Path == filepath.Join(root, "gems", "cli-tools-1.0.0")
	if !hasVersion {
		t.Fatalf("scan did not select the remaining gem version: %#v", pkg)
	}
}

func assertCLIGemRemainingLaunch(t *testing.T, f *cliFixture) {
	t.Helper()
	command, input, output, stderr := startCLIZsh(t, f)
	writeZshInput(t, input, "gem-one\nprint -r -- DIU_GEM_REMAINING_DONE\n")
	waitCLIZshOutput(t, output, "DIU_GEM_REMAINING_DONE")
	records := waitCLIRecords(t, f, 5)
	assertCLIRecordedLaunchIdentity(t, records[0], core.ToolGem, "gem-one")
	assertCLIRecordedToolUse(t, f, core.ToolGem, "cli-tools", 1)
	finishCLIZsh(t, command, input, stderr)
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

func TestCLICargoUninstallRejectsStaleRootAfterScan(t *testing.T) {
	f := cargoRootSwitchFixture(t)
	oldBinary := filepath.Join(f.home, "cargo-root", "bin", "rg")
	before := readCLIFile(t, oldBinary)
	assertCLIExit(t, f.cli(t, "scan"), 0)
	for _, flag := range []string{"--dry-run", "--yes"} {
		result := f.cli(t, "manage", "--uninstall", "ripgrep", "--tool", "cargo", flag)
		assertCLIExit(t, result, 1)
		if !strings.Contains(result.stderr, "cargo install root is unknown") {
			t.Fatalf("missing unknown-root error: %q", result.stderr)
		}
	}
	assertCLIMissing(t, filepath.Join(f.home, "cargo-uninstall-args"))
	assertCLIFile(t, oldBinary, before)
	assertCLIFile(t, filepath.Join(f.home, "cargo-other", "bin", "rg"), "other installation\n")
}

func cargoRootSwitchFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := newCLICargoFixture(t)
	first, second := filepath.Join(f.home, "cargo-root"), filepath.Join(f.home, "cargo-other")
	for _, root := range []string{first, second} {
		writeCLIFile(t, filepath.Join(root, "inventory"), "ripgrep v14.1.1:\n    rg\n", 0o600)
	}
	writeCLIFile(t, filepath.Join(second, "bin", "rg"), "other installation\n", 0o700)
	f.env = append(f.env, "DIU_CARGO_CONFIGURED_ROOT="+second)
	writeCLIFile(t, filepath.Join(f.bin, "cargo"), cargoRootSwitchScript, 0o700)
	f.setup(t)
	f.env = append(f.env, "CARGO_INSTALL_ROOT=", "PATH="+f.basePath())
	return f
}

const cargoRootSwitchScript = `#!/bin/sh
set -eu
root=${CARGO_INSTALL_ROOT:-$DIU_CARGO_CONFIGURED_ROOT}
case "$1" in
    install) /bin/cat "$root/inventory" ;;
    uninstall)
        printf '%s\n' "$@" > "$HOME/cargo-uninstall-args"
        /bin/rm "$3/bin/rg"
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
