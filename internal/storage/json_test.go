package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func closeStorage(t *testing.T, store Storage) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Failed to close storage: %v", err)
	}
}

func addExecution(t *testing.T, store Storage, record *core.ExecutionRecord) {
	t.Helper()
	if err := store.AddExecution(record); err != nil {
		t.Fatalf("Failed to add execution: %v", err)
	}
}

func updatePackage(t *testing.T, store Storage, pkg *core.PackageInfo) {
	t.Helper()
	if err := store.UpdatePackage(pkg); err != nil {
		t.Fatalf("Failed to update package: %v", err)
	}
}

func newTestStorage(t *testing.T) *JSONStorage {
	t.Helper()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(t.TempDir(), "test.json"),
		},
	}
	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	return storage
}

func TestJSONStorage(t *testing.T) {
	const storageFileName = "test.json"

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:      filepath.Join(tempDir, storageFileName),
			RetentionDays: 30,
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	// Test file creation
	if _, err := os.Stat(config.Storage.JSONFile); os.IsNotExist(err) {
		t.Error("Storage file was not created")
	}

	info, err := os.Stat(ExecutionLogPath(config.Storage.JSONFile))
	if err != nil {
		t.Fatalf("Failed to stat storage file: %v", err)
	}
	if got := info.Mode().Perm(); got != core.PrivateFileMode {
		t.Errorf("Storage file mode = %v, want %v", got, core.PrivateFileMode)
	}
}

func TestJSONStorageSupportsNDJSONManifestPath(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "custom.ndjson")
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: manifestPath},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)
	if store.executionPath == store.filepath {
		t.Fatalf("execution log path collided with manifest path: %s", store.executionPath)
	}
	if got, want := store.executionPath, filepath.Join(filepath.Dir(manifestPath), "custom.executions.ndjson"); got != want {
		t.Fatalf("execution log path = %s, want %s", got, want)
	}

	addExecution(t, store, &core.ExecutionRecord{Tool: core.ToolNPM, Timestamp: time.Now()})
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil || len(executions) != 1 {
		t.Fatalf("executions = %d, %v", len(executions), err)
	}
}

func TestNewJSONStorageRejectsEmptyPath(t *testing.T) {
	config := &core.Config{Storage: core.StorageConfig{JSONFile: " "}}
	if _, err := NewJSONStorage(config); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("NewJSONStorage error = %v", err)
	}
}

func TestAddExecution(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	record := &core.ExecutionRecord{
		Tool:             "test",
		Command:          "test command",
		Args:             []string{"arg1", "arg2"},
		Timestamp:        time.Now(),
		Duration:         5 * time.Second,
		ExitCode:         0,
		WorkingDir:       "/tmp",
		User:             "testuser",
		PackagesAffected: []string{"package1"},
	}

	err = storage.AddExecution(record)
	if err != nil {
		t.Fatalf("Failed to add execution: %v", err)
	}

	// Verify execution was added
	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}

	if len(executions) != 1 {
		t.Errorf("Expected 1 execution, got %d", len(executions))
	}

	if executions[0].Tool != "test" {
		t.Errorf("Expected tool 'test', got %s", executions[0].Tool)
	}
}

func TestAddExecutionsStoresBatch(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	records := []*core.ExecutionRecord{
		{Tool: core.ToolHomebrew, Timestamp: time.Now()},
		{Tool: core.ToolGo, Timestamp: time.Now()},
	}

	if err := store.AddExecutions(records); err != nil {
		t.Fatalf("AddExecutions failed: %v", err)
	}
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	if len(executions) != len(records) {
		t.Fatalf("execution count = %d, want %d", len(executions), len(records))
	}
}

func TestAddExecutionRejectsNilRecord(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	if err := store.AddExecution(nil); !errors.Is(err, ErrNilExecutionRecord) {
		t.Fatalf("AddExecution error = %v", err)
	}
}

func TestAddExecutionRejectsUnmarshalableMetadata(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	record := &core.ExecutionRecord{
		Tool: core.ToolNPM,
		Metadata: map[string]interface{}{
			"invalid": make(chan struct{}),
		},
	}
	if err := store.AddExecution(record); err == nil {
		t.Fatal("AddExecution succeeded with unmarshalable metadata")
	}
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil || len(executions) != 0 {
		t.Fatalf("executions after rejected append = %d, %v", len(executions), err)
	}
}

func TestAddExecutionDoesNotMutateStateWhenAppendFails(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	store.executionPath = t.TempDir()

	record := &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Timestamp:        time.Now(),
		PackagesAffected: []string{"eslint"},
	}
	if err := store.AddExecution(record); err == nil {
		t.Fatal("AddExecution succeeded with invalid execution log path")
	}
	stats, err := store.GetStatistics()
	if err != nil || stats.TotalExecutions != 0 {
		t.Fatalf("statistics after failed append = %#v, %v", stats, err)
	}
	if _, err := store.GetPackage(core.ToolNPM, "eslint"); err == nil {
		t.Fatal("package inventory changed after failed append")
	}
}

func TestUpdatePackageRejectsNilPackage(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	if err := store.UpdatePackage(nil); !errors.Is(err, ErrNilPackage) {
		t.Fatalf("UpdatePackage error = %v", err)
	}
}

func TestLocalJavaScriptExecutionDoesNotCreateGlobalPackage(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")},
		Tools:   core.ToolsConfig{NPM: core.NPMConfig{TrackGlobalOnly: true}},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)

	record := &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Timestamp:        time.Now(),
		PackagesAffected: []string{"eslint"},
		Metadata:         map[string]interface{}{"global": false},
	}
	addExecution(t, store, record)
	if _, err := store.GetPackage(core.ToolNPM, "eslint"); err == nil {
		t.Fatal("local npm execution created a global package entry")
	}
}

func TestGlobalJavaScriptExecutionCreatesPackage(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")},
		Tools:   core.ToolsConfig{NPM: core.NPMConfig{TrackGlobalOnly: true}},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)

	record := &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Timestamp:        time.Now(),
		PackagesAffected: []string{"typescript"},
		Metadata:         map[string]interface{}{"global": true},
	}
	addExecution(t, store, record)
	if _, err := store.GetPackage(core.ToolNPM, "typescript"); err != nil {
		t.Fatalf("global npm execution did not create package: %v", err)
	}
}

func TestUninstallExecutionRemovesPackage(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	updatePackage(t, store, &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew})
	record := &core.ExecutionRecord{
		Tool:             core.ToolHomebrew,
		Timestamp:        time.Now(),
		PackagesAffected: []string{"jq"},
		Metadata:         map[string]interface{}{"action": "uninstall"},
	}
	addExecution(t, store, record)
	if _, err := store.GetPackage(core.ToolHomebrew, "jq"); err == nil {
		t.Fatal("uninstall execution left the package in inventory")
	}
}

func TestGoExecutionDoesNotCreateModuleInventory(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	record := &core.ExecutionRecord{
		Tool:             core.ToolGo,
		Timestamp:        time.Now(),
		PackagesAffected: []string{"example.com/tool@latest"},
	}
	addExecution(t, store, record)
	if _, err := store.GetPackage(core.ToolGo, "example.com/tool@latest"); err == nil {
		t.Fatal("Go execution created a module inventory entry")
	}
}

func TestStorageReadsExternalWrites(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")},
	}
	first, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("first NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, first)
	second, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("second NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, second)

	record := &core.ExecutionRecord{Tool: core.ToolGo, Timestamp: time.Now()}
	addExecution(t, second, record)
	executions, err := first.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("stale execution count = %d, want 1", len(executions))
	}
}

func TestExistingStorageIsStreamedWithoutCachingHistory(t *testing.T) {
	config := &core.Config{Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")}}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	addExecution(t, store, &core.ExecutionRecord{Tool: core.ToolGo, Timestamp: time.Now()})
	closeStorage(t, store)

	reopened, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, reopened)
	if reopened.data != nil {
		t.Fatal("existing storage history was cached during initialization")
	}
	executions, err := reopened.GetExecutions(QueryOptions{Limit: 1})
	if err != nil || len(executions) != 1 {
		t.Fatalf("streamed executions = %d, %v", len(executions), err)
	}
}

func TestInitializeRecreatesMissingExecutionLogAsEmptyHistory(t *testing.T) {
	config := &core.Config{Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")}}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	addExecution(t, store, &core.ExecutionRecord{Tool: core.ToolGo, Timestamp: time.Now()})
	closeStorage(t, store)

	if err := os.Remove(ExecutionLogPath(config.Storage.JSONFile)); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	reopened, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, reopened)
	if _, err := os.Stat(ExecutionLogPath(config.Storage.JSONFile)); err != nil {
		t.Fatalf("execution log was not recreated: %v", err)
	}
	executions, err := reopened.GetExecutions(QueryOptions{})
	if err != nil || len(executions) != 0 {
		t.Fatalf("executions after missing log repair = %d, %v", len(executions), err)
	}
	stats, err := reopened.GetStatistics()
	if err != nil || stats.TotalExecutions != 0 {
		t.Fatalf("statistics after missing log repair = %#v, %v", stats, err)
	}
}

func TestReconcilePackagesRemovesMissingInventory(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	updatePackage(t, store, &core.PackageInfo{Name: "keep", Tool: core.ToolHomebrew})
	updatePackage(t, store, &core.PackageInfo{Name: "remove", Tool: core.ToolHomebrew})
	updatePackage(t, store, &core.PackageInfo{Name: "untouched", Tool: core.ToolNPM})

	seenHomebrew := map[string]struct{}{"keep": {}}
	scanned := map[string]map[string]struct{}{core.ToolHomebrew: seenHomebrew}
	if err := store.ReconcilePackages(scanned, time.Time{}); err != nil {
		t.Fatalf("ReconcilePackages failed: %v", err)
	}
	if _, err := store.GetPackage(core.ToolHomebrew, "remove"); err == nil {
		t.Fatal("missing Homebrew package was not removed")
	}
	if _, err := store.GetPackage(core.ToolNPM, "untouched"); err != nil {
		t.Fatalf("unscanned npm package was removed: %v", err)
	}
}

func TestReconcilePackagesPreservesPackagesUpdatedDuringScan(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	scanStartedAt := time.Now()
	installedAt := scanStartedAt.Add(-time.Hour)
	pkg := &core.PackageInfo{Name: "new", Tool: core.ToolHomebrew, InstallDate: installedAt}
	updatePackage(t, store, pkg)

	scanned := map[string]map[string]struct{}{core.ToolHomebrew: {}}
	if err := store.ReconcilePackages(scanned, scanStartedAt); err != nil {
		t.Fatalf("ReconcilePackages failed: %v", err)
	}
	if _, err := store.GetPackage(core.ToolHomebrew, "new"); err != nil {
		t.Fatalf("concurrent package was removed: %v", err)
	}
}

func TestApplyPackageScanPreservesConcurrentUsage(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	oldUse := time.Now().Add(-time.Hour)
	updatePackage(t, store, &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, UsageCount: 1, LastUsed: oldUse})
	scanStartedAt := time.Now()
	concurrent := &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, UsageCount: 2, LastUsed: oldUse}
	updatePackage(t, store, concurrent)
	scanned := &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, UsageCount: 1, LastUsed: oldUse}
	seen := map[string]map[string]struct{}{core.ToolHomebrew: {"jq": {}}}

	if err := store.ApplyPackageScan([]*core.PackageInfo{scanned}, seen, scanStartedAt); err != nil {
		t.Fatalf("ApplyPackageScan failed: %v", err)
	}
	stored, err := store.GetPackage(core.ToolHomebrew, "jq")
	if err != nil {
		t.Fatalf("GetPackage failed: %v", err)
	}
	if stored.UsageCount != concurrent.UsageCount {
		t.Fatalf("usage count = %d, want %d", stored.UsageCount, concurrent.UsageCount)
	}
}

func TestApplyPackageScanPreservesConcurrentGoUsageDelta(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	updatePackage(t, store, &core.PackageInfo{Name: "gopls", Tool: core.ToolGo, UsageCount: 4})
	updatePackage(t, store, &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, UsageCount: 3})
	scanStartedAt := time.Now()
	updatePackage(t, store, &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, UsageCount: 4})
	scanned := &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, UsageCount: 7}
	seen := map[string]map[string]struct{}{
		core.ToolGo:       {},
		core.ToolGoBinary: {"gopls": {}},
	}

	if err := store.ApplyPackageScan([]*core.PackageInfo{scanned}, seen, scanStartedAt); err != nil {
		t.Fatalf("ApplyPackageScan failed: %v", err)
	}
	stored, err := store.GetPackage(core.ToolGoBinary, "gopls")
	if err != nil {
		t.Fatalf("GetPackage failed: %v", err)
	}
	if stored.UsageCount != 8 {
		t.Fatalf("usage count = %d, want 8", stored.UsageCount)
	}
}

func TestApplyPackageScanDoesNotRestoreConcurrentUninstall(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	pkg := &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew}
	updatePackage(t, store, pkg)
	scanStartedAt := time.Now()
	if err := store.DeletePackage(pkg.Tool, pkg.Name); err != nil {
		t.Fatalf("DeletePackage failed: %v", err)
	}
	seen := map[string]map[string]struct{}{pkg.Tool: {pkg.Name: {}}}

	if err := store.ApplyPackageScan([]*core.PackageInfo{pkg}, seen, scanStartedAt); err != nil {
		t.Fatalf("ApplyPackageScan failed: %v", err)
	}
	if _, err := store.GetPackage(pkg.Tool, pkg.Name); err == nil {
		t.Fatal("concurrently uninstalled package was restored")
	}
}

func TestRemoveMissingPackagesReportsEmptyBucketRemoval(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	store.data.Packages[core.ToolHomebrew] = map[string]core.PackageInfo{}
	scanned := map[string]map[string]struct{}{core.ToolHomebrew: {}}
	if !store.removeMissingPackages(scanned, time.Time{}) {
		t.Fatal("empty package bucket removal was not reported")
	}
}

func TestCaskUninstallRemovesCaskInventory(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	pkg := &core.PackageInfo{Name: "firefox", Tool: core.ToolHomebrewCask}
	updatePackage(t, store, pkg)
	record := &core.ExecutionRecord{
		Tool:             core.ToolHomebrew,
		Args:             []string{"uninstall", "--cask", "firefox"},
		Timestamp:        time.Now(),
		PackagesAffected: []string{"firefox"},
		Metadata:         map[string]interface{}{"action": "uninstall", "type": "cask"},
	}
	addExecution(t, store, record)
	if _, err := store.GetPackage(core.ToolHomebrewCask, "firefox"); err == nil {
		t.Fatal("cask uninstall left the package in inventory")
	}
}

func TestExecutionRecordsAreReturnedAsCopies(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)

	record := &core.ExecutionRecord{
		ID:               "copy-test",
		Tool:             "npm",
		Command:          "npm install eslint",
		Args:             []string{"install", "eslint"},
		Timestamp:        time.Now(),
		Environment:      map[string]string{"NODE_ENV": "test"},
		PackagesAffected: []string{"eslint"},
		Metadata:         map[string]interface{}{"action": "install"},
	}
	addExecution(t, storage, record)

	record.Args[0] = "mutated"
	record.Environment["NODE_ENV"] = "mutated"
	record.PackagesAffected[0] = "mutated"
	record.Metadata["action"] = "mutated"
	assertStoredExecutionCopy(t, storage)
}

func assertStoredExecutionCopy(t *testing.T, storage Storage) {
	t.Helper()
	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}
	executions[0].Args[0] = "changed"
	executions[0].Environment["NODE_ENV"] = "changed"
	executions[0].PackagesAffected[0] = "changed"
	executions[0].Metadata["action"] = "changed"

	reloaded, err := storage.GetExecutionByID("copy-test")
	if err != nil {
		t.Fatalf("Failed to get execution by ID: %v", err)
	}
	recordWasChanged := reloaded.Args[0] != "install" ||
		reloaded.Environment["NODE_ENV"] != "test" ||
		reloaded.PackagesAffected[0] != "eslint" ||
		reloaded.Metadata["action"] != "install"
	if recordWasChanged {
		t.Fatalf("stored execution was mutated through returned pointer")
	}
}

func TestGetExecutions(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	// Add multiple executions
	tools := []string{"brew", "npm", "go"}
	for i, tool := range tools {
		record := &core.ExecutionRecord{
			Tool:      tool,
			Command:   tool + " test",
			Timestamp: time.Now().Add(time.Duration(-i) * time.Hour),
			ExitCode:  0,
		}
		addExecution(t, storage, record)
	}

	// Test filtering by tool
	brewExecs, err := storage.GetExecutions(QueryOptions{Tool: "brew"})
	if err != nil {
		t.Fatalf("Failed to query executions: %v", err)
	}

	if len(brewExecs) != 1 {
		t.Errorf("Expected 1 brew execution, got %d", len(brewExecs))
	}

	// Test limit
	limited, err := storage.GetExecutions(QueryOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Failed to query with limit: %v", err)
	}

	if len(limited) != 2 {
		t.Errorf("Expected 2 executions with limit, got %d", len(limited))
	}
}

func TestPackagesAndStatsAreReturnedAsCopies(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)

	updatePackage(t, storage, &core.PackageInfo{
		Name:         "eslint",
		Tool:         "npm",
		Dependencies: []string{"chalk"},
	})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Command: "npm --version", Timestamp: time.Now()})

	pkg, err := storage.GetPackage("npm", "eslint")
	if err != nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	pkg.Dependencies[0] = "mutated"

	stats, err := storage.GetStatistics()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	stats.ToolsUsed[0] = "mutated"
	stats.ExecutionFrequency["npm"] = 99
	assertStoredPackageAndStatsCopy(t, storage)
}

func assertStoredPackageAndStatsCopy(t *testing.T, storage Storage) {
	t.Helper()
	pkg, err := storage.GetPackage("npm", "eslint")
	if err != nil {
		t.Fatalf("Failed to reload package: %v", err)
	}
	stats, err := storage.GetStatistics()
	if err != nil {
		t.Fatalf("Failed to reload stats: %v", err)
	}
	if pkg.Dependencies[0] != "chalk" || stats.ToolsUsed[0] != "npm" || stats.ExecutionFrequency["npm"] != 1 {
		t.Fatalf("stored package or stats were mutated through returned pointer")
	}
}

func TestPackageManagement(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	pkg := &core.PackageInfo{
		Name:        "test-package",
		Version:     "1.0.0",
		Tool:        "npm",
		InstallDate: time.Now(),
		LastUsed:    time.Now(),
		UsageCount:  5,
	}

	err = storage.UpdatePackage(pkg)
	if err != nil {
		t.Fatalf("Failed to update package: %v", err)
	}

	// Get package
	retrieved, err := storage.GetPackage("npm", "test-package")
	if err != nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	if retrieved.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", retrieved.Version)
	}

	// Get all packages for tool
	packages, err := storage.GetPackages("npm")
	if err != nil {
		t.Fatalf("Failed to get packages: %v", err)
	}

	if len(packages) != 1 {
		t.Errorf("Expected 1 package, got %d", len(packages))
	}
}

func TestBackupRestore(t *testing.T) {
	const storageFileName = "test.json"

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, storageFileName),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Add test data
	record := &core.ExecutionRecord{
		Tool:      "test",
		Command:   "test backup",
		Timestamp: time.Now(),
	}
	addExecution(t, storage, record)

	// Create backup
	err = storage.Backup()
	if err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}

	// Verify backup file exists
	files, _ := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if len(files) == 0 {
		t.Error("Backup file was not created")
	}
	if len(files) > 0 {
		info, err := os.Stat(files[0])
		if err != nil {
			t.Fatalf("Failed to stat backup file: %v", err)
		}
		if got := info.Mode().Perm(); got != core.PrivateFileMode {
			t.Errorf("Backup file mode = %v, want %v", got, core.PrivateFileMode)
		}
		executionBackup := storage.executionBackupPath(files[0])
		if _, err := os.Stat(executionBackup); err != nil {
			t.Fatalf("execution backup was not created: %v", err)
		}
	}
	addExecution(t, storage, &core.ExecutionRecord{
		Tool:      "newer",
		Timestamp: time.Now().Add(time.Minute),
	})

	closeStorage(t, storage)

	// Create new storage and restore
	storage2, _ := NewJSONStorage(config)
	defer closeStorage(t, storage2)

	if len(files) > 0 {
		err = storage2.Restore(files[0])
		if err != nil {
			t.Fatalf("Failed to restore backup: %v", err)
		}

		executions, _ := storage2.GetExecutions(QueryOptions{})
		if len(executions) != 1 || executions[0].Tool != "test" {
			t.Fatalf("restored executions = %#v", executions)
		}
	}
}

func TestBackupRecreatesMissingExecutionLog(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)
	if err := os.Remove(ExecutionLogPath(config.Storage.JSONFile)); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if err := store.Backup(); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}
	backups, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %#v, %v", backups, err)
	}
}

func TestRestoreLegacyBackupMigratesExecutions(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:      filepath.Join(t.TempDir(), "test.json"),
			MaxExecutions: 1,
			RetentionDays: 1,
		},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)
	backupPath := config.Storage.JSONFile + ".backup.legacy"
	writeCompactionFixture(t, backupPath, 3)
	if err := store.Restore(backupPath); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil || len(executions) != 3 {
		t.Fatalf("restored executions = %d, %v", len(executions), err)
	}
}

func TestRestoreRejectsMalformedExecutionBackup(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)
	addExecution(t, store, &core.ExecutionRecord{Tool: "original", Timestamp: time.Now()})
	if err := store.Backup(); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}
	backups, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %#v, %v", backups, err)
	}
	executionBackup := store.executionBackupPath(backups[0])
	if err := os.WriteFile(executionBackup, []byte("{invalid}\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := store.Restore(backups[0]); err == nil {
		t.Fatal("Restore succeeded with malformed execution backup")
	}
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil || len(executions) != 1 || executions[0].Tool != "original" {
		t.Fatalf("executions after rejected restore = %#v, %v", executions, err)
	}
}

func TestRestoreRejectsUnknownExecutionLogFormat(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)
	backupPath := config.Storage.JSONFile + ".backup.unknown"
	backup := core.StorageData{Version: "1.0.0", ExecutionLogFormat: "unknown"}
	encoded, err := json.Marshal(backup)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if err := os.WriteFile(backupPath, encoded, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := store.Restore(backupPath); err == nil {
		t.Fatal("Restore succeeded with an unknown execution log format")
	}
}

func TestCleanup(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	// Add old and new executions
	oldRecord := &core.ExecutionRecord{
		Tool:      "old",
		Timestamp: time.Now().Add(-48 * time.Hour),
	}
	newRecord := &core.ExecutionRecord{
		Tool:      "new",
		Timestamp: time.Now(),
	}

	addExecution(t, storage, oldRecord)
	addExecution(t, storage, newRecord)

	// Cleanup records older than 24 hours
	err = storage.Cleanup(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("Failed to cleanup: %v", err)
	}

	executions, _ := storage.GetExecutions(QueryOptions{})
	if len(executions) != 1 {
		t.Errorf("Expected 1 execution after cleanup, got %d", len(executions))
	}

	if executions[0].Tool != "new" {
		t.Error("Wrong execution retained after cleanup")
	}
}

func TestAddExecutionEnforcesMaxExecutions(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:      filepath.Join(tempDir, "test.json"),
			MaxExecutions: 2,
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	now := time.Now()
	addExecution(t, storage, &core.ExecutionRecord{Tool: "oldest", Timestamp: now.Add(-3 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "middle", Timestamp: now.Add(-2 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "newest", Timestamp: now.Add(-1 * time.Hour)})

	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}

	if len(executions) != 2 {
		t.Fatalf("Expected 2 executions after max_executions pruning, got %d", len(executions))
	}

	if executions[0].Tool != "newest" || executions[1].Tool != "middle" {
		t.Errorf("Expected newest executions to be retained, got %s and %s", executions[0].Tool, executions[1].Tool)
	}
}

func TestAddExecutionEnforcesMaxStorageBytes(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:        filepath.Join(tempDir, "test.json"),
			MaxStorageBytes: 2048,
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	addExecution(t, storage, &core.ExecutionRecord{
		Tool:      "large",
		Command:   strings.Repeat("x", 4096),
		Timestamp: time.Now(),
	})

	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}
	if len(executions) != 0 {
		t.Fatalf("Expected oversized execution to be pruned, got %d executions", len(executions))
	}

	logPath := ExecutionLogPath(config.Storage.JSONFile)
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Failed to stat execution log: %v", err)
	}
	if info.Size() > config.Storage.MaxStorageBytes {
		t.Errorf("Expected storage file to be at most %d bytes, got %d", config.Storage.MaxStorageBytes, info.Size())
	}
}

func TestAddExecutionStoresHistoryOutsideManifest(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(t.TempDir(), "test.json"),
		},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, store)
	record := &core.ExecutionRecord{Tool: "npm", Command: "npm install eslint", Timestamp: time.Now()}
	addExecution(t, store, record)
	manifest, err := os.ReadFile(config.Storage.JSONFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if strings.Contains(string(manifest), record.Command) {
		t.Fatal("execution history was embedded in the manifest")
	}
	logData, err := os.ReadFile(ExecutionLogPath(config.Storage.JSONFile))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(logData), record.Command) {
		t.Fatal("execution was not appended to the execution log")
	}
}

func TestBackupPrunesOldBackups(t *testing.T) {
	const storageFileName = "test.json"

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:   filepath.Join(tempDir, storageFileName),
			MaxBackups: 2,
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	for i := 0; i < 3; i++ {
		addExecution(t, storage, &core.ExecutionRecord{
			Tool:      "test",
			Command:   "test backup pruning",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
		if err := storage.Backup(); err != nil {
			t.Fatalf("Failed to create backup %d: %v", i, err)
		}
	}

	files, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("Expected 2 backup files after pruning, got %d", len(files))
	}
	executionPattern := ExecutionLogPath(config.Storage.JSONFile) + ".backup.*"
	executionFiles, err := filepath.Glob(executionPattern)
	if err != nil || len(executionFiles) != 2 {
		t.Fatalf("execution backups = %d, %v", len(executionFiles), err)
	}
}

func TestUpdatePackageDoesNotPruneExecutions(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	addExecution(t, storage, &core.ExecutionRecord{Tool: "old", Timestamp: time.Now().Add(-2 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "new", Timestamp: time.Now()})

	config.Storage.MaxExecutions = 1
	updatePackage(t, storage, &core.PackageInfo{Name: "test-package", Tool: "npm"})

	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}
	if len(executions) != 2 {
		t.Fatalf("Expected package update to retain 2 executions, got %d", len(executions))
	}
}

func TestNextBackupPathUsesSuffixForCollision(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	jsonStorage := storage
	now := time.Date(2026, 6, 1, 12, 0, 0, 123, time.UTC)
	firstPath, err := jsonStorage.nextBackupPath(now)
	if err != nil {
		t.Fatalf("Failed to get first backup path: %v", err)
	}
	if err := os.WriteFile(firstPath, []byte("{}"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create colliding backup path: %v", err)
	}

	nextPath, err := jsonStorage.nextBackupPath(now)
	if err != nil {
		t.Fatalf("Failed to get next backup path: %v", err)
	}
	if nextPath != firstPath+".1" {
		t.Fatalf("Expected suffixed backup path %s, got %s", firstPath+".1", nextPath)
	}
}

func TestGetExecutionByID(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	record := &core.ExecutionRecord{
		ID:        "test-id-123",
		Tool:      "test",
		Command:   "test command",
		Timestamp: time.Now(),
	}

	addExecution(t, storage, record)

	retrieved, err := storage.GetExecutionByID("test-id-123")
	if err != nil {
		t.Fatalf("Failed to get execution by ID: %v", err)
	}

	if retrieved.Tool != "test" {
		t.Errorf("Expected tool 'test', got %s", retrieved.Tool)
	}

	_, err = storage.GetExecutionByID("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent ID")
	}
}

func TestGetAllPackages(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	pkg1 := &core.PackageInfo{
		Name:        "package1",
		Version:     "1.0.0",
		Tool:        "npm",
		InstallDate: time.Now(),
	}
	pkg2 := &core.PackageInfo{
		Name:        "package2",
		Version:     "2.0.0",
		Tool:        "go",
		InstallDate: time.Now(),
	}

	updatePackage(t, storage, pkg1)
	updatePackage(t, storage, pkg2)

	allPackages, err := storage.GetAllPackages()
	if err != nil {
		t.Fatalf("Failed to get all packages: %v", err)
	}

	if len(allPackages) != 2 {
		t.Errorf("Expected 2 tool groups, got %d", len(allPackages))
	}

	if allPackages["npm"]["package1"] == nil {
		t.Error("Expected npm/package1 to exist")
	}
	if allPackages["go"]["package2"] == nil {
		t.Error("Expected go/package2 to exist")
	}
}

func TestGetStatistics(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: time.Now()})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: time.Now()})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "go", Timestamp: time.Now()})

	stats, err := storage.GetStatistics()
	if err != nil {
		t.Fatalf("Failed to get statistics: %v", err)
	}

	if stats.TotalExecutions != 3 {
		t.Errorf("Expected 3 total executions, got %d", stats.TotalExecutions)
	}

	if stats.ExecutionFrequency["npm"] != 2 {
		t.Errorf("Expected npm frequency 2, got %d", stats.ExecutionFrequency["npm"])
	}

	if stats.ExecutionFrequency["go"] != 1 {
		t.Errorf("Expected go frequency 1, got %d", stats.ExecutionFrequency["go"])
	}
}

func TestUpdateStatistics(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	today := time.Now()
	yesterday := time.Now().Add(-24 * time.Hour)

	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: today})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: today})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: today})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "go", Timestamp: yesterday})

	err = storage.UpdateStatistics()
	if err != nil {
		t.Fatalf("Failed to update statistics: %v", err)
	}

	stats, _ := storage.GetStatistics()
	expectedDay := today.Format("2006-01-02")
	if stats.MostActiveDay != expectedDay {
		t.Errorf("Expected most active day %s, got %s", expectedDay, stats.MostActiveDay)
	}
}

func TestConcurrentAccess(t *testing.T) {
	const (
		concurrentWorkers      = 10
		recordsPerWorker       = 10
		expectedExecutionCount = concurrentWorkers * recordsPerWorker
	)

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	errs := make(chan error, concurrentWorkers)
	for i := 0; i < concurrentWorkers; i++ {
		go func(id int) {
			for j := 0; j < recordsPerWorker; j++ {
				record := &core.ExecutionRecord{
					Tool:      "test",
					Command:   "concurrent test",
					Timestamp: time.Now(),
				}
				if err := storage.AddExecution(record); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}(i)
	}

	for i := 0; i < concurrentWorkers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Failed to add concurrent execution: %v", err)
		}
	}

	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}

	if len(executions) != expectedExecutionCount {
		t.Errorf("Expected %d executions, got %d", expectedExecutionCount, len(executions))
	}
}

func TestQueryOptionsTimeFiltering(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	now := time.Now()
	addExecution(t, storage, &core.ExecutionRecord{Tool: "old", Timestamp: now.Add(-48 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "yesterday", Timestamp: now.Add(-24 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "today", Timestamp: now})

	since := now.Add(-30 * time.Hour)
	results, _ := storage.GetExecutions(QueryOptions{Since: &since})
	if len(results) != 2 {
		t.Errorf("Expected 2 results with Since filter, got %d", len(results))
	}

	until := now.Add(-12 * time.Hour)
	results, _ = storage.GetExecutions(QueryOptions{Until: &until})
	if len(results) != 2 {
		t.Errorf("Expected 2 results with Until filter, got %d", len(results))
	}

	results, _ = storage.GetExecutions(QueryOptions{Since: &since, Until: &until})
	if len(results) != 1 {
		t.Errorf("Expected 1 result with Since+Until filter, got %d", len(results))
	}
}

func TestQueryOptionsPackageFiltering(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	addExecution(t, storage, &core.ExecutionRecord{
		Tool:             "npm",
		Timestamp:        time.Now(),
		PackagesAffected: []string{"express", "lodash"},
	})
	addExecution(t, storage, &core.ExecutionRecord{
		Tool:             "npm",
		Timestamp:        time.Now(),
		PackagesAffected: []string{"react"},
	})

	results, _ := storage.GetExecutions(QueryOptions{Package: "express"})
	if len(results) != 1 {
		t.Errorf("Expected 1 result with package filter, got %d", len(results))
	}

	results, _ = storage.GetExecutions(QueryOptions{Package: "nonexistent"})
	if len(results) != 0 {
		t.Errorf("Expected 0 results for nonexistent package, got %d", len(results))
	}
}

func TestGetPackagesAllTools(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	updatePackage(t, storage, &core.PackageInfo{Name: "pkg1", Tool: "npm"})
	updatePackage(t, storage, &core.PackageInfo{Name: "pkg2", Tool: "go"})
	updatePackage(t, storage, &core.PackageInfo{Name: "pkg3", Tool: "npm"})

	results, err := storage.GetPackages("")
	if err != nil {
		t.Fatalf("Failed to get all packages: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 packages, got %d", len(results))
	}
}

func TestPackageNotFound(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	_, err = storage.GetPackage("npm", "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent package")
	}

	_, err = storage.GetPackage("nonexistent-tool", "pkg")
	if err == nil {
		t.Error("Expected error for nonexistent tool")
	}
}

func TestDeletePackage(t *testing.T) {
	const (
		packageName = "test-package"
		toolName    = "npm"
	)

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	if err := storage.UpdatePackage(&core.PackageInfo{Name: packageName, Tool: toolName}); err != nil {
		t.Fatalf("Failed to update package: %v", err)
	}

	if err := storage.DeletePackage(toolName, packageName); err != nil {
		t.Fatalf("Failed to delete package: %v", err)
	}

	if _, err := storage.GetPackage(toolName, packageName); err == nil {
		t.Fatal("Expected package to be deleted")
	}
}

func TestRestoreNonexistentFile(t *testing.T) {
	const storageFileName = "test.json"

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, storageFileName),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	err = storage.Restore(filepath.Join(tempDir, storageFileName+".backup.missing"))
	if err == nil {
		t.Error("Expected error for nonexistent restore file")
	}
}

func TestRestoreInvalidJSON(t *testing.T) {
	const (
		storageFileName     = "test.json"
		invalidBackupSuffix = ".backup.invalid"
	)

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, storageFileName),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	invalidFile := filepath.Join(tempDir, storageFileName+invalidBackupSuffix)
	if err := os.WriteFile(invalidFile, []byte("not valid json"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write invalid restore file: %v", err)
	}

	err = storage.Restore(invalidFile)
	if err == nil {
		t.Error("Expected error for invalid JSON restore file")
	}
}

func TestInspectJSONFileStreamsCountsAndLatestExecution(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	older := time.Now().Add(-time.Hour)
	latest := time.Now()
	addExecution(t, store, &core.ExecutionRecord{ID: "older", Tool: core.ToolHomebrew, Timestamp: older})
	addExecution(t, store, &core.ExecutionRecord{ID: "latest", Tool: core.ToolNPM, Timestamp: latest})
	updatePackage(t, store, &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew})

	inspection, err := InspectJSONFile(store.filepath)
	if err != nil {
		t.Fatalf("InspectJSONFile failed: %v", err)
	}
	if !inspection.Exists || inspection.SizeBytes == 0 {
		t.Fatalf("inspection file state = %#v", inspection)
	}
	if inspection.ExecutionCount != 2 || inspection.PackageCount != 1 {
		t.Fatalf("inspection counts = %#v", inspection)
	}
	if inspection.LatestExecution == nil || inspection.LatestExecution.ID != "latest" {
		t.Fatalf("latest execution = %#v", inspection.LatestExecution)
	}
	if inspection.Statistics.TotalExecutions != 2 || inspection.Metadata.LastUpdated.IsZero() {
		t.Fatalf("inspection metadata = %#v", inspection)
	}
}

func TestSummarizeExecutionsStreamsFilteredCounts(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	addExecution(t, store, &core.ExecutionRecord{Tool: core.ToolHomebrew, Timestamp: time.Now()})
	addExecution(t, store, &core.ExecutionRecord{Tool: core.ToolHomebrew, Timestamp: time.Now()})
	addExecution(t, store, &core.ExecutionRecord{Tool: core.ToolNPM, Timestamp: time.Now()})

	summary, err := store.SummarizeExecutions(QueryOptions{Tool: core.ToolHomebrew})
	if err != nil {
		t.Fatalf("SummarizeExecutions failed: %v", err)
	}
	if summary.Total != 2 || summary.ToolCounts[core.ToolHomebrew] != 2 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestInspectJSONFileHandlesMissingAndUnknownFields(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	inspection, err := InspectJSONFile(missing)
	if err != nil || inspection.Exists {
		t.Fatalf("missing inspection = %#v, %v", inspection, err)
	}
	path := filepath.Join(t.TempDir(), "storage.json")
	data := []byte(`{"unknown":{"nested":[1,{"ok":true}]},"metadata":null,"executions":null,"packages":null,"statistics":null}`)
	if err := os.WriteFile(path, data, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	inspection, err = InspectJSONFile(path)
	if err != nil || !inspection.Exists || inspection.ExecutionCount != 0 {
		t.Fatalf("unknown-field inspection = %#v, %v", inspection, err)
	}
}

func TestInspectJSONFileRejectsInvalidStorage(t *testing.T) {
	tests := map[string]string{
		"wrong executions type": `{"executions":{}}`,
		"trailing data":         `{} {}`,
		"truncated object":      `{"executions":[`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.json")
			if err := os.WriteFile(path, []byte(content), core.PrivateFileMode); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}
			if _, err := InspectJSONFile(path); err == nil {
				t.Fatal("invalid storage was accepted")
			}
		})
	}
	if _, err := InspectJSONFile(t.TempDir()); err == nil {
		t.Fatal("storage directory was accepted")
	}
}
