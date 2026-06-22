package monitors

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
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

func TestProcessMonitorInitializeUsesBinaryNameForWrapper(t *testing.T) {
	const wrapperBinaryName = "brew"

	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewProcessMonitor(core.ToolHomebrew, wrapperBinaryName)
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	expectedWrapperPath := filepath.Join(tmpDir, wrapperBinaryName)
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
	const (
		wrapperToolName       = "brew"
		originalBinaryPath    = "/usr/local/bin/brew"
		shebangText           = "#!/bin/bash"
		toolAssignment        = `DIU_TOOL="brew"`
		socketAssignment      = `DIU_SOCKET=`
		recordFallbackCmd     = `"$DIU_BINARY" record`
		toolJSONField         = `"tool": "$DIU_TOOL"`
		argsJSONField         = `"args": $args_json`
		exitCodeForwardingCmd = "exit $EXIT_CODE"
	)

	monitor := NewProcessMonitor(wrapperToolName, originalBinaryPath)
	monitor.config = core.DefaultConfig()
	monitor.originalPath = originalBinaryPath

	script := monitor.generateWrapperScript()

	if !strings.Contains(script, shebangText) {
		t.Error("Script should start with shebang")
	}

	if !strings.Contains(script, "nc") {
		t.Error("Script should use nc for socket delivery")
	}

	if !strings.Contains(script, socketAssignment) {
		t.Error("Script should configure the DIU socket path")
	}

	if !strings.Contains(script, recordFallbackCmd) {
		t.Error("Script should fall back to direct diu record")
	}

	if !strings.Contains(script, originalBinaryPath) {
		t.Error("Script should contain original binary path")
	}

	if !strings.Contains(script, toolAssignment) {
		t.Error("Script should assign the tool name")
	}

	if !strings.Contains(script, toolJSONField) {
		t.Error("Script should include the tool field in JSON")
	}

	if !strings.Contains(script, argsJSONField) {
		t.Error("Script should send args as a JSON array")
	}

	if !strings.Contains(script, exitCodeForwardingCmd) {
		t.Error("Script should exit with original exit code")
	}
}

func TestProcessMonitorWrapperRecordsWithoutDaemon(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	config := core.DefaultConfig()
	config.Daemon.SocketPath = filepath.Join(tempHome, "run", "missing.sock")
	config.Storage.JSONFile = filepath.Join(tempHome, "data", "executions.json")
	config.Monitoring.Process.WrapperDir = filepath.Join(tempHome, "wrappers")
	config.Monitoring.Process.AutoInstallWrappers = false

	configPath := filepath.Join(tempHome, ".config", "diu", "config.json")
	if err := config.SaveTo(configPath); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	binaryPath := filepath.Join(t.TempDir(), "diu")
	build := exec.Command("go", "build", "-o", binaryPath, "../../cmd/diu")
	build.Env = append(os.Environ(), "HOME="+tempHome)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build diu test binary: %v\n%s", err, output)
	}

	originalPath := filepath.Join(t.TempDir(), "original-tool")
	if err := os.WriteFile(originalPath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write original command: %v", err)
	}
	if err := os.Chmod(originalPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to chmod original command: %v", err)
	}

	wrapperPath := filepath.Join(t.TempDir(), "wrapped-tool")
	script := generateProcessWrapperScript(originalPath, binaryPath, config.Daemon.SocketPath, "test-tool")
	if err := os.WriteFile(wrapperPath, []byte(script), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write wrapper: %v", err)
	}
	if err := os.Chmod(wrapperPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to chmod wrapper: %v", err)
	}

	run := exec.Command(wrapperPath, "alpha", "beta")
	run.Env = append(os.Environ(), "HOME="+tempHome)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("Wrapper failed: %v\n%s", err, output)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		store, err := storage.NewJSONStorage(config)
		if err == nil {
			executions, queryErr := store.GetExecutions(storage.QueryOptions{Tool: "test-tool"})
			if closeErr := store.Close(); closeErr != nil {
				t.Fatalf("Failed to close storage: %v", closeErr)
			}
			if queryErr != nil {
				t.Fatalf("Failed to query storage: %v", queryErr)
			}
			if len(executions) > 0 {
				if got := strings.Join(executions[0].Args, " "); got != "alpha beta" {
					t.Fatalf("Recorded args = %q, want alpha beta", got)
				}
				return
			}
		}

		if time.Now().After(deadline) {
			t.Fatal("Timed out waiting for wrapper fallback to record execution")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestProcessMonitorInstallWrapper(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewProcessMonitor("testtool", "/usr/bin/testtool")
	monitor.config = config
	monitor.wrapperPath = filepath.Join(tmpDir, "testtool")
	monitor.originalPath = "/usr/bin/testtool"
	monitor.homeDir = homeDir

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
	if info.Mode()&core.ExecutableModeMask == 0 {
		t.Error("Wrapper should be executable")
	}
}

func TestProcessMonitorFindOriginalBinary(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir

	monitor := NewProcessMonitor("ls", "ls")
	monitor.config = config

	original, err := monitor.findOriginalBinary()

	if err != nil {
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
	if err := os.WriteFile(wrapperBinary, []byte("#!/bin/bash"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create wrapper: %v", err)
	}
	if err := os.Chmod(wrapperBinary, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark wrapper executable: %v", err)
	}

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir

	monitor := NewProcessMonitor("mytool", "mytool")
	monitor.config = config

	original, err := monitor.findOriginalBinary()
	if err != nil {
		return
	}

	if original == wrapperBinary {
		t.Error("Should not return wrapper directory binary as original")
	}
}

func TestProcessMonitorInstallWrapperFailsWhenOriginalMissing(t *testing.T) {
	wrapperDir := t.TempDir()
	t.Setenv("PATH", wrapperDir)

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = wrapperDir
	config.Monitoring.Process.AutoInstallWrappers = true

	monitor := NewProcessMonitor("missing", "missing")
	err := monitor.Initialize(config)
	if err == nil {
		t.Fatal("Expected Initialize to fail when original binary cannot be resolved")
	}

	if _, statErr := os.Stat(filepath.Join(wrapperDir, "missing")); !os.IsNotExist(statErr) {
		t.Fatalf("Expected no wrapper to be written, stat err=%v", statErr)
	}
}

func TestProcessMonitorStart(t *testing.T) {
	tmpDir := t.TempDir()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.Monitoring.Process.AutoInstallWrappers = false

	monitor := NewProcessMonitor("test", "/usr/bin/test")
	if err := monitor.Initialize(config); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	ctx := context.Background()
	eventChan := make(chan *core.ExecutionRecord, 10)

	err := monitor.Start(ctx, eventChan)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
}

func TestProcessMonitorExecuteAndTrack(t *testing.T) {
	binaryPath := filepath.Join(t.TempDir(), "testtool")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/bash\nexit 7\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write binary: %v", err)
	}
	if err := os.Chmod(binaryPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to chmod binary: %v", err)
	}

	monitor := NewProcessMonitor("testtool", binaryPath)
	monitor.originalPath = binaryPath

	record, err := monitor.ExecuteAndTrack("testtool", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("ExecuteAndTrack failed: %v", err)
	}
	if record.Tool != "testtool" {
		t.Fatalf("Tool = %s, want testtool", record.Tool)
	}
	if record.Command != "testtool alpha beta" {
		t.Fatalf("Command = %q, want testtool alpha beta", record.Command)
	}
	if record.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", record.ExitCode)
	}
	if strings.Join(record.Args, " ") != "alpha beta" {
		t.Fatalf("Args = %#v, want alpha beta", record.Args)
	}
}

func TestProcessMonitorUpdateShellConfig(t *testing.T) {
	homeDir := t.TempDir()
	zshrc := filepath.Join(homeDir, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("# existing\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write shell config: %v", err)
	}

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = filepath.Join(homeDir, "wrappers")

	monitor := NewProcessMonitor("testtool", "testtool")
	monitor.config = config
	monitor.homeDir = homeDir

	if err := monitor.updateShellConfig(); err != nil {
		t.Fatalf("updateShellConfig failed: %v", err)
	}
	if err := monitor.updateShellConfig(); err != nil {
		t.Fatalf("second updateShellConfig failed: %v", err)
	}

	content, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("Failed to read shell config: %v", err)
	}
	exportLine := `export PATH="` + config.Monitoring.Process.WrapperDir + `:$PATH"`
	if strings.Count(string(content), exportLine) != 1 {
		t.Fatalf("shell config content = %q, want one export line", content)
	}
}

func TestValidateExecutablePath(t *testing.T) {
	tempDir := t.TempDir()
	executablePath := filepath.Join(tempDir, "tool")
	if err := os.WriteFile(executablePath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write executable: %v", err)
	}
	if err := os.Chmod(executablePath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to chmod executable: %v", err)
	}

	validated, err := validateExecutablePath(executablePath)
	if err != nil {
		t.Fatalf("validateExecutablePath failed: %v", err)
	}
	if validated != executablePath {
		t.Fatalf("validated path = %s, want %s", validated, executablePath)
	}

	nonExecutablePath := filepath.Join(tempDir, "notes.txt")
	if err := os.WriteFile(nonExecutablePath, []byte("notes"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write non-executable: %v", err)
	}
	for name, path := range map[string]string{
		"empty":          "",
		"relative":       "tool",
		"directory":      tempDir,
		"non-executable": nonExecutablePath,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateExecutablePath(path); err == nil {
				t.Fatal("Expected validation to fail")
			}
		})
	}
}
