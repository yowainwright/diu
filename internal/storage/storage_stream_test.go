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

func TestAppendExecutionRecordsRejectsDirectoryPath(t *testing.T) {
	records := []core.ExecutionRecord{{Tool: core.ToolNPM}}
	if err := appendExecutionRecords(t.TempDir(), records); err == nil {
		t.Fatal("appendExecutionRecords succeeded with a directory path")
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
