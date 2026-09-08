package storage

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

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

func assertStorageNotInitialized(t *testing.T, manifest string) {
	t.Helper()
	for _, path := range []string{manifest, ExecutionLogPath(manifest)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("initialization changed %s while locked: %v", path, err)
		}
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
