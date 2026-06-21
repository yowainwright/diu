package monitors

import (
	"context"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestPipParseCommand(t *testing.T) {
	monitor := NewPipMonitor().(*PipMonitor)

	record, err := monitor.ParseCommand("pip", []string{"install", "requests==2.32.0", "rich[all]>=13", "-r", "requirements.txt"})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	if record.Tool != core.ToolPip {
		t.Fatalf("Tool = %s, want %s", record.Tool, core.ToolPip)
	}
	if got, want := record.PackagesAffected, []string{"requests", "rich"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PackagesAffected = %#v, want %#v", got, want)
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
			if tt.wantPackage != "" && (len(record.PackagesAffected) != 1 || record.PackagesAffected[0] != tt.wantPackage) {
				t.Fatalf("PackagesAffected = %#v, want %s", record.PackagesAffected, tt.wantPackage)
			}
		})
	}
}

func TestPipGetInstalledPackagesWithFakePip(t *testing.T) {
	prependFakeCommand(t, pip3CommandName, `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then
  printf '%s\n' '[{"name":"requests","version":"2.32.0"},{"name":"rich","version":"13.7.0"}]'
  exit 0
fi
exit 2
`)

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewPipMonitor().(*PipMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if monitor.commandName != pip3CommandName {
		t.Fatalf("commandName = %s, want %s", monitor.commandName, pip3CommandName)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if len(packages) != 2 || packages[0].Name != "requests" || packages[0].Tool != core.ToolPip {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
}

func TestPipGetInstalledPackagesFallsBackToText(t *testing.T) {
	prependFakeCommand(t, pip3CommandName, `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then
  printf 'not json\n'
  exit 0
fi
if [ "$1" = "list" ]; then
  printf 'Package Version\n------- -------\nrequests 2.32.0\nrich 13.7.0\n'
  exit 0
fi
exit 2
`)

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewPipMonitor().(*PipMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if len(packages) != 2 || packages[0].Name != "requests" || packages[0].Version != "2.32.0" {
		t.Fatalf("Unexpected packages: %#v", packages)
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
	if len(record.PackagesAffected) != 1 || record.PackagesAffected[0] != "ruff" {
		t.Fatalf("PackagesAffected = %#v, want ruff", record.PackagesAffected)
	}
	if record.Metadata["action"] != "tool_install" {
		t.Fatalf("Unexpected metadata: %#v", record.Metadata)
	}
}

func TestUVParseCommandVariants(t *testing.T) {
	monitor := NewUVMonitor().(*UVMonitor)
	tests := []struct {
		name        string
		args        []string
		wantAction  string
		wantPackage string
	}{
		{name: "pip install", args: []string{"pip", "install", "httpx>=0.27"}, wantAction: "pip_install", wantPackage: "httpx"},
		{name: "pip uninstall", args: []string{"pip", "uninstall", "httpx"}, wantAction: "pip_uninstall", wantPackage: "httpx"},
		{name: "pip list", args: []string{"pip", "list"}, wantAction: "pip_list"},
		{name: "pip freeze", args: []string{"pip", "freeze"}, wantAction: "pip_freeze"},
		{name: "tool uninstall", args: []string{"tool", "uninstall", "ruff"}, wantAction: "tool_uninstall", wantPackage: "ruff"},
		{name: "tool run", args: []string{"tool", "run", "ruff"}, wantAction: "tool_run", wantPackage: "ruff"},
		{name: "tool list", args: []string{"tool", "list"}, wantAction: "tool_list"},
		{name: "add", args: []string{"add", "pytest"}, wantAction: "add", wantPackage: "pytest"},
		{name: "remove", args: []string{"remove", "pytest"}, wantAction: "remove", wantPackage: "pytest"},
		{name: "sync", args: []string{"sync"}, wantAction: "sync"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := monitor.ParseCommand("uv", tt.args)
			if err != nil {
				t.Fatalf("ParseCommand failed: %v", err)
			}
			if record.Metadata["action"] != tt.wantAction {
				t.Fatalf("action = %#v, want %s", record.Metadata["action"], tt.wantAction)
			}
			if tt.wantPackage != "" && (len(record.PackagesAffected) != 1 || record.PackagesAffected[0] != tt.wantPackage) {
				t.Fatalf("PackagesAffected = %#v, want %s", record.PackagesAffected, tt.wantPackage)
			}
		})
	}
}

func TestUVGetInstalledPackagesWithFakeUV(t *testing.T) {
	prependFakeCommand(t, uvCommandName, `#!/bin/sh
if [ "$1" = "tool" ] && [ "$2" = "list" ]; then
  printf 'ruff v0.5.0\n- ruff\nblack 24.4.2\n'
  exit 0
fi
exit 2
`)

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewUVMonitor().(*UVMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if len(packages) != 2 || packages[0].Name != "ruff" || packages[0].Version != "v0.5.0" {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
}

func TestUVGetInstalledPackagesFallsBackToPipList(t *testing.T) {
	prependFakeCommand(t, uvCommandName, `#!/bin/sh
if [ "$1" = "tool" ] && [ "$2" = "list" ]; then
  exit 2
fi
if [ "$1" = "pip" ] && [ "$2" = "list" ] && [ "$3" = "--format=json" ]; then
  printf '[{"name":"httpx","version":"0.27.0"}]\n'
  exit 0
fi
exit 2
`)

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewUVMonitor().(*UVMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if len(packages) != 1 || packages[0].Name != "httpx" || packages[0].Version != "0.27.0" {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
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
	if len(record.PackagesAffected) != 1 || record.PackagesAffected[0] != "poetry-plugin-export" {
		t.Fatalf("PackagesAffected = %#v, want poetry-plugin-export", record.PackagesAffected)
	}
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
			if tt.wantPackage != "" && (len(record.PackagesAffected) != 1 || record.PackagesAffected[0] != tt.wantPackage) {
				t.Fatalf("PackagesAffected = %#v, want %s", record.PackagesAffected, tt.wantPackage)
			}
		})
	}
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
	if packages[0].Name != "requests" || packages[0].Version != "2.32.0" {
		t.Fatalf("Unexpected first package: %#v", packages[0])
	}
}

func TestPoetryInitializeAndLifecycleWithFakePoetry(t *testing.T) {
	prependFakeCommand(t, poetryCommandName, "#!/bin/sh\nexit 0\n")

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewPoetryMonitor().(*PoetryMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if packages != nil {
		t.Fatalf("Expected no global poetry inventory, got %#v", packages)
	}
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
