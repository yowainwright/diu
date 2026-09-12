package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

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
