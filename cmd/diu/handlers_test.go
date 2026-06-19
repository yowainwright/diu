package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

// =============================================================================
// Daemon Handler Tests
// =============================================================================

func TestDaemonStatusNotRunning(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := daemonStatus(&command{}, nil); err != nil {
			t.Fatalf("daemonStatus failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU daemon is not running") {
		t.Fatalf("Expected 'not running' message, got: %q", output)
	}
}

func TestDaemonStatusRunning(t *testing.T) {
	// This test would require mocking daemon.IsRunning to return true
	// For now, we test the not-running case
	// In a real scenario, we'd start the daemon first
	t.Skip("Skipping - requires running daemon")
}

// =============================================================================
// Query Handler Tests
// =============================================================================

func TestQueryExecutionsEmpty(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t), nil); err != nil {
			t.Fatalf("queryExecutions failed: %v", err)
		}
	})

	if !strings.Contains(output, "No executions found") {
		t.Fatalf("Expected 'No executions found', got: %q", output)
	}
}

func TestQueryExecutionsWithData(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolHomebrew,
		Command:   "brew install jq",
		Args:      []string{"install", "jq"},
		Timestamp: time.Now(),
		Duration:  1500 * time.Millisecond,
		ExitCode:  0,
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t, "--tool", "homebrew"), nil); err != nil {
			t.Fatalf("queryExecutions failed: %v", err)
		}
	})

	if !strings.Contains(output, "Execution History") {
		t.Fatalf("Expected 'Execution History', got: %q", output)
	}
	if !strings.Contains(output, "brew install jq") {
		t.Fatalf("Expected command in output, got: %q", output)
	}
}

func TestQueryExecutionsJSON(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install eslint",
		Args:      []string{"install", "eslint"},
		Timestamp: time.Now(),
		Duration:  2000 * time.Millisecond,
		ExitCode:  0,
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t, "--format", "json"), nil); err != nil {
			t.Fatalf("queryExecutions JSON failed: %v", err)
		}
	})

	var records []core.ExecutionRecord
	if err := json.Unmarshal([]byte(output), &records); err != nil {
		t.Fatalf("Failed to decode JSON output %q: %v", output, err)
	}
	if len(records) == 0 {
		t.Fatal("Expected at least one record in JSON output")
	}
	if records[0].Command != "npm install eslint" {
		t.Fatalf("Expected 'npm install eslint', got: %s", records[0].Command)
	}
}

func TestQueryExecutionsCSV(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolHomebrew,
		Command:   "brew upgrade",
		Args:      []string{"upgrade"},
		Timestamp: time.Now(),
		Duration:  3000 * time.Millisecond,
		ExitCode:  0,
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t, "--format", "csv"), nil); err != nil {
			t.Fatalf("queryExecutions CSV failed: %v", err)
		}
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		t.Fatalf("Expected header + data line, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "tool,command,timestamp,duration_ms,exit_code") {
		t.Fatalf("Expected CSV header, got: %s", lines[0])
	}
	if !strings.Contains(output, "brew upgrade") {
		t.Fatalf("Expected command in CSV output, got: %q", output)
	}
}

func TestQueryExecutionsWithTimeFilter(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	
	// Add old execution (2 days ago)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install old",
		Args:      []string{"install", "old"},
		Timestamp: time.Now().Add(-48 * time.Hour),
		Duration:  1000 * time.Millisecond,
		ExitCode:  0,
	})
	
	// Add recent execution
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install recent",
		Args:      []string{"install", "recent"},
		Timestamp: time.Now().Add(-1 * time.Hour),
		Duration:  1000 * time.Millisecond,
		ExitCode:  0,
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t, "--last", "24h"), nil); err != nil {
			t.Fatalf("queryExecutions with time filter failed: %v", err)
		}
	})

	if strings.Contains(output, "npm install old") {
		t.Fatal("Old execution should be filtered out")
	}
	if !strings.Contains(output, "npm install recent") {
		t.Fatal("Recent execution should be included")
	}
}

// =============================================================================
// Stats Handler Tests
// =============================================================================

func TestShowStatsEmpty(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := showStats(statsCommandForTest(t), nil); err != nil {
			t.Fatalf("showStats failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU Statistics") {
		t.Fatalf("Expected 'DIU Statistics', got: %q", output)
	}
	if !strings.Contains(output, "Total executions: 0") {
		t.Fatalf("Expected 'Total executions: 0', got: %q", output)
	}
}

func TestShowStatsWithData(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Command:          "npm install eslint",
		Args:             []string{"install", "eslint"},
		Timestamp:        time.Now(),
		Duration:         1500 * time.Millisecond,
		ExitCode:         0,
		PackagesAffected: []string{"eslint"},
	})
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:             core.ToolHomebrew,
		Command:          "brew install jq",
		Args:             []string{"install", "jq"},
		Timestamp:        time.Now().Add(-1 * time.Hour),
		Duration:         2000 * time.Millisecond,
		ExitCode:         0,
		PackagesAffected: []string{"jq"},
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := showStats(statsCommandForTest(t), nil); err != nil {
			t.Fatalf("showStats failed: %v", err)
		}
	})

	if !strings.Contains(output, "Total executions: 2") {
		t.Fatalf("Expected 'Total executions: 2', got: %q", output)
	}
	if !strings.Contains(output, "Tool usage:") {
		t.Fatalf("Expected 'Tool usage:' section, got: %q", output)
	}
}

func TestShowStatsDaily(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install test",
		Timestamp: time.Now().Add(-6 * time.Hour),
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := showStats(statsCommandForTest(t, "--daily"), nil); err != nil {
			t.Fatalf("showStats daily failed: %v", err)
		}
	})

	if !strings.Contains(output, "Last 24 Hours") {
		t.Fatalf("Expected 'Last 24 Hours' for daily stats, got: %q", output)
	}
}

func TestShowStatsWeekly(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install test",
		Timestamp: time.Now().Add(-2 * 24 * time.Hour),
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := showStats(statsCommandForTest(t, "--weekly"), nil); err != nil {
			t.Fatalf("showStats weekly failed: %v", err)
		}
	})

	if !strings.Contains(output, "Last 7 Days") {
		t.Fatalf("Expected 'Last 7 Days' for weekly stats, got: %q", output)
	}
}

// =============================================================================
// Package Handler Tests
// =============================================================================

func TestListPackagesEmpty(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := listPackages(packagesCommandForTest(t), nil); err != nil {
			t.Fatalf("listPackages failed: %v", err)
		}
	})

	if !strings.Contains(output, "No packages tracked") {
		t.Fatalf("Expected 'No packages tracked', got: %q", output)
	}
}

func TestListPackagesWithData(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "jq",
		Tool:       core.ToolHomebrew,
		Version:    "1.7",
		UsageCount: 5,
		LastUsed:   time.Now(),
		Path:       "/usr/local/bin/jq",
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := listPackages(packagesCommandForTest(t), nil); err != nil {
			t.Fatalf("listPackages failed: %v", err)
		}
	})

	if !strings.Contains(output, "Tracked Packages") {
		t.Fatalf("Expected 'Tracked Packages', got: %q", output)
	}
	if !strings.Contains(output, "jq") {
		t.Fatalf("Expected 'jq' in output, got: %q", output)
	}
}

func TestListPackagesWithToolFilter(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "jq",
		Tool:       core.ToolHomebrew,
		UsageCount: 5,
		LastUsed:   time.Now(),
	})
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "eslint",
		Tool:       core.ToolNPM,
		UsageCount: 3,
		LastUsed:   time.Now(),
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := listPackages(packagesCommandForTest(t, "--tool", "homebrew"), nil); err != nil {
			t.Fatalf("listPackages with tool filter failed: %v", err)
		}
	})

	if !strings.Contains(output, "jq") {
		t.Fatalf("Expected 'jq' for homebrew filter, got: %q", output)
	}
	if strings.Contains(output, "eslint") {
		t.Fatalf("Should not include npm packages when filtered by homebrew, got: %q", output)
	}
}

func TestListPackagesUnusedFilter(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "old",
		Tool:       core.ToolHomebrew,
		UsageCount: 1,
		LastUsed:   time.Now().Add(-100 * 24 * time.Hour),
	})
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "recent",
		Tool:       core.ToolHomebrew,
		UsageCount: 5,
		LastUsed:   time.Now(),
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := listPackages(packagesCommandForTest(t, "--unused", "30d"), nil); err != nil {
			t.Fatalf("listPackages with unused filter failed: %v", err)
		}
	})

	if strings.Contains(output, "recent") {
		t.Fatalf("Recent package should not be in unused list, got: %q", output)
	}
	if !strings.Contains(output, "No unused packages found") && !strings.Contains(output, "old") {
		t.Fatalf("Expected old package in unused list or no unused message, got: %q", output)
	}
}

func TestCheckPackagesWithSearch(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "jq",
		Tool:       core.ToolHomebrew,
		UsageCount: 5,
		LastUsed:   time.Now(),
	})
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "eslint",
		Tool:       core.ToolNPM,
		UsageCount: 3,
		LastUsed:   time.Now(),
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := checkPackages(checkCommandForTest(t, "--search", "jq", "--format", "json"), nil); err != nil {
			t.Fatalf("checkPackages with search failed: %v", err)
		}
	})

	var packages []core.PackageInfo
	if err := json.Unmarshal([]byte(output), &packages); err != nil {
		t.Fatalf("Failed to decode JSON output %q: %v", output, err)
	}
	if len(packages) != 1 {
		t.Fatalf("Expected 1 package matching 'jq', got %d", len(packages))
	}
	if packages[0].Name != "jq" {
		t.Fatalf("Expected 'jq', got: %s", packages[0].Name)
	}
}

// =============================================================================
// Config Handler Tests
// =============================================================================

func TestGetConfig(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := getConfig(&command{}, []string{"storage.retention_days"}); err != nil {
			t.Fatalf("getConfig failed: %v", err)
		}
	})

	if strings.TrimSpace(output) != fmt.Sprintf("%d", core.DefaultRetentionDays) {
		t.Fatalf("Expected retention_days value, got: %q", output)
	}
}

func TestGetConfigUnknownKey(t *testing.T) {
	setupTestHomeConfig(t)

	err := getConfig(&command{}, []string{"unknown.key"})
	if err == nil {
		t.Fatal("Expected error for unknown config key")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("Expected 'unknown config key' error, got: %v", err)
	}
}

func TestGetConfigNoKey(t *testing.T) {
	err := getConfig(&command{}, []string{})
	if err == nil {
		t.Fatal("Expected error for missing config key")
	}
	if !strings.Contains(err.Error(), "config key required") {
		t.Fatalf("Expected 'config key required' error, got: %v", err)
	}
}

func TestSetConfig(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := setConfig(&command{}, []string{"storage.retention_days", "30"}); err != nil {
			t.Fatalf("setConfig failed: %v", err)
		}
	})

	if !strings.Contains(output, "Configuration updated") {
		t.Fatalf("Expected 'Configuration updated', got: %q", output)
	}

	// Verify it was set
	getOutput := captureStdout(t, func() {
		if err := getConfig(&command{}, []string{"storage.retention_days"}); err != nil {
			t.Fatalf("getConfig after set failed: %v", err)
		}
	})
	if strings.TrimSpace(getOutput) != "30" {
		t.Fatalf("Expected '30', got: %q", getOutput)
	}
}

func TestSetConfigInvalidValue(t *testing.T) {
	err := setConfig(&command{}, []string{"storage.retention_days", "invalid"})
	if err == nil {
		t.Fatal("Expected error for invalid retention_days value")
	}
	if !strings.Contains(err.Error(), "invalid retention_days value") {
		t.Fatalf("Expected 'invalid retention_days value' error, got: %v", err)
	}
}

func TestSetConfigNegativeValue(t *testing.T) {
	err := setConfig(&command{}, []string{"storage.retention_days", "-1"})
	if err == nil {
		t.Fatal("Expected error for negative retention_days value")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("Expected 'non-negative' error, got: %v", err)
	}
}

func TestSetConfigNoArgs(t *testing.T) {
	err := setConfig(&command{}, []string{})
	if err == nil {
		t.Fatal("Expected error for missing args")
	}
	if !strings.Contains(err.Error(), "config key and value required") {
		t.Fatalf("Expected 'config key and value required' error, got: %v", err)
	}
}

func TestListConfig(t *testing.T) {
	config := setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := listConfig(&command{}, nil); err != nil {
			t.Fatalf("listConfig failed: %v", err)
		}
	})

	var listed core.Config
	if err := json.Unmarshal([]byte(output), &listed); err != nil {
		t.Fatalf("Failed to decode config JSON: %v", err)
	}
	if listed.Version != config.Version {
		t.Fatalf("Expected version %q, got %q", config.Version, listed.Version)
	}
}

// =============================================================================
// Setup Handler Tests
// =============================================================================

func TestSetupProject(t *testing.T) {
	config := setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := setupProject(&command{}, nil); err != nil {
			t.Fatalf("setupProject failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU setup completed") {
		t.Fatalf("Expected 'DIU setup completed', got: %q", output)
	}

	// Verify storage file was created
	if _, err := os.Stat(config.Storage.JSONFile); err != nil {
		t.Fatalf("Expected storage file to exist: %v", err)
	}
}

func TestScanPackages(t *testing.T) {
	config := setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())

	// Create a fake binary directory
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create bin dir: %v", err)
	}

	// Create a fake executable
	writeExecutableForTest(t, filepath.Join(binDir, "jq"), "#!/bin/bash\nexit 0\n")

	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolHomebrew: {binDir},
	}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	output := captureStdout(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})

	if !strings.Contains(output, "packages scanned") {
		t.Fatalf("Expected 'packages scanned' message, got: %q", output)
	}
}

func TestBackup(t *testing.T) {
	config := setupTestHomeConfig(t)

	output := captureStdout(t, func() {
		if err := backup(&command{}, nil); err != nil {
			t.Fatalf("backup failed: %v", err)
		}
	})

	if !strings.Contains(output, "Backup created") {
		t.Fatalf("Expected 'Backup created', got: %q", output)
	}

	// Verify backup file exists
	backups, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(backups) == 0 {
		t.Fatal("Expected backup file to be created")
	}
}

func TestCleanup(t *testing.T) {
	setupTestHomeConfig(t)
	config, _ := core.LoadConfig("")
	
	// Set retention to 1 day for this test
	config.Storage.RetentionDays = 1
	if err := config.Save(); err != nil {
		t.Fatalf("config.Save() failed: %v", err)
	}
	
	store, _ := storage.NewJSONStorage(config)
	
	// Add old execution (2 days ago - should be removed)
	if err := store.AddExecution(&core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install old",
		Timestamp: time.Now().Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("AddExecution failed: %v", err)
	}
	
	// Add recent execution (should be kept)
	if err := store.AddExecution(&core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install current",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("AddExecution failed: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() failed: %v", err)
	}

	output := captureStdout(t, func() {
		if err := cleanup(&command{}, nil); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}
	})

	if !strings.Contains(output, "Cleanup completed") {
		t.Fatalf("Expected 'Cleanup completed', got: %q", output)
	}

	// Verify old execution was removed
	store, _ = storage.NewJSONStorage(config)
	defer func() {
		if err := store.Close(); err != nil {
			t.Logf("store.Close() failed: %v", err)
		}
	}()
	executions, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("Expected 1 execution after cleanup, got %d", len(executions))
	}
	if executions[0].Command != "npm install current" {
		t.Fatalf("Expected 'npm install current' to remain, got: %s", executions[0].Command)
	}
}
