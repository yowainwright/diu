package monitors

import (
	"context"
	"slices"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestPNPMParseCommand(t *testing.T) {
	monitor := NewPNPMMonitor().(*PNPMMonitor)
	record, err := monitor.ParseCommand("pnpm", []string{"add", "-g", "typescript@5.5.0", "@scope/tool@1.2.3"})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	assertPNPMParseRecord(t, record)
}

func assertPNPMParseRecord(t *testing.T, record *core.ExecutionRecord) {
	t.Helper()

	if record.Tool != core.ToolPNPM {
		t.Fatalf("Tool = %s, want %s", record.Tool, core.ToolPNPM)
	}
	wantPackages := []string{"typescript", "@scope/tool"}
	if !slices.Equal(record.PackagesAffected, wantPackages) {
		t.Fatalf("PackagesAffected = %#v, want %#v", record.PackagesAffected, wantPackages)
	}
	hasAction := record.Metadata["action"] == "install"
	hasGlobal := record.Metadata["global"].(bool)
	hasMetadata := hasAction && hasGlobal
	if !hasMetadata {
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
	hasOnePackage := len(record.PackagesAffected) == 1
	hasPackage := hasOnePackage && record.PackagesAffected[0] == "eslint"
	if !hasPackage {
		t.Fatalf("PackagesAffected = %#v, want eslint", record.PackagesAffected)
	}
	if record.Metadata["action"] != "exec" {
		t.Fatalf("Unexpected metadata: %#v", record.Metadata)
	}
}

func TestJavaScriptManagerParseCommandVariants(t *testing.T) {
	for _, tt := range javaScriptManagerParseCases {
		t.Run(tt.name, func(t *testing.T) {
			assertJavaScriptManagerParse(t, tt)
		})
	}
}

type javaScriptManagerParseCase struct {
	name        string
	args        []string
	wantAction  string
	wantPackage string
	wantMetaKey string
	wantMeta    interface{}
}

var javaScriptManagerParseCases = []javaScriptManagerParseCase{
	{
		name:        "remove",
		args:        []string{"remove", "--filter", "workspace", "tsx@4.19.0"},
		wantAction:  "uninstall",
		wantPackage: "tsx",
	},
	{name: "update all", args: []string{"update"}, wantMetaKey: "update_all", wantMeta: true},
	{name: "run script", args: []string{"run", "build"}, wantAction: "run", wantMetaKey: "script", wantMeta: "build"},
	{name: "list", args: []string{"list", "--depth=0"}, wantAction: "list"},
}

func assertJavaScriptManagerParse(t *testing.T, test javaScriptManagerParseCase) {
	t.Helper()

	record := parseJavaScriptManagerCommand(core.ToolPNPM, pnpmCommandName, test.args)
	assertJavaScriptAction(t, record, test.wantAction)
	assertJavaScriptPackage(t, record, test.wantPackage)
	assertJavaScriptMetadata(t, record, test.wantMetaKey, test.wantMeta)
}

func assertJavaScriptAction(t *testing.T, record *core.ExecutionRecord, want string) {
	t.Helper()

	actionOK := want == "" || record.Metadata["action"] == want
	if !actionOK {
		t.Fatalf("action = %#v, want %s", record.Metadata["action"], want)
	}
}

func assertJavaScriptPackage(t *testing.T, record *core.ExecutionRecord, want string) {
	t.Helper()

	packageOK := want == "" || packageAffectedMatches(record, want)
	if !packageOK {
		t.Fatalf("PackagesAffected = %#v, want %s", record.PackagesAffected, want)
	}
}

func assertJavaScriptMetadata(t *testing.T, record *core.ExecutionRecord, key string, want interface{}) {
	t.Helper()

	metaOK := key == "" || record.Metadata[key] == want
	if !metaOK {
		t.Fatalf("%s = %#v, want %#v", key, record.Metadata[key], want)
	}
}

func packageAffectedMatches(record *core.ExecutionRecord, expected string) bool {
	hasOnePackage := len(record.PackagesAffected) == 1
	hasExpectedPackage := hasOnePackage && record.PackagesAffected[0] == expected
	return hasExpectedPackage
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
	assertPackageNameVersion(t, packages[0], "@scope/tool", "1.2.3")
	assertPackageNameVersion(t, packages[1], "tsx", "4.19.0")
	assertPackageNameVersion(t, packages[2], "prettier", "3.3.0")
}

func assertPackageNameVersion(t *testing.T, pkg *core.PackageInfo, name, version string) {
	t.Helper()

	if pkg.Name != name {
		t.Fatalf("Unexpected package name: %#v", pkg)
	}
	if pkg.Version != version {
		t.Fatalf("Unexpected package version: %#v", pkg)
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
		nameMatches := name == want[0]
		versionMatches := version == want[1]
		resultMatches := nameMatches && versionMatches
		if !resultMatches {
			t.Fatalf("splitPackageVersion(%q) = %q, %q; want %#v", input, name, version, want)
		}
	}
}

func TestPNPMGetInstalledPackagesWithFakePNPM(t *testing.T) {
	prependFakeCommand(t, pnpmCommandName, fakePNPMListScript)
	monitor := initializedPNPMMonitor(t)
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	assertPNPMInstalledPackages(t, packages)
}

const fakePNPMListScript = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ] && [ "$4" = "--json" ]; then
  printf '%s\n' '[{"dependencies":{"tsx":{"version":"4.19.0","path":"/pnpm/tsx"},"@scope/tool":{"version":"1.2.3"}}}]'
  exit 0
fi
exit 2
`

func initializedPNPMMonitor(t *testing.T) *PNPMMonitor {
	t.Helper()

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	monitor := NewPNPMMonitor().(*PNPMMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return monitor
}

func assertPNPMInstalledPackages(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	if len(packages) != 2 {
		t.Fatalf("Expected 2 packages, got %#v", packages)
	}
	assertPNPMFirstPackage(t, packages[0])
	assertPNPMSecondPackage(t, packages[1])
}

func assertPNPMFirstPackage(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	firstNameMatches := pkg.Name == "@scope/tool"
	firstVersionMatches := pkg.Version == "1.2.3"
	firstPackageMatches := firstNameMatches && firstVersionMatches
	if !firstPackageMatches {
		t.Fatalf("Unexpected first package: %#v", pkg)
	}
}

func assertPNPMSecondPackage(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	secondNameMatches := pkg.Name == "tsx"
	secondVersionMatches := pkg.Version == "4.19.0"
	secondPathMatches := pkg.Path == "/pnpm/tsx"
	secondPackageMatches := secondNameMatches && secondVersionMatches && secondPathMatches
	if !secondPackageMatches {
		t.Fatalf("Unexpected second package: %#v", pkg)
	}
}

func TestBunGetInstalledPackagesWithFakeBun(t *testing.T) {
	prependFakeCommand(t, bunCommandName, fakeBunListScript)
	monitor := initializedBunMonitor(t)
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	assertBunInstalledPackages(t, packages)
}

const fakeBunListScript = `#!/bin/sh
if [ "$1" = "pm" ] && [ "$2" = "ls" ] && [ "$3" = "-g" ] && [ "$4" = "--json" ]; then
  printf '%s\n' '{"dependencies":{"prettier":{"version":"3.3.0"}}}'
  exit 0
fi
exit 2
`

func initializedBunMonitor(t *testing.T) *BunMonitor {
	t.Helper()

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	monitor := NewBunMonitor().(*BunMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return monitor
}

func assertBunInstalledPackages(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	hasOnePackage := len(packages) == 1
	hasPackageName := hasOnePackage && packages[0].Name == "prettier"
	hasPackageVersion := hasOnePackage && packages[0].Version == "3.3.0"
	hasExpectedPackage := hasPackageName && hasPackageVersion
	if !hasExpectedPackage {
		t.Fatalf("Unexpected packages: %#v", packages)
	}
}

func TestPNPMGetInstalledPackagesFallsBackToText(t *testing.T) {
	prependFakeCommand(t, pnpmCommandName, fakePNPMFallbackListScript)
	monitor := initializedPNPMMonitor(t)
	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	assertPNPMFallbackPackages(t, packages)
}

const fakePNPMFallbackListScript = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ] && [ "$4" = "--json" ]; then
  printf 'not json\n'
  exit 0
fi
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ]; then
  printf '├── tsx 4.19.0\n'
  exit 0
fi
exit 2
`

func assertPNPMFallbackPackages(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	hasOnePackage := len(packages) == 1
	hasPackageName := hasOnePackage && packages[0].Name == "tsx"
	hasPackageVersion := hasOnePackage && packages[0].Version == "4.19.0"
	hasExpectedPackage := hasPackageName && hasPackageVersion
	if !hasExpectedPackage {
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
