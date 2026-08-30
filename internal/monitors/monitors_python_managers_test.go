package monitors

import (
	"context"
	"slices"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

const fakePip3JSONScript = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then
  printf '%s\n' '[{"name":"requests","version":"2.32.0"},{"name":"rich","version":"13.7.0"}]'
  exit 0
fi
exit 2
`

const fakePipJSONScript = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then
  printf '%s\n' '[{"name":"click","version":"8.1.7"}]'
  exit 0
fi
exit 2
`

const fakePipTextFallbackScript = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then
  printf 'not json\n'
  exit 0
fi
if [ "$1" = "list" ]; then
  printf 'Package Version\n------- -------\nrequests 2.32.0\nrich 13.7.0\n'
  exit 0
fi
exit 2
`

const fakeUVToolListScript = `#!/bin/sh
if [ "$1" = "tool" ] && [ "$2" = "list" ]; then
  printf 'ruff v0.5.0\n- ruff\nblack 24.4.2\n'
  exit 0
fi
exit 2
`

const fakeUVPipListFallbackScript = `#!/bin/sh
if [ "$1" = "tool" ] && [ "$2" = "list" ]; then
  exit 2
fi
if [ "$1" = "pip" ] && [ "$2" = "list" ] && [ "$3" = "--format=json" ]; then
  printf '[{"name":"httpx","version":"0.27.0"}]\n'
  exit 0
fi
exit 2
`

type pythonParseCase struct {
	name        string
	args        []string
	wantAction  string
	wantPackage string
}

type pythonParseMonitor interface {
	ParseCommand(command string, args []string) (*core.ExecutionRecord, error)
}

type pythonPackageMonitor interface {
	GetInstalledPackages() ([]*core.PackageInfo, error)
}

var uvParseCommandCases = []pythonParseCase{
	{name: "pip install", args: []string{"pip", "install", "httpx>=0.27"}, wantAction: "pip_install", wantPackage: "httpx"},
	{name: "pip uninstall", args: []string{"pip", "uninstall", "httpx"}, wantAction: "pip_uninstall", wantPackage: "httpx"},
	{name: "pip list", args: []string{"pip", "list"}, wantAction: "pip_list"},
	{name: "pip freeze", args: []string{"pip", "freeze"}, wantAction: "pip_freeze"},
	{name: "tool uninstall", args: []string{"tool", "uninstall", "ruff"}, wantAction: "tool_uninstall", wantPackage: "ruff"},
	{name: "tool run", args: []string{"tool", "run", "ruff"}, wantAction: "tool_run", wantPackage: "ruff"},
	{name: "tool run from package", args: []string{"tool", "run", "--from", "ruff", "ruff"}, wantAction: "tool_run", wantPackage: "ruff"},
	{name: "tool list", args: []string{"tool", "list"}, wantAction: "tool_list"},
	{name: "add", args: []string{"add", "pytest"}, wantAction: "add", wantPackage: "pytest"},
	{name: "remove", args: []string{"remove", "pytest"}, wantAction: "remove", wantPackage: "pytest"},
	{name: "sync", args: []string{"sync"}, wantAction: "sync"},
}

func TestPipParseCommand(t *testing.T) {
	monitor := NewPipMonitor().(*PipMonitor)

	record, err := monitor.ParseCommand("pip", []string{"install", "requests==2.32.0", "rich[all]>=13", "-r", "requirements.txt"})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	if record.Tool != core.ToolPip {
		t.Fatalf("Tool = %s, want %s", record.Tool, core.ToolPip)
	}
	wantPackages := []string{"requests", "rich"}
	if !slices.Equal(record.PackagesAffected, wantPackages) {
		t.Fatalf("PackagesAffected = %#v, want %#v", record.PackagesAffected, wantPackages)
	}
	if record.Metadata["action"] != "install" {
		t.Fatalf("Unexpected metadata: %#v", record.Metadata)
	}
}

func TestPipParseCommandVariants(t *testing.T) {
	monitor := NewPipMonitor().(*PipMonitor)
	tests := []struct {
		name        string
		args        []string
		wantAction  string
		wantPackage string
	}{
		{name: "uninstall", args: []string{"uninstall", "-y", "requests"}, wantAction: "uninstall", wantPackage: "requests"},
		{name: "list", args: []string{"list"}, wantAction: "list"},
		{name: "freeze", args: []string{"freeze"}, wantAction: "freeze"},
		{name: "show", args: []string{"show", "rich"}, wantAction: "show", wantPackage: "rich"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := monitor.ParseCommand("pip", tt.args)
			if err != nil {
				t.Fatalf("ParseCommand failed: %v", err)
			}
			if record.Metadata["action"] != tt.wantAction {
				t.Fatalf("action = %#v, want %s", record.Metadata["action"], tt.wantAction)
			}
			hasPackageWant := tt.wantPackage != ""
			packageMatches := pythonPackageAffectedMatches(record, tt.wantPackage)
			packageOK := !hasPackageWant || packageMatches
			if !packageOK {
				t.Fatalf("PackagesAffected = %#v, want %s", record.PackagesAffected, tt.wantPackage)
			}
		})
	}
}

func pythonPackageAffectedMatches(record *core.ExecutionRecord, expected string) bool {
	hasOnePackage := len(record.PackagesAffected) == 1
	hasExpectedPackage := hasOnePackage && record.PackagesAffected[0] == expected
	return hasExpectedPackage
}

func TestPipGetInstalledPackagesWithFakePip(t *testing.T) {
	prependFakeCommand(t, pip3CommandName, fakePip3JSONScript)
	monitor := initializedPipMonitor(t)
	assertPipCommandName(t, monitor, pip3CommandName)

	packages := installedPythonPackages(t, monitor)
	assertPythonPackageTool(t, packages, 2, "requests", core.ToolPip)
}

func TestPipGetInstalledPackagesFallsBackToPipCommand(t *testing.T) {
	setOnlyFakeCommand(t, pipCommandName, fakePipJSONScript)
	monitor := initializedPipMonitor(t)
	assertPipCommandName(t, monitor, pipCommandName)

	packages := installedPythonPackages(t, monitor)
	assertPythonPackage(t, packages, 1, "click", "8.1.7")
}

func TestPipGetInstalledPackagesFallsBackToText(t *testing.T) {
	prependFakeCommand(t, pip3CommandName, fakePipTextFallbackScript)
	monitor := initializedPipMonitor(t)

	packages := installedPythonPackages(t, monitor)
	assertPythonPackage(t, packages, 2, "requests", "2.32.0")
}

func TestPipGetInstalledPackagesRejectsUnsupportedCommand(t *testing.T) {
	monitor := NewPipMonitor().(*PipMonitor)
	monitor.commandName = "python"

	if _, err := monitor.GetInstalledPackages(); err == nil {
		t.Fatal("Expected unsupported pip command error")
	}
}

func TestUVParseCommand(t *testing.T) {
	monitor := NewUVMonitor().(*UVMonitor)

	record, err := monitor.ParseCommand("uv", []string{"tool", "install", "ruff==0.5.0"})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	if record.Tool != core.ToolUV {
		t.Fatalf("Tool = %s, want %s", record.Tool, core.ToolUV)
	}
	assertPythonPackageAffected(t, record, "ruff")
	if record.Metadata["action"] != "tool_install" {
		t.Fatalf("Unexpected metadata: %#v", record.Metadata)
	}
}

func TestUVParseCommandVariants(t *testing.T) {
	monitor := NewUVMonitor().(*UVMonitor)
	assertPythonParseCommandCases(t, monitor, "uv", uvParseCommandCases)
}

func TestUVGetInstalledPackagesWithFakeUV(t *testing.T) {
	prependFakeCommand(t, uvCommandName, fakeUVToolListScript)
	monitor := initializedUVMonitor(t)

	packages := installedPythonPackages(t, monitor)
	assertPythonPackage(t, packages, 2, "ruff", "v0.5.0")
}

func TestUVGetInstalledPackagesFallsBackToPipList(t *testing.T) {
	prependFakeCommand(t, uvCommandName, fakeUVPipListFallbackScript)
	monitor := initializedUVMonitor(t)

	packages := installedPythonPackages(t, monitor)
	assertPythonPackage(t, packages, 1, "httpx", "0.27.0")
}

func TestPoetryParseCommand(t *testing.T) {
	monitor := NewPoetryMonitor().(*PoetryMonitor)

	record, err := monitor.ParseCommand("poetry", []string{"self", "add", "poetry-plugin-export==1.8.0"})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	if record.Tool != core.ToolPoetry {
		t.Fatalf("Tool = %s, want %s", record.Tool, core.ToolPoetry)
	}
	assertPythonPackageAffected(t, record, "poetry-plugin-export")
	if record.Metadata["action"] != "self_add" {
		t.Fatalf("Unexpected metadata: %#v", record.Metadata)
	}
}

func TestPoetryParseCommandVariants(t *testing.T) {
	monitor := NewPoetryMonitor().(*PoetryMonitor)
	tests := []struct {
		name        string
		args        []string
		wantAction  string
		wantPackage string
	}{
		{name: "add", args: []string{"add", "pendulum>=3"}, wantAction: "add", wantPackage: "pendulum"},
		{name: "remove", args: []string{"remove", "pendulum"}, wantAction: "remove", wantPackage: "pendulum"},
		{name: "update", args: []string{"update", "pendulum"}, wantAction: "update", wantPackage: "pendulum"},
		{name: "show", args: []string{"show", "pendulum"}, wantAction: "show", wantPackage: "pendulum"},
		{name: "install", args: []string{"install"}, wantAction: "install"},
		{name: "self remove", args: []string{"self", "remove", "poetry-plugin-export"}, wantAction: "self_remove", wantPackage: "poetry-plugin-export"},
		{name: "self show", args: []string{"self", "show"}, wantAction: "self_show"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := monitor.ParseCommand("poetry", tt.args)
			if err != nil {
				t.Fatalf("ParseCommand failed: %v", err)
			}
			if record.Metadata["action"] != tt.wantAction {
				t.Fatalf("action = %#v, want %s", record.Metadata["action"], tt.wantAction)
			}
			assertOptionalPythonPackageAffected(t, record, tt.wantPackage)
		})
	}
}

func assertPythonPackage(t *testing.T, packages []*core.PackageInfo, count int, name, version string) {
	t.Helper()

	if len(packages) != count {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
	firstPackage := packages[0]
	if firstPackage.Name != name {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
	if firstPackage.Version != version {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
}

func initializedPipMonitor(t *testing.T) *PipMonitor {
	t.Helper()

	config := pythonProcessConfig()
	monitor := NewPipMonitor().(*PipMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return monitor
}

func initializedUVMonitor(t *testing.T) *UVMonitor {
	t.Helper()

	config := pythonProcessConfig()
	monitor := NewUVMonitor().(*UVMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return monitor
}

func pythonProcessConfig() *core.Config {
	config := core.DefaultConfig()
	config.Monitoring.Process.ShouldAutoInstallWrappers = false
	return config
}

func installedPythonPackages(t *testing.T, monitor pythonPackageMonitor) []*core.PackageInfo {
	t.Helper()

	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	return packages
}

func assertPipCommandName(t *testing.T, monitor *PipMonitor, expected string) {
	t.Helper()

	if monitor.commandName != expected {
		t.Fatalf("commandName = %s, want %s", monitor.commandName, expected)
	}
}

func assertPythonPackageTool(t *testing.T, packages []*core.PackageInfo, count int, name, tool string) {
	t.Helper()

	if len(packages) != count {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
	firstPackage := packages[0]
	if firstPackage.Name != name {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
	if firstPackage.Tool != tool {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
}

func assertPythonParseCommandCases(t *testing.T, monitor pythonParseMonitor, command string, cases []pythonParseCase) {
	t.Helper()

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assertPythonParseCommandCase(t, monitor, command, tt)
		})
	}
}

func assertPythonParseCommandCase(t *testing.T, monitor pythonParseMonitor, command string, tt pythonParseCase) {
	t.Helper()

	record, err := monitor.ParseCommand(command, tt.args)
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	if record.Metadata["action"] != tt.wantAction {
		t.Fatalf("action = %#v, want %s", record.Metadata["action"], tt.wantAction)
	}
	assertOptionalPythonPackageAffected(t, record, tt.wantPackage)
}

func assertPythonPackageAffected(t *testing.T, record *core.ExecutionRecord, expected string) {
	t.Helper()

	if !pythonPackageAffectedMatches(record, expected) {
		t.Fatalf("PackagesAffected = %#v, want %s", record.PackagesAffected, expected)
	}
}

func assertOptionalPythonPackageAffected(t *testing.T, record *core.ExecutionRecord, expected string) {
	t.Helper()

	if expected == "" {
		return
	}
	assertPythonPackageAffected(t, record, expected)
}

func TestParsePythonPackageLines(t *testing.T) {
	output := `Package Version
------- -------
requests 2.32.0
rich 13.7.0
`
	packages := parsePythonPackageLines(core.ToolPip, output)
	if len(packages) != 2 {
		t.Fatalf("Expected 2 packages, got %#v", packages)
	}
	assertPythonPackage(t, packages, 2, "requests", "2.32.0")
}

func TestPoetryInitializeAndLifecycleWithFakePoetry(t *testing.T) {
	prependFakeCommand(t, poetryCommandName, "#!/bin/sh\nexit 0\n")
	monitor := initializedPoetryMonitor(t)

	assertPoetryHasNoGlobalInventory(t, monitor)
	assertPoetryStart(t, monitor)
}

func initializedPoetryMonitor(t *testing.T) *PoetryMonitor {
	t.Helper()

	config := pythonProcessConfig()
	monitor := NewPoetryMonitor().(*PoetryMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return monitor
}

func assertPoetryHasNoGlobalInventory(t *testing.T, monitor *PoetryMonitor) {
	t.Helper()

	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if packages != nil {
		t.Fatalf("Expected no global poetry inventory, got %#v", packages)
	}
}

func assertPoetryStart(t *testing.T, monitor *PoetryMonitor) {
	t.Helper()

	if err := monitor.Start(context.Background(), make(chan *core.ExecutionRecord)); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
}

func TestPythonManagerStart(t *testing.T) {
	eventChan := make(chan *core.ExecutionRecord)
	if err := NewPipMonitor().(*PipMonitor).Start(context.Background(), eventChan); err != nil {
		t.Fatalf("Pip Start failed: %v", err)
	}
	if err := NewUVMonitor().(*UVMonitor).Start(context.Background(), eventChan); err != nil {
		t.Fatalf("UV Start failed: %v", err)
	}
}
