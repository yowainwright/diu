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
	config, state := setupLaunchAgentTest(t)
	if err := writeLaunchAgent(config); err != nil {
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
	if err := writeLaunchAgent(config); err != nil {
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
	assertLaunchAgentContents(t, string(data), config)
	info, _ := os.Stat(path)
	if info.Mode().Perm() != core.PrivateFileMode {
		t.Fatalf("plist permissions = %v", info.Mode())
	}
}

func assertLaunchAgentContents(t *testing.T, data string, config *core.Config) {
	t.Helper()
	for _, expected := range []string{launchAgentLabel, "DIU_DAEMON_FOREGROUND", "RunAtLoad", "SuccessfulExit", plistString(os.Getenv("HOME"))} {
		if !strings.Contains(data, expected) {
			t.Fatalf("plist is missing %q", expected)
		}
	}
	if strings.Contains(data, plistString(config.Monitoring.Process.WrapperDir)) {
		t.Fatal("background PATH contains the wrapper directory")
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

func TestBackgroundPathExcludesWrappersAndRelativePaths(t *testing.T) {
	wrapper := filepath.Join(t.TempDir(), "wrappers")
	t.Setenv("PATH", strings.Join([]string{wrapper, "", ".", "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	got := backgroundToolPath(wrapper)
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
	marker := filepath.Join(dir, "escaped")
	script := filepath.Join(dir, "scan")
	writeExecutableForTest(t, script, "#!/bin/sh\n(sleep 1; echo escaped > \"$DIU_SCAN_MARKER\") &\nwait\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	env := append(os.Environ(), "DIU_SCAN_MARKER="+marker)
	if err := runInventoryScan(ctx, script, env); err == nil {
		t.Fatal("canceled scan succeeded")
	}
	time.Sleep(1100 * time.Millisecond)
	assertFileMissing(t, marker)
}
