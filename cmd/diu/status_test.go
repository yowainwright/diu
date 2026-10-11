package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/observability"
)

func TestShowStatusRendersUsageAndLocations(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	config := setupTestHomeConfig(t)
	recordStatusTestData(t, config)
	markFallbackContention(t, config)

	output := showStatusOutput(t)
	assertOutputContainsAll(t, output, statusLocationExpectedOutput, "status output")
}

var statusLocationExpectedOutput = []string{
	"DIU Status",
	"Executions",
	"Tracked packages",
	"Last tool",
	"npm",
	"Last location",
	"~/projects/app",
	"Fallback contention",
	"detected",
	"~/.local/share/diu/executions.json",
	"~/.local/share/diu/executions.ndjson",
	"~/.local/share/diu/diu.log",
}

func recordStatusTestData(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:       core.ToolHomebrew,
		Timestamp:  time.Now().Add(-time.Hour),
		WorkingDir: filepath.Join(os.Getenv("HOME"), "old-project"),
	})
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:       core.ToolNPM,
		Timestamp:  time.Now(),
		WorkingDir: filepath.Join(os.Getenv("HOME"), "projects", "app"),
	})
	updateTestPackage(t, store, &core.PackageInfo{Name: "eslint", Tool: core.ToolNPM})
	closeTestStore(t, store)
}

func markFallbackContention(t *testing.T, config *core.Config) {
	t.Helper()

	if err := observability.MarkFallbackContention(config.Daemon.DataDir); err != nil {
		t.Fatalf("MarkFallbackContention failed: %v", err)
	}
}

func showStatusOutput(t *testing.T) string {
	t.Helper()

	return captureStdout(t, func() {
		if err := showStatus(newStatusCommand(), nil); err != nil {
			t.Fatalf("showStatus failed: %v", err)
		}
	})
}

func assertOutputContainsAll(t *testing.T, output string, expectedValues []string, label string) {
	t.Helper()

	for _, expected := range expectedValues {
		if !strings.Contains(output, expected) {
			t.Fatalf("%s missing %q:\n%s", label, expected, output)
		}
	}
}

func TestShowStatusBeforeInitialization(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	setupTestHomeConfig(t)
	output := captureStdout(t, func() {
		if err := showStatus(newStatusCommand(), nil); err != nil {
			t.Fatalf("showStatus failed: %v", err)
		}
	})
	showsUninitialized := strings.Contains(output, "not initialized")
	showsNever := strings.Contains(output, "never")
	statusLooksEmpty := showsUninitialized && showsNever
	if !statusLooksEmpty {
		t.Fatalf("status output = %q", output)
	}
}

func TestRenderUsageStatusUsesSemanticColors(t *testing.T) {
	t.Setenv("DIU_COLOR", "always")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	output := captureStdout(t, func() {
		renderUsageStatus(coloredUsageStatus())
	})

	assertOutputContainsAll(t, output, coloredStatusExpectedOutput, "colored status output")
}

func coloredUsageStatus() usageStatus {
	return usageStatus{
		DaemonState:        "running",
		StorageState:       "unreadable: invalid JSON",
		LastTool:           "npm",
		LastLocation:       "~/projects/app",
		FallbackContention: "detected now",
	}
}

var coloredStatusExpectedOutput = []string{
	"\x1b[32mrunning\x1b[0m",
	"\x1b[31munreadable: invalid JSON\x1b[0m",
	"\x1b[33mdetected now\x1b[0m",
	"\x1b[1;36mnpm\x1b[0m",
}

func TestStatusJSONBeforeInitialization(t *testing.T) {
	setupTestHomeConfig(t)
	output := jsonCommandOutput(t, "status", "--format", "json")
	var report map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	assertJSONField(t, report, "storage_state", `"not initialized"`)
	assertJSONField(t, report, "last_activity", "null")
	assertJSONField(t, report, "execution_count", "0")
}

type jsonStatusResult struct {
	Count        int       `json:"execution_count"`
	Packages     int       `json:"package_count"`
	LastActivity time.Time `json:"last_activity"`
	LastTool     string    `json:"last_tool"`
	StoragePath  string    `json:"storage_path"`
	LastLocation string    `json:"last_location"`
}

func TestStatusJSONActivityAndPaths(t *testing.T) {
	config := setupTestHomeConfig(t)
	recordStatusTestData(t, config)
	output := jsonCommandOutput(t, "status", "-f", "json")
	var report jsonStatusResult
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	countsMatch := report.Count == 2 && report.Packages == 1
	valid := countsMatch && report.LastTool == core.ToolNPM
	if !valid {
		t.Fatalf("status = %#v", report)
	}
	assertJSONStatusPathsAndActivity(t, report, config.Storage.JSONFile)
}

func assertJSONStatusPathsAndActivity(t *testing.T, report jsonStatusResult, storagePath string) {
	t.Helper()
	absLocation := filepath.IsAbs(report.LastLocation)
	validPaths := report.StoragePath == storagePath && absLocation
	if !validPaths {
		t.Fatalf("paths = %#v", report)
	}
	if report.LastActivity.IsZero() {
		t.Fatal("missing last activity")
	}
}
