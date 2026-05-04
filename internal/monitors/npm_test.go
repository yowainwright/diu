package monitors

import (
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestNPMMonitor(t *testing.T) {
	monitor := NewNPMMonitor()

	if monitor.Name() != core.ToolNPM {
		t.Errorf("Expected monitor name '%s', got %s", core.ToolNPM, monitor.Name())
	}
}

func TestNPMParseCommand(t *testing.T) {
	monitor := NewNPMMonitor().(*NPMMonitor)

	tests := []struct {
		name     string
		args     []string
		packages []string
		metadata map[string]interface{}
	}{
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := monitor.ParseCommand("npm", tt.args)
			if err != nil {
				t.Fatalf("ParseCommand failed: %v", err)
			}

			if len(record.PackagesAffected) != len(tt.packages) {
				t.Errorf("Expected %d packages, got %d: %v",
					len(tt.packages), len(record.PackagesAffected), record.PackagesAffected)
			}

			for i, pkg := range tt.packages {
				if i < len(record.PackagesAffected) && record.PackagesAffected[i] != pkg {
					t.Errorf("Expected package %s, got %s", pkg, record.PackagesAffected[i])
				}
			}

			for key, expectedVal := range tt.metadata {
				if val, exists := record.Metadata[key]; !exists || val != expectedVal {
					t.Errorf("Expected metadata %s=%v, got %v", key, expectedVal, val)
				}
			}
		})
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

	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "single package",
			args:     []string{"express"},
			expected: []string{"express"},
		},
		{
			name:     "multiple packages",
			args:     []string{"express", "lodash", "moment"},
			expected: []string{"express", "lodash", "moment"},
		},
		{
			name:     "package with version",
			args:     []string{"express@4.18.0"},
			expected: []string{"express"},
		},
		{
			name:     "scoped package",
			args:     []string{"@types/node"},
			expected: []string{"@types/node"},
		},
		{
			name:     "skip flags",
			args:     []string{"-g", "typescript", "--save-dev"},
			expected: []string{"typescript"},
		},
		{
			name:     "skip registry flag with value",
			args:     []string{"--registry", "https://npm.example.com", "my-package"},
			expected: []string{"my-package"},
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packages := monitor.extractPackagesFromNPMArgs(tt.args)

			if len(packages) != len(tt.expected) {
				t.Errorf("Expected %d packages, got %d: %v", len(tt.expected), len(packages), packages)
				return
			}

			for i, pkg := range tt.expected {
				if packages[i] != pkg {
					t.Errorf("Expected package %s at index %d, got %s", pkg, i, packages[i])
				}
			}
		})
	}
}

func TestNPMExtractDepth(t *testing.T) {
	monitor := NewNPMMonitor().(*NPMMonitor)

	tests := []struct {
		name     string
		args     []string
		expected int
	}{
		{
			name:     "no depth flag",
			args:     []string{"list"},
			expected: -1,
		},
		{
			name:     "depth 0",
			args:     []string{"list", "--depth", "0"},
			expected: 0,
		},
		{
			name:     "depth 5",
			args:     []string{"--depth", "5", "list"},
			expected: 5,
		},
		{
			name:     "invalid depth",
			args:     []string{"--depth", "abc"},
			expected: -1,
		},
		{
			name:     "depth flag at end without value",
			args:     []string{"list", "--depth"},
			expected: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depth := monitor.extractDepth(tt.args)
			if depth != tt.expected {
				t.Errorf("Expected depth %d, got %d", tt.expected, depth)
			}
		})
	}
}
