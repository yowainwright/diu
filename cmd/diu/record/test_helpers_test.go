package record

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/storage"
)

const homebrewExecutionPayload = `{
	"tool":"brew",
	"command":"brew install jq",
	"args":["install","jq"],
	"exit_code":0,
	"duration_ms":1200,
	"packages_affected":["jq"]
}`

func setupTestHomeConfig(t *testing.T) *core.Config {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = []string{}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{}
	configPath := filepath.Join(homeDir, ".config", "diu", "config.json")
	if err := config.SaveTo(configPath); err != nil {
		t.Fatalf("Failed to save test config: %v", err)
	}
	return config
}

func openTestStore(t *testing.T, config *core.Config) storage.Storage {
	t.Helper()
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to open test storage: %v", err)
	}
	return store
}

func closeTestStore(t *testing.T, store storage.Storage) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Failed to close test storage: %v", err)
	}
}

func runRecordExecution(t *testing.T, payload string) {
	t.Helper()
	withStdin(t, payload, func() {
		if err := RunExecution(NewCommand(), nil); err != nil {
			t.Fatalf("RunExecution failed: %v", err)
		}
	})
}

func assertStoredHomebrewExecution(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{Tool: core.ToolHomebrew})
	if err != nil {
		t.Fatal(err)
	}
	isSingleRecord := len(records) == 1
	isHomebrewRecord := isSingleRecord && records[0].Tool == core.ToolHomebrew
	if !isHomebrewRecord {
		t.Fatalf("homebrew records = %+v", records)
	}
}

func assertFallbackRecordDropped(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("concurrent fallback stored %d executions", len(records))
	}
	_, contended, err := observability.ReadFallbackContention(config.Daemon.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !contended {
		t.Fatal("fallback contention was not recorded")
	}
}

func acquireFallbackRecordLockForTest(t *testing.T, config *core.Config) *os.File {
	t.Helper()
	lock, acquired, err := tryAcquireFallbackRecordLock(config.Storage.JSONFile)
	if err != nil {
		t.Fatalf("tryAcquireFallbackRecordLock error = %v", err)
	}
	if !acquired {
		t.Fatal("tryAcquireFallbackRecordLock did not acquire the lock")
	}
	return lock
}

func releaseFallbackRecordLockForTest(t *testing.T, lock *os.File) {
	t.Helper()
	if err := releaseFallbackRecordLock(lock); err != nil {
		t.Fatalf("releaseFallbackRecordLock failed: %v", err)
	}
}

func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = oldStdin
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	fn()
}
