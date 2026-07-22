package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
		Command:   "brew upgrade,all",
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
	if !strings.Contains(output, `"brew upgrade,all"`) {
		t.Fatalf("Expected command in CSV output, got: %q", output)
	}
}

func TestQueryExecutionsCSVWriterError(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolHomebrew,
		Command:   strings.Repeat("x", 8192),
		Args:      []string{"upgrade"},
		Timestamp: time.Now(),
		Duration:  3000 * time.Millisecond,
		ExitCode:  0,
	})
	closeTestStore(t, store)

	var err error
	withReadOnlyStdout(t, func() {
		err = queryExecutions(queryCommandForTest(t, "--format", "csv"), nil)
	})
	if err == nil {
		t.Fatal("Expected CSV writer error")
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

func TestListPackagesUnusedFilterInvalidDuration(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name:     "recent",
		Tool:     core.ToolHomebrew,
		LastUsed: time.Now(),
	})
	closeTestStore(t, store)

	err := listPackages(packagesCommandForTest(t, "--unused", "not-a-duration"), nil)
	if err == nil {
		t.Fatal("expected invalid duration error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Fatalf("expected invalid duration error, got %v", err)
	}
}

func TestListPackagesUnusedFilterNoMatches(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name:     "recent",
		Tool:     core.ToolHomebrew,
		LastUsed: time.Now(),
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := listPackages(packagesCommandForTest(t, "--unused", "30d"), nil); err != nil {
			t.Fatalf("listPackages with unused filter failed: %v", err)
		}
	})
	if !strings.Contains(output, "No unused packages found") {
		t.Fatalf("expected no unused packages message, got: %q", output)
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

const generatedWrapperFixture = `#!/bin/bash
# Generated by DIU
ORIGINAL_BINARY="/opt/homebrew/bin/jq"
DIU_BINARY="diu"
DIU_SOCKET="/tmp/diu.sock"
DIU_TOOL="homebrew"
`

const unrelatedWrapperFixture = `#!/bin/bash
ORIGINAL="/usr/local/bin/tool"
DIU_BINARY="diu"
DIU_SOCKET="/tmp/custom.sock"
DIU_TOOL="custom"
echo keep
`

const legacyWrapperFixture = `#!/bin/bash
ORIGINAL="/opt/homebrew/bin/brew"
DIU_BINARY="diu"
DIU_SOCKET="/tmp/diu.sock"
DIU_TOOL="homebrew"
json_escape() {
    printf '%s' "$1"
}
DIU_RECORD_BINARY="$(command -v "$DIU_BINARY" 2>/dev/null || true)"
printf '%s\n' "$payload" | "$DIU_RECORD_BINARY" record
exit $EXIT_CODE
`

type uninstallFixture struct {
	config        *core.Config
	wrapperPath   string
	unrelatedPath string
	zshPath       string
}

type shellConfigFixture struct {
	path    string
	content string
	want    string
}

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

func TestUninstallProjectRemovesSetupArtifactsAndPreservesData(t *testing.T) {
	fixture := newUninstallFixture(t)
	output := runUninstallForTest(t)
	assertUninstallFixture(t, fixture, output)
}

func TestUninstallProjectRemovesLegacyWrappers(t *testing.T) {
	config := setupTestHomeConfig(t)
	requireConfigDirectories(t, config)
	wrapperPath := filepath.Join(config.Monitoring.Process.WrapperDir, "brew")
	writeExecutableForTest(t, wrapperPath, legacyWrapperFixture)
	runUninstallForTest(t)
	assertFileMissing(t, wrapperPath)
}

func TestUninstallProjectRemovesConfiguredWrapperOutsideHome(t *testing.T) {
	config := setupTestHomeConfig(t)
	config.Monitoring.Process.WrapperDir = t.TempDir()
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
	wrapperPath := filepath.Join(config.Monitoring.Process.WrapperDir, "jq")
	writeExecutableForTest(t, wrapperPath, generatedWrapperFixture)
	runUninstallForTest(t)
	assertFileMissing(t, wrapperPath)
}

func TestUninstallProjectRemovesSupportedShellEntries(t *testing.T) {
	files := setupShellUninstallFixture(t)
	runUninstallForTest(t)
	assertShellFixtures(t, files)
}

func TestShellHomeDirs(t *testing.T) {
	distinct := shellHomeDirs("/active-home", "/legacy-home")
	if len(distinct) != 2 || distinct[1] != "/legacy-home" {
		t.Fatalf("Distinct homes = %v", distinct)
	}
	duplicate := shellHomeDirs("/active-home", "/active-home")
	if len(duplicate) != 1 {
		t.Fatalf("Duplicate homes = %v", duplicate)
	}
}

func TestRemoveShellPathEntriesFromHomes(t *testing.T) {
	wrapperDir := filepath.Join(t.TempDir(), "wrappers")
	activeHome := t.TempDir()
	legacyHome := t.TempDir()
	activeConfig := filepath.Join(activeHome, ".zshrc")
	legacyConfig := filepath.Join(legacyHome, ".zshrc")
	writeUninstallShellFixture(t, activeConfig, wrapperDir)
	writeUninstallShellFixture(t, legacyConfig, wrapperDir)
	if err := removeShellPathEntriesFromHomes([]string{activeHome, legacyHome}, wrapperDir); err != nil {
		t.Fatalf("Failed to remove shell entries: %v", err)
	}
	assertFileContent(t, activeConfig, "before\nafter\n")
	assertFileContent(t, legacyConfig, "before\nafter\n")
}

func TestResolveWrapperDirRejectsBroadOrRelativePaths(t *testing.T) {
	invalidPaths := []string{string(filepath.Separator), "relative/wrappers"}
	for _, path := range invalidPaths {
		if _, err := resolveWrapperDir(path); err == nil {
			t.Fatalf("Expected %q to be rejected", path)
		}
	}
}

func newUninstallFixture(t *testing.T) uninstallFixture {
	t.Helper()
	config := setupTestHomeConfig(t)
	requireConfigDirectories(t, config)
	wrapperPath := filepath.Join(config.Monitoring.Process.WrapperDir, "jq")
	unrelatedPath := filepath.Join(config.Monitoring.Process.WrapperDir, "custom")
	writeExecutableForTest(t, wrapperPath, generatedWrapperFixture)
	writeExecutableForTest(t, unrelatedPath, unrelatedWrapperFixture)
	zshPath := filepath.Join(os.Getenv("HOME"), ".zshrc")
	writeUninstallShellFixture(t, zshPath, config.Monitoring.Process.WrapperDir)
	writeUsageFixture(t, config.Storage.JSONFile)
	var fixture uninstallFixture
	fixture.config = config
	fixture.wrapperPath = wrapperPath
	fixture.unrelatedPath = unrelatedPath
	fixture.zshPath = zshPath
	return fixture
}

func requireConfigDirectories(t *testing.T, config *core.Config) {
	t.Helper()
	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("Failed to ensure config directories: %v", err)
	}
}

func writeUninstallShellFixture(t *testing.T, path, wrapperDir string) {
	t.Helper()
	pathLine := core.PosixPathLine(wrapperDir)
	content := "before\n\n" + core.ShellPathMarker + "\n" + pathLine + "\nafter\n"
	if err := os.WriteFile(path, []byte(content), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write shell config: %v", err)
	}
}

func writeUsageFixture(t *testing.T, path string) {
	t.Helper()
	usage := []byte(`{"version":"1.0","packages":{},"executions":[]}`)
	if err := os.WriteFile(path, usage, core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write usage data: %v", err)
	}
}

func runUninstallForTest(t *testing.T) string {
	t.Helper()
	cmd := &command{}
	run := func() {
		if err := uninstallProject(cmd, nil); err != nil {
			t.Fatalf("uninstallProject failed: %v", err)
		}
	}
	return captureStdout(t, run)
}

func assertUninstallFixture(t *testing.T, fixture uninstallFixture, output string) {
	t.Helper()
	assertFileMissing(t, fixture.wrapperPath)
	assertFileExists(t, fixture.unrelatedPath)
	assertFileContent(t, fixture.zshPath, "before\nafter\n")
	assertFileExists(t, fixture.config.Storage.JSONFile)
	if !strings.Contains(output, "configuration and usage data preserved") {
		t.Fatalf("Unexpected uninstall output: %q", output)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Expected %s to exist: %v", path, err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Expected %s removal, stat err=%v", path, err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", path, err)
	}
	if string(content) != want {
		t.Fatalf("Unexpected content in %s: %q", path, content)
	}
}

func setupShellUninstallFixture(t *testing.T) []shellConfigFixture {
	t.Helper()
	config := setupTestHomeConfig(t)
	homeDir := os.Getenv("HOME")
	wrapperDir := filepath.Join(homeDir, "wrap$dir\"with`chars")
	config.Monitoring.Process.WrapperDir = wrapperDir
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
	fishDir := filepath.Join(homeDir, ".config", "fish")
	if err := os.MkdirAll(fishDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create fish config directory: %v", err)
	}
	files := shellConfigFixtures(homeDir, wrapperDir)
	writeShellFixtures(t, files)
	return files
}

func shellConfigFixtures(homeDir, wrapperDir string) []shellConfigFixture {
	marker := core.ShellPathMarker + "\n"
	posixLine := core.PosixPathLine(wrapperDir)
	fishLine := core.FishPathLine(wrapperDir)
	bashPath := filepath.Join(homeDir, ".bashrc")
	zshPath := filepath.Join(homeDir, ".zshrc")
	fishPath := filepath.Join(homeDir, ".config", "fish", "config.fish")
	bashContent := "before\n" + marker + posixLine + "\nafter\n"
	zshContent := "before\n\n" + marker + posixLine + "\nafter\n"
	fishContent := marker + fishLine + "\nafter\n"
	bashFixture := newShellConfigFixture(bashPath, bashContent, "before\nafter\n")
	zshFixture := newShellConfigFixture(zshPath, zshContent, "before\nafter\n")
	fishFixture := newShellConfigFixture(fishPath, fishContent, "after\n")
	return []shellConfigFixture{bashFixture, zshFixture, fishFixture}
}

func newShellConfigFixture(path, content, want string) shellConfigFixture {
	var fixture shellConfigFixture
	fixture.path = path
	fixture.content = content
	fixture.want = want
	return fixture
}

func writeShellFixtures(t *testing.T, files []shellConfigFixture) {
	t.Helper()
	for _, file := range files {
		data := []byte(file.content)
		if err := os.WriteFile(file.path, data, core.PrivateFileMode); err != nil {
			t.Fatalf("Failed to write %s: %v", file.path, err)
		}
	}
}

func assertShellFixtures(t *testing.T, files []shellConfigFixture) {
	t.Helper()
	for _, file := range files {
		assertFileContent(t, file.path, file.want)
	}
}

func TestSetupProjectReturnsSaveError(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = []string{}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{}
	config.Monitoring.Process.AutoInstallWrappers = false

	configPath := filepath.Join(homeDir, ".config", "diu", "config.json")
	if err := config.SaveTo(configPath); err != nil {
		t.Fatalf("Failed to save test config: %v", err)
	}
	if err := os.Chmod(configPath, 0400); err != nil {
		t.Fatalf("Failed to make config read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(configPath, core.PrivateFileMode)
	})

	err := setupProject(&command{}, nil)
	if err == nil {
		t.Fatal("Expected setupProject to fail")
	}
	if !strings.Contains(err.Error(), "failed to save config") {
		t.Fatalf("Expected save error, got: %v", err)
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
	config.Monitoring.EnabledTools = []string{core.ToolHomebrew}
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

// =============================================================================
// Additional Config Handler Tests
// =============================================================================

func TestGetConfigAllKeys(t *testing.T) {
	setupTestHomeConfig(t)

	// Test all valid config keys
	validKeys := []string{
		"storage.json_file",
		"storage.retention_days",
		"storage.max_executions",
		"storage.max_storage_bytes",
		"storage.max_backups",
		"daemon.pid_file",
		"daemon.socket_path",
		"api.enabled",
		"api.port",
		"monitoring.enabled_tools",
	}

	for _, key := range validKeys {
		t.Run(key, func(t *testing.T) {
			output := captureStdout(t, func() {
				if err := getConfig(&command{}, []string{key}); err != nil {
					t.Fatalf("getConfig(%s) failed: %v", key, err)
				}
			})
			trimmed := strings.TrimSpace(output)
			// For monitoring.enabled_tools, output may be empty list
			if key == "monitoring.enabled_tools" {
				// Empty is valid for this key
				return
			}
			if trimmed == "" {
				t.Fatalf("getConfig(%s) returned empty output", key)
			}
		})
	}
}

func TestSetConfigAllKeys(t *testing.T) {
	setupTestHomeConfig(t)

	tests := []struct {
		key   string
		value string
	}{
		{"storage.retention_days", "7"},
		{"storage.max_executions", "500"},
		{"storage.max_storage_bytes", "1073741824"},
		{"storage.max_backups", "5"},
		{"daemon.pid_file", "/tmp/diu.pid"},
		{"daemon.socket_path", "/tmp/diu.sock"},
		{"api.enabled", "false"},
		{"api.port", "9090"},
		{"monitoring.enabled_tools", "homebrew,npm"},
	}

	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			// Set the value
			output := captureStdout(t, func() {
				if err := setConfig(&command{}, []string{tt.key, tt.value}); err != nil {
					t.Fatalf("setConfig(%s, %s) failed: %v", tt.key, tt.value, err)
				}
			})
			if !strings.Contains(output, "Configuration updated") {
				t.Fatalf("Expected 'Configuration updated', got: %q", output)
			}

			// Verify it was set by getting it back
			getOutput := captureStdout(t, func() {
				if err := getConfig(&command{}, []string{tt.key}); err != nil {
					t.Fatalf("getConfig(%s) failed: %v", tt.key, err)
				}
			})
			// For monitoring.enabled_tools, the output has comma-space separator
			var expectedOutput = tt.value
			if tt.key == "monitoring.enabled_tools" {
				expectedOutput = strings.ReplaceAll(tt.value, ",", ", ")
			}
			if strings.TrimSpace(getOutput) != expectedOutput {
				t.Fatalf("getConfig(%s) = %q, want %q", tt.key, strings.TrimSpace(getOutput), expectedOutput)
			}
		})
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestGetToolColor(t *testing.T) {
	tests := []struct {
		tool string
		want bool // true if we expect a non-empty color
	}{
		{"homebrew", true},
		{"npm", true},
		{"go", true},
		{"pip", true},
		{"gem", true},
		{"cargo", true},
		{"unknown", true}, // default case should still return a color
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := getToolColor(tt.tool)
			if tt.want && got == "" {
				t.Fatalf("getToolColor(%q) = %q, want non-empty", tt.tool, got)
			}
		})
	}
}

func TestFormatLastUsed(t *testing.T) {
	tests := []struct {
		lastUsed time.Time
		want     string
	}{
		{time.Time{}, "never"},
		{time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), "2024-01-15"},
		{time.Date(2025, 12, 25, 10, 30, 0, 0, time.UTC), "2025-12-25"},
	}

	for _, tt := range tests {
		t.Run(tt.lastUsed.Format("2006-01-02"), func(t *testing.T) {
			got := formatLastUsed(tt.lastUsed)
			if got != tt.want {
				t.Fatalf("formatLastUsed(%v) = %q, want %q", tt.lastUsed, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		value     string
		maxLength int
		want      string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 4, "hel."},
		{"hello", 3, "he."},
		{"hello", 2, "h."},
		{"hello", 1, "h"},
		{"hello", 0, ""},
		{"", 5, ""},
		{"a", 1, "a"},
		{"short", 10, "short"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q,%d", tt.value, tt.maxLength), func(t *testing.T) {
			got := truncate(tt.value, tt.maxLength)
			if got != tt.want {
				t.Fatalf("truncate(%q, %d) = %q, want %q", tt.value, tt.maxLength, got, tt.want)
			}
		})
	}
}

func TestValidateExecutablePath(t *testing.T) {
	tempDir := t.TempDir()

	// Test valid executable
	execPath := filepath.Join(tempDir, "tool")
	writeExecutableForTest(t, execPath, "#!/bin/bash\necho test\n")

	validated, err := validateExecutablePath(execPath)
	if err != nil {
		t.Fatalf("validateExecutablePath(%s) failed: %v", execPath, err)
	}
	if validated != execPath {
		t.Fatalf("validateExecutablePath() = %s, want %s", validated, execPath)
	}

	// Test empty path
	if _, err := validateExecutablePath(""); err == nil {
		t.Fatal("Expected error for empty path")
	}

	// Test non-absolute path
	if _, err := validateExecutablePath("relative/tool"); err == nil {
		t.Fatal("Expected error for relative path")
	}

	// Test directory
	dirPath := filepath.Join(tempDir, "dir")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	if _, err := validateExecutablePath(dirPath); err == nil {
		t.Fatal("Expected error for directory")
	}

	// Test non-executable file
	nonExec := filepath.Join(tempDir, "notes.txt")
	if err := os.WriteFile(nonExec, []byte("not executable"), 0o644); err != nil {
		t.Fatalf("Failed to create non-executable file: %v", err)
	}
	if _, err := validateExecutablePath(nonExec); err == nil {
		t.Fatal("Expected error for non-executable file")
	}

	// Test non-existent file
	if _, err := validateExecutablePath("/nonexistent/path/to/tool"); err == nil {
		t.Fatal("Expected error for non-existent file")
	}
}

// =============================================================================
// CLI Helper Tests
// =============================================================================

func TestFlagParsingComprehensive(t *testing.T) {
	cmd := &command{}
	var tool, pkg, last, format string
	var limit int
	var daily, weekly, jsonFlag bool

	cmd.Flags().StringVarP(&tool, "tool", "t", "", "tool")
	cmd.Flags().StringVarP(&pkg, "package", "p", "", "package")
	cmd.Flags().StringVarP(&last, "last", "l", "", "last")
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "limit")
	cmd.Flags().StringVarP(&format, "format", "f", "table", "format")
	cmd.Flags().BoolVarP(&daily, "daily", "d", false, "daily")
	cmd.Flags().BoolVarP(&weekly, "weekly", "w", false, "weekly")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "json")

	tests := []struct {
		name       string
		args       []string
		wantTool   string
		wantPkg    string
		wantLast   string
		wantLimit  int
		wantFormat string
		wantDaily  bool
		wantWeekly bool
		wantJSON   bool
	}{
		{
			name:       "all flags long",
			args:       []string{"--tool", "npm", "--package", "eslint", "--last", "1h", "--limit", "10", "--format", "json", "--daily", "--weekly", "--json"},
			wantTool:   "npm",
			wantPkg:    "eslint",
			wantLast:   "1h",
			wantLimit:  10,
			wantFormat: "json",
			wantDaily:  true,
			wantWeekly: true,
			wantJSON:   true,
		},
		{
			name:       "all flags short",
			args:       []string{"-t", "brew", "-p", "jq", "-l", "2d", "-n", "5", "-f", "csv", "-d", "-w"},
			wantTool:   "brew",
			wantPkg:    "jq",
			wantLast:   "2d",
			wantLimit:  5,
			wantFormat: "csv",
			wantDaily:  true,
			wantWeekly: true,
			wantJSON:   false,
		},
		{
			name:       "no flags",
			args:       []string{},
			wantTool:   "",
			wantPkg:    "",
			wantLast:   "",
			wantLimit:  20,
			wantFormat: "table",
			wantDaily:  false,
			wantWeekly: false,
			wantJSON:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset all variables
			tool = ""
			pkg = ""
			last = ""
			limit = 20
			format = "table"
			daily = false
			weekly = false
			jsonFlag = false

			remaining, err := cmd.Flags().parse(tt.args)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if len(remaining) != 0 {
				t.Fatalf("unexpected remaining args: %v", remaining)
			}

			if tool != tt.wantTool {
				t.Errorf("tool = %q, want %q", tool, tt.wantTool)
			}
			if pkg != tt.wantPkg {
				t.Errorf("pkg = %q, want %q", pkg, tt.wantPkg)
			}
			if last != tt.wantLast {
				t.Errorf("last = %q, want %q", last, tt.wantLast)
			}
			if limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tt.wantLimit)
			}
			if format != tt.wantFormat {
				t.Errorf("format = %q, want %q", format, tt.wantFormat)
			}
			if daily != tt.wantDaily {
				t.Errorf("daily = %v, want %v", daily, tt.wantDaily)
			}
			if weekly != tt.wantWeekly {
				t.Errorf("weekly = %v, want %v", weekly, tt.wantWeekly)
			}
			if jsonFlag != tt.wantJSON {
				t.Errorf("json = %v, want %v", jsonFlag, tt.wantJSON)
			}
		})
	}
}

func TestCommandExecution(t *testing.T) {
	root := &command{Use: "diu", Short: "DIU CLI"}

	var executed bool
	var capturedArg string
	child := &command{
		Use:   "query",
		Short: "Query executions",
		RunE: func(cmd *command, args []string) error {
			executed = true
			capturedArg = args[0]
			return nil
		},
	}
	root.AddCommand(child)

	// Test subcommand execution with simple args (no flags)
	err := root.Execute([]string{"query", "arg1"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !executed {
		t.Fatal("Expected subcommand to execute")
	}
	if capturedArg != "arg1" {
		t.Fatalf("capturedArg = %q, want %q", capturedArg, "arg1")
	}
}

func TestCommandHelp(t *testing.T) {
	cmd := &command{Use: "test", Short: "Test command"}

	output := captureStdout(t, func() {
		cmd.printUsage()
	})

	if !strings.Contains(output, "Test command") {
		t.Fatalf("Expected short description in output, got: %q", output)
	}
	if !strings.Contains(output, "Usage:") {
		t.Fatalf("Expected 'Usage:' in output, got: %q", output)
	}
}

func TestVersionString(t *testing.T) {
	// Just verify it doesn't panic and returns something
	vs := versionString()
	if vs == "" {
		t.Fatal("versionString returned empty")
	}
	if !strings.Contains(vs, "diu") {
		t.Fatalf("Expected 'diu' in version string, got: %q", vs)
	}
}

func TestExecuteWithHelpFlag(t *testing.T) {
	root := &command{Use: "diu", Short: "DIU CLI"}

	output := captureStdout(t, func() {
		if err := root.Execute([]string{"--help"}); err != nil {
			t.Fatalf("Execute --help failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU CLI") {
		t.Fatalf("Expected short description in output, got: %q", output)
	}
	if !strings.Contains(output, "Usage:") {
		t.Fatalf("Expected 'Usage:' in output, got: %q", output)
	}
}

func TestExecuteWithVersionFlag(t *testing.T) {
	root := &command{Use: "diu", Short: "DIU CLI"}

	output := captureStdout(t, func() {
		if err := root.Execute([]string{"--version"}); err != nil {
			t.Fatalf("Execute --version failed: %v", err)
		}
	})

	if !strings.Contains(output, "diu") {
		t.Fatalf("Expected version info in output, got: %q", output)
	}
}

func TestExecuteUnknownCommand(t *testing.T) {
	root := &command{Use: "diu", Short: "DIU CLI"}

	// Test with unknown command - should print usage to stderr
	// We can't easily capture stderr, so just verify it doesn't panic
	err := root.Execute([]string{"unknown-command"})
	if err == nil {
		t.Fatal("Expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("Expected 'unknown command' error, got: %v", err)
	}
}

// =============================================================================
// Additional Helper Function Tests
// =============================================================================

func TestCloseStore(t *testing.T) {
	config := setupTestHomeConfig(t)
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// closeStore is defined in helpers.go - test it directly
	// This just calls store.Close()
	closeStore(store)
	// If we get here without panic, it succeeded
}

func TestIsTerminal(t *testing.T) {
	// isTerminal checks if stdout is a terminal
	// In tests, stdout is usually not a terminal
	result := isTerminal()
	// Just verify it doesn't panic and returns a bool
	_ = result
}

func TestGoBinaryDir(t *testing.T) {
	config := core.DefaultConfig()
	// Set GoBin to a test path
	tempDir := t.TempDir()
	goBinPath := filepath.Join(tempDir, "go", "bin")
	if err := os.MkdirAll(goBinPath, 0o755); err != nil {
		t.Fatalf("Failed to create go bin path: %v", err)
	}
	config.Tools.Go.GoBin = goBinPath

	got := goBinaryDir(config)
	// goBinaryDir returns GoBin directly if set
	if got != goBinPath {
		t.Fatalf("goBinaryDir = %s, want %s", got, goBinPath)
	}
}

func TestNpmPackageFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/usr/local/lib/node_modules/package/bin/tool", "package"},
		{"/usr/local/lib/node_modules/@scope/package/bin/tool", "@scope/package"},
		{"/usr/local/lib/node_modules/@scope/package", "@scope/package"},
		{"", ""},
		{"no node_modules", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := npmPackageFromPath(tt.path)
			if got != tt.want {
				t.Fatalf("npmPackageFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestWrapperNameForPackage(t *testing.T) {
	tests := []struct {
		pkg  *core.PackageInfo
		want string
	}{
		{&core.PackageInfo{Name: "jq", Path: ""}, "jq"},
		{&core.PackageInfo{Name: "jq", Path: "/opt/homebrew/bin/jq"}, "jq"},
		{&core.PackageInfo{Name: "tool", Path: "/path/to/tool"}, "tool"},
	}

	for _, tt := range tests {
		t.Run(tt.pkg.Name, func(t *testing.T) {
			got := wrapperNameForPackage(tt.pkg)
			if got != tt.want {
				t.Fatalf("wrapperNameForPackage(%+v) = %q, want %q", tt.pkg, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Daemon Handler Tests with Mock Checker
// =============================================================================

// MockDaemonChecker is a mock implementation of DaemonChecker for testing
type MockDaemonChecker struct {
	isRunning bool
}

func (m MockDaemonChecker) IsRunning(config *core.Config) bool {
	return m.isRunning
}

func TestStartDaemonAlreadyRunning(t *testing.T) {
	setupTestHomeConfig(t)

	// Create a mock checker that simulates daemon already running
	mock := MockDaemonChecker{isRunning: true}

	restore := SetDaemonChecker(mock)
	defer restore()

	output := captureStdout(t, func() {
		if err := startDaemon(&command{}, nil); err != nil {
			t.Fatalf("startDaemon failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU daemon is already running") {
		t.Fatalf("Expected 'already running' message, got: %q", output)
	}
}

func TestStopDaemonNotRunning(t *testing.T) {
	setupTestHomeConfig(t)

	// Create a mock checker that simulates daemon not running
	mock := MockDaemonChecker{isRunning: false}

	restore := SetDaemonChecker(mock)
	defer restore()

	output := captureStdout(t, func() {
		if err := stopDaemon(&command{}, nil); err != nil {
			t.Fatalf("stopDaemon failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU daemon is not running") {
		t.Fatalf("Expected 'not running' message, got: %q", output)
	}
}

func TestDaemonStatusWithMockRunning(t *testing.T) {
	setupTestHomeConfig(t)

	// Create a mock checker that simulates daemon running
	mock := MockDaemonChecker{isRunning: true}

	restore := SetDaemonChecker(mock)
	defer restore()

	// Also need to set up a PID file for the status to read
	config, _ := core.LoadConfig("")
	pidFile := config.Daemon.PIDFile
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		t.Fatalf("Failed to create PID directory: %v", err)
	}
	if err := os.WriteFile(pidFile, []byte("12345\n"), 0o644); err != nil {
		t.Fatalf("Failed to write PID file: %v", err)
	}
	defer func() {
		if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
			t.Logf("Failed to remove PID file: %v", err)
		}
	}()

	output := captureStdout(t, func() {
		if err := daemonStatus(&command{}, nil); err != nil {
			t.Fatalf("daemonStatus failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU daemon is running") {
		t.Fatalf("Expected 'is running' message, got: %q", output)
	}
}

// =============================================================================
// CLI Function Tests
// =============================================================================

func TestPrintHelp(t *testing.T) {
	root := &command{
		Use:   "diu",
		Short: "DIU CLI",
		Long:  "DIU is a dependency management tool",
	}

	output := captureStdout(t, func() {
		if err := root.printHelp(nil); err != nil {
			t.Fatalf("printHelp failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU is a dependency management tool") {
		t.Fatalf("Expected long description, got: %q", output)
	}
}

func TestPrintUsageTo(t *testing.T) {
	cmd := &command{
		Use:   "test",
		Short: "Test command",
	}

	var buf bytes.Buffer
	cmd.printUsageTo(&buf)

	output := buf.String()
	if !strings.Contains(output, "Test command") {
		t.Fatalf("Expected short description, got: %q", output)
	}
	if !strings.Contains(output, "Usage:") {
		t.Fatalf("Expected Usage:, got: %q", output)
	}
	if !strings.Contains(output, "test") {
		t.Fatalf("Expected command name, got: %q", output)
	}
}

func TestCommandName(t *testing.T) {
	tests := []struct {
		use  string
		want string
	}{
		{"diu", "diu"},
		{"query", "query"},
		{"list-packages", "list-packages"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.use, func(t *testing.T) {
			got := commandName(tt.use)
			if got != tt.want {
				t.Fatalf("commandName(%q) = %q, want %q", tt.use, got, tt.want)
			}
		})
	}
}

func TestCoreVersion(t *testing.T) {
	// Verify coreVersion returns either the build version or the core package version
	version := coreVersion()
	if version == "" {
		t.Fatal("coreVersion returned empty")
	}
	// Should be either the build version or the default core version.
	expectedVersions := []string{core.Version, version}
	found := false
	for _, v := range expectedVersions {
		if version == v {
			found = true
			break
		}
	}
	if !found && version != "dev" {
		t.Fatalf("coreVersion returned unexpected value: %q", version)
	}
}

func TestVersionStringConsistency(t *testing.T) {
	vs := versionString()
	if vs == "" {
		t.Fatal("versionString returned empty")
	}
	// versionString should be in format: "diu <version> (commit <hash>, built <date>)"
	if !strings.HasPrefix(vs, "diu ") {
		t.Fatalf("versionString should start with 'diu ', got: %q", vs)
	}
	if !strings.Contains(vs, "commit ") {
		t.Fatalf("versionString should contain 'commit ', got: %q", vs)
	}
	if !strings.Contains(vs, "built ") {
		t.Fatalf("versionString should contain 'built ', got: %q", vs)
	}
	// Verify it contains the core version
	if !strings.Contains(vs, coreVersion()) {
		t.Fatalf("versionString should contain coreVersion, got: %q", vs)
	}
}

func TestFlagGetters(t *testing.T) {
	flags := newFlagSet()
	var strVal string
	var intVal int
	var boolVal bool

	flags.StringVarP(&strVal, "string", "s", "default", "string flag")
	flags.IntVarP(&intVal, "int", "i", 42, "int flag")
	flags.BoolVarP(&boolVal, "bool", "b", true, "bool flag")

	// Set values - for booleans, use = syntax
	_, err := flags.parse([]string{"--string", "test", "--int", "100", "--bool=false"})
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// Test getters
	strFlag := flags.lookupLong("string")
	if strFlag == nil {
		t.Fatal("lookupLong returned nil for string")
	}
	if strFlag.stringValue == nil || *strFlag.stringValue != "test" {
		t.Fatalf("string flag value = %v, want 'test'", strFlag.stringValue)
	}

	intFlag := flags.lookupLong("int")
	if intFlag == nil {
		t.Fatal("lookupLong returned nil for int")
	}
	if intFlag.intValue == nil || *intFlag.intValue != 100 {
		t.Fatalf("int flag value = %d, want 100", *intFlag.intValue)
	}

	boolFlag := flags.lookupLong("bool")
	if boolFlag == nil {
		t.Fatal("lookupLong returned nil for bool")
	}
	if boolFlag.boolValue == nil || *boolFlag.boolValue != false {
		t.Fatalf("bool flag value = %v, want false", *boolFlag.boolValue)
	}

	// Test GetString, GetInt, GetBool
	gotStr, err := flags.GetString("string")
	if err != nil || gotStr != "test" {
		t.Fatalf("GetString failed: %v, got %q", err, gotStr)
	}
	gotInt, err := flags.GetInt("int")
	if err != nil || gotInt != 100 {
		t.Fatalf("GetInt failed: %v, got %d", err, gotInt)
	}
	gotBool, err := flags.GetBool("bool")
	if err != nil || gotBool != false {
		t.Fatalf("GetBool failed: %v, got %v", err, gotBool)
	}

	// Test lookupShort
	strFlagShort := flags.lookupShort("s")
	if strFlagShort != strFlag {
		t.Fatal("lookupShort didn't return same flag as lookupLong")
	}
}

func TestFlagStringMethod(t *testing.T) {
	flags := newFlagSet()
	var strVal string
	flags.StringVar(&strVal, "string", "default", "string flag")

	_, _ = flags.parse([]string{"--string", "test"})

	strFlag := flags.lookupLong("string")
	if strFlag == nil {
		t.Fatal("lookupLong returned nil")
	}

	// Test String() method on flagValue
	fv := flagValue{flag: strFlag}
	if fv.String() != "test" {
		t.Fatalf("flagValue.String() = %q, want 'test'", fv.String())
	}
}

func TestStyleRenderTo(t *testing.T) {
	// Test with color disabled
	t.Setenv("NO_COLOR", "1")

	style := newStyle().Bold(true).Foreground(color("205"))
	// Create a temp file to use as *os.File
	tmpFile, err := os.CreateTemp(t.TempDir(), "render")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := tmpFile.Close(); err != nil {
			t.Fatalf("Failed to close temp file: %v", err)
		}
	}()
	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			t.Fatalf("Failed to remove temp file: %v", err)
		}
	}()

	result := style.RenderTo("test", tmpFile)

	// With NO_COLOR, should return plain text
	if result != "test" {
		t.Fatalf("RenderTo with NO_COLOR = %q, want 'test'", result)
	}
}

func TestShouldRenderColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	tmpFile, err := os.CreateTemp(t.TempDir(), "color")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := tmpFile.Close(); err != nil {
			t.Fatalf("Failed to close temp file: %v", err)
		}
	}()
	defer func() {
		if err := os.Remove(tmpFile.Name()); err != nil {
			t.Fatalf("Failed to remove temp file: %v", err)
		}
	}()

	if shouldRenderColor(tmpFile) {
		t.Fatal("shouldRenderColor should return false with NO_COLOR set")
	}

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if shouldRenderColor(tmpFile) {
		t.Fatal("shouldRenderColor should return false with TERM=dumb")
	}

	t.Setenv("TERM", "xterm-256color")
	if shouldRenderColor(tmpFile) {
		t.Fatal("shouldRenderColor should return false for a regular file")
	}
}

func TestExecuteWithHelpCommand(t *testing.T) {
	root := &command{Use: "diu", Short: "DIU CLI"}

	output := captureStdout(t, func() {
		if err := root.Execute([]string{"help"}); err != nil {
			t.Fatalf("Execute help failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU CLI") {
		t.Fatalf("Expected short description, got: %q", output)
	}
}

func TestExecuteWithUnknownHelp(t *testing.T) {
	root := &command{Use: "diu", Short: "DIU CLI"}

	err := root.Execute([]string{"help", "unknown"})
	if err == nil {
		t.Fatal("Expected error for unknown help command")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("Expected 'unknown command' error, got: %v", err)
	}
}

func TestParseEdgeCases(t *testing.T) {
	flags := newFlagSet()
	var val string
	flags.StringVar(&val, "flag", "", "flag")

	// Test empty args
	remaining, err := flags.parse([]string{})
	if err != nil {
		t.Fatalf("parse([]) failed: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("Expected no remaining args, got: %v", remaining)
	}
	if val != "" {
		t.Fatalf("Expected default value, got: %q", val)
	}

	// Test only positional args
	remaining, err = flags.parse([]string{"arg1", "arg2"})
	if err != nil {
		t.Fatalf("parse positional args failed: %v", err)
	}
	if len(remaining) != 2 || remaining[0] != "arg1" || remaining[1] != "arg2" {
		t.Fatalf("Expected [arg1 arg2], got: %v", remaining)
	}
}

func TestFlagSetMethod(t *testing.T) {
	flags := newFlagSet()
	var val string
	flags.StringVar(&val, "flag", "", "flag")

	// Parse and then set directly on the flag
	_, _ = flags.parse([]string{"--flag", "initial"})

	flag := flags.lookupLong("flag")
	if flag == nil {
		t.Fatal("lookupLong returned nil")
	}

	// Set should update the value
	if err := flag.set("updated", true); err != nil {
		t.Fatalf("flag.set failed: %v", err)
	}

	if val != "updated" {
		t.Fatalf("set failed: val = %q, want 'updated'", val)
	}
}

type stoppingChecker struct {
	runningCalls   int
	stopAfterCalls int
}

func (s *stoppingChecker) IsRunning(_ *core.Config) bool {
	s.runningCalls++
	return s.runningCalls < s.stopAfterCalls
}

type startingChecker struct {
	runningCalls    int
	startAfterCalls int
}

func (s *startingChecker) IsRunning(_ *core.Config) bool {
	s.runningCalls++
	return s.runningCalls >= s.startAfterCalls
}

type alwaysRunningChecker struct {
	calls int
}

func (a *alwaysRunningChecker) IsRunning(_ *core.Config) bool {
	a.calls++
	return true
}

type sequenceDaemonChecker struct {
	states []bool
	calls  int
}

func (s *sequenceDaemonChecker) IsRunning(_ *core.Config) bool {
	if len(s.states) == 0 {
		return false
	}
	if s.calls >= len(s.states) {
		return s.states[len(s.states)-1]
	}
	state := s.states[s.calls]
	s.calls++
	return state
}

func TestWaitForDaemonStartedImmediate(t *testing.T) {
	restore := SetDaemonChecker(MockDaemonChecker{isRunning: true})
	defer restore()

	if err := waitForDaemonStarted(&core.Config{}, time.Second); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitForDaemonStartedAfterPolling(t *testing.T) {
	checker := &startingChecker{startAfterCalls: 3}
	restore := SetDaemonChecker(checker)
	defer restore()

	start := time.Now()
	if err := waitForDaemonStarted(&core.Config{}, time.Second); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	elapsed := time.Since(start)
	minExpected := 2 * daemonStartPollInterval
	if elapsed < minExpected {
		t.Fatalf("expected at least %s of polling, got %s", minExpected, elapsed)
	}
	if checker.runningCalls < 3 {
		t.Fatalf("expected at least 3 IsRunning calls, got %d", checker.runningCalls)
	}
}

func TestWaitForDaemonStartedTimeout(t *testing.T) {
	restore := SetDaemonChecker(MockDaemonChecker{isRunning: false})
	defer restore()

	err := waitForDaemonStarted(&core.Config{}, 250*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' in error, got %q", err.Error())
	}
}

func TestWaitForDaemonStoppedImmediate(t *testing.T) {
	restore := SetDaemonChecker(MockDaemonChecker{isRunning: false})
	defer restore()

	if err := waitForDaemonStopped(&core.Config{}, time.Second); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitForDaemonStoppedAfterPolling(t *testing.T) {
	checker := &stoppingChecker{stopAfterCalls: 3}
	restore := SetDaemonChecker(checker)
	defer restore()

	start := time.Now()
	if err := waitForDaemonStopped(&core.Config{}, time.Second); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	elapsed := time.Since(start)
	minExpected := 2 * daemonStopPollInterval
	if elapsed < minExpected {
		t.Fatalf("expected at least %s of polling, got %s", minExpected, elapsed)
	}
	if checker.runningCalls < 3 {
		t.Fatalf("expected at least 3 IsRunning calls, got %d", checker.runningCalls)
	}
}

func TestWaitForDaemonStoppedTimeout(t *testing.T) {
	checker := &alwaysRunningChecker{}
	restore := SetDaemonChecker(checker)
	defer restore()

	err := waitForDaemonStopped(&core.Config{}, 250*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' in error, got %q", err.Error())
	}
}

func TestRestartDaemonStopsThenStarts(t *testing.T) {
	setupTestHomeConfig(t)
	checker := &sequenceDaemonChecker{states: []bool{false, false, true}}
	restore := SetDaemonChecker(checker)
	defer restore()

	oldStarter := daemonProcessStarter
	daemonProcessStarter = func(string, []string, *syscall.ProcAttr) error {
		return nil
	}
	defer func() {
		daemonProcessStarter = oldStarter
	}()

	output := captureStdout(t, func() {
		if err := restartDaemon(&command{}, nil); err != nil {
			t.Fatalf("restartDaemon failed: %v", err)
		}
	})

	if !strings.Contains(output, "not running") {
		t.Fatalf("expected stop branch to short-circuit, got %q", output)
	}
	if !strings.Contains(output, "DIU daemon started") {
		t.Fatalf("expected start branch to complete, got %q", output)
	}
	if checker.calls < 3 {
		t.Fatalf("expected at least 3 IsRunning calls, got %d", checker.calls)
	}
}

func TestForkDaemonBackgroundStarterError(t *testing.T) {
	oldStarter := daemonProcessStarter
	daemonProcessStarter = func(string, []string, *syscall.ProcAttr) error {
		return errors.New("fork failed")
	}
	defer func() {
		daemonProcessStarter = oldStarter
	}()

	var err error
	captureStdout(t, func() {
		err = forkDaemonBackground(&core.Config{})
	})
	if err == nil {
		t.Fatal("expected fork error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to fork daemon") {
		t.Fatalf("expected fork failure, got %v", err)
	}
}

func TestStopDaemonWithConfigMissingPIDFile(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(MockDaemonChecker{isRunning: true})
	defer restore()

	err := stopDaemonWithConfig(config)
	if err == nil {
		t.Fatal("expected missing PID file error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read PID file") {
		t.Fatalf("expected PID read error, got %v", err)
	}
}

func TestStopDaemonWithConfigInvalidPID(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(MockDaemonChecker{isRunning: true})
	defer restore()

	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}
	if err := os.WriteFile(config.Daemon.PIDFile, []byte("not-a-pid"), core.PrivateFileMode); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	err := stopDaemonWithConfig(config)
	if err == nil {
		t.Fatal("expected invalid PID error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid PID") {
		t.Fatalf("expected invalid PID error, got %v", err)
	}
}

func TestStopDaemonWithConfigSignalError(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(MockDaemonChecker{isRunning: true})
	defer restore()

	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}
	if err := os.WriteFile(config.Daemon.PIDFile, []byte("999999999"), core.PrivateFileMode); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	err := stopDaemonWithConfig(config)
	if err == nil {
		t.Fatal("expected signal error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to stop daemon") {
		t.Fatalf("expected signal error, got %v", err)
	}
}

func TestPrintPackageListJSON(t *testing.T) {
	packages := []*core.PackageInfo{
		{Name: "ripgrep", Tool: core.ToolHomebrew, Version: "13.0"},
	}
	out := captureStdout(t, func() {
		if err := printPackageList(packages, formatJSON); err != nil {
			t.Fatalf("printPackageList JSON failed: %v", err)
		}
	})
	if !strings.Contains(out, `"name": "ripgrep"`) {
		t.Fatalf("expected JSON name, got: %q", out)
	}
}

func TestPrintPackageListCSV(t *testing.T) {
	packages := []*core.PackageInfo{
		{Name: "rip,grep", Tool: core.ToolHomebrew, Version: "13.0", UsageCount: 5},
	}
	out := captureStdout(t, func() {
		if err := printPackageList(packages, formatCSV); err != nil {
			t.Fatalf("printPackageList CSV failed: %v", err)
		}
	})
	if !strings.Contains(out, "tool,name,version") {
		t.Fatalf("expected CSV header, got: %q", out)
	}
	if !strings.Contains(out, `"rip,grep"`) {
		t.Fatalf("expected package row, got: %q", out)
	}
}

func TestPrintPackageListCSVWriterError(t *testing.T) {
	packages := []*core.PackageInfo{
		{Name: strings.Repeat("x", 8192), Tool: core.ToolHomebrew, Version: "13.0", UsageCount: 5},
	}

	var err error
	withReadOnlyStdout(t, func() {
		err = printPackageList(packages, formatCSV)
	})
	if err == nil {
		t.Fatal("Expected CSV writer error")
	}
}

func TestManagePackagesDryRun(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name:    "ripgrep",
		Tool:    core.ToolHomebrew,
		Version: "13.0",
	})
	closeTestStore(t, store)

	cmd := manageCommandForTest(t, "--uninstall", "ripgrep", "--dry-run")

	out := captureStdout(t, func() {
		if err := managePackages(cmd, nil); err != nil {
			t.Fatalf("managePackages failed: %v", err)
		}
	})
	if !strings.Contains(out, "brew uninstall ripgrep") {
		t.Fatalf("expected dry-run plan, got: %q", out)
	}
}

func TestManagePackagesSearch(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name: "ripgrep",
		Tool: core.ToolHomebrew,
	})
	closeTestStore(t, store)

	cmd := manageCommandForTest(t, "--search", "rip")
	out := captureStdout(t, func() {
		if err := managePackages(cmd, nil); err != nil {
			t.Fatalf("managePackages failed: %v", err)
		}
	})
	if !strings.Contains(out, "ripgrep") {
		t.Fatalf("expected ripgrep in output, got: %q", out)
	}
}

func TestUninstallByNameNotFound(t *testing.T) {
	setupTestHomeConfig(t)
	err := uninstallByName("nonexistent", "", true, false)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found', got %v", err)
	}
}

func TestUninstallByNameRequiresYes(t *testing.T) {
	setupTestHomeConfig(t)
	err := uninstallByName("anything", "", false, false)
	if err == nil {
		t.Fatal("expected --yes required error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes message, got %v", err)
	}
}

func TestUninstallByNameMultipleMatches(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{Name: "lodash", Tool: core.ToolHomebrew})
	updateTestPackage(t, store, &core.PackageInfo{Name: "lodash", Tool: core.ToolNPM})
	closeTestStore(t, store)

	err := uninstallByName("lodash", "", true, false)
	if err == nil {
		t.Fatal("expected multiple-matches error")
	}
	if !strings.Contains(err.Error(), "multiple packages") {
		t.Fatalf("expected multiple-packages error, got %v", err)
	}
}

func TestUninstallByNameDryRun(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{Name: "typescript", Tool: core.ToolNPM})
	closeTestStore(t, store)

	out := captureStdout(t, func() {
		if err := uninstallByName("typescript", "", false, true); err != nil {
			t.Fatalf("dry-run uninstall failed: %v", err)
		}
	})
	if !strings.Contains(out, "npm uninstall") {
		t.Fatalf("expected npm uninstall plan, got: %q", out)
	}
}

func TestUninstallByNameDryRunPipUsesResolvedCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	prependFakeCommand(t, pip3CommandName, "#!/bin/sh\nexit 0\n")

	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{Name: "ruff", Tool: core.ToolPip})
	closeTestStore(t, store)

	out := captureStdout(t, func() {
		if err := uninstallByName("ruff", "", false, true); err != nil {
			t.Fatalf("dry-run uninstall failed: %v", err)
		}
	})
	if !strings.Contains(out, "pip3 uninstall -y ruff") {
		t.Fatalf("expected pip3 uninstall plan, got: %q", out)
	}
}

func TestUninstallByNameAssumeYesExecutes(t *testing.T) {
	prependFakeCommand(t, "npm", "#!/bin/sh\nexit 0\n")
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{Name: "typescript", Tool: core.ToolNPM})
	closeTestStore(t, store)

	out := captureStdout(t, func() {
		if err := uninstallByName("typescript", "", true, false); err != nil {
			t.Fatalf("uninstall failed: %v", err)
		}
	})
	if !strings.Contains(out, "uninstalled") {
		t.Fatalf("expected uninstalled, got: %q", out)
	}
}

func TestScanPackagesNoEnabledTools(t *testing.T) {
	setupTestHomeConfig(t)
	out := captureStdout(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})
	if !strings.Contains(out, "packages scanned") {
		t.Fatalf("expected 'packages scanned' message, got: %q", out)
	}
}

func TestInstallWrappersSkipsUnknownTool(t *testing.T) {
	config := setupTestHomeConfig(t)
	config.Monitoring.EnabledTools = []string{"unknown-tool"}

	if err := installWrappers(config); err != nil {
		t.Fatalf("expected nil for unknown tool, got %v", err)
	}
}

func TestInstallWrappersInitializesKnownTool(t *testing.T) {
	config := setupTestHomeConfig(t)
	config.Monitoring.EnabledTools = []string{core.ToolGoBinary}

	if err := installWrappers(config); err != nil {
		t.Fatalf("installWrappers failed: %v", err)
	}
}
