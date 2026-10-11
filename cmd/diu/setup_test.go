package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
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

func TestSetupProjectReturnsSaveError(t *testing.T) {
	prepareReadOnlySetupConfig(t)

	err := setupProject(&command{}, nil)
	if err == nil {
		t.Fatal("Expected setupProject to fail")
	}
	if !strings.Contains(err.Error(), "failed to save config") {
		t.Fatalf("Expected save error, got: %v", err)
	}
}

func prepareReadOnlySetupConfig(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	configPath := filepath.Join(homeDir, ".config", "diu", "config.json")
	saveTestConfig(t, configPath)
	makeConfigReadOnly(t, configPath)
}

func saveTestConfig(t *testing.T, path string) {
	t.Helper()

	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = []string{}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{}
	if err := config.SaveTo(path); err != nil {
		t.Fatalf("Failed to save test config: %v", err)
	}
}

func makeConfigReadOnly(t *testing.T, path string) {
	t.Helper()

	if err := os.Chmod(path, 0400); err != nil {
		t.Fatalf("Failed to make config read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, core.PrivateFileMode)
	})
}

func TestBackup(t *testing.T) {
	config := setupTestHomeConfig(t)

	output := captureStderr(t, func() {
		if err := backup(&command{}, nil); err != nil {
			t.Fatalf("backup failed: %v", err)
		}
	})

	if !strings.Contains(output, "Backup created") {
		t.Fatalf("Expected 'Backup created', got: %q", output)
	}
	backups, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(backups) == 0 {
		t.Fatal("Expected backup file to be created")
	}
}

func TestCleanup(t *testing.T) {
	config := prepareCleanupConfig(t)

	output := captureStderr(t, func() {
		if err := cleanup(&command{}, nil); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}
	})

	if !strings.Contains(output, "Cleanup completed") {
		t.Fatalf("Expected 'Cleanup completed', got: %q", output)
	}
	assertCleanupKeptRecentExecution(t, config)
}

func prepareCleanupConfig(t *testing.T) *core.Config {
	t.Helper()

	setupTestHomeConfig(t)
	config, _ := core.LoadConfig("")
	config.Storage.RetentionDays = 1
	if err := config.Save(); err != nil {
		t.Fatalf("config.Save() failed: %v", err)
	}
	addCleanupExecutions(t, config)
	return config
}

func addCleanupExecutions(t *testing.T, config *core.Config) {
	t.Helper()

	store, _ := storage.NewJSONStorage(config)
	addCleanupExecution(t, store, "npm install old", time.Now().Add(-48*time.Hour))
	addCleanupExecution(t, store, "npm install current", time.Now())
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() failed: %v", err)
	}
}

func addCleanupExecution(t *testing.T, store storage.Storage, command string, timestamp time.Time) {
	t.Helper()

	err := store.AddExecution(&core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   command,
		Timestamp: timestamp,
	})
	if err != nil {
		t.Fatalf("AddExecution failed: %v", err)
	}
}

func assertCleanupKeptRecentExecution(t *testing.T, config *core.Config) {
	t.Helper()

	store, _ := storage.NewJSONStorage(config)
	defer func() {
		if err := store.Close(); err != nil {
			t.Logf("store.Close() failed: %v", err)
		}
	}()
	executions, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("Expected 1 execution after cleanup, got %d", len(executions))
	}
	if executions[0].Command != "npm install current" {
		t.Fatalf("Expected 'npm install current' to remain, got: %s", executions[0].Command)
	}
}

func addMaintenanceExecutionFixtures(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	oldTimestamp := time.Now().Add(-60 * 24 * time.Hour)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install old",
		Timestamp: oldTimestamp,
	})
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install current",
		Timestamp: time.Now(),
	})
	closeTestStore(t, store)
}

func assertBackupCommandCreatesFile(t *testing.T, config *core.Config) {
	t.Helper()

	backupOutput := captureStderr(t, func() {
		if err := backup(&command{}, nil); err != nil {
			t.Fatalf("backup failed: %v", err)
		}
	})
	if !strings.Contains(backupOutput, "Backup created") {
		t.Fatalf("Unexpected backup output: %q", backupOutput)
	}
	backups, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(backups) == 0 {
		t.Fatal("Expected backup file to be created")
	}
}

func assertCleanupKeepsCurrentExecution(t *testing.T, config *core.Config) {
	t.Helper()

	cleanupOutput := captureStderr(t, func() {
		if err := cleanup(&command{}, nil); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}
	})
	if !strings.Contains(cleanupOutput, "Cleanup completed") {
		t.Fatalf("Unexpected cleanup output: %q", cleanupOutput)
	}

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	executions, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	hasOneExecution := len(executions) == 1
	hasCurrentCommand := hasOneExecution && executions[0].Command == "npm install current"
	if !hasCurrentCommand {
		t.Fatalf("Unexpected executions after cleanup: %#v", executions)
	}
}

func setupTestHomeConfig(t *testing.T) *core.Config {
	t.Helper()
	stubBackgroundSetup(t)

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = []string{}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{}

	configPath := filepath.Join(homeDir, ".config", "diu", "config.json")
	if err := config.SaveTo(configPath); err != nil {
		t.Fatalf("Failed to save test config: %v", err)
	}
	return config
}

func setupLegacySetupHistory(t *testing.T) *core.Config {
	t.Helper()
	config := setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())
	requireConfigDirectories(t, config)
	record := core.ExecutionRecord{ID: "before-upgrade", Tool: core.ToolGo, Timestamp: time.Now()}
	writeLegacySetupHistory(t, config.Storage.JSONFile, []core.ExecutionRecord{record})
	if err := os.WriteFile(config.Daemon.PIDFile, []byte("999999999"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	return config
}

func writeLegacySetupHistory(t *testing.T, path string, records []core.ExecutionRecord) {
	t.Helper()
	legacy := core.StorageData{Version: "1.0.0", Executions: records}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
}

func readLegacySetupHistory(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func flushLegacySetupHistory(t *testing.T, path string) {
	t.Helper()
	data := readLegacySetupHistory(t, path)
	var legacy core.StorageData
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.ExecutionLogFormat != "" {
		t.Fatal("storage migrated before the old daemon drained")
	}
	record := core.ExecutionRecord{ID: "drained-on-stop", Tool: core.ToolGo, Timestamp: time.Now()}
	records := append(legacy.Executions, record)
	writeLegacySetupHistory(t, path, records)
}

func assertMigratedSetupHistory(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"before-upgrade", "drained-on-stop"}) {
		t.Fatalf("migrated history IDs = %v", ids)
	}
}

func requireConfigDirectories(t *testing.T, config *core.Config) {
	t.Helper()
	if err := config.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallProjectRemovesZshIntegrationAndPreservesData(t *testing.T) {
	config := setupTestHomeConfig(t)
	if err := config.EnsureDirectories(); err != nil {
		t.Fatal(err)
	}
	setupUninstallZshFixture(t, config)
	if err := os.WriteFile(config.Storage.JSONFile, []byte("usage data"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}

	output := captureStderr(t, func() {
		if err := uninstallProject(&command{}, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "configuration and usage data preserved") {
		t.Fatalf("uninstall output = %q", output)
	}

	assertZshSourceRemoved(t)
	assertGeneratedZshIntegrationRemoved(t, config)
	assertFileContents(t, config.Storage.JSONFile, "usage data")
}
