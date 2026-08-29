package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/storage"
)

type usageStatus struct {
	daemonState        string
	storageState       string
	executionCount     int
	packageCount       int
	lastActivity       string
	lastTool           string
	lastLocation       string
	fallbackContention string
	storagePath        string
	historyPath        string
	logPath            string
	wrapperPath        string
}

func showStatus(cmd *command, args []string) error {
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	status := collectUsageStatus(config)
	renderUsageStatus(status)
	return nil
}

func collectUsageStatus(config *core.Config) usageStatus {
	snapshot, storageErr := readLocalStorage(config.Storage.JSONFile)
	status := baseUsageStatus(config, snapshot.Exists, storageErr)
	status.executionCount = snapshot.ExecutionCount
	status.packageCount = snapshot.PackageCount
	applyLatestUsage(&status, snapshot.LatestExecution)
	status.fallbackContention = fallbackContentionStatus(config.Daemon.DataDir)
	return status
}

func baseUsageStatus(config *core.Config, storageExists bool, storageErr error) usageStatus {
	storageState := "not initialized"
	if storageExists {
		storageState = "ready"
	}
	if storageErr != nil {
		storageState = "unreadable: " + storageErr.Error()
	}
	return usageStatus{
		daemonState:  daemonState(config),
		storageState: storageState,
		lastActivity: "never",
		lastTool:     "none",
		lastLocation: "none",
		storagePath:  displayLocalPath(config.Storage.JSONFile),
		historyPath:  displayLocalPath(storage.ExecutionLogPath(config.Storage.JSONFile)),
		logPath:      displayLocalPath(observability.LogPath(config.Daemon.DataDir)),
		wrapperPath:  displayLocalPath(config.Monitoring.Process.WrapperDir),
	}
}

func daemonState(config *core.Config) string {
	if daemon.IsRunning(config) {
		return "running"
	}
	return "stopped"
}

func applyLatestUsage(status *usageStatus, latest *core.ExecutionRecord) {
	if latest == nil {
		return
	}
	status.lastActivity = latest.Timestamp.Local().Format("2006-01-02 15:04:05 MST")
	status.lastTool = latest.Tool
	if latest.WorkingDir != "" {
		status.lastLocation = displayLocalPath(latest.WorkingDir)
	}
}

func fallbackContentionStatus(dataDir string) string {
	last, detected, err := observability.ReadFallbackContention(dataDir)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	if !detected {
		return "none"
	}
	return "detected " + last.Local().Format("2006-01-02 15:04:05 MST")
}

func displayLocalPath(path string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if homeDir == "" {
		return path
	}
	return pathRelativeToHome(path, homeDir)
}

func pathRelativeToHome(path, homeDir string) string {
	cleanPath := filepath.Clean(path)
	cleanHome := filepath.Clean(homeDir)
	if cleanPath == cleanHome {
		return "~"
	}
	homePrefix := cleanHome + string(filepath.Separator)
	if strings.HasPrefix(cleanPath, homePrefix) {
		relativePath := strings.TrimPrefix(cleanPath, homePrefix)
		displayPath := "~" + string(filepath.Separator) + relativePath
		return displayPath
	}
	return path
}

func renderUsageStatus(status usageStatus) {
	out := cliOutput()
	headers := []string{
		out.DataStyle(dx.Accent, "FIELD"),
		out.DataStyle(dx.Accent, "VALUE"),
	}
	rows := usageStatusRows(out, status)
	out.Println(out.DataStyle(dx.Accent, "DIU Status"))
	out.Println()
	out.Println(dx.Table(headers, rows))
}

func usageStatusRows(out *dx.Out, status usageStatus) [][]string {
	return [][]string{
		statusStateRow(out, "Daemon", status.daemonState),
		statusStateRow(out, "Storage health", status.storageState),
		statusRow(out, "Executions", dx.Info, strconv.Itoa(status.executionCount)),
		statusRow(out, "Tracked packages", dx.Info, strconv.Itoa(status.packageCount)),
		statusRow(out, "Last recorded", dx.Info, status.lastActivity),
		statusRow(out, "Last tool", dx.Accent, status.lastTool),
		statusRow(out, "Last location", dx.Accent, status.lastLocation),
		statusStateRow(out, "Fallback contention", status.fallbackContention),
		statusRow(out, "Storage manifest", dx.Muted, status.storagePath),
		statusRow(out, "Execution history", dx.Muted, status.historyPath),
		statusRow(out, "Logs", dx.Muted, status.logPath),
		statusRow(out, "Wrappers", dx.Muted, status.wrapperPath),
	}
}

func statusStateRow(out *dx.Out, label, value string) []string {
	return statusRow(out, label, stateTone(value), value)
}

func statusRow(out *dx.Out, label string, tone dx.Tone, value string) []string {
	styledLabel := out.DataStyle(dx.Muted, label)
	styledValue := out.DataStyle(tone, value)
	return []string{styledLabel, styledValue}
}

func stateTone(value string) dx.Tone {
	switch value {
	case "running", "ready", "none":
		return dx.Success
	}
	if strings.HasPrefix(value, "unreadable") {
		return dx.Error
	}
	return dx.Warning
}
