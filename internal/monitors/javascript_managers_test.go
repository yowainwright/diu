package monitors

import (
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestPNPMParseCommand(t *testing.T) {
	monitor := NewPNPMMonitor().(*PNPMMonitor)

	record, err := monitor.ParseCommand("pnpm", []string{"add", "-g", "typescript@5.5.0", "@scope/tool@1.2.3"})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	if record.Tool != core.ToolPNPM {
		t.Fatalf("Tool = %s, want %s", record.Tool, core.ToolPNPM)
	}
	if got, want := record.PackagesAffected, []string{"typescript", "@scope/tool"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PackagesAffected = %#v, want %#v", got, want)
	}
	if record.Metadata["action"] != "install" || record.Metadata["global"] != true {
		t.Fatalf("Unexpected metadata: %#v", record.Metadata)
	}
}

func TestBunParseCommand(t *testing.T) {
	monitor := NewBunMonitor().(*BunMonitor)

	record, err := monitor.ParseCommand("bun", []string{"x", "eslint@9.0.0"})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	if record.Tool != core.ToolBun {
		t.Fatalf("Tool = %s, want %s", record.Tool, core.ToolBun)
	}
	if len(record.PackagesAffected) != 1 || record.PackagesAffected[0] != "eslint" {
		t.Fatalf("PackagesAffected = %#v, want eslint", record.PackagesAffected)
	}
	if record.Metadata["action"] != "exec" {
		t.Fatalf("Unexpected metadata: %#v", record.Metadata)
	}
}

func TestPNPMGetInstalledPackagesWithFakePNPM(t *testing.T) {
	prependFakeCommand(t, pnpmCommandName, `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ] && [ "$4" = "--json" ]; then
  printf '%s\n' '[{"dependencies":{"tsx":{"version":"4.19.0","path":"/pnpm/tsx"},"@scope/tool":{"version":"1.2.3"}}}]'
  exit 0
fi
exit 2
`)

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewPNPMMonitor().(*PNPMMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("Expected 2 packages, got %#v", packages)
	}
	if packages[0].Name != "@scope/tool" || packages[0].Version != "1.2.3" {
		t.Fatalf("Unexpected first package: %#v", packages[0])
	}
	if packages[1].Name != "tsx" || packages[1].Version != "4.19.0" || packages[1].Path != "/pnpm/tsx" {
		t.Fatalf("Unexpected second package: %#v", packages[1])
	}
}

func TestBunGetInstalledPackagesWithFakeBun(t *testing.T) {
	prependFakeCommand(t, bunCommandName, `#!/bin/sh
if [ "$1" = "pm" ] && [ "$2" = "ls" ] && [ "$3" = "-g" ] && [ "$4" = "--json" ]; then
  printf '%s\n' '{"dependencies":{"prettier":{"version":"3.3.0"}}}'
  exit 0
fi
exit 2
`)

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewBunMonitor().(*BunMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if len(packages) != 1 || packages[0].Name != "prettier" || packages[0].Version != "3.3.0" {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
}
