package monitors

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestNPMMonitor(t *testing.T) {
	monitor := NewNPMMonitor()

	if monitor.Name() != core.ToolNPM {
		t.Errorf("Expected monitor name '%s', got %s", core.ToolNPM, monitor.Name())
	}
}

func TestNPMMonitorStart(t *testing.T) {
	if err := NewNPMMonitor().(*NPMMonitor).Start(context.Background(), make(chan *core.ExecutionRecord)); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
}

type npmParseCommandCase struct {
	name     string
	args     []string
	packages []string
	metadata map[string]interface{}
}

var npmParseCommandCases = []npmParseCommandCase{
	{
		name:     "install single package",
		args:     []string{"install", "express"},
		packages: []string{"express"},
		metadata: map[string]interface{}{
			"subcommand": "install",
			"action":     "install",
			"global":     false,
		},
	},
	{
		name:     "install with i alias",
		args:     []string{"i", "lodash"},
		packages: []string{"lodash"},
		metadata: map[string]interface{}{
			"subcommand": "i",
			"action":     "install",
			"global":     false,
		},
	},
	{
		name:     "install global package",
		args:     []string{"install", "-g", "typescript"},
		packages: []string{"typescript"},
		metadata: map[string]interface{}{
			"subcommand": "install",
			"action":     "install",
			"global":     true,
		},
	},
	{
		name:     "install with --global flag",
		args:     []string{"install", "--global", "yarn"},
		packages: []string{"yarn"},
		metadata: map[string]interface{}{
			"subcommand": "install",
			"action":     "install",
			"global":     true,
		},
	},
	{
		name:     "install dev dependency",
		args:     []string{"install", "--save-dev", "jest"},
		packages: []string{"jest"},
		metadata: map[string]interface{}{
			"subcommand":     "install",
			"action":         "install",
			"dev_dependency": true,
		},
	},
	{
		name:     "install with -D flag",
		args:     []string{"install", "-D", "eslint"},
		packages: []string{"eslint"},
		metadata: map[string]interface{}{
			"subcommand":     "install",
			"action":         "install",
			"dev_dependency": true,
		},
	},
	{
		name:     "install optional dependency",
		args:     []string{"install", "--save-optional", "fsevents"},
		packages: []string{"fsevents"},
		metadata: map[string]interface{}{
			"subcommand":          "install",
			"action":              "install",
			"optional_dependency": true,
		},
	},
	{
		name:     "uninstall package",
		args:     []string{"uninstall", "moment"},
		packages: []string{"moment"},
		metadata: map[string]interface{}{
			"subcommand": "uninstall",
			"action":     "uninstall",
		},
	},
	{
		name:     "uninstall with rm alias",
		args:     []string{"rm", "lodash"},
		packages: []string{"lodash"},
		metadata: map[string]interface{}{
			"subcommand": "rm",
			"action":     "uninstall",
		},
	},
	{
		name:     "update all packages",
		args:     []string{"update"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "update",
			"update_all": true,
		},
	},
	{
		name:     "update specific package",
		args:     []string{"update", "react"},
		packages: []string{"react"},
		metadata: map[string]interface{}{
			"subcommand": "update",
		},
	},
	{
		name:     "list packages",
		args:     []string{"list"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "list",
			"action":     "list",
		},
	},
	{
		name:     "list with depth",
		args:     []string{"list", "--depth", "2"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "list",
			"action":     "list",
			"depth":      2,
		},
	},
	{
		name:     "search packages",
		args:     []string{"search", "react", "components"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand":  "search",
			"search_term": "react components",
		},
	},
	{
		name:     "run script",
		args:     []string{"run", "build"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "run",
			"script":     "build",
		},
	},
	{
		name:     "test command",
		args:     []string{"test"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "test",
			"action":     "test",
		},
	},
	{
		name:     "start command",
		args:     []string{"start"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "start",
			"action":     "start",
		},
	},
	{
		name:     "build command",
		args:     []string{"build"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "build",
			"action":     "build",
		},
	},
	{
		name:     "publish command",
		args:     []string{"publish"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "publish",
			"action":     "publish",
		},
	},
	{
		name:     "link package",
		args:     []string{"link", "my-package"},
		packages: []string{"my-package"},
		metadata: map[string]interface{}{
			"subcommand": "link",
			"action":     "link",
		},
	},
	{
		name:     "audit command",
		args:     []string{"audit"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "audit",
			"action":     "audit",
		},
	},
	{
		name:     "audit with fix",
		args:     []string{"audit", "--fix"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "audit",
			"action":     "audit",
			"fix":        true,
		},
	},
	{
		name:     "fund command",
		args:     []string{"fund"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "fund",
			"action":     "fund",
		},
	},
	{
		name:     "outdated command",
		args:     []string{"outdated"},
		packages: nil,
		metadata: map[string]interface{}{
			"subcommand": "outdated",
			"action":     "outdated",
		},
	},
}

func TestNPMParseCommand(t *testing.T) {
	monitor := NewNPMMonitor().(*NPMMonitor)

	for _, tt := range npmParseCommandCases {
		t.Run(tt.name, func(t *testing.T) {
			assertNPMParseCommand(t, monitor, tt)
		})
	}
}

func assertNPMParseCommand(t *testing.T, monitor *NPMMonitor, test npmParseCommandCase) {
	t.Helper()

	record, err := monitor.ParseCommand("npm", test.args)
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	assertNPMPackages(t, record.PackagesAffected, test.packages)
	assertNPMMetadata(t, record.Metadata, test.metadata)
}

func assertNPMPackages(t *testing.T, got []string, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("Expected %d packages, got %d: %v", len(want), len(got), got)
	}
	for i, pkg := range want {
		if i >= len(got) {
			continue
		}
		if got[i] != pkg {
			t.Errorf("Expected package %s, got %s", pkg, got[i])
		}
	}
}

func assertNPMMetadata(t *testing.T, got map[string]interface{}, want map[string]interface{}) {
	t.Helper()

	for key, expectedVal := range want {
		val, exists := got[key]
		metadataMatches := exists && val == expectedVal
		if !metadataMatches {
			t.Errorf("Expected metadata %s=%v, got %v", key, expectedVal, val)
		}
	}
}

func TestNPMParseCommandEmptyArgs(t *testing.T) {
	monitor := NewNPMMonitor().(*NPMMonitor)

	record, err := monitor.ParseCommand("npm", []string{})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}

	if record.Tool != core.ToolNPM {
		t.Errorf("Expected tool '%s', got %s", core.ToolNPM, record.Tool)
	}

	if len(record.PackagesAffected) != 0 {
		t.Errorf("Expected no packages, got %v", record.PackagesAffected)
	}
}

func TestNPMExtractPackagesFromArgs(t *testing.T) {
	monitor := NewNPMMonitor().(*NPMMonitor)

	for _, tt := range npmExtractPackageCases {
		t.Run(tt.name, func(t *testing.T) {
			assertNPMExtractPackages(t, monitor, tt)
		})
	}
}

type npmExtractPackageCase struct {
	name     string
	args     []string
	expected []string
}

var npmExtractPackageCases = []npmExtractPackageCase{
	{name: "single package", args: []string{"express"}, expected: []string{"express"}},
	{name: "multiple packages", args: []string{"express", "lodash", "moment"}, expected: []string{"express", "lodash", "moment"}},
	{name: "package with version", args: []string{"express@4.18.0"}, expected: []string{"express"}},
	{name: "scoped package", args: []string{"@types/node"}, expected: []string{"@types/node"}},
	{name: "skip flags", args: []string{"-g", "typescript", "--save-dev"}, expected: []string{"typescript"}},
	{name: "skip registry flag with value", args: []string{"--registry", "https://npm.example.com", "my-package"}, expected: []string{"my-package"}},
	{name: "empty args", args: []string{}, expected: nil},
}

func assertNPMExtractPackages(t *testing.T, monitor *NPMMonitor, test npmExtractPackageCase) {
	t.Helper()

	packages := monitor.extractPackagesFromNPMArgs(test.args)
	if len(packages) != len(test.expected) {
		t.Errorf("Expected %d packages, got %d: %v", len(test.expected), len(packages), packages)
		return
	}
	for i, pkg := range test.expected {
		if packages[i] != pkg {
			t.Errorf("Expected package %s at index %d, got %s", pkg, i, packages[i])
		}
	}
}

func TestNPMExtractDepth(t *testing.T) {
	monitor := NewNPMMonitor().(*NPMMonitor)

	for _, tt := range npmDepthCases {
		t.Run(tt.name, func(t *testing.T) {
			depth := monitor.extractDepth(tt.args)
			if depth != tt.expected {
				t.Errorf("Expected depth %d, got %d", tt.expected, depth)
			}
		})
	}
}

type npmDepthCase struct {
	name     string
	args     []string
	expected int
}

var npmDepthCases = []npmDepthCase{
	{name: "no depth flag", args: []string{"list"}, expected: -1},
	{name: "depth 0", args: []string{"list", "--depth", "0"}, expected: 0},
	{name: "depth 5", args: []string{"--depth", "5", "list"}, expected: 5},
	{name: "invalid depth", args: []string{"--depth", "abc"}, expected: -1},
	{name: "depth flag at end without value", args: []string{"list", "--depth"}, expected: -1},
}

func TestNPMInitializeAndGetGlobalPackagesWithFakeNPM(t *testing.T) {
	prependFakeCommand(t, npmCommandName, fakeNPMGlobalPackagesScript)
	prefix := t.TempDir()
	t.Setenv("FAKE_NPM_PREFIX", prefix)
	monitor := initializedNPMMonitor(t)
	assertNPMGlobalPath(t, monitor, prefix)
	packages := npmGlobalPackages(t, monitor)
	assertNPMGlobalPackages(t, packages)
}

const fakeNPMGlobalPackagesScript = `#!/bin/sh
if [ "$1" = "config" ] && [ "$2" = "get" ] && [ "$3" = "prefix" ]; then
  printf '%s\n' "$FAKE_NPM_PREFIX"
  exit 0
fi
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ] && [ "$4" = "--json" ]; then
  printf '%s\n' '{"dependencies":{"eslint":{"version":"9.0.0","dependencies":{"@eslint/js":{}}},"typescript":{"version":"5.5.0"}}}'
  exit 0
fi
exit 2
`

func initializedNPMMonitor(t *testing.T) *NPMMonitor {
	t.Helper()
	config := core.DefaultConfig()
	config.Monitoring.Process.ShouldAutoInstallWrappers = false
	config.Tools.NPM.ShouldTrackGlobalOnly = true
	monitor := NewNPMMonitor().(*NPMMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return monitor
}

func assertNPMGlobalPath(t *testing.T, monitor *NPMMonitor, prefix string) {
	t.Helper()

	if monitor.globalPath != filepath.Join(prefix, "lib", "node_modules") {
		t.Fatalf("globalPath = %s, want prefix node_modules path", monitor.globalPath)
	}
}

func npmGlobalPackages(t *testing.T, monitor *NPMMonitor) []*core.PackageInfo {
	t.Helper()

	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	return packages
}

func assertNPMGlobalPackages(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	byName := make(map[string]*core.PackageInfo)
	for _, pkg := range packages {
		byName[pkg.Name] = pkg
	}
	assertNPMESLintPackage(t, byName["eslint"])
	assertNPMTypeScriptPackage(t, byName["typescript"])
}

func assertNPMESLintPackage(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	hasESLint := pkg != nil
	hasESLintVersion := hasESLint && pkg.Version == "9.0.0"
	if !hasESLintVersion {
		t.Fatalf("Unexpected eslint package: %#v", pkg)
	}
	hasESLintDependencyCount := len(pkg.Dependencies) == 1
	hasESLintDependency := hasESLintDependencyCount && pkg.Dependencies[0] == "@eslint/js"
	if !hasESLintDependency {
		t.Fatalf("Unexpected eslint dependencies: %#v", pkg.Dependencies)
	}
}

func assertNPMTypeScriptPackage(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	hasTypeScript := pkg != nil
	hasTypeScriptVersion := hasTypeScript && pkg.Version == "5.5.0"
	if !hasTypeScriptVersion {
		t.Fatalf("Unexpected typescript package: %#v", pkg)
	}
}

func TestNPMGlobalPackagesFallbackWithFakeNPM(t *testing.T) {
	prependFakeCommand(t, npmCommandName, fakeNPMFallbackPackagesScript)
	t.Setenv("FAKE_NPM_PREFIX", t.TempDir())
	config := core.DefaultConfig()
	config.Monitoring.Process.ShouldAutoInstallWrappers = false
	monitor := initializedNPMMonitorWithConfig(t, config)
	packages := npmFallbackPackages(t, monitor)
	assertNPMFallbackPackages(t, packages)
}

const fakeNPMFallbackPackagesScript = `#!/bin/sh
if [ "$1" = "config" ] && [ "$2" = "get" ] && [ "$3" = "prefix" ]; then
  printf '%s\n' "$FAKE_NPM_PREFIX"
  exit 0
fi
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ] && [ "$4" = "--json" ]; then
  printf 'not json\n'
  exit 0
fi
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ]; then
  printf '/tmp/prefix/lib\n├── eslint@9.0.0\n└── typescript@5.5.0\n'
  exit 0
fi
exit 2
`

func initializedNPMMonitorWithConfig(t *testing.T, config *core.Config) *NPMMonitor {
	t.Helper()

	monitor := NewNPMMonitor().(*NPMMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return monitor
}

func npmFallbackPackages(t *testing.T, monitor *NPMMonitor) []*core.PackageInfo {
	t.Helper()
	packages, err := monitor.globalPackages()
	if err != nil {
		t.Fatalf("globalPackages failed: %v", err)
	}
	return packages
}

func assertNPMFallbackPackages(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	hasPackageCount := len(packages) == 2
	hasPackageName := hasPackageCount && packages[0].Name == "eslint"
	hasPackageVersion := hasPackageCount && packages[0].Version == "9.0.0"
	hasExpectedPackage := hasPackageName && hasPackageVersion
	if !hasExpectedPackage {
		t.Fatalf("Unexpected fallback packages: %#v", packages)
	}
}
