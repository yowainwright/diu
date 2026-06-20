package monitors

import (
	"context"

	"github.com/yowainwright/diu/internal/core"
)

type Monitor interface {
	Name() string
	Initialize(config *core.Config) error
	Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error
	Stop() error
	GetInstalledPackages() ([]*core.PackageInfo, error)
	ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error)
}

type BaseMonitor struct {
	name   string
	config *core.Config
	ctx    context.Context
	cancel context.CancelFunc
}

func NewBaseMonitor(name string) *BaseMonitor {
	return &BaseMonitor{
		name: name,
	}
}

func (m *BaseMonitor) Name() string {
	return m.name
}

func (m *BaseMonitor) Initialize(config *core.Config) error {
	m.config = config
	return nil
}

func (m *BaseMonitor) Stop() error {
	if m.cancel != nil {
		m.cancel()
	}
	return nil
}

type MonitorRegistry struct {
	monitors map[string]Monitor
}

func NewMonitorRegistry() *MonitorRegistry {
	return &MonitorRegistry{
		monitors: make(map[string]Monitor),
	}
}

func (r *MonitorRegistry) Register(monitor Monitor) {
	r.monitors[monitor.Name()] = monitor
}

func (r *MonitorRegistry) Get(name string) (Monitor, bool) {
	monitor, exists := r.monitors[name]
	return monitor, exists
}

func (r *MonitorRegistry) GetAll() []Monitor {
	var monitors []Monitor
	for _, m := range r.monitors {
		monitors = append(monitors, m)
	}
	return monitors
}

func (r *MonitorRegistry) InitializeAll(config *core.Config) error {
	for _, monitor := range r.monitors {
		if err := monitor.Initialize(config); err != nil {
			return err
		}
	}
	return nil
}

func (r *MonitorRegistry) StartAll(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	for _, monitor := range r.monitors {
		if err := monitor.Start(ctx, eventChan); err != nil {
			return err
		}
	}
	return nil
}

func (r *MonitorRegistry) StopAll() error {
	for _, monitor := range r.monitors {
		if err := monitor.Stop(); err != nil {
			return err
		}
	}
	return nil
}

// EnrichExecutionRecord enriches an execution record with parsed metadata using the given monitor.
// This is a shared helper used by both the CLI and daemon to avoid code duplication.
// Note: The caller is responsible for normalizing the tool name and setting the timestamp before calling this function.
func EnrichExecutionRecord(monitor Monitor, record *core.ExecutionRecord) {
	parsed, err := monitor.ParseCommand(record.Command, record.Args)
	if err != nil {
		return
	}

	if len(record.PackagesAffected) == 0 {
		record.PackagesAffected = parsed.PackagesAffected
	}

	if len(parsed.Metadata) == 0 {
		return
	}
	if record.Metadata == nil {
		record.Metadata = make(map[string]interface{})
	}
	for key, value := range parsed.Metadata {
		if _, exists := record.Metadata[key]; !exists {
			record.Metadata[key] = value
		}
	}
}
