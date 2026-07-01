package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

func TestPackageNameForExecutable(t *testing.T) {
	const (
		homebrewExecutable = "/opt/homebrew/Cellar/jq/1.8.1/bin/jq"
		npmExecutable      = "/opt/homebrew/lib/node_modules/@scope/tool/bin/tool"
		goExecutable       = "/Users/test/go/bin/golangci-lint"
		homebrewCommand    = "jq"
		npmCommand         = "tool"
		goCommand          = "golangci-lint"
		homebrewPackage    = "jq"
		npmPackage         = "@scope/tool"
	)

	tests := []struct {
		name string
		tool string
		path string
		cmd  string
		want string
	}{
		{
			name: "homebrew cellar path",
			tool: core.ToolHomebrew,
			path: filepath.Clean(homebrewExecutable),
			cmd:  homebrewCommand,
			want: homebrewPackage,
		},
		{
			name: "npm scoped package path",
			tool: core.ToolNPM,
			path: filepath.Clean(npmExecutable),
			cmd:  npmCommand,
			want: npmPackage,
		},
		{
			name: "go binary fallback",
			tool: core.ToolGo,
			path: filepath.Clean(goExecutable),
			cmd:  goCommand,
			want: goCommand,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := packageNameForExecutable(tt.tool, tt.path, tt.cmd); got != tt.want {
				t.Errorf("packageNameForExecutable(%q, %q, %q) = %q, want %q", tt.tool, tt.path, tt.cmd, got, tt.want)
			}
		})
	}
}

func TestShouldSkipExecutableWrapper(t *testing.T) {
	const (
		hiddenCommand = ".hidden"
		diuCommand    = "diu"
		brewCommand   = "brew"
		normalCommand = "jq"
		emptyCommand  = ""
		skipExpected  = true
		trackExpected = false
	)

	tests := map[string]bool{
		emptyCommand:  skipExpected,
		hiddenCommand: skipExpected,
		diuCommand:    skipExpected,
		brewCommand:   skipExpected,
		normalCommand: trackExpected,
	}

	for command, expected := range tests {
		if got := shouldSkipExecutableWrapper(command); got != expected {
			t.Errorf("shouldSkipExecutableWrapper(%q) = %v, want %v", command, got, expected)
		}
	}
}

func TestFilterPackagesSearchAndUnused(t *testing.T) {
	const (
		searchQuery          = "jq"
		unusedDuration       = "24h"
		usedPackageName      = "jq"
		otherPackageName     = "ripgrep"
		usedPackageCount     = 3
		unusedPackageCount   = 0
		expectedPackageCount = 1
	)

	packages := []*core.PackageInfo{
		{
			Name:       usedPackageName,
			Tool:       core.ToolHomebrew,
			UsageCount: usedPackageCount,
			LastUsed:   time.Now(),
		},
		{
			Name:       otherPackageName,
			Tool:       core.ToolHomebrew,
			UsageCount: unusedPackageCount,
		},
	}

	filtered, err := filterPackages(packages, packageListOptions{Search: searchQuery})
	if err != nil {
		t.Fatalf("filterPackages failed: %v", err)
	}
	if len(filtered) != expectedPackageCount || filtered[0].Name != usedPackageName {
		t.Fatalf("Expected only %s, got %v", usedPackageName, filtered)
	}

	filtered, err = filterPackages(packages, packageListOptions{Unused: unusedDuration})
	if err != nil {
		t.Fatalf("filterPackages unused failed: %v", err)
	}
	if len(filtered) != expectedPackageCount || filtered[0].Name != otherPackageName {
		t.Fatalf("Expected only %s, got %v", otherPackageName, filtered)
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
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "  1  ") {
		t.Fatalf("first package row = %q, want numbering from 1", output)
	}
}

func TestUninstallPlan(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	prependFakeCommand(t, pip3CommandName, "#!/bin/sh\nexit 0\n")

	const (
		homebrewPackage = "jq"
		npmPackage      = "eslint"
		pnpmPackage     = "tsx"
		bunPackage      = "prettier"
		pipPackage      = "ruff"
		uvPackage       = "black"
		goPackage       = "golangci-lint"
		goPath          = "/Users/test/go/bin/golangci-lint"
	)

	tests := []struct {
		name string
		pkg  *core.PackageInfo
		want []string
	}{
		{
			name: "homebrew",
			pkg:  &core.PackageInfo{Name: homebrewPackage, Tool: core.ToolHomebrew},
			want: []string{homebrewCommandName, uninstallSubcommand, homebrewPackage},
		},
		{
			name: "npm",
			pkg:  &core.PackageInfo{Name: npmPackage, Tool: core.ToolNPM},
			want: []string{npmCommandName, uninstallSubcommand, npmGlobalFlag, npmPackage},
		},
		{
			name: "pnpm",
			pkg:  &core.PackageInfo{Name: pnpmPackage, Tool: core.ToolPNPM},
			want: []string{pnpmCommandName, removeSubcommand, npmGlobalFlag, pnpmPackage},
		},
		{
			name: "bun",
			pkg:  &core.PackageInfo{Name: bunPackage, Tool: core.ToolBun},
			want: []string{bunCommandName, removeSubcommand, npmGlobalFlag, bunPackage},
		},
		{
			name: "pip",
			pkg:  &core.PackageInfo{Name: pipPackage, Tool: core.ToolPip},
			want: []string{pip3CommandName, uninstallSubcommand, pipYesFlag, pipPackage},
		},
		{
			name: "uv",
			pkg:  &core.PackageInfo{Name: uvPackage, Tool: core.ToolUV},
			want: []string{uvCommandName, "tool", uninstallSubcommand, uvPackage},
		},
		{
			name: "go executable",
			pkg:  &core.PackageInfo{Name: goPackage, Tool: core.ToolGo, Path: goPath},
			want: []string{removeFilePlan},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uninstallPlan(tt.pkg)
			if err != nil {
				t.Fatalf("uninstallPlan failed: %v", err)
			}
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("uninstallPlan() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidatePackageManagerName(t *testing.T) {
	const (
		homebrewPackage       = "ripgrep"
		scopedNPMPackage      = "@scope/tool"
		tappedHomebrewPackage = "owner/tap/tool"
		flagLikePackage       = "--help"
		traversalPackage      = "../tool"
		shellPackage          = "tool;rm"
	)

	validPackages := []string{
		homebrewPackage,
		scopedNPMPackage,
		tappedHomebrewPackage,
	}
	for _, name := range validPackages {
		t.Run(name, func(t *testing.T) {
			if err := validatePackageManagerName(name); err != nil {
				t.Fatalf("validatePackageManagerName(%q) failed: %v", name, err)
			}
		})
	}

	invalidPackages := []string{
		flagLikePackage,
		traversalPackage,
		shellPackage,
	}
	for _, name := range invalidPackages {
		t.Run(name, func(t *testing.T) {
			if err := validatePackageManagerName(name); err == nil {
				t.Fatalf("validatePackageManagerName(%q) should fail", name)
			}
		})
	}
}

func TestValidateRemovableExecutablePath(t *testing.T) {
	const (
		executableName    = "tool"
		nonExecutableName = "notes.txt"
		executableScript  = "#!/bin/bash\nexit 0\n"
		plainTextContent  = "not executable\n"
	)

	tempDir := t.TempDir()
	executablePath := filepath.Join(tempDir, executableName)
	if err := os.WriteFile(executablePath, []byte(executableScript), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write executable: %v", err)
	}
	if err := os.Chmod(executablePath, core.OwnerExecutableMode); err != nil {
		t.Fatalf("Failed to mark executable: %v", err)
	}

	validated, err := validateRemovableExecutablePath(executablePath)
	if err != nil {
		t.Fatalf("validateRemovableExecutablePath failed: %v", err)
	}
	if validated != executablePath {
		t.Errorf("validateRemovableExecutablePath() = %s, want %s", validated, executablePath)
	}

	nonExecutablePath := filepath.Join(tempDir, nonExecutableName)
	if err := os.WriteFile(nonExecutablePath, []byte(plainTextContent), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write non-executable: %v", err)
	}
	if _, err := validateRemovableExecutablePath(nonExecutablePath); err == nil {
		t.Fatal("Expected non-executable path validation to fail")
	}
}

func TestRecordExecutionWritesToConfiguredStorage(t *testing.T) {
	config := setupTestHomeConfig(t)

	payload := `{
		"tool":"brew",
		"command":"brew install jq",
		"args":["install","jq"],
		"exit_code":0,
		"duration_ms":1200,
		"packages_affected":["jq"]
	}`

	var runErr error
	withStdin(t, payload, func() {
		runErr = recordExecution(&command{}, nil)
	})
	if runErr != nil {
		t.Fatalf("recordExecution failed: %v", runErr)
	}

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

func TestQueryExecutionsFormats(t *testing.T) {
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
	closeTestStore(t, store)

	jsonOutput := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t, "--tool", "npm", "--format", "json", "--limit", "1"), nil); err != nil {
			t.Fatalf("queryExecutions JSON failed: %v", err)
		}
	})
	var records []core.ExecutionRecord
	if err := json.Unmarshal([]byte(jsonOutput), &records); err != nil {
		t.Fatalf("Failed to decode JSON output %q: %v", jsonOutput, err)
	}
	if len(records) != 1 || records[0].Command != "npm install eslint" {
		t.Fatalf("Unexpected JSON records: %#v", records)
	}

	csvOutput := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t, "--format", "csv"), nil); err != nil {
			t.Fatalf("queryExecutions CSV failed: %v", err)
		}
	})
	if !strings.Contains(csvOutput, "tool,command,timestamp,duration_ms,exit_code") || !strings.Contains(csvOutput, "npm install eslint") {
		t.Fatalf("Unexpected CSV output:\n%s", csvOutput)
	}

	tableOutput := captureStdout(t, func() {
		if err := queryExecutions(queryCommandForTest(t), nil); err != nil {
			t.Fatalf("queryExecutions table failed: %v", err)
		}
	})
	if !strings.Contains(tableOutput, "Execution History") || !strings.Contains(tableOutput, "npm install eslint") {
		t.Fatalf("Unexpected table output:\n%s", tableOutput)
	}
}

func TestPackageCommandsUseStorage(t *testing.T) {
	config := setupTestHomeConfig(t)
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

	listOutput := captureStdout(t, func() {
		if err := listPackages(packagesCommandForTest(t, "--tool", "homebrew"), nil); err != nil {
			t.Fatalf("listPackages failed: %v", err)
		}
	})
	if !strings.Contains(listOutput, "Tracked Packages") || !strings.Contains(listOutput, "jq (1.7)") {
		t.Fatalf("Unexpected list output:\n%s", listOutput)
	}

	checkOutput := captureStdout(t, func() {
		if err := checkPackages(checkCommandForTest(t, "--search", "eslint", "--format", "json"), nil); err != nil {
			t.Fatalf("checkPackages failed: %v", err)
		}
	})
	var packages []core.PackageInfo
	if err := json.Unmarshal([]byte(checkOutput), &packages); err != nil {
		t.Fatalf("Failed to decode package JSON %q: %v", checkOutput, err)
	}
	if len(packages) != 1 || packages[0].Name != "eslint" {
		t.Fatalf("Unexpected check packages: %#v", packages)
	}

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

	setOutput := captureStdout(t, func() {
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

	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install old",
		Timestamp: time.Now().Add(-60 * 24 * time.Hour),
	})
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:      core.ToolNPM,
		Command:   "npm install current",
		Timestamp: time.Now(),
	})
	closeTestStore(t, store)

	backupOutput := captureStdout(t, func() {
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

	cleanupOutput := captureStdout(t, func() {
		if err := cleanup(&command{}, nil); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}
	})
	if !strings.Contains(cleanupOutput, "Cleanup completed") {
		t.Fatalf("Unexpected cleanup output: %q", cleanupOutput)
	}

	store = openTestStore(t, config)
	defer closeTestStore(t, store)
	executions, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatalf("GetExecutions failed: %v", err)
	}
	if len(executions) != 1 || executions[0].Command != "npm install current" {
		t.Fatalf("Unexpected executions after cleanup: %#v", executions)
	}
}

func TestPackageAndFormattingHelpers(t *testing.T) {
	packages := []*core.PackageInfo{
		{Name: "low", Tool: core.ToolNPM, UsageCount: 1, LastUsed: time.Now()},
		{Name: "high", Tool: core.ToolHomebrew, UsageCount: 5, LastUsed: time.Now().Add(-24 * time.Hour)},
	}
	sortPackages(packages)
	if packages[0].Name != "high" {
		t.Fatalf("sortPackages placed %q first, want high", packages[0].Name)
	}

	if pkg, err := packageBySelection(packages, 0, "2"); err != nil || pkg.Name != "low" {
		t.Fatalf("packageBySelection = %#v, %v; want low", pkg, err)
	}
	if _, err := packageBySelection(packages, 0, "abc"); err == nil {
		t.Fatal("Expected invalid selection to fail")
	}

	if got := truncate("abcdef", 4); got != "abc." {
		t.Fatalf("truncate = %q, want abc.", got)
	}
	if got := formatLastUsed(time.Time{}); got != "never" {
		t.Fatalf("formatLastUsed zero = %q, want never", got)
	}
	if getToolColor("brew") == "" {
		t.Fatal("Expected brew tool color")
	}

	detailOutput := captureStdout(t, func() {
		printPackageDetail(&core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew, Version: "1.7", Path: "/tmp/jq"})
	})
	if !strings.Contains(detailOutput, "jq") || !strings.Contains(detailOutput, "Version:") {
		t.Fatalf("Unexpected package detail output:\n%s", detailOutput)
	}
}

func TestDurationAndWrapperHelpers(t *testing.T) {
	days, err := parseDuration("2d")
	if err != nil || days != 48*time.Hour {
		t.Fatalf("parseDuration 2d = %s, %v", days, err)
	}
	weeks, err := parseDuration("1w")
	if err != nil || weeks != 7*24*time.Hour {
		t.Fatalf("parseDuration 1w = %s, %v", weeks, err)
	}
	months, err := parseDuration("1mo")
	if err != nil || months != 30*24*time.Hour {
		t.Fatalf("parseDuration 1mo = %s, %v", months, err)
	}
	minutes, err := parseDuration("30m")
	if err != nil || minutes != 30*time.Minute {
		t.Fatalf("parseDuration 30m = %s, %v", minutes, err)
	}
	hours, err := parseDuration("3h")
	if err != nil || hours != 3*time.Hour {
		t.Fatalf("parseDuration 3h = %s, %v", hours, err)
	}

	tempDir := t.TempDir()
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

func TestShowStatsUsesStorage(t *testing.T) {
	config := setupTestHomeConfig(t)
	store := openTestStore(t, config)
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:             core.ToolNPM,
		Command:          "npm install eslint",
		Args:             []string{"install", "eslint"},
		Timestamp:        time.Now(),
		PackagesAffected: []string{"eslint"},
	})
	addTestExecution(t, store, &core.ExecutionRecord{
		Tool:             core.ToolHomebrew,
		Command:          "brew install jq",
		Args:             []string{"install", "jq"},
		Timestamp:        time.Now().Add(-48 * time.Hour),
		PackagesAffected: []string{"jq"},
	})
	updateTestPackage(t, store, &core.PackageInfo{
		Name:       "eslint",
		Tool:       core.ToolNPM,
		UsageCount: 3,
		LastUsed:   time.Now(),
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := showStats(statsCommandForTest(t, "--daily", "--tool", "npm", "--top", "1"), nil); err != nil {
			t.Fatalf("showStats failed: %v", err)
		}
	})
	if !strings.Contains(output, "DIU Statistics (Last 24 Hours)") ||
		!strings.Contains(output, "Total executions:") ||
		!strings.Contains(output, "eslint (npm)") {
		t.Fatalf("Unexpected stats output:\n%s", output)
	}
}

func TestSetupProjectInitializesStorageWithoutWrappers(t *testing.T) {
	config := setupTestHomeConfig(t)

	output := captureStdout(t, func() {
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

func TestScanPackagesDiscoversExecutableWrappers(t *testing.T) {
	config := setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())

	binDir := t.TempDir()
	writeExecutableForTest(t, filepath.Join(binDir, "jq"), "#!/bin/bash\nexit 0\n")
	if err := os.WriteFile(filepath.Join(binDir, "notes"), []byte("not executable"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write non-executable: %v", err)
	}
	writeExecutableForTest(t, filepath.Join(binDir, "brew"), "#!/bin/bash\nexit 0\n")

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
	if !strings.Contains(output, "1 packages scanned") {
		t.Fatalf("Unexpected scan output: %q", output)
	}

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

func TestScanPackagesAdditionalManagers(t *testing.T) {
	config := setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())

	originalDeps := defaultExecutablePathDeps
	t.Cleanup(func() {
		defaultExecutablePathDeps = originalDeps
	})
	defaultExecutablePathDeps = fakeExecutablePathDeps(nil, nil, nil, "", errors.New("home failed"))

	prependFakeCommand(t, pnpmCommandName, `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "-g" ] && [ "$3" = "--depth=0" ] && [ "$4" = "--json" ]; then
  printf '[{"dependencies":{"tsx":{"version":"4.19.0"}}}]\n'
  exit 0
fi
exit 2
`)
	prependFakeCommand(t, bunCommandName, `#!/bin/sh
if [ "$1" = "pm" ] && [ "$2" = "ls" ] && [ "$3" = "-g" ] && [ "$4" = "--json" ]; then
  printf '{"dependencies":{"prettier":{"version":"3.3.0"}}}\n'
  exit 0
fi
exit 2
`)
	prependFakeCommand(t, "pip3", `#!/bin/sh
if [ "$1" = "list" ] && [ "$2" = "--format=json" ]; then
  printf '[{"name":"requests","version":"2.32.0"}]\n'
  exit 0
fi
exit 2
`)
	prependFakeCommand(t, uvCommandName, `#!/bin/sh
if [ "$1" = "tool" ] && [ "$2" = "list" ]; then
  printf 'ruff 0.5.0\n'
  exit 0
fi
exit 2
`)
	prependFakeCommand(t, "poetry", "#!/bin/sh\nexit 0\n")

	config.Monitoring.EnabledTools = []string{core.ToolPNPM, core.ToolBun, core.ToolPip, core.ToolUV, core.ToolPoetry}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{}
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	output := captureStdout(t, func() {
		if err := scanPackages(&command{}, nil); err != nil {
			t.Fatalf("scanPackages failed: %v", err)
		}
	})
	if !strings.Contains(output, "packages scanned") {
		t.Fatalf("Unexpected scan output: %q", output)
	}

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
	t.Setenv("PATH", t.TempDir())

	wrapperDir := filepath.Join(t.TempDir(), "wrappers")
	binDir := filepath.Join(t.TempDir(), "Cellar", "jq", "1.8.1", "bin")
	if err := os.MkdirAll(binDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create bin dir: %v", err)
	}
	originalPath := filepath.Join(binDir, "jq")
	writeExecutableForTest(t, originalPath, "#!/bin/bash\nexit 0\n")

	config.Monitoring.Process.WrapperDir = wrapperDir
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolHomebrew: {binDir},
	}
	config.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")
	if err := os.MkdirAll(wrapperDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create wrapper dir: %v", err)
	}

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
	wrapperPath := filepath.Join(wrapperDir, "jq")
	content, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("Failed to read wrapper: %v", err)
	}
	if !strings.Contains(string(content), originalPath) || !strings.Contains(string(content), config.Daemon.SocketPath) {
		t.Fatalf("Wrapper content missing original path or socket:\n%s", content)
	}
	if !strings.Contains(string(content), `DIU_BINARY="diu"`) {
		t.Fatalf("Wrapper content should resolve diu by command name:\n%s", content)
	}
	if bashPath, err := exec.LookPath("bash"); err == nil {
		if output, err := exec.Command(bashPath, "-n", wrapperPath).CombinedOutput(); err != nil {
			t.Fatalf("Generated wrapper has invalid bash syntax: %v\n%s", err, output)
		}
	}
}

func TestDiscoverExecutableWrappersForAdditionalManagers(t *testing.T) {
	config := setupTestHomeConfig(t)
	t.Setenv("PATH", t.TempDir())

	pnpmDir := t.TempDir()
	bunDir := t.TempDir()
	pipDir := t.TempDir()
	uvDir := t.TempDir()
	writeExecutableForTest(t, filepath.Join(pnpmDir, "tsx"), "#!/bin/bash\nexit 0\n")
	writeExecutableForTest(t, filepath.Join(bunDir, "prettier"), "#!/bin/bash\nexit 0\n")
	writeExecutableForTest(t, filepath.Join(pipDir, "ruff"), "#!/bin/bash\nexit 0\n")
	writeExecutableForTest(t, filepath.Join(uvDir, "black"), "#!/bin/bash\nexit 0\n")

	config.Monitoring.Filesystem.WatchPaths = map[string][]string{
		core.ToolPNPM: {pnpmDir},
		core.ToolBun:  {bunDir},
		core.ToolPip:  {pipDir},
		core.ToolUV:   {uvDir},
	}
	config.Monitoring.EnabledTools = []string{core.ToolPNPM, core.ToolBun, core.ToolPip, core.ToolUV}
	config.Tools.Go.GoBin = filepath.Join(t.TempDir(), "missing")

	targets := discoverExecutableWrappers(config)
	byName := make(map[string]executableWrapper)
	for _, target := range targets {
		byName[target.Name] = target
	}

	for name, wantTool := range map[string]string{
		"tsx":      core.ToolPNPM,
		"prettier": core.ToolBun,
		"ruff":     core.ToolPip,
		"black":    core.ToolUV,
	} {
		target, ok := byName[name]
		if !ok {
			t.Fatalf("Expected target %s in %#v", name, targets)
		}
		if target.Tool != wantTool || target.Package != name {
			t.Fatalf("Target %s = %#v, want tool %s package %s", name, target, wantTool, name)
		}
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
	if err := os.MkdirAll(config.Monitoring.Process.WrapperDir, core.OwnerDirectoryMode); err != nil {
		t.Fatalf("Failed to create wrapper dir: %v", err)
	}

	binaryPath := filepath.Join(t.TempDir(), "mytool")
	writeExecutableForTest(t, binaryPath, "#!/bin/bash\nexit 0\n")
	wrapperPath := filepath.Join(config.Monitoring.Process.WrapperDir, "mytool")
	writeExecutableForTest(t, wrapperPath, "#!/bin/bash\nexit 0\n")

	store := openTestStore(t, config)
	updateTestPackage(t, store, &core.PackageInfo{
		Name: "mytool",
		Tool: core.ToolGo,
		Path: binaryPath,
	})
	closeTestStore(t, store)

	output := captureStdout(t, func() {
		if err := uninstallPackage(&core.PackageInfo{Name: "mytool", Tool: core.ToolGo, Path: binaryPath}, true); err != nil {
			t.Fatalf("uninstallPackage failed: %v", err)
		}
	})
	if !strings.Contains(output, "mytool uninstalled") {
		t.Fatalf("Unexpected uninstall output: %q", output)
	}
	if _, err := os.Stat(binaryPath); !os.IsNotExist(err) {
		t.Fatalf("Expected binary removal, stat err=%v", err)
	}
	if _, err := os.Stat(wrapperPath); !os.IsNotExist(err) {
		t.Fatalf("Expected wrapper removal, stat err=%v", err)
	}

	store = openTestStore(t, config)
	defer closeTestStore(t, store)
	if _, err := store.GetPackage(core.ToolGo, "mytool"); err == nil {
		t.Fatal("Expected package state to be removed")
	}
}

func TestInteractiveAndUninstallHelpers(t *testing.T) {
	pkg := &core.PackageInfo{Name: "jq", Tool: core.ToolHomebrew}
	if !supportsUninstall(pkg) {
		t.Fatal("Expected homebrew package to support uninstall")
	}
	if supportsUninstall(&core.PackageInfo{Name: "unknown", Tool: "unknown"}) {
		t.Fatal("Expected unknown tool to reject uninstall")
	}
	if wrapperNameForPackage(&core.PackageInfo{Name: "pkg", Path: "/tmp/tool"}) != "tool" {
		t.Fatal("Expected wrapper name to prefer executable basename")
	}

	reader := bufio.NewReader(strings.NewReader("value\n"))
	value, err := readPrompt(reader, "prompt> ")
	if err != nil {
		t.Fatalf("readPrompt failed: %v", err)
	}
	if value != "value" {
		t.Fatalf("readPrompt = %q, want value", value)
	}

	cancelReader := bufio.NewReader(strings.NewReader("no\n"))
	if err := confirmAndUninstall(cancelReader, pkg); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("Expected cancellation error, got %v", err)
	}

	browserOutput := captureStdout(t, func() {
		printBrowserScreen([]*core.PackageInfo{pkg}, 0, "j", true)
	})
	if !strings.Contains(browserOutput, "DIU Packages") || !strings.Contains(browserOutput, "u uninstall") {
		t.Fatalf("Unexpected browser screen:\n%s", browserOutput)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}

	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to read stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
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
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "yes")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "dry run")
	parseTestFlags(t, cmd, args...)
	return cmd
}

func statsCommandForTest(t *testing.T, args ...string) *command {
	t.Helper()
	cmd := &command{}
	var daily, weekly bool
	var tool string
	var top int
	cmd.Flags().BoolVarP(&daily, "daily", "d", false, "daily")
	cmd.Flags().BoolVarP(&weekly, "weekly", "w", false, "weekly")
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
	remaining, err := cmd.Flags().parse(args)
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

	deps := fakeExecutablePathDeps(
		map[string]string{"PNPM_HOME": "/opt/pnpm"},
		nil,
		nil,
		userDir,
		nil,
	)
	if got := pnpmGlobalBinDirWithDeps(deps); got != "/opt/pnpm" {
		t.Fatalf("pnpmGlobalBinDirWithDeps env = %s, want /opt/pnpm", got)
	}

	deps = fakeExecutablePathDeps(
		nil,
		map[string]bool{pnpmCommandName: true},
		map[string]string{"pnpm bin -g": "/opt/pnpm/bin\n"},
		userDir,
		nil,
	)
	if got := pnpmGlobalBinDirWithDeps(deps); got != "/opt/pnpm/bin" {
		t.Fatalf("pnpmGlobalBinDirWithDeps command = %s, want /opt/pnpm/bin", got)
	}

	deps = fakeExecutablePathDeps(nil, nil, nil, userDir, nil)
	if got := pnpmGlobalBinDirWithDeps(deps); got != "" {
		t.Fatalf("pnpmGlobalBinDirWithDeps missing = %s, want empty", got)
	}

	deps = fakeExecutablePathDeps(map[string]string{"BUN_INSTALL": "/opt/bun"}, nil, nil, userDir, nil)
	if got := bunGlobalBinDirWithDeps(deps); got != "/opt/bun/bin" {
		t.Fatalf("bunGlobalBinDirWithDeps env = %s, want /opt/bun/bin", got)
	}

	deps = fakeExecutablePathDeps(map[string]string{"HOME": homeDir}, nil, nil, userDir, nil)
	if got := bunGlobalBinDirWithDeps(deps); got != filepath.Join(homeDir, ".bun", "bin") {
		t.Fatalf("bunGlobalBinDirWithDeps HOME = %s", got)
	}

	deps = fakeExecutablePathDeps(nil, nil, nil, userDir, nil)
	if got := uvToolBinDirWithDeps(deps); got != filepath.Join(userDir, ".local", "bin") {
		t.Fatalf("uvToolBinDirWithDeps fallback = %s", got)
	}

	deps = fakeExecutablePathDeps(map[string]string{"UV_TOOL_BIN_DIR": "/opt/uv/bin"}, nil, nil, userDir, nil)
	if got := uvToolBinDirWithDeps(deps); got != "/opt/uv/bin" {
		t.Fatalf("uvToolBinDirWithDeps env = %s, want /opt/uv/bin", got)
	}

	deps = fakeExecutablePathDeps(
		nil,
		map[string]bool{"python": true},
		map[string]string{"python -m site --user-base": "/Users/test/Library/Python/3.12\n"},
		userDir,
		nil,
	)
	if got := pythonUserBaseBinDirWithDeps(deps); got != "/Users/test/Library/Python/3.12/bin" {
		t.Fatalf("pythonUserBaseBinDirWithDeps = %s", got)
	}

	gotCommand, err := firstExistingCommandWithDeps(deps, "python3", "python")
	if err != nil || gotCommand != "python" {
		t.Fatalf("firstExistingCommandWithDeps = %s, %v; want python, nil", gotCommand, err)
	}

	deps = fakeExecutablePathDeps(nil, nil, nil, "", errors.New("home failed"))
	if got := bunGlobalBinDirWithDeps(deps); got != "" {
		t.Fatalf("bunGlobalBinDirWithDeps home error = %s, want empty", got)
	}
}

func TestExecutableBinDirPublicHelpersUseDefaultDeps(t *testing.T) {
	originalDeps := defaultExecutablePathDeps
	t.Cleanup(func() {
		defaultExecutablePathDeps = originalDeps
	})

	defaultExecutablePathDeps = fakeExecutablePathDeps(
		map[string]string{
			"HOME":            "/Users/test",
			"UV_TOOL_BIN_DIR": "/opt/uv/bin",
		},
		map[string]bool{
			pnpmCommandName: true,
			"python3":       true,
		},
		map[string]string{
			"pnpm bin -g":                    "/opt/pnpm/bin\n",
			"python3 -m site --user-base":    "/Users/test/Library/Python/3.12\n",
			"npm config get prefix":          "/unused\n",
			"unexpected command placeholder": "",
		},
		"/Users/fallback",
		nil,
	)

	if got := pnpmGlobalBinDir(); got != "/opt/pnpm/bin" {
		t.Fatalf("pnpmGlobalBinDir = %s", got)
	}
	if got := bunGlobalBinDir(); got != "/Users/test/.bun/bin" {
		t.Fatalf("bunGlobalBinDir = %s", got)
	}
	if got := pythonUserBaseBinDir(); got != "/Users/test/Library/Python/3.12/bin" {
		t.Fatalf("pythonUserBaseBinDir = %s", got)
	}
	if got := uvToolBinDir(); got != "/opt/uv/bin" {
		t.Fatalf("uvToolBinDir = %s", got)
	}
	if got, err := firstExistingCommand("python", "python3"); err != nil || got != "python3" {
		t.Fatalf("firstExistingCommand = %s, %v; want python3, nil", got, err)
	}
}

func TestGoBinaryDirWithDeps(t *testing.T) {
	config := core.DefaultConfig()
	config.Tools.Go.GoBin = "/explicit/go/bin"
	deps := fakeExecutablePathDeps(map[string]string{"GOBIN": "/env/go/bin"}, nil, nil, "/Users/test", nil)
	if got := goBinaryDirWithDeps(config, deps); got != "/explicit/go/bin" {
		t.Fatalf("goBinaryDirWithDeps GoBin = %s", got)
	}

	config.Tools.Go.GoBin = ""
	if got := goBinaryDirWithDeps(config, deps); got != "/env/go/bin" {
		t.Fatalf("goBinaryDirWithDeps GOBIN = %s", got)
	}

	config.Tools.Go.GoPath = "/config/gopath"
	deps = fakeExecutablePathDeps(nil, nil, nil, "/Users/test", nil)
	if got := goBinaryDirWithDeps(config, deps); got != "/config/gopath/bin" {
		t.Fatalf("goBinaryDirWithDeps GoPath = %s", got)
	}

	config.Tools.Go.GoPath = ""
	deps = fakeExecutablePathDeps(map[string]string{"GOPATH": "/env/gopath"}, nil, nil, "/Users/test", nil)
	if got := goBinaryDirWithDeps(config, deps); got != "/env/gopath/bin" {
		t.Fatalf("goBinaryDirWithDeps GOPATH = %s", got)
	}

	deps = fakeExecutablePathDeps(nil, nil, nil, "/Users/test", nil)
	if got := goBinaryDirWithDeps(config, deps); got != "/Users/test/go/bin" {
		t.Fatalf("goBinaryDirWithDeps user home = %s", got)
	}

	deps = fakeExecutablePathDeps(nil, nil, nil, "", errors.New("home failed"))
	if got := goBinaryDirWithDeps(config, deps); got != "" {
		t.Fatalf("goBinaryDirWithDeps home error = %s, want empty", got)
	}
}

func TestNewMonitorSupportsConfiguredTools(t *testing.T) {
	for _, tool := range []string{
		core.ToolHomebrew,
		core.ToolNPM,
		core.ToolPNPM,
		core.ToolBun,
		core.ToolGo,
		core.ToolPip,
		core.ToolUV,
		core.ToolPoetry,
	} {
		monitor, err := newMonitor(tool)
		if err != nil {
			t.Fatalf("newMonitor(%s) failed: %v", tool, err)
		}
		if monitor == nil {
			t.Fatalf("newMonitor(%s) returned nil", tool)
		}
	}

	if _, err := newMonitor("bogus"); err == nil {
		t.Fatal("newMonitor bogus expected error")
	}
}

func TestRunPreparedCommandSuccess(t *testing.T) {
	if err := runPreparedCommand(exec.Command("true")); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunPreparedCommandFailure(t *testing.T) {
	err := runPreparedCommand(exec.Command("false"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "uninstall failed") {
		t.Fatalf("expected wrapped error, got %q", err.Error())
	}
}

func TestRunHomebrewUninstallInvalidName(t *testing.T) {
	err := runHomebrewUninstall("../evil", false)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestRunHomebrewUninstallSuccess(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\nexit 0\n")
	if err := runHomebrewUninstall("ripgrep", false); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunHomebrewUninstallCask(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\necho \"$@\" >&2\nexit 0\n")
	if err := runHomebrewUninstall("vlc", true); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunHomebrewUninstallCommandFails(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\nexit 7\n")
	if err := runHomebrewUninstall("ripgrep", false); err == nil {
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
	dir := t.TempDir()
	binPath := filepath.Join(dir, "mytool")
	writeExecutableForTest(t, binPath, "#!/bin/sh\nexit 0\n")

	pkg := &core.PackageInfo{
		Name: "mytool",
		Tool: core.ToolGoBinary,
		Path: binPath,
	}

	if err := runUninstall(pkg); err != nil {
		t.Fatalf("runUninstall failed: %v", err)
	}
	if _, err := os.Stat(binPath); !errors.Is(err, os.ErrNotExist) {
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
	tests := []struct {
		name        string
		commandName string
		pkg         *core.PackageInfo
	}{
		{
			name:        "pnpm",
			commandName: pnpmCommandName,
			pkg:         &core.PackageInfo{Name: "tsx", Tool: core.ToolPNPM},
		},
		{
			name:        "bun",
			commandName: bunCommandName,
			pkg:         &core.PackageInfo{Name: "prettier", Tool: core.ToolBun},
		},
		{
			name:        "pip",
			commandName: pip3CommandName,
			pkg:         &core.PackageInfo{Name: "ruff", Tool: core.ToolPip},
		},
		{
			name:        "uv",
			commandName: uvCommandName,
			pkg:         &core.PackageInfo{Name: "black", Tool: core.ToolUV},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prependFakeCommand(t, tt.commandName, "#!/bin/sh\nexit 0\n")
			if err := runUninstall(tt.pkg); err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
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
