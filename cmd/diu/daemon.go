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

// startDaemon starts the DIU daemon
func startDaemon(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Check if already running
	if isRunning(config) {
		fmt.Println(infoStyle.Render("DIU daemon is already running"))
		return nil
	}

	d, err := daemon.NewDaemon(config)
	if err != nil {
		return fmt.Errorf("failed to create daemon: %w", err)
	}

	fmt.Println(successStyle.Render("Starting DIU daemon..."))

	// Fork to background
	if os.Getenv("DIU_DAEMON_FOREGROUND") == "" {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}
		execPath, err = validateExecutablePath(execPath)
		if err != nil {
			return fmt.Errorf("invalid daemon executable path: %w", err)
		}

		args := []string{execPath, "daemon", "start"}
		env := append(os.Environ(), "DIU_DAEMON_FOREGROUND=1")
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
			Env:   env,
			Files: []uintptr{devNull.Fd(), devNull.Fd(), devNull.Fd()},
			Sys: &syscall.SysProcAttr{
				Setsid: true,
			},
		}

		// #nosec G204 -- execPath is the current executable path and is validated before forking.
		_, err = syscall.ForkExec(execPath, args, procAttr)
		if err != nil {
			return fmt.Errorf("failed to fork daemon: %w", err)
		}

		time.Sleep(time.Second)
		fmt.Println(successStyle.Render("DIU daemon started"))
		return nil
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

	if !isRunning(config) {
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

	fmt.Println(successStyle.Render("DIU daemon stopped"))
	return nil
}

// restartDaemon restarts the DIU daemon
func restartDaemon(cmd *command, args []string) error {
	if err := stopDaemon(cmd, args); err != nil {
		return err
	}
	time.Sleep(time.Second)
	return startDaemon(cmd, args)
}

// daemonStatus checks and displays daemon status
func daemonStatus(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if isRunning(config) {
		fmt.Println(successStyle.Render("DIU daemon is running"))

		pidBytes, _ := os.ReadFile(config.Daemon.PIDFile)
		pid := strings.TrimSpace(string(pidBytes))
		fmt.Println(subtitleStyle.Render("  PID:"), pid)
	} else {
		fmt.Println(errorStyle.Render("DIU daemon is not running"))
	}

	return nil
}
