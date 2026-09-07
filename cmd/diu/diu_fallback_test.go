package main

import (
	"bytes"
	"context"
	"errors"
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
