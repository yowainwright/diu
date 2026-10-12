package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/storage"
)

var ErrNotRunning = errors.New("daemon is not running")

func (d *Daemon) SetInventoryRefresh(refresh func(context.Context) error) {
	d.refreshInventory = refresh
}

func NewDaemon(config *core.Config) (*Daemon, error) {
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}
	registry, startupWarnings := initializedMonitorRegistry(config)
	return newDaemon(config, store, registry, startupWarnings), nil
}

func initializedMonitorRegistry(config *core.Config) (*monitors.MonitorRegistry, []string) {
	registry := monitors.NewMonitorRegistry()
	var startupWarnings []string
	for _, tool := range config.Monitoring.EnabledTools {
		tool = core.NormalizeToolName(tool)
		monitor, err := monitors.NewMonitor(tool)
		if err != nil {
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
	return registry, startupWarnings
}

func newDaemon(
	config *core.Config,
	store storage.Storage,
	registry *monitors.MonitorRegistry,
	startupWarnings []string,
) *Daemon {
	daemon := &Daemon{
		config:          config,
		storage:         store,
		registry:        registry,
		logger:          log.Default(),
		startupWarnings: startupWarnings,
	}
	daemon.initRuntime()
	return daemon
}

func (d *Daemon) initRuntime() {
	background := context.Background()
	d.ctx, d.cancel = context.WithCancel(background)
	d.eventChan = make(chan *core.ExecutionRecord, core.DefaultEventBuffer)
	d.socketSlots = make(chan struct{}, maxSocketHandlers)
	d.startTime = time.Now()
}

func (d *Daemon) Start() error {
	if err := d.startLocalObservability(); err != nil {
		return fmt.Errorf("failed to initialize local observability: %w", err)
	}
	d.logStartup()
	if err := d.prepareStorage(); err != nil {
		return d.failStart(err)
	}
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

func (d *Daemon) prepareStorage() error {
	if err := d.storage.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare storage: %w", err)
	}
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
	d.hasPIDFileOwnership = true
	return nil
}

func (d *Daemon) startBackgroundWorkers() {
	d.wg.Add(1)
	go d.processEvents()
	d.wg.Add(1)
	go d.runPeriodicCleanup()
	d.startInventoryRefresh()
}

func (d *Daemon) startInventoryRefresh() {
	if d.refreshInventory == nil {
		return
	}
	monitoring := d.config.Monitoring
	interval := monitoring.Filesystem.ScanInterval
	if interval <= 0 {
		return
	}
	d.wg.Add(1)
	go d.runInventoryRefresh(interval)
}

func (d *Daemon) runInventoryRefresh(interval time.Duration) {
	defer d.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-timer.C:
			if err := d.refreshInventory(d.ctx); err != nil {
				d.logger.Printf("Failed to refresh inventory: %v", err)
			}
			timer.Reset(interval)
		}
	}
}

func (d *Daemon) startConfiguredServices() error {
	if err := d.registry.StartAll(d.ctx, d.eventChan); err != nil {
		return fmt.Errorf("failed to start monitors: %w", err)
	}
	if !d.config.API.IsEnabled {
		return nil
	}
	if err := d.startHTTPServer(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}
	return nil
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
	stopped := true
	d.stopped.Store(stopped)
	d.cancel()
	d.recordStopError("stopping monitors", d.registry.StopAll())
	d.stopHTTPServer()
	d.stopSocketListener()
	d.wg.Wait()
	d.recordStopError("closing storage", d.storage.Close())
	d.releaseRuntimePaths()
	d.logger.Println("DIU daemon stopped")
	d.closeLogSink()
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

func (d *Daemon) releaseRuntimePaths() {
	if d.hasPIDFileOwnership {
		d.recordStopError("removing PID file", d.removeOwnedPIDFile())
	}
	d.recordStopError("closing PID file", d.closePIDFile())
	if d.hasSocketOwnership {
		d.recordStopError("removing socket file", d.removeOwnedSocket())
	}
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
	d.admissionMu.Lock()
	defer d.admissionMu.Unlock()
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

func (d *Daemon) storeExecutions(events []*core.ExecutionRecord) {
	for _, event := range events {
		d.enrichExecution(event)
	}
	d.logStorageError(d.storage.AddExecutions(events))
}

func (d *Daemon) logStorageError(err error) {
	if err != nil {
		d.logger.Printf("Failed to store execution: %v", err)
	}
}

func (d *Daemon) enrichExecution(record *core.ExecutionRecord) {
	record.Tool = core.NormalizeToolName(record.Tool)
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if record.Kind == core.ExecutionKindLaunchAttempt {
		return
	}

	monitor, ok := d.registry.Get(record.Tool)
	if !ok {
		return
	}
	monitors.EnrichExecutionRecord(monitor, record)
}

func (d *Daemon) runPeriodicCleanup() {
	defer d.wg.Done()
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
	if !d.config.Storage.IsBackupEnabled {
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
			go d.stopAfterRequest()
		case <-d.ctx.Done():
			return
		}
	}()
}

func (d *Daemon) stopAfterRequest() {
	if err := d.Stop(); err != nil {
		d.logger.Printf("Error stopping daemon: %v", err)
	}
}

func IsRunning(config *core.Config) bool {
	pid, err := readPID(config.Daemon.PIDFile)
	if err != nil {
		return false
	}
	response, err := sendSocketControl(config.Daemon.SocketPath, socketRequestPing)
	matchesPID := err == nil && response.PID == pid
	return matchesPID
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

func ReadPID(config *core.Config) (int, error) {
	return readPID(config.Daemon.PIDFile)
}

func ProcessRunning(pid int) bool {
	err := syscall.Kill(pid, 0)
	hasProcess := err == nil || errors.Is(err, syscall.EPERM)
	return hasProcess
}
