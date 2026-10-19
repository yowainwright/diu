package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/safefs"
)

func TestRunUninstallUsesPlannedArguments(t *testing.T) {
	cases := append([]uninstallPlanCase{}, uninstallPlanCases...)
	cask := uninstallPlanCase{
		name: "homebrew cask", pkg: &core.PackageInfo{Name: "vlc", Tool: homebrewCaskTool},
		want: []string{homebrewCommandName, uninstallSubcommand, homebrewCaskFlag, "vlc"},
	}
	cases = append(cases, cask)
	for _, test := range cases {
		if test.pkg.Tool == core.ToolGo {
			continue
		}
		t.Run(test.name, func(t *testing.T) { assertUninstallArguments(t, test) })
	}
}

func assertUninstallArguments(t *testing.T, test uninstallPlanCase) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("DIU_TEST_UNINSTALL_ARGUMENTS", path)
	t.Setenv("PATH", t.TempDir())
	prependFakeCommand(t, test.want[0], "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DIU_TEST_UNINSTALL_ARGUMENTS\"\n")
	if err := runUninstall(test.pkg); err != nil {
		t.Fatal(err)
	}
	want := strings.Join(test.want[1:], "\n") + "\n"
	assertFileContents(t, path, want)
}

func TestRunUninstallRejectsInvalidNamesBeforeStartingManagers(t *testing.T) {
	tools := []string{core.ToolHomebrew, homebrewCaskTool, core.ToolNPM, core.ToolPNPM, core.ToolBun, core.ToolPip, core.ToolUV, core.ToolCargo, core.ToolPipx}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "started")
			t.Setenv("DIU_TEST_UNINSTALL_STARTED", path)
			t.Setenv("PATH", t.TempDir())
			writeUninstallManagerFixtures(t)
			if err := runUninstall(&core.PackageInfo{Name: "--help", Tool: tool}); err == nil {
				t.Fatal("invalid package name was accepted")
			}
			assertFileMissing(t, path)
		})
	}
}

func writeUninstallManagerFixtures(t *testing.T) {
	t.Helper()
	for _, name := range []string{homebrewCommandName, npmCommandName, pnpmCommandName, bunCommandName, pip3CommandName, uvCommandName, core.ToolCargo, core.ToolPipx} {
		prependFakeCommand(t, name, "#!/bin/sh\nprintf started > \"$DIU_TEST_UNINSTALL_STARTED\"\n")
	}
}

func TestRunUninstallPreservesManagerExitCode(t *testing.T) {
	prependFakeCommand(t, npmCommandName, "#!/bin/sh\nexit 7\n")
	err := runUninstall(&core.PackageInfo{Name: "typescript", Tool: core.ToolNPM})
	var commandErr *dx.CommandError
	hasManagerError := errors.As(err, &commandErr) && commandErr.Code == 7
	if !hasManagerError {
		t.Fatalf("uninstall error = %v, want manager exit code 7", err)
	}
}

func TestDeletePackageStateErrorPreservesDeleteFailure(t *testing.T) {
	deleteErr := errors.New("delete failed")
	for _, closeErr := range []error{nil, os.ErrPermission} {
		err := deletePackageStateError(deleteErr, closeErr)
		if !errors.Is(err, deleteErr) {
			t.Fatalf("delete error was lost: %v", err)
		}
		hasMissingCloseError := closeErr != nil && !strings.Contains(err.Error(), closeErr.Error())
		if hasMissingCloseError {
			t.Fatalf("close failure was lost: %v", err)
		}
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

func TestUninstallPlanRejectsUnownedExecutable(t *testing.T) {
	pkg := &core.PackageInfo{Name: "pnpm", Tool: core.ToolExecutable}
	if supportsUninstall(pkg) {
		t.Fatal("unmapped executable permits automatic uninstall")
	}
	if command, err := uninstallPlan(pkg); err == nil {
		t.Fatalf("unmapped executable produced an uninstall command: %v", command)
	}
}

func TestUninstallPlanPipxUsesScannedIdentity(t *testing.T) {
	_, root := pipxExecutableFixture(t)
	pkg := pipxPackageFixture(root)
	plan, err := uninstallPlan(pkg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pipx", "uninstall", "px-tools@dev"}
	isExpected := slices.Equal(plan, want) && supportsUninstall(pkg)
	if !isExpected {
		t.Fatalf("plan = %v, want %v", plan, want)
	}
}

func pipxPackageFixture(root string) *core.PackageInfo {
	return &core.PackageInfo{
		Name: "px-tools@dev", Tool: core.ToolPipx, Version: "1.0.0",
		Path: filepath.Join(root, "venvs", "px-tools@dev", "bin", "px-one"),
	}
}

func TestUninstallPlanPipxRejectsChangedInstall(t *testing.T) {
	_, root := pipxExecutableFixture(t)
	for _, changed := range []string{"version", "location", "unknown"} {
		pkg := pipxPackageFixture(root)
		switch changed {
		case "version":
			pkg.Version = "2.0.0"
		case "location":
			pkg.Path = filepath.Join(t.TempDir(), "venvs", pkg.Name, "bin", "px-one")
		case "unknown":
			pkg.Path = ""
		}
		if plan, err := uninstallPlan(pkg); err == nil {
			t.Fatalf("changed %s produced uninstall plan: %v", changed, plan)
		}
	}
}

func TestRunUninstallPipxPreservesFailureCode(t *testing.T) {
	_, root := pipxExecutableFixture(t)
	prependFakeCommand(t, "pipx", "#!/bin/sh\nif [ \"$1\" = list ]; then printf '%s' \"$DIU_PIPX_JSON\"; else exit 7; fi\n")
	err := runUninstall(pipxPackageFixture(root))
	var commandErr *dx.CommandError
	hasFailureCode := errors.As(err, &commandErr) && commandErr.Code == 7
	if !hasFailureCode {
		t.Fatalf("pipx uninstall error = %v, want exit code 7", err)
	}
}

func TestUninstallPlanCargoUsesVerifiedRoot(t *testing.T) {
	_, root := cargoExecutableFixture(t)
	pkg := &core.PackageInfo{Name: "ripgrep", Version: "14.1.1", Tool: core.ToolCargo, Path: filepath.Join(root, "bin", "rg")}
	plan, err := uninstallPlan(pkg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cargo", "uninstall", "--root", root, "ripgrep@14.1.1"}
	validPlan := slices.Equal(plan, want) && supportsUninstall(pkg)
	if !validPlan {
		t.Fatalf("plan = %v, want %v", plan, want)
	}
}

func TestUninstallPlanCargoRejectsChangedInventory(t *testing.T) {
	_, root := cargoExecutableFixture(t)
	for _, name := range []string{"ripgrep", "unlisted"} {
		pkg := &core.PackageInfo{Name: name, Version: "99.0.0", Tool: core.ToolCargo, Path: filepath.Join(root, "bin", "rg")}
		if plan, err := uninstallPlan(pkg); err == nil {
			t.Fatalf("changed inventory produced uninstall plan: %v", plan)
		}
	}
}

func TestUninstallPlanCargoRejectsUnknownRoot(t *testing.T) {
	cargoExecutableFixture(t)
	pkg := &core.PackageInfo{Name: "ripgrep", Version: "14.1.1", Tool: core.ToolCargo}
	if plan, err := uninstallPlan(pkg); err == nil {
		t.Fatalf("unverified root produced uninstall plan: %v", plan)
	}
}

func TestPrintDryRunUninstallQuotesCargoRoot(t *testing.T) {
	_, root := cargoExecutableFixture(t)
	root = filepath.Join(root, "custom root")
	pkg := &core.PackageInfo{Name: "ripgrep", Version: "14.1.1", Tool: core.ToolCargo, Path: filepath.Join(root, "bin", "rg")}
	output := captureStdout(t, func() {
		if err := printDryRunUninstall(pkg); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "--root '"+root+"'") {
		t.Fatalf("unquoted root in dry-run: %q", output)
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

func TestUninstallGoBinaryRemovesBinaryAndState(t *testing.T) {
	config := setupTestHomeConfig(t)
	binaryPath, fingerprint := setupGoBinaryUninstallFixture(t, config)

	output := captureStderr(t, func() {
		pkg := &core.PackageInfo{
			Name:        "mytool",
			Tool:        core.ToolGo,
			Path:        binaryPath,
			Fingerprint: fingerprint,
		}
		shouldAssumeYes := true
		if err := uninstallPackage(pkg, shouldAssumeYes); err != nil {
			t.Fatalf("uninstallPackage failed: %v", err)
		}
	})
	assertGoBinaryUninstallOutput(t, output)
	assertGoBinaryUninstallRemovedFile(t, binaryPath)
	assertGoBinaryUninstallRemovedState(t, config)
}

func setupGoBinaryUninstallFixture(t *testing.T, config *core.Config) (string, string) {
	t.Helper()

	binaryPath, fingerprint := writeFingerprintedGoBinary(t)
	addGoBinaryPackageState(t, config, binaryPath, fingerprint)
	return binaryPath, fingerprint
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

func assertGoBinaryUninstallRemovedFile(t *testing.T, binaryPath string) {
	t.Helper()

	if _, err := os.Stat(binaryPath); !os.IsNotExist(err) {
		t.Fatalf("Expected binary removal, stat err=%v", err)
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

func packagesCommandForTest(t *testing.T, args ...string) *command {
	t.Helper()
	cmd := newPackagesCommand()
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
	var shouldAssumeYes, shouldDryRun bool
	cmd.Flags().StringVarP(&tool, "tool", "t", "", "tool")
	cmd.Flags().StringVarP(&search, "search", "s", "", "search")
	cmd.Flags().StringVar(&uninstall, "uninstall", "", "uninstall")
	yesDefault := false
	shouldDryRunDefault := false
	cmd.Flags().BoolVarP(&shouldAssumeYes, "yes", "y", yesDefault, "yes")
	cmd.Flags().BoolVar(&shouldDryRun, "dry-run", shouldDryRunDefault, "dry run")
	parseTestFlags(t, cmd, args...)
	return cmd
}

func TestRunHomebrewUninstallInvalidName(t *testing.T) {
	err := runUninstall(&core.PackageInfo{Name: "../evil", Tool: core.ToolHomebrew})
	if err == nil {
		t.Fatal("expected validation error, got nil")
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
	if err := runUninstall(&core.PackageInfo{Name: "ripgrep", Tool: core.ToolHomebrew}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunHomebrewUninstallCask(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\necho \"$@\" >&2\nexit 0\n")
	if err := runUninstall(&core.PackageInfo{Name: "vlc", Tool: homebrewCaskTool}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunHomebrewUninstallCommandFails(t *testing.T) {
	prependFakeCommand(t, "brew", "#!/bin/sh\nexit 7\n")
	if err := runUninstall(&core.PackageInfo{Name: "ripgrep", Tool: core.ToolHomebrew}); err == nil {
		t.Fatal("expected non-zero exit error")
	}
}

func TestRunNPMUninstallInvalidName(t *testing.T) {
	if err := runUninstall(&core.PackageInfo{Name: "foo;rm", Tool: core.ToolNPM}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRunNPMUninstallSuccess(t *testing.T) {
	prependFakeCommand(t, "npm", "#!/bin/sh\nexit 0\n")
	if err := runUninstall(&core.PackageInfo{Name: "typescript", Tool: core.ToolNPM}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunNPMUninstallCommandFails(t *testing.T) {
	prependFakeCommand(t, "npm", "#!/bin/sh\nexit 1\n")
	if err := runUninstall(&core.PackageInfo{Name: "typescript", Tool: core.ToolNPM}); err == nil {
		t.Fatal("expected non-zero exit error")
	}
}

func TestRunPipUninstallFallsBackToPip3(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	prependFakeCommand(t, pip3CommandName, "#!/bin/sh\nexit 0\n")
	if err := runUninstall(&core.PackageInfo{Name: "ruff", Tool: core.ToolPip}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunPipUninstallPrefersPip3(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	writeExecutableForTest(t, filepath.Join(binDir, pipCommandName), "#!/bin/sh\nexit 12\n")
	writeExecutableForTest(t, filepath.Join(binDir, pip3CommandName), "#!/bin/sh\nexit 0\n")

	if err := runUninstall(&core.PackageInfo{Name: "ruff", Tool: core.ToolPip}); err != nil {
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
