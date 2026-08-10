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
	if err := observability.MarkFallbackContention(config.Daemon.DataDir); err != nil {
		t.Fatalf("MarkFallbackContention failed: %v", err)
	}

	output := captureStdout(t, func() {
		if err := showStatus(&command{}, nil); err != nil {
			t.Fatalf("showStatus failed: %v", err)
		}
	})
	for _, expected := range []string{
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
		"~/.local/share/diu/diu.log",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("status output missing %q:\n%s", expected, output)
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
	if !strings.Contains(output, "not initialized") || !strings.Contains(output, "never") {
		t.Fatalf("status output = %q", output)
	}
}

func TestRenderUsageStatusUsesSemanticColors(t *testing.T) {
	t.Setenv("DIU_COLOR", "always")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	status := usageStatus{
		daemonState:        "running",
		storageState:       "unreadable: invalid JSON",
		lastActivity:       "never",
		lastTool:           "npm",
		lastLocation:       "~/projects/app",
		fallbackContention: "detected now",
	}
	output := captureStdout(t, func() {
		renderUsageStatus(status)
	})
	for _, expected := range []string{
		"\x1b[32mrunning\x1b[0m",
		"\x1b[31munreadable: invalid JSON\x1b[0m",
		"\x1b[33mdetected now\x1b[0m",
		"\x1b[1;36mnpm\x1b[0m",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("colored status output missing %q:\n%s", expected, output)
		}
	}
}
