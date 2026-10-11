package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
	"github.com/yowainwright/diu/internal/storage"
)

func TestSetupProjectInitializesStorageAndZshIntegration(t *testing.T) {
	config := setupTestHomeConfig(t)

	output := captureStderr(t, func() {
		if err := setupProject(&command{}, nil); err != nil {
			t.Fatalf("setupProject failed: %v", err)
		}
	})
	if !strings.Contains(output, "DIU setup completed") {
		t.Fatalf("Unexpected setup output: %q", output)
	}
	if _, err := os.Stat(config.Storage.JSONFile); err != nil {
		t.Fatalf("Expected storage file to exist: %v", err)
	}
	assertSetupZshIntegrationInstalled(t, config)
}

func TestSetupProjectIsRepeatable(t *testing.T) {
	config := setupTestHomeConfig(t)
	for range 2 {
		if err := setupProject(&command{}, nil); err != nil {
			t.Fatal(err)
		}
		assertSetupZshIntegrationInstalled(t, config)
	}
}

func TestSetupProjectSkipsUnavailableManagers(t *testing.T) {
	config := setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())
	config.Monitoring.EnabledTools = []string{core.ToolPoetry}
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
	if err := setupProject(&command{}, nil); err != nil {
		t.Fatalf("setupProject failed: %v", err)
	}
}

func assertSetupZshIntegrationInstalled(t *testing.T, config *core.Config) {
	t.Helper()
	if _, err := os.Stat(zshIntegrationPath(config)); err != nil {
		t.Fatalf("zsh integration file missing: %v", err)
	}
	assertSetupZshSourceBlock(t, config)
}

func assertSetupZshSourceBlock(t *testing.T, config *core.Config) {
	t.Helper()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(homeDir, ".zshrc"))
	if err != nil {
		t.Fatalf("zshrc missing: %v", err)
	}
	blockCount := strings.Count(string(contents), zshSourceBlockStart)
	if blockCount != 1 {
		t.Fatalf("zsh source block count = %d, want 1", blockCount)
	}
	sourceLine := "source " + zshSingleQuote(zshIntegrationPath(config))
	if !strings.Contains(string(contents), sourceLine) {
		t.Fatalf("zshrc is missing integration source line %q", sourceLine)
	}
}

func TestSetupScansInventoryBeforeEnablingBackgroundTracking(t *testing.T) {
	config := setupTestHomeConfig(t)
	binDir := configureExecutableInventoryScan(t, config)
	setupBackgroundTracking = func(*core.Config) error {
		assertScannedExecutablePackage(t, config, binDir)
		return nil
	}
	if err := setupProject(&command{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSetupDrainsLegacyDaemonBeforeMigratingHistory(t *testing.T) {
	config := setupLegacySetupHistory(t)
	t.Cleanup(SetDaemonChecker(isDaemonRunning))
	previous := daemonStopRequester
	t.Cleanup(func() { daemonStopRequester = previous })
	daemonStopRequester = func(*core.Config) error {
		flushLegacySetupHistory(t, config.Storage.JSONFile)
		return nil
	}
	setupBackgroundTracking = func(*core.Config) error {
		assertMigratedSetupHistory(t, config)
		return nil
	}
	if err := setupProject(&command{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSetupPreservesLegacyHistoryWhenDaemonStopFails(t *testing.T) {
	config := setupLegacySetupHistory(t)
	before := readLegacySetupHistory(t, config.Storage.JSONFile)
	stopErr := errors.New("daemon stop failed")
	t.Cleanup(SetDaemonChecker(isDaemonRunning))
	t.Cleanup(stubDaemonStopRequest(stopErr))
	setupBackgroundTracking = func(*core.Config) error {
		t.Fatal("setup started background tracking after stop failed")
		return nil
	}
	if err := setupProject(&command{}, nil); !errors.Is(err, stopErr) {
		t.Fatalf("setup error = %v, want %v", err, stopErr)
	}
	after := readLegacySetupHistory(t, config.Storage.JSONFile)
	if string(after) != string(before) {
		t.Fatal("setup changed legacy history after stop failed")
	}
	assertFileMissing(t, storage.ExecutionLogPath(config.Storage.JSONFile))
}

type setupRecorderState struct {
	isRunning bool
	starts    int
	startErr  error
}

func stubSetupRecorder(t *testing.T) *setupRecorderState {
	t.Helper()
	t.Setenv("DIU_DAEMON_FOREGROUND", "")
	state := &setupRecorderState{isRunning: true}
	t.Cleanup(SetDaemonChecker(func(*core.Config) bool { return state.isRunning }))
	oldStop, oldStart := daemonStopRequester, daemonProcessStarter
	t.Cleanup(func() { daemonStopRequester, daemonProcessStarter = oldStop, oldStart })
	daemonStopRequester = func(*core.Config) error {
		state.isRunning = false
		return nil
	}
	daemonProcessStarter = func(string, []string, *syscall.ProcAttr) error {
		state.starts++
		state.isRunning = state.startErr == nil
		return state.startErr
	}
	return state
}

func TestSetupRestoresRecorderAfterBackgroundFailure(t *testing.T) {
	setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())
	state := stubSetupRecorder(t)
	wantErr := errors.New("background setup failed")
	setupBackgroundTracking = func(*core.Config) error { return wantErr }
	err := setupProject(&command{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("setup error = %v, want %v", err, wantErr)
	}
	assertSetupRecorderRestored(t, state)
}

func assertSetupRecorderRestored(t *testing.T, state *setupRecorderState) {
	t.Helper()
	restored := state.isRunning && state.starts == 1
	if !restored {
		t.Fatalf("recorder = %+v, want running with one restart", state)
	}
}

func TestSetupFailurePreservesStoppedRecorder(t *testing.T) {
	setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())
	state := stubSetupRecorder(t)
	state.isRunning = false
	wantErr := errors.New("background setup failed")
	setupBackgroundTracking = func(*core.Config) error { return wantErr }
	err := setupProject(&command{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("setup error = %v, want %v", err, wantErr)
	}
	unexpectedStart := state.isRunning || state.starts != 0
	if unexpectedStart {
		t.Fatalf("setup started a previously stopped recorder: %+v", state)
	}
}

func TestSetupReturnsBothSetupAndRestoreErrors(t *testing.T) {
	setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())
	state := stubSetupRecorder(t)
	state.startErr = errors.New("recorder restart failed")
	setupErr := errors.New("background setup failed")
	setupBackgroundTracking = func(*core.Config) error { return setupErr }
	err := setupProject(&command{}, nil)
	if !errors.Is(err, setupErr) {
		t.Fatalf("missing original setup error: %v", err)
	}
	if !errors.Is(err, state.startErr) {
		t.Fatalf("missing restart error: %v", err)
	}
}

func TestSetupRestoresRecorderAfterStorageFailure(t *testing.T) {
	config := setupTestHomeConfig(t)
	requireConfigDirectories(t, config)
	if err := os.WriteFile(config.Storage.JSONFile, []byte("invalid JSON"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	state := stubSetupRecorder(t)
	if err := setupProject(&command{}, nil); err == nil {
		t.Fatal("setup accepted corrupt storage")
	}
	assertSetupRecorderRestored(t, state)
}

func TestSetupRestoresRecorderAfterZshIntegrationFailure(t *testing.T) {
	setupTestHomeConfig(t)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(homeDir, ".zshrc"), core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	state := stubSetupRecorder(t)
	if err := setupProject(&command{}, nil); err == nil {
		t.Fatal("setup accepted a directory in place of .zshrc")
	}
	assertSetupRecorderRestored(t, state)
}

func TestSuccessfulSetupDoesNotRestartRecorderTwice(t *testing.T) {
	setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())
	state := stubSetupRecorder(t)
	setupBackgroundTracking = func(*core.Config) error {
		state.isRunning = true
		return nil
	}
	if err := setupProject(&command{}, nil); err != nil {
		t.Fatal(err)
	}
	if state.starts != 0 {
		t.Fatalf("successful setup restarted the recorder %d extra times", state.starts)
	}
}

func TestSetupRestoresRecorderAfterPIDFallback(t *testing.T) {
	config := setupTestHomeConfig(t)
	writeSetupFallbackPID(t, config)
	state := stubSetupRecorder(t)
	state.isRunning = false
	wantErr := errors.New("background setup failed")
	setupBackgroundTracking = func(*core.Config) error { return wantErr }
	err := setupProject(&command{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("setup error = %v, want %v", err, wantErr)
	}
	assertSetupRecorderRestored(t, state)
}

func TestSetupDoesNotRestoreStalePID(t *testing.T) {
	config := setupTestHomeConfig(t)
	writeSetupFallbackPID(t, config)
	state := stubSetupRecorder(t)
	state.isRunning = false
	daemonStopRequester = func(*core.Config) error { return daemon.ErrNotRunning }
	wantErr := errors.New("background setup failed")
	setupBackgroundTracking = func(*core.Config) error { return wantErr }
	err := setupProject(&command{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("setup error = %v, want %v", err, wantErr)
	}
	if state.starts != 0 {
		t.Fatalf("stale PID caused %d recorder starts", state.starts)
	}
}

func writeSetupFallbackPID(t *testing.T, config *core.Config) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	requireConfigDirectories(t, config)
	if err := os.WriteFile(config.Daemon.PIDFile, []byte("999999999"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
}

func TestSetupRecoversAfterStopTimeout(t *testing.T) {
	const stopTimeout = 5 * time.Millisecond
	config, state := setupShutdownRecorderTest(t)
	writeSetupFallbackPID(t, config)
	daemonStopRequester = func(*core.Config) error { return nil }
	rejectSetupAfterStopTimeout(t)
	waitCalls := 0
	waitForExit := func(_ *core.Config, _ int, _ error, timeout time.Duration) error {
		waitCalls++
		if timeout != stopTimeout {
			t.Fatalf("stop timeout = %s, want %s", timeout, stopTimeout)
		}
		if waitCalls == 1 {
			return fmt.Errorf("timed out after %s waiting for daemon process", timeout)
		}
		return nil
	}
	err := setupProjectWithStopTimeout(stopTimeout, waitForExit)
	if err == nil {
		t.Fatal("setup should report the original stop timeout")
	}
	assertOutputContains(t, err.Error(), "timed out")
	assertSetupRecorderRestored(t, state)
	if waitCalls != 2 {
		t.Fatalf("daemon exit checks = %d, want 2", waitCalls)
	}
	assertFileMissing(t, storage.ExecutionLogPath(config.Storage.JSONFile))
}

func setupShutdownRecorderTest(t *testing.T) (*core.Config, *setupRecorderState) {
	t.Helper()
	config := setupTestHomeConfig(t)
	requireConfigDirectories(t, config)
	state := stubSetupRecorder(t)
	state.isRunning = false
	daemonStopRequester = daemon.RequestStop
	return config, state
}

func rejectSetupAfterStopTimeout(t *testing.T) {
	t.Helper()
	setupBackgroundTracking = func(*core.Config) error {
		t.Fatal("setup continued after the stop timeout")
		return nil
	}
}

func startSetupRecorderProcess(t *testing.T, pidPath, helper string) <-chan error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+helper+"$")
	cmd.Env = append(os.Environ(), "DIU_TEST_SLOW_RECORDER_PID="+pidPath)
	startAndWaitForRecorderReady(t, cmd)
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.Wait() }()
	return stopped
}

func TestSetupAbortsWhenRecorderIgnoresStop(t *testing.T) {
	const stopTimeout = 5 * time.Millisecond
	config, state := setupShutdownRecorderTest(t)
	startSetupRecorderProcess(t, config.Daemon.PIDFile, "TestHungSetupRecorderHelper")
	rejectSetupAfterStopTimeout(t)
	started := time.Now()
	err := setupProjectWithStopTimeout(stopTimeout, waitForDaemonExitWithTimeout)
	assertHungRecorderSetupError(t, err, time.Since(started), stopTimeout)
	if state.starts != 0 {
		t.Fatal("setup tried to restart while the original recorder was still alive")
	}
	assertFileMissing(t, config.Storage.JSONFile)
	assertFileMissing(t, storage.ExecutionLogPath(config.Storage.JSONFile))
	assertSetupRecorderStillAlive(t, config)
}

func assertSetupRecorderStillAlive(t *testing.T, config *core.Config) {
	t.Helper()
	pid, err := daemon.ReadPID(config)
	if err != nil {
		t.Fatal(err)
	}
	if !daemon.ProcessRunning(pid) {
		t.Fatal("setup killed the recorder instead of returning a bounded error")
	}
}

func assertHungRecorderSetupError(t *testing.T, err error, elapsed, stopTimeout time.Duration) {
	t.Helper()
	if err == nil {
		t.Fatal("setup accepted a recorder that ignored shutdown")
	}
	if elapsed > 3*stopTimeout+daemonStopPollInterval {
		t.Fatalf("setup exceeded its stop and recovery deadlines: %s", elapsed)
	}
	if !strings.Contains(err.Error(), "diu daemon start") {
		t.Fatalf("setup error has no recovery instructions: %v", err)
	}
}

func TestHungSetupRecorderHelper(t *testing.T) {
	pidPath := os.Getenv("DIU_TEST_SLOW_RECORDER_PID")
	if pidPath == "" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	lockSlowSetupRecorderPID(t, pidPath)
	fmt.Println("ready")
	time.Sleep(time.Minute)
}

func startAndWaitForRecorderReady(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	ready, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(ready)
	if _, err := reader.ReadString('\n'); err != nil {
		_ = cmd.Wait()
		t.Fatal(err)
	}
}

func lockSlowSetupRecorderPID(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, core.PrivateFileMode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		t.Fatal(err)
	}
}
