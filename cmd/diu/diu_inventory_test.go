package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func TestScanPackagesDiscoversExecutableWrappers(t *testing.T) {
	config := setupTestHomeConfig(t)
	binDir := configureExecutableWrapperScan(t, config)

	output := captureStderr(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})
	if !strings.Contains(output, "1 packages scanned") {
		t.Fatalf("Unexpected scan output: %q", output)
	}
	assertScannedWrapperPackage(t, config, binDir)
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

func TestPackageScannerDeduplicatesGoMonitorAndWrapperEntries(t *testing.T) {
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
