//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

func TestCLIRecordsBackgroundLaunchAttempt(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	result := submitCLIBackgroundLaunch(t, f)
	assertCLIExit(t, result, 0)
	records := waitCLIRecords(t, f, 1)
	assertCLIRecordedLaunchAttempt(t, records[0])
	assertCLIRecordedPackageUse(t, f)
}

func TestCLIZshListenerRecordsSubmittedCommand(t *testing.T) {
	f := newCLIFixture(t)
	writeCLIFile(t, filepath.Join(f.managed, "listener-tool"), "#!/bin/sh\nexit 17\n", 0o700)
	f.setup(t)
	integration := readCLIFile(t, filepath.Join(f.config.Daemon.DataDir, "shell-integration.zsh"))
	if !strings.Contains(integration, "_DIU_TOOL_MAP+=('listener-tool'") {
		t.Fatal("setup did not include listener-tool in the executable map")
	}
	command, input, output, stderr := startCLIZsh(t, f)
	writeZshInput(t, input, "listener-tool || true\nprint -r -- DIU_RECORDED\n")
	waitCLIZshOutput(t, output, "DIU_RECORDED")
	records := waitCLIRecords(t, f, 1)
	assertCLIZshRecordedLaunchAttempt(t, records[0])
	assertCLIZshRecordedPackageUse(t, f)
	writeZshInput(t, input, "exit\n")
	_ = input.Close()
	if err := command.Wait(); err != nil {
		t.Fatalf("interactive zsh failed: %v; stderr=%s", err, stderr.String())
	}
}

func submitCLIBackgroundLaunch(t *testing.T, f *cliFixture) cliResult {
	t.Helper()
	payload := `{"kind":"launch_attempt","tool":"npm","command":"prettier","packages_affected":["prettier"],"metadata":{"executable":"prettier"}}`
	return runCLICommand(t, f.command(t, cliBinary, "record", "--background"), payload)
}

func waitCLIRecords(t *testing.T, f *cliFixture, count int) []core.ExecutionRecord {
	t.Helper()
	var records []core.ExecutionRecord
	awaitCLI(t, func() bool {
		result := f.cli(t, "query", "--format", "json", "--limit", "100")
		assertCLIExit(t, result, 0)
		if err := json.Unmarshal([]byte(result.stdout), &records); err != nil {
			t.Fatalf("invalid query response: %v: %s", err, result.stdout)
		}
		return len(records) >= count
	})
	return records
}

func assertCLIRecordedLaunchAttempt(t *testing.T, record core.ExecutionRecord) {
	t.Helper()
	assertCLIRecordedLaunchIdentity(t, record, core.ToolNPM, "prettier")
	if record.Duration != 0 || record.ExitCode != 0 || record.WorkingDir != "" || len(record.Args) != 0 {
		t.Fatalf("launch attempt included execution details: %#v", record)
	}
	assertCLIRecordedExecutable(t, record, "prettier")
}

func assertCLIZshRecordedLaunchAttempt(t *testing.T, record core.ExecutionRecord) {
	t.Helper()
	assertCLIRecordedLaunchIdentity(t, record, core.ToolHomebrew, "listener-tool")
	if record.Timestamp.IsZero() {
		t.Fatal("zsh launch timestamp is missing")
	}
	assertCLIRecordedExecutable(t, record, "listener-tool")
}

func assertCLIRecordedLaunchIdentity(t *testing.T, record core.ExecutionRecord, tool, command string) {
	t.Helper()
	if record.Kind != core.ExecutionKindLaunchAttempt || record.Tool != tool || record.Command != command {
		t.Fatalf("recorded launch identity = %#v", record)
	}
}

func assertCLIRecordedExecutable(t *testing.T, record core.ExecutionRecord, executable string) {
	t.Helper()
	if got, _ := record.Metadata["executable"].(string); got != executable {
		t.Fatalf("launch executable metadata = %q, want %q", got, executable)
	}
}

func assertCLIRecordedPackageUse(t *testing.T, f *cliFixture) {
	t.Helper()
	assertCLIRecordedToolUse(t, f, core.ToolNPM, "prettier")
}

func assertCLIZshRecordedPackageUse(t *testing.T, f *cliFixture) {
	t.Helper()
	assertCLIRecordedToolUse(t, f, core.ToolHomebrew, "listener-tool")
}

func assertCLIRecordedToolUse(t *testing.T, f *cliFixture, tool, name string) {
	t.Helper()
	store, err := storage.NewJSONStorage(f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	pkg, err := store.GetPackage(tool, name)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.UsageCount != 1 || pkg.LastUsed.IsZero() {
		t.Fatalf("package launch usage = %#v", pkg)
	}
}
