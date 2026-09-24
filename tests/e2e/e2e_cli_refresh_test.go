//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCLIDisabledRefreshPreservesWrappersUntilExplicitSetup(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	disableCLIWrappers(t, f)
	before := snapshotCLIHome(t, f.wrappers)
	shells := cliShellContents(t, f)
	if err := os.Remove(filepath.Join(f.managed, "probe")); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		assertCLIExit(t, f.cli(t, "scan", "--refresh-wrappers"), 0)
		assertCLIWrapperState(t, f, before, shells)
	}
	f.setup(t)
	assertCLICleanShells(t, f)
	assertCLIMissing(t, filepath.Join(f.wrappers, "probe"))
	assertCLIMissing(t, filepath.Join(f.wrappers, "brew"))
}

func disableCLIWrappers(t *testing.T, f *cliFixture) {
	t.Helper()
	assertCLIExit(t, f.cli(t, "config", "set", "monitoring.process.auto_install_wrappers", "false"), 0)
}

func cliShellContents(t *testing.T, f *cliFixture) map[string]cliFileState {
	t.Helper()
	files := snapshotCLIHome(t, f.home)
	shells := make(map[string]cliFileState)
	for _, name := range shellFiles {
		shells[name] = files[name]
	}
	return shells
}

func assertCLIWrapperState(t *testing.T, f *cliFixture, wrappers, shells map[string]cliFileState) {
	t.Helper()
	if !reflect.DeepEqual(snapshotCLIHome(t, f.wrappers), wrappers) {
		t.Fatal("disabled refresh changed the wrapper directory")
	}
	if !reflect.DeepEqual(cliShellContents(t, f), shells) {
		t.Fatal("disabled refresh changed shell configuration")
	}
}

func TestCLIDaemonRefreshDoesNotRemoveDisabledWrappers(t *testing.T) {
	f := newCLIFixture(t)
	filesystem := &f.config.Monitoring.Filesystem
	filesystem.ScanInterval = 40 * time.Millisecond
	writeCLIConfig(t, f)
	f.setup(t)
	writeRefreshWitness(t, f)
	wrappers, shells := snapshotCLIHome(t, f.wrappers), cliShellContents(t, f)
	d := startCLIDaemon(t, f)
	disableCLIWrappers(t, f)
	awaitCLI(t, func() bool { return disabledCLIRefreshCount(f) >= 3 })
	assertCLIWrapperState(t, f, wrappers, shells)
	assertCLIExit(t, f.cli(t, "daemon", "stop"), 0)
	assertCLIDaemonStopped(t, d)
}

func writeRefreshWitness(t *testing.T, f *cliFixture) {
	t.Helper()
	witness := `if /bin/grep -q '"auto_install_wrappers": false' "$HOME/.config/diu/config.json"; then
    printf 'disabled scan\n' >> "$HOME/disabled-scans"
fi
`
	script := strings.Replace(brewFixtureScript, "list) exit", "list) "+witness+"exit", 1)
	writeCLIFile(t, filepath.Join(f.bin, "brew"), script, 0o700)
}

func disabledCLIRefreshCount(f *cliFixture) int {
	data, _ := os.ReadFile(filepath.Join(f.home, "disabled-scans"))
	return strings.Count(string(data), "disabled scan\n")
}
