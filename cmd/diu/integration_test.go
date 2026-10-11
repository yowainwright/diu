package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/cmd/diu/record"
	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/storage"
)

func TestCLIRejectsInvalidReportingFlags(t *testing.T) {
	setupTestHomeConfig(t)
	cases := [][]string{
		{"query", "--format", "yaml"},
		{"check", "--format", "yaml"},
		{"query", "--format="},
		{"check", "--format="},
		{"query", "--limit", "-1"},
		{"check", "--limit", "-1"},
		{"stats", "--top", "-1"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			assertInvalidReportingFlags(t, args)
		})
	}
}

func TestCLIAllowsZeroResultCounts(t *testing.T) {
	setupTestHomeConfig(t)
	for _, args := range [][]string{{"query", "--limit", "0"}, {"check", "--limit", "0"}, {"stats", "--top", "0"}} {
		captureStdout(t, func() {
			if err := newRootCommand().Execute(args); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
		})
	}
}

func TestRecordExecutionPreservesSlowNPMEnrichment(t *testing.T) {
	config := setupTestHomeConfig(t)
	installSlowNPMProbe(t)
	payload := `{"tool":"npm","command":"npm install -g typescript","args":["install","-g","typescript"]}`
	runRecordExecution(t, payload)
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{Tool: core.ToolNPM})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("slow enrichment recorded %d executions, want 1", len(records))
	}
	assertNoFallbackContention(t, config)
}

func assertNoFallbackContention(t *testing.T, config *core.Config) {
	t.Helper()
	_, contended, err := observability.ReadFallbackContention(config.Daemon.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if contended {
		t.Fatal("uncontended recording incorrectly reported lock contention")
	}
}

func installSlowNPMProbe(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "npm")
	script := "#!/bin/sh\nsleep 0.12\nprintf '/synthetic/npm\\n'\n"
	if err := os.WriteFile(path, []byte(script), core.OwnerExecutableMode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/usr/bin:/bin")
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

func TestNewReportingFormatsRejectInvalidValues(t *testing.T) {
	setupTestHomeConfig(t)
	for _, name := range []string{"stats", "status", "packages"} {
		assertInvalidReportFormats(t, name)
	}
}

func runRecordExecution(t *testing.T, payload string) {
	t.Helper()

	var runErr error
	withStdin(t, payload, func() {
		runErr = record.RunExecution(record.NewCommand(), nil)
	})
	if runErr != nil {
		t.Fatalf("recordExecution failed: %v", runErr)
	}
}

func TestConfigCommandsAndMaintenance(t *testing.T) {
	config := setupTestHomeConfig(t)
	assertConfigSetGetAndList(t)
	addMaintenanceExecutionFixtures(t, config)
	assertBackupCommandCreatesFile(t, config)
	assertCleanupKeepsCurrentExecution(t, config)
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
