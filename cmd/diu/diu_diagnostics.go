package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/daemon"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

const diagnosticSchemaVersion = 2

type diagnosticReport struct {
	SchemaVersion int                `json:"schema_version"`
	GeneratedAt   time.Time          `json:"generated_at"`
	IsLocalOnly   bool               `json:"local_only"`
	DIUVersion    string             `json:"diu_version"`
	Runtime       diagnosticRuntime  `json:"runtime"`
	Daemon        diagnosticDaemon   `json:"daemon"`
	Config        diagnosticConfig   `json:"config"`
	Storage       diagnosticStorage  `json:"storage"`
	Fallback      diagnosticFallback `json:"fallback"`
	RecentLogs    []string           `json:"recent_logs,omitempty"`
	Warnings      []string           `json:"warnings,omitempty"`
}

type diagnosticRuntime struct {
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

type diagnosticDaemon struct {
	IsRunning bool `json:"running"`
}

type diagnosticConfig struct {
	LogLevel        string   `json:"log_level"`
	EnabledTools    []string `json:"enabled_tools"`
	Monitoring      []string `json:"monitoring_methods"`
	IsBackupEnabled bool     `json:"backup_enabled"`
	BackupInterval  string   `json:"backup_interval"`
	RetentionDays   int      `json:"retention_days"`
	MaxExecutions   int      `json:"max_executions"`
	MaxStorageBytes int64    `json:"max_storage_bytes"`
	IsAPIEnabled    bool     `json:"api_enabled"`
	APIExposure     string   `json:"api_exposure"`
	APIPort         int      `json:"api_port"`
}

type diagnosticStorage struct {
	HasFile            bool   `json:"exists"`
	SizeBytes          int64  `json:"size_bytes,omitempty"`
	LastUpdated        string `json:"last_updated,omitempty"`
	ExecutionCount     int    `json:"execution_count"`
	PackageCount       int    `json:"package_count"`
	HasValidStatistics bool   `json:"statistics_valid"`
}

type diagnosticFallback struct {
	HasContention  bool   `json:"contention_detected"`
	LastContention string `json:"last_contention,omitempty"`
}

func diagnostics(cmd *command, args []string) error {
	config, configErr := core.LoadConfig("")
	if configErr != nil {
		config = core.DefaultConfig()
	}
	report := collectDiagnosticReport(config)
	if configErr != nil {
		replacements := diagnosticReplacements(config)
		warning := diagnosticWarning("config", configErr, replacements)
		report.Warnings = append([]string{warning}, report.Warnings...)
	}
	outputPath := flagString(cmd, "output")
	if outputPath != "" {
		return writeDiagnosticOutput(config, outputPath, report)
	}
	return encodeDiagnosticReport(cliOutput().Stdout(), report)
}

func encodeDiagnosticReport(writer io.Writer, report diagnosticReport) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("failed to encode diagnostics: %w", err)
	}
	return nil
}

func collectDiagnosticReport(config *core.Config) diagnosticReport {
	replacements := diagnosticReplacements(config)
	storageReport, storageErr := inspectDiagnosticStorage(config.Storage.JSONFile)
	logs, logErr := observability.ReadRecentLogs(config.Daemon.DataDir)
	fallbackReport, fallbackErr := inspectDiagnosticFallback(config.Daemon.DataDir)
	warnings := diagnosticWarnings(storageErr, logErr, fallbackErr, replacements)
	return diagnosticReport{
		SchemaVersion: diagnosticSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		IsLocalOnly:   true,
		DIUVersion:    versionString(),
		Runtime:       currentDiagnosticRuntime(),
		Daemon:        diagnosticDaemon{IsRunning: daemon.IsRunning(config)},
		Config:        diagnosticConfigFrom(config),
		Storage:       storageReport,
		Fallback:      fallbackReport,
		RecentLogs:    observability.RedactLines(logs, replacements),
		Warnings:      warnings,
	}
}

func currentDiagnosticRuntime() diagnosticRuntime {
	return diagnosticRuntime{
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

func diagnosticConfigFrom(config *core.Config) diagnosticConfig {
	return diagnosticConfig{
		LogLevel:        config.Daemon.LogLevel,
		EnabledTools:    append([]string(nil), config.Monitoring.EnabledTools...),
		Monitoring:      append([]string(nil), config.Monitoring.Methods...),
		IsBackupEnabled: config.Storage.IsBackupEnabled,
		BackupInterval:  config.Storage.BackupInterval.String(),
		RetentionDays:   config.Storage.RetentionDays,
		MaxExecutions:   config.Storage.MaxExecutions,
		MaxStorageBytes: config.Storage.MaxStorageBytes,
		IsAPIEnabled:    config.API.IsEnabled,
		APIExposure:     diagnosticAPIExposure(config.API.IsEnabled, config.API.Host),
		APIPort:         config.API.Port,
	}
}

func diagnosticAPIExposure(enabled bool, host string) string {
	if !enabled {
		return "disabled"
	}
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return "loopback"
	}
	return "network"
}

func inspectDiagnosticStorage(path string) (diagnosticStorage, error) {
	snapshot, err := readLocalStorage(path)
	if err != nil {
		return diagnosticStorage{}, err
	}
	if !snapshot.HasFile {
		return diagnosticStorage{}, nil
	}
	report := diagnosticStorage{HasFile: true, SizeBytes: snapshot.SizeBytes}
	if !snapshot.Metadata.LastUpdated.IsZero() {
		report.LastUpdated = snapshot.Metadata.LastUpdated.UTC().Format(time.RFC3339)
	}
	report.ExecutionCount = snapshot.ExecutionCount
	report.PackageCount = snapshot.PackageCount
	report.HasValidStatistics = snapshot.Statistics.TotalExecutions == snapshot.ExecutionCount
	return report, nil
}

func inspectDiagnosticFallback(dataDir string) (diagnosticFallback, error) {
	last, detected, err := observability.ReadFallbackContention(dataDir)
	if err != nil {
		return diagnosticFallback{}, err
	}
	if !detected {
		return diagnosticFallback{}, nil
	}
	return diagnosticFallback{
		HasContention:  true,
		LastContention: last.Format(time.RFC3339),
	}, nil
}

func diagnosticWarnings(storageErr, logErr, fallbackErr error, replacements map[string]string) []string {
	var warnings []string
	if storageErr != nil {
		message := observability.RedactText("storage: "+storageErr.Error(), replacements)
		warnings = append(warnings, message)
	}
	if logErr != nil {
		message := observability.RedactText("logs: "+logErr.Error(), replacements)
		warnings = append(warnings, message)
	}
	if fallbackErr != nil {
		message := observability.RedactText("fallback: "+fallbackErr.Error(), replacements)
		warnings = append(warnings, message)
	}
	return warnings
}

func diagnosticWarning(source string, err error, replacements map[string]string) string {
	message := source + ": " + err.Error()
	return observability.RedactText(message, replacements)
}

func writeDiagnosticOutput(config *core.Config, requestedPath string, report diagnosticReport) error {
	path, err := validateDiagnosticOutputPath(config, requestedPath)
	if err != nil {
		return err
	}
	file, err := safefs.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to create diagnostic report: %w", err)
	}
	encodeErr := encodeDiagnosticReport(file, report)
	writeErr := errors.Join(encodeErr, file.Close())
	if writeErr != nil {
		return removeIncompleteDiagnosticOutput(path, writeErr)
	}
	cliOutput().Status(dx.Success, "Diagnostic report written to "+path)
	return nil
}

func removeIncompleteDiagnosticOutput(path string, writeErr error) error {
	removeErr := os.Remove(path)
	if removeErr == nil {
		return writeErr
	}
	if os.IsNotExist(removeErr) {
		return writeErr
	}
	message := fmt.Errorf("failed to remove incomplete diagnostic report: %w", removeErr)
	return errors.Join(writeErr, message)
}

func validateDiagnosticOutputPath(config *core.Config, requestedPath string) (string, error) {
	trimmedPath := strings.TrimSpace(requestedPath)
	if trimmedPath == "" {
		return "", fmt.Errorf("diagnostic output path cannot be empty")
	}
	path, err := filepath.Abs(filepath.Clean(trimmedPath))
	if err != nil {
		return "", fmt.Errorf("invalid diagnostic output path: %w", err)
	}
	for _, managedPath := range diagnosticManagedPaths(config) {
		managedAbsolute, pathErr := filepath.Abs(filepath.Clean(managedPath))
		isManagedPath := pathErr == nil && path == managedAbsolute
		if isManagedPath {
			return "", fmt.Errorf("diagnostic output cannot overwrite DIU-managed file: %s", path)
		}
	}
	return path, nil
}

func diagnosticManagedPaths(config *core.Config) []string {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".config", "diu", "config.json")
	return []string{
		configPath,
		config.Storage.JSONFile,
		storage.ExecutionLogPath(config.Storage.JSONFile),
		config.Daemon.PIDFile,
		config.Daemon.SocketPath,
		observability.LogPath(config.Daemon.DataDir),
		observability.FallbackContentionPath(config.Daemon.DataDir),
	}
}

func diagnosticReplacements(config *core.Config) map[string]string {
	homeDir, _ := os.UserHomeDir()
	executionLogPath := storage.ExecutionLogPath(config.Storage.JSONFile)
	return map[string]string{
		config.Storage.JSONFile:              "$STORAGE_FILE",
		executionLogPath:                     "$EXECUTION_LOG",
		config.Monitoring.Process.WrapperDir: "$WRAPPER_DIR",
		config.Daemon.PIDFile:                "$PID_FILE",
		config.Daemon.SocketPath:             "$SOCKET_PATH",
		config.Daemon.DataDir:                "$DATA_DIR",
		homeDir:                              "$HOME",
	}
}
