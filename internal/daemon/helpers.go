package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

var daemonProcessSignaler = func(pid int, signal os.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(signal)
}

var daemonSocketControlSender = sendSocketControl

var (
	errDaemonStopping = errors.New("daemon stopping")
	errEventQueueFull = errors.New("event queue full")
)

func (d *Daemon) rejectLivePID() error {
	pid, err := readPID(d.config.Daemon.PIDFile)
	if shouldIgnorePID(err, pid) {
		return nil
	}
	locked, err := pidFileLocked(d.config.Daemon.PIDFile, pid)
	if err != nil {
		return fmt.Errorf("failed to verify existing PID file: %w", err)
	}
	if !locked {
		return nil
	}
	return fmt.Errorf("daemon process %d is already running", pid)
}

func shouldIgnorePID(err error, pid int) bool {
	if err != nil {
		return true
	}
	return !ProcessRunning(pid)
}

func (d *Daemon) closePIDFile() error {
	if d.pidFile == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(d.pidFile.Fd()), syscall.LOCK_UN)
	closeErr := d.pidFile.Close()
	d.pidFile = nil
	return errors.Join(unlockErr, closeErr)
}

func (d *Daemon) removeOwnedPIDFile() error {
	pid, err := readPID(d.config.Daemon.PIDFile)
	if os.IsNotExist(err) {
		return nil
	}
	shouldKeepPIDFile := err != nil || pid != os.Getpid()
	if shouldKeepPIDFile {
		return nil
	}
	return os.Remove(d.config.Daemon.PIDFile)
}

func (d *Daemon) removeOwnedSocket() error {
	info, err := os.Lstat(d.config.Daemon.SocketPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	shouldKeepSocket := d.socketInfo == nil || !os.SameFile(d.socketInfo, info)
	if shouldKeepSocket {
		return nil
	}
	return os.Remove(d.config.Daemon.SocketPath)
}

func (d *Daemon) startSocketListener() error {
	listener, socketInfo, err := openSocketListener(d.config.Daemon.SocketPath)
	if err != nil {
		return err
	}
	d.socketListener = listener
	d.socketInfo = socketInfo
	d.hasSocketOwnership = true
	d.startSocketAcceptLoop(listener)
	return nil
}

func openSocketListener(path string) (net.Listener, os.FileInfo, error) {
	if err := prepareSocketPath(path); err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create socket: %w", err)
	}
	disableSocketAutoUnlink(listener)
	info, err := secureAndInspectSocket(listener, path)
	return listener, info, err
}

func prepareSocketPath(path string) error {
	if err := removeStaleSocket(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), core.OwnerDirectoryMode); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}
	return nil
}

func disableSocketAutoUnlink(listener net.Listener) {
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		return
	}
	unlinkOnClose := false
	unixListener.SetUnlinkOnClose(unlinkOnClose)
}

func secureAndInspectSocket(listener net.Listener, path string) (os.FileInfo, error) {
	if err := os.Chmod(path, core.PrivateFileMode); err != nil {
		cleanupFailedSocket(listener, path)
		return nil, fmt.Errorf("failed to secure socket: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		cleanupFailedSocket(listener, path)
		return nil, fmt.Errorf("failed to inspect socket: %w", err)
	}
	return info, nil
}

func cleanupFailedSocket(listener net.Listener, path string) {
	_ = listener.Close()
	_ = os.Remove(path)
}

func (d *Daemon) startSocketAcceptLoop(listener net.Listener) {
	d.wg.Add(1)
	go d.acceptSocketConnections(listener)
}

func (d *Daemon) acceptSocketConnections(listener net.Listener) {
	defer d.wg.Done()
	for d.acceptSocketConnection(listener) {
	}
}

func (d *Daemon) acceptSocketConnection(listener net.Listener) bool {
	if !d.acquireSocketSlot() {
		return false
	}
	conn, err := listener.Accept()
	if err != nil {
		d.releaseSocketSlot()
		return d.continueAfterAcceptError(err)
	}
	d.startSocketHandler(conn)
	return true
}

func (d *Daemon) continueAfterAcceptError(err error) bool {
	if d.ctx.Err() != nil {
		return false
	}
	d.logger.Printf("Socket accept error: %v", err)
	return true
}

func (d *Daemon) startSocketHandler(conn net.Conn) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer d.releaseSocketSlot()
		d.handleSocketConnection(conn)
	}()
}

func (d *Daemon) acquireSocketSlot() bool {
	select {
	case d.socketSlots <- struct{}{}:
		return true
	case <-d.ctx.Done():
		return false
	}
}

func (d *Daemon) releaseSocketSlot() {
	<-d.socketSlots
}

func removeStaleSocket(path string) error {
	exists, err := validateStaleSocket(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := rejectActiveSocket(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to remove stale socket: %w", err)
	}
	return nil
}

func validateStaleSocket(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to inspect stale socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return false, fmt.Errorf("failed to remove stale socket: path is not a socket: %s", path)
	}
	return true, nil
}

func rejectActiveSocket(path string) error {
	conn, dialErr := net.DialTimeout("unix", path, socketControlTimeout)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("failed to remove stale socket: socket is active: %s", path)
	}
	return nil
}

func (d *Daemon) handleSocketConnection(conn net.Conn) {
	defer d.closeSocketConnection(conn)
	raw, err := d.readSocketRequest(conn)
	if err != nil {
		d.logger.Printf("Failed to decode socket request: %v", err)
		return
	}
	if d.handleSocketControlMessage(conn, raw) {
		return
	}
	record, err := decodeSocketExecution(raw)
	if err != nil {
		d.logger.Printf("Failed to decode execution record: %v", err)
		return
	}
	d.admitSocketExecution(record)
}

func (d *Daemon) closeSocketConnection(conn net.Conn) {
	if err := conn.Close(); err != nil {
		d.logger.Printf("Error closing socket connection: %v", err)
	}
}

func (d *Daemon) readSocketRequest(conn net.Conn) (json.RawMessage, error) {
	readDeadline := time.Now().Add(core.DefaultSocketReadTimeout)
	if err := conn.SetReadDeadline(readDeadline); err != nil {
		d.logger.Printf("Failed to set socket read deadline: %v", err)
	}
	decoder := json.NewDecoder(io.LimitReader(conn, maxExecutionRecordBodyBytes+1))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if len(raw) > maxExecutionRecordBodyBytes {
		return nil, fmt.Errorf("socket request exceeds %d bytes", maxExecutionRecordBodyBytes)
	}
	return raw, nil
}

func (d *Daemon) handleSocketControlMessage(conn net.Conn, raw json.RawMessage) bool {
	var control socketControlRequest
	err := json.Unmarshal(raw, &control)
	if err != nil {
		return false
	}
	if control.Type == "" {
		return false
	}
	d.handleSocketControl(conn, control)
	return true
}

func decodeSocketExecution(raw json.RawMessage) (*core.ExecutionRecord, error) {
	var record core.ExecutionRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (d *Daemon) admitSocketExecution(record *core.ExecutionRecord) {
	if err := d.admitExecution(d.ctx, record); err != nil {
		d.logger.Printf("Failed to admit socket event: %v", err)
	}
}

func (d *Daemon) admitExecution(ctx context.Context, record *core.ExecutionRecord) error {
	d.admissionMu.RLock()
	defer d.admissionMu.RUnlock()
	if d.ctx.Err() != nil {
		return errDaemonStopping
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.enqueueExecution(ctx, record)
}

func (d *Daemon) enqueueExecution(ctx context.Context, record *core.ExecutionRecord) error {
	select {
	case d.eventChan <- record:
		return nil
	case <-d.ctx.Done():
		return errDaemonStopping
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(eventAdmissionTimeout):
		return errEventQueueFull
	}
}

func (d *Daemon) handleSocketControl(conn net.Conn, request socketControlRequest) {
	pid := os.Getpid()
	response := socketControlResponse{Status: socketStatusOK, PID: pid}
	switch request.Type {
	case socketRequestPing, socketRequestStop:
		_ = json.NewEncoder(conn).Encode(response)
		if request.Type == socketRequestStop {
			go d.stopAfterRequest()
		}
	default:
		d.logger.Printf("Unknown socket control request: %s", request.Type)
	}
}

func (d *Daemon) startHTTPServer() error {
	addr := fmt.Sprintf("%s:%d", d.config.API.Host, d.config.API.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	actualAddr := listener.Addr().String()
	mux := d.httpMux()
	d.httpServer = httpServer(actualAddr, mux)
	d.serveHTTP(listener, actualAddr)
	return nil
}

func (d *Daemon) httpMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/executions", d.handleExecutions)
	mux.HandleFunc("/api/v1/packages", d.handlePackages)
	mux.HandleFunc("/api/v1/stats", d.handleStats)
	mux.HandleFunc("/api/v1/health", d.handleHealth)
	return mux
}

func httpServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       core.DefaultSocketReadTimeout,
		ReadHeaderTimeout: core.DefaultShutdownTimeout,
		WriteTimeout:      core.DefaultSocketReadTimeout,
		IdleTimeout:       core.DefaultSocketReadTimeout,
	}
}

func (d *Daemon) serveHTTP(listener net.Listener, actualAddr string) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.logger.Printf("HTTP API server listening on %s", actualAddr)
		err := d.httpServer.Serve(listener)
		if err == nil {
			return
		}
		if err == http.ErrServerClosed {
			return
		}
		d.logger.Printf("HTTP server error: %v", err)
	}()
}

func (d *Daemon) handleExecutions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		d.handleGetExecutions(w, r)
	case http.MethodPost:
		d.handlePostExecution(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *Daemon) handleGetExecutions(w http.ResponseWriter, r *http.Request) {
	opts, ok := executionQueryOptions(w, r)
	if !ok {
		return
	}
	executions, err := d.storage.GetExecutions(opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.writeJSONResponse(w, executions, "executions")
}

func executionQueryOptions(w http.ResponseWriter, r *http.Request) (storage.QueryOptions, bool) {
	opts := storage.QueryOptions{
		Tool:    core.NormalizeToolName(r.URL.Query().Get("tool")),
		Package: r.URL.Query().Get("package"),
		Limit:   defaultExecutionQueryLimit,
	}
	limit, ok := executionQueryLimit(w, r)
	if !ok {
		return opts, false
	}
	opts.Limit = limit
	return opts, true
}

func executionQueryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limitStr := r.URL.Query().Get("limit")
	if limitStr == "" {
		return defaultExecutionQueryLimit, true
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		http.Error(w, "invalid limit", http.StatusBadRequest)
		return 0, false
	}
	if limit < 0 {
		http.Error(w, "invalid limit", http.StatusBadRequest)
		return 0, false
	}
	return limit, true
}

func (d *Daemon) handlePostExecution(w http.ResponseWriter, r *http.Request) {
	record, err := decodeExecutionRecordRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := d.admitExecution(r.Context(), record); err != nil {
		writeAdmissionError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func writeAdmissionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errDaemonStopping):
		http.Error(w, "Daemon stopping", http.StatusServiceUnavailable)
	case errors.Is(err, errEventQueueFull):
		http.Error(w, "Event queue full", http.StatusServiceUnavailable)
	default:
		http.Error(w, "Request canceled", http.StatusRequestTimeout)
	}
}

func decodeExecutionRecordRequest(w http.ResponseWriter, r *http.Request) (*core.ExecutionRecord, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxExecutionRecordBodyBytes)

	decoder := json.NewDecoder(r.Body)
	var record core.ExecutionRecord
	if err := decoder.Decode(&record); err != nil {
		return nil, err
	}
	emptyObject := &struct{}{}
	if err := decoder.Decode(emptyObject); err != io.EOF {
		return nil, fmt.Errorf("request body must contain a single JSON object")
	}
	if err := validateExecutionRecord(record); err != nil {
		return nil, err
	}
	return &record, nil
}

func validateExecutionRecord(record core.ExecutionRecord) error {
	if strings.TrimSpace(record.Tool) == "" {
		return fmt.Errorf("tool is required")
	}
	if strings.TrimSpace(record.Command) == "" {
		return fmt.Errorf("command is required")
	}
	if len(record.Command) > maxRecordedCommandLength {
		return fmt.Errorf("command exceeds %d bytes", maxRecordedCommandLength)
	}
	if record.Duration < 0 {
		return fmt.Errorf("duration_ms must be non-negative")
	}
	for _, pkg := range record.PackagesAffected {
		if strings.TrimSpace(pkg) == "" {
			return fmt.Errorf("packages_affected cannot contain empty values")
		}
	}
	return nil
}

func (d *Daemon) handlePackages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tool := core.NormalizeToolName(r.URL.Query().Get("tool"))
	packages, err := d.storage.GetPackages(tool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	d.writeJSONResponse(w, packages, "packages")
}

func (d *Daemon) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats, err := d.storage.Statistics()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	d.writeJSONResponse(w, stats, "stats")
}

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	d.writeJSONResponse(w, d.healthResponse(), "health")
}

func (d *Daemon) writeJSONResponse(w http.ResponseWriter, value any, resource string) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		d.logger.Printf("Failed to encode %s response: %v", resource, err)
	}
}

func (d *Daemon) healthResponse() map[string]interface{} {
	version := core.CurrentVersion()
	uptime := time.Since(d.startTime).String()
	monitorsActive := len(d.registry.All())
	return map[string]interface{}{
		"status":          "healthy",
		"version":         version,
		"uptime":          uptime,
		"monitors_active": monitorsActive,
	}
}

func (d *Daemon) writePIDFile() error {
	if err := os.MkdirAll(filepath.Dir(d.config.Daemon.PIDFile), core.OwnerDirectoryMode); err != nil {
		return err
	}
	file, err := openLockedPIDFile(d.config.Daemon.PIDFile)
	if err != nil {
		return err
	}
	data := []byte(strconv.Itoa(os.Getpid()))
	_, writeErr := file.Write(data)
	if writeErr != nil {
		_ = file.Close()
		_ = os.Remove(d.config.Daemon.PIDFile)
		return writeErr
	}
	d.pidFile = file
	return nil
}

func openLockedPIDFile(path string) (*os.File, error) {
	flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
	file, err := safefs.OpenFile(path, flags, core.PrivateFileMode)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("failed to lock PID file: %w", err)
	}
	return file, nil
}

func (d *Daemon) removeStalePIDFile() error {
	path := d.config.Daemon.PIDFile
	if err := validateStalePIDFile(path); err != nil {
		return err
	}
	pid, pidErr := readPID(path)
	if pidErr != nil {
		return removePIDFile(path)
	}
	return removeUnlockedPIDFileIfFree(path, pid)
}

func validateStalePIDFile(path string) error {
	info, err := safefs.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("PID path is not a regular file: %s", path)
	}
	return nil
}

func removeUnlockedPIDFileIfFree(path string, pid int) error {
	locked, lockErr := pidFileLocked(path, pid)
	if lockErr != nil {
		return lockErr
	}
	if locked {
		return fmt.Errorf("daemon PID file is still owned by a running process")
	}
	return removeUnlockedPIDFile(path, pid)
}

func requestSocketStop(config *core.Config, pid int) error {
	response, err := daemonSocketControlSender(config.Daemon.SocketPath, socketRequestStop)
	if err != nil {
		return signalLockedDaemonProcess(config, pid)
	}
	if response.PID != pid {
		return daemonIdentityError(pid, response.PID)
	}
	return nil
}

func signalLockedDaemonProcess(config *core.Config, pid int) error {
	locked, err := pidFileLocked(config.Daemon.PIDFile, pid)
	if err != nil {
		return fmt.Errorf("failed to verify daemon PID file: %w", err)
	}
	if !locked {
		if err := removeUnlockedPIDFile(config.Daemon.PIDFile, pid); err != nil {
			return fmt.Errorf("failed to remove stale PID file: %w", err)
		}
		return ErrNotRunning
	}
	return signalDaemonProcess(pid)
}

func removePIDFile(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func removeUnlockedPIDFile(path string, expectedPID int) (err error) {
	file, err := safefs.OpenFile(path, os.O_RDONLY, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err := verifyPIDFile(file, expectedPID); err != nil {
		return err
	}
	if err := lockPIDFileForRemoval(file); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, syscall.Flock(int(file.Fd()), syscall.LOCK_UN)) }()
	return removeMatchingPIDFile(path, file)
}

func verifyPIDFile(file *os.File, expectedPID int) error {
	matches, err := pidFileContainsPID(file, expectedPID)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("daemon PID file changed while verifying process %d", expectedPID)
	}
	return nil
}

func lockPIDFileForRemoval(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if isPIDFileLockedError(err) {
		return fmt.Errorf("daemon PID file became locked")
	}
	return err
}

func removeMatchingPIDFile(path string, file *os.File) error {
	openedInfo, err := file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := safefs.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(openedInfo, pathInfo) {
		return fmt.Errorf("daemon PID file changed before removal")
	}
	return removePIDFile(path)
}

func pidFileLocked(path string, expectedPID int) (isLocked bool, err error) {
	file, err := safefs.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	matches, err := pidFileContainsPID(file, expectedPID)
	if err != nil {
		return false, err
	}
	if !matches {
		return false, err
	}
	return tryPIDFileLock(file)
}

func pidFileContainsPID(file *os.File, expectedPID int) (bool, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return false, err
	}
	pid, err := parsePID(data)
	if err != nil {
		return false, nil
	}
	return pid == expectedPID, nil
}

func tryPIDFileLock(file *os.File) (bool, error) {
	lockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if isPIDFileLockedError(lockErr) {
		return true, nil
	}
	if lockErr != nil {
		return false, lockErr
	}
	return false, syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func isPIDFileLockedError(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func daemonIdentityError(filePID, socketPID int) error {
	return fmt.Errorf("daemon identity mismatch: PID file has %d, socket reports %d", filePID, socketPID)
}

func readPID(path string) (int, error) {
	info, err := safefs.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("PID path is not a regular file: %s", path)
	}
	pidBytes, err := safefs.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return parsePID(pidBytes)
}

func parsePID(pidBytes []byte) (int, error) {
	text := strings.TrimSpace(string(pidBytes))
	pid, err := strconv.Atoi(text)
	invalidPID := err != nil || pid < 1
	if invalidPID {
		return 0, fmt.Errorf("invalid PID %q", text)
	}
	return pid, nil
}

func signalDaemonProcess(pid int) error {
	if err := daemonProcessSignaler(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to signal daemon process %d: %w", pid, err)
	}
	return nil
}

func sendSocketControl(path, requestType string) (socketControlResponse, error) {
	conn, err := dialSocketControl(path)
	if err != nil {
		return socketControlResponse{}, err
	}
	defer func() {
		_ = conn.Close()
	}()
	if err := writeSocketControlRequest(conn, requestType); err != nil {
		return socketControlResponse{}, err
	}
	return readSocketControlResponse(conn)
}

func dialSocketControl(path string) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", path, socketControlTimeout)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(socketControlTimeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func writeSocketControlRequest(conn net.Conn, requestType string) error {
	request := socketControlRequest{Type: requestType}
	return json.NewEncoder(conn).Encode(request)
}

func readSocketControlResponse(conn net.Conn) (socketControlResponse, error) {
	var response socketControlResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return socketControlResponse{}, err
	}
	if err := validateSocketControlResponse(response); err != nil {
		return socketControlResponse{}, err
	}
	return response, nil
}

func validateSocketControlResponse(response socketControlResponse) error {
	invalidResponse := response.Status != socketStatusOK || response.PID < 1
	if invalidResponse {
		return fmt.Errorf("invalid daemon response")
	}
	return nil
}
