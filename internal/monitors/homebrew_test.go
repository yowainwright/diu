package monitors

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestHomebrewMonitor(t *testing.T) {
	monitor := NewHomebrewMonitor()

	if monitor.Name() != "homebrew" {
		t.Errorf("Expected monitor name 'homebrew', got %s", monitor.Name())
	}
}

func TestHomebrewMonitorStart(t *testing.T) {
	if err := NewHomebrewMonitor().(*HomebrewMonitor).Start(context.Background(), make(chan *core.ExecutionRecord)); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
}

var homebrewParseCommandCases = []struct {
	name     string
	args     []string
	expected struct {
		packages []string
		metadata map[string]interface{}
	}
}{
	{
		name: "install command",
		args: []string{"install", "wget"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: []string{"wget"},
			metadata: map[string]interface{}{
				"subcommand": "install",
				"type":       "formula",
			},
		},
	},
	{
		name: "install with cask",
		args: []string{"install", "--cask", "firefox"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: []string{"firefox"},
			metadata: map[string]interface{}{
				"subcommand": "install",
				"type":       "cask",
			},
		},
	},
	{
		name: "uninstall command",
		args: []string{"uninstall", "wget"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: []string{"wget"},
			metadata: map[string]interface{}{
				"subcommand": "uninstall",
				"action":     "uninstall",
			},
		},
	},
	{
		name: "uninstall cask",
		args: []string{"uninstall", "--cask", "firefox"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: []string{"firefox"},
			metadata: map[string]interface{}{
				"subcommand": "uninstall",
				"action":     "uninstall",
				"type":       "cask",
			},
		},
	},
	{
		name: "upgrade all",
		args: []string{"upgrade"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand":  "upgrade",
				"upgrade_all": true,
			},
		},
	},
	{
		name: "upgrade package",
		args: []string{"upgrade", "wget"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: []string{"wget"},
			metadata: map[string]interface{}{
				"subcommand": "upgrade",
			},
		},
	},
	{
		name: "reinstall package",
		args: []string{"reinstall", "wget"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: []string{"wget"},
			metadata: map[string]interface{}{
				"subcommand": "reinstall",
				"action":     "reinstall",
			},
		},
	},
	{
		name: "tap command",
		args: []string{"tap", "owner/tap"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			metadata: map[string]interface{}{
				"subcommand": "tap",
				"tap":        "owner/tap",
			},
		},
	},
	{
		name: "untap command",
		args: []string{"untap", "owner/tap"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			metadata: map[string]interface{}{
				"subcommand": "untap",
				"untap":      "owner/tap",
			},
		},
	},
	{
		name: "list command",
		args: []string{"list"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "list",
				"action":     "list",
			},
		},
	},
	{
		name: "search command",
		args: []string{"search", "postgres", "client"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			metadata: map[string]interface{}{
				"subcommand":  "search",
				"search_term": "postgres client",
			},
		},
	},
	{
		name: "info command",
		args: []string{"info", "wget"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: []string{"wget"},
			metadata: map[string]interface{}{
				"subcommand": "info",
			},
		},
	},
	{
		name: "services command",
		args: []string{"services", "restart", "postgresql"},
		expected: struct {
			packages []string
			metadata map[string]interface{}
		}{
			packages: []string{"postgresql"},
			metadata: map[string]interface{}{
				"subcommand":     "services",
				"service_action": "restart",
			},
		},
	},
}

func TestHomebrewParseCommand(t *testing.T) {
	monitor := NewHomebrewMonitor().(*HomebrewMonitor)

	for _, tt := range homebrewParseCommandCases {
		t.Run(tt.name, func(t *testing.T) {
			assertHomebrewParseCommand(t, monitor, tt.args, tt.expected)
		})
	}
}

func assertHomebrewParseCommand(
	t *testing.T,
	monitor *HomebrewMonitor,
	args []string,
	expected struct {
		packages []string
		metadata map[string]interface{}
	},
) {
	t.Helper()

	record, err := monitor.ParseCommand("brew", args)
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	assertHomebrewPackages(t, record.PackagesAffected, expected.packages)
	assertHomebrewMetadata(t, record.Metadata, expected.metadata)
}

func assertHomebrewPackages(t *testing.T, got []string, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("Expected %d packages, got %d", len(want), len(got))
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

func assertHomebrewMetadata(t *testing.T, got map[string]interface{}, want map[string]interface{}) {
	t.Helper()

	for key, expectedVal := range want {
		val, exists := got[key]
		metadataMatches := exists && val == expectedVal
		if !metadataMatches {
			t.Errorf("Expected metadata %s=%v, got %v", key, expectedVal, val)
		}
	}
}

func TestFormulaPackagesUseNewestInstalledVersion(t *testing.T) {
	info := homebrewInstalledInfo{
		Formulae: []homebrewFormula{{
			Name: "node",
			Installed: []homebrewInstallation{
				{Version: "20.0.0", Time: 200},
				{Version: "18.0.0", Time: 100},
			},
		}},
	}
	packages := info.formulaPackages()
	hasOnePackage := len(packages) == 1
	hasVersion := hasOnePackage && packages[0].Version == "20.0.0"
	if !hasVersion {
		t.Fatalf("formula packages = %#v", packages)
	}
}

func TestHomebrewInitialize(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	monitor := NewHomebrewMonitor()

	err := monitor.Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize monitor: %v", err)
	}
}

func TestHomebrewDetectAndListWithFakeBrew(t *testing.T) {
	prependFakeCommand(t, homebrewCommandName, fakeHomebrewInfoScript)
	cellar := prepareFakeHomebrewPaths(t)
	config := testHomebrewConfig()
	monitor := initializedHomebrewMonitor(t, config)

	assertHomebrewPaths(t, monitor, cellar)
	packages := installedHomebrewPackages(t, monitor)
	assertDetectedHomebrewPackages(t, packages)
}

const fakeHomebrewInfoScript = `#!/bin/sh
if [ "$1" = "--cellar" ]; then
  printf '%s\n' "$FAKE_BREW_CELLAR"
  exit 0
fi
if [ "$1" = "--prefix" ]; then
  printf '%s\n' "$FAKE_BREW_PREFIX"
  exit 0
fi
if [ "$1" = "info" ] && [ "$2" = "--json=v2" ] && [ "$3" = "--installed" ]; then
  printf '%s\n' '{"formulae":[{"name":"jq","dependencies":["oniguruma"],"installed":[{"version":"1.7","time":1704164645}]}],"casks":[{"token":"firefox","version":"128.0","installed_time":1704164645}]}'
  exit 0
fi
exit 2
`

func prepareFakeHomebrewPaths(t *testing.T) string {
	t.Helper()

	tempDir := t.TempDir()
	cellar := filepath.Join(tempDir, "Cellar")
	prefix := filepath.Join(tempDir, "prefix")
	caskroom := filepath.Join(prefix, "Caskroom")
	for _, dir := range []string{cellar, caskroom} {
		if err := os.MkdirAll(dir, core.OwnerDirectoryMode); err != nil {
			t.Fatalf("Failed to create %s: %v", dir, err)
		}
	}
	t.Setenv("FAKE_BREW_CELLAR", cellar)
	t.Setenv("FAKE_BREW_PREFIX", prefix)
	return cellar
}

func testHomebrewConfig() *core.Config {
	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Homebrew.CellarPaths = nil
	config.Tools.Homebrew.TrackCasks = true
	return config
}

func initializedHomebrewMonitor(t *testing.T, config *core.Config) *HomebrewMonitor {
	t.Helper()

	monitor := NewHomebrewMonitor().(*HomebrewMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	return monitor
}

func assertHomebrewPaths(t *testing.T, monitor *HomebrewMonitor, cellar string) {
	t.Helper()

	if !contains(monitor.cellarPaths, cellar) {
		t.Fatalf("cellarPaths = %#v, want %s", monitor.cellarPaths, cellar)
	}
	if monitor.caskroom == "" {
		t.Fatalf("caskroom was not detected")
	}
}

func installedHomebrewPackages(t *testing.T, monitor *HomebrewMonitor) []*core.PackageInfo {
	t.Helper()

	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	return packages
}

func assertDetectedHomebrewPackages(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	if len(packages) != 2 {
		t.Fatalf("Expected formula and cask packages, got %#v", packages)
	}
	byName := packagesByName(packages)
	assertDetectedJQPackage(t, byName["jq"])
	assertDetectedFirefoxPackage(t, byName["firefox"])
}

func packagesByName(packages []*core.PackageInfo) map[string]*core.PackageInfo {
	byName := make(map[string]*core.PackageInfo)
	for _, pkg := range packages {
		byName[pkg.Name] = pkg
	}
	return byName
}

func assertDetectedJQPackage(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	hasJQVersion := pkg.Version == "1.7"
	hasJQTool := pkg.Tool == core.ToolHomebrew
	hasJQPackage := hasJQVersion && hasJQTool
	if !hasJQPackage {
		t.Fatalf("Unexpected jq package: %#v", pkg)
	}
	if len(pkg.Dependencies) != 0 {
		t.Fatalf("Unexpected jq dependencies: %#v", pkg.Dependencies)
	}
}

func assertDetectedFirefoxPackage(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	if pkg.Tool != homebrewCaskTool {
		t.Fatalf("Unexpected firefox package: %#v", pkg)
	}
}

func TestHomebrewUsesFastInstalledPackageLists(t *testing.T) {
	prependFakeCommand(t, homebrewCommandName, fakeHomebrewListScript)
	t.Setenv("FAKE_BREW_PREFIX", t.TempDir())
	config := testHomebrewConfig()
	monitor := initializedHomebrewMonitor(t, config)
	packages := installedHomebrewPackages(t, monitor)
	assertFastHomebrewPackages(t, packages)
}

const fakeHomebrewListScript = `#!/bin/sh
if [ "$1" = "--prefix" ]; then
  printf '%s\n' "$FAKE_BREW_PREFIX"
  exit 0
fi
if [ "$1" = "list" ] && [ "$2" = "--formula" ] && [ "$3" = "--versions" ]; then
  printf 'jq 1.7.1\n'
  exit 0
fi
if [ "$1" = "list" ] && [ "$2" = "--cask" ] && [ "$3" = "--versions" ]; then
  printf 'firefox 128.0\n'
  exit 0
fi
exit 2
`

func assertFastHomebrewPackages(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	hasPackageCount := len(packages) == 2
	hasFormulaVersion := hasPackageCount && packages[0].Version == "1.7.1"
	hasCaskVersion := hasPackageCount && packages[1].Version == "128.0"
	hasExpectedPackages := hasFormulaVersion && hasCaskVersion
	if !hasExpectedPackages {
		t.Fatalf("installed packages = %#v", packages)
	}
}

func TestHomebrewRejectsInvalidInstalledInfo(t *testing.T) {
	prependFakeCommand(t, homebrewCommandName, fakeInvalidHomebrewInfoScript)
	t.Setenv("FAKE_BREW_PREFIX", t.TempDir())
	config := testHomebrewConfig()
	config.Tools.Homebrew.TrackCasks = false
	monitor := initializedHomebrewMonitor(t, config)

	if _, err := monitor.formulae(); err == nil {
		t.Fatal("formulae should reject invalid Homebrew JSON")
	}
}

const fakeInvalidHomebrewInfoScript = `#!/bin/sh
if [ "$1" = "--prefix" ]; then
  printf '%s\n' "$FAKE_BREW_PREFIX"
  exit 0
fi
if [ "$1" = "info" ] && [ "$2" = "--json=v2" ] && [ "$3" = "--installed" ]; then
  printf 'not json\n'
  exit 0
fi
exit 2
`
