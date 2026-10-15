package record

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/dx"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/storage"
)

func TestRecordExecutionWritesToConfiguredStorage(t *testing.T) {
	config := setupTestHomeConfig(t)
	runRecordExecution(t, homebrewExecutionPayload)
	assertStoredHomebrewExecution(t, config)
}

func TestRunExecutionRejectsInvalidConfig(t *testing.T) {
	setInvalidRecordConfig(t)
	withStdin(t, "", func() {
		err := RunExecution(NewCommand(), nil)
		hasError := err != nil
		hasExpectedMessage := hasError && strings.Contains(err.Error(), "failed to parse config")
		if !hasExpectedMessage {
			t.Fatalf("RunExecution error = %v", err)
		}
	})
}

func TestRunExecutionRecordsUnmappedExecutable(t *testing.T) {
	for _, name := range []string{"pnpm", "two words", `quote"and'`, `back\slash`, "line\nbreak", "--help"} {
		t.Run(name, func(t *testing.T) {
			config := setupTestHomeConfig(t)
			if err := NewCommand().Execute([]string{"--executable=" + name}); err != nil {
				t.Fatal(err)
			}
			assertStoredUnmappedLaunch(t, config, name)
		})
	}
}

func TestRunExecutionRejectsEmptyInput(t *testing.T) {
	setupTestHomeConfig(t)
	withStdin(t, "", func() {
		if err := NewCommand().Execute([]string{"--executable="}); err == nil {
			t.Fatal("empty input must not create a launch")
		}
	})
}

func assertStoredUnmappedLaunch(t *testing.T, config *core.Config, name string) {
	t.Helper()
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one execution, got %#v", records)
	}
	assertUnmappedLaunch(t, records[0], name)
	assertUnmappedUsage(t, store, name)
}

func assertUnmappedLaunch(t *testing.T, record *core.ExecutionRecord, name string) {
	t.Helper()
	isLaunch := record.Kind == core.ExecutionKindLaunchAttempt
	correctTool := record.Tool == core.ToolExecutable
	correctCommand := record.Command == name
	validIdentity := isLaunch && correctTool && correctCommand
	if !validIdentity {
		t.Fatalf("unexpected unmapped launch: %#v", record)
	}
	wantPackages := []string{name}
	if !slices.Equal(record.PackagesAffected, wantPackages) {
		t.Fatalf("unexpected package identity: %#v", record.PackagesAffected)
	}
	if record.Metadata["executable"] != name {
		t.Fatalf("missing launch metadata: %#v", record)
	}
	if record.Timestamp.IsZero() {
		t.Fatal("missing launch timestamp")
	}
}

func assertUnmappedUsage(t *testing.T, store storage.Storage, name string) {
	t.Helper()
	pkg, err := store.GetPackage(core.ToolExecutable, name)
	if err != nil {
		t.Fatal(err)
	}
	validUsage := pkg.UsageCount == 1 && !pkg.LastUsed.IsZero()
	if !validUsage {
		t.Fatalf("unexpected usage: %#v", pkg)
	}
}

func TestRunExecutionUsesBackgroundRecorderWhenRequested(t *testing.T) {
	config := setupTestHomeConfig(t)
	locks := acquireAllRecorderSlots(t, config.Daemon.DataDir)
	defer closeRecorderSlots(t, locks)
	command := NewCommand()
	setBackgroundRecordFlag(t, command)
	withStdin(t, `{"kind":"launch_attempt"}`, func() {
		if err := RunExecution(command, nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStoreFallbackExecutionRejectsMalformedPayload(t *testing.T) {
	config := setupTestHomeConfig(t)
	err := storeFallbackExecutionFrom(config, 0, strings.NewReader("{"))
	hasError := err != nil
	hasExpectedMessage := hasError && strings.Contains(err.Error(), "failed to decode execution record")
	if !hasExpectedMessage {
		t.Fatalf("storeFallbackExecutionFrom error = %v", err)
	}
}

func TestStoreFallbackExecutionReportsStorageOpenFailure(t *testing.T) {
	config := setupTestHomeConfig(t)
	config.Storage.JSONFile = storagePathInsideFile(t)
	payload := strings.NewReader(`{"tool":"brew","command":"brew install jq"}`)
	err := storeFallbackExecutionFrom(config, 0, payload)
	hasError := err != nil
	hasExpectedMessage := hasError && strings.Contains(err.Error(), "failed to open storage")
	if !hasExpectedMessage {
		t.Fatalf("storeFallbackExecutionFrom error = %v", err)
	}
}

func TestRecordFallbackExecutionPropagatesReaderErrors(t *testing.T) {
	config := setupTestHomeConfig(t)
	assertFallbackRecordingError(t, config, errors.New("record failed"))
	assertFallbackRecordingError(t, config, context.DeadlineExceeded)
}

func TestRecordFallbackExecutionReportsLockDirectoryFailure(t *testing.T) {
	config := setupTestHomeConfig(t)
	config.Storage.JSONFile = storagePathInsideFile(t)
	err := recordFallbackExecution(config, strings.NewReader(""))
	hasError := err != nil
	hasExpectedMessage := hasError && strings.Contains(err.Error(), "failed to create fallback lock directory")
	if !hasExpectedMessage {
		t.Fatalf("recordFallbackExecution error = %v", err)
	}
}

func TestCloseStorageHandlesCloseFailure(t *testing.T) {
	closeStorage(closeFailureStorage{err: errors.New("close failed")})
}

func setInvalidRecordConfig(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(testConfigPath()), core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testConfigPath(), []byte("{"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
}

func setBackgroundRecordFlag(t *testing.T, command *dx.Command) {
	t.Helper()
	_, err := command.Flags().Parse([]string{"--background"})
	if err != nil {
		t.Fatal(err)
	}
}

func storagePathInsideFile(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(blocker, "executions.json")
}

func assertFallbackRecordingError(t *testing.T, config *core.Config, recordErr error) {
	t.Helper()
	err := recordFallbackExecution(config, recorderErrorReader{err: recordErr})
	if !errors.Is(err, recordErr) {
		t.Fatalf("recordFallbackExecution error = %v, want %v", err, recordErr)
	}
}

type closeFailureStorage struct {
	storage.Storage
	err error
}

func (s closeFailureStorage) Close() error {
	return s.err
}

func TestRecordExecutionDropsConcurrentFallback(t *testing.T) {
	config := setupTestHomeConfig(t)
	lock := acquireFallbackRecordLockForTest(t, config)
	defer releaseFallbackRecordLockForTest(t, lock)
	payload := `{"tool":"brew","command":"brew install jq"}`
	withStdin(t, payload, func() {
		err := RunExecution(NewCommand(), nil)
		hasBusyError := err != nil && strings.Contains(err.Error(), "remained busy")
		if !hasBusyError {
			t.Fatalf("recordExecution error = %v", err)
		}
	})
	assertFallbackRecordDropped(t, config)
}

func TestLaunchAttemptEnrichmentPreservesResolvedIdentity(t *testing.T) {
	config := setupTestHomeConfig(t)
	record := &core.ExecutionRecord{
		Kind: core.ExecutionKindLaunchAttempt, Tool: core.ToolNPM, Command: "prettier",
		PackagesAffected: []string{"prettier"}, Metadata: map[string]interface{}{"executable": "prettier"},
	}
	enrichRecord(config, record)
	hasPackage := len(record.PackagesAffected) == 1 && record.PackagesAffected[0] == "prettier"
	identityPreserved := record.Tool == core.ToolNPM && record.Command == "prettier" && hasPackage
	if !identityPreserved {
		t.Fatalf("launch identity changed during enrichment: %#v", record)
	}
	if record.Timestamp.IsZero() {
		t.Fatal("launch timestamp was not filled")
	}
}

func TestRecorderSlotArgumentValidation(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  int
		valid bool
	}{
		{name: "zero", args: []string{"0"}, valid: true},
		{name: "last slot", args: []string{"3"}, want: 3, valid: true},
		{name: "missing", valid: false},
		{name: "extra", args: []string{"0", "1"}, valid: false},
		{name: "not a number", args: []string{"bad"}, valid: false},
		{name: "negative", args: []string{"-1"}, valid: false},
		{name: "past limit", args: []string{"4"}, valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := recorderSlotArgument(test.args)
			isValid := err == nil
			slotMatches := !test.valid || got == test.want
			invalidResult := isValid != test.valid || !slotMatches
			if invalidResult {
				t.Fatalf("slot argument %v = (%d, %v)", test.args, got, err)
			}
		})
	}
}

func TestRecorderCommandsAreHiddenAndValidateArguments(t *testing.T) {
	commands := []*dx.Command{NewSupervisorCommand(), NewWorkerCommand()}
	for _, cmd := range commands {
		if !cmd.IsHidden {
			t.Fatalf("internal command %q is visible", cmd.Use)
		}
		if err := cmd.RunE(cmd, nil); err == nil {
			t.Fatalf("internal command %q accepted missing slot", cmd.Use)
		}
	}
}

func TestWithRecorderSlotLoadsConfigBeforeDescriptor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(testConfigPath()), core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testConfigPath(), []byte("{"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	err := withRecorderSlot([]string{"0"}, func(*core.Config, *os.File, int) error { return nil })
	if err == nil {
		t.Fatal("invalid config must prevent opening an inherited descriptor")
	}
}

func TestWithRecorderSlotUsesInheritedDescriptor(t *testing.T) {
	config := setupTestHomeConfig(t)
	marker := filepath.Join(t.TempDir(), "worker-ran")
	lock := createRecorderSlotFile(t, config)
	runRecorderSlotProcess(t, lock, marker)
	assertRecorderSlotMarker(t, marker)
}

func testConfigPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "diu", "config.json")
}

func createRecorderSlotFile(t *testing.T, config *core.Config) *os.File {
	t.Helper()
	if err := os.MkdirAll(config.Daemon.DataDir, core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(core.RecorderSlotPath(config.Daemon.DataDir, 0), os.O_CREATE|os.O_RDWR, core.PrivateFileMode)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestRecorderSlotProcessHelper(t *testing.T) {
	if os.Getenv("DIU_TEST_RECORDER_SLOT") != "1" {
		return
	}
	t.Setenv("HOME", os.Getenv("DIU_TEST_RECORDER_SLOT_HOME"))
	err := withRecorderSlot([]string{"0"}, func(_ *core.Config, _ *os.File, slot int) error {
		return os.WriteFile(os.Getenv("DIU_TEST_RECORDER_SLOT_MARKER"), []byte("slot=0"), core.PrivateFileMode)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func runRecorderSlotProcess(t *testing.T, lock *os.File, marker string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecorderSlotProcessHelper$")
	cmd.Env = append(os.Environ(), "DIU_TEST_RECORDER_SLOT=1", "DIU_TEST_RECORDER_SLOT_HOME="+os.Getenv("HOME"), "DIU_TEST_RECORDER_SLOT_MARKER="+marker)
	cmd.ExtraFiles = []*os.File{lock}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("recorder slot command failed: %v\n%s", err, output)
	}
}

func assertRecorderSlotMarker(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	markerMatches := string(data) == "slot=0"
	if !markerMatches {
		t.Fatalf("recorder slot marker = %q, %v", data, err)
	}
}

const homebrewExecutionPayload = `{
	"tool":"brew",
	"command":"brew install jq",
	"args":["install","jq"],
	"exit_code":0,
	"duration_ms":1200,
	"packages_affected":["jq"]
}`

func setupTestHomeConfig(t *testing.T) *core.Config {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	config := core.DefaultConfig()
	config.Monitoring.EnabledTools = []string{}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{}
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

func runRecordExecution(t *testing.T, payload string) {
	t.Helper()
	withStdin(t, payload, func() {
		if err := RunExecution(NewCommand(), nil); err != nil {
			t.Fatalf("RunExecution failed: %v", err)
		}
	})
}

func assertStoredHomebrewExecution(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{Tool: core.ToolHomebrew})
	if err != nil {
		t.Fatal(err)
	}
	isSingleRecord := len(records) == 1
	isHomebrewRecord := isSingleRecord && records[0].Tool == core.ToolHomebrew
	if !isHomebrewRecord {
		t.Fatalf("homebrew records = %+v", records)
	}
}

func assertFallbackRecordDropped(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("concurrent fallback stored %d executions", len(records))
	}
	_, contended, err := observability.ReadFallbackContention(config.Daemon.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !contended {
		t.Fatal("fallback contention was not recorded")
	}
}

func acquireFallbackRecordLockForTest(t *testing.T, config *core.Config) *os.File {
	t.Helper()
	lock, err := tryAcquireFallbackRecordLock(config.Storage.JSONFile)
	if err != nil {
		t.Fatalf("tryAcquireFallbackRecordLock error = %v", err)
	}
	if lock == nil {
		t.Fatal("tryAcquireFallbackRecordLock did not acquire the lock")
	}
	return lock
}

func releaseFallbackRecordLockForTest(t *testing.T, lock *os.File) {
	t.Helper()
	if err := releaseFallbackRecordLock(lock); err != nil {
		t.Fatalf("releaseFallbackRecordLock failed: %v", err)
	}
}

func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = oldStdin
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	fn()
}
