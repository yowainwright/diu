package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/storage"
)

type mockStorage struct {
	storage.Storage
	mu           sync.RWMutex
	executions   []*core.ExecutionRecord
	packages     map[string][]*core.PackageInfo
	isClosed     bool
	addErr       error
	getErr       error
	packagesErr  error
	statsErr     error
	batchCalls   int
	backupCalls  int
	cleanupCalls int
	prepareCalls int
	cleanupErr   error
	lastQuery    storage.QueryOptions
}

const (
	enrichRawToolName           = "brew"
	enrichCommandName           = "brew install wget"
	enrichInstallSubcommand     = "install"
	enrichPackageName           = "wget"
	enrichSubcommandMetadataKey = "subcommand"
	enrichExpectedPackageCount  = 1
)

func TestNewDaemon(t *testing.T) {
	cfg := testConfig(t)
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	assertNewDaemon(t, d, cfg)
}

func TestNewDaemonRejectsInvalidStoragePath(t *testing.T) {
	cfg := testConfig(t)
	cfg.Storage.JSONFile = ""

	d, err := NewDaemon(cfg)
	invalidResult := d != nil || err == nil
	if invalidResult {
		t.Fatalf("NewDaemon = %#v, %v", d, err)
	}
	if !strings.Contains(err.Error(), "failed to initialize storage") {
		t.Fatalf("NewDaemon error = %v", err)
	}
}

func TestNewDaemonWithMonitors(t *testing.T) {
	cfg := testConfig(t)
	cfg.Monitoring.EnabledTools = []string{"homebrew", "npm", "go"}

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	monitors := d.registry.All()
	if len(monitors) != 3 {
		t.Errorf("Expected 3 monitors, got %d", len(monitors))
	}
}

func TestNewDaemonRegistersEverySupportedMonitor(t *testing.T) {
	setFakeCommandsInPath(t, "brew", "npm", "pnpm", "bun", "go", "pip3", "uv", "poetry")
	cfg := testConfig(t)
	cfg.Monitoring.EnabledTools = allSupportedMonitorTools()
	d := newDaemonForTest(t, cfg)
	defer closeStorageForTest(t, d.storage)
	assertRegisteredMonitorCount(t, d, len(cfg.Monitoring.EnabledTools))
}

func TestNewDaemonIgnoresUnknownMonitor(t *testing.T) {
	cfg := testConfig(t)
	cfg.Monitoring.EnabledTools = []string{"unknown_tool"}

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon should not fail for unknown tools: %v", err)
	}

	monitors := d.registry.All()
	if len(monitors) != 0 {
		t.Errorf("Expected 0 monitors for unknown tool, got %d", len(monitors))
	}
}

func TestStartCompactsStorageBeforeReady(t *testing.T) {
	config := testConfig(t)
	config.Storage.MaxExecutions = 5
	config.Storage.MaxStorageBytes = 4096
	writeOversizedDaemonStorage(t, config.Storage.JSONFile)
	d := startDaemonForCompactionTest(t, config)
	defer stopDaemonForTest(t, d)
	assertDaemonCompactedStorage(t, config)
	if !IsRunning(config) {
		t.Fatal("daemon was not ready after storage compaction")
	}
}

func TestStartAndStop(t *testing.T) {
	cfg := testConfig(t)
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	assertDaemonStarted(t, d, cfg)
	stopDaemonForTest(t, d)
	assertDaemonStopped(t, d, cfg)
	assertDaemonLifecycleLogged(t, cfg)
}

func TestStartReportsPIDFileFailure(t *testing.T) {
	cfg := testConfig(t)
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("file"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	cfg.Daemon.PIDFile = filepath.Join(blocked, "diu.pid")

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	defer closeStorageForTest(t, d.storage)

	err = d.Start()
	assertPIDFileStartError(t, err)
	assertStartupFailureLogged(t, cfg, err)
}

func TestStartPreservesRunningDaemonPID(t *testing.T) {
	cfg := testConfig(t)
	first := startDaemonForTestConfig(t, cfg)
	defer stopDaemonForTest(t, first)
	wantPID := readPIDFileForTest(t, cfg)

	second := newDaemonForTest(t, cfg)
	if err := second.Start(); err == nil {
		t.Fatal("second daemon start succeeded")
	}
	gotPID := readPIDFileForTest(t, cfg)
	assertPIDFileUnchanged(t, wantPID, gotPID)
}

func TestStartReclaimsUnlockedPIDForLiveUnrelatedProcess(t *testing.T) {
	cfg := testConfig(t)
	pid := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(cfg.Daemon.PIDFile, []byte(pid), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("Start failed with stale reused PID: %v", err)
	}
	stopDaemonForTest(t, d)
}

func TestStartSupportsContextCancellation(t *testing.T) {
	cfg := testConfig(t)
	d := newDaemonForTest(t, cfg)
	startDaemonForCancellation(t, d)

	d.cancel()
	time.Sleep(100 * time.Millisecond)
	assertDaemonContextCanceled(t, d)
	stopDaemonForTest(t, d)
}

func TestPrepareStorage(t *testing.T) {
	store := newMockStorage()
	d := &Daemon{storage: store}
	if err := d.prepareStorage(); err != nil {
		t.Fatalf("prepareStorage failed: %v", err)
	}
	if store.prepareCalls != 1 {
		t.Fatalf("prepare calls = %d", store.prepareCalls)
	}
	if store.cleanupCalls != 0 {
		t.Fatalf("cleanup calls = %d", store.cleanupCalls)
	}
}

func TestPrepareStorageFailure(t *testing.T) {
	store := newMockStorage()
	store.cleanupErr = errors.New("cleanup failed")
	d := &Daemon{storage: store}
	err := d.prepareStorage()
	hasPrepareError := err != nil && strings.Contains(err.Error(), "failed to prepare storage")
	if !hasPrepareError {
		t.Fatalf("prepareStorage error = %v", err)
	}
}

func TestStartInventoryRefreshCanBeDisabled(t *testing.T) {
	cfg := testConfig(t)
	d, _ := newEventDaemon(t, cfg, 0)
	defer d.cancel()
	calls := make(chan context.Context, 1)
	d.SetInventoryRefresh(func(ctx context.Context) error {
		calls <- ctx
		return nil
	})
	d.startInventoryRefresh()
	assertNoRefreshCall(t, calls)
}

func TestRunInventoryRefreshRetriesAndStops(t *testing.T) {
	cfg := testConfig(t)
	cfg.Monitoring.Filesystem.ScanInterval = time.Millisecond
	d, _ := newEventDaemon(t, cfg, 0)
	calls := make(chan context.Context, 1)
	d.SetInventoryRefresh(func(ctx context.Context) error {
		select {
		case calls <- ctx:
		case <-ctx.Done():
		}
		return errors.New("temporary failure")
	})
	d.startInventoryRefresh()
	defer d.cancel()
	awaitRefreshCall(t, calls)
	ctx := awaitRefreshCall(t, calls)
	d.cancel()
	assertRefreshStopped(t, d)
	if ctx.Err() == nil {
		t.Fatal("refresh context was not canceled")
	}
}

func TestRunInventoryRefreshDoesNotOverlap(t *testing.T) {
	cfg := testConfig(t)
	cfg.Monitoring.Filesystem.ScanInterval = time.Millisecond
	d, _ := newEventDaemon(t, cfg, 0)
	calls := make(chan context.Context, 2)
	d.SetInventoryRefresh(func(ctx context.Context) error {
		calls <- ctx
		<-ctx.Done()
		return ctx.Err()
	})
	d.startInventoryRefresh()
	defer d.cancel()
	awaitRefreshCall(t, calls)
	assertNoRefreshCall(t, calls)
	d.cancel()
	assertRefreshStopped(t, d)
}

func TestStopIsIdempotent(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := d.Stop(); err != nil {
		t.Fatalf("First Stop failed: %v", err)
	}

	if err := d.Stop(); err != nil {
		t.Fatalf("Second Stop should not fail: %v", err)
	}
}

func TestWaitUnblocksAfterStop(t *testing.T) {
	cfg := testConfig(t)
	d := startDaemonForWaitTest(t, cfg)
	done := waitForDaemonInBackground(d)

	stopDaemonForWaitTest(t, d)
	assertDaemonWaitDone(t, done)
}

func TestProcessEvents(t *testing.T) {
	cfg := testConfig(t)
	d := newDaemonForTest(t, cfg)
	mockStore := newMockStorage()
	d.storage = mockStore
	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer stopDaemonForTest(t, d)

	sendDaemonEvent(t, d, eventProcessingRecord())
	time.Sleep(100 * time.Millisecond)
	if mockStore.executionCount() != 1 {
		t.Errorf("Expected 1 execution, got %d", mockStore.executionCount())
	}
}

func TestProcessEventsConcurrently(t *testing.T) {
	cfg := testConfig(t)
	d := startDaemonWithMockStorage(t, cfg)
	defer stopDaemonForTest(t, d)
	mockStore := d.storage.(*mockStorage)
	eventCount := 50

	sendConcurrentDaemonEvents(d, eventCount)
	assertMockExecutionCount(t, mockStore, eventCount)
}

func TestProcessEventsChannelClose(t *testing.T) {
	cfg := testConfig(t)
	d, mockStore := newEventDaemon(t, cfg, 0)
	done := startProcessEvents(d)

	queueDaemonEvent(d, "test", "homebrew")
	time.Sleep(50 * time.Millisecond)
	close(d.eventChan)
	assertProcessEventsDone(t, done, "processEvents did not exit after channel close")
	assertMockExecutionCount(t, mockStore, 1)
	assertBatchCalls(t, mockStore, 1)
}

func TestProcessEventsDrainsQueuedEventsOnCancel(t *testing.T) {
	cfg := testConfig(t)
	d, mockStore := newEventDaemon(t, cfg, 2)

	queueDaemonEvent(d, "one", "homebrew")
	queueDaemonEvent(d, "two", "npm")
	d.cancel()
	done := startProcessEvents(d)
	assertProcessEventsDone(t, done, "processEvents did not exit after cancellation")
	assertMockExecutionCount(t, mockStore, 2)
}

func TestProcessEventsRejectsEventsAfterFinalDrain(t *testing.T) {
	d, mock := newEventDaemon(t, testConfig(t), 64)
	done := startProcessEvents(d)
	d.cancel()
	assertProcessEventsDone(t, done, "event consumer did not stop")
	body := `{"tool":"npm","command":"install"}`
	for range 32 {
		assertStoppedDaemonRejectsExecution(t, d, body)
		d.admitSocketExecution(&core.ExecutionRecord{Tool: core.ToolNPM, Command: "install"})
	}
	if len(d.eventChan) != 0 {
		t.Fatalf("%d socket events were admitted after the final drain", len(d.eventChan))
	}
	assertMockExecutionCount(t, mock, 0)
}

func TestProcessEventsPersistsAcceptedHTTPEvents(t *testing.T) {
	d, mock := newEventDaemon(t, testConfig(t), 1)
	body := `{"tool":"npm","command":"install"}`
	first := handleExecutionAPIRequest(d, http.MethodPost, "/api/v1/executions", body)
	assertResponseStatus(t, first, http.StatusAccepted)
	responses := concurrentExecutionRequests(d, body, 32)
	d.cancel()
	done := startProcessEvents(d)
	accepted := 1 + countAcceptedExecutionResponses(t, responses, 32)
	assertProcessEventsDone(t, done, "event consumer did not stop")
	assertMockExecutionCount(t, mock, accepted)
	if len(d.eventChan) != 0 {
		t.Fatal("shutdown stranded admitted events")
	}
}

func TestEnrichExecution(t *testing.T) {
	cfg := testConfig(t)
	cfg.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	d := newDaemonForTest(t, cfg)
	defer closeStorageForTest(t, d.storage)

	record := &core.ExecutionRecord{
		Tool:    enrichRawToolName,
		Command: enrichCommandName,
		Args:    []string{enrichInstallSubcommand, enrichPackageName},
	}

	d.enrichExecution(record)
	assertEnrichedHomebrewExecution(t, record)
}

func TestEnrichExecutionPreservesLaunchAttemptIdentity(t *testing.T) {
	d := newDaemonForTest(t, testConfig(t))
	defer closeStorageForTest(t, d.storage)
	usedAt := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	record := daemonLaunchAttemptRecord(usedAt)
	d.enrichExecution(record)
	assertDaemonLaunchAttemptIdentity(t, record, usedAt)
}

func TestRunPeriodicCleanupRunsConfiguredBackups(t *testing.T) {
	d, mock := startBackupCleanupDaemon(t)
	waitForBackupCall(t, mock)
	d.cancel()
	d.wg.Wait()
	if backupCallCount(mock) == 0 {
		t.Fatal("configured storage backup did not run")
	}
}

func TestPruneOldRecordsHandlesCleanupError(t *testing.T) {
	cfg := testConfig(t)
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mock := newMockStorage()
	d.storage = mock

	d.pruneOldRecords()
}

func TestIsRunning(t *testing.T) {
	cfg := testConfig(t)

	assertDaemonIsNotRunning(t, cfg, "missing PID file")
	writePIDFileForTest(t, cfg, "invalid")
	assertDaemonIsNotRunning(t, cfg, "invalid PID")
	writePIDFileForTest(t, cfg, strconv.Itoa(os.Getpid())+"\n")
	assertDaemonIsNotRunning(t, cfg, "current process without socket")
	writePIDFileForTest(t, cfg, "999999999")
	assertDaemonIsNotRunning(t, cfg, "non-existent process")
	removeFileForTest(t, cfg.Daemon.PIDFile)
}

func TestIsRunningRejectsDirectoryAsPIDFile(t *testing.T) {
	cfg := testConfig(t)
	cfg.Daemon.PIDFile = t.TempDir()
	if IsRunning(cfg) {
		t.Fatal("directory reported as a running daemon PID file")
	}
}

func TestRequestStopSignalsProcessWhenSocketIsUnavailable(t *testing.T) {
	cfg := testConfig(t)
	pid := 4242
	lockTestPIDFile(t, cfg.Daemon.PIDFile, pid)
	originalSignaler := daemonProcessSignaler
	t.Cleanup(func() {
		daemonProcessSignaler = originalSignaler
	})
	signaledPID := 0
	daemonProcessSignaler = func(gotPID int, signal os.Signal) error {
		signaledPID = gotPID
		if signal != syscall.SIGTERM {
			t.Fatalf("signal = %v, want SIGTERM", signal)
		}
		return nil
	}

	if err := RequestStop(cfg); err != nil {
		t.Fatalf("RequestStop failed: %v", err)
	}
	if signaledPID != pid {
		t.Fatalf("signaled PID = %d, want %d", signaledPID, pid)
	}
}

func TestRequestStopRemovesUnlockedPIDFile(t *testing.T) {
	cfg := testConfig(t)
	pid := 4242
	if err := os.WriteFile(cfg.Daemon.PIDFile, []byte(strconv.Itoa(pid)), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	restoreDaemonControlHooks(t)
	signaled := false
	daemonProcessSignaler = func(int, os.Signal) error {
		signaled = true
		return nil
	}

	err := RequestStop(cfg)
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestStop error = %v", err)
	}
	if signaled {
		t.Fatal("unverified PID was signaled")
	}
	if _, err := os.Stat(cfg.Daemon.PIDFile); !os.IsNotExist(err) {
		t.Fatalf("stale PID file still exists: %v", err)
	}
}

func TestRequestStopSignalsProcessWhenStopRequestFails(t *testing.T) {
	cfg := testConfig(t)
	pid := 4242
	lockTestPIDFile(t, cfg.Daemon.PIDFile, pid)
	restoreDaemonControlHooks(t)
	daemonSocketControlSender = failingStopControlSender(pid)
	signaledPID := 0
	daemonProcessSignaler = func(gotPID int, signal os.Signal) error {
		signaledPID = gotPID
		return nil
	}

	if err := RequestStop(cfg); err != nil {
		t.Fatalf("RequestStop failed: %v", err)
	}
	if signaledPID != pid {
		t.Fatalf("signaled PID = %d, want %d", signaledPID, pid)
	}
}

func TestReadPID(t *testing.T) {
	cfg := testConfig(t)
	if err := os.WriteFile(cfg.Daemon.PIDFile, []byte("4242"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	pid, err := ReadPID(cfg)
	readExpectedPID := err == nil && pid == 4242
	if !readExpectedPID {
		t.Fatalf("ReadPID = %d, %v", pid, err)
	}
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		executions: make([]*core.ExecutionRecord, 0),
		packages:   make(map[string][]*core.PackageInfo),
	}
}

func (m *mockStorage) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isClosed = true
	return nil
}

func (m *mockStorage) AddExecution(record *core.ExecutionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addErr != nil {
		return m.addErr
	}
	m.executions = append(m.executions, record)
	return nil
}

func (m *mockStorage) AddExecutions(records []*core.ExecutionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addErr != nil {
		return m.addErr
	}
	m.batchCalls++
	m.executions = append(m.executions, records...)
	return nil
}

func (m *mockStorage) GetExecutions(opts storage.QueryOptions) ([]*core.ExecutionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastQuery = opts
	if m.getErr != nil {
		return nil, m.getErr
	}
	result := filterMockExecutions(m.executions, opts.Tool)
	result = limitMockExecutions(result, opts.Limit)
	return result, nil
}

func filterMockExecutions(executions []*core.ExecutionRecord, tool string) []*core.ExecutionRecord {
	result := make([]*core.ExecutionRecord, 0)
	for _, execution := range executions {
		shouldSkip := tool != "" && execution.Tool != tool
		if shouldSkip {
			continue
		}
		result = append(result, execution)
	}
	return result
}

func limitMockExecutions(executions []*core.ExecutionRecord, limit int) []*core.ExecutionRecord {
	shouldReturnAll := limit <= 0 || len(executions) <= limit
	if shouldReturnAll {
		return executions
	}
	return executions[:limit]
}

func (m *mockStorage) UpdatePackage(pkg *core.PackageInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.packages[pkg.Tool] = append(m.packages[pkg.Tool], pkg)
	return nil
}

func (m *mockStorage) GetPackages(tool string) ([]*core.PackageInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.packagesErr != nil {
		return nil, m.packagesErr
	}
	if tool == "" {
		var all []*core.PackageInfo
		for _, pkgs := range m.packages {
			all = append(all, pkgs...)
		}
		return all, nil
	}
	return m.packages[tool], nil
}

//nolint:legibility // storage.Storage requires this method name.
func (m *mockStorage) Statistics() (*core.StorageStatistics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.statsErr != nil {
		return nil, m.statsErr
	}
	return &core.StorageStatistics{
		TotalExecutions: len(m.executions),
		ExecutionFrequency: map[string]int{
			"homebrew": 5,
			"npm":      3,
		},
	}, nil
}

func (m *mockStorage) Backup() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backupCalls++
	return nil
}

func (m *mockStorage) Cleanup(before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupCalls++
	if m.cleanupErr != nil {
		return m.cleanupErr
	}
	filtered := make([]*core.ExecutionRecord, 0)
	for _, e := range m.executions {
		if e.Timestamp.After(before) {
			filtered = append(filtered, e)
		}
	}
	m.executions = filtered
	return nil
}

func (m *mockStorage) Prepare() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prepareCalls++
	return m.cleanupErr
}

func startDaemonForCompactionTest(t *testing.T, config *core.Config) *Daemon {
	t.Helper()

	d, err := NewDaemon(config)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	return d
}

func assertDaemonCompactedStorage(t *testing.T, config *core.Config) {
	t.Helper()

	executionPath := storage.ExecutionLogPath(config.Storage.JSONFile)
	info, err := os.Stat(executionPath)
	if err != nil {
		t.Fatalf("compacted storage = %v, %v", info, err)
	}
	if info.Size() > config.Storage.MaxStorageBytes {
		t.Fatalf("compacted storage = %v, %v", info, err)
	}
}

func writeOversizedDaemonStorage(t *testing.T, path string) {
	t.Helper()
	records := make([]core.ExecutionRecord, 20)
	for index := range records {
		records[index] = core.ExecutionRecord{
			ID:        executionID(index),
			Tool:      core.ToolNPM,
			Command:   strings.Repeat("x", 512),
			Timestamp: time.Now(),
		}
	}
	data := core.StorageData{Version: "1.0.0", Executions: records}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if err := os.WriteFile(path, encoded, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func executionID(index int) string {
	return fmt.Sprintf("execution-%d", index)
}

func (m *mockStorage) executionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.executions)
}

func testConfig(t *testing.T) *core.Config {
	t.Helper()
	tmpDir := t.TempDir()
	socketPath := shortSocketPath(t)
	return &core.Config{
		Version:    "1.0",
		Daemon:     testDaemonConfig(tmpDir, socketPath),
		Storage:    testStorageConfig(tmpDir),
		Monitoring: core.MonitoringConfig{EnabledTools: []string{}},
		API:        testAPIConfig(),
	}
}

func testDaemonConfig(tmpDir, socketPath string) core.DaemonConfig {
	return core.DaemonConfig{
		Port:       0,
		LogLevel:   "info",
		DataDir:    tmpDir,
		PIDFile:    filepath.Join(tmpDir, "diu.pid"),
		SocketPath: socketPath,
	}
}

func testStorageConfig(tmpDir string) core.StorageConfig {
	return core.StorageConfig{
		Backend:       "json",
		JSONFile:      filepath.Join(tmpDir, "executions.json"),
		RetentionDays: 365,
	}
}

func testAPIConfig() core.APIConfig {
	return core.APIConfig{
		IsEnabled: false,
		Host:      "127.0.0.1",
		Port:      0,
	}
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "diu-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("RemoveAll failed: %v", err)
		}
	})
	return filepath.Join(dir, "diu.sock")
}

func setFakeCommandsInPath(t *testing.T, commands ...string) {
	t.Helper()
	binDir := t.TempDir()
	for _, command := range commands {
		commandPath := filepath.Join(binDir, command)
		if err := os.WriteFile(commandPath, []byte("#!/bin/sh\n"), core.OwnerExecutableMode); err != nil {
			t.Fatalf("write fake %s command: %v", command, err)
		}
	}
	t.Setenv("PATH", binDir)
}

func stopDaemonForTest(t *testing.T, d *Daemon) {
	t.Helper()
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func closeStorageForTest(t *testing.T, store storage.Storage) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Storage close failed: %v", err)
	}
}

func closeForTest(t *testing.T, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func addMockExecution(t *testing.T, store *mockStorage, record *core.ExecutionRecord) {
	t.Helper()
	if err := store.AddExecution(record); err != nil {
		t.Fatalf("AddExecution failed: %v", err)
	}
}

func updateMockPackage(t *testing.T, store *mockStorage, pkg *core.PackageInfo) {
	t.Helper()
	if err := store.UpdatePackage(pkg); err != nil {
		t.Fatalf("UpdatePackage failed: %v", err)
	}
}

func removeFileForTest(t *testing.T, path string) {
	t.Helper()
	err := os.Remove(path)
	if err == nil {
		return
	}
	if os.IsNotExist(err) {
		return
	}
	t.Fatalf("Remove failed: %v", err)
}

func decodeRecorderJSON(t *testing.T, recorder *httptest.ResponseRecorder, target interface{}) {
	t.Helper()
	response := recorder.Result()
	defer closeForTest(t, response.Body)
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
}

func assertNewDaemon(t *testing.T, d *Daemon, cfg *core.Config) {
	t.Helper()
	assertDaemonInstance(t, d)
	assertDaemonConfig(t, d, cfg)
	assertDaemonComponents(t, d)
	assertDaemonEventBuffer(t, d)
}

func assertDaemonInstance(t *testing.T, d *Daemon) {
	t.Helper()
	if d == nil {
		t.Fatal("Expected daemon to be non-nil")
	}
}

func assertDaemonConfig(t *testing.T, d *Daemon, cfg *core.Config) {
	t.Helper()
	if d.config != cfg {
		t.Error("Config not set correctly")
	}
}

func assertDaemonComponents(t *testing.T, d *Daemon) {
	t.Helper()
	if d.storage == nil {
		t.Error("Storage not initialized")
	}
	if d.registry == nil {
		t.Error("Registry not initialized")
	}
	if d.eventChan == nil {
		t.Error("Event channel not initialized")
	}
}

func assertDaemonEventBuffer(t *testing.T, d *Daemon) {
	t.Helper()
	if cap(d.eventChan) != core.DefaultEventBuffer {
		t.Errorf("Event channel capacity: got %d, want %d", cap(d.eventChan), core.DefaultEventBuffer)
	}
}

func assertDaemonStarted(t *testing.T, d *Daemon, cfg *core.Config) {
	t.Helper()
	assertPIDFileCreated(t, cfg)
	assertDaemonRuntimeStarted(t, d, cfg)
	assertDaemonSocketCreated(t, cfg)
}

func assertPIDFileCreated(t *testing.T, cfg *core.Config) {
	t.Helper()
	info, err := os.Stat(cfg.Daemon.PIDFile)
	if os.IsNotExist(err) {
		t.Error("PID file not created")
	}
	hasWrongPIDMode := err == nil && info.Mode().Perm() != core.PrivateFileMode
	if hasWrongPIDMode {
		t.Errorf("PID file mode: got %v, want %v", info.Mode().Perm(), core.PrivateFileMode)
	}
	locked, err := pidFileLocked(cfg.Daemon.PIDFile, os.Getpid())
	lockMissing := err != nil || !locked
	if lockMissing {
		t.Fatalf("PID file lock = %v, %v", locked, err)
	}
}

func assertDaemonRuntimeStarted(t *testing.T, d *Daemon, cfg *core.Config) {
	t.Helper()
	if d.IsStopped() {
		t.Error("Daemon should not be stopped after Start")
	}
	if !IsRunning(cfg) {
		t.Error("Daemon should respond to an identity check after Start")
	}
}

func assertDaemonSocketCreated(t *testing.T, cfg *core.Config) {
	t.Helper()
	socketInfo, err := os.Stat(cfg.Daemon.SocketPath)
	if err != nil {
		t.Fatalf("Failed to stat daemon socket: %v", err)
	}
	if socketInfo.Mode().Perm() != core.PrivateFileMode {
		t.Errorf("Socket mode: got %v, want %v", socketInfo.Mode().Perm(), core.PrivateFileMode)
	}
}

func assertDaemonStopped(t *testing.T, d *Daemon, cfg *core.Config) {
	t.Helper()
	if !d.IsStopped() {
		t.Error("Daemon should be stopped after Stop")
	}
	if _, err := os.Stat(cfg.Daemon.PIDFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed after Stop")
	}
}

func assertDaemonLifecycleLogged(t *testing.T, cfg *core.Config) {
	t.Helper()
	lines, err := observability.ReadRecentLogs(cfg.Daemon.DataDir)
	if err != nil {
		t.Fatalf("ReadRecentLogs failed: %v", err)
	}
	logs := strings.Join(lines, "\n")
	hasStartLog := strings.Contains(logs, "Starting DIU daemon")
	hasStopLog := strings.Contains(logs, "DIU daemon stopped")
	hasLifecycleLogs := hasStartLog && hasStopLog
	if !hasLifecycleLogs {
		t.Fatalf("Daemon lifecycle missing from local log: %q", logs)
	}
}

func assertPIDFileStartError(t *testing.T, err error) {
	t.Helper()
	hasPIDFileError := err != nil && strings.Contains(err.Error(), "PID file")
	if !hasPIDFileError {
		t.Fatalf("Start error = %v", err)
	}
}

func assertStartupFailureLogged(t *testing.T, cfg *core.Config, err error) {
	t.Helper()
	lines, logErr := observability.ReadRecentLogs(cfg.Daemon.DataDir)
	if logErr != nil {
		t.Fatalf("ReadRecentLogs failed: %v", logErr)
	}
	if !strings.Contains(strings.Join(lines, "\n"), err.Error()) {
		t.Fatalf("startup failure missing from logs: %#v", lines)
	}
}

func startDaemonForTestConfig(t *testing.T, cfg *core.Config) *Daemon {
	t.Helper()

	d := newDaemonForTest(t, cfg)
	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	return d
}

func newDaemonForTest(t *testing.T, cfg *core.Config) *Daemon {
	t.Helper()

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	return d
}

func readPIDFileForTest(t *testing.T, cfg *core.Config) []byte {
	t.Helper()

	pid, err := os.ReadFile(cfg.Daemon.PIDFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	return pid
}

func assertPIDFileUnchanged(t *testing.T, wantPID, gotPID []byte) {
	t.Helper()

	if string(gotPID) != string(wantPID) {
		t.Fatalf("running daemon PID changed from %q to %q", wantPID, gotPID)
	}
}

func eventProcessingRecord() *core.ExecutionRecord {
	return &core.ExecutionRecord{
		ID:        "test-1",
		Tool:      "homebrew",
		Command:   "install",
		Args:      []string{"wget"},
		Timestamp: time.Now(),
	}
}

func sendDaemonEvent(t *testing.T, d *Daemon, record *core.ExecutionRecord) {
	t.Helper()
	select {
	case d.eventChan <- record:
	case <-time.After(time.Second):
		t.Fatal("Failed to send event to channel")
	}
}

func daemonLaunchAttemptRecord(usedAt time.Time) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Kind: core.ExecutionKindLaunchAttempt, Tool: "npm", Command: "prettier",
		Timestamp: usedAt, PackagesAffected: []string{"prettier"},
		Metadata: map[string]interface{}{"executable": "prettier"},
	}
}

func assertDaemonLaunchAttemptIdentity(t *testing.T, record *core.ExecutionRecord, usedAt time.Time) {
	t.Helper()
	hasPackage := len(record.PackagesAffected) == 1 && record.PackagesAffected[0] == "prettier"
	toolMatches := record.Tool == core.ToolNPM
	commandMatches := record.Command == "prettier"
	identityPreserved := toolMatches && commandMatches && hasPackage
	if !identityPreserved {
		t.Fatalf("launch identity changed during daemon enrichment: %#v", record)
	}
	if !record.Timestamp.Equal(usedAt) {
		t.Fatalf("launch timestamp = %s, want %s", record.Timestamp, usedAt)
	}
}

func assertEnrichedHomebrewExecution(t *testing.T, record *core.ExecutionRecord) {
	t.Helper()
	if record.Tool != core.ToolHomebrew {
		t.Errorf("Expected normalized tool %q, got %q", core.ToolHomebrew, record.Tool)
	}

	hasPackageCount := len(record.PackagesAffected) == enrichExpectedPackageCount
	hasPackageName := hasPackageCount && record.PackagesAffected[0] == enrichPackageName
	if !hasPackageName {
		t.Errorf("Expected package %q to be extracted, got %v", enrichPackageName, record.PackagesAffected)
	}

	if record.Metadata[enrichSubcommandMetadataKey] != enrichInstallSubcommand {
		t.Errorf("Expected %s metadata %q, got %v", enrichSubcommandMetadataKey, enrichInstallSubcommand, record.Metadata)
	}

	if record.Timestamp.IsZero() {
		t.Error("Expected missing timestamp to be filled")
	}
}

func writePIDFileForTest(t *testing.T, cfg *core.Config, pid string) {
	t.Helper()

	if err := os.WriteFile(cfg.Daemon.PIDFile, []byte(pid), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write PID file: %v", err)
	}
}

func assertDaemonIsNotRunning(t *testing.T, cfg *core.Config, context string) {
	t.Helper()

	if IsRunning(cfg) {
		t.Errorf("IsRunning true for %s", context)
	}
}

func lockTestPIDFile(t *testing.T, path string, pid int) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, core.PrivateFileMode)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	if _, err := file.WriteString(strconv.Itoa(pid)); err != nil {
		_ = file.Close()
		t.Fatalf("WriteString failed: %v", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		t.Fatalf("Flock failed: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	})
}

func openSharedLockedPIDFile(t *testing.T, path string, pid int) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, core.PrivateFileMode)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}
	if _, err := file.WriteString(strconv.Itoa(pid)); err != nil {
		t.Fatalf("WriteString failed: %v", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatalf("Flock failed: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if err := file.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
	})
}

func restoreDaemonControlHooks(t *testing.T) {
	t.Helper()
	originalSender := daemonSocketControlSender
	originalSignaler := daemonProcessSignaler
	t.Cleanup(func() {
		daemonSocketControlSender = originalSender
		daemonProcessSignaler = originalSignaler
	})
}

func failingStopControlSender(pid int) func(string, string) (socketControlResponse, error) {
	return func(_ string, request string) (socketControlResponse, error) {
		if request == socketRequestPing {
			return socketControlResponse{Status: socketStatusOK, PID: pid}, nil
		}
		return socketControlResponse{}, errors.New("socket closed")
	}
}

func allSupportedMonitorTools() []string {
	return []string{
		core.ToolHomebrew,
		core.ToolNPM,
		core.ToolPNPM,
		core.ToolBun,
		core.ToolGo,
		core.ToolPip,
		core.ToolUV,
		core.ToolPoetry,
	}
}

func assertRegisteredMonitorCount(t *testing.T, d *Daemon, want int) {
	t.Helper()

	if got := len(d.registry.All()); got != want {
		t.Fatalf("registered monitors = %d, want %d", got, want)
	}
}

func startDaemonForCancellation(t *testing.T, d *Daemon) {
	t.Helper()

	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
}

func assertDaemonContextCanceled(t *testing.T, d *Daemon) {
	t.Helper()

	select {
	case <-d.ctx.Done():
	default:
		t.Error("Context should be cancelled")
	}
}

func sendConcurrentDaemonEvents(d *Daemon, eventCount int) {
	var wg sync.WaitGroup
	for i := 0; i < eventCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			record := &core.ExecutionRecord{
				ID:        string(rune(id)),
				Tool:      "homebrew",
				Command:   "install",
				Timestamp: time.Now(),
			}
			select {
			case d.eventChan <- record:
			case <-time.After(time.Second):
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)
}

func startDaemonForWaitTest(t *testing.T, cfg *core.Config) *Daemon {
	t.Helper()

	d := newDaemonForTest(t, cfg)
	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	return d
}

func waitForDaemonInBackground(d *Daemon) chan struct{} {
	done := make(chan struct{})
	go func() {
		d.Wait()
		close(done)
	}()
	return done
}

func stopDaemonForWaitTest(t *testing.T, d *Daemon) {
	t.Helper()

	if err := d.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func assertDaemonWaitDone(t *testing.T, done chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not unblock after Stop")
	}
}

func startBackupCleanupDaemon(t *testing.T) (*Daemon, *mockStorage) {
	t.Helper()

	cfg := testConfig(t)
	cfg.Storage.IsBackupEnabled = true
	cfg.Storage.BackupInterval = 10 * time.Millisecond
	d := newDaemonForTest(t, cfg)
	mock := newMockStorage()
	d.storage = mock
	d.wg.Add(1)
	go d.runPeriodicCleanup()
	return d, mock
}

func waitForBackupCall(t *testing.T, mock *mockStorage) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if backupCallCount(mock) > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func backupCallCount(mock *mockStorage) int {
	mock.mu.RLock()
	defer mock.mu.RUnlock()
	return mock.backupCalls
}

func newEventDaemon(t *testing.T, cfg *core.Config, buffer int) (*Daemon, *mockStorage) {
	t.Helper()

	d, mockStore := daemonWithMockStorage(t, cfg)
	if buffer > 0 {
		d.eventChan = make(chan *core.ExecutionRecord, buffer)
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.ctx = ctx
	d.cancel = cancel
	return d, mockStore
}

func startProcessEvents(d *Daemon) chan struct{} {
	done := make(chan struct{})
	d.wg.Add(1)
	go d.processEvents()
	go func() {
		d.wg.Wait()
		close(done)
	}()
	return done
}

func queueDaemonEvent(d *Daemon, id, tool string) {
	d.eventChan <- &core.ExecutionRecord{ID: id, Tool: tool}
}

func assertProcessEventsDone(t *testing.T, done chan struct{}, message string) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func assertBatchCalls(t *testing.T, mockStore *mockStorage, want int) {
	t.Helper()

	if mockStore.batchCalls != want {
		t.Errorf("Expected %d batched storage write, got %d", want, mockStore.batchCalls)
	}
}

func awaitRefreshCall(t *testing.T, calls <-chan context.Context) context.Context {
	t.Helper()
	select {
	case ctx := <-calls:
		return ctx
	case <-time.After(time.Second):
		t.Fatal("inventory refresh did not run")
		return nil
	}
}

func assertNoRefreshCall(t *testing.T, calls <-chan context.Context) {
	t.Helper()
	select {
	case <-calls:
		t.Fatal("inventory refreshes overlapped")
	case <-time.After(20 * time.Millisecond):
	}
}

func assertRefreshStopped(t *testing.T, d *Daemon) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("inventory refresh did not stop")
	}
}
