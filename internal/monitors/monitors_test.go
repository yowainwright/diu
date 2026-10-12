package monitors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestMain(m *testing.M) {
	os.Exit(runWithIsolatedHome(m))
}

func TestNewMonitorSupportsConfiguredTools(t *testing.T) {
	config := core.DefaultConfig()
	assertNewMonitorSupportsTools(t, config.Monitoring.EnabledTools)
	aliases := []string{"brew", "golang", "pip3", "python", "python3", " NPM ", " PNPM ", " BUN ", " UV ", " POETRY "}
	assertNewMonitorSupportsTools(t, aliases)
	assertNewMonitorRejectsBogusTool(t)
}

func assertNewMonitorSupportsTools(t *testing.T, tools []string) {
	t.Helper()
	for _, tool := range tools {
		monitor, err := NewMonitor(tool)
		hasMonitor := err == nil && monitor != nil
		if !hasMonitor {
			t.Fatalf("NewMonitor(%q) = %v, %v", tool, monitor, err)
		}
		if monitor.Name() != core.NormalizeToolName(tool) {
			t.Fatalf("NewMonitor(%q) selected %q", tool, monitor.Name())
		}
	}
}

func assertNewMonitorRejectsBogusTool(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"bogus", "unsupported"} {
		if _, err := NewMonitor(tool); err == nil {
			t.Fatalf("NewMonitor(%q) accepted an unsupported tool", tool)
		}
	}
}

type mockMonitor struct {
	*BaseMonitor
	hasStarted bool
}

func newMockMonitor(name string) *mockMonitor {
	return &mockMonitor{
		BaseMonitor: NewBaseMonitor(name),
	}
}

func (m *mockMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	m.hasStarted = true
	return nil
}

//nolint:legibility // Monitor interface requires this method name.
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

func TestStartCancelsContextOnStop(t *testing.T) {
	monitor := NewBaseMonitor("test")
	if err := monitor.Start(context.Background(), nil); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := monitor.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	assertContextCanceled(t, monitor.ctx)
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

	stopMonitorWithCancel(t, monitor)
	assertContextCanceled(t, ctx)
}

func stopMonitorWithCancel(t *testing.T, monitor *BaseMonitor) {
	t.Helper()

	if err := monitor.Stop(); err != nil {
		t.Fatalf("Stop with cancel failed: %v", err)
	}
}

func assertContextCanceled(t *testing.T, ctx context.Context) {
	t.Helper()

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

func TestMonitorRegistryAll(t *testing.T) {
	registry := NewMonitorRegistry()
	names := []string{"monitor1", "monitor2", "monitor3"}
	registerMockMonitors(registry, names...)

	all := registry.All()
	if len(all) != 3 {
		t.Errorf("Expected 3 monitors, got %d", len(all))
	}
	assertMonitorNames(t, all, names)
}

func registerMockMonitors(registry *MonitorRegistry, names ...string) []*mockMonitor {
	monitors := make([]*mockMonitor, 0, len(names))
	for _, name := range names {
		monitor := newMockMonitor(name)
		registry.Register(monitor)
		monitors = append(monitors, monitor)
	}
	return monitors
}

func assertMonitorNames(t *testing.T, monitors []Monitor, want []string) {
	t.Helper()

	seen := make(map[string]bool)
	for _, monitor := range monitors {
		seen[monitor.Name()] = true
	}
	for _, name := range want {
		if !seen[name] {
			t.Errorf("Monitor %s not found in GetAll", name)
		}
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
	monitors := registerInitializedMonitors(t, registry, config, "monitor1", "monitor2")

	ctx := context.Background()
	eventChan := make(chan *core.ExecutionRecord, 10)
	err := registry.StartAll(ctx, eventChan)
	if err != nil {
		t.Fatalf("StartAll failed: %v", err)
	}
	assertMonitorsStarted(t, monitors)
}

func registerInitializedMonitors(
	t *testing.T,
	registry *MonitorRegistry,
	config *core.Config,
	names ...string,
) []*mockMonitor {
	t.Helper()

	monitors := registerMockMonitors(registry, names...)
	for _, monitor := range monitors {
		if err := monitor.Initialize(config); err != nil {
			t.Fatalf("%s Initialize failed: %v", monitor.Name(), err)
		}
	}
	return monitors
}

func assertMonitorsStarted(t *testing.T, monitors []*mockMonitor) {
	t.Helper()

	for _, monitor := range monitors {
		if !monitor.hasStarted {
			t.Errorf("%s was not started", monitor.Name())
		}
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

type enrichMonitor struct {
	*BaseMonitor
	parsed *core.ExecutionRecord
	err    error
}

func (e *enrichMonitor) Start(ctx context.Context, eventChan chan<- *core.ExecutionRecord) error {
	return nil
}

//nolint:legibility // Monitor interface requires this method name.
func (e *enrichMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
	return nil, nil
}

func (e *enrichMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
	return e.parsed, e.err
}

func TestEnrichExecutionRecordParseError(t *testing.T) {
	m := &enrichMonitor{BaseMonitor: NewBaseMonitor("m"), err: errors.New("boom")}
	record := &core.ExecutionRecord{Command: "x"}

	EnrichExecutionRecord(m, record)

	if record.PackagesAffected != nil {
		t.Fatalf("shouldContain no packages, got %v", record.PackagesAffected)
	}
	if record.Metadata != nil {
		t.Fatalf("shouldContain no metadata, got %v", record.Metadata)
	}
}

func TestEnrichExecutionRecordFillsPackages(t *testing.T) {
	m := &enrichMonitor{
		BaseMonitor: NewBaseMonitor("m"),
		parsed: &core.ExecutionRecord{
			PackagesAffected: []string{"a", "b"},
		},
	}
	record := &core.ExecutionRecord{Command: "x"}

	EnrichExecutionRecord(m, record)

	hasTwoPackages := len(record.PackagesAffected) == 2
	hasFirstPackage := hasTwoPackages && record.PackagesAffected[0] == "a"
	if !hasFirstPackage {
		t.Fatalf("packages not filled: %v", record.PackagesAffected)
	}
}

func TestEnrichExecutionRecordDoesNotOverwritePackages(t *testing.T) {
	m := &enrichMonitor{
		BaseMonitor: NewBaseMonitor("m"),
		parsed: &core.ExecutionRecord{
			PackagesAffected: []string{"new"},
		},
	}
	record := &core.ExecutionRecord{PackagesAffected: []string{"existing"}}

	EnrichExecutionRecord(m, record)

	hasOnePackage := len(record.PackagesAffected) == 1
	hasExistingPackage := hasOnePackage && record.PackagesAffected[0] == "existing"
	if !hasExistingPackage {
		t.Fatalf("shouldContain packages preserved, got %v", record.PackagesAffected)
	}
}

func TestEnrichExecutionRecordMergesMetadata(t *testing.T) {
	m := &enrichMonitor{
		BaseMonitor: NewBaseMonitor("m"),
		parsed: &core.ExecutionRecord{
			Metadata: map[string]interface{}{"new": 1, "shared": "from-parse"},
		},
	}
	record := &core.ExecutionRecord{
		Metadata: map[string]interface{}{"shared": "from-record"},
	}

	EnrichExecutionRecord(m, record)

	if record.Metadata["new"] != 1 {
		t.Fatalf("shouldContain new key added, got %v", record.Metadata["new"])
	}
	if record.Metadata["shared"] != "from-record" {
		t.Fatalf("shouldContain shared preserved, got %v", record.Metadata["shared"])
	}
}

func TestEnrichExecutionRecordCreatesMetadataMap(t *testing.T) {
	m := &enrichMonitor{
		BaseMonitor: NewBaseMonitor("m"),
		parsed: &core.ExecutionRecord{
			Metadata: map[string]interface{}{"k": "v"},
		},
	}
	record := &core.ExecutionRecord{}

	EnrichExecutionRecord(m, record)

	hasMetadata := record.Metadata != nil
	hasValue := hasMetadata && record.Metadata["k"] == "v"
	if !hasValue {
		t.Fatalf("shouldContain metadata initialized, got %v", record.Metadata)
	}
}

func TestEnrichExecutionRecordEmptyMetadataSkipsAlloc(t *testing.T) {
	m := &enrichMonitor{
		BaseMonitor: NewBaseMonitor("m"),
		parsed:      &core.ExecutionRecord{},
	}
	record := &core.ExecutionRecord{}

	EnrichExecutionRecord(m, record)

	if record.Metadata != nil {
		t.Fatalf("shouldContain metadata stays nil, got %v", record.Metadata)
	}
}

func runWithIsolatedHome(m *testing.M) int {
	home, err := os.MkdirTemp("", "diu-monitors-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(home) }()
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

func prependFakeCommand(t *testing.T, name, script string) string {
	t.Helper()

	binDir := t.TempDir()
	path := writeFakeCommand(t, binDir, name, script)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func setOnlyFakeCommand(t *testing.T, name, script string) string {
	t.Helper()

	binDir := t.TempDir()
	path := writeFakeCommand(t, binDir, name, script)
	t.Setenv("PATH", binDir)
	return path
}

func writeFakeCommand(t *testing.T, binDir, name, script string) string {
	t.Helper()

	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("Failed to write fake %s command: %v", name, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("Failed to chmod fake %s command: %v", name, err)
	}
	return path
}
