package monitors

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestProcessMonitor(t *testing.T) {
	monitor := NewProcessMonitor("test-tool", "test-binary")
	nameMatches := monitor.Name() == "test-tool"
	pathMatches := monitor.binaryPath == "test-binary"
	monitorMatches := nameMatches && pathMatches
	if !monitorMatches {
		t.Fatalf("monitor = %q, %q", monitor.Name(), monitor.binaryPath)
	}
}

func TestProcessMonitorFindOriginalBinaryUsesExecutableFromPATH(t *testing.T) {
	binDir := t.TempDir()
	binaryPath := writeFakeCommand(t, binDir, "mytool", "#!/bin/sh\nexit 0\n")
	path := strings.Join([]string{"", "relative-bin", binDir}, string(os.PathListSeparator))
	t.Setenv("PATH", path)
	monitor := NewProcessMonitor("mytool", "mytool")
	originalPath, err := monitor.findOriginalBinary()
	if err != nil {
		t.Fatalf("findOriginalBinary failed: %v", err)
	}
	assertResolvedExecutablePath(t, originalPath, binaryPath)
}

func TestProcessMonitorFindOriginalBinaryUsesAbsolutePath(t *testing.T) {
	binaryPath := writeFakeCommand(t, t.TempDir(), "mytool", "#!/bin/sh\nexit 0\n")
	monitor := NewProcessMonitor("mytool", binaryPath)
	originalPath, err := monitor.findOriginalBinary()
	if err != nil {
		t.Fatalf("findOriginalBinary failed: %v", err)
	}
	assertResolvedExecutablePath(t, originalPath, binaryPath)
}

func TestProcessMonitorInitializeKeepsRequestedPathWhenMissing(t *testing.T) {
	setOnlyFakeCommand(t, "another-tool", "#!/bin/sh\nexit 0\n")
	monitor := NewProcessMonitor("missing", "missing")
	if err := monitor.Initialize(core.DefaultConfig()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if monitor.originalPath != "missing" {
		t.Fatalf("original path = %q, want missing", monitor.originalPath)
	}
}

func TestProcessMonitorParseCommand(t *testing.T) {
	monitor := NewProcessMonitor("mytool", "mytool")
	record, err := monitor.ParseCommand("mytool", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("ParseCommand failed: %v", err)
	}
	argsMatch := strings.Join(record.Args, " ") == "alpha beta"
	recordMatches := record.Tool == "mytool" && record.Command == "mytool" && argsMatch
	if !recordMatches {
		t.Fatalf("parsed record = %#v", record)
	}
}

func TestProcessMonitorGetInstalledPackagesIsNotImplemented(t *testing.T) {
	packages, err := NewProcessMonitor("test", "test").GetInstalledPackages()
	returnedError := err != nil
	returnedNoPackages := packages == nil
	invalidResult := !returnedError || !returnedNoPackages
	if invalidResult {
		t.Fatalf("GetInstalledPackages() = %v, %v; want nil and an error", packages, err)
	}
}

func TestProcessMonitorStartAndStop(t *testing.T) {
	monitor := NewProcessMonitor("test", "test")
	if err := monitor.Start(context.Background(), nil); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := monitor.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	assertContextCancelled(t, monitor.ctx)
}

func assertContextCancelled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("monitor context was not cancelled")
	}
}

func TestProcessMonitorExecuteAndTrack(t *testing.T) {
	binaryPath := writeFakeCommand(t, t.TempDir(), "testtool", "#!/bin/sh\nexit 7\n")
	monitor := NewProcessMonitor("testtool", binaryPath)
	monitor.originalPath = binaryPath
	record, err := monitor.ExecuteAndTrack("testtool", []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("ExecuteAndTrack failed: %v", err)
	}
	assertTrackedProcessRecord(t, record)
}

func assertTrackedProcessRecord(t *testing.T, record *core.ExecutionRecord) {
	t.Helper()
	toolAndCommandMatch := record.Tool == "testtool" && record.Command == "testtool alpha beta"
	recordMatches := toolAndCommandMatch && record.ExitCode == 7
	if !recordMatches {
		t.Fatalf("tracked record = %#v", record)
	}
	argsMatch := strings.Join(record.Args, " ") == "alpha beta"
	timestampPresent := !record.Timestamp.IsZero()
	detailsPresent := argsMatch && timestampPresent
	if !detailsPresent {
		t.Fatalf("tracked record details = %#v", record)
	}
}

func TestValidateExecutablePath(t *testing.T) {
	directory := t.TempDir()
	executable := writeFakeCommand(t, directory, "tool", "#!/bin/sh\nexit 0\n")
	assertResolvedExecutablePath(t, executable, executable)
	nonExecutable := filepath.Join(directory, "notes.txt")
	if err := os.WriteFile(nonExecutable, []byte("notes"), core.PrivateFileMode); err != nil {
		t.Fatal(err)
	}
	assertExecutablePathsRejected(t, directory, nonExecutable)
}

func assertResolvedExecutablePath(t *testing.T, actual, path string) {
	t.Helper()
	expected, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	actualInfo, err := os.Stat(actual)
	if err != nil {
		t.Fatalf("Stat resolved path failed: %v", err)
	}
	if !os.SameFile(actualInfo, expected) {
		t.Fatalf("resolved path %q is not the expected executable %q", actual, path)
	}
}

func assertExecutablePathsRejected(t *testing.T, directory, nonExecutable string) {
	t.Helper()
	paths := map[string]string{"empty": "", "relative": "tool", "directory": directory, "non-executable": nonExecutable}
	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			if _, err := validateExecutablePath(path); err == nil {
				t.Fatal("validateExecutablePath accepted an invalid path")
			}
		})
	}
}
