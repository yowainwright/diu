package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
	"github.com/yowainwright/diu/internal/storage"
)

func TestPackageNameForExecutable(t *testing.T) {
	for _, tt := range packageNameForExecutableCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := packageNameForExecutable(tt.tool, tt.path, tt.cmd); got != tt.want {
				t.Errorf("packageNameForExecutable(%q, %q, %q) = %q, want %q", tt.tool, tt.path, tt.cmd, got, tt.want)
			}
		})
	}
}

type packageNameForExecutableCase struct {
	name string
	tool string
	path string
	cmd  string
	want string
}

var packageNameForExecutableCases = []packageNameForExecutableCase{
	{
		name: "homebrew cellar path",
		tool: core.ToolHomebrew,
		path: "/opt/homebrew/Cellar/jq/1.8.1/bin/jq",
		cmd:  "jq",
		want: "jq",
	},
	{
		name: "npm scoped package path",
		tool: core.ToolNPM,
		path: "/opt/homebrew/lib/node_modules/@scope/tool/bin/tool",
		cmd:  "tool",
		want: "@scope/tool",
	},
	{
		name: "go binary fallback",
		tool: core.ToolGo,
		path: "/Users/test/go/bin/golangci-lint",
		cmd:  "golangci-lint",
		want: "golangci-lint",
	},
}

func TestShouldSkipExecutableWrapper(t *testing.T) {
	for command, expected := range shouldSkipExecutableWrapperCases {
		if got := shouldSkipExecutableWrapper(command); got != expected {
			t.Errorf("shouldSkipExecutableWrapper(%q) = %v, want %v", command, got, expected)
		}
	}
}

var shouldSkipExecutableWrapperCases = map[string]bool{
	"":        true,
	".hidden": true,
	"diu":     true,
	"brew":    true,
	"jq":      false,
}

func TestFilterPackagesSearchAndUnused(t *testing.T) {
	packages := filterPackageFixtures()
	assertPackageFilter(t, packages, packageListOptions{Search: "jq"}, "jq")
	assertPackageFilter(t, packages, packageListOptions{Unused: "24h"}, "ripgrep")
}

func filterPackageFixtures() []*core.PackageInfo {
	return []*core.PackageInfo{
		{Name: "jq", Tool: core.ToolHomebrew, UsageCount: 3, LastUsed: time.Now()},
		{Name: "ripgrep", Tool: core.ToolHomebrew, UsageCount: 0},
	}
}

func assertPackageFilter(
	t *testing.T,
	packages []*core.PackageInfo,
	options packageListOptions,
	wantName string,
) {
	t.Helper()

	filtered, err := filterPackages(packages, options)
	if err != nil {
		t.Fatalf("filterPackages failed: %v", err)
	}
	hasExpectedPackage := len(filtered) == 1 && filtered[0].Name == wantName
	if !hasExpectedPackage {
		t.Fatalf("Expected only %s, got %v", wantName, filtered)
	}
}

func TestPrintPackageListNumbersFromOne(t *testing.T) {
	packages := []*core.PackageInfo{
		{Name: "jq", Tool: core.ToolHomebrew},
		{Name: "eslint", Tool: core.ToolNPM},
	}

	var printErr error
	output := captureStdout(t, func() {
		printErr = printPackageList(packages, formatTable)
	})
	if printErr != nil {
		t.Fatalf("printPackageList failed: %v", printErr)
	}

	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	hasLines := len(lines) > 0
	hasFirstNumber := hasLines && strings.HasPrefix(lines[0], "1 ")
	if !hasFirstNumber {
		t.Fatalf("first package row = %q, want numbering from 1", output)
	}
}

func TestUninstallPlan(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	prependFakeCommand(t, pip3CommandName, "#!/bin/sh\nexit 0\n")

	for _, tt := range uninstallPlanCases {
		t.Run(tt.name, func(t *testing.T) {
			assertUninstallPlan(t, tt)
		})
	}
}

type uninstallPlanCase struct {
	name string
	pkg  *core.PackageInfo
	want []string
}

var uninstallPlanCases = []uninstallPlanCase{
	{
		name: "homebrew",
		pkg:  &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew},
		want: []string{homebrewCommandName, uninstallSubcommand, "jq"},
	},
	{
		name: "npm",
		pkg:  &core.PackageInfo{Name: "eslint", Tool: core.ToolNPM},
		want: []string{npmCommandName, uninstallSubcommand, npmGlobalFlag, "eslint"},
	},
	{
		name: "pnpm",
		pkg:  &core.PackageInfo{Name: "tsx", Tool: core.ToolPNPM},
		want: []string{pnpmCommandName, removeSubcommand, npmGlobalFlag, "tsx"},
	},
	{
		name: "bun",
		pkg:  &core.PackageInfo{Name: "prettier", Tool: core.ToolBun},
		want: []string{bunCommandName, removeSubcommand, npmGlobalFlag, "prettier"},
	},
	{
		name: "pip",
		pkg:  &core.PackageInfo{Name: "ruff", Tool: core.ToolPip},
		want: []string{pip3CommandName, uninstallSubcommand, pipYesFlag, "ruff"},
	},
	{
		name: "uv",
		pkg:  &core.PackageInfo{Name: "black", Tool: core.ToolUV},
		want: []string{uvCommandName, "tool", uninstallSubcommand, "black"},
	},
	{
		name: "go executable",
		pkg:  &core.PackageInfo{Name: "golangci-lint", Tool: core.ToolGo, Path: "/Users/test/go/bin/golangci-lint"},
		want: []string{removeFilePlan},
	},
}

func assertUninstallPlan(t *testing.T, test uninstallPlanCase) {
	t.Helper()

	got, err := uninstallPlan(test.pkg)
	if err != nil {
		t.Fatalf("uninstallPlan failed: %v", err)
	}
	if strings.Join(got, " ") != strings.Join(test.want, " ") {
		t.Errorf("uninstallPlan() = %v, want %v", got, test.want)
	}
}

func TestValidatePackageManagerName(t *testing.T) {
	for _, name := range validPackageManagerNames {
		t.Run(name, func(t *testing.T) {
			if err := validatePackageManagerName(name); err != nil {
				t.Fatalf("validatePackageManagerName(%q) failed: %v", name, err)
			}
		})
	}

	for _, name := range invalidPackageManagerNames {
		t.Run(name, func(t *testing.T) {
			if err := validatePackageManagerName(name); err == nil {
				t.Fatalf("validatePackageManagerName(%q) should fail", name)
			}
		})
	}
}

var validPackageManagerNames = []string{
	"ripgrep",
	"@scope/tool",
	"owner/tap/tool",
}

var invalidPackageManagerNames = []string{
	"--help",
	"../tool",
	"tool;rm",
}

func TestValidateRemovableExecutablePath(t *testing.T) {
	tempDir := t.TempDir()
	executablePath := writeRemovableExecutable(t, tempDir)

	validated, err := validateRemovableExecutablePath(executablePath)
	if err != nil {
		t.Fatalf("validateRemovableExecutablePath failed: %v", err)
	}
	if validated != executablePath {
		t.Errorf("validateRemovableExecutablePath() = %s, want %s", validated, executablePath)
	}
	assertNonExecutablePathRejected(t, tempDir)
}

func writeRemovableExecutable(t *testing.T, tempDir string) string {
	t.Helper()

	executablePath := filepath.Join(tempDir, "tool")
	if err := os.WriteFile(executablePath, []byte("#!/bin/bash\nexit 0\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write executable: %v", err)
	}
	if err := os.Chmod(executablePath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark executable: %v", err)
	}
	return executablePath
}

func assertNonExecutablePathRejected(t *testing.T, tempDir string) {
	t.Helper()

	nonExecutablePath := filepath.Join(tempDir, "notes.txt")
	if err := os.WriteFile(nonExecutablePath, []byte("not executable\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write non-executable: %v", err)
	}
	if _, err := validateRemovableExecutablePath(nonExecutablePath); err == nil {
		t.Fatal("Expected non-executable path validation to fail")
	}
}

func TestRecordExecutionWritesToConfiguredStorage(t *testing.T) {
	config := setupTestHomeConfig(t)
	runRecordExecution(t, homebrewExecutionPayload)
	assertStoredHomebrewExecution(t, config)
}

const homebrewExecutionPayload = `{
	"tool":"brew",
	"command":"brew install jq",
	"args":["install","jq"],
	"exit_code":0,
	"duration_ms":1200,
	"packages_affected":["jq"]
}`

func runRecordExecution(t *testing.T, payload string) {
	t.Helper()

	var runErr error
	withStdin(t, payload, func() {
		runErr = recordExecution(&command{}, nil)
	})
	if runErr != nil {
		t.Fatalf("recordExecution failed: %v", runErr)
	}
}

func assertStoredHomebrewExecution(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	executions, err := store.GetExecutions(storage.QueryOptions{Tool: core.ToolHomebrew})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("Expected 1 homebrew execution, got %d", len(executions))
	}
	if executions[0].Tool != core.ToolHomebrew {
		t.Fatalf("Tool = %q, want %q", executions[0].Tool, core.ToolHomebrew)
	}
}

func TestRecordExecutionDropsConcurrentFallback(t *testing.T) {
	config := setupTestHomeConfig(t)
	lock := acquireFallbackRecordLockForTest(t, config)
	defer releaseFallbackRecordLockForTest(t, lock)
	payload := `{"tool":"brew","command":"brew install jq"}`
	withStdin(t, payload, func() {
		err := recordExecution(&command{}, nil)
		hasBusyError := err != nil && strings.Contains(err.Error(), "remained busy")
		if !hasBusyError {
			t.Fatalf("recordExecution error = %v", err)
		}
	})
	assertFallbackRecordDropped(t, config)
}

func acquireFallbackRecordLockForTest(t *testing.T, config *core.Config) *os.File {
	t.Helper()

	lock, acquired, err := tryAcquireFallbackRecordLock(config.Storage.JSONFile)
	lockAcquired := err == nil && acquired
	if !lockAcquired {
		t.Fatalf("tryAcquireFallbackRecordLock = %v, %v", acquired, err)
	}
	return lock
}

func releaseFallbackRecordLockForTest(t *testing.T, lock *os.File) {
	t.Helper()

	if err := releaseFallbackRecordLock(lock); err != nil {
		t.Fatalf("releaseFallbackRecordLock failed: %v", err)
	}
}

func assertFallbackRecordDropped(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	executions, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	if len(executions) != 0 {
		t.Fatalf("concurrent fallback stored %d executions", len(executions))
	}
	report := collectDiagnosticReport(config)
	if !report.Fallback.ContentionDetected {
		t.Fatal("diagnostics did not report fallback contention")
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

func TestPackageCommandsUseStorage(t *testing.T) {
	config := setupTestHomeConfig(t)
	addPackageCommandFixtures(t, config)
	assertListPackagesUsesStorage(t)
	assertCheckPackagesUsesStorage(t)
	assertManagePackagesDryRun(t)
}

func addPackageCommandFixtures(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "jq",
		Tool:       core.ToolHomebrew,
		Version:    "1.7",
		UsageCount: 4,
		LastUsed:   time.Now().Add(-48 * time.Hour),
		Path:       "/opt/homebrew/bin/jq",
	})
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "eslint",
		Tool:       core.ToolNPM,
		Version:    "9.0.0",
		UsageCount: 2,
	})
	closeTestStore(t, store)
}

func assertListPackagesUsesStorage(t *testing.T) {
	t.Helper()

	listOutput := captureStdout(t, func() {
		if err := listPackages(packagesCommandForTest(t, "--tool", "homebrew"), nil); err != nil {
			t.Fatalf("listPackages failed: %v", err)
		}
	})
	hasListTitle := strings.Contains(listOutput, "Tracked Packages")
	hasListPackage := strings.Contains(listOutput, "jq (1.7)")
	hasListOutput := hasListTitle && hasListPackage
	if !hasListOutput {
		t.Fatalf("Unexpected list output:\n%s", listOutput)
	}
}

func assertCheckPackagesUsesStorage(t *testing.T) {
	t.Helper()

	checkOutput := captureStdout(t, func() {
		if err := checkPackages(checkCommandForTest(t, "--search", "eslint", "--format", "json"), nil); err != nil {
			t.Fatalf("checkPackages failed: %v", err)
		}
	})
	var packages []core.PackageInfo
	if err := json.Unmarshal([]byte(checkOutput), &packages); err != nil {
		t.Fatalf("Failed to decode package JSON %q: %v", checkOutput, err)
	}
	hasPackageCount := len(packages) == 1
	hasPackageName := hasPackageCount && packages[0].Name == "eslint"
	if !hasPackageName {
		t.Fatalf("Unexpected check packages: %#v", packages)
	}
}

func assertManagePackagesDryRun(t *testing.T) {
	t.Helper()

	manageOutput := captureStdout(t, func() {
		if err := managePackages(manageCommandForTest(t, "--uninstall", "jq", "--tool", "homebrew", "--dry-run"), nil); err != nil {
			t.Fatalf("managePackages dry-run failed: %v", err)
		}
	})
	if strings.TrimSpace(manageOutput) != "brew uninstall jq" {
		t.Fatalf("Dry-run output = %q, want brew uninstall jq", manageOutput)
	}
}

func TestConfigCommandsAndMaintenance(t *testing.T) {
	config := setupTestHomeConfig(t)
	assertConfigSetGetAndList(t)
	addMaintenanceExecutionFixtures(t, config)
	assertBackupCommandCreatesFile(t, config)
	assertCleanupKeepsCurrentExecution(t, config)
}

func assertConfigSetGetAndList(t *testing.T) {
	t.Helper()

	setOutput := captureStderr(t, func() {
		if err := setConfig(&command{}, []string{"storage.retention_days", "30"}); err != nil {
			t.Fatalf("setConfig failed: %v", err)
		}
	})
	if !strings.Contains(setOutput, "Configuration updated") {
		t.Fatalf("Unexpected setConfig output: %q", setOutput)
	}

	getOutput := captureStdout(t, func() {
		if err := getConfig(&command{}, []string{"storage.retention_days"}); err != nil {
			t.Fatalf("getConfig failed: %v", err)
		}
	})
	if strings.TrimSpace(getOutput) != "30" {
		t.Fatalf("retention_days = %q, want 30", getOutput)
	}

	listOutput := captureStdout(t, func() {
		if err := listConfig(&command{}, nil); err != nil {
			t.Fatalf("listConfig failed: %v", err)
		}
	})
	var listed core.Config
	if err := json.Unmarshal([]byte(listOutput), &listed); err != nil {
		t.Fatalf("Failed to decode config list output: %v", err)
	}
	if listed.Storage.RetentionDays != 30 {
		t.Fatalf("Listed retention_days = %d, want 30", listed.Storage.RetentionDays)
	}
}

func addMaintenanceExecutionFixtures(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	oldTimestamp := time.Now().Add(-60 * 24 * time.Hour)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install old",
		Timestamp: oldTimestamp,
	})
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install current",
		Timestamp: time.Now(),
	})
	closeTestStore(t, store)
}

func assertBackupCommandCreatesFile(t *testing.T, config *core.Config) {
	t.Helper()

	backupOutput := captureStderr(t, func() {
		if err := backup(&command{}, nil); err != nil {
			t.Fatalf("backup failed: %v", err)
		}
	})
	if !strings.Contains(backupOutput, "Backup created") {
		t.Fatalf("Unexpected backup output: %q", backupOutput)
	}
	backups, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(backups) == 0 {
		t.Fatal("Expected backup file to be created")
	}
}

func assertCleanupKeepsCurrentExecution(t *testing.T, config *core.Config) {
	t.Helper()

	cleanupOutput := captureStderr(t, func() {
		if err := cleanup(&command{}, nil); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}
	})
	if !strings.Contains(cleanupOutput, "Cleanup completed") {
		t.Fatalf("Unexpected cleanup output: %q", cleanupOutput)
	}

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	executions, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	hasOneExecution := len(executions) == 1
	hasCurrentCommand := hasOneExecution && executions[0].Command == "npm install current"
	if !hasCurrentCommand {
		t.Fatalf("Unexpected executions after cleanup: %#v", executions)
	}
}

func TestPackageAndFormattingHelpers(t *testing.T) {
	packages := formattingPackageFixtures()
	assertPackageSortsByUsage(t, packages)
	assertPackageSelection(t, packages)
	assertFormatLastUsedNever(t)
	assertPackageDetailOutput(t)
}

func formattingPackageFixtures() []*core.PackageInfo {
	highLastUsed := time.Now().Add(-24 * time.Hour)
	return []*core.PackageInfo{
		{Name: "low", Tool: core.ToolNPM, UsageCount: 1, LastUsed: time.Now()},
		{Name: "high", Tool: core.ToolHomebrew, UsageCount: 5, LastUsed: highLastUsed},
	}
}

func assertPackageSortsByUsage(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	sortPackages(packages)
	if packages[0].Name != "high" {
		t.Fatalf("sortPackages placed %q first, want high", packages[0].Name)
	}
}

func assertPackageSelection(t *testing.T, packages []*core.PackageInfo) {
	t.Helper()

	pkg, err := packageBySelection(packages, 0, "2")
	selectedLow := err == nil && pkg.Name == "low"
	if !selectedLow {
		t.Fatalf("packageBySelection = %#v, %v; want low", pkg, err)
	}
	if _, err := packageBySelection(packages, 0, "abc"); err == nil {
		t.Fatal("Expected invalid selection to fail")
	}
}

func assertFormatLastUsedNever(t *testing.T) {
	t.Helper()

	if got := formatLastUsed(time.Time{}); got != "never" {
		t.Fatalf("formatLastUsed zero = %q, want never", got)
	}
}

func assertPackageDetailOutput(t *testing.T) {
	t.Helper()

	detailOutput := captureStderr(t, func() {
		printPackageDetail(&core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, Version: "1.7", Path: "/tmp/jq"})
	})
	hasName := strings.Contains(detailOutput, "jq")
	hasVersion := strings.Contains(detailOutput, "Version:")
	hasDetail := hasName && hasVersion
	if !hasDetail {
		t.Fatalf("Unexpected package detail output:\n%s", detailOutput)
	}
}

func TestPackageRowsPreserveFourDigitSelections(t *testing.T) {
	packages := []*core.PackageInfo{{Name: "package-1000", Tool: core.ToolNPM}}
	rows := packageRows(packages, 999)

	hasOneRow := len(rows) == 1
	hasSelection := hasOneRow && strings.HasPrefix(rows[0], "1000")
	if !hasSelection {
		t.Fatalf("packageRows() = %#v, want full selection number", rows)
	}
}

func TestDurationAndWrapperHelpers(t *testing.T) {
	assertParsedDuration(t, "2d", 48*time.Hour)
	assertParsedDuration(t, "1w", 7*24*time.Hour)
	assertParsedDuration(t, "1mo", 30*24*time.Hour)
	assertParsedDuration(t, "30m", 30*time.Minute)
	assertParsedDuration(t, "3h", 3*time.Hour)

	tempDir := t.TempDir()
	assertExecutableWrapperPath(t, tempDir)
	assertOwnerExecutableFile(t, tempDir)
}

func assertExecutableWrapperPath(t *testing.T, tempDir string) {
	t.Helper()

	path, err := executableWrapperPath(tempDir, "tool")
	if err != nil {
		t.Fatalf("executableWrapperPath failed: %v", err)
	}
	if path != filepath.Join(tempDir, "tool") {
		t.Fatalf("wrapper path = %s, want %s", path, filepath.Join(tempDir, "tool"))
	}
	if _, err := executableWrapperPath(tempDir, "../tool"); err == nil {
		t.Fatal("Expected escaping wrapper name to fail")
	}
}

func assertOwnerExecutableFile(t *testing.T, tempDir string) {
	t.Helper()

	written := filepath.Join(tempDir, "written")
	if err := writeOwnerExecutableFile(written, []byte("#!/bin/bash\n")); err != nil {
		t.Fatalf("writeOwnerExecutableFile failed: %v", err)
	}
	info, err := os.Stat(written)
	if err != nil {
		t.Fatalf("Failed to stat written wrapper: %v", err)
	}
	if info.Mode().Perm() != core.OwnerExecutableMode {
		t.Fatalf("wrapper mode = %v, want %v", info.Mode().Perm(), core.OwnerExecutableMode)
	}
}

func assertParsedDuration(t *testing.T, value string, want time.Duration) {
	t.Helper()

	got, err := parseDuration(value)
	matches := err == nil && got == want
	if !matches {
		t.Fatalf("parseDuration %s = %s, %v; want %s", value, got, err, want)
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

func TestSetupProjectInitializesStorageWithoutWrappers(t *testing.T) {
	config := setupTestHomeConfig(t)

	output := captureStderr(t, func() {
		if err := setupProject(&command{}, nil); err != nil {
			t.Fatalf("setupProject failed: %v", err)
		}
	})
	if !strings.Contains(output, "DIU setup completed") {
		t.Fatalf("Unexpected setup output: %q", output)
	}
	if _, err := os.Stat(config.Storage.JSONFile); err != nil {
		t.Fatalf("Expected storage file to exist: %v", err)
	}
}

func TestSetupProjectSkipsUnavailableManagers(t *testing.T) {
	config := setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())
	config.Monitoring.EnabledTools = []string{core.ToolPoetry}
	config.Monitoring.Process.AutoInstallWrappers = true
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	output := captureStderr(t, func() {
		if err := setupProject(&command{}, nil); err != nil {
			t.Fatalf("setupProject failed: %v", err)
		}
	})
	if strings.Contains(output, "failed to install poetry wrapper") {
		t.Fatalf("Unavailable manager should be skipped without a warning: %q", output)
	}
}

func TestScanPackagesDiscoversExecutableWrappers(t *testing.T) {
	config := setupTestHomeConfig(t)
	binDir := configureExecutableWrapperScan(t, config)

	output := captureStderr(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})
	if !strings.Contains(output, "1 packages scanned") {
		t.Fatalf("Unexpected scan output: %q", output)
	}
	assertScannedWrapperPackage(t, config, binDir)
}

func configureExecutableWrapperScan(t *testing.T, config *core.Config) string {
	t.Helper()

	t.Setenv("PATH", t.TempDir())

	binDir := executableWrapperScanBinDir(t)
	saveExecutableWrapperScanConfig(t, config, binDir)
	return binDir
}

func executableWrapperScanBinDir(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	writeExecutableForTest(t, filepath.Join(binDir, "jq"), "#!/bin/bash\nexit 0\n")
	if err := os.WriteFile(filepath.Join(binDir, "notes"), []byte("not executable"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write non-executable: %v", err)
	}
	writeExecutableForTest(t, filepath.Join(binDir, "brew"), "#!/bin/bash\nexit 0\n")
	return binDir
}

func saveExecutableWrapperScanConfig(t *testing.T, config *core.Config, binDir string) {
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

func assertScannedWrapperPackage(t *testing.T, config *core.Config, binDir string) {
	t.Helper()

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	pkg, err := store.GetPackage(core.ToolHomebrew, "jq")
	if err != nil {
		t.Fatalf("Expected scanned jq package: %v", err)
	}
	if pkg.Path != filepath.Join(binDir, "jq") {
		t.Fatalf("Package path = %s, want scanned executable path", pkg.Path)
	}
}

func TestMergeExistingPackageMigratesLegacyGoUsage(t *testing.T) {
	legacy, lastUsed := legacyGoPackageFixture()
	inventory := legacyGoInventory(legacy)
	pkg := &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary}

	mergeExistingPackage(inventory, pkg)
	assertLegacyGoUsageMigrated(t, pkg, legacy, lastUsed)
	assertGoInventoryScopes(t)
}

func legacyGoPackageFixture() (*core.PackageInfo, time.Time) {
	lastUsed := time.Now().Add(-time.Hour)
	return &core.PackageInfo{
		Name:       "gopls",
		Tool:       core.ToolGo,
		LastUsed:   lastUsed,
		UsageCount: 12,
	}, lastUsed
}

func legacyGoInventory(legacy *core.PackageInfo) map[string]map[string]*core.PackageInfo {
	return map[string]map[string]*core.PackageInfo{
		core.ToolGo: {"gopls": legacy},
	}
}

func assertLegacyGoUsageMigrated(t *testing.T, pkg, legacy *core.PackageInfo, lastUsed time.Time) {
	t.Helper()

	legacyUsageMigrated := pkg.UsageCount == legacy.UsageCount
	legacyTimeMigrated := pkg.LastUsed.Equal(lastUsed)
	legacyMetadataMigrated := legacyUsageMigrated && legacyTimeMigrated
	if !legacyMetadataMigrated {
		t.Fatalf("migrated package = %#v", pkg)
	}
}

func assertGoInventoryScopes(t *testing.T) {
	t.Helper()

	scopes := inventoryScopes(core.ToolGo, core.DefaultConfig())
	hasGoScope := slices.Contains(scopes, core.ToolGo)
	hasGoBinaryScope := slices.Contains(scopes, core.ToolGoBinary)
	hasGoScopes := hasGoScope && hasGoBinaryScope
	if !hasGoScopes {
		t.Fatalf("Go inventory scopes = %#v", scopes)
	}
}

func TestMergeExistingPackageCombinesLegacyAndCurrentGoUsage(t *testing.T) {
	legacyUse := time.Now().Add(-time.Hour)
	currentUse := time.Now()
	inventory := map[string]map[string]*core.PackageInfo{
		core.ToolGo:       {"gopls": {Name: "gopls", UsageCount: 4, LastUsed: legacyUse}},
		core.ToolGoBinary: {"gopls": {Name: "gopls", UsageCount: 3, LastUsed: currentUse}},
	}
	pkg := &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary}

	mergeExistingPackage(inventory, pkg)
	if pkg.UsageCount != 7 {
		t.Fatalf("usage count = %d, want 7", pkg.UsageCount)
	}
	if !pkg.LastUsed.Equal(currentUse) {
		t.Fatalf("last used = %s, want %s", pkg.LastUsed, currentUse)
	}
}

func TestPackageScannerDeduplicatesGoMonitorAndWrapperEntries(t *testing.T) {
	scanner := &packageScanner{
		scan:            newInventoryScan(),
		existing:        legacyAndCurrentGoInventory(),
		scannedPackages: make(map[string]*core.PackageInfo),
	}
	scanner.addPackage(&core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary})
	scanner.addPackage(&core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, Path: "/go/bin/gopls"})

	if len(scanner.packages) != 1 {
		t.Fatalf("packages = %d, want 1", len(scanner.packages))
	}
	if scanner.packages[0].UsageCount != 7 {
		t.Fatalf("usage count = %d, want 7", scanner.packages[0].UsageCount)
	}
}

func TestMergeExistingPackageReusesUnchangedGoFingerprint(t *testing.T) {
	existing := &core.PackageInfo{
		Name: "gopls", Tool: core.ToolGoBinary, Path: "/go/bin/gopls",
		Fingerprint: "sha256", SizeBytes: 42, ModifiedAt: 123,
	}
	inventory := map[string]map[string]*core.PackageInfo{core.ToolGoBinary: {"gopls": existing}}
	pkg := &core.PackageInfo{
		Name: "gopls", Tool: core.ToolGoBinary, Path: existing.Path,
		SizeBytes: existing.SizeBytes, ModifiedAt: existing.ModifiedAt,
	}

	mergeExistingPackage(inventory, pkg)
	if pkg.Fingerprint != existing.Fingerprint {
		t.Fatalf("fingerprint = %q, want %q", pkg.Fingerprint, existing.Fingerprint)
	}
}

func TestPopulateGoBinaryFingerprintCachesFileSignature(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gopls")
	writeExecutableForTest(t, path, "#!/bin/sh\nexit 0\n")
	pkg := &core.PackageInfo{Name: "gopls", Tool: core.ToolGoBinary, Path: path}
	if err := populateGoBinaryFingerprint(pkg); err != nil {
		t.Fatalf("populateGoBinaryFingerprint failed: %v", err)
	}
	assertGoBinaryFingerprintSignature(t, pkg)
	assertGoBinaryFingerprintCached(t, pkg)
}

func assertGoBinaryFingerprintSignature(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	hasFingerprint := pkg.Fingerprint != ""
	hasSize := pkg.SizeBytes != 0
	hasModifiedAt := pkg.ModifiedAt != 0
	hasSignature := hasFingerprint && hasSize && hasModifiedAt
	if !hasSignature {
		t.Fatalf("fingerprinted package = %#v", pkg)
	}
}

func assertGoBinaryFingerprintCached(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	originalFingerprint := pkg.Fingerprint
	if err := populateGoBinaryFingerprint(pkg); err != nil {
		t.Fatalf("cached populateGoBinaryFingerprint failed: %v", err)
	}
	if pkg.Fingerprint != originalFingerprint {
		t.Fatalf("fingerprint changed: %q", pkg.Fingerprint)
	}
}

func TestPopulateGoBinaryFingerprintRejectsMissingBinary(t *testing.T) {
	pkg := &core.PackageInfo{Name: "missing", Tool: core.ToolGoBinary, Path: filepath.Join(t.TempDir(), "missing")}
	if err := populateGoBinaryFingerprint(pkg); err == nil {
		t.Fatal("missing Go binary was fingerprinted")
	}
}

func legacyAndCurrentGoInventory() map[string]map[string]*core.PackageInfo {
	return map[string]map[string]*core.PackageInfo{
		core.ToolGo:       {"gopls": {Name: "gopls", UsageCount: 4}},
		core.ToolGoBinary: {"gopls": {Name: "gopls", UsageCount: 3}},
	}
}

func TestInventoryScopesSkipIncompleteNPMScan(t *testing.T) {
	config := core.DefaultConfig()
	config.Tools.NPM.TrackGlobalOnly = false
	if scopes := inventoryScopes(core.ToolNPM, config); scopes != nil {
		t.Fatalf("npm inventory scopes = %#v", scopes)
	}
}

func TestScanPackagesAdditionalManagers(t *testing.T) {
	config := setupTestHomeConfig(t)
	configureAdditionalManagerScan(t, config)

	output := captureStderr(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})
	if !strings.Contains(output, "packages scanned") {
		t.Fatalf("Unexpected scan output: %q", output)
	}
	assertAdditionalManagerPackages(t, config)
}

func configureAdditionalManagerScan(t *testing.T, config *core.Config) {
	t.Helper()

	t.Setenv("PATH", t.TempDir())
	restoreExecutablePathDeps(t)
	prependAdditionalManagerCommands(t)
	config.Monitoring.EnabledTools = []string{core.ToolPNPM, core.ToolBun, core.ToolPip, core.ToolUV, core.ToolPoetry}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{}
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}
}

func restoreExecutablePathDeps(t *testing.T) {
	t.Helper()

	originalDeps := defaultExecutablePathDeps
	t.Cleanup(func() {
		defaultExecutablePathDeps = originalDeps
	})
	defaultExecutablePathDeps = fakeExecutablePathDeps(nil, nil, nil, "", errors.New("home failed"))
}

func prependAdditionalManagerCommands(t *testing.T) {
	t.Helper()

	prependFakeCommand(t, pnpmCommandName, fakePNPMScanScript)
	prependFakeCommand(t, bunCommandName, fakeBunScanScript)
	prependFakeCommand(t, "pip3", fakePipScanScript)
	prependFakeCommand(t, uvCommandName, fakeUVScanScript)
	prependFakeCommand(t, "poetry", "#!/bin/sh\nexit 0\n")
}

const fakePNPMScanScript = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ] && [ "$4" = "--json" ]; then
  printf '[{"dependencies":{"tsx":{"version":"4.19.0"}}}]\n'
  exit 0
fi
exit 2
`

const fakeBunScanScript = `#!/bin/sh
if [ "$1" = "pm" ] && [ "$2" = "ls" ] && [ "$3" = "-g" ] && [ "$4" = "--json" ]; then
  printf '{"dependencies":{"prettier":{"version":"3.3.0"}}}\n'
  exit 0
fi
exit 2
`

const fakePipScanScript = `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then
  printf '[{"name":"requests","version":"2.32.0"}]\n'
  exit 0
fi
exit 2
`

const fakeUVScanScript = `#!/bin/sh
if [ "$1" = "tool" ] && [ "$2" = "list" ]; then
  printf 'ruff 0.5.0\n'
  exit 0
fi
exit 2
`

func assertAdditionalManagerPackages(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	for _, want := range []struct {
		tool string
		name string
	}{
		{core.ToolPNPM, "tsx"},
		{core.ToolBun, "prettier"},
		{core.ToolPip, "requests"},
		{core.ToolUV, "ruff"},
	} {
		if _, err := store.GetPackage(want.tool, want.name); err != nil {
			t.Fatalf("Expected scanned package %s/%s: %v", want.tool, want.name, err)
		}
	}
}

func TestInstallExecutableWrappersWritesScripts(t *testing.T) {
	config := setupTestHomeConfig(t)
	wrapperDir, originalPath := configureExecutableWrapperInstall(t, config)

	targets := discoverExecutableWrappers(config)
	if len(targets) != 1 {
		t.Fatalf("Expected one wrapper target, got %#v", targets)
	}
	if targets[0].Package != "jq" {
		t.Fatalf("Package = %s, want jq", targets[0].Package)
	}

	if err := installExecutableWrappers(config); err != nil {
		t.Fatalf("installExecutableWrappers failed: %v", err)
	}
	assertInstalledWrapperScript(t, config, wrapperDir, originalPath)
}

func configureExecutableWrapperInstall(t *testing.T, config *core.Config) (string, string) {
	t.Helper()

	t.Setenv("PATH", t.TempDir())

	wrapperDir := filepath.Join(t.TempDir(), "wrappers")
	binDir, originalPath := homebrewWrapperFixture(t)

	config.Monitoring.Process.WrapperDir = wrapperDir
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolHomebrew: {binDir},
	}
	config.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")
	if err := os.MkdirAll(wrapperDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create wrapper dir: %v", err)
	}
	return wrapperDir, originalPath
}

func homebrewWrapperFixture(t *testing.T) (string, string) {
	t.Helper()

	binDir := filepath.Join(t.TempDir(), "Cellar", "jq", "1.8.1", "bin")
	if err := os.MkdirAll(binDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create bin dir: %v", err)
	}
	originalPath := filepath.Join(binDir, "jq")
	writeExecutableForTest(t, originalPath, "#!/bin/bash\nexit 0\n")
	return binDir, originalPath
}

func assertInstalledWrapperScript(t *testing.T, config *core.Config, wrapperDir, originalPath string) {
	t.Helper()

	wrapperPath := filepath.Join(wrapperDir, "jq")
	wrapperContent := readWrapperContent(t, wrapperPath)
	assertWrapperContentReferences(t, wrapperContent, config, originalPath)
	assertWrapperScriptSyntax(t, wrapperPath)
}

func readWrapperContent(t *testing.T, wrapperPath string) string {
	t.Helper()

	content, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("Failed to read wrapper: %v", err)
	}
	return string(content)
}

func assertWrapperContentReferences(t *testing.T, wrapperContent string, config *core.Config, originalPath string) {
	t.Helper()

	hasOriginalPath := strings.Contains(wrapperContent, originalPath)
	hasSocketPath := strings.Contains(wrapperContent, config.Daemon.SocketPath)
	hasWrapperReferences := hasOriginalPath && hasSocketPath
	if !hasWrapperReferences {
		t.Fatalf("Wrapper content missing original path or socket:\n%s", wrapperContent)
	}
	if !strings.Contains(wrapperContent, `DIU_BINARY="diu"`) {
		t.Fatalf("Wrapper content should resolve diu by command name:\n%s", wrapperContent)
	}
	if !strings.Contains(wrapperContent, core.GeneratedWrapperMarker) {
		t.Fatalf("Wrapper content should include the DIU marker:\n%s", wrapperContent)
	}
}

func assertWrapperScriptSyntax(t *testing.T, wrapperPath string) {
	t.Helper()

	if bashPath, err := exec.LookPath("bash"); err == nil {
		if output, err := exec.Command(bashPath, "-n", wrapperPath).CombinedOutput(); err != nil {
			t.Fatalf("Generated wrapper has invalid bash syntax: %v\n%s", err, output)
		}
	}
}

func TestDiscoverExecutableWrappersForAdditionalManagers(t *testing.T) {
	config := setupTestHomeConfig(t)
	configureAdditionalWrapperDiscovery(t, config)

	targets := discoverExecutableWrappers(config)
	assertAdditionalWrapperTargets(t, targets)
}

func configureAdditionalWrapperDiscovery(t *testing.T, config *core.Config) {
	t.Helper()

	t.Setenv("PATH", t.TempDir())

	pnpmDir := wrapperDiscoveryDir(t, "tsx")
	bunDir := wrapperDiscoveryDir(t, "prettier")
	pipDir := wrapperDiscoveryDir(t, "ruff")
	uvDir := wrapperDiscoveryDir(t, "black")

	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolPNPM: {pnpmDir},
		core.ToolBun:  {bunDir},
		core.ToolPip:  {pipDir},
		core.ToolUV:   {uvDir},
	}
	config.Monitoring.EnabledTools = []string{core.ToolPNPM, core.ToolBun, core.ToolPip, core.ToolUV}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")
}

func wrapperDiscoveryDir(t *testing.T, name string) string {
	t.Helper()

	dir := t.TempDir()
	writeExecutableForTest(t, filepath.Join(dir, name), "#!/bin/bash\nexit 0\n")
	return dir
}

func assertAdditionalWrapperTargets(t *testing.T, targets []executableWrapper) {
	t.Helper()

	byName := wrappersByName(targets)
	for name, wantTool := range additionalWrapperTargetTools {
		assertAdditionalWrapperTarget(t, targets, byName, name, wantTool)
	}
}

func wrappersByName(targets []executableWrapper) map[string]executableWrapper {
	byName := make(map[string]executableWrapper)
	for _, target := range targets {
		byName[target.Name] = target
	}
	return byName
}

var additionalWrapperTargetTools = map[string]string{
	"tsx":      core.ToolPNPM,
	"prettier": core.ToolBun,
	"ruff":     core.ToolPip,
	"black":    core.ToolUV,
}

func assertAdditionalWrapperTarget(
	t *testing.T,
	targets []executableWrapper,
	byName map[string]executableWrapper,
	name string,
	wantTool string,
) {
	t.Helper()

	target, ok := byName[name]
	if !ok {
		t.Fatalf("Expected target %s in %#v", name, targets)
	}
	targetMatches := target.Tool == wantTool && target.Package == name
	if !targetMatches {
		t.Fatalf("Target %s = %#v, want tool %s package %s", name, target, wantTool, name)
	}
}

func TestDiscoverExecutableWrappersSkipsDisabledWatchPaths(t *testing.T) {
	config := setupTestHomeConfig(t)

	uvDir := t.TempDir()
	writeExecutableForTest(t, filepath.Join(uvDir, "ruff"), "#!/bin/bash\nexit 0\n")
	config.Monitoring.EnabledTools = []string{core.ToolPip}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolUV: {uvDir},
	}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")

	if targets := discoverExecutableWrappers(config); len(targets) != 0 {
		t.Fatalf("Expected disabled uv watch path to be ignored, got %#v", targets)
	}
}

func TestUninstallGoBinaryRemovesExecutableWrapperAndState(t *testing.T) {
	config := setupTestHomeConfig(t)
	binaryPath, wrapperPath, fingerprint := setupGoBinaryUninstallFixture(t, config)

	output := captureStderr(t, func() {
		pkg := &core.PackageInfo{
			Name:        "mytool",
			Tool:        core.ToolGo,
			Path:        binaryPath,
			Fingerprint: fingerprint,
		}
		assumeYes := true
		if err := uninstallPackage(pkg, assumeYes); err != nil {
			t.Fatalf("uninstallPackage failed: %v", err)
		}
	})
	assertGoBinaryUninstallOutput(t, output)
	assertGoBinaryUninstallRemovedFiles(t, binaryPath, wrapperPath)
	assertGoBinaryUninstallRemovedState(t, config)
}

func setupGoBinaryUninstallFixture(t *testing.T, config *core.Config) (string, string, string) {
	t.Helper()

	ensureGoBinaryWrapperDir(t, config)
	binaryPath, fingerprint := writeFingerprintedGoBinary(t)
	wrapperPath := filepath.Join(config.Monitoring.Process.WrapperDir, "mytool")
	writeExecutableForTest(t, wrapperPath, "#!/bin/bash\nexit 0\n")
	addGoBinaryPackageState(t, config, binaryPath, fingerprint)
	return binaryPath, wrapperPath, fingerprint
}

func ensureGoBinaryWrapperDir(t *testing.T, config *core.Config) {
	t.Helper()

	if err := os.MkdirAll(config.Monitoring.Process.WrapperDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create wrapper dir: %v", err)
	}
}

func writeFingerprintedGoBinary(t *testing.T) (string, string) {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "mytool")
	writeExecutableForTest(t, binaryPath, "#!/bin/bash\nexit 0\n")
	fingerprint, err := safefs.SHA256(binaryPath)
	if err != nil {
		t.Fatalf("Failed to fingerprint binary: %v", err)
	}
	return binaryPath, fingerprint
}

func addGoBinaryPackageState(t *testing.T, config *core.Config, binaryPath, fingerprint string) {
	t.Helper()

	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name:        "mytool",
		Tool:        core.ToolGo,
		Path:        binaryPath,
		Fingerprint: fingerprint,
	})
	closeTestStore(t, store)
}

func assertGoBinaryUninstallOutput(t *testing.T, output string) {
	t.Helper()

	if !strings.Contains(output, "mytool uninstalled") {
		t.Fatalf("Unexpected uninstall output: %q", output)
	}
}

func assertGoBinaryUninstallRemovedFiles(t *testing.T, binaryPath, wrapperPath string) {
	t.Helper()

	if _, err := os.Stat(binaryPath); !os.IsNotExist(err) {
		t.Fatalf("Expected binary removal, stat err=%v", err)
	}
	if _, err := os.Stat(wrapperPath); !os.IsNotExist(err) {
		t.Fatalf("Expected wrapper removal, stat err=%v", err)
	}
}

func assertGoBinaryUninstallRemovedState(t *testing.T, config *core.Config) {
	t.Helper()

	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	if _, err := store.GetPackage(core.ToolGo, "mytool"); err == nil {
		t.Fatal("Expected package state to be removed")
	}
}

func TestInteractiveAndUninstallHelpers(t *testing.T) {
	pkg := &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew}
	assertSupportsPackageUninstall(t, pkg)
	assertPackageWrapperName(t)
	assertConfirmAndUninstallCancellation(t, pkg)
	assertConfirmAndUninstallEOF(t, pkg)
	assertBrowserScreenShowsUninstall(t, pkg)
}

func assertSupportsPackageUninstall(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	if !supportsUninstall(pkg) {
		t.Fatal("Expected homebrew package to support uninstall")
	}
	if supportsUninstall(&core.PackageInfo{Name: "unknown", Tool: "unknown"}) {
		t.Fatal("Expected unknown tool to reject uninstall")
	}
}

func assertPackageWrapperName(t *testing.T) {
	t.Helper()

	if wrapperNameForPackage(&core.PackageInfo{Name: "pkg", Path: "/tmp/tool"}) != "tool" {
		t.Fatal("Expected wrapper name to prefer executable basename")
	}
}

func assertConfirmAndUninstallCancellation(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	cancelReader := bufio.NewReader(strings.NewReader("no\n"))
	var cancelErr error
	captureStderr(t, func() {
		cancelErr = confirmAndUninstall(cancelReader, pkg)
	})
	cancelled := cancelErr != nil && strings.Contains(cancelErr.Error(), "cancelled")
	if !cancelled {
		t.Fatalf("Expected cancellation error, got %v", cancelErr)
	}
}

func assertConfirmAndUninstallEOF(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	var readErr error
	captureStderr(t, func() {
		readErr = confirmAndUninstall(bufio.NewReader(strings.NewReader("")), pkg)
	})
	if !errors.Is(readErr, io.EOF) {
		t.Fatalf("Expected wrapped EOF, got %v", readErr)
	}
}

func assertBrowserScreenShowsUninstall(t *testing.T, pkg *core.PackageInfo) {
	t.Helper()

	browserOutput := captureStderr(t, func() {
		allowUninstall := true
		printBrowserScreen([]*core.PackageInfo{pkg}, 0, "j", allowUninstall)
	})
	hasTitle := strings.Contains(browserOutput, "DIU Packages")
	hasUninstallHelp := strings.Contains(browserOutput, "u uninstall")
	screenOK := hasTitle && hasUninstallHelp
	if !screenOK {
		t.Fatalf("Unexpected browser screen:\n%s", browserOutput)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	capture := newOutputCapture(t)
	oldStdout := os.Stdout
	os.Stdout = capture.writer
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()
	capture.closeWriter(t)
	return capture.output(t, "stdout")
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	capture := newOutputCapture(t)
	oldStderr := os.Stderr
	os.Stderr = capture.writer
	defer func() {
		os.Stderr = oldStderr
	}()

	fn()

	capture.closeWriter(t)
	return capture.output(t, "stderr")
}

type outputCapture struct {
	reader *os.File
	writer *os.File
}

func newOutputCapture(t *testing.T) outputCapture {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	return outputCapture{reader: reader, writer: writer}
}

func (capture outputCapture) closeWriter(t *testing.T) {
	t.Helper()

	if err := capture.writer.Close(); err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}
}

func (capture outputCapture) output(t *testing.T, label string) string {
	t.Helper()

	data, err := io.ReadAll(capture.reader)
	if err != nil {
		t.Fatalf("Failed to read %s: %v", label, err)
	}
	if err := capture.reader.Close(); err != nil {
		t.Fatalf("Failed to close reader: %v", err)
	}
	return string(data)
}

func withReadOnlyStdout(t *testing.T, fn func()) {
	t.Helper()

	stdoutPath := filepath.Join(t.TempDir(), "stdout")
	if err := os.WriteFile(stdoutPath, nil, core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to create read-only stdout target: %v", err)
	}
	file, err := os.Open(stdoutPath)
	if err != nil {
		t.Fatalf("Failed to open read-only stdout target: %v", err)
	}

	oldStdout := os.Stdout
	os.Stdout = file
	defer func() {
		os.Stdout = oldStdout
		if err := file.Close(); err != nil {
			t.Fatalf("Failed to close read-only stdout target: %v", err)
		}
	}()

	fn()
}

func setupTestHomeConfig(t *testing.T) *core.Config {
	t.Helper()

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
	return config
}

func openTestStore(t *testing.T, config *core.Config) storage.Storage {
	t.Helper()
	store, err := storage.NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to open test storage: %v", err)
	}
	return store
}

func closeTestStore(t *testing.T, store storage.Storage) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Failed to close test storage: %v", err)
	}
}

func addTestExecution(t *testing.T, store storage.Storage, record *core.ExecutionRecord) {
	t.Helper()
	if err := store.AddExecution(record); err != nil {
		t.Fatalf("Failed to add test execution: %v", err)
	}
}

func updateTestPackage(t *testing.T, store storage.Storage, pkg *core.PackageInfo) {
	t.Helper()
	if err := store.UpdatePackage(pkg); err != nil {
		t.Fatalf("Failed to update test package: %v", err)
	}
}

func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()

	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create stdin pipe: %v", err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatalf("Failed to write stdin: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Failed to close stdin writer: %v", err)
	}

	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
		if err := reader.Close(); err != nil {
			t.Fatalf("Failed to close stdin reader: %v", err)
		}
	}()

	fn()
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

func packagesCommandForTest(t *testing.T, args ...string) *command {
	t.Helper()
	cmd := &command{}
	var tool, unused string
	cmd.Flags().StringVarP(&tool, "tool", "t", "", "tool")
	cmd.Flags().StringVarP(&unused, "unused", "u", "", "unused")
	parseTestFlags(t, cmd, args...)
	return cmd
}

func checkCommandForTest(t *testing.T, args ...string) *command {
	t.Helper()
	cmd := &command{}
	var tool, search, unused, format string
	var limit int
	cmd.Flags().StringVarP(&tool, "tool", "t", "", "tool")
	cmd.Flags().StringVarP(&search, "search", "s", "", "search")
	cmd.Flags().StringVarP(&unused, "unused", "u", "", "unused")
	cmd.Flags().IntVarP(&limit, "limit", "n", defaultListLimit, "limit")
	cmd.Flags().StringVarP(&format, "format", "f", formatTable, "format")
	parseTestFlags(t, cmd, args...)
	return cmd
}

func manageCommandForTest(t *testing.T, args ...string) *command {
	t.Helper()
	cmd := &command{}
	var tool, search, uninstall string
	var yes, dryRun bool
	cmd.Flags().StringVarP(&tool, "tool", "t", "", "tool")
	cmd.Flags().StringVarP(&search, "search", "s", "", "search")
	cmd.Flags().StringVar(&uninstall, "uninstall", "", "uninstall")
	yesDefault := false
	dryRunDefault := false
	cmd.Flags().BoolVarP(&yes, "yes", "y", yesDefault, "yes")
	cmd.Flags().BoolVar(&dryRun, "dry-run", dryRunDefault, "dry run")
	parseTestFlags(t, cmd, args...)
	return cmd
}

func statsCommandForTest(t *testing.T, args ...string) *command {
	t.Helper()
	cmd := &command{}
	var daily, weekly bool
	var tool string
	var top int
	dailyDefault := false
	weeklyDefault := false
	cmd.Flags().BoolVarP(&daily, "daily", "d", dailyDefault, "daily")
	cmd.Flags().BoolVarP(&weekly, "weekly", "w", weeklyDefault, "weekly")
	cmd.Flags().StringVarP(&tool, "tool", "t", "", "tool")
	cmd.Flags().IntVar(&top, "top", 10, "top")
	parseTestFlags(t, cmd, args...)
	return cmd
}

func writeExecutableForTest(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write executable %s: %v", path, err)
	}
	if err := os.Chmod(path, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to chmod executable %s: %v", path, err)
	}
}

func parseTestFlags(t *testing.T, cmd *command, args ...string) {
	t.Helper()
	remaining, err := cmd.Flags().Parse(args)
	if err != nil {
		t.Fatalf("Failed to parse test flags %v: %v", args, err)
	}
	if len(remaining) != 0 {
		t.Fatalf("Unexpected remaining test args: %v", remaining)
	}
}

func prependFakeCommand(t *testing.T, name, script string) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func fakeExecutablePathDeps(env map[string]string, commands map[string]bool, outputs map[string]string, home string, homeErr error) executablePathDeps {
	return executablePathDeps{
		getenv: func(key string) string {
			return env[key]
		},
		userHomeDir: func() (string, error) {
			if homeErr != nil {
				return "", homeErr
			}
			return home, nil
		},
		lookPath: func(name string) (string, error) {
			if commands[name] {
				return filepath.Join("/usr/bin", name), nil
			}
			return "", errors.New("not found")
		},
		commandOutput: func(name string, args ...string) ([]byte, error) {
			key := strings.Join(append([]string{name}, args...), " ")
			output, ok := outputs[key]
			if !ok {
				return nil, errors.New("command failed")
			}
			return []byte(output), nil
		},
	}
}

func TestExecutableBinDirHelpersWithDeps(t *testing.T) {
	const (
		homeDir = "/Users/test"
		userDir = "/Users/fallback"
	)

	assertPNPMBinDirWithDeps(t, userDir)
	assertBunBinDirWithDeps(t, homeDir, userDir)
	assertUVToolBinDirWithDeps(t, userDir)
	assertPythonUserBaseBinDirWithDeps(t, userDir)
	assertFirstExistingCommandWithDeps(t, userDir)
}

func assertPNPMBinDirWithDeps(t *testing.T, userDir string) {
	t.Helper()

	assertPNPMBinDirFromEnv(t, userDir)
	assertPNPMBinDirFromCommand(t, userDir)
	assertPNPMBinDirMissing(t, userDir)
}

func assertPNPMBinDirFromEnv(t *testing.T, userDir string) {
	t.Helper()

	deps := fakeExecutablePathDeps(
		map[string]string{"PNPM_HOME": "/opt/pnpm"},
		nil,
		nil,
		userDir,
		nil,
	)
	assertBinDir(t, "pnpmGlobalBinDirWithDeps env", pnpmGlobalBinDirWithDeps(deps), "/opt/pnpm")
}

func assertPNPMBinDirFromCommand(t *testing.T, userDir string) {
	t.Helper()

	deps := fakeExecutablePathDeps(
		nil,
		map[string]bool{pnpmCommandName: true},
		map[string]string{"pnpm bin -g": "/opt/pnpm/bin\n"},
		userDir,
		nil,
	)
	assertBinDir(t, "pnpmGlobalBinDirWithDeps command", pnpmGlobalBinDirWithDeps(deps), "/opt/pnpm/bin")
}

func assertPNPMBinDirMissing(t *testing.T, userDir string) {
	t.Helper()

	deps := fakeExecutablePathDeps(nil, nil, nil, userDir, nil)
	assertBinDir(t, "pnpmGlobalBinDirWithDeps missing", pnpmGlobalBinDirWithDeps(deps), "")
}

func assertBunBinDirWithDeps(t *testing.T, homeDir, userDir string) {
	t.Helper()

	deps := fakeExecutablePathDeps(map[string]string{"BUN_INSTALL": "/opt/bun"}, nil, nil, userDir, nil)
	assertBinDir(t, "bunGlobalBinDirWithDeps env", bunGlobalBinDirWithDeps(deps), "/opt/bun/bin")

	deps = fakeExecutablePathDeps(map[string]string{"HOME": homeDir}, nil, nil, userDir, nil)
	wantHomeBin := filepath.Join(homeDir, ".bun", "bin")
	assertBinDir(t, "bunGlobalBinDirWithDeps HOME", bunGlobalBinDirWithDeps(deps), wantHomeBin)

	deps = fakeExecutablePathDeps(nil, nil, nil, "", errors.New("home failed"))
	assertBinDir(t, "bunGlobalBinDirWithDeps home error", bunGlobalBinDirWithDeps(deps), "")
}

func assertUVToolBinDirWithDeps(t *testing.T, userDir string) {
	t.Helper()

	deps := fakeExecutablePathDeps(nil, nil, nil, userDir, nil)
	wantFallback := filepath.Join(userDir, ".local", "bin")
	assertBinDir(t, "uvToolBinDirWithDeps fallback", uvToolBinDirWithDeps(deps), wantFallback)

	deps = fakeExecutablePathDeps(map[string]string{"UV_TOOL_BIN_DIR": "/opt/uv/bin"}, nil, nil, userDir, nil)
	assertBinDir(t, "uvToolBinDirWithDeps env", uvToolBinDirWithDeps(deps), "/opt/uv/bin")
}

func assertPythonUserBaseBinDirWithDeps(t *testing.T, userDir string) {
	t.Helper()

	deps := fakeExecutablePathDeps(
		nil,
		map[string]bool{"python": true},
		map[string]string{"python -m site --user-base": "/Users/test/Library/Python/3.12\n"},
		userDir,
		nil,
	)
	want := "/Users/test/Library/Python/3.12/bin"
	assertBinDir(t, "pythonUserBaseBinDirWithDeps", pythonUserBaseBinDirWithDeps(deps), want)
}

func assertFirstExistingCommandWithDeps(t *testing.T, userDir string) {
	t.Helper()

	deps := fakeExecutablePathDeps(nil, map[string]bool{"python": true}, nil, userDir, nil)
	gotCommand, err := firstExistingCommandWithDeps(deps, "python3", "python")
	foundPython := err == nil && gotCommand == "python"
	if !foundPython {
		t.Fatalf("firstExistingCommandWithDeps = %s, %v; want python, nil", gotCommand, err)
	}
}

func assertBinDir(t *testing.T, label, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %s, want %s", label, got, want)
	}
}

func TestExecutableBinDirPublicHelpersUseDefaultDeps(t *testing.T) {
	useFakeDefaultExecutablePathDeps(t)

	assertBinDir(t, "pnpmGlobalBinDir", pnpmGlobalBinDir(), "/opt/pnpm/bin")
	assertBinDir(t, "bunGlobalBinDir", bunGlobalBinDir(), "/Users/test/.bun/bin")
	assertBinDir(t, "pythonUserBaseBinDir", pythonUserBaseBinDir(), "/Users/test/Library/Python/3.12/bin")
	assertBinDir(t, "uvToolBinDir", uvToolBinDir(), "/opt/uv/bin")
	assertFirstExistingDefaultCommand(t)
}

var defaultExecutablePathEnv = map[string]string{
	"HOME":            "/Users/test",
	"UV_TOOL_BIN_DIR": "/opt/uv/bin",
}

var defaultExecutablePathCommands = map[string]bool{
	pnpmCommandName: true,
	"python3":       true,
}

var defaultExecutablePathOutputs = map[string]string{
	"pnpm bin -g":                    "/opt/pnpm/bin\n",
	"python3 -m site --user-base":    "/Users/test/Library/Python/3.12\n",
	"npm config get prefix":          "/unused\n",
	"unexpected command placeholder": "",
}

func useFakeDefaultExecutablePathDeps(t *testing.T) {
	t.Helper()

	originalDeps := defaultExecutablePathDeps
	t.Cleanup(func() {
		defaultExecutablePathDeps = originalDeps
	})
	defaultExecutablePathDeps = fakeExecutablePathDeps(
		defaultExecutablePathEnv,
		defaultExecutablePathCommands,
		defaultExecutablePathOutputs,
		"/Users/fallback",
		nil,
	)
}

func assertFirstExistingDefaultCommand(t *testing.T) {
	t.Helper()

	got, err := firstExistingCommand("python", "python3")
	foundPython3 := err == nil && got == "python3"
	if !foundPython3 {
		t.Fatalf("firstExistingCommand = %s, %v; want python3, nil", got, err)
	}
}

func TestGoBinaryDirWithDeps(t *testing.T) {
	assertGoBinaryDirUsesConfigGoBin(t)
	assertGoBinaryDirUsesEnvGoBin(t)
	assertGoBinaryDirUsesConfigGoPath(t)
	assertGoBinaryDirUsesEnvGoPath(t)
	assertGoBinaryDirUsesHomeFallback(t)
	assertGoBinaryDirHandlesHomeError(t)
}

func assertGoBinaryDirUsesConfigGoBin(t *testing.T) {
	t.Helper()

	config := core.DefaultConfig()
	config.Tools.Go.GoBin = "/explicit/go/bin"
	deps := fakeExecutablePathDeps(map[string]string{"GOBIN": "/env/go/bin"}, nil, nil, "/Users/test", nil)
	assertGoBinaryDir(t, config, deps, "GoBin", "/explicit/go/bin")
}

func assertGoBinaryDirUsesEnvGoBin(t *testing.T) {
	t.Helper()

	config := goConfigWithoutExplicitDirs()
	deps := fakeExecutablePathDeps(map[string]string{"GOBIN": "/env/go/bin"}, nil, nil, "/Users/test", nil)
	assertGoBinaryDir(t, config, deps, "GOBIN", "/env/go/bin")
}

func assertGoBinaryDirUsesConfigGoPath(t *testing.T) {
	t.Helper()

	config := goConfigWithoutExplicitDirs()
	config.Tools.Go.GoPath = "/config/gopath"
	deps := fakeExecutablePathDeps(nil, nil, nil, "/Users/test", nil)
	assertGoBinaryDir(t, config, deps, "GoPath", "/config/gopath/bin")
}

func assertGoBinaryDirUsesEnvGoPath(t *testing.T) {
	t.Helper()

	config := goConfigWithoutExplicitDirs()
	deps := fakeExecutablePathDeps(map[string]string{"GOPATH": "/env/gopath"}, nil, nil, "/Users/test", nil)
	assertGoBinaryDir(t, config, deps, "GOPATH", "/env/gopath/bin")
}

func assertGoBinaryDirUsesHomeFallback(t *testing.T) {
	t.Helper()

	config := goConfigWithoutExplicitDirs()
	deps := fakeExecutablePathDeps(nil, nil, nil, "/Users/test", nil)
	assertGoBinaryDir(t, config, deps, "user home", "/Users/test/go/bin")
}

func assertGoBinaryDirHandlesHomeError(t *testing.T) {
	t.Helper()

	config := goConfigWithoutExplicitDirs()
	deps := fakeExecutablePathDeps(nil, nil, nil, "", errors.New("home failed"))
	assertGoBinaryDir(t, config, deps, "home error", "")
}

func goConfigWithoutExplicitDirs() *core.Config {
	config := core.DefaultConfig()
	config.Tools.Go.GoBin = ""
	config.Tools.Go.GoPath = ""
	return config
}

func assertGoBinaryDir(t *testing.T, config *core.Config, deps executablePathDeps, label, want string) {
	t.Helper()

	if got := goBinaryDirWithDeps(config, deps); got != want {
		t.Fatalf("goBinaryDirWithDeps %s = %s, want %s", label, got, want)
	}
}

func TestNewMonitorSupportsConfiguredTools(t *testing.T) {
	assertNewMonitorSupportsTools(t, configuredMonitorTools)
	assertNewMonitorRejectsBogusTool(t)
}

var configuredMonitorTools = []string{
	core.ToolHomebrew,
	core.ToolNPM,
	core.ToolPNPM,
	core.ToolBun,
	core.ToolGo,
	core.ToolPip,
	core.ToolUV,
	core.ToolPoetry,
}

func assertNewMonitorSupportsTools(t *testing.T, tools []string) {
	t.Helper()

	for _, tool := range tools {
		monitor, err := newMonitor(tool)
		if err != nil {
			t.Fatalf("newMonitor(%s) failed: %v", tool, err)
		}
		if monitor == nil {
			t.Fatalf("newMonitor(%s) returned nil", tool)
		}
	}
}

func assertNewMonitorRejectsBogusTool(t *testing.T) {
	t.Helper()

	if _, err := newMonitor("bogus"); err == nil {
		t.Fatal("newMonitor bogus expected error")
	}
}

func TestRunHomebrewUninstallInvalidName(t *testing.T) {
	isCask := false
	err := runHomebrewUninstall("../evil", isCask)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestCommandOutputErrorOmitsEmptyPrefix(t *testing.T) {
	err := commandOutputError("", errors.New("failed"))
	if err.Error() != "failed" {
		t.Fatalf("commandOutputError = %q, want failed", err)
	}
}

func TestRunCommandKeepsActionOutputOffStdout(t *testing.T) {
	var runErr error
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			runErr = runCommand("printf", "%s", "manager output")
		})
	})
	if runErr != nil {
		t.Fatalf("runCommand failed: %v", runErr)
	}
	cleanStdout := stdout == ""
	hasStderr := stderr == "manager output"
	streamsOK := cleanStdout && hasStderr
	if !streamsOK {
		t.Fatalf("streams = stdout %q, stderr %q", stdout, stderr)
	}
}

func TestRunHomebrewUninstallSuccess(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\nexit 0\n")
	isCask := false
	if err := runHomebrewUninstall("ripgrep", isCask); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunHomebrewUninstallCask(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\necho \"$@\" >&2\nexit 0\n")
	isCask := true
	if err := runHomebrewUninstall("vlc", isCask); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunHomebrewUninstallCommandFails(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\nexit 7\n")
	isCask := false
	if err := runHomebrewUninstall("ripgrep", isCask); err == nil {
		t.Fatal("expected non-zero exit error")
	}
}

func TestRunNPMUninstallInvalidName(t *testing.T) {
	if err := runNPMUninstall("foo;rm"); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRunNPMUninstallSuccess(t *testing.T) {
	prependFakeCommand(t, "npm", "#!/bin/sh\nexit 0\n")
	if err := runNPMUninstall("typescript"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunNPMUninstallCommandFails(t *testing.T) {
	prependFakeCommand(t, "npm", "#!/bin/sh\nexit 1\n")
	if err := runNPMUninstall("typescript"); err == nil {
		t.Fatal("expected non-zero exit error")
	}
}

func TestRunPipUninstallFallsBackToPip3(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	prependFakeCommand(t, pip3CommandName, "#!/bin/sh\nexit 0\n")
	if err := runPipUninstall("ruff"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunPipUninstallPrefersPip3(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	writeExecutableForTest(t, filepath.Join(binDir, pipCommandName), "#!/bin/sh\nexit 12\n")
	writeExecutableForTest(t, filepath.Join(binDir, pip3CommandName), "#!/bin/sh\nexit 0\n")

	if err := runPipUninstall("ruff"); err != nil {
		t.Fatalf("expected pip3 to be selected, got %v", err)
	}
}

func TestRunUninstallGoBinary(t *testing.T) {
	binPath, pkg := goBinaryPackageForUninstall(t)

	if err := runUninstall(pkg); err != nil {
		t.Fatalf("runUninstall failed: %v", err)
	}
	assertPathRemoved(t, binPath)
}

func goBinaryPackageForUninstall(t *testing.T) (string, *core.PackageInfo) {
	t.Helper()

	dir := t.TempDir()
	binPath := filepath.Join(dir, "mytool")
	writeExecutableForTest(t, binPath, "#!/bin/sh\nexit 0\n")
	fingerprint, err := safefs.SHA256(binPath)
	if err != nil {
		t.Fatalf("Failed to fingerprint binary: %v", err)
	}
	return binPath, &core.PackageInfo{
		Name:        "mytool",
		Tool:        core.ToolGoBinary,
		Path:        binPath,
		Fingerprint: fingerprint,
	}
}

func assertPathRemoved(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected file removed, stat err: %v", err)
	}
}

func TestRunUninstallUnsupportedTool(t *testing.T) {
	pkg := &core.PackageInfo{Name: "foo", Tool: "bogus"}
	if err := runUninstall(pkg); err == nil {
		t.Fatal("expected unsupported error")
	}
}

func TestRunUninstallHomebrewDispatch(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\nexit 0\n")
	pkg := &core.PackageInfo{Name: "ripgrep", Tool: core.ToolHomebrew}
	if err := runUninstall(pkg); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunUninstallHomebrewCaskDispatch(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\nexit 0\n")
	pkg := &core.PackageInfo{Name: "vlc", Tool: homebrewCaskTool}
	if err := runUninstall(pkg); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunUninstallNPMDispatch(t *testing.T) {
	prependFakeCommand(t, "npm", "#!/bin/sh\nexit 0\n")
	pkg := &core.PackageInfo{Name: "typescript", Tool: core.ToolNPM}
	if err := runUninstall(pkg); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunUninstallAdditionalPackageManagerDispatch(t *testing.T) {
	for _, tt := range additionalPackageManagerUninstallCases {
		t.Run(tt.name, func(t *testing.T) {
			assertRunUninstallDispatch(t, tt)
		})
	}
}

type packageManagerUninstallCase struct {
	name        string
	commandName string
	pkg         core.PackageInfo
}

var additionalPackageManagerUninstallCases = []packageManagerUninstallCase{
	{name: "pnpm", commandName: pnpmCommandName, pkg: core.PackageInfo{Name: "tsx", Tool: core.ToolPNPM}},
	{name: "bun", commandName: bunCommandName, pkg: core.PackageInfo{Name: "prettier", Tool: core.ToolBun}},
	{name: "pip", commandName: pip3CommandName, pkg: core.PackageInfo{Name: "ruff", Tool: core.ToolPip}},
	{name: "uv", commandName: uvCommandName, pkg: core.PackageInfo{Name: "black", Tool: core.ToolUV}},
}

func assertRunUninstallDispatch(t *testing.T, tt packageManagerUninstallCase) {
	t.Helper()

	prependFakeCommand(t, tt.commandName, "#!/bin/sh\nexit 0\n")
	if err := runUninstall(&tt.pkg); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunUninstallGoMissingPathReturnsError(t *testing.T) {
	pkg := &core.PackageInfo{Name: "ghost", Tool: core.ToolGo, Path: ""}
	if err := runUninstall(pkg); err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestRemoveGoBinaryFailsForMissingPath(t *testing.T) {
	pkg := &core.PackageInfo{Name: "ghost", Tool: core.ToolGo, Path: ""}
	if err := removeGoBinary(pkg); err == nil {
		t.Fatal("expected validation error for empty path")
	}
}

func TestRemoveGoBinaryRejectsChangedExecutable(t *testing.T) {
	binaryPath, fingerprint := changedGoBinaryForRemoval(t)
	pkg := &core.PackageInfo{
		Name:        "tool",
		Tool:        core.ToolGoBinary,
		Path:        binaryPath,
		Fingerprint: fingerprint,
	}

	err := removeGoBinary(pkg)
	assertRemoveGoBinaryRejected(t, err, "changed since the last scan")
	assertPathStillExists(t, binaryPath, "changed binary")
}

func changedGoBinaryForRemoval(t *testing.T) (string, string) {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "tool")
	writeExecutableForTest(t, binaryPath, "#!/bin/sh\nexit 0\n")
	fingerprint, err := safefs.SHA256(binaryPath)
	if err != nil {
		t.Fatalf("Failed to fingerprint binary: %v", err)
	}
	writeExecutableForTest(t, binaryPath, "#!/bin/sh\nexit 1\n")
	return binaryPath, fingerprint
}

func assertRemoveGoBinaryRejected(t *testing.T, err error, message string) {
	t.Helper()

	rejected := err != nil && strings.Contains(err.Error(), message)
	if !rejected {
		t.Fatalf("removeGoBinary error = %v", err)
	}
}

func assertPathStillExists(t *testing.T, path, label string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s should remain: %v", label, err)
	}
}

func TestRemoveGoBinaryRequiresScanFingerprint(t *testing.T) {
	binaryPath := filepath.Join(t.TempDir(), "tool")
	writeExecutableForTest(t, binaryPath, "#!/bin/sh\nexit 0\n")
	pkg := &core.PackageInfo{Name: "tool", Tool: core.ToolGoBinary, Path: binaryPath}

	err := removeGoBinary(pkg)
	missingFingerprintRejected := err != nil && strings.Contains(err.Error(), "run diu scan")
	if !missingFingerprintRejected {
		t.Fatalf("removeGoBinary error = %v", err)
	}
	if _, err := os.Stat(binaryPath); err != nil {
		t.Fatalf("unfingerprinted binary should remain: %v", err)
	}
}

func TestRemoveUninstalledPackageState(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{Name: "ripgrep", Tool: core.ToolHomebrew})
	closeTestStore(t, store)

	pkg := &core.PackageInfo{Name: "ripgrep", Tool: core.ToolHomebrew}
	if err := removeUninstalledPackageState(pkg); err != nil {
		t.Fatalf("removeUninstalledPackageState failed: %v", err)
	}
}
