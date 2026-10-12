package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/monitors"
)

func TestJSONStorage(t *testing.T) {
	const storageFileName = "test.json"

	storage, config := newNamedTestStorage(t, storageFileName)
	defer closeStorage(t, storage)
	assertStorageManifestCreated(t, config.Storage.JSONFile)
	assertExecutionLogPermissions(t, config.Storage.JSONFile)
}

func TestStorageManifestPreservesExecutionsField(t *testing.T) {
	storage, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, storage)
	data, err := os.ReadFile(config.Storage.JSONFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if string(manifest["executions"]) != "[]" {
		t.Fatalf("executions field = %s", manifest["executions"])
	}
}

func TestJSONStorageSupportsNDJSONManifestPath(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "custom.ndjson")
	store := newStorageForManifest(t, manifestPath)
	defer closeStorage(t, store)
	assertNDJSONManifestSidecarPath(t, store, manifestPath)
	assertSingleExecutionRoundTrip(t, store)
}

func TestNewJSONStorageRejectsEmptyPath(t *testing.T) {
	config := &core.Config{Storage: core.StorageConfig{JSONFile: " "}}
	if _, err := NewJSONStorage(config); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("NewJSONStorage error = %v", err)
	}
}

func TestStorageLockDeadlineDuringInitialization(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "executions.json")
	unlock := holdStorageLock(t, manifest)
	config := &core.Config{Storage: core.StorageConfig{JSONFile: manifest}}
	assertStorageDeadline(t, func() error {
		_, err := NewJSONStorageWithLockWait(config, 50*time.Millisecond)
		return err
	})
	assertStorageNotInitialized(t, manifest)
	unlock()
	store := newStorageForManifest(t, manifest)
	closeStorage(t, store)
}

func TestInitializeRecreatesMissingExecutionLogAsEmptyHistory(t *testing.T) {
	config, reopened := reopenStorageAfterMissingExecutionLog(t)
	defer closeStorage(t, reopened)
	assertMissingExecutionLogRecreated(t, config)
	executions, err := reopened.GetExecutions(QueryOptions{})
	assertEmptyHistoryAfterMissingLogRepair(t, executions, err)
	stats, err := reopened.Statistics()
	assertEmptyStatisticsAfterMissingLogRepair(t, stats, err)
}

func TestStorageOpenProcess(t *testing.T) {
	manifest := os.Getenv("DIU_TEST_STORAGE_PATH")
	if manifest == "" {
		return
	}
	fmt.Println("opening")
	store := newStorageForManifest(t, manifest)
	defer closeStorage(t, store)
	command := os.Getenv("DIU_TEST_STORAGE_COMMAND")
	if command != "" {
		record := &core.ExecutionRecord{Tool: core.ToolGo, Command: command, Timestamp: time.Now()}
		addExecution(t, store, record)
	}
}

func TestInitializeWaitsForPendingCommitLock(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	unlock := holdStorageLock(t, store.filepath)
	record := core.ExecutionRecord{Tool: core.ToolGo, Command: "pending", Timestamp: time.Now()}
	commit := preparePendingCommit(t, store, record)
	paths := []string{commit.ManifestTemp, commit.ExecutionTemp, store.storageCommitJournalPath()}
	contents := readCommitFiles(t, paths)
	done := startStorageOpenProcess(t, store.filepath, "")
	assertStorageOpenBlocked(t, done)
	assertCommitFilesUnchanged(t, contents)
	unlock()
	waitForStorageProcess(t, done)
	assertExecutionLogIncludesExecution(t, config, &record)
	if _, err := os.Stat(store.storageCommitJournalPath()); !os.IsNotExist(err) {
		t.Fatalf("recovered journal still exists: %v", err)
	}
}

func TestSimultaneousFirstInitialization(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "new", "executions.json")
	unlock := holdStorageLock(t, manifest)
	processes := make([]<-chan error, 4)
	for i := range processes {
		processes[i] = startStorageOpenProcess(t, manifest, fmt.Sprintf("record-%d", i))
	}
	assertStorageOpenBlocked(t, processes[0])
	assertStorageNotInitialized(t, manifest)
	unlock()
	for _, done := range processes {
		waitForStorageProcess(t, done)
	}
	store := newStorageForManifest(t, manifest)
	defer closeStorage(t, store)
	assertStatisticsTotal(t, store, len(processes))
}

func TestAddExecution(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	record := testExecutionRecord()
	addExecution(t, storage, record)
	assertStoredExecution(t, storage, record.Tool)
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
	assertNoExecutionsAfterRejectedAppend(t, executions, err)
}

func TestAddExecutionDoesNotMutateStateWhenAppendFails(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	store.executionPath = t.TempDir()

	record := globalNPMExecutionRecord("eslint")
	if err := store.AddExecution(record); err == nil {
		t.Fatal("AddExecution succeeded with invalid execution log path")
	}
	assertNoStateAfterAppendFailure(t, store)
}

func TestLocalJavaScriptExecutionDoesNotCreateGlobalPackage(t *testing.T) {
	store := newGlobalOnlyNPMStorage(t)
	defer closeStorage(t, store)

	record := localNPMExecutionRecord("eslint")
	addExecution(t, store, record)
	if _, err := store.GetPackage(core.ToolNPM, "eslint"); err == nil {
		t.Fatal("local npm execution created a global package entry")
	}
}

func TestGlobalJavaScriptExecutionCreatesPackage(t *testing.T) {
	store := newGlobalOnlyNPMStorage(t)
	defer closeStorage(t, store)

	record := globalNPMExecutionRecord("typescript")
	addExecution(t, store, record)
	if _, err := store.GetPackage(core.ToolNPM, "typescript"); err != nil {
		t.Fatalf("global npm execution did not create package: %v", err)
	}
}

func TestLaunchAttemptUpdatesPackageUsage(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	usedAt := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	addExecution(t, store, launchAttemptPackageRecord(usedAt))
	assertLaunchAttemptPackageUsage(t, store, usedAt)
	assertSingleLaunchAttemptStored(t, store)
}

func TestJavaScriptExecutableUsageUpdatesInventory(t *testing.T) {
	managers := []monitors.Monitor{monitors.NewNPMMonitor(), monitors.NewPNPMMonitor(), monitors.NewBunMonitor()}
	for _, monitor := range managers {
		t.Run(monitor.Name(), func(t *testing.T) {
			assertJavaScriptExecutableUsage(t, monitor)
		})
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

func TestFailedUninstallPreservesPackageAndExecution(t *testing.T) {
	for _, action := range []string{"uninstall", "remove", "pip_uninstall", "tool_uninstall"} {
		t.Run(action, func(t *testing.T) {
			store := newTestStorage(t)
			defer closeStorage(t, store)
			now := time.Now().UTC()
			lastUsed := now.Add(-time.Hour)
			pkg := &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, UsageCount: 42, LastUsed: lastUsed}
			updatePackage(t, store, pkg)
			record := failedUninstallRecord(action)
			addExecution(t, store, record)
			assertFailedUninstallState(t, store, pkg)
		})
	}
}

func TestUVProjectExecutionsPreserveToolInventory(t *testing.T) {
	commands := [][]string{
		{"add", "ruff", "django"},
		{"remove", "ruff"},
		{"pip", "install", "ruff", "django"},
		{"pip", "uninstall", "ruff"},
	}
	for _, args := range commands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			store := newTestStorage(t)
			defer closeStorage(t, store)
			updatePackage(t, store, &core.PackageInfo{Name: "ruff", Tool: core.ToolUV, UsageCount: 42})
			record := uvExecutionRecord(t, args)
			addExecution(t, store, record)
			assertUVProjectInventory(t, store)
		})
	}
}

func TestUVToolExecutionsUpdateInventory(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	install := uvExecutionRecord(t, []string{"tool", "install", "ruff"})
	addExecution(t, store, install)
	assertUVToolUsage(t, store, 1)
	wrapped := uvExecutionRecord(t, []string{"--version"})
	wrapped.PackagesAffected = []string{"ruff"}
	wrapped.Metadata["executable"] = "ruff"
	addExecution(t, store, wrapped)
	assertUVToolUsage(t, store, 2)
	uninstall := uvExecutionRecord(t, []string{"tool", "uninstall", "ruff"})
	addExecution(t, store, uninstall)
	if _, err := store.GetPackage(core.ToolUV, "ruff"); err == nil {
		t.Fatal("tool uninstall left ruff in inventory")
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

func TestAddExecutionEnforcesMaxExecutions(t *testing.T) {
	storage, _ := newLimitedStorage(t, core.StorageConfig{MaxExecutions: 2})
	defer closeStorage(t, storage)
	addMaxExecutionRecords(t, storage)
	executions := loadExecutionsForTest(t, storage)
	assertLoadedExecutionCount(t, executions, 2)
	assertNewestExecutionsRetained(t, executions)
}

func TestAddExecutionEnforcesMaxStorageBytes(t *testing.T) {
	storage, config := newLimitedStorage(t, core.StorageConfig{MaxStorageBytes: 2048})
	defer closeStorage(t, storage)
	addLargeExecution(t, storage)
	executions := loadExecutionsForTest(t, storage)
	assertLoadedExecutionCount(t, executions, 0)
	assertExecutionLogUnderLimit(t, config)
}

func TestAddExecutionDoesNotAppendWhenManifestCommitFails(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	store.marshalStorage = failingStorageMarshal
	record := &core.ExecutionRecord{Tool: "npm", Command: "npm install broken", Timestamp: time.Now()}
	if err := store.AddExecution(record); err == nil {
		t.Fatal("AddExecution succeeded with failing manifest commit")
	}
	assertExecutionLogOmitsExecution(t, config, record)
}

func TestConcurrentAccess(t *testing.T) {
	const (
		concurrentWorkers      = 10
		recordsPerWorker       = 10
		expectedExecutionCount = concurrentWorkers * recordsPerWorker
	)

	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	errs := addConcurrentExecutions(storage, concurrentWorkers, recordsPerWorker)
	assertConcurrentResults(t, errs, concurrentWorkers)
	executions := loadExecutionsForTest(t, storage)
	assertLoadedExecutionCount(t, executions, expectedExecutionCount)
}

func TestStorageLockDeadlineDuringWrite(t *testing.T) {
	config := &core.Config{Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "executions.json")}}
	store, err := NewJSONStorageWithLockWait(config, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStorage(t, store)
	unlock := holdStorageLock(t, config.Storage.JSONFile)
	assertStorageDeadline(t, func() error {
		return store.AddExecution(&core.ExecutionRecord{Tool: core.ToolGo, Timestamp: time.Now()})
	})
	unlock()
	reopened := newStorageForManifest(t, config.Storage.JSONFile)
	defer closeStorage(t, reopened)
	assertStatisticsTotal(t, reopened, 0)
}

func TestStorageLockBudgetExcludesCommitWork(t *testing.T) {
	config := &core.Config{Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "executions.json")}}
	store, err := NewJSONStorageWithLockWait(config, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStorage(t, store)
	store.marshalStorage = func(data *core.StorageData) ([]byte, error) {
		time.Sleep(120 * time.Millisecond)
		return marshalStorageData(data)
	}
	record := &core.ExecutionRecord{Tool: core.ToolGo, Timestamp: time.Now()}
	addExecution(t, store, record)
	addExecution(t, store, record)
	assertStatisticsTotal(t, store, 2)
}

func TestExhaustedStorageLockBudgetAllowsUncontendedWrites(t *testing.T) {
	for _, wait := range []time.Duration{0, 50 * time.Millisecond} {
		t.Run(wait.String(), func(t *testing.T) {
			assertUncontendedWriteAfterBudgetExhaustion(t, wait)
		})
	}
}

func TestRecordNearStorageLimitSerializesManifestOnce(t *testing.T) {
	store := performanceStorage(t, 16063)
	marshals := 0
	store.marshalStorage = func(data *core.StorageData) ([]byte, error) {
		marshals++
		if len(data.Executions) != 0 {
			t.Fatal("recording serialized the execution history")
		}
		return marshalStorageData(data)
	}
	addExecution(t, store, &core.ExecutionRecord{Tool: "tool", Timestamp: time.Now()})
	if marshals != 1 {
		t.Fatalf("recording serialized the manifest %d times, want 1", marshals)
	}
}

func TestAddExecutionCompactsBeforeReload(t *testing.T) {
	store, config := newCompactionTestStorage(t)
	writeCompactionFixture(t, config.Storage.JSONFile, 100)
	record := &core.ExecutionRecord{ID: "new", Tool: core.ToolNPM, Timestamp: time.Now()}
	if err := store.AddExecution(record); err != nil {
		t.Fatalf("AddExecution failed: %v", err)
	}
	executions, err := store.GetExecutions(QueryOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	retained := len(executions) == 1 && executions[0].ID == record.ID
	if !retained {
		t.Fatal("new execution was not retained")
	}
	assertStorageSize(t, ExecutionLogPath(config.Storage.JSONFile), config.Storage.MaxStorageBytes)
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

func TestDelayedExecutionsPreserveLastUsed(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	records := outOfOrderPackageExecutions()
	for _, record := range records {
		addExecution(t, store, record)
	}
	assertLatestPackageUsage(t, store, records[1].Timestamp, len(records))
}

func TestOutOfOrderExecutionBatchPreservesLastUsed(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	records := outOfOrderPackageExecutions()
	if err := store.AddExecutions(records); err != nil {
		t.Fatal(err)
	}
	assertLatestPackageUsage(t, store, records[1].Timestamp, len(records))
}

func TestAddExecutionStoresHistoryOutsideManifest(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	record := &core.ExecutionRecord{Tool: "npm", Command: "npm install eslint", Timestamp: time.Now()}
	addExecution(t, store, record)

	assertManifestOmitsExecution(t, config, record)
	assertExecutionLogIncludesExecution(t, config, record)
}

func TestUpdatePackageRejectsNilPackage(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	if err := store.UpdatePackage(nil); !errors.Is(err, ErrNilPackage) {
		t.Fatalf("UpdatePackage error = %v", err)
	}
}

func TestPackageManagement(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)

	updatePackage(t, storage, testPackageInfo())
	assertStoredTestPackage(t, storage)
	assertToolPackageCount(t, storage, "npm", 1)
}

func TestUpdatePackageDoesNotPruneExecutions(t *testing.T) {
	storage, config := newLimitedStorage(t, core.StorageConfig{})
	defer closeStorage(t, storage)
	addPackagePruneExecutions(t, storage)
	config.Storage.MaxExecutions = 1
	updatePackage(t, storage, &core.PackageInfo{Name: "test-package", Tool: "npm"})
	assertPackageUpdateRetainsExecutions(t, storage)
}

func TestDeletePackage(t *testing.T) {
	const (
		packageName = "test-package"
		toolName    = "npm"
	)

	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	updatePackage(t, storage, &core.PackageInfo{Name: packageName, Tool: toolName})
	deletePackageForTest(t, storage, toolName, packageName)
	assertPackageNotFound(t, storage, toolName, packageName)
}

func TestApplyPackageScanRemovesMissingInventory(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	updatePackage(t, store, &core.PackageInfo{Name: "keep", Tool: core.ToolHomebrew})
	updatePackage(t, store, &core.PackageInfo{Name: "remove", Tool: core.ToolHomebrew})
	updatePackage(t, store, &core.PackageInfo{Name: "untouched", Tool: core.ToolNPM})

	seenHomebrew := map[string]struct{}{"keep": {}}
	scanned := map[string]map[string]struct{}{core.ToolHomebrew: seenHomebrew}
	if err := store.ApplyPackageScan(nil, scanned, time.Time{}); err != nil {
		t.Fatalf("ApplyPackageScan failed: %v", err)
	}
	if _, err := store.GetPackage(core.ToolHomebrew, "remove"); err == nil {
		t.Fatal("missing Homebrew package was not removed")
	}
	if _, err := store.GetPackage(core.ToolNPM, "untouched"); err != nil {
		t.Fatalf("unscanned npm package was removed: %v", err)
	}
}

func TestApplyPackageScanPreservesPackagesUpdatedDuringScan(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	scanStartedAt := time.Now()
	installedAt := scanStartedAt.Add(-time.Hour)
	pkg := &core.PackageInfo{Name: "new", Tool: core.ToolHomebrew, InstallDate: installedAt}
	updatePackage(t, store, pkg)

	scanned := map[string]map[string]struct{}{core.ToolHomebrew: {}}
	if err := store.ApplyPackageScan(nil, scanned, scanStartedAt); err != nil {
		t.Fatalf("ApplyPackageScan failed: %v", err)
	}
	if _, err := store.GetPackage(core.ToolHomebrew, "new"); err != nil {
		t.Fatalf("concurrent package was removed: %v", err)
	}
}

func TestApplyPackageScanPreservesConcurrentUsage(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	scanStartedAt, concurrent, scanned, seen := concurrentUsageScanFixture(t, store)

	if err := store.ApplyPackageScan([]*core.PackageInfo{scanned}, seen, scanStartedAt); err != nil {
		t.Fatalf("ApplyPackageScan failed: %v", err)
	}
	assertConcurrentUsagePreserved(t, store, concurrent)
}

func TestApplyPackageScanPreservesConcurrentGoUsageDelta(t *testing.T) {
	store := newTestStorage(t)
	defer closeStorage(t, store)
	scanStartedAt, scanned, seen := concurrentGoUsageScanFixture(t, store)

	if err := store.ApplyPackageScan([]*core.PackageInfo{scanned}, seen, scanStartedAt); err != nil {
		t.Fatalf("ApplyPackageScan failed: %v", err)
	}
	assertGoUsageCount(t, store, 8)
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

func TestBackupRestore(t *testing.T) {
	storage, config := newNamedTestStorage(t, "test.json")
	addBackupRestoreExecution(t, storage)
	backupPath := createBackupForTest(t, storage, config)
	addNewerExecution(t, storage)
	closeStorage(t, storage)

	storage2 := newStorageWithConfig(t, config)
	defer closeStorage(t, storage2)
	restoreBackupForTest(t, storage2, backupPath)
	assertRestoredExecutionTool(t, storage2, "test")
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
	assertBackupCount(t, backups, err, 1)
}

func TestBackupPrunesOldBackups(t *testing.T) {
	storage, config := newLimitedStorage(t, core.StorageConfig{MaxBackups: 2})
	defer closeStorage(t, storage)
	createBackupsForPruning(t, storage)
	assertBackupManifestCount(t, config, 2)
	assertBackupLogCount(t, config, 2)
}

func TestRestoreLegacyBackupMigratesExecutions(t *testing.T) {
	store, config := newLegacyRestoreStorage(t)
	defer closeStorage(t, store)
	backupPath := config.Storage.JSONFile + ".backup.legacy"
	writeCompactionFixture(t, backupPath, 3)
	if err := store.Restore(backupPath); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	executions, err := store.GetExecutions(QueryOptions{})
	assertRestoredExecutionCount(t, executions, err, 3)
}

func TestRestoreRejectsMalformedExecutionBackup(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	addExecution(t, store, &core.ExecutionRecord{Tool: "original", Timestamp: time.Now()})
	backupPath := createBackupForTest(t, store, config)
	writeMalformedExecutionBackup(t, store, backupPath)
	if err := store.Restore(backupPath); err == nil {
		t.Fatal("Restore succeeded with malformed execution backup")
	}
	assertOriginalExecutionPreserved(t, store)
}

func TestRestoreRejectsUnknownExecutionLogFormat(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	backupPath := config.Storage.JSONFile + ".backup.unknown"
	writeUnknownExecutionLogBackup(t, backupPath)
	if err := store.Restore(backupPath); err == nil {
		t.Fatal("Restore succeeded with an unknown execution log format")
	}
}

func TestRestoreDoesNotReplaceExecutionLogWhenManifestCommitFails(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	addExecution(t, store, &core.ExecutionRecord{Tool: "original", Command: "original", Timestamp: time.Now()})
	backupPath := createBackupForTest(t, store, config)
	newer := &core.ExecutionRecord{Tool: "newer", Command: "newer", Timestamp: time.Now().Add(time.Minute)}
	addExecution(t, store, newer)
	store.marshalStorage = failingStorageMarshal
	if err := store.Restore(backupPath); err == nil {
		t.Fatal("Restore succeeded with failing manifest commit")
	}
	assertExecutionLogIncludesExecution(t, config, newer)
}

func TestRestoreNonexistentFile(t *testing.T) {
	storage, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, storage)

	err := storage.Restore(config.Storage.JSONFile + ".backup.missing")
	if err == nil {
		t.Error("Expected error for nonexistent restore file")
	}
}

func TestRestoreInvalidJSON(t *testing.T) {
	storage, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, storage)
	invalidFile := config.Storage.JSONFile + ".backup.invalid"
	writeInvalidRestoreFile(t, invalidFile)
	assertRestoreFails(t, storage, invalidFile)
}

func TestCleanup(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	addCleanupTestExecutions(t, storage)
	cleanupBefore(t, storage, time.Now().Add(-24*time.Hour))
	assertCleanupRetainedTool(t, storage, "new")
}

func TestCleanupDoesNotCompactLogWhenManifestCommitFails(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	old := &core.ExecutionRecord{Tool: "npm", Command: "npm install old", Timestamp: time.Now().Add(-48 * time.Hour)}
	addExecution(t, store, old)
	addExecution(t, store, &core.ExecutionRecord{Tool: "npm", Command: "npm install new", Timestamp: time.Now()})
	store.marshalStorage = failingStorageMarshal
	if err := store.Cleanup(time.Now().Add(-24 * time.Hour)); err == nil {
		t.Fatal("Cleanup succeeded with failing manifest commit")
	}
	assertExecutionLogIncludesExecution(t, config, old)
}

func TestCleanupCompletesPendingStorageCommitBeforeCompacting(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	addExecution(t, store, &core.ExecutionRecord{
		Tool:      "npm",
		Command:   "npm install old",
		Timestamp: time.Now().Add(-48 * time.Hour),
	})
	pending := core.ExecutionRecord{
		Tool:      "npm",
		Command:   "npm install pending cleanup",
		Timestamp: time.Now(),
	}
	writeInterruptedStorageCommit(t, store, []core.ExecutionRecord{pending})
	if err := store.Cleanup(time.Now().Add(-24 * time.Hour)); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	assertExecutionLogIncludesExecution(t, config, &pending)
}

func TestCleanupUpdatesStatistics(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	today := addCleanupStatisticsExecutions(t, storage)
	if err := storage.Cleanup(time.Time{}); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	stats, _ := storage.Statistics()
	assertMostActiveDay(t, stats, today)
}

func TestCleanupCompactsOversizedStorage(t *testing.T) {
	store, config := newCompactionTestStorage(t)
	writeCompactionFixture(t, config.Storage.JSONFile, 100)
	if err := store.Cleanup(time.Time{}); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	assertCompactedStorage(t, store, config)
}

func TestCleanupPreservesMalformedStorage(t *testing.T) {
	store, config := newCompactionTestStorage(t)
	original := []byte(`{"version":"1.0.0","executions":[`)
	if err := os.WriteFile(config.Storage.JSONFile, original, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := store.Cleanup(time.Time{}); err == nil {
		t.Fatal("Cleanup succeeded for malformed storage")
	}
	assertStorageContents(t, config.Storage.JSONFile, original)
}

func TestCleanupPreservesMalformedExecutionLog(t *testing.T) {
	store, config := newCompactionTestStorage(t)
	logPath := ExecutionLogPath(config.Storage.JSONFile)
	original := []byte("{invalid}\n")
	if err := os.WriteFile(logPath, original, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := store.Cleanup(time.Time{}); err == nil {
		t.Fatal("Cleanup succeeded for malformed execution log")
	}
	assertStorageContents(t, logPath, original)
}

func TestCleanupPreservesPartialExecutionLogTail(t *testing.T) {
	store, config := newCompactionTestStorage(t)
	logPath := ExecutionLogPath(config.Storage.JSONFile)
	original := []byte(`{"tool":"npm"}`)
	if err := os.WriteFile(logPath, original, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := store.Cleanup(time.Time{}); err == nil {
		t.Fatal("Cleanup succeeded with partial execution log tail")
	}
	assertStorageContents(t, logPath, original)
}

func TestPrepareCompletesPendingStorageCommitBeforeCompacting(t *testing.T) {
	store, config := newLimitedStorage(t, core.StorageConfig{MaxExecutions: 1})
	defer closeStorage(t, store)
	addExecution(t, store, &core.ExecutionRecord{
		Tool:      "npm",
		Command:   "npm install old",
		Timestamp: time.Now().Add(-time.Minute),
	})
	pending := core.ExecutionRecord{
		Tool:      "npm",
		Command:   "npm install pending prepare",
		Timestamp: time.Now(),
	}
	writeInterruptedStorageCommit(t, store, []core.ExecutionRecord{pending})
	if err := store.Prepare(); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	assertExecutionLogIncludesExecution(t, config, &pending)
}

func TestPrepareDoesNotCompactForRetentionOnly(t *testing.T) {
	store := newRetentionPrepareStorage(t)
	defer closeStorage(t, store)
	record := &core.ExecutionRecord{Tool: core.ToolNPM, Timestamp: time.Now().AddDate(0, 0, -2)}
	addExecution(t, store, record)
	if err := store.Prepare(); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	executions, err := store.GetExecutions(QueryOptions{})
	assertExecutionCount(t, executions, err, 1)
}

func TestInspectJSONFileCompletesPendingStorageCommitBeforeReading(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	pending := core.ExecutionRecord{Tool: "npm", Command: "npm install pending inspect", Timestamp: time.Now()}
	writeInterruptedStorageCommit(t, store, []core.ExecutionRecord{pending})
	inspection, err := InspectJSONFile(config.Storage.JSONFile)
	if err != nil {
		t.Fatalf("InspectJSONFile failed: %v", err)
	}
	if inspection.ExecutionCount != 1 {
		t.Fatalf("ExecutionCount = %d, want 1", inspection.ExecutionCount)
	}
}

func TestInspectJSONFileTreatsMissingExecutionLogAsEmptyHistory(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "storage.json")},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)
	if err := os.Remove(ExecutionLogPath(config.Storage.JSONFile)); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	inspection, err := InspectJSONFile(config.Storage.JSONFile)
	resultMatches := err == nil && inspection.HasFile && inspection.ExecutionCount == 0
	if !resultMatches {
		t.Fatalf("inspection = %#v, %v", inspection, err)
	}
}

func TestInspectJSONFileIgnoresPartialTrailingLogLine(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "storage.json")},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)
	addExecution(t, store, &core.ExecutionRecord{Tool: core.ToolNPM, Timestamp: time.Now()})
	appendPartialExecutionLine(t, config.Storage.JSONFile)
	inspection, err := InspectJSONFile(config.Storage.JSONFile)
	resultMatches := err == nil && inspection.ExecutionCount == 1
	if !resultMatches {
		t.Fatalf("inspection with partial tail = %#v, %v", inspection, err)
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
	assertInspectionFileState(t, inspection)
	assertInspectionCounts(t, inspection)
	assertInspectionLatest(t, inspection)
	assertInspectionMetadata(t, inspection)
}

func TestInspectJSONFileHandlesMissingAndUnknownFields(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	inspection, err := InspectJSONFile(missing)
	missingOK := err == nil && !inspection.HasFile
	if !missingOK {
		t.Fatalf("missing inspection = %#v, %v", inspection, err)
	}
	path := filepath.Join(t.TempDir(), "storage.json")
	data := []byte(`{"unknown":{"nested":[1,{"ok":true}]},"metadata":null,"executions":null,"packages":null,"statistics":null}`)
	if err := os.WriteFile(path, data, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	inspection, err = InspectJSONFile(path)
	unknownFieldsOK := err == nil && inspection.HasFile && inspection.ExecutionCount == 0
	if !unknownFieldsOK {
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

func TestStorageReadsExternalWrites(t *testing.T) {
	first, second := newSharedStoragePair(t)
	defer closeStorage(t, first)
	defer closeStorage(t, second)

	record := &core.ExecutionRecord{Tool: core.ToolGo, Timestamp: time.Now()}
	addExecution(t, second, record)
	executions, err := first.GetExecutions(QueryOptions{})
	assertStaleExecutionCount(t, executions, err, 1)
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
	assertStreamedExecutionCount(t, executions, err, 1)
}

func TestLoadCompletesPendingStorageCommit(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	record := core.ExecutionRecord{Tool: "npm", Command: "npm install recovered", Timestamp: time.Now()}
	writeInterruptedStorageCommit(t, store, []core.ExecutionRecord{record})
	closeStorage(t, store)

	reopened := newStorageWithConfig(t, config)
	defer closeStorage(t, reopened)
	assertExecutionLogIncludesExecution(t, config, &record)
	assertStatisticsTotal(t, reopened, 1)
}

func TestGetExecutionsCompletesPendingStorageCommitBeforeReading(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	pending := core.ExecutionRecord{Tool: "npm", Command: "npm install pending query", Timestamp: time.Now()}
	writeInterruptedStorageCommit(t, store, []core.ExecutionRecord{pending})
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	gotPending := len(executions) == 1 && executions[0].Command == pending.Command
	if !gotPending {
		t.Fatalf("executions = %#v, want %s", executions, pending.Command)
	}
	assertExecutionLogIncludesExecution(t, config, &pending)
}

func TestGetExecutionsIgnoresPartialTrailingLogLine(t *testing.T) {
	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "storage.json")},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	defer closeStorage(t, store)
	addExecution(t, store, &core.ExecutionRecord{Tool: core.ToolNPM, Timestamp: time.Now()})
	appendPartialExecutionLine(t, config.Storage.JSONFile)

	executions, err := store.GetExecutions(QueryOptions{})
	resultMatches := err == nil && len(executions) == 1
	if !resultMatches {
		t.Fatalf("executions with partial tail = %d, %v", len(executions), err)
	}
}

func TestExecutionRecordsAreReturnedAsCopies(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	record := copyTestExecutionRecord()
	addExecution(t, storage, record)
	mutateExecutionRecord(record)
	assertStoredExecutionCopy(t, storage)
}

func TestGetExecutions(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	addQueryTestExecutions(t, storage)
	assertQueryExecutionCount(t, storage, QueryOptions{Tool: "brew"}, 1)
	assertQueryExecutionCount(t, storage, QueryOptions{Limit: 2}, 2)
}

func TestQueryOptionsTimeFiltering(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	now := addTimeFilterExecutions(t, storage)

	since := now.Add(-30 * time.Hour)
	until := now.Add(-12 * time.Hour)
	assertQueryCount(t, storage, QueryOptions{Since: &since}, 2)
	assertQueryCount(t, storage, QueryOptions{Until: &until}, 2)
	assertQueryCount(t, storage, QueryOptions{Since: &since, Until: &until}, 1)
}

func TestQueryOptionsPackageFiltering(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	addPackageFilterExecutions(t, storage)
	assertQueryCount(t, storage, QueryOptions{Package: "express"}, 1)
	assertQueryCount(t, storage, QueryOptions{Package: "nonexistent"}, 0)
}

func TestGetExecutionsStreamsLegacyStorageBeforeMigration(t *testing.T) {
	store, config := newCompactionTestStorage(t)
	writeCompactionFixture(t, config.Storage.JSONFile, 7)
	executions, err := store.GetExecutions(QueryOptions{})
	assertExecutionCount(t, executions, err, 7)
	hasExecutionLog, err := storageUsesExecutionLog(config.Storage.JSONFile)
	assertLegacyStorageFormat(t, hasExecutionLog, err)
}

func TestGetPackageCompletesPendingStorageCommitBeforeReading(t *testing.T) {
	store, config := newNamedTestStorage(t, "test.json")
	defer closeStorage(t, store)
	pending := core.ExecutionRecord{
		Tool:             "npm",
		Command:          "npm install pending-package",
		PackagesAffected: []string{"pending-package"},
		Timestamp:        time.Now(),
	}
	writeInterruptedStorageCommit(t, store, []core.ExecutionRecord{pending})
	if _, err := store.GetPackage("npm", "pending-package"); err != nil {
		t.Fatalf("GetPackage failed: %v", err)
	}
	assertExecutionLogIncludesExecution(t, config, &pending)
}

func TestPackageNotFound(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	assertPackageNotFound(t, storage, "npm", "nonexistent")
	assertPackageNotFound(t, storage, "nonexistent-tool", "pkg")
}

func TestPackagesAndStatsAreReturnedAsCopies(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	seedPackageAndStatsCopyTest(t, storage)
	mutatePackageCopy(t, storage)
	mutateStatsCopy(t, storage)
	assertStoredPackageAndStatsCopy(t, storage)
}

func TestGetPackagesAllTools(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	addAllToolsPackages(t, storage)
	results := loadPackagesForTest(t, storage, "")
	if len(results) != 3 {
		t.Errorf("Expected 3 packages, got %d", len(results))
	}
}

func TestGetAllPackages(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	addAllPackageTestData(t, storage)
	allPackages, err := storage.AllPackages()
	if err != nil {
		t.Fatalf("Failed to get all packages: %v", err)
	}
	assertAllPackageGroups(t, allPackages)
}

func TestGetStatistics(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	addStatisticsTestExecutions(t, storage)
	stats, err := storage.Statistics()
	if err != nil {
		t.Fatalf("Failed to get statistics: %v", err)
	}
	assertStatisticsCounts(t, stats)
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
	hasTotal := summary.Total == 2
	hasHomebrewCount := summary.ToolCounts[core.ToolHomebrew] == 2
	hasSummary := hasTotal && hasHomebrewCount
	if !hasSummary {
		t.Fatalf("summary = %#v", summary)
	}
}

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

func newNamedTestStorage(t *testing.T, storageFileName string) (*JSONStorage, *core.Config) {
	t.Helper()

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
	return storage, config
}

func assertStorageManifestCreated(t *testing.T, manifestPath string) {
	t.Helper()

	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Error("Storage file was not created")
	}
}

func assertExecutionLogPermissions(t *testing.T, manifestPath string) {
	t.Helper()

	info, err := os.Stat(ExecutionLogPath(manifestPath))
	if err != nil {
		t.Fatalf("Failed to stat storage file: %v", err)
	}
	if got := info.Mode().Perm(); got != core.PrivateFileMode {
		t.Errorf("Storage file mode = %v, want %v", got, core.PrivateFileMode)
	}
}

func newStorageForManifest(t *testing.T, manifestPath string) *JSONStorage {
	t.Helper()

	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: manifestPath},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	return store
}

func assertNDJSONManifestSidecarPath(t *testing.T, store *JSONStorage, manifestPath string) {
	t.Helper()

	if store.executionPath == store.filepath {
		t.Fatalf("execution log path collided with manifest path: %s", store.executionPath)
	}
	if got, want := store.executionPath, filepath.Join(filepath.Dir(manifestPath), "custom.executions.ndjson"); got != want {
		t.Fatalf("execution log path = %s, want %s", got, want)
	}
}

func assertSingleExecutionRoundTrip(t *testing.T, store *JSONStorage) {
	t.Helper()

	addExecution(t, store, &core.ExecutionRecord{Tool: core.ToolNPM, Timestamp: time.Now()})
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("executions = %d, %v", len(executions), err)
	}
	if len(executions) != 1 {
		t.Fatalf("executions = %d, %v", len(executions), err)
	}
}

func testExecutionRecord() *core.ExecutionRecord {
	return &core.ExecutionRecord{
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
}

func assertStoredExecution(t *testing.T, storage *JSONStorage, tool string) {
	t.Helper()
	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}

	if len(executions) != 1 {
		t.Errorf("Expected 1 execution, got %d", len(executions))
	}
	if executions[0].Tool != tool {
		t.Errorf("Expected tool 'test', got %s", executions[0].Tool)
	}
}

func outOfOrderPackageExecutions() []*core.ExecutionRecord {
	newest := time.Now().UTC()
	older := newest.Add(-48 * time.Hour)
	var records []*core.ExecutionRecord
	for _, timestamp := range []time.Time{older, newest, newest, older} {
		record := &core.ExecutionRecord{
			Tool:             core.ToolHomebrew,
			Timestamp:        timestamp,
			PackagesAffected: []string{"jq"},
		}
		records = append(records, record)
	}
	return records
}

func assertLatestPackageUsage(t *testing.T, store Storage, newest time.Time, count int) {
	t.Helper()
	pkg, err := store.GetPackage(core.ToolHomebrew, "jq")
	if err != nil {
		t.Fatal(err)
	}
	if !pkg.LastUsed.Equal(newest) {
		t.Errorf("last used = %s, want %s", pkg.LastUsed, newest)
	}
	if pkg.UsageCount != count {
		t.Errorf("usage count = %d, want %d", pkg.UsageCount, count)
	}
	records, err := store.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != count {
		t.Errorf("history count = %d, want %d", len(records), count)
	}
}

func assertNoExecutionsAfterRejectedAppend(t *testing.T, executions []*core.ExecutionRecord, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("executions after rejected append = %d, %v", len(executions), err)
	}
	if len(executions) != 0 {
		t.Fatalf("executions after rejected append = %d, %v", len(executions), err)
	}
}

func assertNoStateAfterAppendFailure(t *testing.T, store *JSONStorage) {
	t.Helper()

	stats, err := store.Statistics()
	if err != nil {
		t.Fatalf("statistics after failed append = %#v, %v", stats, err)
	}
	if stats.TotalExecutions != 0 {
		t.Fatalf("statistics after failed append = %#v, %v", stats, err)
	}
	if _, err := store.GetPackage(core.ToolNPM, "eslint"); err == nil {
		t.Fatal("package inventory changed after failed append")
	}
}

func newGlobalOnlyNPMStorage(t *testing.T) *JSONStorage {
	t.Helper()

	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")},
		Tools:   core.ToolsConfig{NPM: core.NPMConfig{ShouldTrackGlobalOnly: true}},
	}
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	return store
}

func localNPMExecutionRecord(name string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Timestamp:        time.Now(),
		PackagesAffected: []string{name},
		Metadata:         map[string]interface{}{"global": false},
	}
}

func globalNPMExecutionRecord(name string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Timestamp:        time.Now(),
		PackagesAffected: []string{name},
		Metadata:         map[string]interface{}{"global": true},
	}
}

func launchAttemptPackageRecord(usedAt time.Time) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Kind: core.ExecutionKindLaunchAttempt, Tool: core.ToolNPM, Command: "prettier",
		Timestamp: usedAt, PackagesAffected: []string{"prettier"},
		Metadata: map[string]interface{}{"executable": "prettier"},
	}
}

func assertLaunchAttemptPackageUsage(t *testing.T, store Storage, usedAt time.Time) {
	t.Helper()
	pkg, err := store.GetPackage(core.ToolNPM, "prettier")
	if err != nil {
		t.Fatal(err)
	}
	usageMatches := pkg.UsageCount == 1 && pkg.LastUsed.Equal(usedAt)
	if !usageMatches {
		t.Fatalf("launch usage = count %d at %s, want count 1 at %s", pkg.UsageCount, pkg.LastUsed, usedAt)
	}
}

func assertSingleLaunchAttemptStored(t *testing.T, store Storage) {
	t.Helper()
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	isSingleLaunchAttempt := len(executions) == 1 && executions[0].Kind == core.ExecutionKindLaunchAttempt
	if !isSingleLaunchAttempt {
		t.Fatalf("stored launch attempts = %#v", executions)
	}
}

func assertJavaScriptExecutableUsage(t *testing.T, monitor monitors.Monitor) {
	t.Helper()
	store := newGlobalOnlyNPMStorage(t)
	defer closeStorage(t, store)
	for index, args := range [][]string{nil, {"--version"}, {"remove", "something", "-g"}} {
		record := javascriptExecutableRecord(monitor.Name(), args)
		monitors.EnrichExecutionRecord(monitor, record)
		addExecution(t, store, record)
		pkg, err := store.GetPackage(monitor.Name(), "example-cli")
		if err != nil {
			t.Fatal(err)
		}
		correctUsage := pkg.UsageCount == index+1 && pkg.LastUsed.Equal(record.Timestamp)
		if !correctUsage {
			t.Fatalf("executable usage = %+v, want count %d and timestamp %v", pkg, index+1, record.Timestamp)
		}
	}
}

func javascriptExecutableRecord(tool string, args []string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool: tool, Command: "example-cli", Args: args, Timestamp: time.Now(),
		PackagesAffected: []string{"example-cli"},
		Metadata:         map[string]interface{}{"executable": "example-cli", "original_path": "/bin/example-cli"},
	}
}

func failedUninstallRecord(action string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:             core.ToolHomebrew,
		Timestamp:        time.Now(),
		ExitCode:         1,
		PackagesAffected: []string{"jq"},
		Metadata:         map[string]interface{}{"action": action},
	}
}

func assertFailedUninstallState(t *testing.T, store Storage, original *core.PackageInfo) {
	t.Helper()
	pkg, err := store.GetPackage(original.Tool, original.Name)
	if err != nil {
		t.Fatalf("failed uninstall removed inventory: %v", err)
	}
	countPreserved := pkg.UsageCount == original.UsageCount
	usagePreserved := countPreserved && pkg.LastUsed.Equal(original.LastUsed)
	if !usagePreserved {
		t.Fatalf("failed uninstall changed package usage: %+v", pkg)
	}
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	hasFailedExecution := len(executions) == 1 && executions[0].ExitCode == 1
	if !hasFailedExecution {
		t.Fatalf("failed uninstall was not retained in history: %+v", executions)
	}
}

func uvExecutionRecord(t *testing.T, args []string) *core.ExecutionRecord {
	t.Helper()
	record, err := monitors.NewUVMonitor().ParseCommand("uv", args)
	if err != nil {
		t.Fatal(err)
	}
	record.Timestamp = time.Now()
	return record
}

func assertUVProjectInventory(t *testing.T, store Storage) {
	t.Helper()
	packages, err := store.GetPackages(core.ToolUV)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 {
		t.Fatalf("project command changed tool inventory: %+v", packages)
	}
	pkg := packages[0]
	usagePreserved := pkg.Name == "ruff" && pkg.UsageCount == 42 && pkg.LastUsed.IsZero()
	if !usagePreserved {
		t.Fatalf("project command changed tool usage: %+v", pkg)
	}
	records, err := store.GetExecutions(QueryOptions{})
	hasExecution := err == nil && len(records) == 1
	if !hasExecution {
		t.Fatalf("project command missing from history: %v, %v", records, err)
	}
}

func assertUVToolUsage(t *testing.T, store Storage, count int) {
	t.Helper()
	pkg, err := store.GetPackage(core.ToolUV, "ruff")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.UsageCount != count {
		t.Fatalf("tool usage count = %d, want %d", pkg.UsageCount, count)
	}
}

func newSharedStoragePair(t *testing.T) (*JSONStorage, *JSONStorage) {
	t.Helper()

	config := &core.Config{
		Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "test.json")},
	}
	first := newLabeledStorage(t, config, "first")
	second := newLabeledStorage(t, config, "second")
	return first, second
}

func newLabeledStorage(t *testing.T, config *core.Config, label string) *JSONStorage {
	t.Helper()

	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("%s NewJSONStorage failed: %v", label, err)
	}
	return store
}

func assertStaleExecutionCount(t *testing.T, executions []*core.ExecutionRecord, err error, want int) {
	t.Helper()

	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	if len(executions) != want {
		t.Fatalf("stale execution count = %d, want 1", len(executions))
	}
}

func assertStreamedExecutionCount(t *testing.T, executions []*core.ExecutionRecord, err error, want int) {
	t.Helper()

	if err != nil {
		t.Fatalf("streamed executions = %d, %v", len(executions), err)
	}
	if len(executions) != want {
		t.Fatalf("streamed executions = %d, %v", len(executions), err)
	}
}

func reopenStorageAfterMissingExecutionLog(t *testing.T) (*core.Config, *JSONStorage) {
	t.Helper()

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
	return config, reopened
}

func assertMissingExecutionLogRecreated(t *testing.T, config *core.Config) {
	t.Helper()

	if _, err := os.Stat(ExecutionLogPath(config.Storage.JSONFile)); err != nil {
		t.Fatalf("execution log was not recreated: %v", err)
	}
}

func assertEmptyHistoryAfterMissingLogRepair(t *testing.T, executions []*core.ExecutionRecord, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("executions after missing log repair = %d, %v", len(executions), err)
	}
	if len(executions) != 0 {
		t.Fatalf("executions after missing log repair = %d, %v", len(executions), err)
	}
}

func assertEmptyStatisticsAfterMissingLogRepair(t *testing.T, stats *core.StorageStatistics, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("statistics after missing log repair = %#v, %v", stats, err)
	}
	if stats.TotalExecutions != 0 {
		t.Fatalf("statistics after missing log repair = %#v, %v", stats, err)
	}
}

func concurrentUsageScanFixture(
	t *testing.T,
	store *JSONStorage,
) (time.Time, *core.PackageInfo, *core.PackageInfo, map[string]map[string]struct{}) {
	t.Helper()

	oldUse := time.Now().Add(-time.Hour)
	updatePackage(t, store, &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, UsageCount: 1, LastUsed: oldUse})
	scanStartedAt := time.Now()
	concurrent := &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, UsageCount: 2, LastUsed: oldUse}
	updatePackage(t, store, concurrent)
	scanned := &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, UsageCount: 1, LastUsed: oldUse}
	seen := map[string]map[string]struct{}{core.ToolHomebrew: {"jq": {}}}
	return scanStartedAt, concurrent, scanned, seen
}

func assertConcurrentUsagePreserved(t *testing.T, store *JSONStorage, concurrent *core.PackageInfo) {
	t.Helper()

	stored, err := store.GetPackage(core.ToolHomebrew, "jq")
	if err != nil {
		t.Fatalf("GetPackage failed: %v", err)
	}
	if stored.UsageCount != concurrent.UsageCount {
		t.Fatalf("usage count = %d, want %d", stored.UsageCount, concurrent.UsageCount)
	}
}

func concurrentGoUsageScanFixture(
	t *testing.T,
	store *JSONStorage,
) (time.Time, *core.PackageInfo, map[string]map[string]struct{}) {
	t.Helper()

	updatePackage(t, store, &core.PackageInfo{Name: "gopls", Tool: core.ToolGo, UsageCount: 4})
	updatePackage(t, store, &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, UsageCount: 3})
	scanStartedAt := time.Now()
	updatePackage(t, store, &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, UsageCount: 4})
	scanned := &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, UsageCount: 7}
	seen := map[string]map[string]struct{}{
		core.ToolGo:       {},
		core.ToolGoBinary: {"gopls": {}},
	}
	return scanStartedAt, scanned, seen
}

func assertGoUsageCount(t *testing.T, store *JSONStorage, want int) {
	t.Helper()

	stored, err := store.GetPackage(core.ToolGoBinary, "gopls")
	if err != nil {
		t.Fatalf("GetPackage failed: %v", err)
	}
	if stored.UsageCount != want {
		t.Fatalf("usage count = %d, want %d", stored.UsageCount, want)
	}
}

func testPackageInfo() *core.PackageInfo {
	return &core.PackageInfo{
		Name:        "test-package",
		Version:     "1.0.0",
		Tool:        "npm",
		InstallDate: time.Now(),
		LastUsed:    time.Now(),
		UsageCount:  5,
	}
}

func assertStoredTestPackage(t *testing.T, storage Storage) {
	t.Helper()

	retrieved, err := storage.GetPackage("npm", "test-package")
	if err != nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	if retrieved.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", retrieved.Version)
	}
}

func assertToolPackageCount(t *testing.T, storage Storage, tool string, want int) {
	t.Helper()

	packages, err := storage.GetPackages(tool)
	if err != nil {
		t.Fatalf("Failed to get packages: %v", err)
	}
	if len(packages) != want {
		t.Errorf("Expected %d package, got %d", want, len(packages))
	}
}

func addBackupRestoreExecution(t *testing.T, store Storage) {
	t.Helper()

	record := &core.ExecutionRecord{
		Tool:      "test",
		Command:   "test backup",
		Timestamp: time.Now(),
	}
	addExecution(t, store, record)
}

func addNewerExecution(t *testing.T, store Storage) {
	t.Helper()

	record := &core.ExecutionRecord{
		Tool:      "newer",
		Timestamp: time.Now().Add(time.Minute),
	}
	addExecution(t, store, record)
}

func newStorageWithConfig(t *testing.T, config *core.Config) *JSONStorage {
	t.Helper()

	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	return store
}

func restoreBackupForTest(t *testing.T, store *JSONStorage, backupPath string) {
	t.Helper()

	if err := store.Restore(backupPath); err != nil {
		t.Fatalf("Failed to restore backup: %v", err)
	}
}

func createBackupForTest(t *testing.T, store *JSONStorage, config *core.Config) string {
	t.Helper()

	if err := store.Backup(); err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}
	files, _ := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	assertBackupCount(t, files, nil, 1)
	assertBackupFile(t, store, files[0])
	return files[0]
}

func assertBackupFile(t *testing.T, store *JSONStorage, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat backup file: %v", err)
	}
	if got := info.Mode().Perm(); got != core.PrivateFileMode {
		t.Errorf("Backup file mode = %v, want %v", got, core.PrivateFileMode)
	}
	executionBackup := store.executionBackupPath(path)
	if _, err := os.Stat(executionBackup); err != nil {
		t.Fatalf("execution backup was not created: %v", err)
	}
}

func assertRestoredExecutionTool(t *testing.T, store *JSONStorage, tool string) {
	t.Helper()

	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("restored executions = %#v, %v", executions, err)
	}
	if len(executions) != 1 {
		t.Fatalf("restored executions = %#v", executions)
	}
	if executions[0].Tool != tool {
		t.Fatalf("restored executions = %#v", executions)
	}
}

func assertBackupCount(t *testing.T, backups []string, err error, want int) {
	t.Helper()

	if err != nil {
		t.Fatalf("backups = %#v, %v", backups, err)
	}
	if len(backups) != want {
		t.Fatalf("backups = %#v, %v", backups, err)
	}
}

func newLegacyRestoreStorage(t *testing.T) (*JSONStorage, *core.Config) {
	t.Helper()

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
	return store, config
}

func assertRestoredExecutionCount(t *testing.T, executions []*core.ExecutionRecord, err error, want int) {
	t.Helper()

	if err != nil {
		t.Fatalf("restored executions = %d, %v", len(executions), err)
	}
	if len(executions) != want {
		t.Fatalf("restored executions = %d, %v", len(executions), err)
	}
}

func writeMalformedExecutionBackup(t *testing.T, store *JSONStorage, backupPath string) {
	t.Helper()

	executionBackup := store.executionBackupPath(backupPath)
	if err := os.WriteFile(executionBackup, []byte("{invalid}\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func assertOriginalExecutionPreserved(t *testing.T, store *JSONStorage) {
	t.Helper()

	executions, err := store.GetExecutions(QueryOptions{})
	loadedExecutions := err == nil
	hasOneExecution := len(executions) == 1
	hasOriginalTool := hasOneExecution && executions[0].Tool == "original"
	keptOriginal := loadedExecutions && hasOriginalTool
	if !keptOriginal {
		t.Fatalf("executions after rejected restore = %#v, %v", executions, err)
	}
}

func writeUnknownExecutionLogBackup(t *testing.T, backupPath string) {
	t.Helper()

	backup := core.StorageData{Version: "1.0.0", ExecutionLogFormat: "unknown"}
	encoded, err := json.Marshal(backup)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if err := os.WriteFile(backupPath, encoded, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func addCleanupTestExecutions(t *testing.T, storage Storage) {
	t.Helper()

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
}

func cleanupBefore(t *testing.T, storage Storage, before time.Time) {
	t.Helper()

	if err := storage.Cleanup(before); err != nil {
		t.Fatalf("Failed to cleanup: %v", err)
	}
}

func assertCleanupRetainedTool(t *testing.T, storage Storage, tool string) {
	t.Helper()

	executions, _ := storage.GetExecutions(QueryOptions{})
	if len(executions) != 1 {
		t.Errorf("Expected 1 execution after cleanup, got %d", len(executions))
	}

	if executions[0].Tool != tool {
		t.Error("Wrong execution retained after cleanup")
	}
}

func addMaxExecutionRecords(t *testing.T, storage Storage) {
	t.Helper()
	now := time.Now()
	addExecution(t, storage, &core.ExecutionRecord{Tool: "oldest", Timestamp: now.Add(-3 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "middle", Timestamp: now.Add(-2 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "newest", Timestamp: now.Add(-1 * time.Hour)})
}

func loadExecutionsForTest(t *testing.T, storage Storage) []*core.ExecutionRecord {
	t.Helper()
	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}
	return executions
}

func assertLoadedExecutionCount(t *testing.T, executions []*core.ExecutionRecord, want int) {
	t.Helper()
	if len(executions) != want {
		t.Fatalf("Expected %d executions, got %d", want, len(executions))
	}
}

func assertNewestExecutionsRetained(t *testing.T, executions []*core.ExecutionRecord) {
	t.Helper()

	if executions[0].Tool != "newest" {
		t.Errorf("Expected newest executions to be retained, got %s and %s", executions[0].Tool, executions[1].Tool)
	}
	if executions[1].Tool != "middle" {
		t.Errorf("Expected newest executions to be retained, got %s and %s", executions[0].Tool, executions[1].Tool)
	}
}

func addLargeExecution(t *testing.T, storage Storage) {
	t.Helper()
	addExecution(t, storage, &core.ExecutionRecord{
		Tool:      "large",
		Command:   strings.Repeat("x", 4096),
		Timestamp: time.Now(),
	})
}

func newLimitedStorage(t *testing.T, options core.StorageConfig) (*JSONStorage, *core.Config) {
	t.Helper()

	options.JSONFile = filepath.Join(t.TempDir(), "test.json")
	config := &core.Config{Storage: options}
	return newStorageWithConfig(t, config), config
}

func assertExecutionLogUnderLimit(t *testing.T, config *core.Config) {
	t.Helper()

	logPath := ExecutionLogPath(config.Storage.JSONFile)
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Failed to stat execution log: %v", err)
	}
	if info.Size() > config.Storage.MaxStorageBytes {
		t.Errorf("Expected storage file to be at most %d bytes, got %d", config.Storage.MaxStorageBytes, info.Size())
	}
}

func failingStorageMarshal(*core.StorageData) ([]byte, error) {
	return nil, errors.New("marshal failed")
}

func writeInterruptedStorageCommit(t *testing.T, store *JSONStorage, records []core.ExecutionRecord) {
	t.Helper()
	executionTemp, err := store.prepareAppendedExecutionLog(records)
	if err != nil {
		t.Fatalf("prepareAppendedExecutionLog failed: %v", err)
	}
	store.applyExecutions(records)
	commit, err := store.prepareStorageCommit(executionTemp)
	if err != nil {
		t.Fatalf("prepareStorageCommit failed: %v", err)
	}
	if err := store.writeStorageCommitJournal(commit); err != nil {
		t.Fatalf("writeStorageCommitJournal failed: %v", err)
	}
	if err := replacePreparedFile(commit.ManifestTemp, store.filepath); err != nil {
		t.Fatalf("replacePreparedFile failed: %v", err)
	}
}

func assertManifestOmitsExecution(t *testing.T, config *core.Config, record *core.ExecutionRecord) {
	t.Helper()

	manifest, err := os.ReadFile(config.Storage.JSONFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if strings.Contains(string(manifest), record.Command) {
		t.Fatal("execution history was embedded in the manifest")
	}
}

func assertExecutionLogIncludesExecution(t *testing.T, config *core.Config, record *core.ExecutionRecord) {
	t.Helper()

	logData, err := os.ReadFile(ExecutionLogPath(config.Storage.JSONFile))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(logData), record.Command) {
		t.Fatal("execution was not appended to the execution log")
	}
}

func assertExecutionLogOmitsExecution(t *testing.T, config *core.Config, record *core.ExecutionRecord) {
	t.Helper()

	logData, err := os.ReadFile(ExecutionLogPath(config.Storage.JSONFile))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if strings.Contains(string(logData), record.Command) {
		t.Fatal("execution was appended to the execution log")
	}
}

func assertStatisticsTotal(t *testing.T, store Storage, want int) {
	t.Helper()

	statistics, err := store.Statistics()
	if err != nil {
		t.Fatalf("Statistics failed: %v", err)
	}
	if statistics.TotalExecutions != want {
		t.Fatalf("TotalExecutions = %d, want %d", statistics.TotalExecutions, want)
	}
}

func createBackupsForPruning(t *testing.T, storage *JSONStorage) {
	t.Helper()
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
}

func assertBackupManifestCount(t *testing.T, config *core.Config, want int) {
	t.Helper()
	files, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	assertBackupCount(t, files, err, want)
}

func assertBackupLogCount(t *testing.T, config *core.Config, want int) {
	t.Helper()
	executionPattern := ExecutionLogPath(config.Storage.JSONFile) + ".backup.*"
	executionFiles, err := filepath.Glob(executionPattern)
	assertExecutionBackupCount(t, executionFiles, err, want)
}

func assertExecutionBackupCount(t *testing.T, executionFiles []string, err error, want int) {
	t.Helper()

	if err != nil {
		t.Fatalf("execution backups = %d, %v", len(executionFiles), err)
	}
	if len(executionFiles) != want {
		t.Fatalf("execution backups = %d, %v", len(executionFiles), err)
	}
}

func addPackagePruneExecutions(t *testing.T, storage Storage) {
	t.Helper()

	addExecution(t, storage, &core.ExecutionRecord{Tool: "old", Timestamp: time.Now().Add(-2 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "new", Timestamp: time.Now()})
}

func assertPackageUpdateRetainsExecutions(t *testing.T, storage Storage) {
	t.Helper()

	executions := loadExecutionsForTest(t, storage)
	if len(executions) != 2 {
		t.Fatalf("Expected package update to retain 2 executions, got %d", len(executions))
	}
}

func addCleanupStatisticsExecutions(t *testing.T, storage Storage) time.Time {
	t.Helper()
	today := time.Now()
	yesterday := time.Now().Add(-24 * time.Hour)
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: today})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: today})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: today})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "go", Timestamp: yesterday})
	return today
}

func assertMostActiveDay(t *testing.T, stats *core.StorageStatistics, today time.Time) {
	t.Helper()

	expectedDay := today.Format("2006-01-02")
	if stats.MostActiveDay != expectedDay {
		t.Errorf("Expected most active day %s, got %s", expectedDay, stats.MostActiveDay)
	}
}

func addConcurrentExecutions(storage Storage, workers, recordsPerWorker int) chan error {
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			errs <- addWorkerExecutions(storage, recordsPerWorker)
		}()
	}
	return errs
}

func addWorkerExecutions(storage Storage, recordsPerWorker int) error {
	for j := 0; j < recordsPerWorker; j++ {
		record := &core.ExecutionRecord{
			Tool:      "test",
			Command:   "concurrent test",
			Timestamp: time.Now(),
		}
		if err := storage.AddExecution(record); err != nil {
			return err
		}
	}
	return nil
}

func assertConcurrentResults(t *testing.T, errs <-chan error, workers int) {
	t.Helper()
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Failed to add concurrent execution: %v", err)
		}
	}
}

func deletePackageForTest(t *testing.T, storage Storage, tool, name string) {
	t.Helper()

	if err := storage.DeletePackage(tool, name); err != nil {
		t.Fatalf("Failed to delete package: %v", err)
	}
}

func writeInvalidRestoreFile(t *testing.T, invalidFile string) {
	t.Helper()

	if err := os.WriteFile(invalidFile, []byte("not valid json"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write invalid restore file: %v", err)
	}
}

func assertRestoreFails(t *testing.T, storage Storage, path string) {
	t.Helper()

	if err := storage.Restore(path); err == nil {
		t.Error("Expected error for invalid JSON restore file")
	}
}

func preparePendingCommit(t *testing.T, store *JSONStorage, record core.ExecutionRecord) storageCommitJournal {
	t.Helper()
	records := []core.ExecutionRecord{record}
	executionTemp, err := store.prepareAppendedExecutionLog(records)
	if err != nil {
		t.Fatal(err)
	}
	store.applyExecutions(records)
	commit, err := store.prepareStorageCommit(executionTemp)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.writeStorageCommitJournal(commit); err != nil {
		t.Fatal(err)
	}
	return commit
}

func assertStorageNotInitialized(t *testing.T, manifest string) {
	t.Helper()
	for _, path := range []string{manifest, ExecutionLogPath(manifest)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("initialization changed %s while locked: %v", path, err)
		}
	}
}

func assertUncontendedWriteAfterBudgetExhaustion(t *testing.T, wait time.Duration) {
	t.Helper()
	config := &core.Config{Storage: core.StorageConfig{JSONFile: filepath.Join(t.TempDir(), "executions.json")}}
	store, err := NewJSONStorageWithLockWait(config, wait)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStorage(t, store)
	unlock := holdStorageLock(t, config.Storage.JSONFile)
	record := &core.ExecutionRecord{Tool: core.ToolGo, Timestamp: time.Now()}
	assertStorageDeadline(t, func() error { return store.AddExecution(record) })
	unlock()
	addExecution(t, store, record)
	assertStatisticsTotal(t, store, 1)
}

func assertStorageDeadline(t *testing.T, operation func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- operation() }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("lock wait ignored deadline: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("lock wait exceeded deadline")
	}
}

func holdStorageLock(t *testing.T, manifest string) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(manifest), core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(manifest+".lock", os.O_CREATE|os.O_RDWR, core.PrivateFileMode)
	if err != nil {
		t.Fatal(err)
	}
	unlock := sync.OnceFunc(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(unlock)
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return unlock
}

func startStorageOpenProcess(t *testing.T, manifest, command string) <-chan error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStorageOpenProcess$")
	cmd.Env = append(os.Environ(), "DIU_TEST_STORAGE_PATH="+manifest, "DIU_TEST_STORAGE_COMMAND="+command)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForStorageProcessStart(t, stdout)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done
}

func waitForStorageProcessStart(t *testing.T, stdout io.Reader) {
	t.Helper()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	hasStarted := err == nil && line == "opening\n"
	if !hasStarted {
		t.Fatalf("storage process did not start: %q, %v", line, err)
	}
}

func assertStorageOpenBlocked(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("storage opened before the writer released its lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func waitForStorageProcess(t *testing.T, done <-chan error) {
	t.Helper()
	if err := <-done; err != nil {
		t.Fatalf("storage process failed: %v", err)
	}
}

func readCommitFiles(t *testing.T, paths []string) map[string]string {
	t.Helper()
	contents := make(map[string]string)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		contents[path] = string(data)
	}
	return contents
}

func assertCommitFilesUnchanged(t *testing.T, contents map[string]string) {
	t.Helper()
	for path, want := range contents {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("pending commit changed while locked: %v", err)
			continue
		}
		if string(data) != want {
			t.Errorf("pending commit changed while locked: %s", path)
		}
	}
}

func BenchmarkRecordNearStorageLimit(b *testing.B) {
	for _, count := range []int{0, 16063} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store := performanceStorage(b, count)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				record := &core.ExecutionRecord{Tool: core.ToolGoBinary, Command: "tool", Timestamp: time.Now()}
				if err := store.AddExecution(record); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			info, err := os.Stat(store.executionPath)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(info.Size()), "history-bytes")
		})
	}
}

func performanceStorage(b testing.TB, count int) *JSONStorage {
	b.Helper()
	config := core.DefaultConfig()
	config.Storage.JSONFile = filepath.Join(b.TempDir(), "executions.json")
	store, err := NewJSONStorage(config)
	if err != nil {
		b.Fatal(err)
	}
	records := make([]*core.ExecutionRecord, count)
	for index := range records {
		records[index] = &core.ExecutionRecord{ID: fmt.Sprintf("seed-%05d", index), Tool: "seed", Command: strings.Repeat("x", 480), Timestamp: time.Now()}
	}
	if err := store.AddExecutions(records); err != nil {
		b.Fatal(err)
	}
	return store
}

func appendPartialExecutionLine(t *testing.T, manifestPath string) {
	t.Helper()

	file, err := os.OpenFile(ExecutionLogPath(manifestPath), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	if _, err := file.WriteString(`{"tool":"tail"}`); err != nil {
		t.Fatalf("WriteString failed: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func copyTestExecutionRecord() *core.ExecutionRecord {
	return &core.ExecutionRecord{
		ID:               "copy-test",
		Tool:             "npm",
		Command:          "npm install eslint",
		Args:             []string{"install", "eslint"},
		Timestamp:        time.Now(),
		Environment:      map[string]string{"NODE_ENV": "test"},
		PackagesAffected: []string{"eslint"},
		Metadata:         map[string]interface{}{"action": "install"},
	}
}

func mutateExecutionRecord(record *core.ExecutionRecord) {
	record.Args[0] = "mutated"
	record.Environment["NODE_ENV"] = "mutated"
	record.PackagesAffected[0] = "mutated"
	record.Metadata["action"] = "mutated"
}

func assertStoredExecutionCopy(t *testing.T, storage Storage) {
	t.Helper()
	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}
	mutateReturnedExecution(executions[0])

	reloaded, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to reload executions: %v", err)
	}
	assertReloadedExecutionUnchanged(t, reloaded[0])
}

func mutateReturnedExecution(record *core.ExecutionRecord) {
	record.Args[0] = "changed"
	record.Environment["NODE_ENV"] = "changed"
	record.PackagesAffected[0] = "changed"
	record.Metadata["action"] = "changed"
}

func assertReloadedExecutionUnchanged(t *testing.T, record *core.ExecutionRecord) {
	t.Helper()

	if record.Args[0] != "install" {
		t.Fatalf("stored execution was mutated through returned pointer")
	}
	if record.Environment["NODE_ENV"] != "test" {
		t.Fatalf("stored execution was mutated through returned pointer")
	}
	if record.PackagesAffected[0] != "eslint" {
		t.Fatalf("stored execution was mutated through returned pointer")
	}
	if record.Metadata["action"] != "install" {
		t.Fatalf("stored execution was mutated through returned pointer")
	}
}

func addQueryTestExecutions(t *testing.T, storage Storage) {
	t.Helper()

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
}

func assertQueryExecutionCount(t *testing.T, storage Storage, opts QueryOptions, want int) {
	t.Helper()

	executions, err := storage.GetExecutions(opts)
	if err != nil {
		t.Fatalf("Failed to query executions: %v", err)
	}
	if len(executions) != want {
		t.Errorf("Expected %d executions, got %d", want, len(executions))
	}
}

func seedPackageAndStatsCopyTest(t *testing.T, storage Storage) {
	t.Helper()
	updatePackage(t, storage, &core.PackageInfo{
		Name:         "eslint",
		Tool:         "npm",
		Dependencies: []string{"chalk"},
	})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Command: "npm --version", Timestamp: time.Now()})
}

func mutatePackageCopy(t *testing.T, storage Storage) {
	t.Helper()
	pkg, err := storage.GetPackage("npm", "eslint")
	if err != nil {
		t.Fatalf("Failed to get package: %v", err)
	}
	pkg.Dependencies[0] = "mutated"
}

func mutateStatsCopy(t *testing.T, storage Storage) {
	t.Helper()
	stats, err := storage.Statistics()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	stats.ToolsUsed[0] = "mutated"
	stats.ExecutionFrequency["npm"] = 99
}

func assertStoredPackageAndStatsCopy(t *testing.T, storage Storage) {
	t.Helper()
	pkg, err := storage.GetPackage("npm", "eslint")
	if err != nil {
		t.Fatalf("Failed to reload package: %v", err)
	}
	stats, err := storage.Statistics()
	if err != nil {
		t.Fatalf("Failed to reload stats: %v", err)
	}
	if pkg.Dependencies[0] != "chalk" {
		t.Fatalf("stored package or stats were mutated through returned pointer")
	}
	if stats.ToolsUsed[0] != "npm" {
		t.Fatalf("stored package or stats were mutated through returned pointer")
	}
	if stats.ExecutionFrequency["npm"] != 1 {
		t.Fatalf("stored package or stats were mutated through returned pointer")
	}
}

func addAllPackageTestData(t *testing.T, storage Storage) {
	t.Helper()
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
}

func assertAllPackageGroups(t *testing.T, allPackages map[string]map[string]*core.PackageInfo) {
	t.Helper()
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

func addStatisticsTestExecutions(t *testing.T, storage Storage) {
	t.Helper()
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: time.Now()})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: time.Now()})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "go", Timestamp: time.Now()})
}

func assertStatisticsCounts(t *testing.T, stats *core.StorageStatistics) {
	t.Helper()
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

func addTimeFilterExecutions(t *testing.T, storage Storage) time.Time {
	t.Helper()

	now := time.Now()
	addExecution(t, storage, &core.ExecutionRecord{Tool: "old", Timestamp: now.Add(-48 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "yesterday", Timestamp: now.Add(-24 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "today", Timestamp: now})
	return now
}

func assertQueryCount(t *testing.T, storage Storage, opts QueryOptions, want int) {
	t.Helper()

	results, err := storage.GetExecutions(opts)
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}
	if len(results) != want {
		t.Errorf("Expected %d query results, got %d", want, len(results))
	}
}

func addPackageFilterExecutions(t *testing.T, storage Storage) {
	t.Helper()
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
}

func addAllToolsPackages(t *testing.T, storage Storage) {
	t.Helper()
	updatePackage(t, storage, &core.PackageInfo{Name: "pkg1", Tool: "npm"})
	updatePackage(t, storage, &core.PackageInfo{Name: "pkg2", Tool: "go"})
	updatePackage(t, storage, &core.PackageInfo{Name: "pkg3", Tool: "npm"})
}

func loadPackagesForTest(t *testing.T, storage Storage, tool string) []*core.PackageInfo {
	t.Helper()

	results, err := storage.GetPackages(tool)
	if err != nil {
		t.Fatalf("Failed to get packages: %v", err)
	}
	return results
}

func assertPackageNotFound(t *testing.T, storage Storage, tool, name string) {
	t.Helper()

	if _, err := storage.GetPackage(tool, name); err == nil {
		t.Error("Expected error for nonexistent package")
	}
}

func assertInspectionFileState(t *testing.T, inspection JSONInspection) {
	t.Helper()

	if !inspection.HasFile {
		t.Fatalf("inspection file state = %#v", inspection)
	}
	if inspection.SizeBytes == 0 {
		t.Fatalf("inspection file state = %#v", inspection)
	}
}

func assertInspectionCounts(t *testing.T, inspection JSONInspection) {
	t.Helper()

	if inspection.ExecutionCount != 2 {
		t.Fatalf("inspection counts = %#v", inspection)
	}
	if inspection.PackageCount != 1 {
		t.Fatalf("inspection counts = %#v", inspection)
	}
}

func assertInspectionLatest(t *testing.T, inspection JSONInspection) {
	t.Helper()

	if inspection.LatestExecution == nil {
		t.Fatalf("latest execution = %#v", inspection.LatestExecution)
	}
	if inspection.LatestExecution.ID != "latest" {
		t.Fatalf("latest execution = %#v", inspection.LatestExecution)
	}
}

func assertInspectionMetadata(t *testing.T, inspection JSONInspection) {
	t.Helper()

	if inspection.Statistics.TotalExecutions != 2 {
		t.Fatalf("inspection metadata = %#v", inspection)
	}
	if inspection.Metadata.LastUpdated.IsZero() {
		t.Fatalf("inspection metadata = %#v", inspection)
	}
}

func newRetentionPrepareStorage(t *testing.T) *JSONStorage {
	t.Helper()
	config := core.DefaultConfig()
	config.Storage.JSONFile = filepath.Join(t.TempDir(), "executions.json")
	config.Storage.RetentionDays = 1
	config.Storage.MaxExecutions = 0
	config.Storage.MaxStorageBytes = 0
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	return store
}

func assertExecutionCount(t *testing.T, executions []*core.ExecutionRecord, err error, want int) {
	t.Helper()

	if err != nil {
		t.Fatalf("legacy executions = %d, %v", len(executions), err)
	}
	if len(executions) != want {
		t.Fatalf("legacy executions = %d, %v", len(executions), err)
	}
}

func assertLegacyStorageFormat(t *testing.T, hasExecutionLog bool, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("legacy format detection = %v, %v", hasExecutionLog, err)
	}
	if hasExecutionLog {
		t.Fatalf("legacy format detection = %v, %v", hasExecutionLog, err)
	}
}

func newCompactionTestStorage(t *testing.T) (*JSONStorage, *core.Config) {
	t.Helper()
	config := core.DefaultConfig()
	config.Storage.JSONFile = filepath.Join(t.TempDir(), "executions.json")
	config.Storage.MaxStorageBytes = 4096
	config.Storage.MaxExecutions = 20
	store, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("NewJSONStorage failed: %v", err)
	}
	return store, config
}

func writeCompactionFixture(t *testing.T, path string, count int) {
	t.Helper()
	executions := compactionExecutions(count)
	data := compactionFixtureData(executions)
	writeCompactionFixtureData(t, path, data)
}

func compactionExecutions(count int) []core.ExecutionRecord {
	now := time.Now().Add(-time.Duration(count) * time.Minute)
	executions := make([]core.ExecutionRecord, 0, count)
	for index := 0; index < count; index++ {
		record := core.ExecutionRecord{
			ID:        executionFixtureID(index),
			Tool:      core.ToolHomebrew,
			Command:   strings.Repeat("x", 256),
			Timestamp: now.Add(time.Duration(index) * time.Minute),
		}
		executions = append(executions, record)
	}
	return executions
}

func writeCompactionFixtureData(t *testing.T, path string, data core.StorageData) {
	t.Helper()

	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent failed: %v", err)
	}
	if err := os.WriteFile(path, encoded, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func executionFixtureID(index int) string {
	return fmt.Sprintf("execution-%03d", index)
}

func compactionFixtureData(executions []core.ExecutionRecord) core.StorageData {
	packages := map[string]map[string]core.PackageInfo{
		core.ToolHomebrew: {
			"jq": {Name: "jq", Tool: core.ToolHomebrew},
		},
	}
	tombstones := map[string]map[string]int64{
		core.ToolNPM: {"old": 42},
	}
	return core.StorageData{
		Version:           "1.0.0",
		Metadata:          core.StorageMetadata{Created: time.Now()},
		Executions:        executions,
		Packages:          packages,
		PackageTombstones: tombstones,
		Statistics:        core.StorageStatistics{},
	}
}

func assertCompactedStorage(t *testing.T, store *JSONStorage, config *core.Config) {
	t.Helper()
	assertStorageSize(t, ExecutionLogPath(config.Storage.JSONFile), config.Storage.MaxStorageBytes)
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	if len(executions) == 0 {
		t.Fatalf("compacted execution count = %d", len(executions))
	}
	if len(executions) > config.Storage.MaxExecutions {
		t.Fatalf("compacted execution count = %d", len(executions))
	}
	if executions[0].ID != executionFixtureID(99) {
		t.Fatalf("newest execution = %q", executions[0].ID)
	}
	assertCompactedState(t, store, len(executions))
}

func assertCompactedState(t *testing.T, store *JSONStorage, executionCount int) {
	t.Helper()
	assertCompactedPackage(t, store)
	assertCompactedStatistics(t, store, executionCount)
	assertCompactedManifest(t, store)
}

func assertCompactedPackage(t *testing.T, store *JSONStorage) {
	t.Helper()

	if _, err := store.GetPackage(core.ToolHomebrew, "jq"); err != nil {
		t.Fatalf("package inventory was not preserved: %v", err)
	}
}

func assertCompactedStatistics(t *testing.T, store *JSONStorage, executionCount int) {
	t.Helper()

	statistics, err := store.Statistics()
	if err != nil {
		t.Fatalf("compacted statistics = %#v, %v", statistics, err)
	}
	if statistics.TotalExecutions != executionCount {
		t.Fatalf("compacted statistics = %#v, %v", statistics, err)
	}
}

func assertCompactedManifest(t *testing.T, store *JSONStorage) {
	t.Helper()

	stored := readCompactedManifest(t, store)
	assertCompactedManifestState(t, stored)
}

func readCompactedManifest(t *testing.T, store *JSONStorage) core.StorageData {
	t.Helper()

	data, err := os.ReadFile(store.filepath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var stored core.StorageData
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	return stored
}

func assertCompactedManifestState(t *testing.T, stored core.StorageData) {
	t.Helper()

	if stored.PackageTombstones[core.ToolNPM]["old"] != 42 {
		t.Fatalf("package tombstones = %#v", stored.PackageTombstones)
	}
	if stored.ExecutionLogFormat != executionLogFormat {
		t.Fatalf("execution log format = %q", stored.ExecutionLogFormat)
	}
	if len(stored.Executions) != 0 {
		t.Fatalf("manifest contains %d executions", len(stored.Executions))
	}
}

func assertStorageSize(t *testing.T, path string, maxBytes int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size() > maxBytes {
		t.Fatalf("storage size = %d, max = %d", info.Size(), maxBytes)
	}
}

func assertStorageContents(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("storage changed after failed compaction")
	}
}
