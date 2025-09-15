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