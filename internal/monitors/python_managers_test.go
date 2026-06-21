package monitors

import (
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
