package monitors

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestProcessMonitor(t *testing.T) {
	monitor := NewProcessMonitor("test-tool", "test-binary")

	if monitor.Name() != "test-tool" {
		t.Errorf("Expected name 'test-tool', got %s", monitor.Name())
	}

	if monitor.binaryPath != "test-binary" {
		t.Errorf("Expected binaryPath 'test-binary', got %s", monitor.binaryPath)
	}
}

func TestProcessMonitorInitialize(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewProcessMonitor("test", "/usr/bin/test")
	err := monitor.Initialize(config)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	expectedWrapperPath := filepath.Join(tmpDir, "test")
	if monitor.wrapperPath != expectedWrapperPath {
		t.Errorf("Expected wrapperPath %s, got %s", expectedWrapperPath, monitor.wrapperPath)
	}
}

func TestProcessMonitorParseCommand(t *testing.T) {
	monitor := NewProcessMonitor("mytool", "/usr/bin/mytool")

	record, err := monitor.ParseCommand("mytool", []string{"arg1", "arg2"})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}

	if record.Tool != "mytool" {
		t.Errorf("Expected tool 'mytool', got %s", record.Tool)
	}

	if record.Command != "mytool" {
		t.Errorf("Expected command 'mytool', got %s", record.Command)
	}

	if len(record.Args) != 2 {
		t.Errorf("Expected 2 args, got %d", len(record.Args))
	}
}

func TestProcessMonitorGetInstalledPackages(t *testing.T) {
	monitor := NewProcessMonitor("test", "/usr/bin/test")

	packages, err := monitor.GetInstalledPackages()
	if err == nil {
		t.Error("Expected error from base ProcessMonitor.GetInstalledPackages")
	}

	if packages != nil {
		t.Error("Expected nil packages")
	}
}

func TestProcessMonitorGenerateWrapperScript(t *testing.T) {
	monitor := NewProcessMonitor("brew", "/usr/local/bin/brew")
	monitor.originalPath = "/usr/local/bin/brew"

	script := monitor.generateWrapperScript()

	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Script should start with shebang")
	}

	if !strings.Contains(script, core.DefaultSocketPath) {
		t.Errorf("Script should contain socket path %s", core.DefaultSocketPath)
	}

	if !strings.Contains(script, "/usr/local/bin/brew") {
		t.Error("Script should contain original binary path")
	}

	if !strings.Contains(script, `"tool": "brew"`) && !strings.Contains(script, `\"tool\": \"brew\"`) {
		t.Error("Script should contain tool name in JSON")
	}

	if !strings.Contains(script, "exit $EXIT_CODE") {
		t.Error("Script should exit with original exit code")
	}
}

func TestProcessMonitorInstallWrapper(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewProcessMonitor("testtool", "/usr/bin/testtool")
	monitor.BaseMonitor.config = config
	monitor.wrapperPath = filepath.Join(tmpDir, "testtool")
	monitor.originalPath = "/usr/bin/testtool"

	err := monitor.InstallWrapper()
	if err != nil {
		t.Fatalf("InstallWrapper failed: %v", err)
	}

	if _, err := os.Stat(monitor.wrapperPath); os.IsNotExist(err) {
		t.Error("Wrapper script not created")
	}

	content, err := os.ReadFile(monitor.wrapperPath)
	if err != nil {
		t.Fatalf("Failed to read wrapper: %v", err)
	}

	if !strings.Contains(string(content), "#!/bin/bash") {
		t.Error("Wrapper should be a bash script")
	}

	info, _ := os.Stat(monitor.wrapperPath)
	if info.Mode()&0111 == 0 {
		t.Error("Wrapper should be executable")
	}
}

func TestProcessMonitorFindOriginalBinary(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir

	monitor := NewProcessMonitor("ls", "ls")
	monitor.BaseMonitor.config = config

	original := monitor.findOriginalBinary()

	if original == "" {
		t.Skip("ls not found in PATH")
	}

	if original == filepath.Join(tmpDir, "ls") {
		t.Error("Should not find wrapper dir in original binary search")
	}

	if !strings.Contains(original, "ls") {
		t.Errorf("Original should contain 'ls', got %s", original)
	}
}

func TestProcessMonitorFindOriginalBinarySkipsWrapperDir(t *testing.T) {
	tmpDir := t.TempDir()

	wrapperBinary := filepath.Join(tmpDir, "mytool")
	if err := os.WriteFile(wrapperBinary, []byte("#!/bin/bash"), 0755); err != nil {
		t.Fatalf("Failed to create wrapper: %v", err)
	}

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir

	monitor := NewProcessMonitor("mytool", "mytool")
	monitor.BaseMonitor.config = config

	original := monitor.findOriginalBinary()

	if original == wrapperBinary {
		t.Error("Should not return wrapper directory binary as original")
	}
}

func TestCreateWrapperScript(t *testing.T) {
	script := CreateWrapperScript("npm", "/usr/local/bin/npm", "/tmp/wrappers")

	if !strings.Contains(script, "#!/bin/bash") {
		t.Error("Script should start with shebang")
	}

	if !strings.Contains(script, "/usr/local/bin/npm") {
		t.Error("Script should contain original path")
	}

	if !strings.Contains(script, `"tool": "npm"`) && !strings.Contains(script, `\"tool\": \"npm\"`) {
		t.Error("Script should contain tool name")
	}

	if !strings.Contains(script, "curl") {
		t.Error("Script should use curl for HTTP API")
	}

	if !strings.Contains(script, "exit $EXIT_CODE") {
		t.Error("Script should preserve exit code")
	}
}

func TestProcessMonitorStart(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewProcessMonitor("test", "/usr/bin/test")
	monitor.Initialize(config)

	ctx := context.Background()
	eventChan := make(chan *core.ExecutionRecord, 10)

	err := monitor.Start(ctx, eventChan)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
}
