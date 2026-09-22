//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

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

func TestCLIRecordsActualWrapperExecutionWithoutDaemon(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	assertCLICommandContract(t, f, "bash", "probe")
	records := waitCLIRecords(t, f, 1)
	if len(records) != 1 {
		t.Fatalf("expected one tool execution, got %d: %#v", len(records), records)
	}
	record := records[0]
	if record.ExitCode != 17 || record.WorkingDir != f.home {
		t.Fatalf("record lost execution details: %#v", record)
	}
	if len(record.Args) != 6 || record.Args[1] != "" || record.Args[4] != "line\nbreak" {
		t.Fatalf("record changed argument boundaries: %#v", record.Args)
	}
}

func replaceCLIRecorder(t *testing.T, f *cliFixture, script string) {
	t.Helper()
	path := filepath.Join(f.bin, "diu")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, path, script, 0o700)
}

func TestCLISlowRecorderDoesNotHoldCommandPipesOpen(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	replaceCLIRecorder(t, f, "#!/bin/bash\n/bin/cat > \"$HOME/received-event\"\n/bin/sleep 30\n")
	started := time.Now()
	assertCLICommandContract(t, f, "bash", "probe")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("command waited for background recording: %s", elapsed)
	}
	awaitCLI(t, func() bool {
		_, err := os.Stat(filepath.Join(f.home, "received-event"))
		return err == nil
	})
}

func TestCLIRecorderSubcommandsAreNotRecordedAgain(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	replaceCLIRecorder(t, f, recursiveRecorderScript)
	assertCLICommandContract(t, f, "bash", "probe")
	marker := filepath.Join(f.home, "recorder-done")
	awaitCLI(t, func() bool { _, err := os.Stat(marker); return err == nil })
	assertCLIFile(t, filepath.Join(f.home, "recorder-calls"), "called\n")
	assertCLIFile(t, marker, "17\n")
}

const recursiveRecorderScript = `#!/bin/bash
# Stop after one nested attempt even if the guard regresses.
[ ! -e "$HOME/recorder-calls" ] || { printf 'recursive\n' >> "$HOME/recorder-calls"; exit 99; }
printf 'called\n' > "$HOME/recorder-calls"
/bin/cat > /dev/null
probe nested </dev/null >/dev/null 2>&1
printf '%s\n' "$?" > "$HOME/recorder-done"
`

func TestCLIOrphanedWrappersRunCommandsWithoutRecording(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	if err := os.Remove(filepath.Join(f.bin, "diu")); err != nil {
		t.Fatal(err)
	}
	before := snapshotCLIHome(t, f.home)
	assertCLICommandContract(t, f, "bash", "probe")
	after := snapshotCLIHome(t, f.home)
	if len(before) != len(after) {
		t.Fatal("orphaned wrapper created files")
	}
	for path, state := range before {
		if after[path] != state {
			t.Errorf("orphaned wrapper modified %s", path)
		}
	}
}

func TestCLIRecordingDoesNotInvokeHelpersFromPATH(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	for _, name := range []string{"date", "cat", "whoami", "nc"} {
		script := "#!/bin/sh\nprintf '%s\\n' \"$0\" >> \"$HOME/poisoned-helper\"\nexit 99\n"
		writeCLIFile(t, filepath.Join(f.bin, name), script, 0o700)
	}
	assertCLICommandContract(t, f, "bash", "probe")
	waitCLIRecords(t, f, 1)
	assertCLIMissing(t, filepath.Join(f.home, "poisoned-helper"))
}

func TestCLILockedRecorderDropsEventsWithoutBlockingCommands(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	lock := openFallbackLock(t, f.config.Storage.JSONFile+".fallback.lock")
	defer closeFallbackLock(t, lock)
	started := time.Now()
	assertCLICommandContract(t, f, "bash", "probe")
	if time.Since(started) > 2*time.Second {
		t.Fatal("command waited on the recorder lock")
	}
	marker := filepath.Join(f.config.Daemon.DataDir, "fallback-contention")
	awaitCLI(t, func() bool { _, err := os.Stat(marker); return err == nil })
	result := f.cli(t, "query", "--format", "json")
	if strings.TrimSpace(result.stdout) != "[]" {
		t.Fatalf("contended recorder wrote execution history: %#v", result)
	}
}
