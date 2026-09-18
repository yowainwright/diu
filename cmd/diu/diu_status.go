package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/storage"
)

type usageStatus struct {
	DaemonState        string     `json:"daemon_state"`
	StorageState       string     `json:"storage_state"`
	ExecutionCount     int        `json:"execution_count"`
	PackageCount       int        `json:"package_count"`
	LastActivity       *time.Time `json:"last_activity"`
	LastTool           string     `json:"last_tool"`
	LastLocation       string     `json:"last_location"`
	FallbackContention string     `json:"fallback_contention"`
	StoragePath        string     `json:"storage_path"`
	HistoryPath        string     `json:"history_path"`
	LogPath            string     `json:"log_path"`
	WrapperPath        string     `json:"wrapper_path"`
}

func showStatus(cmd *command, args []string) error {
	if err := validateReportFormat(cmd); err != nil {
		return err
	}
	config, err := core.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	status := collectUsageStatus(config)
	if flagString(cmd, "format") == formatJSON {
		return printJSON(status)
	}
	renderUsageStatus(status)
	return nil
}

func collectUsageStatus(config *core.Config) usageStatus {
	snapshot, storageErr := readLocalStorage(config.Storage.JSONFile)
	status := baseUsageStatus(config, snapshot.HasFile, storageErr)
	status.ExecutionCount = snapshot.ExecutionCount
	status.PackageCount = snapshot.PackageCount
	applyLatestUsage(&status, snapshot.LatestExecution)
	status.FallbackContention = fallbackContentionStatus(config.Daemon.DataDir)
	return status
}

func baseUsageStatus(config *core.Config, hasStorage bool, storageErr error) usageStatus {
	storageState := "not initialized"
	if hasStorage {
		storageState = "ready"
	}
	if storageErr != nil {
		storageState = "unreadable: " + storageErr.Error()
	}
	return usageStatus{
		DaemonState:  daemonState(config),
		StorageState: storageState,
		LastTool:     "none",
		LastLocation: "none",
		StoragePath:  config.Storage.JSONFile,
		HistoryPath:  storage.ExecutionLogPath(config.Storage.JSONFile),
		LogPath:      observability.LogPath(config.Daemon.DataDir),
		WrapperPath:  config.Monitoring.Process.WrapperDir,
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
	status.LastActivity = &latest.Timestamp
	status.LastTool = latest.Tool
	if latest.WorkingDir != "" {
		status.LastLocation = latest.WorkingDir
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
		out.StyleData(dx.Accent, "FIELD"),
		out.StyleData(dx.Accent, "VALUE"),
	}
	rows := usageStatusRows(out, status)
	out.Println(out.StyleData(dx.Accent, "DIU Status"))
	out.Println()
	out.Println(dx.Table(headers, rows))
}

func usageStatusRows(out *dx.Out, status usageStatus) [][]string {
	return [][]string{
		statusStateRow(out, "Daemon", status.DaemonState),
		statusStateRow(out, "Storage health", status.StorageState),
		statusRow(out, "Executions", dx.Info, strconv.Itoa(status.ExecutionCount)),
		statusRow(out, "Tracked packages", dx.Info, strconv.Itoa(status.PackageCount)),
		statusRow(out, "Last recorded", dx.Info, lastActivityLabel(status.LastActivity)),
		statusRow(out, "Last tool", dx.Accent, status.LastTool),
		statusRow(out, "Last location", dx.Accent, displayLocalPath(status.LastLocation)),
		statusStateRow(out, "Fallback contention", status.FallbackContention),
		statusRow(out, "Storage manifest", dx.Muted, displayLocalPath(status.StoragePath)),
		statusRow(out, "Execution history", dx.Muted, displayLocalPath(status.HistoryPath)),
		statusRow(out, "Logs", dx.Muted, displayLocalPath(status.LogPath)),
		statusRow(out, "Wrappers", dx.Muted, displayLocalPath(status.WrapperPath)),
	}
}

func lastActivityLabel(timestamp *time.Time) string {
	if timestamp == nil {
		return "never"
	}
	return timestamp.Local().Format("2006-01-02 15:04:05 MST")
}

func statusStateRow(out *dx.Out, label, value string) []string {
	return statusRow(out, label, stateTone(value), value)
}

func statusRow(out *dx.Out, label string, tone dx.Tone, value string) []string {
	styledLabel := out.StyleData(dx.Muted, label)
	styledValue := out.StyleData(tone, value)
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
