package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

type executionAPIRequest struct {
	method string
	path   string
	body   string
	want   int
}

type invalidExecutionCase struct {
	name string
	body string
	want string
}

type readOnlyAPICase struct {
	path    string
	handler http.HandlerFunc
}

type storageFailureAPICase struct {
	path    string
	handler http.HandlerFunc
	want    string
}

func TestStartSocketListenerAcceptsExecutions(t *testing.T) {
	cfg := testConfig(t)
	d := startDaemonWithMockStorage(t, cfg)
	defer stopDaemonForTest(t, d)

	sendSocketExecution(t, cfg.Daemon.SocketPath)
	assertMockExecutionCount(t, d.storage.(*mockStorage), 1)
}

func TestStartSocketListenerReportsInvalidPaths(t *testing.T) {
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

func TestStartHTTPServer(t *testing.T) {
	d := startHTTPAPIDaemon(t)
	defer stopDaemonForTest(t, d)
	assertHTTPHealthOK(t, d)
}

func TestStartHTTPServerRejectsInvalidAddress(t *testing.T) {
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

func TestHandleExecutions(t *testing.T) {
	d := newHTTPAPIDaemon(t)
	seedHTTPAPIExecutions(t, d.storage.(*mockStorage))
	runExecutionAPIReadTests(t, d)
	runExecutionAPIWriteTests(t, d)
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

func TestHandleGetExecutionsReportsStorageFailure(t *testing.T) {
	cfg := testConfig(t)
	d, mockStore := daemonWithMockStorage(t, cfg)
	defer closeStorageForTest(t, d.storage)
	mockStore.getErr = errors.New("storage unavailable")

	recorder := httptest.NewRecorder()
	d.handleExecutions(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/executions", nil))
	assertStorageFailureResponse(t, recorder, "storage unavailable")
}

func TestHandlePostExecutionRejectsInvalidRecords(t *testing.T) {
	cfg := testConfig(t)
	d := newDaemonForTest(t, cfg)
	defer closeStorageForTest(t, d.storage)

	for _, test := range invalidExecutionCases() {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidExecutionRequest(t, d, test)
		})
	}
}

func TestHandlePostExecutionWaitsForQueueCapacity(t *testing.T) {
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

func TestHandlePostExecutionRejectsCanceledRequest(t *testing.T) {
	d, _ := newEventDaemon(t, testConfig(t), 64)
	defer d.cancel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 32 {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/executions", strings.NewReader(`{"tool":"npm","command":"install"}`))
		response := httptest.NewRecorder()
		d.handleExecutions(response, request.WithContext(ctx))
		assertResponseStatus(t, response, http.StatusRequestTimeout)
	}
	if len(d.eventChan) != 0 {
		t.Fatal("canceled requests were admitted")
	}
}

func TestHandlePackages(t *testing.T) {
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

func TestHandlePackagesReportsStorageFailure(t *testing.T) {
	cfg := testConfig(t)
	d, mockStore := daemonWithMockStorage(t, cfg)
	defer closeStorageForTest(t, d.storage)
	mockStore.packagesErr = errors.New("packages unavailable")
	test := storageFailureAPICase{path: "/api/v1/packages", handler: d.handlePackages, want: "packages unavailable"}
	assertStorageFailureAPI(t, test)
}

func TestHandleStats(t *testing.T) {
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

func TestHandleStatsRejectsWrites(t *testing.T) {
	cfg := testConfig(t)
	d := newDaemonForTest(t, cfg)
	defer closeStorageForTest(t, d.storage)
	test := readOnlyAPICase{path: "/api/v1/stats", handler: d.handleStats}
	assertReadOnlyAPIRejectsWrite(t, test)
}

func TestHandleStatsReportsStorageFailure(t *testing.T) {
	cfg := testConfig(t)
	d, mockStore := daemonWithMockStorage(t, cfg)
	defer closeStorageForTest(t, d.storage)
	mockStore.statsErr = errors.New("stats unavailable")
	test := storageFailureAPICase{path: "/api/v1/stats", handler: d.handleStats, want: "stats unavailable"}
	assertStorageFailureAPI(t, test)
}

func TestHandleHealth(t *testing.T) {
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

func TestHandleHealthRejectsWrites(t *testing.T) {
	cfg := testConfig(t)
	d := newDaemonForTest(t, cfg)
	defer closeStorageForTest(t, d.storage)
	test := readOnlyAPICase{path: "/api/v1/health", handler: d.handleHealth}
	assertReadOnlyAPIRejectsWrite(t, test)
}

func TestRemovePIDFileIgnoresMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.pid")
	if err := removePIDFile(path); err != nil {
		t.Fatalf("removePIDFile failed: %v", err)
	}
}

func TestPIDFileLockedAllowsSharedProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diu.pid")
	openSharedLockedPIDFile(t, path, 4242)
	locked, err := pidFileLocked(path, 4242)
	probeSucceeded := err == nil && !locked
	if !probeSucceeded {
		t.Fatalf("shared PID lock = %v, %v", locked, err)
	}
}

func TestPIDFileLockedRejectsMismatchedPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diu.pid")
	lockTestPIDFile(t, path, 4242)
	locked, err := pidFileLocked(path, 5252)
	probeSucceeded := err == nil && !locked
	if !probeSucceeded {
		t.Fatalf("mismatched PID lock = %v, %v", locked, err)
	}
}

func TestDaemonIdentityError(t *testing.T) {
	err := daemonIdentityError(41, 42)
	want := "PID file has 41, socket reports 42"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("daemonIdentityError = %v", err)
	}
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

func countAcceptedExecutionResponses(t *testing.T, responses <-chan int, count int) int {
	t.Helper()
	accepted := 0
	timeout := time.After(time.Second)
	for range count {
		select {
		case status := <-responses:
			if status == http.StatusAccepted {
				accepted++
			} else if status != http.StatusServiceUnavailable {
				t.Fatalf("unexpected admission status: %d", status)
			}
		case <-timeout:
			t.Fatal("admissions did not finish after cancellation")
		}
	}
	return accepted
}

func concurrentExecutionRequests(d *Daemon, body string, count int) <-chan int {
	responses := make(chan int, count)
	for range count {
		go func() {
			response := handleExecutionAPIRequest(d, http.MethodPost, "/api/v1/executions", body)
			responses <- response.Code
		}()
	}
	return responses
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

func assertReadOnlyAPIRejectsWrite(t *testing.T, test readOnlyAPICase) {
	t.Helper()
	recorder := httptest.NewRecorder()
	test.handler(recorder, httptest.NewRequest(http.MethodPost, test.path, nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("%s status = %d, want 405", test.path, recorder.Code)
	}
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

func assertStorageFailureResponse(t *testing.T, recorder *httptest.ResponseRecorder, message string) {
	t.Helper()

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), message) {
		t.Fatalf("body = %q", recorder.Body.String())
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
