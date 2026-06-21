package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
)

// DaemonChecker is an interface for checking daemon status
type DaemonChecker interface {
	IsRunning(config *core.Config) bool
}

// RealDaemonChecker uses the actual daemon package
type RealDaemonChecker struct{}

func (RealDaemonChecker) IsRunning(config *core.Config) bool {
	return daemon.IsRunning(config)
}

// defaultDaemonChecker is used by default
var defaultDaemonChecker DaemonChecker = RealDaemonChecker{}

var daemonProcessStarter = func(execPath string, args []string, procAttr *syscall.ProcAttr) error {
	// #nosec G204 -- execPath is the current executable path and is validated before forking.
	if _, err := syscall.ForkExec(execPath, args, procAttr); err != nil {
		return err
	}
	return nil
}

// SetDaemonChecker sets a custom checker (for testing)
func SetDaemonChecker(checker DaemonChecker) func() {
	old := defaultDaemonChecker
	defaultDaemonChecker = checker
	return func() {
		defaultDaemonChecker = old
	}
}

// startDaemon starts the DIU daemon
func startDaemon(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return startDaemonWithConfig(config)
}

// startDaemonWithConfig starts the DIU daemon with the given config
func startDaemonWithConfig(config *core.Config) error {
	if defaultDaemonChecker.IsRunning(config) {
		fmt.Println(infoStyle.Render("DIU daemon is already running"))
		return nil
	}

	if os.Getenv("DIU_DAEMON_FOREGROUND") == "" {
		return forkDaemonBackground(config)
	}
	return runDaemonForeground(config)
}

func forkDaemonBackground(config *core.Config) error {
	fmt.Println(successStyle.Render("Starting DIU daemon..."))

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	execPath, err = validateExecutablePath(execPath)
	if err != nil {
		return fmt.Errorf("invalid daemon executable path: %w", err)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", os.DevNull, err)
	}
	defer func() {
		if err := devNull.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to close %s: %v\n", os.DevNull, err)
		}
	}()

	procAttr := &syscall.ProcAttr{
		Env:   append(os.Environ(), "DIU_DAEMON_FOREGROUND=1"),
		Files: []uintptr{devNull.Fd(), devNull.Fd(), devNull.Fd()},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	}

	if err := daemonProcessStarter(execPath, []string{execPath, "daemon", "start"}, procAttr); err != nil {
		return fmt.Errorf("failed to fork daemon: %w", err)
	}

	if err := waitForDaemonStarted(config, daemonStartTimeout); err != nil {
		return err
	}

	fmt.Println(successStyle.Render("DIU daemon started"))
	return nil
}

func runDaemonForeground(config *core.Config) error {
	d, err := daemon.NewDaemon(config)
	if err != nil {
		return fmt.Errorf("failed to create daemon: %w", err)
	}
	if err := d.Start(); err != nil {
		return err
	}
	d.Wait()
	return nil
}

// stopDaemon stops the DIU daemon
func stopDaemon(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return stopDaemonWithConfig(config)
}

// daemonStartTimeout is the maximum time to wait for the daemon PID check to pass after forking.
const daemonStartTimeout = 10 * time.Second

// daemonStartPollInterval is the interval between IsRunning checks while waiting for startup.
const daemonStartPollInterval = 100 * time.Millisecond

// daemonStopTimeout is the maximum time to wait for the daemon to exit after SIGTERM.
const daemonStopTimeout = 10 * time.Second

// daemonStopPollInterval is the interval between IsRunning checks while waiting for shutdown.
const daemonStopPollInterval = 100 * time.Millisecond

// stopDaemonWithConfig stops the DIU daemon with the given config
func stopDaemonWithConfig(config *core.Config) error {
	if !defaultDaemonChecker.IsRunning(config) {
		fmt.Println(infoStyle.Render("DIU daemon is not running"))
		return nil
	}

	pidBytes, err := os.ReadFile(config.Daemon.PIDFile)
	if err != nil {
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		return fmt.Errorf("invalid PID: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process not found: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
	}

	if err := waitForDaemonStopped(config, daemonStopTimeout); err != nil {
		return err
	}

	fmt.Println(successStyle.Render("DIU daemon stopped"))
	return nil
}

// waitForDaemonStarted polls IsRunning until the daemon starts or the timeout elapses.
func waitForDaemonStarted(config *core.Config, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if defaultDaemonChecker.IsRunning(config) {
			return nil
		}
		time.Sleep(daemonStartPollInterval)
	}
	return fmt.Errorf("timed out after %s waiting for daemon to start", timeout)
}

// waitForDaemonStopped polls IsRunning until the daemon exits or the timeout elapses.
func waitForDaemonStopped(config *core.Config, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !defaultDaemonChecker.IsRunning(config) {
			return nil
		}
		time.Sleep(daemonStopPollInterval)
	}
	return fmt.Errorf("timed out after %s waiting for daemon to stop", timeout)
}

// restartDaemon restarts the DIU daemon
func restartDaemon(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := stopDaemonWithConfig(config); err != nil {
		return err
	}
	return startDaemonWithConfig(config)
}

// daemonStatus checks and displays daemon status
func daemonStatus(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if defaultDaemonChecker.IsRunning(config) {
		fmt.Println(successStyle.Render("DIU daemon is running"))

		pidBytes, _ := os.ReadFile(config.Daemon.PIDFile)
		pid := strings.TrimSpace(string(pidBytes))
		fmt.Println(subtitleStyle.Render("  PID:"), pid)
	} else {
		fmt.Println(errorStyle.Render("DIU daemon is not running"))
	}

	return nil
}
