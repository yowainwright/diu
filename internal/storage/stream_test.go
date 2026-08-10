package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	lengthMatches := records.Len() == 2
	orderingMatches := !records.Less(0, 1)
	if !lengthMatches || !orderingMatches {
		t.Fatalf("unexpected heap ordering: %#v", records)
	}
	records.Swap(0, 1)
	got := records.Pop()
	popMatches := got == later
	remainingMatches := records.Len() == 1
	if !popMatches || !remainingMatches {
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
	if err == nil || usesLog || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("format detection = %v, %v", usesLog, err)
	}
}

func TestInspectJSONFileReportsMissingExecutionLog(t *testing.T) {
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
	if err == nil || !inspection.Exists {
		t.Fatalf("inspection = %#v, %v", inspection, err)
	}
}

func TestAppendExecutionRecordsRejectsDirectoryPath(t *testing.T) {
	records := []core.ExecutionRecord{{Tool: core.ToolNPM}}
	if err := appendExecutionRecords(t.TempDir(), records); err == nil {
		t.Fatal("appendExecutionRecords succeeded with a directory path")
	}
}
