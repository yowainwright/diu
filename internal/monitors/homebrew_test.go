package monitors

import (
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestHomebrewMonitor(t *testing.T) {
	monitor := NewHomebrewMonitor()

	if monitor.Name() != "homebrew" {
		t.Errorf("Expected monitor name 'homebrew', got %s", monitor.Name())
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

func TestHomebrewInitialize(t *testing.T) {
	config := core.DefaultConfig()
	monitor := NewHomebrewMonitor()

	err := monitor.Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize monitor: %v", err)
	}
}

func TestParseInstalledFormulae(t *testing.T) {
	output := []byte(`{
		"formulae": [
			{
				"name": "wget",
				"dependencies": ["libidn2"],
				"linked_keg": "1.21.4",
				"installed": [
					{"version": "1.21.4", "time": 1710000000}
				]
			}
		]
	}`)

	packages, err := parseInstalledFormulae(output)
	if err != nil {
		t.Fatalf("parseInstalledFormulae failed: %v", err)
	}

	if len(packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(packages))
	}

	pkg := packages[0]
	if pkg.Name != "wget" {
		t.Fatalf("expected wget, got %s", pkg.Name)
	}
	if pkg.Tool != "homebrew" {
		t.Fatalf("expected tool homebrew, got %s", pkg.Tool)
	}
	if pkg.Version != "1.21.4" {
		t.Fatalf("expected version 1.21.4, got %s", pkg.Version)
	}
	if len(pkg.Dependencies) != 1 || pkg.Dependencies[0] != "libidn2" {
		t.Fatalf("expected dependencies to be preserved, got %v", pkg.Dependencies)
	}
}
