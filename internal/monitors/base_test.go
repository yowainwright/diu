package monitors

import (
	"context"
	"errors"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

type mockMonitor struct {
	*BaseMonitor
	startCalled bool
	stopCalled  bool
}

func newMockMonitor(name string) *mockMonitor {
	return &mockMonitor{
		BaseMonitor: NewBaseMonitor(name),
	}
}

func (m *mockMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	m.startCalled = true
	return nil
}

func (m *mockMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	return nil, errors.New("not implemented")
}

func (m *mockMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	return &core.ExecutionRecord{
		Tool:    m.name,
		Command: cmd,
		Args:    args,
	}, nil
}

func TestBaseMonitor(t *testing.T) {
	monitor := NewBaseMonitor("test-monitor")

	if monitor.Name() != "test-monitor" {
		t.Errorf("Expected name 'test-monitor', got %s", monitor.Name())
	}
}

func TestBaseMonitorInitialize(t *testing.T) {
	config := core.DefaultConfig()
	monitor := NewBaseMonitor("test")

	err := monitor.Initialize(config)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if monitor.config != config {
		t.Error("Config not set after Initialize")
	}
}

func TestBaseMonitorStop(t *testing.T) {
	monitor := NewBaseMonitor("test")

	err := monitor.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	monitor.ctx = ctx
	monitor.cancel = cancel

	err = monitor.Stop()
	if err != nil {
		t.Fatalf("Stop with cancel failed: %v", err)
	}

	select {
	case <-ctx.Done():
	default:
		t.Error("Context should be cancelled after Stop")
	}
}

func TestMonitorRegistry(t *testing.T) {
	registry := NewMonitorRegistry()

	if registry.monitors == nil {
		t.Fatal("Registry monitors map should be initialized")
	}

	if len(registry.monitors) != 0 {
		t.Error("Registry should start empty")
	}
}

func TestMonitorRegistryRegister(t *testing.T) {
	registry := NewMonitorRegistry()

	monitor1 := newMockMonitor("monitor1")
	monitor2 := newMockMonitor("monitor2")

	registry.Register(monitor1)
	registry.Register(monitor2)

	if len(registry.monitors) != 2 {
		t.Errorf("Expected 2 monitors, got %d", len(registry.monitors))
	}
}

func TestMonitorRegistryGet(t *testing.T) {
	registry := NewMonitorRegistry()

	monitor := newMockMonitor("test-monitor")
	registry.Register(monitor)

	retrieved, exists := registry.Get("test-monitor")
	if !exists {
		t.Error("Monitor should exist")
	}
	if retrieved.Name() != "test-monitor" {
		t.Error("Retrieved wrong monitor")
	}

	_, exists = registry.Get("nonexistent")
	if exists {
		t.Error("Nonexistent monitor should not be found")
	}
}

func TestMonitorRegistryGetAll(t *testing.T) {
	registry := NewMonitorRegistry()

	monitor1 := newMockMonitor("monitor1")
	monitor2 := newMockMonitor("monitor2")
	monitor3 := newMockMonitor("monitor3")

	registry.Register(monitor1)
	registry.Register(monitor2)
	registry.Register(monitor3)

	all := registry.GetAll()
	if len(all) != 3 {
		t.Errorf("Expected 3 monitors, got %d", len(all))
	}

	names := make(map[string]bool)
	for _, m := range all {
		names[m.Name()] = true
	}

	for _, name := range []string{"monitor1", "monitor2", "monitor3"} {
		if !names[name] {
			t.Errorf("Monitor %s not found in GetAll", name)
		}
	}
}

func TestMonitorRegistryInitializeAll(t *testing.T) {
	registry := NewMonitorRegistry()
	config := core.DefaultConfig()

	monitor1 := newMockMonitor("monitor1")
	monitor2 := newMockMonitor("monitor2")

	registry.Register(monitor1)
	registry.Register(monitor2)

	err := registry.InitializeAll(config)
	if err != nil {
		t.Fatalf("InitializeAll failed: %v", err)
	}

	if monitor1.config != config {
		t.Error("monitor1 not initialized")
	}
	if monitor2.config != config {
		t.Error("monitor2 not initialized")
	}
}

func TestMonitorRegistryStopAll(t *testing.T) {
	registry := NewMonitorRegistry()

	monitor1 := newMockMonitor("monitor1")
	monitor2 := newMockMonitor("monitor2")

	registry.Register(monitor1)
	registry.Register(monitor2)

	err := registry.StopAll()
	if err != nil {
		t.Fatalf("StopAll failed: %v", err)
	}
}

func TestMonitorRegistryStartAll(t *testing.T) {
	registry := NewMonitorRegistry()
	config := core.DefaultConfig()

	monitor1 := newMockMonitor("monitor1")
	monitor2 := newMockMonitor("monitor2")

	monitor1.Initialize(config)
	monitor2.Initialize(config)

	registry.Register(monitor1)
	registry.Register(monitor2)

	ctx := context.Background()
	eventChan := make(chan *core.ExecutionRecord, 10)

	err := registry.StartAll(ctx, eventChan)
	if err != nil {
		t.Fatalf("StartAll failed: %v", err)
	}
}

func TestMonitorRegistryOverwrite(t *testing.T) {
	registry := NewMonitorRegistry()

	monitor1 := newMockMonitor("same-name")
	monitor2 := newMockMonitor("same-name")

	registry.Register(monitor1)
	registry.Register(monitor2)

	if len(registry.monitors) != 1 {
		t.Errorf("Expected 1 monitor after overwrite, got %d", len(registry.monitors))
	}

	retrieved, _ := registry.Get("same-name")
	if retrieved != monitor2 {
		t.Error("Second registration should overwrite first")
	}
}

func TestContainsHelper(t *testing.T) {
	tests := []struct {
		slice    []string
		item     string
		expected bool
	}{
		{[]string{"a", "b", "c"}, "b", true},
		{[]string{"a", "b", "c"}, "d", false},
		{[]string{}, "a", false},
		{[]string{"test"}, "test", true},
		{[]string{"Test"}, "test", false},
	}

	for _, tt := range tests {
		result := contains(tt.slice, tt.item)
		if result != tt.expected {
			t.Errorf("contains(%v, %s) = %v, expected %v",
				tt.slice, tt.item, result, tt.expected)
		}
	}
}
