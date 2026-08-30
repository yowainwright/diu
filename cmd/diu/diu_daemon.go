package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
	"github.com/yowainwright/diu/internal/dx"
)

type DaemonChecker func(config *core.Config) bool

var defaultDaemonChecker DaemonChecker = daemon.IsRunning
var daemonStopRequester = daemon.RequestStop

var daemonProcessStarter = func(execPath string, args []string, procAttr *syscall.ProcAttr) error {
	if _, err := syscall.ForkExec(execPath, args, procAttr); err != nil {
		return err
	}
	return nil
}

func SetDaemonChecker(checker DaemonChecker) func() {
	old := defaultDaemonChecker
	defaultDaemonChecker = checker
	return func() {
		defaultDaemonChecker = old
	}
}

func startDaemon(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return startDaemonWithConfig(config)
}

func startDaemonWithConfig(config *core.Config) error {
	if defaultDaemonChecker(config) {
		cliOutput().Status(dx.Info, "DIU daemon is already running")
		return nil
	}

	if os.Getenv("DIU_DAEMON_FOREGROUND") == "" {
		return forkDaemonBackground(config)
	}
	return runDaemonForeground(config)
}

func forkDaemonBackground(config *core.Config) error {
	out := cliOutput()
	activity := out.StartActivity("Starting DIU daemon")
	defer activity.Stop()

	if err := startDaemonProcess(activity); err != nil {
		return err
	}
	if err := waitForDaemonStarted(config, daemonStartTimeout); err != nil {
		return err
	}

	activity.Success("DIU daemon started")
	return nil
}

func startDaemonProcess(activity *dx.Activity) error {
	execPath, err := daemonExecutablePath()
	if err != nil {
		return err
	}
	devNull, err := openDaemonDevNull()
	if err != nil {
		return err
	}
	defer closeDaemonDevNull(devNull, activity)

	procAttr := daemonProcAttr(devNull)
	args := []string{execPath, "daemon", "start"}
	if err := daemonProcessStarter(execPath, args, procAttr); err != nil {
		return fmt.Errorf("failed to fork daemon: %w", err)
	}
	return nil
}

func daemonExecutablePath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	execPath, err = validateExecutablePath(execPath)
	if err != nil {
		return "", fmt.Errorf("invalid daemon executable path: %w", err)
	}
	return execPath, nil
}

func openDaemonDevNull() (*os.File, error) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", os.DevNull, err)
	}
	return devNull, nil
}

func closeDaemonDevNull(devNull *os.File, activity *dx.Activity) {
	if err := devNull.Close(); err != nil {
		activity.Notice(dx.Warning, fmt.Sprintf("failed to close %s: %v", os.DevNull, err))
	}
}

func daemonProcAttr(devNull *os.File) *syscall.ProcAttr {
	return &syscall.ProcAttr{
		Env:   append(os.Environ(), "DIU_DAEMON_FOREGROUND=1"),
		Files: []uintptr{devNull.Fd(), devNull.Fd(), devNull.Fd()},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	}
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

func stopDaemon(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	return stopDaemonWithConfig(config)
}

const daemonStartTimeout = 10 * time.Second

const daemonStartPollInterval = 100 * time.Millisecond

const daemonStopTimeout = 10 * time.Second

const daemonStopPollInterval = 100 * time.Millisecond

func stopDaemonWithConfig(config *core.Config) error {
	isRunning := defaultDaemonChecker(config)
	pid, pidErr := daemon.ReadPID(config)
	if daemonAlreadyStopped(isRunning, pidErr) {
		cliOutput().Status(dx.Info, "DIU daemon is not running")
		return nil
	}

	alreadyStopped, err := requestDaemonStop(config)
	stopRequestFinished := alreadyStopped || err != nil
	if stopRequestFinished {
		return err
	}
	return waitForDaemonStopActivity(config, pid, pidErr)
}

func waitForDaemonStopActivity(config *core.Config, pid int, pidErr error) error {
	out := cliOutput()
	activity := out.StartActivity("Stopping DIU daemon")
	defer activity.Stop()

	if err := waitForDaemonExit(config, pid, pidErr); err != nil {
		return err
	}

	activity.Success("DIU daemon stopped")
	return nil
}

func daemonAlreadyStopped(isRunning bool, pidErr error) bool {
	missingPIDFile := os.IsNotExist(pidErr)
	alreadyStopped := !isRunning && missingPIDFile
	return alreadyStopped
}

func requestDaemonStop(config *core.Config) (bool, error) {
	err := daemonStopRequester(config)
	if errors.Is(err, daemon.ErrNotRunning) {
		cliOutput().Status(dx.Info, "DIU daemon is not running")
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to stop daemon: %w", err)
	}
	return false, nil
}

func waitForDaemonExit(config *core.Config, pid int, pidErr error) error {
	if pidErr == nil {
		return waitForDaemonProcessStopped(pid, daemonStopTimeout)
	}
	return waitForDaemonStopped(config, daemonStopTimeout)
}

func waitForDaemonStarted(config *core.Config, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if defaultDaemonChecker(config) {
			return nil
		}
		time.Sleep(daemonStartPollInterval)
	}
	return fmt.Errorf("timed out after %s waiting for daemon to start", timeout)
}

func waitForDaemonStopped(config *core.Config, timeout time.Duration) error {
	pid, err := daemon.ReadPID(config)
	if err == nil {
		return waitForDaemonProcessStopped(pid, timeout)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !defaultDaemonChecker(config) {
			return nil
		}
		time.Sleep(daemonStopPollInterval)
	}
	return fmt.Errorf("timed out after %s waiting for daemon to stop", timeout)
}

func waitForDaemonProcessStopped(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !daemon.ProcessRunning(pid) {
			return nil
		}
		time.Sleep(daemonStopPollInterval)
	}
	return fmt.Errorf("timed out after %s waiting for daemon process %d to stop", timeout, pid)
}

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

func daemonStatus(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if defaultDaemonChecker(config) {
		out := cliOutput()
		out.Println(out.StyleData(dx.Success, "DIU daemon is running"))

		pidBytes, _ := os.ReadFile(config.Daemon.PIDFile)
		pid := strings.TrimSpace(string(pidBytes))
		out.Println(out.StyleData(dx.Muted, "  PID:"), pid)
	} else {
		cliOutput().Status(dx.Info, "DIU daemon is not running")
	}

	return nil
}
