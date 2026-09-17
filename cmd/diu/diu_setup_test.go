package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/yowainwright/diu/internal/core"
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
