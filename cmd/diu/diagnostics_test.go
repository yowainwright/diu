package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/observability"
)

func TestCollectDiagnosticReportRedactsSensitiveData(t *testing.T) {
	config := setupTestHomeConfig(t)
	seedSensitiveDiagnosticData(t, config)
	report := collectDiagnosticReport(config)
	assertDiagnosticReportLocal(t, report)
	assertDiagnosticStorageCounts(t, report)
	output := marshalDiagnosticReport(t, report)
	assertDiagnosticSecretsRedacted(t, output, config)
}

func seedSensitiveDiagnosticData(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	addTestExecution(t, store, sensitiveDiagnosticExecution(config.Daemon.DataDir))
	pkg := &core.PackageInfo{Name: "secret-package", Tool: core.ToolNPM}
	if err := store.UpdatePackage(pkg); err != nil {
		t.Fatalf("UpdatePackage failed: %v", err)
	}
	closeTestStore(t, store)
	writeDiagnosticLog(t, config, "failed in "+config.Daemon.DataDir)
}

func assertDiagnosticReportLocal(t *testing.T, report diagnosticReport) {
	t.Helper()

	if !report.LocalOnly {
		t.Fatal("diagnostic report must be marked local-only")
	}
}

func assertDiagnosticStorageCounts(t *testing.T, report diagnosticReport) {
	t.Helper()

	hasExecutionCount := report.Storage.ExecutionCount == 1
	hasPackageCount := report.Storage.PackageCount == 1
	hasStorageCounts := hasExecutionCount && hasPackageCount
	if !hasStorageCounts {
		t.Fatalf("storage counts = %d, %d", report.Storage.ExecutionCount, report.Storage.PackageCount)
	}
}

func marshalDiagnosticReport(t *testing.T, report diagnosticReport) string {
	t.Helper()

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	return string(encoded)
}

func assertDiagnosticSecretsRedacted(t *testing.T, output string, config *core.Config) {
	t.Helper()

	for _, secret := range []string{config.Daemon.DataDir, "secret-package", "token=secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("diagnostic report leaked %q", secret)
		}
	}
	if !strings.Contains(output, "$DATA_DIR") {
		t.Fatalf("diagnostic report did not redact its data directory: %s", output)
	}
}

func TestDiagnosticsWritesPrivateReportFile(t *testing.T) {
	setupTestHomeConfig(t)
	outputPath := filepath.Join(t.TempDir(), "diu-diagnostics.json")
	statusOutput := writeDiagnosticsForTest(t, outputPath)
	if !strings.Contains(statusOutput, "Diagnostic report written") {
		t.Fatalf("status output = %q", statusOutput)
	}
	assertDiagnosticFileMode(t, outputPath)
	report := readDiagnosticReport(t, outputPath)
	assertDiagnosticReportMetadata(t, report)
}

func writeDiagnosticsForTest(t *testing.T, outputPath string) string {
	t.Helper()

	cmd := diagnosticsCommandForTest(t, "--output", outputPath)
	return captureStderr(t, func() {
		if err := diagnostics(cmd, nil); err != nil {
			t.Fatalf("diagnostics failed: %v", err)
		}
	})
}

func assertDiagnosticFileMode(t *testing.T, outputPath string) {
	t.Helper()

	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != core.PrivateFileMode {
		t.Fatalf("report mode = %v, want %v", info.Mode().Perm(), core.PrivateFileMode)
	}
}

func readDiagnosticReport(t *testing.T, outputPath string) diagnosticReport {
	t.Helper()

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var report diagnosticReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	return report
}

func assertDiagnosticReportMetadata(t *testing.T, report diagnosticReport) {
	t.Helper()

	hasSchemaVersion := report.SchemaVersion == diagnosticSchemaVersion
	hasMetadata := hasSchemaVersion && report.LocalOnly
	if !hasMetadata {
		t.Fatalf("diagnostic metadata = %d, %v", report.SchemaVersion, report.LocalOnly)
	}
}

func TestDiagnosticsRefusesManagedOutputPath(t *testing.T) {
	config := setupTestHomeConfig(t)
	report := collectDiagnosticReport(config)
	err := writeDiagnosticOutput(config, config.Storage.JSONFile, report)
	rejectedManagedPath := err != nil && strings.Contains(err.Error(), "DIU-managed file")
	if !rejectedManagedPath {
		t.Fatalf("writeDiagnosticOutput error = %v", err)
	}
}

func TestDiagnosticsDoesNotOverwriteExistingOutput(t *testing.T) {
	config := setupTestHomeConfig(t)
	outputPath := filepath.Join(t.TempDir(), "existing.json")
	if err := os.WriteFile(outputPath, []byte("keep me"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	err := writeDiagnosticOutput(config, outputPath, collectDiagnosticReport(config))
	if err == nil {
		t.Fatal("writeDiagnosticOutput overwrote an existing file")
	}
	data, readErr := os.ReadFile(outputPath)
	filePreserved := readErr == nil && string(data) == "keep me"
	if !filePreserved {
		t.Fatalf("existing output changed: %q, %v", data, readErr)
	}
}

func TestDiagnosticsReportsMalformedConfig(t *testing.T) {
	setupTestHomeConfig(t)
	configPath := filepath.Join(os.Getenv("HOME"), ".config", "diu", "config.json")
	if err := os.WriteFile(configPath, []byte("{invalid"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	output := captureStdout(t, func() {
		if err := diagnostics(diagnosticsCommandForTest(t), nil); err != nil {
			t.Fatalf("diagnostics failed: %v", err)
		}
	})
	var report diagnosticReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	hasWarning := len(report.Warnings) > 0
	hasConfigWarning := hasWarning && strings.HasPrefix(report.Warnings[0], "config: ")
	if !hasConfigWarning {
		t.Fatalf("diagnostic warnings = %#v", report.Warnings)
	}
}

func TestRemoveIncompleteDiagnosticOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "diagnostics.json")
	if err := os.WriteFile(path, nil, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	wantErr := errors.New("write failed")
	err := removeIncompleteDiagnosticOutput(path, wantErr)
	if !errors.Is(err, wantErr) {
		t.Fatalf("remove error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("incomplete output still exists: %v", err)
	}
}

func TestRemoveIncompleteDiagnosticOutputReportsCleanupFailure(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	if err := os.WriteFile(child, nil, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	wantErr := errors.New("write failed")
	err := removeIncompleteDiagnosticOutput(dir, wantErr)
	writeErrorPreserved := errors.Is(err, wantErr)
	cleanupErrorReported := strings.Contains(err.Error(), "failed to remove incomplete diagnostic report")
	reportedBothErrors := writeErrorPreserved && cleanupErrorReported
	if !reportedBothErrors {
		t.Fatalf("remove error = %v", err)
	}
}

func sensitiveDiagnosticExecution(dataDir string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		ID:         "sensitive-record",
		Tool:       core.ToolNPM,
		Command:    "token=secret",
		Args:       []string{"secret-package"},
		Timestamp:  time.Now(),
		WorkingDir: dataDir,
		User:       "private-user",
	}
}

func writeDiagnosticLog(t *testing.T, config *core.Config, message string) {
	t.Helper()
	logger, sink, err := observability.NewLocalLogger(config.Daemon.DataDir)
	if err != nil {
		t.Fatalf("NewLocalLogger failed: %v", err)
	}
	logger.Print(message)
	if err := sink.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func diagnosticsCommandForTest(t *testing.T, args ...string) *command {
	t.Helper()
	cmd := &command{}
	var output string
	cmd.Flags().StringVarP(&output, "output", "o", "", "output")
	parseTestFlags(t, cmd, args...)
	return cmd
}
