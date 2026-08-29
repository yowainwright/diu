package main

import (
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
		if err := showStatus(&command{}, nil); err != nil {
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
		if err := showStatus(&command{}, nil); err != nil {
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
		daemonState:        "running",
		storageState:       "unreadable: invalid JSON",
		lastActivity:       "never",
		lastTool:           "npm",
		lastLocation:       "~/projects/app",
		fallbackContention: "detected now",
	}
}

var coloredStatusExpectedOutput = []string{
	"\x1b[32mrunning\x1b[0m",
	"\x1b[31munreadable: invalid JSON\x1b[0m",
	"\x1b[33mdetected now\x1b[0m",
	"\x1b[1;36mnpm\x1b[0m",
}
