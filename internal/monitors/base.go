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