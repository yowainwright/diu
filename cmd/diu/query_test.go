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
	"github.com/yowainwright/diu/internal/storage"
)

type querySummaryStore struct {
	storage.Storage
	summary storage.ExecutionSummary
	err     error
	query   *storage.QueryOptions
}

func (s querySummaryStore) SummarizeExecutions(opts storage.QueryOptions) (storage.ExecutionSummary, error) {
	if s.query != nil {
		*s.query = opts
	}
	return s.summary, s.err
}

func TestCollectStatsReportUsesStorageSummary(t *testing.T) {
	var query storage.QueryOptions
	toolCounts := map[string]int{core.ToolNPM: 42}
	summary := storage.ExecutionSummary{Total: 42, ToolCounts: toolCounts}
	store := statsReportErrorStore{querySummaryStore: querySummaryStore{summary: summary, query: &query}}
	since := time.Now().Add(-time.Hour)
	opts := storage.QueryOptions{Tool: core.ToolNPM, Since: &since}
	report, err := collectStatsReport(store, statsCommandOptions{query: opts})
	if err != nil {
		t.Fatal(err)
	}
	if query != opts {
		t.Fatalf("summary query = %#v, want %#v", query, opts)
	}
	assertStatsSummary(t, report)
}

func assertStatsSummary(t *testing.T, report statsReport) {
	t.Helper()
	totalMatches := report.TotalExecutions == 42
	toolMatches := report.ToolCounts[core.ToolNPM] == 42
	singleTool := len(report.ToolCounts) == 1
	summaryMatches := totalMatches && toolMatches && singleTool
	if !summaryMatches {
		t.Fatalf("report = %#v, want storage summary", report)
	}
}

func TestCollectStatsReportSummaryError(t *testing.T) {
	wantErr := errors.New("query failed")
	store := querySummaryStore{err: wantErr}
	_, err := collectStatsReport(store, statsCommandOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("collectStatsReport error = %v, want wrapped %v", err, wantErr)
	}
}

type statisticsErrorStore struct {
	querySummaryStore
	statsErr error
}

func (s statisticsErrorStore) Statistics() (*core.StorageStatistics, error) {
	return nil, s.statsErr
}

func TestPrintStatsReturnsStatisticsError(t *testing.T) {
	wantErr := errors.New("statistics unavailable")
	store := statisticsErrorStore{statsErr: wantErr}
	err := printStats(store, statsCommandOptions{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("printStats error = %v, want wrapped %v", err, wantErr)
	}
}

type statsReportErrorStore struct {
	querySummaryStore
	statsErr    error
	packagesErr error
}

func (s statsReportErrorStore) Statistics() (*core.StorageStatistics, error) {
	return &core.StorageStatistics{}, s.statsErr
}

func (s statsReportErrorStore) GetPackages(string) ([]*core.PackageInfo, error) {
	return nil, s.packagesErr
}

func TestStatsJSONStorageFailuresLeaveOutputEmpty(t *testing.T) {
	wantErr := errors.New("storage unavailable")
	stores := []statsReportErrorStore{
		{querySummaryStore: querySummaryStore{err: wantErr}},
		{statsErr: wantErr},
		{packagesErr: wantErr},
	}
	for _, store := range stores {
		assertStatsJSONFailure(t, store, wantErr)
	}
}

func TestLaunchAttemptQueryOmitsUnknownOutcome(t *testing.T) {
	record := &core.ExecutionRecord{
		Kind: core.ExecutionKindLaunchAttempt, Tool: core.ToolNPM, Command: "prettier",
		Timestamp: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC),
		Duration:  time.Second, ExitCode: 127, WorkingDir: "/repo",
	}
	assertLaunchAttemptTableOmitsOutcome(t, record)
	assertLaunchAttemptCSVOmitsOutcome(t, record)
}

func assertLaunchAttemptTableOmitsOutcome(t *testing.T, record *core.ExecutionRecord) {
	t.Helper()
	output := captureStdout(t, func() { printExecutionsTable([]*core.ExecutionRecord{record}) })
	hasExitCode := strings.Contains(output, "Exit code:")
	hasLocation := strings.Contains(output, "Location:")
	includesUnknownDetails := hasExitCode || hasLocation
	if includesUnknownDetails {
		t.Fatalf("launch attempt output included an execution outcome or location: %q", output)
	}
}

func assertLaunchAttemptCSVOmitsOutcome(t *testing.T, record *core.ExecutionRecord) {
	t.Helper()
	row := executionCSVRow(record)
	durationIsEmpty := row[3] == ""
	exitCodeIsEmpty := row[4] == ""
	locationIsEmpty := row[5] == ""
	fieldsAreEmpty := durationIsEmpty && exitCodeIsEmpty && locationIsEmpty
	if !fieldsAreEmpty {
		t.Fatalf("launch attempt CSV outcome fields = %v, want empty", row[3:])
	}
}

func assertStatsJSONFailure(t *testing.T, store statsStorage, wantErr error) {
	t.Helper()
	var err error
	output := captureStdout(t, func() {
		err = printStats(store, statsCommandOptions{top: 5, format: formatJSON})
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
	if output != "" {
		t.Fatalf("failed stats printed a report: %q", output)
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

func TestQueryExecutionsFormats(t *testing.T) {
	config := setupTestHomeConfig(t)
	addQueryFormatExecution(t, config)

	assertJSONQueryOutput(t)
	assertCSVExecutionQueryOutput(t)
	assertTableQueryOutput(t)
}

func addQueryFormatExecution(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Command:          "npm install eslint",
		Args:             []string{"install", "eslint"},
		Timestamp:        time.Now(),
		Duration:         1500 * time.Millisecond,
		ExitCode:         0,
		WorkingDir:       filepath.Join(os.Getenv("HOME"), "projects", "diu"),
		PackagesAffected: []string{"eslint"},
	})
	closeTestStore(t, store)
}

func assertJSONQueryOutput(t *testing.T) {
	t.Helper()

	jsonOutput := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t, "--tool", "npm", "--format", "json", "--limit", "1"), nil); err != nil {
			t.Fatalf("queryExecutions JSON failed: %v", err)
		}
	})
	var records []core.ExecutionRecord
	if err := json.Unmarshal([]byte(jsonOutput), &records); err != nil {
		t.Fatalf("Failed to decode JSON output %q: %v", jsonOutput, err)
	}
	hasOneRecord := len(records) == 1
	hasCommand := hasOneRecord && records[0].Command == "npm install eslint"
	if !hasCommand {
		t.Fatalf("Unexpected JSON records: %#v", records)
	}
}

func assertCSVExecutionQueryOutput(t *testing.T) {
	t.Helper()

	csvOutput := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t, "--format", "csv"), nil); err != nil {
			t.Fatalf("queryExecutions CSV failed: %v", err)
		}
	})
	hasCSVHeader := strings.Contains(csvOutput, "tool,command,timestamp,duration_ms,exit_code,working_dir")
	hasCSVCommand := strings.Contains(csvOutput, "npm install eslint")
	hasCSVOutput := hasCSVHeader && hasCSVCommand
	if !hasCSVOutput {
		t.Fatalf("Unexpected CSV output:\n%s", csvOutput)
	}
}

func assertTableQueryOutput(t *testing.T) {
	t.Helper()

	tableOutput := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t), nil); err != nil {
			t.Fatalf("queryExecutions table failed: %v", err)
		}
	})
	hasTableHeader := strings.Contains(tableOutput, "Execution History")
	hasTableLocation := strings.Contains(tableOutput, "Location: ~/projects/diu")
	hasTableOutput := hasTableHeader && hasTableLocation
	if !hasTableOutput {
		t.Fatalf("Unexpected table output:\n%s", tableOutput)
	}
}

func TestShowStatsUsesStorage(t *testing.T) {
	config := setupTestHomeConfig(t)
	addShowStatsStorageFixtures(t, config)

	output := captureStdout(t, func() {
		if err := showStats(statsCommandForTest(t, "--daily", "--tool", "npm", "--top", "1"), nil); err != nil {
			t.Fatalf("showStats failed: %v", err)
		}
	})
	assertShowStatsStorageOutput(t, output)
}

func addShowStatsStorageFixtures(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	addTestExecution(t, store, showStatsNPMExecution())
	addTestExecution(t, store, showStatsHomebrewExecution())
	updateTestPackage(t, store, showStatsPackage())
	closeTestStore(t, store)
}

func showStatsNPMExecution() *core.ExecutionRecord {
	return &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Command:          "npm install eslint",
		Args:             []string{"install", "eslint"},
		Timestamp:        time.Now(),
		PackagesAffected: []string{"eslint"},
	}
}

func showStatsHomebrewExecution() *core.ExecutionRecord {
	timestamp := time.Now().Add(-48 * time.Hour)
	return &core.ExecutionRecord{
		Tool:             core.ToolHomebrew,
		Command:          "brew install jq",
		Args:             []string{"install", "jq"},
		Timestamp:        timestamp,
		PackagesAffected: []string{"jq"},
	}
}

func showStatsPackage() *core.PackageInfo {
	return &core.PackageInfo{
		Name:       "eslint",
		Tool:       core.ToolNPM,
		UsageCount: 3,
		LastUsed:   time.Now(),
	}
}

func assertShowStatsStorageOutput(t *testing.T, output string) {
	t.Helper()

	hasTitle := strings.Contains(output, "DIU Statistics (Last 24 Hours)")
	hasTotal := strings.Contains(output, "Total executions:")
	hasPackage := strings.Contains(output, "eslint (npm)")
	outputOK := hasTitle && hasTotal && hasPackage
	if !outputOK {
		t.Fatalf("Unexpected stats output:\n%s", output)
	}
}

func queryCommandForTest(t *testing.T, args ...string) *command {
	t.Helper()
	cmd := &command{}
	var tool, pkg, last, format string
	var limit int
	cmd.Flags().StringVarP(&tool, "tool", "t", "", "tool")
	cmd.Flags().StringVarP(&pkg, "package", "p", "", "package")
	cmd.Flags().StringVarP(&last, "last", "l", "", "last")
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "limit")
	cmd.Flags().StringVarP(&format, "format", "f", formatTable, "format")
	parseTestFlags(t, cmd, args...)
	return cmd
}

func statsCommandForTest(t *testing.T, args ...string) *command {
	t.Helper()
	cmd := newStatsCommand()
	parseTestFlags(t, cmd, args...)
	return cmd
}
