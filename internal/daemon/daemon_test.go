package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

type mockStorage struct {
	mu          sync.RWMutex
	executions  []*core.ExecutionRecord
	packages    map[string][]*core.PackageInfo
	closed      bool
	addErr      error
	getErr      error
	initialized bool
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		executions: make([]*core.ExecutionRecord, 0),
		packages:   make(map[string][]*core.PackageInfo),
	}
}

func (m *mockStorage) Initialize(config *core.Config) error {
	m.initialized = true
	return nil
}

func (m *mockStorage) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
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

func (m *mockStorage) GetExecutions(opts storage.QueryOptions) ([]*core.ExecutionRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.getErr != nil {
		return nil, m.getErr
	}

	result := make([]*core.ExecutionRecord, 0)
	for _, e := range m.executions {
		if opts.Tool != "" && e.Tool != opts.Tool {
			continue
		}
		result = append(result, e)
	}

	if opts.Limit > 0 && len(result) > opts.Limit {
		result = result[:opts.Limit]
	}
	return result, nil
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
	if tool == "" {
		var all []*core.PackageInfo
		for _, pkgs := range m.packages {
			all = append(all, pkgs...)
		}
		return all, nil
	}
	return m.packages[tool], nil
}

func (m *mockStorage) GetAllPackages() (map[string]map[string]*core.PackageInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]map[string]*core.PackageInfo)
	for tool, pkgs := range m.packages {
		result[tool] = make(map[string]*core.PackageInfo)
		for _, p := range pkgs {
			result[tool][p.Name] = p
		}
	}
	return result, nil
}

func (m *mockStorage) GetStatistics() (*core.StorageStatistics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
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
	return nil
}

func (m *mockStorage) Restore(path string) error {
	return nil
}

func (m *mockStorage) Cleanup(before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := make([]*core.ExecutionRecord, 0)
	for _, e := range m.executions {
		if e.Timestamp.After(before) {
			filtered = append(filtered, e)
		}
	}
	m.executions = filtered
	return nil
}

func (m *mockStorage) getExecutionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.executions)
}

func testConfig(t *testing.T) *core.Config {
	t.Helper()
	tmpDir := t.TempDir()
	return &core.Config{
		Version: "1.0",
		Daemon: core.DaemonConfig{
			Port:     0,
			LogLevel: "info",
			DataDir:  tmpDir,
			PIDFile:  filepath.Join(tmpDir, "diu.pid"),
		},
		Storage: core.StorageConfig{
			Backend:       "json",
			JSONFile:      filepath.Join(tmpDir, "executions.json"),
			RetentionDays: 365,
		},
		Monitoring: core.MonitoringConfig{
			EnabledTools: []string{},
		},
		API: core.APIConfig{
			Enabled: false,
			Host:    "127.0.0.1",
			Port:    0,
		},
	}
}

func TestNewDaemon(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	if d == nil {
		t.Fatal("Expected daemon to be non-nil")
	}

	if d.config != cfg {
		t.Error("Config not set correctly")
	}

	if d.storage == nil {
		t.Error("Storage not initialized")
	}

	if d.registry == nil {
		t.Error("Registry not initialized")
	}

	if d.eventChan == nil {
		t.Error("Event channel not initialized")
	}

	if cap(d.eventChan) != 100 {
		t.Errorf("Event channel capacity: got %d, want 100", cap(d.eventChan))
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

	if _, err := os.Stat(cfg.Daemon.PIDFile); os.IsNotExist(err) {
		t.Error("PID file not created")
	}

	if d.IsStopped() {
		t.Error("Daemon should not be stopped after Start")
	}

	if err := d.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if !d.IsStopped() {
		t.Error("Daemon should be stopped after Stop")
	}

	if _, err := os.Stat(cfg.Daemon.PIDFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed after Stop")
	}
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

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer d.Stop()

	record := &core.ExecutionRecord{
		ID:        "test-1",
		Tool:      "homebrew",
		Command:   "install",
		Args:      []string{"wget"},
		Timestamp: time.Now(),
	}

	select {
	case d.eventChan <- record:
	case <-time.After(time.Second):
		t.Fatal("Failed to send event to channel")
	}

	time.Sleep(100 * time.Millisecond)

	if mockStore.getExecutionCount() != 1 {
		t.Errorf("Expected 1 execution, got %d", mockStore.getExecutionCount())
	}
}

func TestDaemonHTTPAPI(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.Enabled = true
	cfg.API.Port = 0

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	mockStore.AddExecution(&core.ExecutionRecord{
		ID:        "test-1",
		Tool:      "homebrew",
		Command:   "install",
		Timestamp: time.Now(),
	})

	t.Run("GET /api/v1/executions", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/executions", nil)
		w := httptest.NewRecorder()

		d.handleExecutions(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}

		var executions []*core.ExecutionRecord
		if err := json.NewDecoder(resp.Body).Decode(&executions); err != nil {
			t.Fatalf("Failed to decode response: %v", err)
		}

		if len(executions) != 1 {
			t.Errorf("Expected 1 execution, got %d", len(executions))
		}
	})

	t.Run("GET /api/v1/executions with tool filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/executions?tool=npm", nil)
		w := httptest.NewRecorder()

		d.handleExecutions(w, req)

		var executions []*core.ExecutionRecord
		json.NewDecoder(w.Result().Body).Decode(&executions)

		if len(executions) != 0 {
			t.Errorf("Expected 0 executions for npm, got %d", len(executions))
		}
	})

	t.Run("POST /api/v1/executions", func(t *testing.T) {
		record := core.ExecutionRecord{
			ID:        "test-2",
			Tool:      "npm",
			Command:   "install",
			Timestamp: time.Now(),
		}
		body, _ := json.Marshal(record)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/executions", strings.NewReader(string(body)))
		w := httptest.NewRecorder()

		d.handleExecutions(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusAccepted {
			t.Errorf("Expected status 202, got %d", resp.StatusCode)
		}
	})

	t.Run("POST /api/v1/executions invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/executions", strings.NewReader("invalid json"))
		w := httptest.NewRecorder()

		d.handleExecutions(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", resp.StatusCode)
		}
	})

	t.Run("DELETE /api/v1/executions not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/executions", nil)
		w := httptest.NewRecorder()

		d.handleExecutions(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("Expected status 405, got %d", resp.StatusCode)
		}
	})
}

func TestDaemonPackagesAPI(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	mockStore.UpdatePackage(&core.PackageInfo{
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

func TestDaemonSocketListener(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer d.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("unix", "/tmp/diu.sock")
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer conn.Close()

	record := core.ExecutionRecord{
		ID:        "socket-test-1",
		Tool:      "go",
		Command:   "install",
		Timestamp: time.Now(),
	}

	if err := json.NewEncoder(conn).Encode(record); err != nil {
		t.Fatalf("Failed to send record: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if mockStore.getExecutionCount() != 1 {
		t.Errorf("Expected 1 execution from socket, got %d", mockStore.getExecutionCount())
	}
}

func TestIsRunning(t *testing.T) {
	cfg := testConfig(t)

	if IsRunning(cfg) {
		t.Error("Should return false when PID file doesn't exist")
	}

	if err := os.WriteFile(cfg.Daemon.PIDFile, []byte("invalid"), 0644); err != nil {
		t.Fatalf("Failed to write PID file: %v", err)
	}

	if IsRunning(cfg) {
		t.Error("Should return false for invalid PID")
	}

	if err := os.WriteFile(cfg.Daemon.PIDFile, []byte("999999999"), 0644); err != nil {
		t.Fatalf("Failed to write PID file: %v", err)
	}

	if IsRunning(cfg) {
		t.Error("Should return false for non-existent process")
	}

	os.Remove(cfg.Daemon.PIDFile)
}

func TestDaemonWithMonitors(t *testing.T) {
	cfg := testConfig(t)
	cfg.Monitoring.EnabledTools = []string{"homebrew", "npm", "go"}

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	monitors := d.registry.GetAll()
	if len(monitors) != 3 {
		t.Errorf("Expected 3 monitors, got %d", len(monitors))
	}
}

func TestDaemonUnknownMonitor(t *testing.T) {
	cfg := testConfig(t)
	cfg.Monitoring.EnabledTools = []string{"unknown_tool"}

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon should not fail for unknown tools: %v", err)
	}

	monitors := d.registry.GetAll()
	if len(monitors) != 0 {
		t.Errorf("Expected 0 monitors for unknown tool, got %d", len(monitors))
	}
}

func TestDaemonContextCancellation(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	d.cancel()

	time.Sleep(100 * time.Millisecond)

	select {
	case <-d.ctx.Done():
	default:
		t.Error("Context should be cancelled")
	}

	d.Stop()
}

func TestDaemonConcurrentEvents(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer d.Stop()

	var wg sync.WaitGroup
	eventCount := 50

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

	count := mockStore.getExecutionCount()
	if count != eventCount {
		t.Errorf("Expected %d executions, got %d", eventCount, count)
	}
}

func TestDaemonHTTPServerWithAPI(t *testing.T) {
	cfg := testConfig(t)
	cfg.API.Enabled = true
	cfg.API.Host = "127.0.0.1"
	cfg.API.Port = 18081

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	if err := d.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer d.Stop()

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:18081/api/v1/health")
	if err != nil {
		t.Fatalf("Failed to make HTTP request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestHandleExecutionsWithLimit(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	for i := 0; i < 10; i++ {
		mockStore.AddExecution(&core.ExecutionRecord{
			ID:        string(rune(i)),
			Tool:      "homebrew",
			Timestamp: time.Now(),
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/executions?limit=5", nil)
	w := httptest.NewRecorder()

	d.handleExecutions(w, req)

	var executions []*core.ExecutionRecord
	json.NewDecoder(w.Result().Body).Decode(&executions)

	if len(executions) != 5 {
		t.Errorf("Expected 5 executions with limit, got %d", len(executions))
	}
}

func TestProcessEventsChannelClose(t *testing.T) {
	cfg := testConfig(t)

	d, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	mockStore := newMockStorage()
	d.storage = mockStore

	ctx, cancel := context.WithCancel(context.Background())
	d.ctx = ctx
	d.cancel = cancel

	d.wg.Add(1)
	go d.processEvents()

	record := &core.ExecutionRecord{
		ID:   "test",
		Tool: "homebrew",
	}
	d.eventChan <- record

	time.Sleep(50 * time.Millisecond)

	close(d.eventChan)

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("processEvents did not exit after channel close")
	}

	if mockStore.getExecutionCount() != 1 {
		t.Errorf("Expected 1 execution, got %d", mockStore.getExecutionCount())
	}
}
