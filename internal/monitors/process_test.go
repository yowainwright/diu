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
	monitor := NewProcessMonitor("brew", "/usr/local/bin/brew")
	monitor.config = core.DefaultConfig()
	monitor.originalPath = "/usr/local/bin/brew"

	script := monitor.generateWrapperScript()
	assertWrapperScriptContainsRequiredParts(t, script)
}

var requiredWrapperScriptParts = []string{
	"#!/bin/bash",
	core.GeneratedWrapperMarker,
	"nc",
	`DIU_SOCKET=`,
	`command -v "$DIU_BINARY"`,
	`"$DIU_RECORD_BINARY" record`,
	"/usr/local/bin/brew",
	`DIU_TOOL="brew"`,
	`"tool": "$DIU_TOOL"`,
	`"args": $args_json`,
	"exit $EXIT_CODE",
}

func assertWrapperScriptContainsRequiredParts(t *testing.T, script string) {
	t.Helper()

	for _, part := range requiredWrapperScriptParts {
		if !strings.Contains(script, part) {
			t.Fatalf("Script missing %q", part)
		}
	}
}

func TestProcessMonitorWrapperRecordsWithoutDaemon(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	config := wrapperFallbackConfig(tempHome)
	saveWrapperFallbackConfig(t, config, tempHome)
	binaryPath := buildDIUTestBinary(t, tempHome)
	originalPath := writeOriginalCommand(t)
	wrapperPath := writeProcessWrapper(t, config, originalPath, binaryPath)

	runProcessWrapper(t, wrapperPath, tempHome)
	waitForWrapperFallbackRecord(t, config)
}

func wrapperFallbackConfig(tempHome string) *core.Config {
	config := core.DefaultConfig()
	config.Daemon.SocketPath = filepath.Join(tempHome, "run", "missing.sock")
	config.Storage.JSONFile = filepath.Join(tempHome, "data", "executions.json")
	config.Monitoring.Process.WrapperDir = filepath.Join(tempHome, "wrappers")
	config.Monitoring.Process.AutoInstallWrappers = false
	return config
}

func saveWrapperFallbackConfig(t *testing.T, config *core.Config, tempHome string) {
	t.Helper()

	configPath := filepath.Join(tempHome, ".config", "diu", "config.json")
	if err := config.SaveTo(configPath); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
}

func buildDIUTestBinary(t *testing.T, tempHome string) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "diu")
	build := exec.Command("go", "build", "-o", binaryPath, "../../cmd/diu")
	build.Env = append(os.Environ(), "HOME="+tempHome)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build diu test binary: %v\n%s", err, output)
	}
	return binaryPath
}

func writeOriginalCommand(t *testing.T) string {
	t.Helper()

	originalPath := filepath.Join(t.TempDir(), "original-tool")
	if err := os.WriteFile(originalPath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write original command: %v", err)
	}
	if err := os.Chmod(originalPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to chmod original command: %v", err)
	}
	return originalPath
}

func writeProcessWrapper(t *testing.T, config *core.Config, originalPath, binaryPath string) string {
	t.Helper()

	wrapperPath := filepath.Join(t.TempDir(), "wrapped-tool")
	script := generateProcessWrapperScript(originalPath, binaryPath, config.Daemon.SocketPath, "test-tool")
	if err := os.WriteFile(wrapperPath, []byte(script), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write wrapper: %v", err)
	}
	if err := os.Chmod(wrapperPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to chmod wrapper: %v", err)
	}
	return wrapperPath
}

func runProcessWrapper(t *testing.T, wrapperPath, tempHome string) {
	t.Helper()

	run := exec.Command(wrapperPath, "alpha", "beta")
	run.Env = append(os.Environ(), "HOME="+tempHome)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("Wrapper failed: %v\n%s", err, output)
	}
}

func waitForWrapperFallbackRecord(t *testing.T, config *core.Config) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if wrapperFallbackRecorded(t, config) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Timed out waiting for wrapper fallback to record execution")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func wrapperFallbackRecorded(t *testing.T, config *core.Config) bool {
	t.Helper()

	store, err := storage.NewJSONStorage(config)
	if err != nil {
		return false
	}
	executions, queryErr := store.GetExecutions(storage.QueryOptions{Tool: "test-tool"})
	if closeErr := store.Close(); closeErr != nil {
		t.Fatalf("Failed to close storage: %v", closeErr)
	}
	if queryErr != nil {
		t.Fatalf("Failed to query storage: %v", queryErr)
	}
	if len(executions) == 0 {
		return false
	}
	assertRecordedArgs(t, executions[0])
	return true
}

func TestProcessMonitorInstallWrapper(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := t.TempDir()
	monitor := installWrapperMonitor(t, tmpDir, homeDir)

	if err := monitor.InstallWrapper(); err != nil {
		t.Fatalf("InstallWrapper failed: %v", err)
	}
	assertInstalledProcessWrapper(t, monitor.wrapperPath)
}

func installWrapperMonitor(t *testing.T, tmpDir, homeDir string) *ProcessMonitor {
	t.Helper()

	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = tmpDir
	config.Monitoring.Process.AutoInstallWrappers = false
	monitor := NewProcessMonitor("testtool", "/usr/bin/testtool")
	monitor.config = config
	monitor.wrapperPath = filepath.Join(tmpDir, "testtool")
	monitor.originalPath = "/usr/bin/testtool"
	monitor.homeDir = homeDir
	return monitor
}

func assertInstalledProcessWrapper(t *testing.T, wrapperPath string) {
	t.Helper()

	if _, err := os.Stat(wrapperPath); os.IsNotExist(err) {
		t.Error("Wrapper script not created")
	}
	content, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("Failed to read wrapper: %v", err)
	}
	if !strings.Contains(string(content), "#!/bin/bash") {
		t.Error("Wrapper should be a bash script")
	}
	info, _ := os.Stat(wrapperPath)
	if info.Mode()&core.ExecutableModeMask == 0 {
		t.Error("Wrapper should be executable")
	}
}

func TestProcessMonitorFindOriginalBinary(t *testing.T) {
	tmpDir := t.TempDir()
	monitor := processMonitorWithWrapperDir("ls", "ls", tmpDir)
	original, err := monitor.findOriginalBinary()
	if err != nil {
		t.Skip("ls not found in PATH")
	}
	assertOriginalBinarySkipsWrapperDir(t, original, filepath.Join(tmpDir, "ls"))
}

func processMonitorWithWrapperDir(name, command, wrapperDir string) *ProcessMonitor {
	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = wrapperDir
	monitor := NewProcessMonitor(name, command)
	monitor.config = config
	return monitor
}

func assertOriginalBinarySkipsWrapperDir(t *testing.T, original, wrapperPath string) {
	t.Helper()

	if original == wrapperPath {
		t.Error("Should not find wrapper dir in original binary search")
	}
	if !strings.Contains(original, "ls") {
		t.Errorf("Original should contain 'ls', got %s", original)
	}
}

func TestProcessMonitorFindOriginalBinarySkipsWrapperDir(t *testing.T) {
	tmpDir := t.TempDir()
	wrapperBinary := filepath.Join(tmpDir, "mytool")
	writeWrapperBinary(t, wrapperBinary)
	monitor := processMonitorWithWrapperDir("mytool", "mytool", tmpDir)

	original, err := monitor.findOriginalBinary()
	if err != nil {
		return
	}

	if original == wrapperBinary {
		t.Error("Should not return wrapper directory binary as original")
	}
}

func writeWrapperBinary(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("#!/bin/bash"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create wrapper: %v", err)
	}
	if err := os.Chmod(path, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark wrapper executable: %v", err)
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
	writeExecutableScript(t, binaryPath)
	monitor := processMonitorWithWrapperDir("mytool", binaryPath, wrapperDir)

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
	wrapperDir, wrapperPath := wrapperDirWithBinary(t)
	symlinkPath := filepath.Join(t.TempDir(), "mytool")
	symlinkOrSkip(t, wrapperPath, symlinkPath)
	monitor := processMonitorWithWrapperDir("mytool", symlinkPath, wrapperDir)
	assertFindOriginalBinaryFails(t, monitor, "Expected symlink to wrapper path to be rejected")
}

func TestProcessMonitorFindOriginalBinarySkipsPathSymlinkToWrapperPath(t *testing.T) {
	wrapperDir, wrapperPath := wrapperDirWithBinary(t)
	pathDir := t.TempDir()
	symlinkPath := filepath.Join(pathDir, "mytool")
	symlinkOrSkip(t, wrapperPath, symlinkPath)
	t.Setenv("PATH", pathDir)

	monitor := processMonitorWithWrapperDir("mytool", "mytool", wrapperDir)
	assertFindOriginalBinaryFails(t, monitor, "Expected PATH symlink to wrapper path to be skipped")
}

func writeExecutableScript(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create binary: %v", err)
	}
	if err := os.Chmod(path, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark binary executable: %v", err)
	}
}

func wrapperDirWithBinary(t *testing.T) (string, string) {
	t.Helper()

	wrapperDir := t.TempDir()
	wrapperPath := filepath.Join(wrapperDir, "mytool")
	writeExecutableScript(t, wrapperPath)
	return wrapperDir, wrapperPath
}

func symlinkOrSkip(t *testing.T, oldname, newname string) {
	t.Helper()

	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("Symlinks are not available: %v", err)
	}
}

func assertFindOriginalBinaryFails(t *testing.T, monitor *ProcessMonitor, message string) {
	t.Helper()

	if _, err := monitor.findOriginalBinary(); err == nil {
		t.Fatal(message)
	}
}

func TestProcessMonitorFindOriginalBinarySkipsResolvedWrapperDirFromPath(t *testing.T) {
	realWrapperDir, _ := wrapperDirWithBinary(t)
	wrapperAlias := filepath.Join(t.TempDir(), "wrappers")
	symlinkOrSkip(t, realWrapperDir, wrapperAlias)
	t.Setenv("PATH", realWrapperDir)

	monitor := processMonitorWithWrapperDir("mytool", "mytool", wrapperAlias)
	assertFindOriginalBinaryFails(t, monitor, "Expected resolved wrapper directory candidate to be skipped")
}

func TestPathWithinDirectory(t *testing.T) {
	parent := t.TempDir()
	childPath := writeChildExecutable(t, parent, "child")

	assertPathWithinDirectory(t, childPath, parent)
	assertPathWithinDirectory(t, parent, parent)
	assertPathOutsideDirectory(t, filepath.Join(t.TempDir(), "tool"), parent)
	assertPathOutsideDirectory(t, "", parent)
	assertPathOutsideDirectory(t, childPath, "")
}

func TestPathWithinDirectoryRelativePaths(t *testing.T) {
	changeWorkingDirectory(t, t.TempDir())
	childPath := writeChildExecutable(t, "root", "child")

	assertPathWithinDirectory(t, childPath, "root")
}

func writeChildExecutable(t *testing.T, parent, child string) string {
	t.Helper()

	childDir := filepath.Join(parent, child)
	if err := os.MkdirAll(childDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create child dir: %v", err)
	}
	childPath := filepath.Join(childDir, "tool")
	writeExecutableScript(t, childPath)
	return childPath
}

func changeWorkingDirectory(t *testing.T, workingDir string) {
	t.Helper()

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
}

func assertPathWithinDirectory(t *testing.T, path, directory string) {
	t.Helper()

	if !pathWithinDirectory(path, directory) {
		t.Fatalf("Expected %q to be within %q", path, directory)
	}
}

func assertPathOutsideDirectory(t *testing.T, path, directory string) {
	t.Helper()

	if pathWithinDirectory(path, directory) {
		t.Fatalf("Expected %q to be outside %q", path, directory)
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
	binaryPath := writeFailingTestTool(t)
	monitor := NewProcessMonitor("testtool", binaryPath)
	monitor.originalPath = binaryPath

	record, err := monitor.ExecuteAndTrack("testtool", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("ExecuteAndTrack failed: %v", err)
	}
	assertTrackedProcessRecord(t, record)
}

func writeFailingTestTool(t *testing.T) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "testtool")
	content := []byte("#!/bin/bash\nexit 7\n")
	if err := os.WriteFile(binaryPath, content, core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write binary: %v", err)
	}
	if err := os.Chmod(binaryPath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to chmod binary: %v", err)
	}
	return binaryPath
}

func assertTrackedProcessRecord(t *testing.T, record *core.ExecutionRecord) {
	t.Helper()

	if record.Tool != "testtool" {
		t.Fatalf("Tool = %s, want testtool", record.Tool)
	}
	if record.Command != "testtool alpha beta" {
		t.Fatalf("Command = %q, want testtool alpha beta", record.Command)
	}
	if record.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", record.ExitCode)
	}
	assertRecordedArgs(t, record)
}

func TestProcessMonitorUpdateShellConfig(t *testing.T) {
	homeDir := t.TempDir()
	zshrc, fishConfig := writeShellConfigs(t, homeDir)
	wrapperDir := filepath.Join(homeDir, "wrap$dir\"with`chars")
	monitor := shellConfigMonitor(homeDir, wrapperDir)

	assertShellConfigUpdatesTwice(t, monitor)
	assertPosixPathLineOnce(t, zshrc, wrapperDir)
	assertFishPathLineOnce(t, fishConfig, wrapperDir)
}

func writeShellConfigs(t *testing.T, homeDir string) (string, string) {
	t.Helper()

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
	return zshrc, fishConfig
}

func shellConfigMonitor(homeDir, wrapperDir string) *ProcessMonitor {
	config := core.DefaultConfig()
	config.Monitoring.Process.WrapperDir = wrapperDir
	monitor := NewProcessMonitor("testtool", "testtool")
	monitor.config = config
	monitor.homeDir = homeDir
	return monitor
}

func assertShellConfigUpdatesTwice(t *testing.T, monitor *ProcessMonitor) {
	t.Helper()

	if err := monitor.updateShellConfig(); err != nil {
		t.Fatalf("updateShellConfig failed: %v", err)
	}
	if err := monitor.updateShellConfig(); err != nil {
		t.Fatalf("second updateShellConfig failed: %v", err)
	}
}

func assertPosixPathLineOnce(t *testing.T, zshrc, wrapperDir string) {
	t.Helper()

	content := readShellConfig(t, zshrc)
	exportLine := core.PosixPathLine(wrapperDir)
	if strings.Count(content, exportLine) != 1 {
		t.Fatalf("shell config content = %q, want one export line", content)
	}
}

func assertFishPathLineOnce(t *testing.T, fishConfig, wrapperDir string) {
	t.Helper()

	content := readShellConfig(t, fishConfig)
	fishLine := core.FishPathLine(wrapperDir)
	if strings.Count(content, fishLine) != 1 {
		t.Fatalf("fish config content = %q, want one fish path line", content)
	}
	if strings.Contains(content, "export PATH=") {
		t.Fatalf("fish config content = %q, should not use POSIX export", content)
	}
}

func readShellConfig(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read shell config: %v", err)
	}
	return string(content)
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
	executablePath := writeExecutablePath(t, tempDir)
	nonExecutablePath := writeNonExecutablePath(t, tempDir)

	assertValidateExecutablePathOK(t, executablePath)
	assertValidateExecutablePathRejectsBadPaths(t, tempDir, nonExecutablePath)
}

func writeExecutablePath(t *testing.T, tempDir string) string {
	t.Helper()

	executablePath := filepath.Join(tempDir, "tool")
	writeExecutableScript(t, executablePath)
	return executablePath
}

func writeNonExecutablePath(t *testing.T, tempDir string) string {
	t.Helper()

	nonExecutablePath := filepath.Join(tempDir, "notes.txt")
	if err := os.WriteFile(nonExecutablePath, []byte("notes"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write non-executable: %v", err)
	}
	return nonExecutablePath
}

func assertValidateExecutablePathOK(t *testing.T, executablePath string) {
	t.Helper()

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
}

func assertValidateExecutablePathRejectsBadPaths(t *testing.T, tempDir, nonExecutablePath string) {
	t.Helper()

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

func assertRecordedArgs(t *testing.T, execution *core.ExecutionRecord) {
	t.Helper()

	got := strings.Join(execution.Args, " ")
	if got != "alpha beta" {
		t.Fatalf("Recorded args = %q, want alpha beta", got)
	}
}
