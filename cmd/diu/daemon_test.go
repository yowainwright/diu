package main

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
)

func SetDaemonChecker(checker func(*core.Config) bool) func() {
	old := defaultDaemonChecker
	defaultDaemonChecker = checker
	return func() {
		defaultDaemonChecker = old
	}
}

func TestWaitForDaemonProcessStopped(t *testing.T) {
	deadPID := 999999999
	if err := waitForDaemonProcessStopped(deadPID, time.Second); err != nil {
		t.Fatalf("dead process wait failed: %v", err)
	}
	err := waitForDaemonProcessStopped(os.Getpid(), 0)
	hasWaitError := err != nil && strings.Contains(err.Error(), "waiting for daemon process")
	if !hasWaitError {
		t.Fatalf("live process wait error = %v", err)
	}
}

func TestLaunchAgentSetupAndRemoval(t *testing.T) {
	config, state := setupLaunchAgentTest(t)
	if err := installLaunchAgent(config); err != nil {
		t.Fatal(err)
	}
	assertLaunchAgentFile(t, config)
	if !state.isRunning {
		t.Fatal("setup did not start the login service")
	}
	if err := uninstallBackgroundTracking(); err != nil {
		t.Fatal(err)
	}
	path, _ := launchAgentPath()
	assertFileMissing(t, path)
	if state.isLoaded {
		t.Fatal("uninstall left the login service loaded")
	}
}

func TestLaunchAgentSetupIsRepeatable(t *testing.T) {
	config, state := setupLaunchAgentTest(t)
	for range 2 {
		if err := installLaunchAgent(config); err != nil {
			t.Fatal(err)
		}
	}
	if state.bootstraps != 2 {
		t.Fatalf("bootstrap count = %d, want 2", state.bootstraps)
	}
	if state.bootouts != 1 {
		t.Fatalf("bootout count = %d, want 1", state.bootouts)
	}
}

func TestCleanupRemovesLoginFileAfterBootoutFailure(t *testing.T) {
	_, state := setupLaunchAgentTest(t)
	if err := writeLaunchAgent(); err != nil {
		t.Fatal(err)
	}
	state.isLoaded = true
	stopErr := errors.New("bootout failed")
	launchctlCommand = failingBootoutCommand(state, stopErr)
	if err := uninstallBackgroundTracking(); !errors.Is(err, stopErr) {
		t.Fatalf("uninstall error = %v, want bootout failure", err)
	}
	path, _ := launchAgentPath()
	assertFileMissing(t, path)
}

func failingBootoutCommand(state *launchAgentTestState, stopErr error) func(...string) ([]byte, error) {
	return func(args ...string) ([]byte, error) {
		if args[0] == "bootout" {
			return nil, stopErr
		}
		return state.command(args...)
	}
}

func TestLaunchAgentReportsBootstrapFailure(t *testing.T) {
	config, state := setupLaunchAgentTest(t)
	state.bootstrapErr = errors.New("no GUI session")
	if err := installLaunchAgent(config); !errors.Is(err, state.bootstrapErr) {
		t.Fatalf("setup error = %v, want bootstrap failure", err)
	}
	if state.isRunning {
		t.Fatal("failed bootstrap reported a running service")
	}
}

func TestManagedDaemonStartUsesLaunchctl(t *testing.T) {
	config, state := setupLaunchAgentTest(t)
	if err := writeLaunchAgent(); err != nil {
		t.Fatal(err)
	}
	if err := startDaemonWithConfig(config); err != nil {
		t.Fatal(err)
	}
	if state.bootstraps != 1 {
		t.Fatal("managed daemon did not bootstrap through launchctl")
	}
}

type launchAgentTestState struct {
	isLoaded     bool
	isRunning    bool
	bootstraps   int
	bootouts     int
	bootstrapErr error
}

func setupLaunchAgentTest(t *testing.T) (*core.Config, *launchAgentTestState) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("LaunchAgents require macOS")
	}
	config := setupTestHomeConfig(t)
	state := &launchAgentTestState{}
	previous := launchctlCommand
	launchctlCommand = state.command
	t.Cleanup(func() { launchctlCommand = previous })
	restore := SetDaemonChecker(func(*core.Config) bool { return state.isRunning })
	t.Cleanup(restore)
	return config, state
}

func (s *launchAgentTestState) command(args ...string) ([]byte, error) {
	switch args[0] {
	case "print":
		return nil, s.printError()
	case "bootstrap":
		return nil, s.bootstrap()
	case "bootout":
		s.bootouts++
		s.isLoaded = false
		s.isRunning = false
	case "kickstart":
		s.isRunning = true
	}
	return nil, nil
}

func (s *launchAgentTestState) printError() error {
	if !s.isLoaded {
		return os.ErrNotExist
	}
	return nil
}

func (s *launchAgentTestState) bootstrap() error {
	if s.bootstrapErr != nil {
		return s.bootstrapErr
	}
	if s.isLoaded {
		return errors.New("service already loaded")
	}
	s.bootstraps++
	s.isLoaded = true
	return nil
}

func assertLaunchAgentFile(t *testing.T, config *core.Config) {
	t.Helper()
	path, _ := launchAgentPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("invalid plist: %v: %s", err, output)
	}
	assertLaunchAgentContents(t, string(data))
	info, _ := os.Stat(path)
	if info.Mode().Perm() != core.PrivateFileMode {
		t.Fatalf("plist permissions = %v", info.Mode())
	}
}

func assertLaunchAgentContents(t *testing.T, data string) {
	t.Helper()
	for _, expected := range []string{launchAgentLabel, "DIU_DAEMON_FOREGROUND", "RunAtLoad", "SuccessfulExit", plistString(os.Getenv("HOME"))} {
		if !strings.Contains(data, expected) {
			t.Fatalf("plist is missing %q", expected)
		}
	}
}

func TestInstalledDaemonExecutablePreservesStableSymlink(t *testing.T) {
	stable := symlinkCurrentExecutable(t)
	t.Setenv("PATH", filepath.Dir(stable))
	got, err := installedDaemonExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if got != stable {
		t.Fatalf("executable = %q, want stable path %q", got, stable)
	}
}

func TestDaemonExecutableThroughSymlink(t *testing.T) {
	if os.Getenv("DIU_TEST_EXECUTABLE_SYMLINK") == "1" {
		if _, err := daemonExecutablePath(); err != nil {
			t.Fatal(err)
		}
		return
	}
	stable := symlinkCurrentExecutable(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, stable, "-test.run=^TestDaemonExecutableThroughSymlink$")
	cmd.Env = append(os.Environ(), "DIU_TEST_EXECUTABLE_SYMLINK=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("daemon executable rejected symlink: %v: %s", err, output)
	}
}

func symlinkCurrentExecutable(t *testing.T) string {
	t.Helper()
	current, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(t.TempDir(), "diu")
	if err := os.Symlink(current, stable); err != nil {
		t.Fatal(err)
	}
	return stable
}

func TestBackgroundPathExcludesRelativePaths(t *testing.T) {
	t.Setenv("PATH", strings.Join([]string{"", ".", "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	got := backgroundToolPath()
	if got != "/usr/bin:/bin" {
		t.Fatalf("background PATH = %q", got)
	}
}

func TestBackgroundScanEnvironmentKeepsValuesAndDisablesTerminalEffects(t *testing.T) {
	t.Setenv("PATH", strings.Join([]string{".", "/usr/bin", "relative", "/bin"}, string(os.PathListSeparator)))
	t.Setenv("DIU_ACTIVITY", "always")
	t.Setenv("DIU_COLOR", "always")
	t.Setenv("DIU_TEST_SCAN_VALUE", "value=preserved")
	assertBackgroundScanEnvironment(t, backgroundScanEnvironment())
}

func scanEnvironmentValues(env []string) map[string]string {
	values := make(map[string]string)
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = value
	}
	return values
}

func assertBackgroundScanEnvironment(t *testing.T, env []string) {
	t.Helper()
	values := scanEnvironmentValues(env)
	want := map[string]string{
		"PATH":                "/usr/bin:/bin",
		"DIU_ACTIVITY":        "never",
		"DIU_COLOR":           "never",
		"DIU_TEST_SCAN_VALUE": "value=preserved",
	}
	for key, value := range want {
		if values[key] != value {
			t.Fatalf("background %s = %q, want %q", key, values[key], value)
		}
	}
}

func TestRefreshInventoryProcessHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := refreshInventoryProcess(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh error = %v, want cancellation", err)
	}
}

func TestPlistStringEscapesXML(t *testing.T) {
	want := "/path/with & <xml> and \"quotes\""
	var got string
	if err := xml.Unmarshal([]byte(plistString(want)), &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("plist value = %q, want %q", got, want)
	}
}

func TestInventoryScanCancellationStopsChildProcesses(t *testing.T) {
	dir := t.TempDir()
	marker, ready, release := inventoryCancellationPaths(dir)
	script := writeInventoryCancellationScript(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanDone := startInventoryCancellationScan(ctx, script, marker, ready, release)
	waitForFile(t, ready, time.Second)
	cancel()
	assertInventoryCancellation(t, scanDone, marker, release)
}

func inventoryCancellationPaths(dir string) (string, string, string) {
	return filepath.Join(dir, "escaped"), filepath.Join(dir, "ready"), filepath.Join(dir, "release")
}

func writeInventoryCancellationScript(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "scan")
	child := `echo ready > "$DIU_SCAN_READY"
while [ ! -e "$DIU_SCAN_RELEASE" ]; do sleep 0.001; done
echo escaped > "$DIU_SCAN_MARKER"`
	writeExecutableForTest(t, script, "#!/bin/sh\n(\n"+child+"\n) &\nwait\n")
	return script
}

func startInventoryCancellationScan(ctx context.Context, script, marker, ready, release string) <-chan error {
	env := append(os.Environ(), "DIU_SCAN_MARKER="+marker, "DIU_SCAN_READY="+ready, "DIU_SCAN_RELEASE="+release)
	scanDone := make(chan error, 1)
	go func() { scanDone <- runInventoryScan(ctx, script, env) }()
	return scanDone
}

func assertInventoryCancellation(t *testing.T, scanDone <-chan error, marker, release string) {
	t.Helper()
	if err := <-scanDone; err == nil {
		t.Fatal("canceled scan succeeded")
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	assertFileMissing(t, marker)
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q", path)
}

func TestDaemonStatusNotRunning(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStderr(t, func() {
		if err := daemonStatus(&command{}, nil); err != nil {
			t.Fatalf("daemonStatus error = %v", err)
		}
	})
	if !strings.Contains(output, "DIU daemon is not running") {
		t.Fatalf("daemonStatus output = %q", output)
	}
}

func isDaemonRunning(*core.Config) bool {
	return true
}

func isDaemonStopped(*core.Config) bool {
	return false
}

func TestStartDaemonAlreadyRunning(t *testing.T) {
	setupTestHomeConfig(t)

	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()

	output := captureStderr(t, func() {
		if err := startDaemon(&command{}, nil); err != nil {
			t.Fatalf("startDaemon failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU daemon is already running") {
		t.Fatalf("Expected 'already running' message, got: %q", output)
	}
}

func TestStartDaemonForegroundReportsInvalidStorage(t *testing.T) {
	t.Setenv("DIU_DAEMON_FOREGROUND", "1")
	config := core.DefaultConfig()
	config.Storage.JSONFile = ""
	config.Daemon.PIDFile = filepath.Join(t.TempDir(), "diu.pid")
	config.Daemon.SocketPath = filepath.Join(t.TempDir(), "diu.sock")
	restore := SetDaemonChecker(isDaemonStopped)
	defer restore()

	err := startDaemonWithConfig(config)
	hasCreateDaemonError := err != nil && strings.Contains(err.Error(), "failed to create daemon")
	if !hasCreateDaemonError {
		t.Fatalf("startDaemonWithConfig error = %v", err)
	}
}

func TestStopDaemonNotRunning(t *testing.T) {
	setupTestHomeConfig(t)

	restore := SetDaemonChecker(isDaemonStopped)
	defer restore()

	output := captureStderr(t, func() {
		if err := stopDaemon(&command{}, nil); err != nil {
			t.Fatalf("stopDaemon failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU daemon is not running") {
		t.Fatalf("Expected 'not running' message, got: %q", output)
	}
}

func TestDaemonStatusWithMockRunning(t *testing.T) {
	setupTestHomeConfig(t)
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()
	writeDaemonStatusPIDFile(t)

	output := captureStdout(t, func() {
		if err := daemonStatus(&command{}, nil); err != nil {
			t.Fatalf("daemonStatus failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU daemon is running") {
		t.Fatalf("Expected 'is running' message, got: %q", output)
	}
}

func writeDaemonStatusPIDFile(t *testing.T) {
	t.Helper()

	config, _ := core.LoadConfig("")
	pidFile := config.Daemon.PIDFile
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		t.Fatalf("Failed to create PID directory: %v", err)
	}
	if err := os.WriteFile(pidFile, []byte("12345\n"), 0o644); err != nil {
		t.Fatalf("Failed to write PID file: %v", err)
	}
	defer func() {
		removeErr := os.Remove(pidFile)
		shouldLogRemoveErr := removeErr != nil && !os.IsNotExist(removeErr)
		if shouldLogRemoveErr {
			t.Logf("Failed to remove PID file: %v", removeErr)
		}
	}()
}

type stoppingChecker struct {
	runningCalls   int
	stopAfterCalls int
}

func (s *stoppingChecker) IsRunning(_ *core.Config) bool {
	s.runningCalls++
	return s.runningCalls < s.stopAfterCalls
}

type startingChecker struct {
	runningCalls    int
	startAfterCalls int
}

func (s *startingChecker) IsRunning(_ *core.Config) bool {
	s.runningCalls++
	return s.runningCalls >= s.startAfterCalls
}

type alwaysRunningChecker struct {
	calls int
}

func (a *alwaysRunningChecker) IsRunning(_ *core.Config) bool {
	a.calls++
	return true
}

type sequenceDaemonChecker struct {
	states []bool
	calls  int
}

func (s *sequenceDaemonChecker) IsRunning(_ *core.Config) bool {
	if len(s.states) == 0 {
		return false
	}
	if s.calls >= len(s.states) {
		return s.states[len(s.states)-1]
	}
	state := s.states[s.calls]
	s.calls++
	return state
}

func TestWaitForDaemonStartedImmediate(t *testing.T) {
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()

	if err := waitForDaemonStarted(&core.Config{}, time.Second); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitForDaemonStartedAfterPolling(t *testing.T) {
	checker := &startingChecker{startAfterCalls: 3}
	restore := SetDaemonChecker(checker.IsRunning)
	defer restore()

	if err := waitForDaemonStartedWithPollInterval(&core.Config{}, time.Second, time.Millisecond); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if checker.runningCalls < 3 {
		t.Fatalf("expected at least 3 IsRunning calls, got %d", checker.runningCalls)
	}
}

func TestWaitForDaemonStartedTimeout(t *testing.T) {
	restore := SetDaemonChecker(isDaemonStopped)
	defer restore()

	err := waitForDaemonStartedWithPollInterval(&core.Config{}, 5*time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' in error, got %q", err.Error())
	}
}

func TestWaitForDaemonStoppedImmediate(t *testing.T) {
	restore := SetDaemonChecker(isDaemonStopped)
	defer restore()

	if err := waitForDaemonStopped(&core.Config{}, time.Second); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitForDaemonStoppedAfterPolling(t *testing.T) {
	checker := &stoppingChecker{stopAfterCalls: 3}
	restore := SetDaemonChecker(checker.IsRunning)
	defer restore()

	if err := waitForDaemonStoppedWithPollInterval(&core.Config{}, time.Second, time.Millisecond); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if checker.runningCalls < 3 {
		t.Fatalf("expected at least 3 IsRunning calls, got %d", checker.runningCalls)
	}
}

func TestWaitForDaemonStoppedTimeout(t *testing.T) {
	checker := &alwaysRunningChecker{}
	restore := SetDaemonChecker(checker.IsRunning)
	defer restore()

	err := waitForDaemonStoppedWithPollInterval(&core.Config{}, 5*time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' in error, got %q", err.Error())
	}
}

func TestRestartDaemonStopsThenStarts(t *testing.T) {
	setupTestHomeConfig(t)
	checker := &sequenceDaemonChecker{states: []bool{false, false, true}}
	restore := SetDaemonChecker(checker.IsRunning)
	defer restore()

	oldStarter := daemonProcessStarter
	daemonProcessStarter = func(string, []string, *syscall.ProcAttr) error {
		return nil
	}
	defer func() {
		daemonProcessStarter = oldStarter
	}()

	output := captureStderr(t, func() {
		if err := restartDaemon(&command{}, nil); err != nil {
			t.Fatalf("restartDaemon failed: %v", err)
		}
	})

	if !strings.Contains(output, "not running") {
		t.Fatalf("expected stop branch to short-circuit, got %q", output)
	}
	if !strings.Contains(output, "DIU daemon started") {
		t.Fatalf("expected start branch to complete, got %q", output)
	}
	if checker.calls < 3 {
		t.Fatalf("expected at least 3 IsRunning calls, got %d", checker.calls)
	}
}

func TestForkDaemonBackgroundStarterError(t *testing.T) {
	oldStarter := daemonProcessStarter
	daemonProcessStarter = func(string, []string, *syscall.ProcAttr) error {
		return errors.New("fork failed")
	}
	defer func() {
		daemonProcessStarter = oldStarter
	}()

	var err error
	captureStdout(t, func() {
		err = forkDaemonBackground(&core.Config{})
	})
	if err == nil {
		t.Fatal("expected fork error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to fork daemon") {
		t.Fatalf("expected fork failure, got %v", err)
	}
}

func TestStopDaemonWithConfigMissingPIDFile(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()
	requestErr := errors.New("failed to read daemon PID: file does not exist")
	restoreStop := stubDaemonStopRequest(requestErr)
	defer restoreStop()

	err := stopDaemonWithConfig(config)
	if err == nil {
		t.Fatal("expected missing PID file error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read daemon PID") {
		t.Fatalf("expected PID read error, got %v", err)
	}
}

func TestStopDaemonWithConfigInvalidPID(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()
	requestErr := errors.New("failed to read daemon PID: invalid PID")
	restoreStop := stubDaemonStopRequest(requestErr)
	defer restoreStop()

	writeInvalidDaemonPIDFile(t, config)

	err := stopDaemonWithConfig(config)
	if err == nil {
		t.Fatal("expected invalid PID error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid PID") {
		t.Fatalf("expected invalid PID error, got %v", err)
	}
}

func writeInvalidDaemonPIDFile(t *testing.T, config *core.Config) {
	t.Helper()

	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}
	if err := os.WriteFile(config.Daemon.PIDFile, []byte("not-a-pid"), core.PrivateFileMode); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
}

func TestStopDaemonWithConfigSignalError(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()
	requestErr := errors.New("socket unavailable")
	restoreStop := stubDaemonStopRequest(requestErr)
	defer restoreStop()
	writeDaemonPIDFile(t, config, "999999999")

	err := stopDaemonWithConfig(config)
	if err == nil {
		t.Fatal("expected signal error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to stop daemon") {
		t.Fatalf("expected signal error, got %v", err)
	}
}

func writeDaemonPIDFile(t *testing.T, config *core.Config, pid string) {
	t.Helper()

	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}
	if err := os.WriteFile(config.Daemon.PIDFile, []byte(pid), core.PrivateFileMode); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
}

func TestStopDaemonWithConfigRemovesStalePID(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(isDaemonStopped)
	defer restore()
	restoreStop := stubDaemonStopRequest(daemon.ErrNotRunning)
	defer restoreStop()

	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}
	if err := os.WriteFile(config.Daemon.PIDFile, []byte("4242"), core.PrivateFileMode); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	output := captureStderr(t, func() {
		if err := stopDaemonWithConfig(config); err != nil {
			t.Fatalf("stopDaemonWithConfig failed: %v", err)
		}
	})
	if !strings.Contains(output, "DIU daemon is not running") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func stubDaemonStopRequest(requestErr error) func() {
	old := daemonStopRequester
	daemonStopRequester = func(*core.Config) error {
		return requestErr
	}
	return func() {
		daemonStopRequester = old
	}
}

func stubBackgroundSetup(t *testing.T) {
	t.Helper()
	previous := setupBackgroundTracking
	setupBackgroundTracking = func(*core.Config) error { return nil }
	t.Cleanup(func() { setupBackgroundTracking = previous })
}
