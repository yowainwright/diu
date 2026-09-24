//go:build e2e

package e2e

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func TestCLIRecorderDropsEventsWhenAllSlotsAreOccupied(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	locks := holdAllRecorderTestSlots(t, f.config.Daemon.DataDir)
	defer closeRecorderTestSlots(locks)
	assertBusyRecorderPreservesCommands(t, f, locks)
}

func holdAllRecorderTestSlots(t *testing.T, dataDir string) []*os.File {
	t.Helper()
	locks := make([]*os.File, 0, core.MaxRecorderWorkers)
	for slot := range core.MaxRecorderWorkers {
		locks = append(locks, holdRecorderTestSlot(t, core.RecorderSlotPath(dataDir, slot)))
	}
	return locks
}

func holdRecorderTestSlot(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return file
}

func closeRecorderTestSlots(locks []*os.File) {
	for _, lock := range locks {
		if lock != nil {
			_ = lock.Close()
		}
	}
}

func assertBusyRecorderPreservesCommands(t *testing.T, f *cliFixture, locks []*os.File) {
	t.Helper()
	original := runCLICommand(t, f.command(t, filepath.Join(f.managed, "probe"), cliProbeArgs()...), "record input")
	for range 8 {
		result := runCLICommand(t, f.command(t, filepath.Join(f.wrappers, "probe"), cliProbeArgs()...), "record input")
		if result != original {
			t.Fatalf("busy recorder changed command behavior: %#v != %#v", result, original)
		}
	}
	assertCLINoRecordedEvents(t, f)
	_ = locks[0].Close()
	locks[0] = nil
	assertCLICommandContract(t, f, "bash", "probe")
	waitCLIRecords(t, f, 1)
}

func assertCLINoRecordedEvents(t *testing.T, f *cliFixture) {
	t.Helper()
	result := f.cli(t, "query", "--format", "json")
	assertCLIExit(t, result, 0)
	if result.stdout != "[]\n" {
		t.Fatalf("events were not dropped at capacity: %s", result.stdout)
	}
}

func TestCLIRecorderCapsConcurrentWorkersAndReleasesSlots(t *testing.T) {
	f := newCLIFixture(t)
	f.setup(t)
	listener := listenForStalledRecorderEvents(t, f.config.Daemon.SocketPath)
	defer func() { _ = listener.Close() }()
	connections := startRecorderWriters(t, f, listener)
	defer closeRecorderConnections(connections)
	assertRecorderPoolIsFull(t, f, listener)
	assertCommandsRunWhilePoolIsFull(t, f)
	closeRecorderConnections(connections)
	awaitCLI(t, func() bool { return busyRecorderSlotCount(f.config.Daemon.DataDir) == 0 })
}

func listenForStalledRecorderEvents(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	address := &net.UnixAddr{Name: path, Net: "unix"}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

func startRecorderWriters(t *testing.T, f *cliFixture, listener *net.UnixListener) []*net.UnixConn {
	t.Helper()
	connections := make([]*net.UnixConn, 0, core.MaxRecorderWorkers)
	for range core.MaxRecorderWorkers {
		result := runCLICommand(t, f.command(t, cliBinary, "record", "--background"), largeRecorderEvent())
		assertCLIExit(t, result, 0)
		connection, err := acceptRecorderWriter(listener)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
	}
	return connections
}

func acceptRecorderWriter(listener *net.UnixListener) (*net.UnixConn, error) {
	_ = listener.SetDeadline(time.Now().Add(2 * time.Second))
	return listener.AcceptUnix()
}

func busyRecorderSlotCount(dataDir string) int {
	busy := 0
	for slot := range core.MaxRecorderWorkers {
		path := core.RecorderSlotPath(dataDir, slot)
		file, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			continue
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != nil {
			busy++
		} else {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		}
		_ = file.Close()
	}
	return busy
}

func largeRecorderEvent() string {
	padding := strings.Repeat("x", 900_000)
	prefix := `{"tool":"brew","command":"brew list","args":["list"],"padding":"`
	suffix := `"}`
	event := prefix + padding + suffix
	return event
}

func assertRecorderPoolIsFull(t *testing.T, f *cliFixture, listener *net.UnixListener) {
	t.Helper()
	if count := busyRecorderSlotCount(f.config.Daemon.DataDir); count != core.MaxRecorderWorkers {
		t.Fatalf("busy recorder slots = %d, want %d", count, core.MaxRecorderWorkers)
	}
	result := runCLICommand(t, f.command(t, cliBinary, "record", "--background"), largeRecorderEvent())
	assertCLIExit(t, result, 0)
	_ = listener.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if connection, err := listener.AcceptUnix(); err == nil {
		_ = connection.Close()
		t.Fatal("recorder accepted a fifth background job")
	}
}

func assertCommandsRunWhilePoolIsFull(t *testing.T, f *cliFixture) {
	t.Helper()
	original := runCLICommand(t, f.command(t, filepath.Join(f.managed, "probe"), cliProbeArgs()...), "record input")
	for range 8 {
		result := runCLICommand(t, f.command(t, filepath.Join(f.wrappers, "probe"), cliProbeArgs()...), "record input")
		if result != original {
			t.Fatalf("full recorder pool changed command behavior: %#v != %#v", result, original)
		}
	}
}

func closeRecorderConnections(connections []*net.UnixConn) {
	for _, connection := range connections {
		_ = connection.Close()
	}
}
