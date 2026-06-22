package monitors

import (
	"context"
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

func TestJavaScriptManagerParseCommandVariants(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantAction  string
		wantPackage string
		wantMetaKey string
		wantMeta    interface{}
	}{
		{
			name:        "remove",
			args:        []string{"remove", "--filter", "workspace", "tsx@4.19.0"},
			wantAction:  "uninstall",
			wantPackage: "tsx",
		},
		{
			name:        "update all",
			args:        []string{"update"},
			wantMetaKey: "update_all",
			wantMeta:    true,
		},
		{
			name:        "run script",
			args:        []string{"run", "build"},
			wantAction:  "run",
			wantMetaKey: "script",
			wantMeta:    "build",
		},
		{
			name:       "list",
			args:       []string{"list", "--depth=0"},
			wantAction: "list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := parseJavaScriptManagerCommand(core.ToolPNPM, pnpmCommandName, tt.args)
			if tt.wantAction != "" && record.Metadata["action"] != tt.wantAction {
				t.Fatalf("action = %#v, want %s", record.Metadata["action"], tt.wantAction)
			}
			if tt.wantPackage != "" && (len(record.PackagesAffected) != 1 || record.PackagesAffected[0] != tt.wantPackage) {
				t.Fatalf("PackagesAffected = %#v, want %s", record.PackagesAffected, tt.wantPackage)
			}
			if tt.wantMetaKey != "" && record.Metadata[tt.wantMetaKey] != tt.wantMeta {
				t.Fatalf("%s = %#v, want %#v", tt.wantMetaKey, record.Metadata[tt.wantMetaKey], tt.wantMeta)
			}
		})
	}
}

func TestParseSimplePackageLines(t *testing.T) {
	output := `
/Users/test/.local/share/pnpm/global/5
├── @scope/tool@1.2.3
└── tsx 4.19.0
- prettier@3.3.0
`

	packages := parseSimplePackageLines(core.ToolPNPM, output)
	if len(packages) != 3 {
		t.Fatalf("Expected 3 packages, got %#v", packages)
	}
	if packages[0].Name != "@scope/tool" || packages[0].Version != "1.2.3" {
		t.Fatalf("Unexpected scoped package: %#v", packages[0])
	}
	if packages[1].Name != "tsx" || packages[1].Version != "4.19.0" {
		t.Fatalf("Unexpected version column package: %#v", packages[1])
	}
	if packages[2].Name != "prettier" || packages[2].Version != "3.3.0" {
		t.Fatalf("Unexpected dash package: %#v", packages[2])
	}
}

func TestSplitPackageVersion(t *testing.T) {
	tests := map[string][2]string{
		"":                 {"", ""},
		"typescript":       {"typescript", ""},
		"typescript@5.5.0": {"typescript", "5.5.0"},
		"@scope/tool":      {"@scope/tool", ""},
		"@scope/tool@1.2":  {"@scope/tool", "1.2"},
	}

	for input, want := range tests {
		name, version := splitPackageVersion(input)
		if name != want[0] || version != want[1] {
			t.Fatalf("splitPackageVersion(%q) = %q, %q; want %#v", input, name, version, want)
		}
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

func TestPNPMGetInstalledPackagesFallsBackToText(t *testing.T) {
	prependFakeCommand(t, pnpmCommandName, `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ] && [ "$4" = "--json" ]; then
  printf 'not json\n'
  exit 0
fi
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ]; then
  printf '├── tsx 4.19.0\n'
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
	if len(packages) != 1 || packages[0].Name != "tsx" || packages[0].Version != "4.19.0" {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
}

func TestBunGetInstalledPackagesReturnsTextError(t *testing.T) {
	prependFakeCommand(t, bunCommandName, `#!/bin/sh
exit 2
`)

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewBunMonitor().(*BunMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if _, err := monitor.GetInstalledPackages(); err == nil {
		t.Fatal("Expected error for failed bun list")
	}
}

func TestJavaScriptManagerStart(t *testing.T) {
	eventChan := make(chan *core.ExecutionRecord)
	if err := NewPNPMMonitor().(*PNPMMonitor).Start(context.Background(), eventChan); err != nil {
		t.Fatalf("PNPM Start failed: %v", err)
	}
	if err := NewBunMonitor().(*BunMonitor).Start(context.Background(), eventChan); err != nil {
		t.Fatalf("Bun Start failed: %v", err)
	}
}
