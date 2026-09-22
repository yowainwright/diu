package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestCleanupContinuesAfterRecorderFailure(t *testing.T) {
	fixture := newUninstallFixture(t)
	config := fixture.config
	config.Monitoring.Process.ShouldAutoInstallWrappers = true
	paths := cleanupPathsForTest(t, fixture.config)
	stopErr := errors.New("recorder shutdown failed")
	err := removeSetupArtifacts(paths, func() error {
		assertWrapperInstallationDisabled(t)
		return stopErr
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("cleanup error = %v, want shutdown failure", err)
	}
	assertUninstallArtifacts(t, fixture)
}

func cleanupPathsForTest(t *testing.T, config *core.Config) uninstallPaths {
	t.Helper()
	if err := config.Save(); err != nil {
		t.Fatal(err)
	}
	paths, err := loadUninstallPaths()
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func assertWrapperInstallationDisabled(t *testing.T) {
	t.Helper()
	config, err := core.LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if config.Monitoring.Process.ShouldAutoInstallWrappers {
		t.Fatal("cleanup left automatic wrapper installation enabled")
	}
}

func TestCleanupContinuesAfterWrapperFailure(t *testing.T) {
	fixture := newUninstallFixture(t)
	config := fixture.config
	cache := filepath.Join(config.Monitoring.Process.WrapperDir, ".diu-delegates")
	if err := os.Symlink(t.TempDir(), cache); err != nil {
		t.Fatal(err)
	}
	paths := cleanupPathsForTest(t, fixture.config)
	err := removeSetupArtifacts(paths, func() error { return nil })
	if err == nil {
		t.Fatal("expected error for symlinked delegation cache")
	}
	assertUninstallArtifacts(t, fixture)
}

func TestCleanupContinuesAfterShellFailure(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := os.Mkdir(filepath.Join(os.Getenv("HOME"), ".bashrc"), 0o700); err != nil {
		t.Fatal(err)
	}
	paths := cleanupPathsForTest(t, fixture.config)
	err := removeSetupArtifacts(paths, func() error { return nil })
	if err == nil {
		t.Fatal("expected error for unreadable shell configuration")
	}
	assertUninstallArtifacts(t, fixture)
}

func TestCleanupRemovesDelegatesAndIsRepeatable(t *testing.T) {
	fixture := newUninstallFixture(t)
	cache := writeDelegateFixture(t, fixture.config)
	paths := cleanupPathsForTest(t, fixture.config)
	for range 2 {
		if err := removeSetupArtifacts(paths, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	assertFileMissing(t, cache)
	assertUninstallArtifacts(t, fixture)
}

func writeDelegateFixture(t *testing.T, config *core.Config) string {
	t.Helper()
	cache := filepath.Join(config.Monitoring.Process.WrapperDir, ".diu-delegates")
	dir := filepath.Join(cache, strings.Repeat("a", 64))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutableForTest(t, filepath.Join(dir, "jq"), "#!/bin/bash\n# DIU delegated command\nexec /usr/bin/true \"$@\"\n")
	return cache
}

func TestCleanupPreservesUnrelatedDelegateFilesAndSymlinks(t *testing.T) {
	fixture := newUninstallFixture(t)
	cache := writeDelegateFixture(t, fixture.config)
	dir := filepath.Join(cache, strings.Repeat("a", 64))
	custom := filepath.Join(dir, "custom")
	writeExecutableForTest(t, custom, unrelatedWrapperFixture)
	link := filepath.Join(cache, strings.Repeat("b", 64))
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	config := fixture.config
	if err := removeGeneratedWrappers(config.Monitoring.Process.WrapperDir); err != nil {
		t.Fatal(err)
	}
	assertFileMissing(t, filepath.Join(dir, "jq"))
	assertFileContent(t, custom, unrelatedWrapperFixture)
	assertFileExists(t, link)
}

func TestDisabledWrapperConfigurationRemovesExistingSetup(t *testing.T) {
	fixture := newUninstallFixture(t)
	if err := configureCommandWrappers(fixture.config, nil); err != nil {
		t.Fatal(err)
	}
	assertUninstallArtifacts(t, fixture)
}

func TestExecutableWrapperInstallationHonorsDisabledSetting(t *testing.T) {
	config := setupTestHomeConfig(t)
	if err := installExecutableWrappers(config); err != nil {
		t.Fatal(err)
	}
	assertFileMissing(t, config.Monitoring.Process.WrapperDir)
}

func TestWrapperUpgradeReplacesLegacyScript(t *testing.T) {
	fixture := newUninstallFixture(t)
	writeExecutableForTest(t, fixture.wrapperPath, legacyWrapperFixture)
	target := executableWrapper{Name: "jq", OriginalPath: "/usr/bin/true", Tool: core.ToolHomebrew, Package: "jq"}
	if err := writeExecutableWrapper(fixture.config, target); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, fixture.wrapperPath, executableWrapperScript(fixture.config, target))
}

func TestWrapperInstallationConfigCanBeReenabled(t *testing.T) {
	config := setupTestHomeConfig(t)
	key := "monitoring.process.auto_install_wrappers"
	for _, value := range []string{"true", "false"} {
		if err := updateConfigValue(config, key, value); err != nil {
			t.Fatal(err)
		}
		got, ok := readConfigValue(config, key)
		want := value == "true"
		matches := ok && got == want
		if !matches {
			t.Fatalf("wrapper configuration = %v, want %s", got, value)
		}
	}
	if err := updateConfigValue(config, key, "invalid"); err == nil {
		t.Fatal("accepted invalid wrapper configuration")
	}
}

func TestExecutableWrapperRecordingIsDetached(t *testing.T) {
	config := setupTestHomeConfig(t)
	target := executableWrapper{Name: "jq", OriginalPath: "/usr/bin/true"}
	script := executableWrapperScript(config, target)
	for _, expected := range []string{"} </dev/null >/dev/null 2>&1 &", `DIU_RECORDING=1 "$DIU_RECORD_BINARY" record`, "START_TIME=$(/bin/date", "payload=$(/bin/cat"} {
		if !strings.Contains(script, expected) {
			t.Errorf("wrapper missing %q", expected)
		}
	}
}
