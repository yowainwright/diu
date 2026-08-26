package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func TestCleanupCompactsOversizedStorage(t *testing.T) {
	store, config := newCompactionTestStorage(t)
	writeCompactionFixture(t, config.Storage.JSONFile, 100)
	if err := store.Cleanup(time.Time{}); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	assertCompactedStorage(t, store, config)
}

func TestGetExecutionsStreamsLegacyStorageBeforeMigration(t *testing.T) {
	store, config := newCompactionTestStorage(t)
	writeCompactionFixture(t, config.Storage.JSONFile, 7)
	executions, err := store.GetExecutions(QueryOptions{})
	if err != nil || len(executions) != 7 {
		t.Fatalf("legacy executions = %d, %v", len(executions), err)
	}
	usesLog, err := storageUsesExecutionLog(config.Storage.JSONFile)
	if err != nil || usesLog {
		t.Fatalf("legacy format detection = %v, %v", usesLog, err)
	}
}

func TestAddExecutionCompactsBeforeReload(t *testing.T) {
	store, config := newCompactionTestStorage(t)
	writeCompactionFixture(t, config.Storage.JSONFile, 100)
	record := &core.ExecutionRecord{ID: "new", Tool: core.ToolNPM, Timestamp: time.Now()}
	if err := store.AddExecution(record); err != nil {
		t.Fatalf("AddExecution failed: %v", err)
	}
	if _, err := store.GetExecutionByID(record.ID); err != nil {
		t.Fatalf("new execution was not retained: %v", err)
	}
	assertStorageSize(t, ExecutionLogPath(config.Storage.JSONFile), config.Storage.MaxStorageBytes)
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

func TestCompactionLimitsReserveHeadroom(t *testing.T) {
	if got := compactedLimit(10*byteHeadroomThreshold, byteHeadroomThreshold); got != 9*byteHeadroomThreshold {
		t.Fatalf("byte limit with headroom = %d", got)
	}
	if got := compactedRecordLimit(10000); got != 9000 {
		t.Fatalf("record limit with headroom = %d", got)
	}
}

func TestWriteCompactedStorageCleansUpAfterRenameFailure(t *testing.T) {
	targetDirectory := t.TempDir()
	records := []compactExecution{{data: []byte(`{"id":"one"}`)}}
	if err := writeCompactedStorage(targetDirectory, records); err == nil {
		t.Fatal("writeCompactedStorage succeeded with a directory target")
	}
	pattern := "." + filepath.Base(targetDirectory) + ".compact-*"
	temporaryPattern := filepath.Join(filepath.Dir(targetDirectory), pattern)
	temporaryFiles, err := filepath.Glob(temporaryPattern)
	if err != nil || len(temporaryFiles) != 0 {
		t.Fatalf("temporary compaction files = %#v, %v", temporaryFiles, err)
	}
}

func TestWriteAllReturnsWriterError(t *testing.T) {
	want := errors.New("write failed")
	writer := failingWriter{err: want}
	if err := writeAll(writer, []byte("record")); !errors.Is(err, want) {
		t.Fatalf("writeAll error = %v", err)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
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
	data := compactionFixtureData(executions)
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
	if len(executions) == 0 || len(executions) > config.Storage.MaxExecutions {
		t.Fatalf("compacted execution count = %d", len(executions))
	}
	if executions[0].ID != executionFixtureID(99) {
		t.Fatalf("newest execution = %q", executions[0].ID)
	}
	assertCompactedState(t, store, len(executions))
}

func assertCompactedState(t *testing.T, store *JSONStorage, executionCount int) {
	t.Helper()
	if _, err := store.GetPackage(core.ToolHomebrew, "jq"); err != nil {
		t.Fatalf("package inventory was not preserved: %v", err)
	}
	statistics, err := store.GetStatistics()
	if err != nil || statistics.TotalExecutions != executionCount {
		t.Fatalf("compacted statistics = %#v, %v", statistics, err)
	}
	data, err := os.ReadFile(store.filepath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var stored core.StorageData
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
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
