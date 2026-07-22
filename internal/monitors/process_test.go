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

func TestNewProcessMonitorUsesUserHomeDir(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	monitor := NewProcessMonitor("test", "test")
	if monitor.homeDir != homeDir {
		t.Fatalf("homeDir = %q, want %q", monitor.homeDir, homeDir)
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
		recordLookupCmd       = `command -v "$DIU_BINARY"`
		recordFallbackCmd     = `"$DIU_RECORD_BINARY" record`
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
	if !strings.Contains(script, core.GeneratedWrapperMarker) {
		t.Error("Script should include the DIU marker")
	}

	if !strings.Contains(script, "nc") {
		t.Error("Script should use nc for socket delivery")
	}

	if !strings.Contains(script, socketAssignment) {
		t.Error("Script should configure the DIU socket path")
	}

	if !strings.Contains(script, recordLookupCmd) {
		t.Error("Script should resolve diu at runtime")
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

func TestProcessMonitorFindOriginalBinaryRejectsAbsoluteWrapperPath(t *testing.T) {
	wrapperDir := t.TempDir()
	wrapperPath := filepath.Join(wrapperDir, "mytool")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create wrapper: %v", err)
	}
	if err := os.Chmod(wrapperPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark wrapper executable: %v", err)
	}

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = wrapperDir

	monitor := NewProcessMonitor("mytool", wrapperPath)
	monitor.config = config

	if _, err := monitor.findOriginalBinary(); err == nil {
		t.Fatal("Expected absolute wrapper path to be rejected")
	}
}

func TestProcessMonitorFindOriginalBinaryReturnsAbsoluteNonWrapperPath(t *testing.T) {
	wrapperDir := t.TempDir()
	binaryPath := filepath.Join(t.TempDir(), "mytool")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create binary: %v", err)
	}
	if err := os.Chmod(binaryPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark binary executable: %v", err)
	}

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = wrapperDir

	monitor := NewProcessMonitor("mytool", binaryPath)
	monitor.config = config

	original, err := monitor.findOriginalBinary()
	if err != nil {
		t.Fatalf("findOriginalBinary failed: %v", err)
	}
	expectedPath, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		t.Fatalf("Failed to resolve binary path: %v", err)
	}
	if original != expectedPath {
		t.Fatalf("original path = %s, want %s", original, expectedPath)
	}
}

func TestProcessMonitorFindOriginalBinaryRejectsAbsoluteSymlinkToWrapperPath(t *testing.T) {
	wrapperDir := t.TempDir()
	wrapperPath := filepath.Join(wrapperDir, "mytool")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create wrapper: %v", err)
	}
	if err := os.Chmod(wrapperPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark wrapper executable: %v", err)
	}

	symlinkPath := filepath.Join(t.TempDir(), "mytool")
	if err := os.Symlink(wrapperPath, symlinkPath); err != nil {
		t.Skipf("Symlinks are not available: %v", err)
	}

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = wrapperDir

	monitor := NewProcessMonitor("mytool", symlinkPath)
	monitor.config = config

	if _, err := monitor.findOriginalBinary(); err == nil {
		t.Fatal("Expected symlink to wrapper path to be rejected")
	}
}

func TestProcessMonitorFindOriginalBinarySkipsPathSymlinkToWrapperPath(t *testing.T) {
	wrapperDir := t.TempDir()
	wrapperPath := filepath.Join(wrapperDir, "mytool")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create wrapper: %v", err)
	}
	if err := os.Chmod(wrapperPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark wrapper executable: %v", err)
	}

	pathDir := t.TempDir()
	symlinkPath := filepath.Join(pathDir, "mytool")
	if err := os.Symlink(wrapperPath, symlinkPath); err != nil {
		t.Skipf("Symlinks are not available: %v", err)
	}
	t.Setenv("PATH", pathDir)

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = wrapperDir

	monitor := NewProcessMonitor("mytool", "mytool")
	monitor.config = config

	if _, err := monitor.findOriginalBinary(); err == nil {
		t.Fatal("Expected PATH symlink to wrapper path to be skipped")
	}
}

func TestProcessMonitorFindOriginalBinarySkipsResolvedWrapperDirFromPath(t *testing.T) {
	realWrapperDir := t.TempDir()
	wrapperPath := filepath.Join(realWrapperDir, "mytool")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create wrapper: %v", err)
	}
	if err := os.Chmod(wrapperPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark wrapper executable: %v", err)
	}

	wrapperAlias := filepath.Join(t.TempDir(), "wrappers")
	if err := os.Symlink(realWrapperDir, wrapperAlias); err != nil {
		t.Skipf("Symlinks are not available: %v", err)
	}
	t.Setenv("PATH", realWrapperDir)

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = wrapperAlias

	monitor := NewProcessMonitor("mytool", "mytool")
	monitor.config = config

	if _, err := monitor.findOriginalBinary(); err == nil {
		t.Fatal("Expected resolved wrapper directory candidate to be skipped")
	}
}

func TestPathWithinDirectory(t *testing.T) {
	parent := t.TempDir()
	childDir := filepath.Join(parent, "child")
	if err := os.MkdirAll(childDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create child dir: %v", err)
	}
	childPath := filepath.Join(childDir, "tool")
	if err := os.WriteFile(childPath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create child path: %v", err)
	}

	if !pathWithinDirectory(childPath, parent) {
		t.Fatal("Expected child path to be within parent")
	}
	if !pathWithinDirectory(parent, parent) {
		t.Fatal("Expected directory to be within itself")
	}
	if pathWithinDirectory(filepath.Join(t.TempDir(), "tool"), parent) {
		t.Fatal("Expected outside path to be outside parent")
	}
	if pathWithinDirectory("", parent) {
		t.Fatal("Expected empty path to be outside parent")
	}
	if pathWithinDirectory(childPath, "") {
		t.Fatal("Expected empty directory to reject containment")
	}
}

func TestPathWithinDirectoryRelativePaths(t *testing.T) {
	workingDir := t.TempDir()
	originalWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("Failed to change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWorkingDir); err != nil {
			t.Fatalf("Failed to restore working directory: %v", err)
		}
	})

	childDir := filepath.Join("root", "child")
	if err := os.MkdirAll(childDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create relative child dir: %v", err)
	}
	childPath := filepath.Join(childDir, "tool")
	if err := os.WriteFile(childPath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create relative child path: %v", err)
	}

	if !pathWithinDirectory(childPath, "root") {
		t.Fatal("Expected relative child path to be within relative parent")
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
	fishDir := filepath.Join(homeDir, ".config", "fish")
	if err := os.MkdirAll(fishDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create fish config dir: %v", err)
	}
	fishConfig := filepath.Join(fishDir, "config.fish")
	if err := os.WriteFile(fishConfig, []byte("# existing\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write fish config: %v", err)
	}

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = filepath.Join(homeDir, "wrap$dir\"with`chars")

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
	exportLine := core.PosixPathLine(config.Monitoring.Process.WrapperDir)
	if strings.Count(string(content), exportLine) != 1 {
		t.Fatalf("shell config content = %q, want one export line", content)
	}

	fishContent, err := os.ReadFile(fishConfig)
	if err != nil {
		t.Fatalf("Failed to read fish config: %v", err)
	}
	fishLine := core.FishPathLine(config.Monitoring.Process.WrapperDir)
	if strings.Count(string(fishContent), fishLine) != 1 {
		t.Fatalf("fish config content = %q, want one fish path line", fishContent)
	}
	if strings.Contains(string(fishContent), "export PATH=") {
		t.Fatalf("fish config content = %q, should not use POSIX export", fishContent)
	}
}

func TestProcessMonitorUpdateShellConfigReturnsReadError(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(homeDir, ".zshrc"), core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create invalid shell config: %v", err)
	}
	monitor := NewProcessMonitor("testtool", "testtool")
	monitor.config = core.DefaultConfig()
	monitor.homeDir = homeDir

	if err := monitor.updateShellConfig(); err == nil {
		t.Fatal("Expected shell config read error")
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
	expectedPath, err := filepath.EvalSymlinks(executablePath)
	if err != nil {
		t.Fatalf("Failed to resolve executable path: %v", err)
	}
	if validated != expectedPath {
		t.Fatalf("validated path = %s, want %s", validated, expectedPath)
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
