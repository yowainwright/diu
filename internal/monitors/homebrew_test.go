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

func TestHomebrewParseCommand(t *testing.T) {
	monitor := NewHomebrewMonitor().(*HomebrewMonitor)

	tests := []struct {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := monitor.ParseCommand("brew", tt.args)
			if err != nil {
				t.Fatalf("ParseCommand failed: %v", err)
			}

			// Check packages
			if len(record.PackagesAffected) != len(tt.expected.packages) {
				t.Errorf("Expected %d packages, got %d",
					len(tt.expected.packages), len(record.PackagesAffected))
			}

			for i, pkg := range tt.expected.packages {
				if i < len(record.PackagesAffected) && record.PackagesAffected[i] != pkg {
					t.Errorf("Expected package %s, got %s",
						pkg, record.PackagesAffected[i])
				}
			}

			// Check metadata
			for key, expectedVal := range tt.expected.metadata {
				if val, exists := record.Metadata[key]; !exists || val != expectedVal {
					t.Errorf("Expected metadata %s=%v, got %v",
						key, expectedVal, val)
				}
			}
		})
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
	if len(packages) != 1 || packages[0].Version != "20.0.0" {
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
	prependFakeCommand(t, homebrewCommandName, `#!/bin/sh
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
`)

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

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Homebrew.CellarPaths = nil
	config.Tools.Homebrew.TrackCasks = true

	monitor := NewHomebrewMonitor().(*HomebrewMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if !contains(monitor.cellarPaths, cellar) {
		t.Fatalf("cellarPaths = %#v, want %s", monitor.cellarPaths, cellar)
	}
	if monitor.caskroom == "" {
		t.Fatalf("caskroom was not detected")
	}

	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("Expected formula and cask packages, got %#v", packages)
	}
	byName := make(map[string]*core.PackageInfo)
	for _, pkg := range packages {
		byName[pkg.Name] = pkg
	}
	if byName["jq"].Version != "1.7" || byName["jq"].Tool != core.ToolHomebrew {
		t.Fatalf("Unexpected jq package: %#v", byName["jq"])
	}
	if byName["firefox"].Tool != homebrewCaskTool {
		t.Fatalf("Unexpected firefox package: %#v", byName["firefox"])
	}
}

func TestHomebrewUsesFastInstalledPackageLists(t *testing.T) {
	prependFakeCommand(t, homebrewCommandName, `#!/bin/sh
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
`)
	t.Setenv("FAKE_BREW_PREFIX", t.TempDir())
	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Homebrew.TrackCasks = true
	monitor := NewHomebrewMonitor().(*HomebrewMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}
	if len(packages) != 2 || packages[0].Version != "1.7.1" || packages[1].Version != "128.0" {
		t.Fatalf("installed packages = %#v", packages)
	}
}

func TestHomebrewRejectsInvalidInstalledInfo(t *testing.T) {
	prependFakeCommand(t, homebrewCommandName, `#!/bin/sh
if [ "$1" = "--prefix" ]; then
  printf '%s\n' "$FAKE_BREW_PREFIX"
  exit 0
fi
if [ "$1" = "info" ] && [ "$2" = "--json=v2" ] && [ "$3" = "--installed" ]; then
  printf 'not json\n'
  exit 0
fi
exit 2
`)
	t.Setenv("FAKE_BREW_PREFIX", t.TempDir())

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Homebrew.TrackCasks = false

	monitor := NewHomebrewMonitor().(*HomebrewMonitor)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if _, err := monitor.getFormulae(); err == nil {
		t.Fatal("getFormulae should reject invalid Homebrew JSON")
	}
}
