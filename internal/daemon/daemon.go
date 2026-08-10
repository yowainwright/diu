package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

const (
	maxExecutionRecordBodyBytes = 1 << 20
	maxRecordedCommandLength    = 4096
	socketControlTimeout        = time.Second
	socketRequestPing           = "ping"
	socketRequestStop           = "stop"
	socketStatusOK              = "ok"
	maxEventBatchSize           = 64
	maxSocketHandlers           = 16
	eventAdmissionTimeout       = time.Second
	defaultExecutionQueryLimit  = 100
)

type socketControlRequest struct {
	Type string `json:"type"`
}

type socketControlResponse struct {
	Status string `json:"status"`
	PID    int    `json:"pid"`
}

var daemonProcessSignaler = func(pid int, signal os.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(signal)
}

var daemonSocketControlSender = sendSocketControl

var ErrNotRunning = errors.New("daemon is not running")

type Daemon struct {
	config          *core.Config
	storage         storage.Storage
	registry        *monitors.MonitorRegistry
	eventChan       chan *core.ExecutionRecord
	httpServer      *http.Server
	socketListener  net.Listener
	socketInfo      os.FileInfo
	socketSlots     chan struct{}
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	startTime       time.Time
	stopOnce        sync.Once
	stopErr         error
	stopped         atomic.Bool
	logger          *log.Logger
	logSink         io.Closer
	startupWarnings []string
	pidFile         *os.File
	pidFileOwned    bool
	socketOwned     bool
}

func NewDaemon(config *core.Config) (*Daemon, error) {
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	registry := monitors.NewMonitorRegistry()
	var startupWarnings []string

	for _, tool := range config.Monitoring.EnabledTools {
		tool = core.NormalizeToolName(tool)
		var monitor monitors.Monitor
		switch tool {
		case core.ToolHomebrew:
			monitor = monitors.NewHomebrewMonitor()
		case core.ToolNPM:
			monitor = monitors.NewNPMMonitor()
		case core.ToolPNPM:
			monitor = monitors.NewPNPMMonitor()
		case core.ToolBun:
			monitor = monitors.NewBunMonitor()
		case core.ToolGo:
			monitor = monitors.NewGoMonitor()
		case core.ToolPip:
			monitor = monitors.NewPipMonitor()
		case core.ToolUV:
			monitor = monitors.NewUVMonitor()
		case core.ToolPoetry:
			monitor = monitors.NewPoetryMonitor()
		default:
			warning := fmt.Sprintf("Unknown tool: %s", tool)
			startupWarnings = append(startupWarnings, warning)
			continue
		}

		if err := monitor.Initialize(config); err != nil {
			warning := fmt.Sprintf("Failed to initialize %s monitor: %v", tool, err)
			startupWarnings = append(startupWarnings, warning)
			continue
		}
		registry.Register(monitor)
	}

	background := context.Background()
	ctx, cancel := context.WithCancel(background)
	eventChan := make(chan *core.ExecutionRecord, core.DefaultEventBuffer)
	socketSlots := make(chan struct{}, maxSocketHandlers)
	startTime := time.Now()

	d := &Daemon{
		config:          config,
		storage:         store,
		registry:        registry,
		eventChan:       eventChan,
		socketSlots:     socketSlots,
		ctx:             ctx,
		cancel:          cancel,
		startTime:       startTime,
		logger:          log.Default(),
		startupWarnings: startupWarnings,
	}

	return d, nil
}

func (d *Daemon) Start() error {
	if err := d.startLocalObservability(); err != nil {
		return fmt.Errorf("failed to initialize local observability: %w", err)
	}
	d.logStartup()
	if err := d.claimRuntimePaths(); err != nil {
		return d.failStart(err)
	}
	d.startBackgroundWorkers()
	if err := d.startConfiguredServices(); err != nil {
		return d.failStart(err)
	}
	d.handleSignals()
	return nil
}

func (d *Daemon) logStartup() {
	d.logger.Printf("Starting DIU daemon v%s", core.CurrentVersion())
	for _, warning := range d.startupWarnings {
		d.logger.Print(warning)
	}
}

func (d *Daemon) claimRuntimePaths() error {
	if err := d.rejectLivePID(); err != nil {
		return err
	}
	if err := d.startSocketListener(); err != nil {
		return fmt.Errorf("failed to start socket listener: %w", err)
	}
	if err := d.removeStalePIDFile(); err != nil {
		return fmt.Errorf("failed to remove stale PID file: %w", err)
	}
	if err := d.writePIDFile(); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	d.pidFileOwned = true
	return nil
}

func (d *Daemon) startBackgroundWorkers() {
	d.wg.Add(1)
	go d.processEvents()
	d.wg.Add(1)
	go d.runPeriodicCleanup()
}

func (d *Daemon) startConfiguredServices() error {
	if err := d.registry.StartAll(d.ctx, d.eventChan); err != nil {
		return fmt.Errorf("failed to start monitors: %w", err)
	}
	if !d.config.API.Enabled {
		return nil
	}
	if err := d.startHTTPServer(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}
	return nil
}

func (d *Daemon) rejectLivePID() error {
	pid, err := readPID(d.config.Daemon.PIDFile)
	if err != nil || !ProcessRunning(pid) {
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

func (d *Daemon) startLocalObservability() error {
	logger, sink, err := observability.NewLocalLogger(d.config.Daemon.DataDir)
	if err != nil {
		return err
	}
	d.logger = logger
	d.logSink = sink
	return nil
}

func (d *Daemon) failStart(startErr error) error {
	d.logger.Printf("Failed to start DIU daemon: %v", startErr)
	if stopErr := d.Stop(); stopErr != nil {
		return fmt.Errorf("%w; cleanup failed: %v", startErr, stopErr)
	}
	return startErr
}

func (d *Daemon) Stop() error {
	d.stopOnce.Do(d.stop)
	return d.stopErr
}

func (d *Daemon) stop() {
	d.logger.Println("Stopping DIU daemon...")
	d.stopped.Store(true)
	d.cancel()
	d.stopMonitors()
	d.stopHTTPServer()
	d.stopSocketListener()
	d.wg.Wait()
	d.closeStorage()
	d.releaseRuntimePaths()
	d.logger.Println("DIU daemon stopped")
	d.closeLogSink()
}

func (d *Daemon) stopMonitors() {
	d.recordStopError("stopping monitors", d.registry.StopAll())
}

func (d *Daemon) stopHTTPServer() {
	if d.httpServer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), core.DefaultShutdownTimeout)
	defer cancel()
	d.recordStopError("shutting down HTTP server", d.httpServer.Shutdown(ctx))
}

func (d *Daemon) stopSocketListener() {
	if d.socketListener != nil {
		d.recordStopError("closing socket listener", d.socketListener.Close())
	}
}

func (d *Daemon) closeStorage() {
	d.recordStopError("closing storage", d.storage.Close())
}

func (d *Daemon) releaseRuntimePaths() {
	if d.pidFileOwned {
		d.recordStopError("removing PID file", d.removeOwnedPIDFile())
	}
	d.recordStopError("closing PID file", d.closePIDFile())
	if d.socketOwned {
		d.recordStopError("removing socket file", d.removeOwnedSocket())
	}
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

func (d *Daemon) closeLogSink() {
	if d.logSink != nil {
		d.stopErr = errors.Join(d.stopErr, d.logSink.Close())
	}
}

func (d *Daemon) recordStopError(action string, err error) {
	if err == nil {
		return
	}
	d.logger.Printf("Error %s: %v", action, err)
	d.stopErr = errors.Join(d.stopErr, err)
}

func (d *Daemon) removeOwnedPIDFile() error {
	pid, err := readPID(d.config.Daemon.PIDFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || pid != os.Getpid() {
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
	if d.socketInfo == nil || !os.SameFile(d.socketInfo, info) {
		return nil
	}
	return os.Remove(d.config.Daemon.SocketPath)
}

func (d *Daemon) Wait() {
	d.wg.Wait()
}

func (d *Daemon) IsStopped() bool {
	return d.stopped.Load()
}

func (d *Daemon) processEvents() {
	defer d.wg.Done()

	for {
		select {
		case event, ok := <-d.eventChan:
			if !ok {
				return
			}
			events := d.collectEventBatch(event)
			d.storeExecutions(events)

		case <-d.ctx.Done():
			d.drainQueuedEvents()
			return
		}
	}
}

func (d *Daemon) drainQueuedEvents() {
	for {
		select {
		case event, ok := <-d.eventChan:
			if !ok {
				return
			}
			events := d.collectEventBatch(event)
			d.storeExecutions(events)
		default:
			return
		}
	}
}

func (d *Daemon) collectEventBatch(first *core.ExecutionRecord) []*core.ExecutionRecord {
	events := []*core.ExecutionRecord{first}
	for len(events) < maxEventBatchSize {
		select {
		case event, ok := <-d.eventChan:
			if !ok {
				return events
			}
			events = append(events, event)
		default:
			return events
		}
	}
	return events
}

type executionBatchStorage interface {
	AddExecutions(records []*core.ExecutionRecord) error
}

func (d *Daemon) storeExecutions(events []*core.ExecutionRecord) {
	for _, event := range events {
		d.enrichExecution(event)
	}
	batchStore, supportsBatching := d.storage.(executionBatchStorage)
	if supportsBatching {
		err := batchStore.AddExecutions(events)
		d.logStorageError(err)
		return
	}
	for _, event := range events {
		err := d.storage.AddExecution(event)
		d.logStorageError(err)
	}
}

func (d *Daemon) logStorageError(err error) {
	if err != nil {
		d.logger.Printf("Failed to store execution: %v", err)
	}
}

func (d *Daemon) enrichExecution(record *core.ExecutionRecord) {
	// Normalize tool name before looking up monitor
	record.Tool = core.NormalizeToolName(record.Tool)
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}

	monitor, ok := d.registry.Get(record.Tool)
	if !ok {
		return
	}
	monitors.EnrichExecutionRecord(monitor, record)
}

func (d *Daemon) runPeriodicCleanup() {
	defer d.wg.Done()
	d.pruneOldRecords()
	cleanupTicker := time.NewTicker(24 * time.Hour)
	defer cleanupTicker.Stop()
	backupTicker, backupEvents := d.backupSchedule()
	if backupTicker != nil {
		defer backupTicker.Stop()
	}
	for {
		select {
		case <-cleanupTicker.C:
			d.pruneOldRecords()
		case <-backupEvents:
			d.backupStorage()
		case <-d.ctx.Done():
			return
		}
	}
}

func (d *Daemon) backupSchedule() (*time.Ticker, <-chan time.Time) {
	if !d.config.Storage.BackupEnabled {
		return nil, nil
	}
	interval := d.config.Storage.BackupInterval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	ticker := time.NewTicker(interval)
	return ticker, ticker.C
}

func (d *Daemon) backupStorage() {
	if err := d.storage.Backup(); err != nil {
		d.logger.Printf("Failed to back up storage: %v", err)
	}
}

func (d *Daemon) pruneOldRecords() {
	emptyCutoff := time.Time{}
	if err := d.storage.Cleanup(emptyCutoff); err != nil {
		d.logger.Printf("Failed to prune old records: %v", err)
	}
}

func (d *Daemon) startSocketListener() error {
	listener, socketInfo, err := openSocketListener(d.config.Daemon.SocketPath)
	if err != nil {
		return err
	}
	d.socketListener = listener
	d.socketInfo = socketInfo
	d.socketOwned = true
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
	if unixListener, ok := listener.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
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
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect stale socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("failed to remove stale socket: path is not a socket: %s", path)
	}
	conn, dialErr := net.DialTimeout("unix", path, socketControlTimeout)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("failed to remove stale socket: socket is active: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("failed to remove stale socket: %w", err)
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
	if err := json.Unmarshal(raw, &control); err == nil && control.Type != "" {
		d.handleSocketControl(conn, control)
		return true
	}
	return false
}

func decodeSocketExecution(raw json.RawMessage) (*core.ExecutionRecord, error) {
	var record core.ExecutionRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (d *Daemon) admitSocketExecution(record *core.ExecutionRecord) {
	select {
	case d.eventChan <- record:
	case <-d.ctx.Done():
		d.logger.Printf("Daemon stopping, dropping socket event")
	case <-time.After(eventAdmissionTimeout):
		d.logger.Printf("Event queue full, rejecting socket event")
	}
}

func (d *Daemon) handleSocketControl(conn net.Conn, request socketControlRequest) {
	pid := os.Getpid()
	response := socketControlResponse{Status: socketStatusOK, PID: pid}
	switch request.Type {
	case socketRequestPing:
		_ = json.NewEncoder(conn).Encode(response)
	case socketRequestStop:
		_ = json.NewEncoder(conn).Encode(response)
		go func() {
			if err := d.Stop(); err != nil {
				d.logger.Printf("Error stopping daemon: %v", err)
			}
		}()
	default:
		d.logger.Printf("Unknown socket control request: %s", request.Type)
	}
}

func (d *Daemon) startHTTPServer() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/executions", d.handleExecutions)
	mux.HandleFunc("/api/v1/packages", d.handlePackages)
	mux.HandleFunc("/api/v1/stats", d.handleStats)
	mux.HandleFunc("/api/v1/health", d.handleHealth)

	addr := fmt.Sprintf("%s:%d", d.config.API.Host, d.config.API.Port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	actualAddr := listener.Addr().String()

	d.httpServer = &http.Server{
		Addr:              actualAddr,
		Handler:           mux,
		ReadTimeout:       core.DefaultSocketReadTimeout,
		ReadHeaderTimeout: core.DefaultShutdownTimeout,
		WriteTimeout:      core.DefaultSocketReadTimeout,
		IdleTimeout:       core.DefaultSocketReadTimeout,
	}

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.logger.Printf("HTTP API server listening on %s", actualAddr)
		if err := d.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			d.logger.Printf("HTTP server error: %v", err)
		}
	}()

	return nil
}

func (d *Daemon) handleExecutions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tool := core.NormalizeToolName(r.URL.Query().Get("tool"))
		packageName := r.URL.Query().Get("package")
		opts := storage.QueryOptions{
			Tool:    tool,
			Package: packageName,
			Limit:   defaultExecutionQueryLimit,
		}

		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			limit, err := strconv.Atoi(limitStr)
			if err != nil || limit < 0 {
				http.Error(w, "invalid limit", http.StatusBadRequest)
				return
			}
			opts.Limit = limit
		}

		executions, err := d.storage.GetExecutions(opts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(executions); err != nil {
			d.logger.Printf("Failed to encode executions response: %v", err)
		}

	case http.MethodPost:
		record, err := decodeExecutionRecordRequest(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		select {
		case d.eventChan <- record:
			w.WriteHeader(http.StatusAccepted)
		case <-d.ctx.Done():
			http.Error(w, "Daemon stopping", http.StatusServiceUnavailable)
		case <-r.Context().Done():
			http.Error(w, "Request canceled", http.StatusRequestTimeout)
		case <-time.After(eventAdmissionTimeout):
			http.Error(w, "Event queue full", http.StatusServiceUnavailable)
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(packages); err != nil {
		d.logger.Printf("Failed to encode packages response: %v", err)
	}
}

func (d *Daemon) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats, err := d.storage.GetStatistics()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		d.logger.Printf("Failed to encode stats response: %v", err)
	}
}

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	version := core.CurrentVersion()
	uptime := time.Since(d.startTime).String()
	monitorsActive := len(d.registry.GetAll())
	health := map[string]interface{}{
		"status":          "healthy",
		"version":         version,
		"uptime":          uptime,
		"monitors_active": monitorsActive,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(health); err != nil {
		d.logger.Printf("Failed to encode health response: %v", err)
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
	info, err := safefs.Lstat(d.config.Daemon.PIDFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("PID path is not a regular file: %s", d.config.Daemon.PIDFile)
	}
	pid, pidErr := readPID(d.config.Daemon.PIDFile)
	if pidErr != nil {
		return removePIDFile(d.config.Daemon.PIDFile)
	}
	locked, lockErr := pidFileLocked(d.config.Daemon.PIDFile, pid)
	if lockErr != nil {
		return lockErr
	}
	if locked {
		return fmt.Errorf("daemon PID file is still owned by a running process")
	}
	return removeUnlockedPIDFile(d.config.Daemon.PIDFile, pid)
}

func (d *Daemon) handleSignals() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer signal.Stop(sigChan)
		select {
		case sig := <-sigChan:
			d.logger.Printf("Received signal: %v", sig)
			go func() {
				if err := d.Stop(); err != nil {
					d.logger.Printf("Error stopping daemon: %v", err)
				}
			}()
		case <-d.ctx.Done():
			return
		}
	}()
}

func IsRunning(config *core.Config) bool {
	pid, err := readPID(config.Daemon.PIDFile)
	if err != nil {
		return false
	}
	response, err := sendSocketControl(config.Daemon.SocketPath, socketRequestPing)
	return err == nil && response.PID == pid
}

func RequestStop(config *core.Config) error {
	pid, err := readPID(config.Daemon.PIDFile)
	if err != nil {
		return fmt.Errorf("failed to read daemon PID: %w", err)
	}
	pingResponse, err := daemonSocketControlSender(config.Daemon.SocketPath, socketRequestPing)
	if err != nil {
		return signalLockedDaemonProcess(config, pid)
	}
	if pingResponse.PID != pid {
		return daemonIdentityError(pid, pingResponse.PID)
	}
	return requestSocketStop(config, pid)
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
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
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

func pidFileLocked(path string, expectedPID int) (locked bool, err error) {
	file, err := safefs.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()
	matches, err := pidFileContainsPID(file, expectedPID)
	if err != nil || !matches {
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
	if errors.Is(lockErr, syscall.EWOULDBLOCK) || errors.Is(lockErr, syscall.EAGAIN) {
		return true, nil
	}
	if lockErr != nil {
		return false, lockErr
	}
	return false, syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
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
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil || pid < 1 {
		return 0, fmt.Errorf("invalid PID %q", strings.TrimSpace(string(pidBytes)))
	}
	return pid, nil
}

func signalDaemonProcess(pid int) error {
	if err := daemonProcessSignaler(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to signal daemon process %d: %w", pid, err)
	}
	return nil
}

func ReadPID(config *core.Config) (int, error) {
	return readPID(config.Daemon.PIDFile)
}

func ProcessRunning(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func sendSocketControl(path, requestType string) (socketControlResponse, error) {
	conn, err := net.DialTimeout("unix", path, socketControlTimeout)
	if err != nil {
		return socketControlResponse{}, err
	}
	defer func() {
		_ = conn.Close()
	}()
	if err := conn.SetDeadline(time.Now().Add(socketControlTimeout)); err != nil {
		return socketControlResponse{}, err
	}
	request := socketControlRequest{Type: requestType}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return socketControlResponse{}, err
	}
	var response socketControlResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return socketControlResponse{}, err
	}
	if response.Status != socketStatusOK || response.PID < 1 {
		return socketControlResponse{}, fmt.Errorf("invalid daemon response")
	}
	return response, nil
}
