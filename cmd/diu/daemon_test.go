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
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

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
	child := "echo ready > \"$DIU_SCAN_READY\"\nwhile [ ! -e \"$DIU_SCAN_RELEASE\" ]; do sleep 0.001; done\necho escaped > \"$DIU_SCAN_MARKER\""
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
