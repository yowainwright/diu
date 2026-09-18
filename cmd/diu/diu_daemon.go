package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/safefs"
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
		if launchAgentInstalled() {
			return startManagedDaemon(config)
		}
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
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve daemon executable: %w", err)
	}
	_, err = validateExecutablePath(resolved)
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
	d.SetInventoryRefresh(func(ctx context.Context) error { return refreshInventoryProcess(ctx, config) })
	if err := d.Start(); err != nil {
		return err
	}
	d.Wait()
	return d.Stop()
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
	_, err := stopDaemonWithState(config, waitForDaemonExit)
	return err
}

func stopDaemonWithState(config *core.Config, waitForExit func(*core.Config, int, error) error) (bool, error) {
	isRunning := defaultDaemonChecker(config)
	pid, pidErr := daemon.ReadPID(config)
	if daemonAlreadyStopped(isRunning, pidErr) {
		cliOutput().Status(dx.Info, "DIU daemon is not running")
		return false, nil
	}

	alreadyStopped, err := requestDaemonStop(config)
	stopRequestFinished := alreadyStopped || err != nil
	if stopRequestFinished {
		return false, err
	}
	return true, waitForDaemonStopActivity(config, pid, pidErr, waitForExit)
}

func waitForDaemonStopActivity(config *core.Config, pid int, pidErr error, waitForExit func(*core.Config, int, error) error) error {
	out := cliOutput()
	activity := out.StartActivity("Stopping DIU daemon")
	defer activity.Stop()

	if err := waitForExit(config, pid, pidErr); err != nil {
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

const launchAgentLabel = "io.github.yowainwright.diu"

var launchctlCommand = runLaunchctl

const launchAgentTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>io.github.yowainwright.diu</string>
<key>ProgramArguments</key><array>%s<string>daemon</string><string>start</string></array>
<key>EnvironmentVariables</key><dict>
<key>DIU_DAEMON_FOREGROUND</key><string>1</string>
<key>HOME</key>%s
<key>PATH</key>%s
</dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ProcessType</key><string>Background</string>
</dict></plist>
`

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
}

func launchAgentDomain() string {
	return "gui/" + fmt.Sprint(os.Getuid())
}

func launchAgentTarget() string {
	domain := launchAgentDomain()
	return fmt.Sprintf("%s/%s", domain, launchAgentLabel)
}

func launchAgentInstalled() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	path, err := launchAgentPath()
	if err != nil {
		return false
	}
	_, err = safefs.Lstat(path)
	return err == nil
}

func installLaunchAgent(config *core.Config) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if err := writeLaunchAgent(config); err != nil {
		return err
	}
	if err := unloadLaunchAgent(); err != nil {
		return err
	}
	if err := stopExistingDaemon(config); err != nil {
		return err
	}
	return startLaunchAgent(config)
}

func stopExistingDaemon(config *core.Config) error {
	_, pidErr := daemon.ReadPID(config)
	if daemonAlreadyStopped(defaultDaemonChecker(config), pidErr) {
		return nil
	}
	return stopDaemonWithConfig(config)
}

func startManagedDaemon(config *core.Config) error {
	if err := startLaunchAgent(config); err != nil {
		return err
	}
	cliOutput().Status(dx.Success, "DIU daemon started")
	return nil
}

func writeLaunchAgent(config *core.Config) error {
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	data, err := launchAgentPlist(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), core.OwnerDirectoryMode); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	if err := safefs.WriteFileAtomic(path, data, core.PrivateFileMode); err != nil {
		return fmt.Errorf("write DIU login service: %w", err)
	}
	return nil
}

func launchAgentPlist(config *core.Config) ([]byte, error) {
	executable, err := installedDaemonExecutable()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := backgroundToolPath(config.Monitoring.Process.WrapperDir)
	data := fmt.Sprintf(launchAgentTemplate, plistString(executable), plistString(home), plistString(path))
	return []byte(data), nil
}

func plistString(value string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	return fmt.Sprintf("<string>%s</string>", escaped.String())
}

func installedDaemonExecutable() (string, error) {
	current, err := daemonExecutablePath()
	if err != nil {
		return "", err
	}
	candidate, err := exec.LookPath("diu")
	if err != nil {
		return current, nil
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return current, nil
	}
	if sameExecutable(current, candidate) {
		return candidate, nil
	}
	return current, nil
}

func sameExecutable(first, second string) bool {
	firstInfo, err := os.Stat(first) // #nosec G703 -- Inspect caller-selected PATH entries for file identity; no file contents are read or written.
	if err != nil {
		return false
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		return false
	}
	return os.SameFile(firstInfo, secondInfo)
}

func backgroundToolPath(wrapperDir string) string {
	var paths []string
	for _, path := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(path) {
			continue
		}
		if filepath.Clean(path) == filepath.Clean(wrapperDir) {
			continue
		}
		paths = append(paths, path)
	}
	return strings.Join(paths, string(os.PathListSeparator))
}

func runLaunchctl(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), daemonStartTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/launchctl", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func startLaunchAgent(config *core.Config) error {
	if _, err := launchctlCommand("enable", launchAgentTarget()); err != nil {
		return err
	}
	if err := bootstrapLaunchAgent(); err != nil {
		return err
	}
	if _, err := launchctlCommand("kickstart", launchAgentTarget()); err != nil {
		return err
	}
	return waitForDaemonStarted(config, daemonStartTimeout)
}

func bootstrapLaunchAgent() error {
	if _, err := launchctlCommand("print", launchAgentTarget()); err == nil {
		return nil
	}
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	_, err = launchctlCommand("bootstrap", launchAgentDomain(), path)
	return err
}

func unloadLaunchAgent() error {
	if _, err := launchctlCommand("print", launchAgentTarget()); err != nil {
		return nil
	}
	_, err := launchctlCommand("bootout", launchAgentTarget())
	return err
}

func uninstallBackgroundTracking() error {
	config, err := core.LoadConfig("")
	if err != nil {
		return err
	}
	if launchAgentInstalled() {
		if err := removeLaunchAgent(); err != nil {
			return err
		}
	}
	return stopExistingDaemon(config)
}

func removeLaunchAgent() error {
	if err := unloadLaunchAgent(); err != nil {
		return err
	}
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func refreshInventoryProcess(parent context.Context, config *core.Config) error {
	executable, err := installedDaemonExecutable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, commandTimeout)
	defer cancel()
	return runInventoryScan(ctx, executable, backgroundScanEnvironment(config))
}

func runInventoryScan(ctx context.Context, executable string, env []string) error {
	cmd := exec.CommandContext(ctx, executable, "scan", "--refresh-wrappers")
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scan: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func backgroundScanEnvironment(config *core.Config) []string {
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "PATH=") {
			env = append(env, value)
		}
	}
	path := backgroundToolPath(config.Monitoring.Process.WrapperDir)
	return append(env, "PATH="+path, "DIU_ACTIVITY=never", "DIU_COLOR=never")
}
