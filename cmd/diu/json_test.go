package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func jsonCommandOutput(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv("DIU_COLOR", "always")
	return captureStdout(t, func() {
		if err := newRootCommand().Execute(args); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReportingJSONEmpty(t *testing.T) {
	setupTestHomeConfig(t)
	for _, name := range []string{"query", "check", "packages"} {
		output := jsonCommandOutput(t, name, "--format", "json")
		if strings.TrimSpace(output) != "[]" {
			t.Fatalf("%s empty output = %q, want []", name, output)
		}
	}
	output := jsonCommandOutput(t, "stats", "--format", "json")
	var report map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	assertJSONField(t, report, "total_executions", "0")
	assertJSONField(t, report, "tool_counts", "{}")
	assertJSONField(t, report, "top_packages", "[]")
}

func assertJSONField(t *testing.T, report map[string]json.RawMessage, key, want string) {
	t.Helper()
	if string(report[key]) != want {
		t.Fatalf("%s = %s, want %s", key, report[key], want)
	}
}

func seedJSONPackages(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	old := time.Now().Add(-60 * 24 * time.Hour)
	updateTestPackage(t, store, &core.PackageInfo{Name: "old", Tool: core.ToolNPM, LastUsed: old, UsageCount: 10})
	updateTestPackage(t, store, &core.PackageInfo{Name: "recent", Tool: core.ToolNPM, LastUsed: time.Now(), UsageCount: 2})
	updateTestPackage(t, store, &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, LastUsed: old, UsageCount: 20})
	closeTestStore(t, store)
}

func TestPackagesJSONFilters(t *testing.T) {
	config := setupTestHomeConfig(t)
	seedJSONPackages(t, config)
	output := jsonCommandOutput(t, "packages", "-f", "json", "--tool", "npm", "--unused", "30d")
	var packages []core.PackageInfo
	if err := json.Unmarshal([]byte(output), &packages); err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 {
		t.Fatalf("packages = %#v", packages)
	}
	if packages[0].Name != "old" {
		t.Fatalf("package = %#v", packages[0])
	}
}

func TestPackagesJSONNoFilterMatches(t *testing.T) {
	config := setupTestHomeConfig(t)
	seedJSONPackages(t, config)
	output := jsonCommandOutput(t, "packages", "--format", "json", "--unused", "365d")
	if strings.TrimSpace(output) != "[]" {
		t.Fatalf("output = %q, want []", output)
	}
}

func seedJSONExecutions(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{Tool: core.ToolNPM, Timestamp: time.Now()})
	old := time.Now().Add(-10 * 24 * time.Hour)
	addTestExecution(t, store, &core.ExecutionRecord{Tool: core.ToolNPM, Timestamp: old})
	addTestExecution(t, store, &core.ExecutionRecord{Tool: core.ToolHomebrew, Timestamp: time.Now()})
	closeTestStore(t, store)
}

func TestStatsJSONFiltersAndRanking(t *testing.T) {
	config := setupTestHomeConfig(t)
	seedJSONPackages(t, config)
	seedJSONExecutions(t, config)
	for _, period := range []string{"--daily", "--weekly"} {
		output := jsonCommandOutput(t, "stats", "-f", "json", "--tool", "npm", period, "--top", "1")
		assertFilteredJSONStats(t, output)
	}
	output := jsonCommandOutput(t, "stats", "--format", "json", "--top", "0")
	var report map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	assertJSONField(t, report, "total_executions", "3")
	assertJSONField(t, report, "top_packages", "[]")
}

type jsonStatsResult struct {
	Total         int                `json:"total_executions"`
	Tools         map[string]int     `json:"tool_counts"`
	Packages      []core.PackageInfo `json:"top_packages"`
	MostActiveDay string             `json:"most_active_day"`
}

func assertFilteredJSONStats(t *testing.T, output string) {
	t.Helper()
	var report jsonStatsResult
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	singleTool := len(report.Tools) == 1
	onlyNPM := report.Tools[core.ToolNPM] == 1 && singleTool
	countsMatch := report.Total == 1 && onlyNPM
	if !countsMatch {
		t.Fatalf("filtered totals = %#v", report)
	}
	assertJSONTopPackage(t, report.Packages)
	if report.MostActiveDay != "" {
		t.Fatalf("filtered stats include global most active day: %s", report.MostActiveDay)
	}
}

func assertJSONTopPackage(t *testing.T, packages []core.PackageInfo) {
	t.Helper()
	if len(packages) != 1 {
		t.Fatalf("ranking = %#v", packages)
	}
	if packages[0].Name != "old" {
		t.Fatalf("ranking = %#v", packages)
	}
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

func TestNewReportingFormatsRejectInvalidValues(t *testing.T) {
	setupTestHomeConfig(t)
	for _, name := range []string{"stats", "status", "packages"} {
		assertInvalidReportFormats(t, name)
	}
}

func assertInvalidReportFormats(t *testing.T, name string) {
	t.Helper()
	for _, format := range []string{"", "yaml", "csv"} {
		assertInvalidReportingFlags(t, []string{name, "--format", format})
	}
}
