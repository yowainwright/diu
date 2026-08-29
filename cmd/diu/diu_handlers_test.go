package main

import (
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
	"github.com/yowainwright/diu/internal/daemon"
	"github.com/yowainwright/diu/internal/storage"
)

func TestDaemonStatusNotRunning(t *testing.T) {
	setupTestHomeConfig(t)

	output := captureStderr(t, func() {
		if err := daemonStatus(&command{}, nil); err != nil {
			t.Fatalf("daemonStatus error = %v", err)
		}
	})
	if !strings.Contains(output, "DIU daemon is not running") {
		t.Fatalf("daemonStatus output = %q", output)
	}
}

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
	addQueryExecutionForTest(t, config)
	output := runQueryCommandForTest(t, "--tool", "homebrew")
	assertOutputContains(t, output, "Execution History")
	assertOutputContains(t, output, "brew install jq")
}

func addQueryExecutionForTest(t *testing.T, config *core.Config) {
	t.Helper()

	addStoredExecutionForTest(t, config, &core.ExecutionRecord{
		Tool:      core.ToolHomebrew,
		Command:   "brew install jq",
		Args:      []string{"install", "jq"},
		Timestamp: time.Now(),
		Duration:  1500 * time.Millisecond,
		ExitCode:  0,
	})
}

func addStoredExecutionForTest(t *testing.T, config *core.Config, record *core.ExecutionRecord) {
	t.Helper()

	store := openTestStore(t, config)
	addTestExecution(t, store, record)
	closeTestStore(t, store)
}

func runQueryCommandForTest(t *testing.T, args ...string) string {
	t.Helper()

	return captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t, args...), nil); err != nil {
			t.Fatalf("queryExecutions failed: %v", err)
		}
	})
}

func assertOutputContains(t *testing.T, output, value string) {
	t.Helper()

	if !strings.Contains(output, value) {
		t.Fatalf("Expected %q in output, got: %q", value, output)
	}
}

func TestQueryExecutionsJSON(t *testing.T) {
	config := setupTestHomeConfig(t)
	record := queryExecutionRecord(core.ToolNPM, "npm install eslint", []string{"install", "eslint"})
	addStoredExecutionForTest(t, config, record)

	output := runQueryCommandForTest(t, "--format", "json")
	records := decodeExecutionOutput(t, output)
	assertFirstExecutionCommand(t, records, "npm install eslint")
}

func TestQueryExecutionsCSV(t *testing.T) {
	config := setupTestHomeConfig(t)
	record := queryExecutionRecord(core.ToolHomebrew, "brew upgrade,all", []string{"upgrade"})
	addStoredExecutionForTest(t, config, record)

	output := runQueryCommandForTest(t, "--format", "csv")
	assertCSVQueryOutput(t, output)
}

func queryExecutionRecord(tool, command string, args []string) *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:      tool,
		Command:   command,
		Args:      args,
		Timestamp: time.Now(),
		Duration:  2000 * time.Millisecond,
		ExitCode:  0,
	}
}

func decodeExecutionOutput(t *testing.T, output string) []core.ExecutionRecord {
	t.Helper()

	var records []core.ExecutionRecord
	if err := json.Unmarshal([]byte(output), &records); err != nil {
		t.Fatalf("Failed to decode JSON output %q: %v", output, err)
	}
	return records
}

func assertFirstExecutionCommand(t *testing.T, records []core.ExecutionRecord, command string) {
	t.Helper()

	if len(records) == 0 {
		t.Fatal("Expected at least one record in JSON output")
	}
	if records[0].Command != command {
		t.Fatalf("Expected %q, got: %s", command, records[0].Command)
	}
}

func assertCSVQueryOutput(t *testing.T, output string) {
	t.Helper()

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
	addTimeFilteredExecutions(t, config)

	output := runQueryCommandForTest(t, "--last", "24h")
	assertOutputOmits(t, output, "npm install old")
	assertOutputContains(t, output, "npm install recent")
}

func addTimeFilteredExecutions(t *testing.T, config *core.Config) {
	t.Helper()

	addStoredExecutionForTest(t, config, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install old",
		Args:      []string{"install", "old"},
		Timestamp: time.Now().Add(-48 * time.Hour),
		Duration:  1000 * time.Millisecond,
		ExitCode:  0,
	})
	addStoredExecutionForTest(t, config, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install recent",
		Args:      []string{"install", "recent"},
		Timestamp: time.Now().Add(-1 * time.Hour),
		Duration:  1000 * time.Millisecond,
		ExitCode:  0,
	})
}

func assertOutputOmits(t *testing.T, output, value string) {
	t.Helper()

	if strings.Contains(output, value) {
		t.Fatalf("Expected %q to be omitted, got: %q", value, output)
	}
}

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
	addStatsExecutions(t, config)

	output := captureStdout(t, func() {
		if err := showStats(statsCommandForTest(t), nil); err != nil {
			t.Fatalf("showStats failed: %v", err)
		}
	})

	assertStatsOutputHasData(t, output)
}

func addStatsExecutions(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	addTestExecution(t, store, statsNPMExecution())
	addTestExecution(t, store, statsHomebrewExecution())
	closeTestStore(t, store)
}

func statsNPMExecution() *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Command:          "npm install eslint",
		Args:             []string{"install", "eslint"},
		Timestamp:        time.Now(),
		Duration:         1500 * time.Millisecond,
		ExitCode:         0,
		PackagesAffected: []string{"eslint"},
	}
}

func statsHomebrewExecution() *core.ExecutionRecord {
	timestamp := time.Now().Add(-1 * time.Hour)
	duration := 2000 * time.Millisecond
	return &core.ExecutionRecord{
		Tool:             core.ToolHomebrew,
		Command:          "brew install jq",
		Args:             []string{"install", "jq"},
		Timestamp:        timestamp,
		Duration:         duration,
		ExitCode:         0,
		PackagesAffected: []string{"jq"},
	}
}

func assertStatsOutputHasData(t *testing.T, output string) {
	t.Helper()

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
	timestamp := time.Now().Add(-2 * 24 * time.Hour)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install test",
		Timestamp: timestamp,
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
	addListedPackage(t, config, &core.PackageInfo{
		Name:       "jq",
		Tool:       core.ToolHomebrew,
		Version:    "1.7",
		UsageCount: 5,
		LastUsed:   time.Now(),
		Path:       "/usr/local/bin/jq",
	})

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

func addListedPackage(t *testing.T, config *core.Config, pkg *core.PackageInfo) {
	t.Helper()

	store := openTestStore(t, config)
	updateTestPackage(t, store, pkg)
	closeTestStore(t, store)
}

func TestListPackagesWithToolFilter(t *testing.T) {
	config := setupTestHomeConfig(t)
	addFilteredPackages(t, config)

	output := captureStdout(t, func() {
		if err := listPackages(packagesCommandForTest(t, "--tool", "homebrew"), nil); err != nil {
			t.Fatalf("listPackages with tool filter failed: %v", err)
		}
	})

	assertFilteredPackageOutput(t, output)
}

func addFilteredPackages(t *testing.T, config *core.Config) {
	t.Helper()

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
}

func assertFilteredPackageOutput(t *testing.T, output string) {
	t.Helper()

	if !strings.Contains(output, "jq") {
		t.Fatalf("Expected 'jq' for homebrew filter, got: %q", output)
	}
	if strings.Contains(output, "eslint") {
		t.Fatalf("Should not include npm packages when filtered by homebrew, got: %q", output)
	}
}

func TestListPackagesUnusedFilter(t *testing.T) {
	config := setupTestHomeConfig(t)
	addUnusedFilterPackages(t, config)

	output := captureStdout(t, func() {
		if err := listPackages(packagesCommandForTest(t, "--unused", "30d"), nil); err != nil {
			t.Fatalf("listPackages with unused filter failed: %v", err)
		}
	})
	assertUnusedFilterOutput(t, output)
}

func addUnusedFilterPackages(t *testing.T, config *core.Config) {
	t.Helper()

	addListedPackage(t, config, oldHomebrewPackage())
	addListedPackage(t, config, recentHomebrewPackage())
}

func oldHomebrewPackage() *core.PackageInfo {
	lastUsed := time.Now().Add(-100 * 24 * time.Hour)
	return &core.PackageInfo{
		Name:       "old",
		Tool:       core.ToolHomebrew,
		UsageCount: 1,
		LastUsed:   lastUsed,
	}
}

func recentHomebrewPackage() *core.PackageInfo {
	return &core.PackageInfo{
		Name:       "recent",
		Tool:       core.ToolHomebrew,
		UsageCount: 5,
		LastUsed:   time.Now(),
	}
}

func assertUnusedFilterOutput(t *testing.T, output string) {
	t.Helper()

	if strings.Contains(output, "recent") {
		t.Fatalf("Recent package should not be in unused list, got: %q", output)
	}
	hasEmptyMessage := strings.Contains(output, "No unused packages found")
	hasOldPackage := strings.Contains(output, "old")
	hasExpectedOutput := hasEmptyMessage || hasOldPackage
	if !hasExpectedOutput {
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
	addFilteredPackages(t, config)

	output := captureStdout(t, func() {
		if err := checkPackages(checkCommandForTest(t, "--search", "jq", "--format", "json"), nil); err != nil {
			t.Fatalf("checkPackages with search failed: %v", err)
		}
	})
	assertPackageSearchOutput(t, output)
}

func assertPackageSearchOutput(t *testing.T, output string) {
	t.Helper()

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

	output := captureStderr(t, func() {
		if err := setConfig(&command{}, []string{"storage.retention_days", "30"}); err != nil {
			t.Fatalf("setConfig failed: %v", err)
		}
	})

	if !strings.Contains(output, "Configuration updated") {
		t.Fatalf("Expected 'Configuration updated', got: %q", output)
	}
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

	output := captureStderr(t, func() {
		if err := setupProject(&command{}, nil); err != nil {
			t.Fatalf("setupProject failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU setup completed") {
		t.Fatalf("Expected 'DIU setup completed', got: %q", output)
	}
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
	hasTwoHomes := len(distinct) == 2
	hasLegacyHome := hasTwoHomes && distinct[1] == "/legacy-home"
	if !hasLegacyHome {
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
	return captureStderr(t, run)
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
	prepareReadOnlySetupConfig(t)

	err := setupProject(&command{}, nil)
	if err == nil {
		t.Fatal("Expected setupProject to fail")
	}
	if !strings.Contains(err.Error(), "failed to save config") {
		t.Fatalf("Expected save error, got: %v", err)
	}
}

func prepareReadOnlySetupConfig(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	configPath := filepath.Join(homeDir, ".config", "diu", "config.json")
	saveWrapperlessConfig(t, configPath)
	makeConfigReadOnly(t, configPath)
}

func saveWrapperlessConfig(t *testing.T, path string) {
	t.Helper()

	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = []string{}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{}
	config.Monitoring.Process.ShouldAutoInstallWrappers = false
	if err := config.SaveTo(path); err != nil {
		t.Fatalf("Failed to save test config: %v", err)
	}
}

func makeConfigReadOnly(t *testing.T, path string) {
	t.Helper()

	if err := os.Chmod(path, 0400); err != nil {
		t.Fatalf("Failed to make config read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, core.PrivateFileMode)
	})
}

func TestScanPackages(t *testing.T) {
	prepareScanPackagesConfig(t)

	output := captureStderr(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})

	if !strings.Contains(output, "packages scanned") {
		t.Fatalf("Expected 'packages scanned' message, got: %q", output)
	}
}

func prepareScanPackagesConfig(t *testing.T) {
	t.Helper()

	config := setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())
	binDir := filepath.Join(t.TempDir(), "bin")
	createBinDir(t, binDir)
	writeExecutableForTest(t, filepath.Join(binDir, "jq"), "#!/bin/bash\nexit 0\n")
	saveScanPackagesConfig(t, config, binDir)
}

func createBinDir(t *testing.T, binDir string) {
	t.Helper()

	if err := os.MkdirAll(binDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create bin dir: %v", err)
	}
}

func saveScanPackagesConfig(t *testing.T, config *core.Config, binDir string) {
	t.Helper()

	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolHomebrew: {binDir},
	}
	config.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
}

func TestBackup(t *testing.T) {
	config := setupTestHomeConfig(t)

	output := captureStderr(t, func() {
		if err := backup(&command{}, nil); err != nil {
			t.Fatalf("backup failed: %v", err)
		}
	})

	if !strings.Contains(output, "Backup created") {
		t.Fatalf("Expected 'Backup created', got: %q", output)
	}
	backups, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(backups) == 0 {
		t.Fatal("Expected backup file to be created")
	}
}

func TestCleanup(t *testing.T) {
	config := prepareCleanupConfig(t)

	output := captureStderr(t, func() {
		if err := cleanup(&command{}, nil); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}
	})

	if !strings.Contains(output, "Cleanup completed") {
		t.Fatalf("Expected 'Cleanup completed', got: %q", output)
	}
	assertCleanupKeptRecentExecution(t, config)
}

func prepareCleanupConfig(t *testing.T) *core.Config {
	t.Helper()

	setupTestHomeConfig(t)
	config, _ := core.LoadConfig("")
	config.Storage.RetentionDays = 1
	if err := config.Save(); err != nil {
		t.Fatalf("config.Save() failed: %v", err)
	}
	addCleanupExecutions(t, config)
	return config
}

func addCleanupExecutions(t *testing.T, config *core.Config) {
	t.Helper()

	store, _ := storage.NewJSONStorage(config)
	addCleanupExecution(t, store, "npm install old", time.Now().Add(-48*time.Hour))
	addCleanupExecution(t, store, "npm install current", time.Now())
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() failed: %v", err)
	}
}

func addCleanupExecution(t *testing.T, store storage.Storage, command string, timestamp time.Time) {
	t.Helper()

	err := store.AddExecution(&core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   command,
		Timestamp: timestamp,
	})
	if err != nil {
		t.Fatalf("AddExecution failed: %v", err)
	}
}

func assertCleanupKeptRecentExecution(t *testing.T, config *core.Config) {
	t.Helper()

	store, _ := storage.NewJSONStorage(config)
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

func TestGetConfigAllKeys(t *testing.T) {
	setupTestHomeConfig(t)
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
			if key == "monitoring.enabled_tools" {
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

	for _, tt := range setConfigCases {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			assertSetConfigCase(t, tt)
		})
	}
}

type setConfigCase struct {
	key   string
	value string
}

var setConfigCases = []setConfigCase{
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

func assertSetConfigCase(t *testing.T, test setConfigCase) {
	t.Helper()

	output := captureStderr(t, func() {
		if err := setConfig(&command{}, []string{test.key, test.value}); err != nil {
			t.Fatalf("setConfig(%s, %s) failed: %v", test.key, test.value, err)
		}
	})
	if !strings.Contains(output, "Configuration updated") {
		t.Fatalf("Expected 'Configuration updated', got: %q", output)
	}
	assertConfigValue(t, test)
}

func assertConfigValue(t *testing.T, test setConfigCase) {
	t.Helper()

	getOutput := captureStdout(t, func() {
		if err := getConfig(&command{}, []string{test.key}); err != nil {
			t.Fatalf("getConfig(%s) failed: %v", test.key, err)
		}
	})
	expectedOutput := expectedConfigOutput(test)
	if strings.TrimSpace(getOutput) != expectedOutput {
		t.Fatalf("getConfig(%s) = %q, want %q", test.key, strings.TrimSpace(getOutput), expectedOutput)
	}
}

func expectedConfigOutput(test setConfigCase) string {
	if test.key == "monitoring.enabled_tools" {
		return strings.ReplaceAll(test.value, ",", ", ")
	}
	return test.value
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

func TestValidateExecutablePath(t *testing.T) {
	tempDir := t.TempDir()
	assertValidExecutablePath(t, tempDir)
	assertInvalidExecutablePaths(t, tempDir)
}

func assertValidExecutablePath(t *testing.T, tempDir string) {
	t.Helper()

	execPath := filepath.Join(tempDir, "tool")
	writeExecutableForTest(t, execPath, "#!/bin/bash\necho test\n")
	validated, err := validateExecutablePath(execPath)
	if err != nil {
		t.Fatalf("validateExecutablePath(%s) failed: %v", execPath, err)
	}
	if validated != execPath {
		t.Fatalf("validateExecutablePath() = %s, want %s", validated, execPath)
	}
}

func assertInvalidExecutablePaths(t *testing.T, tempDir string) {
	t.Helper()

	assertValidateExecutablePathFails(t, "")
	assertValidateExecutablePathFails(t, "relative/tool")
	dirPath := filepath.Join(tempDir, "dir")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	assertValidateExecutablePathFails(t, dirPath)
	nonExec := filepath.Join(tempDir, "notes.txt")
	if err := os.WriteFile(nonExec, []byte("not executable"), 0o644); err != nil {
		t.Fatalf("Failed to create non-executable file: %v", err)
	}
	assertValidateExecutablePathFails(t, nonExec)
	assertValidateExecutablePathFails(t, "/nonexistent/path/to/tool")
}

func assertValidateExecutablePathFails(t *testing.T, path string) {
	t.Helper()

	if _, err := validateExecutablePath(path); err == nil {
		t.Fatalf("Expected error for %q", path)
	}
}

func TestVersionString(t *testing.T) {
	vs := versionString()
	if vs == "" {
		t.Fatal("versionString returned empty")
	}
	if !strings.Contains(vs, "diu") {
		t.Fatalf("Expected 'diu' in version string, got: %q", vs)
	}
}

func TestCloseStore(t *testing.T) {
	config := setupTestHomeConfig(t)
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	closeStore(store)
}

func TestIsTerminal(t *testing.T) {
	result := isTerminal()
	_ = result
}

func TestGoBinaryDir(t *testing.T) {
	config := core.DefaultConfig()
	tempDir := t.TempDir()
	goBinPath := filepath.Join(tempDir, "go", "bin")
	if err := os.MkdirAll(goBinPath, 0o755); err != nil {
		t.Fatalf("Failed to create go bin path: %v", err)
	}
	config.Tools.Go.GoBin = goBinPath

	got := goBinaryDir(config)
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

func isDaemonRunning(*core.Config) bool {
	return true
}

func isDaemonStopped(*core.Config) bool {
	return false
}

func TestStartDaemonAlreadyRunning(t *testing.T) {
	setupTestHomeConfig(t)

	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()

	output := captureStderr(t, func() {
		if err := startDaemon(&command{}, nil); err != nil {
			t.Fatalf("startDaemon failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU daemon is already running") {
		t.Fatalf("Expected 'already running' message, got: %q", output)
	}
}

func TestStartDaemonForegroundReportsInvalidStorage(t *testing.T) {
	t.Setenv("DIU_DAEMON_FOREGROUND", "1")
	config := core.DefaultConfig()
	config.Storage.JSONFile = ""
	config.Daemon.PIDFile = filepath.Join(t.TempDir(), "diu.pid")
	config.Daemon.SocketPath = filepath.Join(t.TempDir(), "diu.sock")
	restore := SetDaemonChecker(isDaemonStopped)
	defer restore()

	err := startDaemonWithConfig(config)
	hasCreateDaemonError := err != nil && strings.Contains(err.Error(), "failed to create daemon")
	if !hasCreateDaemonError {
		t.Fatalf("startDaemonWithConfig error = %v", err)
	}
}

func TestCommandsReportInvalidConfig(t *testing.T) {
	t.Setenv("DIU_ACTIVITY", "never")
	t.Setenv("DIU_COLOR", "never")
	writeInvalidConfigForCommands(t)

	for _, test := range invalidConfigCommandCases {
		t.Run(test.name, func(t *testing.T) {
			assertCommandReportsInvalidConfig(t, test)
		})
	}
}

func writeInvalidConfigForCommands(t *testing.T) {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	configDir := filepath.Join(homeDir, ".config", "diu")
	if err := os.MkdirAll(configDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

type invalidConfigCommandCase struct {
	name string
	run  func(*testing.T) error
}

var invalidConfigCommandCases = []invalidConfigCommandCase{
	{name: "daemon start", run: func(t *testing.T) error { return startDaemon(&command{}, nil) }},
	{name: "daemon stop", run: func(t *testing.T) error { return stopDaemon(&command{}, nil) }},
	{name: "daemon restart", run: func(t *testing.T) error { return restartDaemon(&command{}, nil) }},
	{name: "daemon status", run: func(t *testing.T) error { return daemonStatus(&command{}, nil) }},
	{name: "query", run: func(t *testing.T) error { return queryExecutions(queryCommandForTest(t), nil) }},
	{name: "stats", run: func(t *testing.T) error { return showStats(statsCommandForTest(t), nil) }},
	{name: "packages", run: func(t *testing.T) error { return listPackages(packagesCommandForTest(t), nil) }},
	{name: "check", run: func(t *testing.T) error { return checkPackages(checkCommandForTest(t), nil) }},
	{name: "manage", run: func(t *testing.T) error { return managePackages(manageCommandForTest(t), nil) }},
	{name: "config get", run: func(t *testing.T) error { return getConfig(&command{}, []string{"api.port"}) }},
	{name: "config set", run: func(t *testing.T) error { return setConfig(&command{}, []string{"api.port", "8081"}) }},
	{name: "config list", run: func(t *testing.T) error { return listConfig(&command{}, nil) }},
	{name: "setup", run: func(t *testing.T) error { return setupProject(&command{}, nil) }},
	{name: "uninstall", run: func(t *testing.T) error { return uninstallProject(&command{}, nil) }},
	{name: "scan", run: func(t *testing.T) error { return scanPackages(&command{}, nil) }},
	{name: "cleanup", run: func(t *testing.T) error { return cleanup(&command{}, nil) }},
	{name: "backup", run: func(t *testing.T) error { return backup(&command{}, nil) }},
}

func assertCommandReportsInvalidConfig(t *testing.T, test invalidConfigCommandCase) {
	t.Helper()

	var err error
	captureStderr(t, func() { err = test.run(t) })
	hasConfigError := err != nil && strings.Contains(err.Error(), "failed to load config")
	if !hasConfigError {
		t.Fatalf("command error = %v", err)
	}
}

func TestStopDaemonNotRunning(t *testing.T) {
	setupTestHomeConfig(t)

	restore := SetDaemonChecker(isDaemonStopped)
	defer restore()

	output := captureStderr(t, func() {
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
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()
	writeDaemonStatusPIDFile(t)

	output := captureStdout(t, func() {
		if err := daemonStatus(&command{}, nil); err != nil {
			t.Fatalf("daemonStatus failed: %v", err)
		}
	})

	if !strings.Contains(output, "DIU daemon is running") {
		t.Fatalf("Expected 'is running' message, got: %q", output)
	}
}

func writeDaemonStatusPIDFile(t *testing.T) {
	t.Helper()

	config, _ := core.LoadConfig("")
	pidFile := config.Daemon.PIDFile
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		t.Fatalf("Failed to create PID directory: %v", err)
	}
	if err := os.WriteFile(pidFile, []byte("12345\n"), 0o644); err != nil {
		t.Fatalf("Failed to write PID file: %v", err)
	}
	defer func() {
		removeErr := os.Remove(pidFile)
		shouldLogRemoveErr := removeErr != nil && !os.IsNotExist(removeErr)
		if shouldLogRemoveErr {
			t.Logf("Failed to remove PID file: %v", removeErr)
		}
	}()
}

func TestCoreVersion(t *testing.T) {
	version := coreVersion()
	if version == "" {
		t.Fatal("coreVersion returned empty")
	}
	if !isAllowedCoreVersion(version) {
		t.Fatalf("coreVersion returned unexpected value: %q", version)
	}
}

func isAllowedCoreVersion(version string) bool {
	if version == core.Version {
		return true
	}
	return version == "dev"
}

func TestVersionStringConsistency(t *testing.T) {
	vs := versionString()
	if vs == "" {
		t.Fatal("versionString returned empty")
	}
	if !strings.HasPrefix(vs, "diu ") {
		t.Fatalf("versionString should start with 'diu ', got: %q", vs)
	}
	if !strings.Contains(vs, "commit ") {
		t.Fatalf("versionString should contain 'commit ', got: %q", vs)
	}
	if !strings.Contains(vs, "built ") {
		t.Fatalf("versionString should contain 'built ', got: %q", vs)
	}
	if !strings.Contains(vs, coreVersion()) {
		t.Fatalf("versionString should contain coreVersion, got: %q", vs)
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
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()

	if err := waitForDaemonStarted(&core.Config{}, time.Second); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitForDaemonStartedAfterPolling(t *testing.T) {
	checker := &startingChecker{startAfterCalls: 3}
	restore := SetDaemonChecker(checker.IsRunning)
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
	restore := SetDaemonChecker(isDaemonStopped)
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
	restore := SetDaemonChecker(isDaemonStopped)
	defer restore()

	if err := waitForDaemonStopped(&core.Config{}, time.Second); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitForDaemonStoppedAfterPolling(t *testing.T) {
	checker := &stoppingChecker{stopAfterCalls: 3}
	restore := SetDaemonChecker(checker.IsRunning)
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
	restore := SetDaemonChecker(checker.IsRunning)
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
	restore := SetDaemonChecker(checker.IsRunning)
	defer restore()

	oldStarter := daemonProcessStarter
	daemonProcessStarter = func(string, []string, *syscall.ProcAttr) error {
		return nil
	}
	defer func() {
		daemonProcessStarter = oldStarter
	}()

	output := captureStderr(t, func() {
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
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()
	requestErr := errors.New("failed to read daemon PID: file does not exist")
	restoreStop := stubDaemonStopRequest(requestErr)
	defer restoreStop()

	err := stopDaemonWithConfig(config)
	if err == nil {
		t.Fatal("expected missing PID file error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to read daemon PID") {
		t.Fatalf("expected PID read error, got %v", err)
	}
}

func TestStopDaemonWithConfigInvalidPID(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()
	requestErr := errors.New("failed to read daemon PID: invalid PID")
	restoreStop := stubDaemonStopRequest(requestErr)
	defer restoreStop()

	writeInvalidDaemonPIDFile(t, config)

	err := stopDaemonWithConfig(config)
	if err == nil {
		t.Fatal("expected invalid PID error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid PID") {
		t.Fatalf("expected invalid PID error, got %v", err)
	}
}

func writeInvalidDaemonPIDFile(t *testing.T, config *core.Config) {
	t.Helper()

	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}
	if err := os.WriteFile(config.Daemon.PIDFile, []byte("not-a-pid"), core.PrivateFileMode); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
}

func TestStopDaemonWithConfigSignalError(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(isDaemonRunning)
	defer restore()
	requestErr := errors.New("socket unavailable")
	restoreStop := stubDaemonStopRequest(requestErr)
	defer restoreStop()
	writeDaemonPIDFile(t, config, "999999999")

	err := stopDaemonWithConfig(config)
	if err == nil {
		t.Fatal("expected signal error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to stop daemon") {
		t.Fatalf("expected signal error, got %v", err)
	}
}

func writeDaemonPIDFile(t *testing.T, config *core.Config, pid string) {
	t.Helper()

	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}
	if err := os.WriteFile(config.Daemon.PIDFile, []byte(pid), core.PrivateFileMode); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}
}

func TestStopDaemonWithConfigRemovesStalePID(t *testing.T) {
	config := setupTestHomeConfig(t)
	restore := SetDaemonChecker(isDaemonStopped)
	defer restore()
	restoreStop := stubDaemonStopRequest(daemon.ErrNotRunning)
	defer restoreStop()

	if err := config.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}
	if err := os.WriteFile(config.Daemon.PIDFile, []byte("4242"), core.PrivateFileMode); err != nil {
		t.Fatalf("failed to write PID file: %v", err)
	}

	output := captureStderr(t, func() {
		if err := stopDaemonWithConfig(config); err != nil {
			t.Fatalf("stopDaemonWithConfig failed: %v", err)
		}
	})
	if !strings.Contains(output, "DIU daemon is not running") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func stubDaemonStopRequest(requestErr error) func() {
	old := daemonStopRequester
	daemonStopRequester = func(*core.Config) error {
		return requestErr
	}
	return func() {
		daemonStopRequester = old
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

func TestPackageBrowserQuitsCleanly(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{Name: "ripgrep", Tool: core.ToolHomebrew})
	closeTestStore(t, store)

	var runErr error
	var output string
	withStdin(t, "q\n", func() {
		output = captureStderr(t, func() {
			canUninstall := false
			runErr = runPackageBrowser(canUninstall)
		})
	})
	if runErr != nil {
		t.Fatalf("runPackageBrowser failed: %v", runErr)
	}
	hasTitle := strings.Contains(output, "DIU Packages")
	hasQuitAction := strings.Contains(output, "q quit")
	hasBrowserOutput := hasTitle && hasQuitAction
	if !hasBrowserOutput {
		t.Fatalf("browser output = %q", output)
	}
}

func TestPackageBrowserNavigatesSearchesAndShowsDetails(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	addBrowserNavigationPackages(t)

	input := "n\np\n/\ntarget\n1\nq\n"
	canUninstall := false
	output := runPackageBrowserForTest(t, input, canUninstall)
	for _, text := range []string{"Search: target", "target-package", "Version: 1.2.3", "Path: /opt/target"} {
		if !strings.Contains(output, text) {
			t.Fatalf("browser output = %q, want %q", output, text)
		}
	}
}

func addBrowserNavigationPackages(t *testing.T) {
	t.Helper()

	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	for index := 0; index < defaultPageSize; index++ {
		name := fmt.Sprintf("package-%02d", index)
		updateTestPackage(t, store, &core.PackageInfo{Name: name, Tool: core.ToolNPM})
	}
	updateTestPackage(t, store, browserTargetPackage())
	closeTestStore(t, store)
}

func browserTargetPackage() *core.PackageInfo {
	return &core.PackageInfo{
		Name:       "target-package",
		Tool:       core.ToolNPM,
		Version:    "1.2.3",
		Path:       "/opt/target",
		UsageCount: 4,
	}
}

func runPackageBrowserForTest(t *testing.T, input string, canUninstall bool) string {
	t.Helper()

	var runErr error
	var output string
	withStdin(t, input, func() {
		output = captureStderr(t, func() {
			runErr = runPackageBrowser(canUninstall)
		})
	})
	if runErr != nil {
		t.Fatalf("runPackageBrowser failed: %v", runErr)
	}
	return output
}

func TestPackageBrowserUninstallsConfirmedPackage(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	prependFakeCommand(t, "npm", "#!/bin/sh\nexit 0\n")
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{Name: "typescript", Tool: core.ToolNPM})
	closeTestStore(t, store)

	canUninstall := true
	output := runPackageBrowserForTest(t, "u\n1\ntypescript\nq\n", canUninstall)
	if !strings.Contains(output, "[ok] typescript uninstalled") {
		t.Fatalf("browser output = %q", output)
	}
	assertNoNPMPackagesTracked(t, config)
}

func assertNoNPMPackagesTracked(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	packages, err := store.GetPackages(core.ToolNPM)
	if err != nil {
		t.Fatalf("GetPackages failed: %v", err)
	}
	if len(packages) != 0 {
		t.Fatalf("packages after uninstall = %#v", packages)
	}
}

func TestPackageBrowserReportsInvalidSelectionsAndCancellation(t *testing.T) {
	t.Setenv("DIU_COLOR", "never")
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{Name: "typescript", Tool: core.ToolNPM})
	closeTestStore(t, store)

	input := "u\nbad\nu\n1\nwrong\ninvalid\nq\n"
	var runErr error
	var output string
	withStdin(t, input, func() {
		output = captureStderr(t, func() {
			canUninstall := true
			runErr = runPackageBrowser(canUninstall)
		})
	})
	if runErr != nil {
		t.Fatalf("runPackageBrowser failed: %v", runErr)
	}
	for _, text := range []string{"invalid selection: bad", "uninstall cancelled", "invalid selection: invalid"} {
		if !strings.Contains(output, text) {
			t.Fatalf("browser output = %q, want %q", output, text)
		}
	}
}

func TestUninstallByNameNotFound(t *testing.T) {
	setupTestHomeConfig(t)
	shouldAssumeYes := true
	shouldDryRun := false
	err := uninstallByName("nonexistent", "", shouldAssumeYes, shouldDryRun)
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found', got %v", err)
	}
}

func TestUninstallByNameRequiresYes(t *testing.T) {
	setupTestHomeConfig(t)
	shouldAssumeYes := false
	shouldDryRun := false
	err := uninstallByName("anything", "", shouldAssumeYes, shouldDryRun)
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

	shouldAssumeYes := true
	shouldDryRun := false
	err := uninstallByName("lodash", "", shouldAssumeYes, shouldDryRun)
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
		shouldAssumeYes := false
		shouldDryRun := true
		if err := uninstallByName("typescript", "", shouldAssumeYes, shouldDryRun); err != nil {
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
		shouldAssumeYes := false
		shouldDryRun := true
		if err := uninstallByName("ruff", "", shouldAssumeYes, shouldDryRun); err != nil {
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

	out := captureStderr(t, func() {
		shouldAssumeYes := true
		shouldDryRun := false
		if err := uninstallByName("typescript", "", shouldAssumeYes, shouldDryRun); err != nil {
			t.Fatalf("uninstall failed: %v", err)
		}
	})
	if !strings.Contains(out, "uninstalled") {
		t.Fatalf("expected uninstalled, got: %q", out)
	}
}

func TestScanPackagesNoEnabledTools(t *testing.T) {
	setupTestHomeConfig(t)
	out := captureStderr(t, func() {
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

	if err := installWrappers(config, nil); err != nil {
		t.Fatalf("expected nil for unknown tool, got %v", err)
	}
}

func TestInstallWrappersInitializesKnownTool(t *testing.T) {
	config := setupTestHomeConfig(t)
	config.Monitoring.EnabledTools = []string{core.ToolGoBinary}

	if err := installWrappers(config, nil); err != nil {
		t.Fatalf("installWrappers failed: %v", err)
	}
}
