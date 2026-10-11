//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/storage"
)

func TestCLIUninstallRemovesZshListenerAndPreservesHistory(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	assertCLIExit(t, submitCLIBackgroundLaunch(t, f), 0)
	waitCLIRecords(t, f, 1)
	historyPath := storage.ExecutionLogPath(f.config.Storage.JSONFile)
	history := readCLIFile(t, historyPath)
	writeCLIFile(t, filepath.Join(f.home, "personal.txt"), "retain this\n", 0o600)

	for range 2 {
		assertCLIExit(t, f.cli(t, "uninstall"), 0)
		assertCLICleanShells(t, f)
		assertCLIMissing(t, filepath.Join(f.config.Daemon.DataDir, "shell-integration.zsh"))
		assertCLIFile(t, historyPath, history)
		assertCLIFile(t, filepath.Join(f.home, "personal.txt"), "retain this\n")
		assertCLIExit(t, f.cli(t, "scan"), 0)
		assertCLIMissing(t, filepath.Join(f.config.Daemon.DataDir, "shell-integration.zsh"))
	}
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
	assertCLICleanShells(t, f)
	assertCLIMissing(t, filepath.Join(f.config.Daemon.DataDir, "shell-integration.zsh"))
}

func TestCLIUninstallPreservesUnownedIntegrationFile(t *testing.T) {
	f := newCLIFixture(t)
	integration := filepath.Join(f.config.Daemon.DataDir, "shell-integration.zsh")
	writeCLIFile(t, integration, "user-owned\n", 0o600)
	assertCLIExit(t, f.cli(t, "uninstall"), 0)
	assertCLIFile(t, integration, "user-owned\n")
	if _, err := os.Stat(filepath.Join(f.home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
}
