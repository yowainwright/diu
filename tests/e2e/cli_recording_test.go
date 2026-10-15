//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
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
	finishCLIZsh(t, command, input, stderr)
}

func TestCLIZshRecordsToolsMissingFromInventory(t *testing.T) {
	f := unmappedCLIFixture(t)
	lateTool := prepareCLIUnmappedTools(t, f)
	command, input, output, stderr := startCLIZsh(t, f)
	writeCLIFile(t, lateTool, unmappedCLIToolScript, 0o700)
	for _, name := range []string{"pnpm", "just", "unlisted-tool"} {
		writeZshInput(t, input, name+" --version\nprint -r -- DIU_COMMAND_$?\n")
		waitCLIZshOutput(t, output, "DIU_COMMAND_0")
		assertCLIFile(t, filepath.Join(f.home, name+"-ran"), "")
	}
	records := waitCLIRecords(t, f, 3)
	assertCLIUnmappedRecords(t, f, records)
	finishCLIZsh(t, command, input, stderr)
}

func prepareCLIUnmappedTools(t *testing.T, f *cliFixture) string {
	t.Helper()
	writeCLIFile(t, filepath.Join(f.home, ".tool-versions"), "pnpm 12.5.1\n", 0o600)
	before := snapshotCLIHome(t, f.home)
	f.setup(t)
	assertCLIChanges(t, before, snapshotCLIHome(t, f.home))
	assertCLIUnmappedTools(t, f)
	lateTool := filepath.Join(f.bin, "unlisted-tool")
	if err := os.Remove(lateTool); err != nil {
		t.Fatal(err)
	}
	return lateTool
}

func unmappedCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := newCLIFixture(t)
	f.config.Monitoring.EnabledTools = []string{core.ToolPNPM}
	monitoring := &f.config.Monitoring
	monitoring.Filesystem.WatchPaths = map[string][]string{}
	writeCLIConfig(t, f)
	for _, name := range []string{"pnpm", "just", "unlisted-tool"} {
		writeCLIFile(t, filepath.Join(f.bin, name), unmappedCLIToolScript, 0o700)
	}
	return f
}

const unmappedCLIToolScript = `#!/bin/sh
case "$1" in
    --version|install) ;;
    *) exit 1 ;;
esac
: > "$HOME/${0##*/}-ran"
printf 'fixture 1.0\n'
`

func assertCLIUnmappedTools(t *testing.T, f *cliFixture) {
	t.Helper()
	integration := filepath.Join(f.config.Daemon.DataDir, "shell-integration.zsh")
	contents := readCLIFile(t, integration)
	for _, name := range []string{"pnpm", "just", "unlisted-tool"} {
		if strings.Contains(contents, "_DIU_TOOL_MAP+=('"+name+"'") {
			t.Fatalf("fixture tool unexpectedly present in inventory map: %s", name)
		}
		assertCLIMissing(t, filepath.Join(f.home, name+"-ran"))
	}
}

func assertCLIUnmappedRecords(t *testing.T, f *cliFixture, records []core.ExecutionRecord) {
	t.Helper()
	counts := make(map[string]int)
	for _, record := range records {
		assertCLIRecordedLaunchIdentity(t, record, core.ToolExecutable, record.Command)
		counts[record.Command]++
	}
	if len(records) != 3 {
		t.Fatalf("expected three launch records, got %#v", records)
	}
	for _, name := range []string{"pnpm", "just", "unlisted-tool"} {
		if counts[name] != 1 {
			t.Fatalf("%s launch count = %d, want 1", name, counts[name])
		}
		assertCLIRecordedToolUse(t, f, core.ToolExecutable, name, 1)
	}
}

func TestCLIZshRecordsExternalCommandBehindFunction(t *testing.T) {
	f := unmappedCLIFixture(t)
	f.setup(t)
	zshrc := filepath.Join(f.home, ".zshrc")
	contents := readCLIFile(t, zshrc) + "\npnpm() { command pnpm \"$@\"; }\ndiu_fixture_function() { :; }\n"
	writeCLIFile(t, zshrc, contents, 0o600)
	command, input, output, stderr := startCLIZsh(t, f)
	writeZshInput(t, input, "pnpm --version\npnpm install\ncommand pnpm --version\ndiu_fixture_function\nprint -r -- DIU_EXTERNAL_$?\n")
	waitCLIZshOutput(t, output, "DIU_EXTERNAL_0")
	assertCLIFile(t, filepath.Join(f.home, "pnpm-ran"), "")
	records := waitCLIRecords(t, f, 3)
	if len(records) != 3 {
		t.Fatalf("expected three pnpm launches, got %#v", records)
	}
	assertCLIRecordedLaunches(t, records, core.ToolExecutable, "pnpm")
	assertCLIRecordedToolUse(t, f, core.ToolExecutable, "pnpm", 3)
	finishCLIZsh(t, command, input, stderr)
}

func TestCLIZshRejectsNonCommandMatches(t *testing.T) {
	f := unmappedCLIFixture(t)
	f.setup(t)
	command, input, output, stderr := startCLIZsh(t, f)
	negative := "echo pnpm just unlisted-tool\n: > unlisted-tool\ncommand -v pnpm\nbuiltin pnpm\ndiu --version\nmissing-diu-fixture-command || true\n"
	writeZshInput(t, input, negative)
	writeZshInput(t, input, filepath.Join(f.bin, "unlisted-tool")+" --version\nprint -r -- DIU_EXTERNAL_$?\n")
	waitCLIZshOutput(t, output, "DIU_EXTERNAL_0")
	records := waitCLIRecords(t, f, 1)
	if len(records) != 1 {
		t.Fatalf("non-command matches produced records: %#v", records)
	}
	assertCLIRecordedLaunchIdentity(t, records[0], core.ToolExecutable, "unlisted-tool")
	finishCLIZsh(t, command, input, stderr)
}

func TestCLIZshPreservesUnmappedExecutableNames(t *testing.T) {
	f := unmappedCLIFixture(t)
	name := `odd "name\tool`
	path := filepath.Join(f.bin, name)
	writeCLIFile(t, path, unmappedCLIToolScript, 0o700)
	f.setup(t)
	command, input, output, stderr := startCLIZsh(t, f)
	writeZshInput(t, input, "'"+path+"' --version\nprint -r -- DIU_QUOTED_$?\n")
	waitCLIZshOutput(t, output, "DIU_QUOTED_0")
	records := waitCLIRecords(t, f, 1)
	if len(records) != 1 {
		t.Fatalf("expected one quoted launch, got %#v", records)
	}
	assertCLIRecordedLaunchIdentity(t, records[0], core.ToolExecutable, name)
	assertCLIRecordedToolUse(t, f, core.ToolExecutable, name, 1)
	finishCLIZsh(t, command, input, stderr)
}

func TestCLIZshRecordsHomeRelativeExecutable(t *testing.T) {
	f := unmappedCLIFixture(t)
	f.setup(t)
	command, input, output, stderr := startCLIZsh(t, f)
	writeZshInput(t, input, homeRelativeCLICommands)
	waitCLIZshOutput(t, output, "DIU_HOME_0")
	assertCLIFile(t, filepath.Join(f.home, "unlisted-tool-ran"), "")
	records := waitCLIRecords(t, f, 1)
	if len(records) != 1 {
		t.Fatalf("expected one home-relative launch, got %#v", records)
	}
	assertCLIRecordedLaunchIdentity(t, records[0], core.ToolExecutable, "unlisted-tool")
	assertCLIRecordedToolUse(t, f, core.ToolExecutable, "unlisted-tool", 1)
	finishCLIZsh(t, command, input, stderr)
}

const homeRelativeCLICommands = `'~/bin/unlisted-tool' --version || true
"~/bin/unlisted-tool" --version || true
\~/bin/unlisted-tool --version || true
~/bin/unlisted-tool --version
print -r -- DIU_HOME_$?
`

func TestCLIZshPreservesHomeRelativeQuoting(t *testing.T) {
	f := unmappedCLIFixture(t)
	name := "$(touch DIU_REEVALUATED)"
	writeCLIFile(t, filepath.Join(f.bin, name), unmappedCLIToolScript, 0o700)
	f.setup(t)
	command, input, output, stderr := startCLIZsh(t, f)
	writeZshInput(t, input, "~/bin/'"+name+"' --version\nprint -r -- DIU_QUOTED_HOME_$?\n")
	waitCLIZshOutput(t, output, "DIU_QUOTED_HOME_0")
	assertCLIMissing(t, filepath.Join(f.home, "DIU_REEVALUATED"))
	assertCLIFile(t, filepath.Join(f.home, name+"-ran"), "")
	records := waitCLIRecords(t, f, 1)
	if len(records) != 1 {
		t.Fatalf("expected one literal executable launch, got %#v", records)
	}
	assertCLIRecordedLaunchIdentity(t, records[0], core.ToolExecutable, name)
	assertCLIRecordedToolUse(t, f, core.ToolExecutable, name, 1)
	finishCLIZsh(t, command, input, stderr)
}

func TestCLIZshPreservesMappedToolRecording(t *testing.T) {
	f := mappedAliasCLIFixture(t)
	f.setup(t)
	command, input, output, stderr := startCLIZsh(t, f)
	writeZshInput(t, input, "mapped-tool\nmapped-alias\nprint -r -- DIU_MAPPED_$?\n")
	waitCLIZshOutput(t, output, "DIU_MAPPED_0")
	records := waitCLIRecords(t, f, 2)
	if len(records) != 2 {
		t.Fatalf("expected two mapped launches, got %#v", records)
	}
	for _, record := range records {
		assertCLIRecordedLaunchIdentity(t, record, core.ToolHomebrew, record.Command)
	}
	assertCLIRecordedToolUse(t, f, core.ToolHomebrew, "mapped-tool", 2)
	finishCLIZsh(t, command, input, stderr)
}

func mappedAliasCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := newCLIFixture(t)
	path := filepath.Join(f.home, "Cellar", "mapped-tool", "1.0", "bin", "mapped-tool")
	writeCLIFile(t, path, "#!/bin/sh\nexit 0\n", 0o700)
	for _, name := range []string{"mapped-tool", "mapped-alias"} {
		if err := os.Symlink(path, filepath.Join(f.managed, name)); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestCLIZshRecordsRepeatedUnmappedCommandsWithoutDaemon(t *testing.T) {
	f := unmappedCLIFixture(t)
	f.setup(t)
	assertCLIExit(t, f.cli(t, "daemon", "stop"), 0)
	command, input, output, stderr := startCLIZsh(t, f)
	hashCommand := "hash pnpm=" + filepath.Join(f.bin, "pnpm")
	writeZshInput(t, input, hashCommand+"\npnpm --version\npnpm --version\nprint -r -- DIU_REPEAT_$?\n")
	waitCLIZshOutput(t, output, "DIU_REPEAT_0")
	records := waitCLIRecords(t, f, 2)
	if len(records) != 2 {
		t.Fatalf("expected two repeated launches, got %#v", records)
	}
	assertCLIRecordedLaunches(t, records, core.ToolExecutable, "pnpm")
	assertCLIRecordedToolUse(t, f, core.ToolExecutable, "pnpm", 2)
	finishCLIZsh(t, command, input, stderr)
}

func TestCLIZshTracksUVExecutablesWithoutProjectArguments(t *testing.T) {
	f := uvCLIFixture(t)
	f.setup(t)
	command, input, output, stderr := startCLIZsh(t, f)
	writeZshInput(t, input, uvCLICommands+"print -r -- DIU_'UV_COMMANDS_DONE'\n")
	waitCLIZshOutput(t, output, "DIU_UV_COMMANDS_DONE")
	records := waitCLIRecords(t, f, 4)
	assertCLIRecordedLaunches(t, records, core.ToolExecutable, "uv")
	writeZshInput(t, input, "listener-tool\nprint -r -- DIU_'UV_EXECUTABLE_DONE'\n")
	waitCLIZshOutput(t, output, "DIU_UV_EXECUTABLE_DONE")
	records = waitCLIRecords(t, f, 5)
	if len(records) != 5 {
		t.Fatalf("uv project arguments produced extra launches: %#v", records)
	}
	assertCLIRecordedLaunchIdentity(t, records[0], core.ToolUV, "listener-tool")
	finishCLIZsh(t, command, input, stderr)
}

const uvCLICommands = `uv add listener-tool
uv remove listener-tool
uv pip install listener-tool
uv tool run --from listener-tool listener-tool
`

func uvCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := newCLIFixture(t)
	f.config.Monitoring.EnabledTools = []string{core.ToolUV}
	paths := []string{f.managed}
	monitoring := &f.config.Monitoring
	monitoring.Filesystem.WatchPaths = map[string][]string{core.ToolUV: paths}
	writeCLIConfig(t, f)
	writeCLIFile(t, filepath.Join(f.managed, "listener-tool"), "#!/bin/sh\nexit 0\n", 0o700)
	uv := `#!/bin/sh
if [ "$1 $2" = 'tool list' ]; then
    printf 'listener-tool v1.0.0\n- listener-tool\n'
fi
`
	writeCLIFile(t, filepath.Join(f.bin, "uv"), uv, 0o700)
	return f
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
	hasTiming := record.Duration != 0 || record.ExitCode != 0
	hasContext := record.WorkingDir != "" || len(record.Args) != 0
	hasExecutionDetails := hasTiming || hasContext
	if hasExecutionDetails {
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
	isLaunch := record.Kind == core.ExecutionKindLaunchAttempt
	hasIdentity := record.Tool == tool && record.Command == command
	isExpectedLaunch := isLaunch && hasIdentity
	if !isExpectedLaunch {
		t.Fatalf("recorded launch identity = %#v", record)
	}
}

func assertCLIRecordedLaunches(t *testing.T, records []core.ExecutionRecord, tool, command string) {
	t.Helper()
	for _, record := range records {
		assertCLIRecordedLaunchIdentity(t, record, tool, command)
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
	assertCLIRecordedToolUse(t, f, core.ToolNPM, "prettier", 1)
}

func assertCLIZshRecordedPackageUse(t *testing.T, f *cliFixture) {
	t.Helper()
	assertCLIRecordedToolUse(t, f, core.ToolHomebrew, "listener-tool", 1)
}

func assertCLIRecordedToolUse(t *testing.T, f *cliFixture, tool, name string, want int) {
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
	hasUsage := pkg.UsageCount == want && !pkg.LastUsed.IsZero()
	if !hasUsage {
		t.Fatalf("package launch usage = %#v", pkg)
	}
}
