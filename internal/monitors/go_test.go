package monitors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestGoMonitor(t *testing.T) {
	monitor := NewGoMonitor()

	if monitor.Name() != core.ToolGo {
		t.Errorf("Expected monitor name '%s', got %s", core.ToolGo, monitor.Name())
	}
}

func TestGoMonitorInitialize(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewGoMonitor().(*GoMonitor)
	err := monitor.Initialize(config)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if monitor.goPath == "" {
		t.Error("Expected goPath to be set")
	}

	if monitor.goBin == "" {
		t.Error("Expected goBin to be set")
	}
}

func TestGoMonitorInitializeWithConfig(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Go.GoPath = "/custom/gopath"
	config.Tools.Go.GoBin = "/custom/gobin"

	monitor := NewGoMonitor().(*GoMonitor)
	err := monitor.Initialize(config)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if monitor.goPath != "/custom/gopath" {
		t.Errorf("Expected goPath '/custom/gopath', got %s", monitor.goPath)
	}

	if monitor.goBin != "/custom/gobin" {
		t.Errorf("Expected goBin '/custom/gobin', got %s", monitor.goBin)
	}
}

func TestGoParseCommand(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)

	tests := []struct {
		name     string
		args     []string
		packages []string
		metadata map[string]interface{}
	}{
		{
			name:     "get package",
			args:     []string{"get", "github.com/spf13/cobra"},
			packages: []string{"github.com/spf13/cobra"},
			metadata: map[string]interface{}{
				"subcommand": "get",
				"action":     "get",
			},
		},
		{
			name:     "get with update flag",
			args:     []string{"get", "-u", "github.com/gin-gonic/gin"},
			packages: []string{"github.com/gin-gonic/gin"},
			metadata: map[string]interface{}{
				"subcommand": "get",
				"action":     "get",
				"update":     true,
			},
		},
		{
			name:     "install package",
			args:     []string{"install", "github.com/golangci/golangci-lint/cmd/golangci-lint@latest"},
			packages: []string{"github.com/golangci/golangci-lint/cmd/golangci-lint@latest"},
			metadata: map[string]interface{}{
				"subcommand": "install",
				"action":     "install",
			},
		},
		{
			name:     "mod download",
			args:     []string{"mod", "download"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand":  "mod",
				"mod_command": "download",
				"action":      "mod_download",
			},
		},
		{
			name:     "mod tidy",
			args:     []string{"mod", "tidy"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand":  "mod",
				"mod_command": "tidy",
				"action":      "mod_tidy",
			},
		},
		{
			name:     "mod vendor",
			args:     []string{"mod", "vendor"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand":  "mod",
				"mod_command": "vendor",
				"action":      "mod_vendor",
			},
		},
		{
			name:     "mod init",
			args:     []string{"mod", "init", "github.com/user/project"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand":  "mod",
				"mod_command": "init",
				"module":      "github.com/user/project",
			},
		},
		{
			name:     "build",
			args:     []string{"build", "./..."},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "build",
				"action":     "build",
			},
		},
		{
			name:     "build with output",
			args:     []string{"build", "-o", "myapp", "./cmd/app"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "build",
				"action":     "build",
				"output":     "myapp",
			},
		},
		{
			name:     "build with -o= syntax",
			args:     []string{"build", "-o=myapp"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "build",
				"action":     "build",
				"output":     "myapp",
			},
		},
		{
			name:     "run file",
			args:     []string{"run", "main.go"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "run",
				"action":     "run",
				"file":       "main.go",
			},
		},
		{
			name:     "test all",
			args:     []string{"test", "./..."},
			packages: []string{"./..."},
			metadata: map[string]interface{}{
				"subcommand": "test",
				"action":     "test",
			},
		},
		{
			name:     "test specific package",
			args:     []string{"test", "github.com/user/project/pkg"},
			packages: []string{"github.com/user/project/pkg"},
			metadata: map[string]interface{}{
				"subcommand": "test",
				"action":     "test",
			},
		},
		{
			name:     "fmt",
			args:     []string{"fmt", "./..."},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "fmt",
				"action":     "fmt",
			},
		},
		{
			name:     "vet",
			args:     []string{"vet", "./..."},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "vet",
				"action":     "vet",
			},
		},
		{
			name:     "list modules",
			args:     []string{"list", "-m", "all"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "list",
				"action":     "list",
				"modules":    true,
			},
		},
		{
			name:     "clean",
			args:     []string{"clean"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "clean",
				"action":     "clean",
			},
		},
		{
			name:     "clean modcache",
			args:     []string{"clean", "-modcache"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "clean",
				"action":     "clean",
				"modcache":   true,
			},
		},
		{
			name:     "env",
			args:     []string{"env"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "env",
				"action":     "env",
			},
		},
		{
			name:     "version",
			args:     []string{"version"},
			packages: nil,
			metadata: map[string]interface{}{
				"subcommand": "version",
				"action":     "version",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := monitor.ParseCommand("go", tt.args)
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

func TestGoParseCommandEmptyArgs(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)

	record, err := monitor.ParseCommand("go", []string{})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}

	if record.Tool != core.ToolGo {
		t.Errorf("Expected tool '%s', got %s", core.ToolGo, record.Tool)
	}

	if len(record.PackagesAffected) != 0 {
		t.Errorf("Expected no packages, got %v", record.PackagesAffected)
	}
}

func TestGoExtractGoPackages(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)

	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "single package",
			args:     []string{"github.com/spf13/cobra"},
			expected: []string{"github.com/spf13/cobra"},
		},
		{
			name:     "multiple packages",
			args:     []string{"github.com/spf13/cobra", "github.com/spf13/viper"},
			expected: []string{"github.com/spf13/cobra", "github.com/spf13/viper"},
		},
		{
			name:     "package with version",
			args:     []string{"github.com/spf13/cobra@v1.8.0"},
			expected: []string{"github.com/spf13/cobra@v1.8.0"},
		},
		{
			name:     "skip flags",
			args:     []string{"-u", "github.com/spf13/cobra", "-v"},
			expected: []string{"github.com/spf13/cobra"},
		},
		{
			name:     "skip current directory patterns",
			args:     []string{".", "./...", "..."},
			expected: []string{".", "./...", "..."},
		},
		{
			name:     "simple package name",
			args:     []string{"mypackage"},
			expected: []string{"mypackage"},
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packages := monitor.extractGoPackages(tt.args)

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

func TestGoExtractOutputFlag(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "no output flag",
			args:     []string{"build", "./..."},
			expected: "",
		},
		{
			name:     "-o flag",
			args:     []string{"build", "-o", "myapp", "./cmd"},
			expected: "myapp",
		},
		{
			name:     "-o= syntax",
			args:     []string{"build", "-o=myapp"},
			expected: "myapp",
		},
		{
			name:     "-o at end",
			args:     []string{"-o", "output"},
			expected: "output",
		},
		{
			name:     "-o without value",
			args:     []string{"build", "-o"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := monitor.extractOutputFlag(tt.args)
			if output != tt.expected {
				t.Errorf("Expected output '%s', got '%s'", tt.expected, output)
			}
		})
	}
}

func TestGoGetBinaries(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Go.GoBin = tmpDir

	monitor := NewGoMonitor().(*GoMonitor)
	monitor.Initialize(config)

	executablePath := filepath.Join(tmpDir, "testbin")
	if err := os.WriteFile(executablePath, []byte("#!/bin/bash\necho test"), 0755); err != nil {
		t.Fatalf("Failed to create test executable: %v", err)
	}

	nonExecPath := filepath.Join(tmpDir, "nonexec")
	if err := os.WriteFile(nonExecPath, []byte("not executable"), 0644); err != nil {
		t.Fatalf("Failed to create non-executable: %v", err)
	}

	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	packages, err := monitor.getBinaries()
	if err != nil {
		t.Fatalf("getBinaries failed: %v", err)
	}

	if len(packages) != 1 {
		t.Errorf("Expected 1 binary, got %d", len(packages))
	}

	if len(packages) > 0 {
		if packages[0].Name != "testbin" {
			t.Errorf("Expected binary name 'testbin', got %s", packages[0].Name)
		}
		if packages[0].Tool != core.ToolGoBinary {
			t.Errorf("Expected tool '%s', got %s", core.ToolGoBinary, packages[0].Tool)
		}
	}
}

func TestGoGetBinariesNonExistentDir(t *testing.T) {
	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Go.GoBin = "/nonexistent/path/that/does/not/exist"

	monitor := NewGoMonitor().(*GoMonitor)
	monitor.Initialize(config)

	packages, err := monitor.getBinaries()
	if err != nil {
		t.Fatalf("getBinaries should not error for non-existent dir: %v", err)
	}

	if packages != nil && len(packages) != 0 {
		t.Errorf("Expected nil or empty packages, got %v", packages)
	}
}

func TestGoGetBinariesEmptyGoBin(t *testing.T) {
	monitor := NewGoMonitor().(*GoMonitor)
	monitor.goBin = ""

	packages, err := monitor.getBinaries()
	if err != nil {
		t.Fatalf("getBinaries should not error for empty goBin: %v", err)
	}

	if packages != nil {
		t.Errorf("Expected nil packages, got %v", packages)
	}
}

func TestGoGetInstalledPackages(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.AutoInstallWrappers = false
	config.Tools.Go.GoBin = tmpDir

	monitor := NewGoMonitor().(*GoMonitor)
	monitor.Initialize(config)

	executablePath := filepath.Join(tmpDir, "mytool")
	if err := os.WriteFile(executablePath, []byte("#!/bin/bash"), 0755); err != nil {
		t.Fatalf("Failed to create executable: %v", err)
	}

	packages, err := monitor.GetInstalledPackages()
	if err != nil {
		t.Fatalf("GetInstalledPackages failed: %v", err)
	}

	if packages == nil {
		t.Fatal("Expected non-nil packages")
	}
}
