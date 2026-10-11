//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestCLIZshBoundsRecorderHandoffsAndReusesCapacity(t *testing.T) {
	f := newCLIFixture(t)
	writeCLIFile(t, filepath.Join(f.managed, "listener-tool"), "#!/bin/sh\nexit 0\n", 0o700)
	f.setup(t)
	starts := stallCLIZshRecorders(t, f)
	_, input, output, _ := startCLIZsh(t, f)
	burst := "false" + strings.Repeat(" && listener-tool", 24)
	for range 2 {
		writeZshInput(t, input, burst+"\nprint -r -- DIU_'BURST_DONE'\n")
		waitCLIZshOutput(t, output, "DIU_BURST_DONE")
		awaitCLI(t, func() bool { return len(readCLIZshRecorderStarts(t, starts)) >= core.MaxRecorderWorkers })
		assertCLIZshHandoffBound(t, starts)
	}
	releaseCLIZshRecorder(t, starts)
	writeZshInput(t, input, "false && listener-tool\nprint -r -- DIU_'CAPACITY_REUSED'\n")
	waitCLIZshOutput(t, output, "DIU_CAPACITY_REUSED")
	awaitCLI(t, func() bool { return len(readCLIZshRecorderStarts(t, starts)) == core.MaxRecorderWorkers+1 })
}

const stalledCLIZshRecorder = `#!/bin/bash
printf '%s %s\n' "$$" "$PPID" >> "$HOME/recorder-starts"
read -r released < "$HOME/recorder-gate"
`

func stallCLIZshRecorders(t *testing.T, f *cliFixture) string {
	t.Helper()
	starts := filepath.Join(f.home, "recorder-starts")
	writeCLIFile(t, starts, "", 0o600)
	gatePath := filepath.Join(f.home, "recorder-gate")
	if err := syscall.Mkfifo(gatePath, 0o600); err != nil {
		t.Fatal(err)
	}
	gate, err := os.OpenFile(gatePath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gate.Close() })
	installStalledCLIZshRecorder(t, f, starts)
	return starts
}

func installStalledCLIZshRecorder(t *testing.T, f *cliFixture, starts string) {
	t.Helper()
	path := filepath.Join(f.bin, "diu")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, path, stalledCLIZshRecorder, 0o700)
	t.Cleanup(func() {
		for _, launch := range readCLIZshRecorderStarts(t, starts) {
			_ = syscall.Kill(launch.pid, syscall.SIGKILL)
		}
	})
}

type cliZshRecorderStart struct {
	pid    int
	parent int
}

func readCLIZshRecorderStarts(t *testing.T, path string) []cliZshRecorderStart {
	t.Helper()
	lines := strings.Fields(readCLIFile(t, path))
	if len(lines)%2 != 0 {
		t.Fatalf("incomplete recorder start: %v", lines)
	}
	starts := make([]cliZshRecorderStart, 0, len(lines)/2)
	for index := 0; index < len(lines); index += 2 {
		pid := parseCLIZshProcessID(t, lines[index])
		parent := parseCLIZshProcessID(t, lines[index+1])
		starts = append(starts, cliZshRecorderStart{pid: pid, parent: parent})
	}
	return starts
}

func parseCLIZshProcessID(t *testing.T, value string) int {
	t.Helper()
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 0 {
		t.Fatalf("invalid process ID %q: %v", value, err)
	}
	return pid
}

func assertCLIZshHandoffBound(t *testing.T, path string) {
	t.Helper()
	starts := readCLIZshRecorderStarts(t, path)
	parent := strconv.Itoa(starts[0].parent)
	children := readCLIFile(t, "/proc/"+parent+"/task/"+parent+"/children")
	// Each pipeline can have one printf child and one recorder child, including starts still being scheduled.
	if count := len(strings.Fields(children)); count > 2*core.MaxRecorderWorkers {
		t.Fatalf("shell forked %d children for %d handoff slots", count, core.MaxRecorderWorkers)
	}
	if len(starts) != core.MaxRecorderWorkers {
		t.Fatalf("shell launched %d recorders, want %d", len(starts), core.MaxRecorderWorkers)
	}
}

func releaseCLIZshRecorder(t *testing.T, path string) {
	t.Helper()
	pid := readCLIZshRecorderStarts(t, path)[0].pid
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	awaitCLI(t, func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH })
}
