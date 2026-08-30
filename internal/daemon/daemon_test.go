package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	mu            sync.RWMutex
	executions    []*core.ExecutionRecord
	packages      map[string][]*core.PackageInfo
	isClosed      bool
	addErr        error
	getErr        error
	packagesErr   error
	statsErr      error
	isInitialized bool
	batchCalls    int
	backupCalls   int
	cleanupCalls  int
	prepareCalls  int
	cleanupErr    error
	lastQuery     storage.QueryOptions
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		executions: make([]*core.ExecutionRecord, 0),
		packages:   make(map[string][]*core.PackageInfo),
	}
}

func (m *mockStorage) Initialize(config *core.Config) error {
	m.isInitialized = true
	return nil
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
		if tool != "" {
			if execution.Tool != tool {
				continue
			}
		}
		result = append(result, execution)
	}
	return result
}

func limitMockExecutions(executions []*core.ExecutionRecord, limit int) []*core.ExecutionRecord {
	if limit <= 0 {
		return executions
	}
	if len(executions) <= limit {
		return executions
	}
	return executions[:limit]
}

func (m *mockStorage) GetExecutionByID(id string) (*core.ExecutionRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.executions {
		if e.ID == id {
			return e, nil
		}
	}
	return nil, nil
}

func (m *mockStorage) UpdatePackage(pkg *core.PackageInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.packages[pkg.Tool]; !ok {
		m.packages[pkg.Tool] = make([]*core.PackageInfo, 0)
	}
	m.packages[pkg.Tool] = append(m.packages[pkg.Tool], pkg)
	return nil
}

func (m *mockStorage) GetPackage(tool, name string) (*core.PackageInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if pkgs, ok := m.packages[tool]; ok {
		for _, p := range pkgs {
			if p.Name == name {
				return p, nil
			}
		}
	}
	return nil, nil
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
func (m *mockStorage) AllPackages() (map[string]map[string]*core.PackageInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]map[string]*core.PackageInfo)
	for tool, pkgs := range m.packages {
		result[tool] = packagesByName(pkgs)
	}
	return result, nil
}

func packagesByName(packages []*core.PackageInfo) map[string]*core.PackageInfo {
	byName := make(map[string]*core.PackageInfo)
	for _, pkg := range packages {
		byName[pkg.Name] = pkg
	}
	return byName
}

func (m *mockStorage) DeletePackage(tool, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pkgs := m.packages[tool]
	for i, pkg := range pkgs {
		if pkg.Name == name {
			m.packages[tool] = append(pkgs[:i], pkgs[i+1:]...)
			break
		}
	}
	return nil
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

func (m *mockStorage) UpdateStatistics() error {
	return nil
}

func (m *mockStorage) Backup() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backupCalls++
	return nil
}

func (m *mockStorage) Restore(path string) error {
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

func TestDaemonStartCompactsStorageBeforeReady(t *testing.T) {
	config := testConfig(t)
	config.Daemon.SocketPath = shortSocketPath(t)
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
	return &core.Config{
		Version:    "1.0",
		Daemon:     testDaemonConfig(tmpDir),
		Storage:    testStorageConfig(tmpDir),
		Monitoring: core.MonitoringConfig{EnabledTools: []string{}},
		API:        testAPIConfig(),
	}
}

func testDaemonConfig(tmpDir string) core.DaemonConfig {
	return core.DaemonConfig{
		Port:       0,
		LogLevel:   "info",
		DataDir:    tmpDir,
		PIDFile:    filepath.Join(tmpDir, "diu.pid"),
		SocketPath: filepath.Join(tmpDir, "diu.sock"),
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

func TestNewDaemon(t *testing.T) {
	cfg := testConfig(t)
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	assertNewDaemon(t, d, cfg)
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

func TestDaemonStartStop(t *testing.T) {
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

func TestDaemonStartReportsPIDFileFailure(t *testing.T) {
	cfg := testConfig(t)
	cfg.Daemon.SocketPath = shortSocketPath(t)
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

func TestFailedSecondStartPreservesRunningDaemonPID(t *testing.T) {
	cfg := testConfig(t)
	cfg.Daemon.SocketPath = shortSocketPath(t)
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

func TestDaemonStartReclaimsUnlockedPIDForLiveUnrelatedProcess(t *testing.T) {
	cfg := testConfig(t)
	cfg.Daemon.SocketPath = shortSocketPath(t)
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

func TestDaemonDoubleStop(t *testing.T) {
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

func TestDaemonEventProcessing(t *testing.T) {
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

const (
	enrichRawToolName           = "brew"
	enrichCommandName           = "brew install wget"
	enrichInstallSubcommand     = "install"
	enrichPackageName           = "wget"
	enrichSubcommandMetadataKey = "subcommand"
	enrichExpectedPackageCount  = 1
)

func TestDaemonEnrichExecution(t *testing.T) {
	cfg := testConfig(t)
	cfg.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	cfg.Monitoring.Process.WrapperDir = t.TempDir()
	cfg.Monitoring.Process.ShouldAutoInstallWrappers = false

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

func TestDaemonHTTPAPI(t *testing.T) {
	d := newHTTPAPIDaemon(t)
	seedHTTPAPIExecutions(t, d.storage.(*mockStorage))
	runExecutionAPIReadTests(t, d)
	runExecutionAPIWriteTests(t, d)
}

func newHTTPAPIDaemon(t *testing.T) *Daemon {
	t.Helper()

	cfg := testConfig(t)
	cfg.API.IsEnabled = true
	cfg.API.Port = 0
	d := newDaemonForTest(t, cfg)
	d.storage = newMockStorage()
	return d
}

func seedHTTPAPIExecutions(t *testing.T, mockStore *mockStorage) {
	t.Helper()
	addMockExecution(t, mockStore, &core.ExecutionRecord{
		ID:        "test-1",
		Tool:      "homebrew",
		Command:   "install",
		Timestamp: time.Now(),
	})
}

func runExecutionAPIReadTests(t *testing.T, d *Daemon) {
	t.Helper()
	t.Run("GET /api/v1/executions", func(t *testing.T) { assertGetExecutionsAPI(t, d) })
	t.Run("GET /api/v1/executions with tool filter", func(t *testing.T) { assertFilteredExecutionsAPI(t, d) })
	t.Run("GET /api/v1/executions with invalid limit", func(t *testing.T) {
		request := executionAPIRequest{method: http.MethodGet, path: "/api/v1/executions?limit=-1", want: http.StatusBadRequest}
		assertExecutionAPIStatus(t, d, request)
	})
}

func runExecutionAPIWriteTests(t *testing.T, d *Daemon) {
	t.Helper()
	t.Run("POST /api/v1/executions", func(t *testing.T) { assertPostExecutionsAPI(t, d) })
	t.Run("POST /api/v1/executions missing command", func(t *testing.T) {
		request := executionAPIRequest{method: http.MethodPost, path: "/api/v1/executions", body: `{"tool":"npm"}`, want: http.StatusBadRequest}
		assertExecutionAPIStatus(t, d, request)
	})
	t.Run("POST /api/v1/executions too large", func(t *testing.T) { assertLargeExecutionAPI(t, d) })
	t.Run("POST /api/v1/executions invalid JSON", func(t *testing.T) {
		request := executionAPIRequest{method: http.MethodPost, path: "/api/v1/executions", body: "invalid json", want: http.StatusBadRequest}
		assertExecutionAPIStatus(t, d, request)
	})
	t.Run("DELETE /api/v1/executions not allowed", func(t *testing.T) {
		request := executionAPIRequest{method: http.MethodDelete, path: "/api/v1/executions", want: http.StatusMethodNotAllowed}
		assertExecutionAPIStatus(t, d, request)
	})
}

type executionAPIRequest struct {
	method string
	path   string
	body   string
	want   int
}

func assertGetExecutionsAPI(t *testing.T, d *Daemon) {
	t.Helper()
	w := handleExecutionAPIRequest(d, http.MethodGet, "/api/v1/executions", "")
	assertResponseStatus(t, w, http.StatusOK)
	var executions []*core.ExecutionRecord
	decodeRecorderJSON(t, w, &executions)
	if len(executions) != 1 {
		t.Errorf("Expected 1 execution, got %d", len(executions))
	}
}

func assertFilteredExecutionsAPI(t *testing.T, d *Daemon) {
	t.Helper()
	w := handleExecutionAPIRequest(d, http.MethodGet, "/api/v1/executions?tool=npm", "")
	var executions []*core.ExecutionRecord
	decodeRecorderJSON(t, w, &executions)
	if len(executions) != 0 {
		t.Errorf("Expected 0 executions for npm, got %d", len(executions))
	}
}

func assertPostExecutionsAPI(t *testing.T, d *Daemon) {
	t.Helper()
	record := core.ExecutionRecord{ID: "test-2", Tool: "npm", Command: "install", Timestamp: time.Now()}
	body, _ := json.Marshal(record)
	w := handleExecutionAPIRequest(d, http.MethodPost, "/api/v1/executions", string(body))
	assertResponseStatus(t, w, http.StatusAccepted)
}

func assertLargeExecutionAPI(t *testing.T, d *Daemon) {
	t.Helper()
	body := `{"tool":"npm","command":"` + strings.Repeat("x", maxExecutionRecordBodyBytes) + `"}`
	request := executionAPIRequest{method: http.MethodPost, path: "/api/v1/executions", body: body, want: http.StatusBadRequest}
	assertExecutionAPIStatus(t, d, request)
}

func assertExecutionAPIStatus(t *testing.T, d *Daemon, request executionAPIRequest) {
	t.Helper()
	w := handleExecutionAPIRequest(d, request.method, request.path, request.body)
	assertResponseStatus(t, w, request.want)
}

func handleExecutionAPIRequest(d *Daemon, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	d.handleExecutions(w, req)
	return w
}

func assertResponseStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	resp := recorder.Result()
	if resp.StatusCode != want {
		t.Errorf("Expected status %d, got %d", want, resp.StatusCode)
	}
}

func TestDaemonRejectsInvalidExecutionRecords(t *testing.T) {
	cfg := testConfig(t)
	d := newDaemonForTest(t, cfg)
	defer closeStorageForTest(t, d.storage)

	for _, test := range invalidExecutionCases() {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidExecutionRequest(t, d, test)
		})
	}
}

type invalidExecutionCase struct {
	name string
	body string
	want string
}

func invalidExecutionCases() []invalidExecutionCase {
	longCommandBody := `{"tool":"npm","command":"` + strings.Repeat("x", maxRecordedCommandLength+1) + `"}`
	return []invalidExecutionCase{
		{name: "missing tool", body: `{"command":"install"}`, want: "tool is required"},
		{name: "long command", body: longCommandBody, want: "command exceeds"},
		{name: "negative duration", body: `{"tool":"npm","command":"install","duration_ms":-1}`, want: "duration_ms must be non-negative"},
		{name: "empty package", body: `{"tool":"npm","command":"install","packages_affected":[""]}`, want: "packages_affected cannot contain empty values"},
		{name: "multiple objects", body: `{"tool":"npm","command":"install"} {}`, want: "single JSON object"},
	}
}

func assertInvalidExecutionRequest(t *testing.T, d *Daemon, test invalidExecutionCase) {
	t.Helper()
	recorder := handleExecutionAPIRequest(d, http.MethodPost, "/api/v1/executions", test.body)
	assertResponseStatus(t, recorder, http.StatusBadRequest)
	if !strings.Contains(recorder.Body.String(), test.want) {
		t.Fatalf("body = %q, want %q", recorder.Body.String(), test.want)
	}
}

func TestDaemonRejectsExecutionWhenQueueIsUnavailable(t *testing.T) {
	cfg := testConfig(t)
	d := newDaemonForTest(t, cfg)
	defer closeStorageForTest(t, d.storage)
	d.eventChan = make(chan *core.ExecutionRecord, 1)
	d.eventChan <- &core.ExecutionRecord{Tool: core.ToolNPM, Command: "install"}
	body := `{"tool":"npm","command":"install"}`

	recorder, done := handleExecutionAPIRequestAsync(d, body)
	assertExecutionQueueBlocks(t, done)
	<-d.eventChan
	assertExecutionQueueResumes(t, done)
	assertResponseStatus(t, recorder, http.StatusAccepted)

	d.cancel()
	assertStoppedDaemonRejectsExecution(t, d, body)
}

func handleExecutionAPIRequestAsync(d *Daemon, body string) (*httptest.ResponseRecorder, chan struct{}) {
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/executions", strings.NewReader(body))
	go func() {
		d.handleExecutions(recorder, request)
		close(done)
	}()
	return recorder, done
}

func assertExecutionQueueBlocks(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("handler should apply backpressure while the event queue is full")
	case <-time.After(20 * time.Millisecond):
	}
}

func assertExecutionQueueResumes(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not resume after event queue capacity became available")
	}
}

func assertStoppedDaemonRejectsExecution(t *testing.T, d *Daemon, body string) {
	t.Helper()
	recorder := handleExecutionAPIRequest(d, http.MethodPost, "/api/v1/executions", body)
	hasStatus := recorder.Code == http.StatusServiceUnavailable
	hasMessage := strings.Contains(recorder.Body.String(), "stopping")
	hasStoppedResponse := hasStatus && hasMessage
	if !hasStoppedResponse {
		t.Fatalf("stopped daemon response = %d, %q", recorder.Code, recorder.Body.String())
	}
}

func TestDaemonPackagesAPI(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	updateMockPackage(t, mockStore, &core.PackageInfo{
		Name: "wget",
		Tool: "homebrew",
	})

	t.Run("GET /api/v1/packages", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/packages", nil)
		w := httptest.NewRecorder()

		d.handlePackages(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
	})

	t.Run("POST /api/v1/packages not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/packages", nil)
		w := httptest.NewRecorder()

		d.handlePackages(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("Expected status 405, got %d", resp.StatusCode)
		}
	})
}

func TestDaemonStatsAPI(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	t.Run("GET /api/v1/stats", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
		w := httptest.NewRecorder()

		d.handleStats(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		var stats core.StorageStatistics
		if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}
	})
}

func TestDaemonHealthAPI(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	t.Run("GET /api/v1/health", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		w := httptest.NewRecorder()

		d.handleHealth(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "healthy") {
			t.Error("Health response should contain 'healthy'")
		}
	})
}

func TestDaemonReadOnlyAPIsRejectWrites(t *testing.T) {
	cfg := testConfig(t)
	d := newDaemonForTest(t, cfg)
	defer closeStorageForTest(t, d.storage)

	for _, test := range readOnlyAPICases(d) {
		assertReadOnlyAPIRejectsWrite(t, test)
	}
}

type readOnlyAPICase struct {
	path    string
	handler http.HandlerFunc
}

func readOnlyAPICases(d *Daemon) []readOnlyAPICase {
	return []readOnlyAPICase{
		{path: "/api/v1/stats", handler: d.handleStats},
		{path: "/api/v1/health", handler: d.handleHealth},
	}
}

func assertReadOnlyAPIRejectsWrite(t *testing.T, test readOnlyAPICase) {
	t.Helper()
	recorder := httptest.NewRecorder()
	test.handler(recorder, httptest.NewRequest(http.MethodPost, test.path, nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("%s status = %d, want 405", test.path, recorder.Code)
	}
}

func TestDaemonSocketListener(t *testing.T) {
	cfg := testConfig(t)
	d := startDaemonWithMockStorage(t, cfg)
	defer stopDaemonForTest(t, d)

	sendSocketExecution(t, cfg.Daemon.SocketPath)
	assertMockExecutionCount(t, d.storage.(*mockStorage), 1)
}

func startDaemonWithMockStorage(t *testing.T, cfg *core.Config) *Daemon {
	t.Helper()
	d, _ := daemonWithMockStorage(t, cfg)

	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	return d
}

func daemonWithMockStorage(t *testing.T, cfg *core.Config) (*Daemon, *mockStorage) {
	t.Helper()

	d := newDaemonForTest(t, cfg)
	mockStore := newMockStorage()
	d.storage = mockStore
	return d, mockStore
}

func sendSocketExecution(t *testing.T, socketPath string) {
	t.Helper()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer closeForTest(t, conn)

	record := socketExecutionRecord()
	if err := json.NewEncoder(conn).Encode(record); err != nil {
		t.Fatalf("Failed to send record: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
}

func socketExecutionRecord() core.ExecutionRecord {
	return core.ExecutionRecord{
		ID:        "socket-test-1",
		Tool:      "go",
		Command:   "install",
		Timestamp: time.Now(),
	}
}

func assertMockExecutionCount(t *testing.T, mockStore *mockStorage, want int) {
	t.Helper()

	if mockStore.executionCount() != want {
		t.Errorf("Expected %d execution from socket, got %d", want, mockStore.executionCount())
	}
}

func TestDaemonSocketListenerReportsInvalidPaths(t *testing.T) {
	t.Run("active socket is preserved", func(t *testing.T) {
		assertActiveSocketIsPreserved(t)
	})

	t.Run("stale socket cannot be removed", func(t *testing.T) {
		assertStaleSocketRemoveFailure(t)
	})

	t.Run("socket directory cannot be created", func(t *testing.T) {
		assertSocketDirectoryCreateFailure(t)
	})
}

func assertActiveSocketIsPreserved(t *testing.T) {
	t.Helper()

	cfg := testConfig(t)
	socketDir := activeSocketDir(t)
	cfg.Daemon.SocketPath = filepath.Join(socketDir, "diu.sock")
	listener := listenOnSocket(t, cfg.Daemon.SocketPath)
	defer closeForTest(t, listener)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	err = d.Start()
	assertActiveSocketStartError(t, err)
	assertSocketPathExists(t, cfg.Daemon.SocketPath)
}

func assertActiveSocketStartError(t *testing.T, err error) {
	t.Helper()

	hasActiveSocketError := err != nil && strings.Contains(err.Error(), "socket is active")
	if !hasActiveSocketError {
		t.Fatalf("Start error = %v", err)
	}
}

func assertSocketPathExists(t *testing.T, socketPath string) {
	t.Helper()

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("active socket was removed: %v", err)
	}
}

func activeSocketDir(t *testing.T) string {
	t.Helper()

	socketDir, err := os.MkdirTemp("/tmp", "diu-socket-test-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(socketDir); err != nil {
			t.Errorf("RemoveAll failed: %v", err)
		}
	})
	return socketDir
}

func listenOnSocket(t *testing.T, socketPath string) net.Listener {
	t.Helper()

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	return listener
}

func assertStaleSocketRemoveFailure(t *testing.T) {
	t.Helper()

	cfg := testConfig(t)
	socketDir := blockedSocketPath(t)
	cfg.Daemon.SocketPath = socketDir
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	defer closeStorageForTest(t, d.storage)
	err = d.startSocketListener()
	hasRemoveError := err != nil && strings.Contains(err.Error(), "remove stale socket")
	if !hasRemoveError {
		t.Fatalf("startSocketListener error = %v", err)
	}
}

func blockedSocketPath(t *testing.T) string {
	t.Helper()

	socketDir := filepath.Join(t.TempDir(), "socket")
	if err := os.Mkdir(socketDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(socketDir, "child"), nil, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	return socketDir
}

func assertSocketDirectoryCreateFailure(t *testing.T) {
	t.Helper()

	cfg := testConfig(t)
	blocked := socketParentWithoutWritePermission(t)
	cfg.Daemon.SocketPath = filepath.Join(blocked, "nested", "diu.sock")
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	defer closeStorageForTest(t, d.storage)
	err = d.startSocketListener()
	hasDirectoryError := err != nil && strings.Contains(err.Error(), "create socket directory")
	if !hasDirectoryError {
		t.Fatalf("startSocketListener error = %v", err)
	}
}

func socketParentWithoutWritePermission(t *testing.T) string {
	t.Helper()

	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.Mkdir(blocked, 0o500); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blocked, core.OwnerDirectoryMode); err != nil {
			t.Fatalf("Chmod failed: %v", err)
		}
	})
	return blocked
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

func TestDaemonIdentityError(t *testing.T) {
	err := daemonIdentityError(41, 42)
	want := "PID file has 41, socket reports 42"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("daemonIdentityError = %v", err)
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

func TestRemovePIDFileIgnoresMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.pid")
	if err := removePIDFile(path); err != nil {
		t.Fatalf("removePIDFile failed: %v", err)
	}
}

func TestPIDFileLockAllowsSharedProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diu.pid")
	openSharedLockedPIDFile(t, path, 4242)
	locked, err := pidFileLocked(path, 4242)
	probeSucceeded := err == nil && !locked
	if !probeSucceeded {
		t.Fatalf("shared PID lock = %v, %v", locked, err)
	}
}

func TestPIDFileLockRejectsMismatchedPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diu.pid")
	lockTestPIDFile(t, path, 4242)
	locked, err := pidFileLocked(path, 5252)
	probeSucceeded := err == nil && !locked
	if !probeSucceeded {
		t.Fatalf("mismatched PID lock = %v, %v", locked, err)
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

func TestDaemonWithMonitors(t *testing.T) {
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

func TestDaemonRegistersEverySupportedMonitor(t *testing.T) {
	setFakeCommandsInPath(t, "brew", "npm", "pnpm", "bun", "go", "pip3", "uv", "poetry")
	cfg := testConfig(t)
	cfg.Monitoring.EnabledTools = allSupportedMonitorTools()
	d := newDaemonForTest(t, cfg)
	defer closeStorageForTest(t, d.storage)
	assertRegisteredMonitorCount(t, d, len(cfg.Monitoring.EnabledTools))
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

func TestDaemonUnknownMonitor(t *testing.T) {
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

func TestDaemonContextCancellation(t *testing.T) {
	cfg := testConfig(t)
	d := newDaemonForTest(t, cfg)
	startDaemonForCancellation(t, d)

	d.cancel()
	time.Sleep(100 * time.Millisecond)
	assertDaemonContextCanceled(t, d)
	stopDaemonForTest(t, d)
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

func TestDaemonConcurrentEvents(t *testing.T) {
	cfg := testConfig(t)
	d := startDaemonWithMockStorage(t, cfg)
	defer stopDaemonForTest(t, d)
	mockStore := d.storage.(*mockStorage)
	eventCount := 50

	sendConcurrentDaemonEvents(d, eventCount)
	assertMockExecutionCount(t, mockStore, eventCount)
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

func TestDaemonHTTPServerWithAPI(t *testing.T) {
	d := startHTTPAPIDaemon(t)
	defer stopDaemonForTest(t, d)
	assertHTTPHealthOK(t, d)
}

func startHTTPAPIDaemon(t *testing.T) *Daemon {
	t.Helper()

	cfg := testConfig(t)
	cfg.API.IsEnabled = true
	cfg.API.Host = "127.0.0.1"
	cfg.API.Port = 0
	d, _ := daemonWithMockStorage(t, cfg)
	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	return d
}

func assertHTTPHealthOK(t *testing.T, d *Daemon) {
	t.Helper()

	resp, err := http.Get("http://" + d.httpServer.Addr + "/api/v1/health")
	if err != nil {
		t.Fatalf("Failed to make HTTP request: %v", err)
	}
	defer closeForTest(t, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestDaemonHTTPServerRejectsInvalidAddress(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.Host = "invalid host"
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	defer closeStorageForTest(t, d.storage)

	err = d.startHTTPServer()
	hasListenError := err != nil && strings.Contains(err.Error(), "failed to listen")
	if !hasListenError {
		t.Fatalf("startHTTPServer error = %v", err)
	}
}

func TestHandleExecutionsWithLimit(t *testing.T) {
	cfg := testConfig(t)
	d, mockStore := daemonWithMockStorage(t, cfg)
	addMockExecutions(t, mockStore, 10)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/executions?limit=5", nil)
	w := httptest.NewRecorder()
	d.handleExecutions(w, req)
	assertExecutionResponseCount(t, w, 5)
}

func addMockExecutions(t *testing.T, mockStore *mockStorage, count int) {
	t.Helper()

	for i := 0; i < count; i++ {
		addMockExecution(t, mockStore, &core.ExecutionRecord{
			ID:        string(rune(i)),
			Tool:      "homebrew",
			Timestamp: time.Now(),
		})
	}
}

func assertExecutionResponseCount(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()

	var executions []*core.ExecutionRecord
	decodeRecorderJSON(t, w, &executions)
	if len(executions) != want {
		t.Errorf("Expected %d executions with limit, got %d", want, len(executions))
	}
}

func TestHandleExecutionsUsesBoundedDefaultLimit(t *testing.T) {
	cfg := testConfig(t)
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	defer closeStorageForTest(t, d.storage)
	mockStore := newMockStorage()
	d.storage = mockStore

	recorder := httptest.NewRecorder()
	d.handleExecutions(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/executions", nil))
	if mockStore.lastQuery.Limit != defaultExecutionQueryLimit {
		t.Fatalf("default limit = %d, want %d", mockStore.lastQuery.Limit, defaultExecutionQueryLimit)
	}
}

func TestDaemonExecutionsAPIReportsStorageFailure(t *testing.T) {
	cfg := testConfig(t)
	d, mockStore := daemonWithMockStorage(t, cfg)
	defer closeStorageForTest(t, d.storage)
	mockStore.getErr = errors.New("storage unavailable")

	recorder := httptest.NewRecorder()
	d.handleExecutions(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/executions", nil))
	assertStorageFailureResponse(t, recorder, "storage unavailable")
}

func assertStorageFailureResponse(t *testing.T, recorder *httptest.ResponseRecorder, message string) {
	t.Helper()

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), message) {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}

func TestDaemonPackageAndStatsAPIsReportStorageFailures(t *testing.T) {
	cfg := testConfig(t)
	d, mockStore := daemonWithMockStorage(t, cfg)
	defer closeStorageForTest(t, d.storage)
	mockStore.packagesErr = errors.New("packages unavailable")
	mockStore.statsErr = errors.New("stats unavailable")

	for _, test := range storageFailureAPICases(d) {
		assertStorageFailureAPI(t, test)
	}
}

type storageFailureAPICase struct {
	path    string
	handler http.HandlerFunc
	want    string
}

func storageFailureAPICases(d *Daemon) []storageFailureAPICase {
	return []storageFailureAPICase{
		{path: "/api/v1/packages", handler: d.handlePackages, want: "packages unavailable"},
		{path: "/api/v1/stats", handler: d.handleStats, want: "stats unavailable"},
	}
}

func assertStorageFailureAPI(t *testing.T, test storageFailureAPICase) {
	t.Helper()

	recorder := httptest.NewRecorder()
	test.handler(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
	hasStatus := recorder.Code == http.StatusInternalServerError
	hasBody := strings.Contains(recorder.Body.String(), test.want)
	hasResponse := hasStatus && hasBody
	if !hasResponse {
		t.Fatalf("%s response = %d, %q", test.path, recorder.Code, recorder.Body.String())
	}
}

func TestDaemonWaitUnblocksAfterStop(t *testing.T) {
	cfg := testConfig(t)
	d := startDaemonForWaitTest(t, cfg)
	done := waitForDaemonInBackground(d)

	stopDaemonForWaitTest(t, d)
	assertDaemonWaitDone(t, done)
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

func TestDaemonPruneOldRecordsHandlesCleanupError(t *testing.T) {
	cfg := testConfig(t)
	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mock := newMockStorage()
	d.storage = mock

	d.pruneOldRecords()
}

func TestDaemonRunsConfiguredBackups(t *testing.T) {
	d, mock := startBackupCleanupDaemon(t)
	waitForBackupCall(t, mock)
	d.cancel()
	d.wg.Wait()
	if backupCallCount(mock) == 0 {
		t.Fatal("configured storage backup did not run")
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
