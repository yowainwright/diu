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
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
)

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

func TestSetupRestoresRecorderAfterWrapperFailure(t *testing.T) {
	config := setupTestHomeConfig(t)
	configureExecutableWrapperScan(t, config)
	requireConfigDirectories(t, config)
	wrapper := filepath.Join(config.Monitoring.Process.WrapperDir, "jq")
	if err := os.Mkdir(wrapper, core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	state := stubSetupRecorder(t)
	if err := setupProject(&command{}, nil); err == nil {
		t.Fatal("setup accepted a directory in place of a wrapper")
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

func TestSetupRecoversAfterSlowPIDFallbackStop(t *testing.T) {
	config := setupTestHomeConfig(t)
	requireConfigDirectories(t, config)
	t.Setenv("PATH", t.TempDir())
	state := stubSetupRecorder(t)
	state.isRunning = false
	daemonStopRequester = daemon.RequestStop
	stopped := startSlowSetupRecorder(t, config.Daemon.PIDFile)
	wantErr := errors.New("background setup failed after slow stop")
	setupBackgroundTracking = func(*core.Config) error { return wantErr }
	err := setupProject(&command{}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("setup error = %v, want %v", err, wantErr)
	}
	assertSetupRecorderRestored(t, state)
	if err := <-stopped; err != nil {
		t.Fatalf("recorder process failed: %v", err)
	}
}

func startSlowSetupRecorder(t *testing.T, pidPath string) <-chan error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSlowSetupRecorderHelper$")
	cmd.Env = append(os.Environ(), "DIU_TEST_SLOW_RECORDER_PID="+pidPath)
	startAndWaitForRecorderReady(t, cmd)
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.Wait() }()
	return stopped
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

func TestSlowSetupRecorderHelper(t *testing.T) {
	pidPath := os.Getenv("DIU_TEST_SLOW_RECORDER_PID")
	if pidPath == "" {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	lockSlowSetupRecorderPID(t, pidPath)
	fmt.Println("ready")
	<-signals
	time.Sleep(daemonStopTimeout + 2*daemonStopPollInterval)
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
