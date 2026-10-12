package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/storage"
)

func TestScanPackagesDiscoversExecutables(t *testing.T) {
	config := setupTestHomeConfig(t)
	binDir := configureExecutableInventoryScan(t, config)

	output := captureStderr(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})
	if !strings.Contains(output, "1 packages scanned") {
		t.Fatalf("Unexpected scan output: %q", output)
	}
	assertScannedExecutablePackage(t, config, binDir)
}

func TestInventoryScopesRespectManagerCoverage(t *testing.T) {
	for _, test := range inventoryScopeCases() {
		config := core.DefaultConfig()
		config.Tools.Homebrew.ShouldTrackCasks = test.shouldTrackCasks
		config.Tools.NPM.ShouldTrackGlobalOnly = test.shouldTrackGlobalOnly
		if got := inventoryScopes(test.tool, config); !slices.Equal(got, test.want) {
			t.Fatalf("inventoryScopes(%s) = %v, want %v", test.tool, got, test.want)
		}
	}
}

type inventoryScopeCase struct {
	tool                  string
	shouldTrackCasks      bool
	shouldTrackGlobalOnly bool
	want                  []string
}

func inventoryScopeCases() []inventoryScopeCase {
	return []inventoryScopeCase{
		{core.ToolHomebrew, false, true, []string{core.ToolHomebrew}},
		{core.ToolHomebrew, true, true, []string{core.ToolHomebrew, homebrewCaskTool}},
		{core.ToolNPM, false, true, []string{core.ToolNPM}},
		{core.ToolNPM, false, false, nil},
		{core.ToolPoetry, false, true, nil},
		{core.ToolGo, false, true, []string{core.ToolGoBinary, core.ToolGo}},
		{core.ToolUV, false, true, []string{core.ToolUV}},
	}
}

func TestCommitPackageScanReconcilesOnlyCompletedScopes(t *testing.T) {
	store := newInventoryTestStore(t)
	updateTestPackage(t, store, &core.PackageInfo{Name: "gone", Tool: core.ToolHomebrew})
	updateTestPackage(t, store, &core.PackageInfo{Name: "keep", Tool: core.ToolNPM})
	scan := newInventoryScan()
	scan.complete([]string{core.ToolHomebrew})
	if err := commitPackageScan(store, nil, scan); err != nil {
		t.Fatal(err)
	}
	packages, err := store.AllPackages()
	if err != nil {
		t.Fatal(err)
	}
	hasReconciledPackages := len(packages[core.ToolHomebrew]) == 0 &&
		packages[core.ToolNPM]["keep"] != nil
	if !hasReconciledPackages {
		t.Fatalf("scan removed packages outside completed scopes: %#v", packages)
	}
}

func newInventoryTestStore(t *testing.T) *storage.JSONStorage {
	t.Helper()
	config := setupTestHomeConfig(t)
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestStore(t, store) })
	return store
}

func TestCommitPackageScanReportsStorageFailure(t *testing.T) {
	config := setupTestHomeConfig(t)
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestStore(t, store)
	if err := os.WriteFile(config.Storage.JSONFile, []byte("invalid JSON"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	err = commitPackageScan(store, nil, newInventoryScan())
	hasStorageFailure := err != nil && strings.Contains(err.Error(), "failed to apply package scan")
	if !hasStorageFailure {
		t.Fatalf("scan error = %v, want storage failure", err)
	}
}

func TestScanPackagesDoesNotInstallZshIntegration(t *testing.T) {
	config := setupTestHomeConfig(t)
	captureStderr(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatal(err)
		}
	})
	assertShellIntegrationMissing(t, zshIntegrationPath(config))
}

func TestMergeExistingPackageMigratesLegacyGoUsage(t *testing.T) {
	legacy, lastUsed := legacyGoPackageFixture()
	inventory := legacyGoInventory(legacy)
	pkg := &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary}

	mergeExistingPackage(inventory, pkg)
	assertLegacyGoUsageMigrated(t, pkg, legacy, lastUsed)
	assertGoInventoryScopes(t)
}

func TestMergeExistingPackageCombinesLegacyAndCurrentGoUsage(t *testing.T) {
	legacyUse := time.Now().Add(-time.Hour)
	currentUse := time.Now()
	inventory := map[string]map[string]*core.PackageInfo{
		core.ToolGo:       {"gopls": {Name: "gopls", UsageCount: 4, LastUsed: legacyUse}},
		core.ToolGoBinary: {"gopls": {Name: "gopls", UsageCount: 3, LastUsed: currentUse}},
	}
	pkg := &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary}

	mergeExistingPackage(inventory, pkg)
	if pkg.UsageCount != 7 {
		t.Fatalf("usage count = %d, want 7", pkg.UsageCount)
	}
	if !pkg.LastUsed.Equal(currentUse) {
		t.Fatalf("last used = %s, want %s", pkg.LastUsed, currentUse)
	}
}

func TestPackageScannerDeduplicatesGoInventoryEntries(t *testing.T) {
	scanner := &packageScanner{
		scan:            newInventoryScan(),
		existing:        legacyAndCurrentGoInventory(),
		scannedPackages: make(map[string]*core.PackageInfo),
	}
	scanner.addPackage(&core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary})
	scanner.addPackage(&core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, Path: "/go/bin/gopls"})

	if len(scanner.packages) != 1 {
		t.Fatalf("packages = %d, want 1", len(scanner.packages))
	}
	if scanner.packages[0].UsageCount != 7 {
		t.Fatalf("usage count = %d, want 7", scanner.packages[0].UsageCount)
	}
}

func TestMergeExistingPackageReusesUnchangedGoFingerprint(t *testing.T) {
	existing := &core.PackageInfo{
		Name: "gopls", Tool: core.ToolGoBinary, Path: "/go/bin/gopls",
		Fingerprint: "sha256", SizeBytes: 42, ModifiedAt: 123,
	}
	inventory := map[string]map[string]*core.PackageInfo{core.ToolGoBinary: {"gopls": existing}}
	pkg := &core.PackageInfo{
		Name: "gopls", Tool: core.ToolGoBinary, Path: existing.Path,
		SizeBytes: existing.SizeBytes, ModifiedAt: existing.ModifiedAt,
	}

	mergeExistingPackage(inventory, pkg)
	if pkg.Fingerprint != existing.Fingerprint {
		t.Fatalf("fingerprint = %q, want %q", pkg.Fingerprint, existing.Fingerprint)
	}
}

func TestPopulateGoBinaryFingerprintCachesFileSignature(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gopls")
	writeExecutableForTest(t, path, "#!/bin/sh\nexit 0\n")
	pkg := &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, Path: path}
	if err := populateGoBinaryFingerprint(pkg); err != nil {
		t.Fatalf("populateGoBinaryFingerprint failed: %v", err)
	}
	assertGoBinaryFingerprintSignature(t, pkg)
	assertGoBinaryFingerprintCached(t, pkg)
}

func TestPopulateGoBinaryFingerprintRejectsMissingBinary(t *testing.T) {
	pkg := &core.PackageInfo{Name: "missing", Tool: core.ToolGoBinary, Path: filepath.Join(t.TempDir(), "missing")}
	if err := populateGoBinaryFingerprint(pkg); err == nil {
		t.Fatal("missing Go binary was fingerprinted")
	}
}

func TestPackageScannerReportsGoFingerprintFailure(t *testing.T) {
	var warnings bytes.Buffer
	activity := dx.NewOut(io.Discard, &warnings, nil).StartActivity("scan")
	defer activity.Stop()
	scanner := &packageScanner{activity: activity}
	pkg := &core.PackageInfo{Tool: core.ToolGoBinary, Path: filepath.Join(t.TempDir(), "missing"), ModifiedAt: 123}
	scanner.prepareGoFingerprint(pkg)
	if pkg.Fingerprint != "" {
		t.Fatalf("missing binary received fingerprint %q", pkg.Fingerprint)
	}
	if !strings.Contains(warnings.String(), "failed to fingerprint Go binary") {
		t.Fatalf("missing fingerprint warning: %q", warnings.String())
	}
}

func TestInventoryScopesSkipIncompleteNPMScan(t *testing.T) {
	config := core.DefaultConfig()
	config.Tools.NPM.ShouldTrackGlobalOnly = false
	if scopes := inventoryScopes(core.ToolNPM, config); scopes != nil {
		t.Fatalf("npm inventory scopes = %#v", scopes)
	}
}

func TestScanPackagesAdditionalManagers(t *testing.T) {
	config := setupTestHomeConfig(t)
	configureAdditionalManagerScan(t, config)

	output := captureStderr(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})
	if !strings.Contains(output, "packages scanned") {
		t.Fatalf("Unexpected scan output: %q", output)
	}
	assertAdditionalManagerPackages(t, config)
}

func TestScanPackages(t *testing.T) {
	prepareScanPackagesConfig(t)

	output := captureStderr(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})

	if !strings.Contains(output, "packages scanned") {
		t.Fatalf("Expected 'packages scanned' message, got: %q", output)
	}
}

func prepareScanPackagesConfig(t *testing.T) {
	t.Helper()

	config := setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())
	binDir := filepath.Join(t.TempDir(), "bin")
	createBinDir(t, binDir)
	writeExecutableForTest(t, filepath.Join(binDir, "jq"), "#!/bin/bash\nexit 0\n")
	saveScanPackagesConfig(t, config, binDir)
}

func createBinDir(t *testing.T, binDir string) {
	t.Helper()

	if err := os.MkdirAll(binDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create bin dir: %v", err)
	}
}

func saveScanPackagesConfig(t *testing.T, config *core.Config, binDir string) {
	t.Helper()

	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolHomebrew: {binDir},
	}
	config.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
}

func TestScanPackagesNoEnabledTools(t *testing.T) {
	setupTestHomeConfig(t)
	out := captureStderr(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})
	if !strings.Contains(out, "packages scanned") {
		t.Fatalf("expected 'packages scanned' message, got: %q", out)
	}
}

func configureExecutableInventoryScan(t *testing.T, config *core.Config) string {
	t.Helper()

	t.Setenv("PATH", t.TempDir())

	binDir := executableInventoryScanBinDir(t)
	saveExecutableInventoryScanConfig(t, config, binDir)
	return binDir
}

func executableInventoryScanBinDir(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	writeExecutableForTest(t, filepath.Join(binDir, "jq"), "#!/bin/bash\nexit 0\n")
	if err := os.WriteFile(filepath.Join(binDir, "notes"), []byte("not executable"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write non-executable: %v", err)
	}
	writeExecutableForTest(t, filepath.Join(binDir, "brew"), "#!/bin/bash\nexit 0\n")
	return binDir
}

func saveExecutableInventoryScanConfig(t *testing.T, config *core.Config, binDir string) {
	t.Helper()

	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolHomebrew: {binDir},
	}
	config.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
}

func assertScannedExecutablePackage(t *testing.T, config *core.Config, binDir string) {
	t.Helper()

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	pkg, err := store.GetPackage(core.ToolHomebrew, "jq")
	if err != nil {
		t.Fatalf("Expected scanned jq package: %v", err)
	}
	if pkg.Path != filepath.Join(binDir, "jq") {
		t.Fatalf("Package path = %s, want scanned executable path", pkg.Path)
	}
}

func legacyGoPackageFixture() (*core.PackageInfo, time.Time) {
	lastUsed := time.Now().Add(-time.Hour)
	return &core.PackageInfo{
		Name:       "gopls",
		Tool:       core.ToolGo,
		LastUsed:   lastUsed,
		UsageCount: 12,
	}, lastUsed
}

func legacyGoInventory(legacy *core.PackageInfo) map[string]map[string]*core.PackageInfo {
	return map[string]map[string]*core.PackageInfo{
		core.ToolGo: {"gopls": legacy},
	}
}

func assertLegacyGoUsageMigrated(t *testing.T, pkg, legacy *core.PackageInfo, lastUsed time.Time) {
	t.Helper()

	legacyUsageMigrated := pkg.UsageCount == legacy.UsageCount
	legacyTimeMigrated := pkg.LastUsed.Equal(lastUsed)
	legacyMetadataMigrated := legacyUsageMigrated && legacyTimeMigrated
	if !legacyMetadataMigrated {
		t.Fatalf("migrated package = %#v", pkg)
	}
}

func assertGoInventoryScopes(t *testing.T) {
	t.Helper()

	scopes := inventoryScopes(core.ToolGo, core.DefaultConfig())
	hasGoScope := slices.Contains(scopes, core.ToolGo)
	hasGoBinaryScope := slices.Contains(scopes, core.ToolGoBinary)
	hasGoScopes := hasGoScope && hasGoBinaryScope
	if !hasGoScopes {
		t.Fatalf("Go inventory scopes = %#v", scopes)
	}
}

func assertGoBinaryFingerprintSignature(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	hasFingerprint := pkg.Fingerprint != ""
	hasSize := pkg.SizeBytes != 0
	hasModifiedAt := pkg.ModifiedAt != 0
	hasSignature := hasFingerprint && hasSize && hasModifiedAt
	if !hasSignature {
		t.Fatalf("fingerprinted package = %#v", pkg)
	}
}

func assertGoBinaryFingerprintCached(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	originalFingerprint := pkg.Fingerprint
	if err := populateGoBinaryFingerprint(pkg); err != nil {
		t.Fatalf("cached populateGoBinaryFingerprint failed: %v", err)
	}
	if pkg.Fingerprint != originalFingerprint {
		t.Fatalf("fingerprint changed: %q", pkg.Fingerprint)
	}
}

func legacyAndCurrentGoInventory() map[string]map[string]*core.PackageInfo {
	return map[string]map[string]*core.PackageInfo{
		core.ToolGo:       {"gopls": {Name: "gopls", UsageCount: 4}},
		core.ToolGoBinary: {"gopls": {Name: "gopls", UsageCount: 3}},
	}
}

func configureAdditionalManagerScan(t *testing.T, config *core.Config) {
	t.Helper()

	t.Setenv("PATH", t.TempDir())
	restoreExecutablePathDeps(t)
	prependAdditionalManagerCommands(t)
	config.Monitoring.EnabledTools = []string{core.ToolPNPM, core.ToolBun, core.ToolPip, core.ToolUV, core.ToolPoetry}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{}
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
}

func prependAdditionalManagerCommands(t *testing.T) {
	t.Helper()

	prependFakeCommand(t, pnpmCommandName, fakePNPMScanScript)
	prependFakeCommand(t, bunCommandName, fakeBunScanScript)
	prependFakeCommand(t, "pip3", fakePipScanScript)
	prependFakeCommand(t, uvCommandName, fakeUVScanScript)
	prependFakeCommand(t, "poetry", "#!/bin/sh\nexit 0\n")
}

const fakePNPMScanScript = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ] && [ "$4" = "--json" ]; then
  printf '[{"dependencies":{"tsx":{"version":"4.19.0"}}}]\n'
  exit 0
fi
exit 2
`

const fakeBunScanScript = `#!/bin/sh
if [ "$1" = "pm" ] && [ "$2" = "ls" ] && [ "$3" = "-g" ] && [ "$4" = "--json" ]; then
  printf '{"dependencies":{"prettier":{"version":"3.3.0"}}}\n'
  exit 0
fi
exit 2
`

const fakePipScanScript = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then
  printf '[{"name":"requests","version":"2.32.0"}]\n'
  exit 0
fi
exit 2
`

const fakeUVScanScript = `#!/bin/sh
if [ "$1" = "tool" ] && [ "$2" = "list" ]; then
  printf 'ruff 0.5.0\n'
  exit 0
fi
exit 2
`

func assertAdditionalManagerPackages(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	for _, want := range []struct {
		tool string
		name string
	}{
		{core.ToolPNPM, "tsx"},
		{core.ToolBun, "prettier"},
		{core.ToolPip, "requests"},
		{core.ToolUV, "ruff"},
	} {
		if _, err := store.GetPackage(want.tool, want.name); err != nil {
			t.Fatalf("Expected scanned package %s/%s: %v", want.tool, want.name, err)
		}
	}
}

var configuredMonitorTools = []string{
	core.ToolHomebrew,
	core.ToolNPM,
	core.ToolPNPM,
	core.ToolBun,
	core.ToolGo,
	core.ToolPip,
	core.ToolUV,
	core.ToolPoetry,
}
