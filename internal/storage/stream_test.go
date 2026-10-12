package storage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func TestExecutionMinHeapMethods(t *testing.T) {
	earlier := &core.ExecutionRecord{Timestamp: time.Unix(1, 0)}
	later := &core.ExecutionRecord{Timestamp: time.Unix(2, 0)}
	records := executionMinHeap{}
	records.Push(later)
	records.Push(earlier)

	assertExecutionHeapOrder(t, records)
	records.Swap(0, 1)
	assertExecutionHeapPop(t, records, later)
}

func assertExecutionHeapOrder(t *testing.T, records executionMinHeap) {
	t.Helper()

	lengthMatches := records.Len() == 2
	orderingMatches := !records.Less(0, 1)
	heapMatches := lengthMatches && orderingMatches
	if !heapMatches {
		t.Fatalf("unexpected heap ordering: %#v", records)
	}
}

func assertExecutionHeapPop(t *testing.T, records executionMinHeap, want *core.ExecutionRecord) {
	t.Helper()

	got := records.Pop()
	popMatches := got == want
	remainingMatches := records.Len() == 1
	popResultMatches := popMatches && remainingMatches
	if !popResultMatches {
		t.Fatalf("Pop = %#v, heap = %#v", got, records)
	}
}

func TestStorageUsesExecutionLogRejectsUnknownFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.json")
	data := core.StorageData{Version: "1.0.0", ExecutionLogFormat: "unknown"}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if err := os.WriteFile(path, encoded, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	usesLog, err := storageUsesExecutionLog(path)
	rejectedFormat := errors.Is(err, ErrUnsupportedExecutionLogFormat)
	resultMatches := rejectedFormat && !usesLog
	if !resultMatches {
		t.Fatalf("format detection = %v, %v", usesLog, err)
	}
}

func TestStorageUsesExecutionLogFindsFormatAfterExecutions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.json")
	data := []byte(`{"version":"1.0.0","executions":[],"execution_log_format":"ndjson-v1"}`)
	if err := os.WriteFile(path, data, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	usesLog, err := storageUsesExecutionLog(path)
	resultMatches := err == nil && usesLog
	if !resultMatches {
		t.Fatalf("format detection = %v, %v", usesLog, err)
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

func TestExecutionRecordsAreReturnedAsCopies(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	record := copyTestExecutionRecord()
	addExecution(t, storage, record)
	mutateExecutionRecord(record)
	assertStoredExecutionCopy(t, storage)
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

func TestGetExecutions(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	addQueryTestExecutions(t, storage)
	assertQueryExecutionCount(t, storage, QueryOptions{Tool: "brew"}, 1)
	assertQueryExecutionCount(t, storage, QueryOptions{Limit: 2}, 2)
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

func TestPackagesAndStatsAreReturnedAsCopies(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	seedPackageAndStatsCopyTest(t, storage)
	mutatePackageCopy(t, storage)
	mutateStatsCopy(t, storage)
	assertStoredPackageAndStatsCopy(t, storage)
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

func TestQueryOptionsPackageFiltering(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	addPackageFilterExecutions(t, storage)
	assertQueryCount(t, storage, QueryOptions{Package: "express"}, 1)
	assertQueryCount(t, storage, QueryOptions{Package: "nonexistent"}, 0)
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

func TestGetPackagesAllTools(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	addAllToolsPackages(t, storage)
	results := loadPackagesForTest(t, storage, "")
	if len(results) != 3 {
		t.Errorf("Expected 3 packages, got %d", len(results))
	}
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

func TestPackageNotFound(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	assertPackageNotFound(t, storage, "npm", "nonexistent")
	assertPackageNotFound(t, storage, "nonexistent-tool", "pkg")
}

func assertPackageNotFound(t *testing.T, storage Storage, tool, name string) {
	t.Helper()

	if _, err := storage.GetPackage(tool, name); err == nil {
		t.Error("Expected error for nonexistent package")
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
