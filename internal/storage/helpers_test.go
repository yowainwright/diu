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

func TestWriteAllReturnsWriterError(t *testing.T) {
	want := errors.New("write failed")
	writer := failingWriter{err: want}
	if err := writeAll(writer, []byte("record")); !errors.Is(err, want) {
		t.Fatalf("writeAll error = %v", err)
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

func TestNextBackupPathUsesSuffixForCollision(t *testing.T) {
	storage := newTestStorage(t)
	defer closeStorage(t, storage)
	now := time.Date(2026, 6, 1, 12, 0, 0, 123, time.UTC)
	firstPath := nextBackupPathForTest(t, storage, now)
	if err := os.WriteFile(firstPath, []byte("{}"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create colliding backup path: %v", err)
	}
	nextPath := nextBackupPathForTest(t, storage, now)
	assertSuffixedBackupPath(t, firstPath, nextPath)
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

func TestCompactionLimitsReserveHeadroom(t *testing.T) {
	if got := compactedLimit(10*byteHeadroomThreshold, byteHeadroomThreshold); got != 9*byteHeadroomThreshold {
		t.Fatalf("byte limit with headroom = %d", got)
	}
	if got := compactedRecordLimit(10000); got != 9000 {
		t.Fatalf("record limit with headroom = %d", got)
	}
}

func nextBackupPathForTest(t *testing.T, storage *JSONStorage, now time.Time) string {
	t.Helper()

	path, err := storage.nextBackupPath(now)
	if err != nil {
		t.Fatalf("Failed to get backup path: %v", err)
	}
	return path
}

func assertSuffixedBackupPath(t *testing.T, firstPath, nextPath string) {
	t.Helper()

	want := firstPath + ".1"
	if nextPath != want {
		t.Fatalf("Expected suffixed backup path %s, got %s", want, nextPath)
	}
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

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}
