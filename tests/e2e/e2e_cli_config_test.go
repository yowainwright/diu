//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCLIUninstallWithoutConfigLeavesFilesystemUnchanged(t *testing.T) {
	f := newCLIFixture(t)
	removeCLIConfig(t, f)
	before := snapshotCLIHome(t, f.home)
	for range 2 {
		assertCLIExit(t, f.cli(t, "uninstall"), 0)
		if !reflect.DeepEqual(snapshotCLIHome(t, f.home), before) {
			t.Fatal("uninstall created or changed files without an existing config")
		}
	}
}

func TestCLIUninstallCleansOrphansWithoutRecreatingConfig(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	removeCLIConfig(t, f)
	assertCLIExit(t, f.cli(t, "uninstall"), 0)
	assertCLIMissing(t, filepath.Join(f.home, ".config", "diu"))
	assertCLICleanShells(t, f)
	assertCLIMissing(t, filepath.Join(f.wrappers, "probe"))
	assertCLIMissing(t, filepath.Join(f.wrappers, "brew"))
}

func removeCLIConfig(t *testing.T, f *cliFixture) {
	t.Helper()
	dir := filepath.Join(f.home, ".config", "diu")
	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
}
