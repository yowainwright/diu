package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/storage"
)

type Daemon struct {
	config         *core.Config
	storage        storage.Storage
	registry       *monitors.MonitorRegistry
	eventChan      chan *core.ExecutionRecord
	httpServer     *http.Server
	socketListener net.Listener
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	startTime      time.Time
	stopOnce       sync.Once
	stopped        bool
}

func NewDaemon(config *core.Config) (*Daemon, error) {
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	registry := monitors.NewMonitorRegistry()

	for _, tool := range config.Monitoring.EnabledTools {
		var monitor monitors.Monitor
		switch tool {
		case core.ToolHomebrew:
			monitor = monitors.NewHomebrewMonitor()
		case core.ToolNPM:
			monitor = monitors.NewNPMMonitor()
		case core.ToolGo:
			monitor = monitors.NewGoMonitor()
		default:
			log.Printf("Unknown tool: %s", tool)
			continue
		}

		if err := monitor.Initialize(config); err != nil {
			log.Printf("Failed to initialize %s monitor: %v", tool, err)
			continue
		}
		registry.Register(monitor)
	}

	ctx, cancel := context.WithCancel(context.Background())

	d := &Daemon{
		config:    config,
		storage:   store,
		registry:  registry,
		eventChan: make(chan *core.ExecutionRecord, 100),
		ctx:       ctx,
		cancel:    cancel,
		startTime: time.Now(),
	}

	return d, nil
}

func (d *Daemon) Start() error {
	log.Printf("Starting DIU daemon v0.1.0")

	if err := d.writePIDFile(); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}

	d.wg.Add(1)
	go d.processEvents()

	if err := d.registry.StartAll(d.ctx, d.eventChan); err != nil {
		return fmt.Errorf("failed to start monitors: %w", err)
	}

	if err := d.startSocketListener(); err != nil {
		log.Printf("Failed to start socket listener: %v", err)
	}

	if d.config.API.Enabled {
		if err := d.startHTTPServer(); err != nil {
			return fmt.Errorf("failed to start HTTP server: %w", err)
		}
	}

	d.handleSignals()

	return nil
}

func (d *Daemon) Stop() error {
	var stopErr error
	d.stopOnce.Do(func() {
		log.Println("Stopping DIU daemon...")
		d.stopped = true

		d.cancel()

		if err := d.registry.StopAll(); err != nil {
			log.Printf("Error stopping monitors: %v", err)
		}

		if d.httpServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := d.httpServer.Shutdown(ctx); err != nil {
				log.Printf("Error shutting down HTTP server: %v", err)
			}
		}

		if d.socketListener != nil {
			d.socketListener.Close()
		}

		close(d.eventChan)

		d.wg.Wait()

		if err := d.storage.Close(); err != nil {
			log.Printf("Error closing storage: %v", err)
		}

		if err := os.Remove(d.config.Daemon.PIDFile); err != nil && !os.IsNotExist(err) {
			log.Printf("Error removing PID file: %v", err)
		}

		log.Println("DIU daemon stopped")
	})
	return stopErr
}

func (d *Daemon) IsStopped() bool {
	return d.stopped
}

func (d *Daemon) processEvents() {
	defer d.wg.Done()

	for {
		select {
		case event, ok := <-d.eventChan:
			if !ok {
				return
			}
			if err := d.storage.AddExecution(event); err != nil {
				log.Printf("Failed to store execution: %v", err)
			}

		case <-d.ctx.Done():
			return
		}
	}
}

func (d *Daemon) startSocketListener() error {
	socketPath := core.DefaultSocketPath

	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to create socket: %w", err)
	}

	d.socketListener = listener

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-d.ctx.Done():
					return
				default:
					log.Printf("Socket accept error: %v", err)
					continue
				}
			}

			go d.handleSocketConnection(conn)
		}
	}()

	return nil
}

func (d *Daemon) handleSocketConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	var record core.ExecutionRecord
	if err := decoder.Decode(&record); err != nil {
		log.Printf("Failed to decode execution record: %v", err)
		return
	}

	select {
	case d.eventChan <- &record:
	case <-time.After(time.Second):
		log.Printf("Event channel full, dropping event")
	}
}

func (d *Daemon) startHTTPServer() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/executions", d.handleExecutions)
	mux.HandleFunc("/api/v1/packages", d.handlePackages)
	mux.HandleFunc("/api/v1/stats", d.handleStats)
	mux.HandleFunc("/api/v1/health", d.handleHealth)

	addr := fmt.Sprintf("%s:%d", d.config.API.Host, d.config.API.Port)
	d.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		log.Printf("HTTP API server listening on %s", addr)
		if err := d.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	return nil
}

func (d *Daemon) handleExecutions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		opts := storage.QueryOptions{
			Tool:    r.URL.Query().Get("tool"),
			Package: r.URL.Query().Get("package"),
		}

		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if limit, err := strconv.Atoi(limitStr); err == nil {
				opts.Limit = limit
			}
		}

		executions, err := d.storage.GetExecutions(opts)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(executions)

	case http.MethodPost:
		var record core.ExecutionRecord
		if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		select {
		case d.eventChan <- &record:
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, "Event queue full", http.StatusServiceUnavailable)
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *Daemon) handlePackages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tool := r.URL.Query().Get("tool")
	packages, err := d.storage.GetPackages(tool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(packages)
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
	json.NewEncoder(w).Encode(stats)
}

func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	health := map[string]interface{}{
		"status":          "healthy",
		"version":         "0.1.0",
		"uptime":          time.Since(d.startTime).String(),
		"monitors_active": len(d.registry.GetAll()),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func (d *Daemon) writePIDFile() error {
	pid := os.Getpid()
	return os.WriteFile(d.config.Daemon.PIDFile, []byte(strconv.Itoa(pid)), 0644)
}

func (d *Daemon) handleSignals() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		select {
		case sig := <-sigChan:
			log.Printf("Received signal: %v", sig)
			d.Stop()
		case <-d.ctx.Done():
			return
		}
	}()
}

func IsRunning(config *core.Config) bool {
	if _, err := os.Stat(config.Daemon.PIDFile); err != nil {
		return false
	}

	pidBytes, err := os.ReadFile(config.Daemon.PIDFile)
	if err != nil {
		return false
	}

	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	return err == nil
}
