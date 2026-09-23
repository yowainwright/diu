//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestCLIUninstallRestoresShellsAndPreservesHistory(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	assertCLICommandContract(t, f, "bash", "probe")
	waitCLIRecords(t, f, 1)
	historyPath := filepath.Join(f.config.Daemon.DataDir, "executions.ndjson")
	history := readCLIFile(t, historyPath)
	writeCLIFile(t, filepath.Join(f.wrappers, "personal"), "keep this\n", 0o700)
	for range 2 {
		assertCLIExit(t, f.cli(t, "uninstall"), 0)
		assertCLICleanup(t, f)
		assertCLIFile(t, historyPath, history)
		assertCLIFile(t, filepath.Join(f.wrappers, "personal"), "keep this\n")
	}
	assertCLIUnwrappedCommandContract(t, f)
}

func assertCLICleanup(t *testing.T, f *cliFixture) {
	t.Helper()
	assertCLICleanShells(t, f)
	assertCLIMissing(t, filepath.Join(f.wrappers, "probe"))
	assertCLIMissing(t, filepath.Join(f.wrappers, "brew"))
	assertCLIFile(t, filepath.Join(f.managed, "probe"), probeFixtureScript)
	assertCLIFile(t, filepath.Join(f.home, "personal.txt"), "must survive\n")
	setting := f.cli(t, "config", "get", "monitoring.process.auto_install_wrappers")
	assertCLIExit(t, setting, 0)
	if strings.TrimSpace(setting.stdout) != "false" {
		t.Fatalf("uninstall left wrapper installation enabled: %#v", setting)
	}
}

func assertCLICleanShells(t *testing.T, f *cliFixture) {
	t.Helper()
	for _, name := range shellFiles {
		path := filepath.Join(f.home, name)
		assertCLIFile(t, path, shellSentinel)
		info, err := os.Stat(path)
		hasWrongMode := err != nil || info.Mode().Perm() != 0o640
		if hasWrongMode {
			t.Fatalf("cleanup changed shell permissions: %s: %v, %v", name, info, err)
		}
	}
	assertCLIFile(t, filepath.Join(f.home, ".zprofile"), shellSentinel)
}

func TestCLIUninstallRemovesRealDelegationCache(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	preferred := filepath.Join(f.home, "preferred")
	writeCLIFile(t, filepath.Join(preferred, "probe"), "#!/bin/sh\nprintf 'preferred\\n'\n", 0o700)
	f.env = append(f.env, "PATH="+preferred+":"+f.basePath())
	assertCLIExit(t, runCLICommand(t, f.shell(t, "bash", "probe"), ""), 0)
	assertCLIDelegates(t, f)
	assertCLIExit(t, f.cli(t, "uninstall"), 0)
	assertCLIMissing(t, filepath.Join(f.wrappers, ".diu-delegates"))
	assertCLICleanup(t, f)
}

func TestCLIUninstallDisablesFutureSetupAndRefresh(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	assertCLIExit(t, f.cli(t, "uninstall"), 0)
	assertCLIExit(t, f.cli(t, "scan", "--refresh-wrappers"), 0)
	f.setup(t)
	assertCLICleanup(t, f)
}

func TestCLIUninstallContinuesAfterShellReadFailure(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	broken := filepath.Join(f.home, ".bashrc")
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(broken, 0o700); err != nil {
		t.Fatal(err)
	}
	result := f.cli(t, "uninstall")
	assertCLIExit(t, result, 1)
	assertCLIFile(t, filepath.Join(f.home, ".zshrc"), shellSentinel)
	assertCLIFile(t, filepath.Join(f.home, ".config/fish/config.fish"), shellSentinel)
	assertCLIMissing(t, filepath.Join(f.wrappers, "probe"))
}

func TestCLIUninstallContinuesAfterRecorderStateFailure(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	writeCLIFile(t, f.config.Daemon.PIDFile, "invalid-pid\n", 0o600)
	result := f.cli(t, "uninstall")
	assertCLIExit(t, result, 1)
	if !strings.Contains(result.stderr, "PID") {
		t.Fatalf("missing actionable recorder error: %#v", result)
	}
	assertCLICleanup(t, f)
}

func TestCLIUninstallPreservesSymlinkTargetsAndPersonalWrappers(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	outside := filepath.Join(f.home, "outside")
	writeCLIFile(t, outside, "retain target\n", 0o700)
	if err := os.Symlink(outside, filepath.Join(f.wrappers, "personal-link")); err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, filepath.Join(f.wrappers, "personal"), "#!/bin/bash\nprintf keep\n", 0o700)
	assertCLIExit(t, f.cli(t, "uninstall"), 0)
	assertCLIFile(t, outside, "retain target\n")
	assertCLIFile(t, filepath.Join(f.wrappers, "personal-link"), "retain target\n")
	assertCLIFile(t, filepath.Join(f.wrappers, "personal"), "#!/bin/bash\nprintf keep\n")
}

func TestCLIUninstallRemovesLegacyFishPathBlock(t *testing.T) {
	f := newCLIFixture(t)
	f.wrappers = filepath.Join(f.home, "wrappers `legacy`")
	process := &f.config.Monitoring.Process
	process.WrapperDir = f.wrappers
	writeCLIConfig(t, f)
	f.setup(t)
	quoted := core.ShellEscapeString(f.wrappers)
	line := fmt.Sprintf("if not contains \"%s\" $PATH\n    set -gx PATH \"%s\" $PATH\nend", quoted, quoted)
	content := shellSentinel + "\n# DIU path configuration\n" + line + "\n"
	writeCLIFile(t, filepath.Join(f.home, ".config/fish/config.fish"), content, 0o640)
	assertCLIExit(t, f.cli(t, "uninstall"), 0)
	assertCLICleanup(t, f)
}
