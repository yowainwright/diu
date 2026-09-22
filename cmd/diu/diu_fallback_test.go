package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/observability"
	"github.com/yowainwright/diu/internal/storage"
)

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

func TestExecutableWrapperPreservesCommandSelection(t *testing.T) {
	config := setupTestHomeConfig(t)
	preferredDir, managedDir := t.TempDir(), t.TempDir()
	name := "node"
	original := filepath.Join(managedDir, name)
	writeExecutableForTest(t, original, "#!/bin/sh\nprintf 'managed\\n'\n")
	writeExecutableForTest(t, filepath.Join(preferredDir, name), "#!/bin/sh\nprintf 'preferred\\n'\nexit 7\n")
	config.Monitoring.EnabledTools = []string{core.ToolHomebrew}
	config.Monitoring.Filesystem.WatchPaths = map[string][]string{core.ToolHomebrew: {managedDir}}
	if err := os.MkdirAll(config.Monitoring.Process.WrapperDir, core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", preferredDir+":"+managedDir+":/usr/bin:/bin")
	if err := installExecutableWrappers(config); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", config.Monitoring.Process.WrapperDir+":"+os.Getenv("PATH"))
	assertSelectedCommand(t, name, "preferred\n", 7)
}

func assertSelectedCommand(t *testing.T, name, want string, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name)
	output, err := cmd.Output()
	if string(output) != want {
		t.Fatalf("selected command output = %q, want %q (error: %v)", output, want, err)
	}
	if cmd.ProcessState.ExitCode() != exitCode {
		t.Fatalf("exit code = %d, want %d", cmd.ProcessState.ExitCode(), exitCode)
	}
}

func TestWrappersFollowChangedPATH(t *testing.T) {
	for _, template := range []string{"executable", "process"} {
		t.Run(template, func(t *testing.T) {
			config := setupTestHomeConfig(t)
			original := writeFallbackOriginal(t)
			wrapper := installFallbackTestWrapper(t, config, original, template)
			preferred := t.TempDir()
			name := filepath.Base(wrapper)
			writeExecutableForTest(t, filepath.Join(preferred, name), "#!/bin/sh\nprintf 'preferred\\n'\nexit 7\n")
			alias := filepath.Join(t.TempDir(), "wrapper alias")
			if err := os.Symlink(filepath.Dir(wrapper), alias); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", alias+":"+filepath.Dir(wrapper)+":"+preferred+":/usr/bin:/bin")
			assertSelectedCommand(t, name, "preferred\n", 7)
		})
	}
}

func TestWrappersHandleReturningShim(t *testing.T) {
	for _, template := range []string{"executable", "process"} {
		t.Run(template, func(t *testing.T) {
			config := setupTestHomeConfig(t)
			original := writeFallbackOriginal(t)
			wrapper := installFallbackTestWrapper(t, config, original, template)
			shimDir := t.TempDir()
			shim := filepath.Join(shimDir, filepath.Base(wrapper))
			writeExecutableForTest(t, shim, "#!/bin/bash\nexec \"$DIU_TEST_WRAPPER\" \"$@\"\n")
			t.Setenv("DIU_TEST_WRAPPER", wrapper)
			t.Setenv("PATH", filepath.Dir(wrapper)+":"+shimDir+":/usr/bin:/bin")
			runContendedWrapper(t, wrapper)
		})
	}
}

func TestWrapperDiscoveryPrefersPATHOrder(t *testing.T) {
	preferred, other := t.TempDir(), t.TempDir()
	name := "tool"
	writeExecutableForTest(t, filepath.Join(preferred, name), "#!/bin/sh\nexit 0\n")
	writeExecutableForTest(t, filepath.Join(other, name), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", preferred+":"+other)
	targets := make(map[string]executableWrapper)
	addExecutableDir(targets, core.ToolHomebrew, other)
	addExecutableDir(targets, core.ToolGoBinary, preferred)
	if targets[name].Tool != core.ToolGoBinary {
		t.Fatalf("selected %s, want Go binary first in PATH", targets[name].Tool)
	}
}

func TestWrapperDiscoveryResolvesSharedBinOwnership(t *testing.T) {
	for _, tools := range [][]string{{core.ToolHomebrew, core.ToolNPM}, {core.ToolNPM, core.ToolHomebrew}} {
		t.Run(tools[0], func(t *testing.T) {
			bin := sharedWrapperBin(t)
			t.Setenv("PATH", bin)
			targets := make(map[string]executableWrapper)
			for _, tool := range tools {
				addExecutableDir(targets, tool, bin)
			}
			for name, want := range map[string]executableWrapper{
				"tsc":      {Tool: core.ToolNPM, Package: "typescript"},
				"scoped":   {Tool: core.ToolNPM, Package: "@scope/cli"},
				"jq":       {Tool: core.ToolHomebrew, Package: "jq"},
				"brew-tsc": {Tool: core.ToolHomebrew, Package: "typescript"},
			} {
				got := targets[name]
				wrongOwner := got.Tool != want.Tool || got.Package != want.Package
				if wrongOwner {
					t.Errorf("%s owner = %s/%s, want %s/%s", name, got.Tool, got.Package, want.Tool, want.Package)
				}
			}
		})
	}
}

func sharedWrapperBin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"tsc":      "lib/node_modules/typescript/bin/tsc",
		"scoped":   "lib/node_modules/@scope/cli/bin/scoped",
		"jq":       "Cellar/jq/1.8/bin/jq",
		"brew-tsc": "Cellar/typescript/5/libexec/lib/node_modules/typescript/bin/tsc",
	} {
		original := filepath.Join(root, target)
		writeSharedBinExecutable(t, original, filepath.Join(bin, name))
	}
	return bin
}

func writeSharedBinExecutable(t *testing.T, original, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(original), core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	writeExecutableForTest(t, original, "#!/bin/sh\nexit 0\n")
	if err := os.Symlink(original, link); err != nil {
		t.Fatal(err)
	}
}

func TestWrappersSkipPreviousWrapperDirectory(t *testing.T) {
	for _, template := range []string{"executable", "process"} {
		t.Run(template, func(t *testing.T) {
			config := setupTestHomeConfig(t)
			original := writeFallbackOriginal(t)
			previous := installFallbackTestWrapper(t, config, original, template)
			config.Monitoring.Process.WrapperDir = t.TempDir()
			current := installFallbackTestWrapper(t, config, original, template)
			preferred := t.TempDir()
			name := filepath.Base(current)
			writeExecutableForTest(t, filepath.Join(preferred, name), "#!/bin/sh\nprintf 'preferred\\n'\nexit 7\n")
			path := filepath.Dir(current) + ":" + filepath.Dir(previous) + ":" + preferred + ":/usr/bin:/bin"
			t.Setenv("PATH", path)
			assertSelectedCommand(t, name, "preferred\n", 7)
		})
	}
}

func TestWrappersRejectGeneratedOriginalWithoutPATH(t *testing.T) {
	for _, template := range []string{"executable", "process"} {
		t.Run(template, func(t *testing.T) {
			config := setupTestHomeConfig(t)
			previous := installFallbackTestWrapper(t, config, writeFallbackOriginal(t), template)
			config.Monitoring.Process.WrapperDir = t.TempDir()
			current := installFallbackTestWrapper(t, config, previous, template)
			path := filepath.Dir(current) + ":" + filepath.Dir(previous) + ":/usr/bin:/bin"
			t.Setenv("PATH", path)
			assertSelectedCommand(t, filepath.Base(current), "", 127)
		})
	}
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
	t.Setenv("PATH", binDir+":/usr/bin:/bin")
}

func TestWrappersBoundStorageLockWait(t *testing.T) {
	binaryDir := buildFallbackTestBinary(t)
	for _, template := range []string{"executable", "process"} {
		t.Run(template, func(t *testing.T) {
			config := setupTestHomeConfig(t)
			t.Setenv("PATH", binaryDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			original := writeFallbackOriginal(t)
			wrapper := installFallbackTestWrapper(t, config, original, template)
			unlock := holdFallbackStorageLock(t, config)
			runContendedWrapper(t, wrapper)
			unlock()
			assertFallbackRecordDropped(t, config)
		})
	}
}

func buildFallbackTestBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "diu")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build recorder: %v\n%s", err, output)
	}
	return dir
}

func writeFallbackOriginal(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "original-tool")
	script := "#!/bin/bash\nprintf 'original output\\n'\nprintf 'original error\\n' >&2\nexit 7\n"
	if err := os.WriteFile(path, []byte(script), core.OwnerExecutableMode); err != nil {
		t.Fatal(err)
	}
	return path
}

func installFallbackTestWrapper(t *testing.T, config *core.Config, original, template string) string {
	t.Helper()
	if template == "process" {
		return installFallbackProcessWrapper(t, config, original)
	}
	if err := os.MkdirAll(config.Monitoring.Process.WrapperDir, core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	target := executableWrapper{Name: "wrapped-tool", OriginalPath: original, Tool: core.ToolGo, Package: "test-tool"}
	if err := writeExecutableWrapper(config, target); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(config.Monitoring.Process.WrapperDir, target.Name)
}

func installFallbackProcessWrapper(t *testing.T, config *core.Config, original string) string {
	t.Helper()
	monitor := monitors.NewProcessMonitor("test-tool", original)
	if err := monitor.Initialize(config); err != nil {
		t.Fatal(err)
	}
	if err := monitor.InstallWrapper(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(config.Monitoring.Process.WrapperDir, filepath.Base(original))
}

func holdFallbackStorageLock(t *testing.T, config *core.Config) func() {
	t.Helper()
	manifest := config.Storage.JSONFile
	if err := os.MkdirAll(filepath.Dir(manifest), core.OwnerDirectoryMode); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(manifest+".lock", os.O_CREATE|os.O_RDWR, core.PrivateFileMode)
	if err != nil {
		t.Fatal(err)
	}
	unlock := sync.OnceFunc(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(unlock)
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return unlock
}

func runContendedWrapper(t *testing.T, wrapper string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, wrapper)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	err := cmd.Run()
	t.Logf("wrapper completed in %s", time.Since(started))
	if ctx.Err() != nil {
		t.Fatalf("wrapper exceeded fallback wait budget: %v", ctx.Err())
	}
	assertFallbackCommandResult(t, err, stdout.String(), stderr.String())
}

func assertFallbackCommandResult(t *testing.T, err error, stdout, stderr string) {
	t.Helper()
	var exitErr *exec.ExitError
	hasOriginalExit := errors.As(err, &exitErr) && exitErr.ExitCode() == 7
	if !hasOriginalExit {
		t.Fatalf("wrapper changed original exit status: %v", err)
	}
	hasOriginalOutput := stdout == "original output\n" && stderr == "original error\n"
	if !hasOriginalOutput {
		t.Fatalf("wrapper changed output: stdout=%q, stderr=%q", stdout, stderr)
	}
}

func TestWrappersUseSystemNC(t *testing.T) {
	socketDir := t.TempDir()
	for _, template := range []string{"executable", "process"} {
		t.Run(template, func(t *testing.T) {
			config := setupTestHomeConfig(t)
			config.Daemon.SocketPath = filepath.Join(socketDir, template)
			listener := listenForWrapperEvent(t, config.Daemon.SocketPath)
			marker := installTrackedNCProbe(t)
			original := writeFallbackOriginal(t)
			wrapper := installFallbackTestWrapper(t, config, original, template)
			runContendedWrapper(t, wrapper)
			record := readWrapperSocketRecord(t, listener)
			assertWrapperUsedSystemNC(t, marker, record)
		})
	}
}

func listenForWrapperEvent(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	if _, err := os.Stat("/usr/bin/nc"); err != nil {
		t.Skipf("system nc unavailable: %v", err)
	}
	address := &net.UnixAddr{Name: path, Net: "unix"}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return listener
}

func installTrackedNCProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "nc-calls")
	script := "#!/bin/sh\nprintf 'called\\n' >> \"$DIU_TEST_NC_CALLS\"\nexec /usr/bin/nc \"$@\"\n"
	writeExecutableForTest(t, filepath.Join(dir, "nc"), script)
	t.Setenv("DIU_TEST_NC_CALLS", marker)
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return marker
}

func readWrapperSocketRecord(t *testing.T, listener *net.UnixListener) core.ExecutionRecord {
	t.Helper()
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var record core.ExecutionRecord
	if err := json.NewDecoder(conn).Decode(&record); err != nil {
		t.Fatal(err)
	}
	return record
}

func assertWrapperUsedSystemNC(t *testing.T, marker string, record core.ExecutionRecord) {
	t.Helper()
	if record.ExitCode != 7 {
		t.Fatalf("recorded exit code = %d, want 7", record.ExitCode)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("event delivery invoked nc from PATH: %v", err)
	}
}
