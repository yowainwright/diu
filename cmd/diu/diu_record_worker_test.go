package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/storage"
)

func TestRecorderPayloadFileCopiesAndUnlinksInput(t *testing.T) {
	file, err := recorderPayloadFile(t.TempDir(), strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	assertReaderContents(t, file, "payload")
	assertPathMissing(t, file.Name())
}

func TestRecorderPayloadHelpersRejectOversizeInput(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), core.MaxRecorderPayloadBytes+1)
	if err := copyRecorderPayload(newRecorderTempFile(t), bytes.NewReader(payload)); err == nil {
		t.Fatal("oversized payload copy was accepted")
	}
	if _, err := readRecorderPayload(bytes.NewReader(payload)); err == nil {
		t.Fatal("oversized worker payload was accepted")
	}
}

func TestCopyRecorderPayloadReturnsReaderError(t *testing.T) {
	wantErr := errors.New("reader failed")
	file := newRecorderTempFile(t)
	if err := copyRecorderPayload(file, recorderErrorReader{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("payload copy error = %v, want %v", err, wantErr)
	}
}

func newRecorderTempFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

type recorderErrorReader struct{ err error }

func (r recorderErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestConfigureRecorderProcessPassesPrivateInputs(t *testing.T) {
	lock, input := createRecorderInputFiles(t)
	cmd := exec.Command("unused")
	configureRecorderProcess(cmd, lock, input)
	assertRecorderProcessConfiguration(t, cmd, lock, input)
}

func createRecorderInputFiles(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	lock, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(t.TempDir(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	t.Cleanup(func() { _ = input.Close() })
	return lock, input
}

func assertRecorderProcessConfiguration(t *testing.T, cmd *exec.Cmd, lock, input *os.File) {
	t.Helper()
	processIsDetached := cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid
	lockIsInherited := len(cmd.ExtraFiles) == 1 && cmd.ExtraFiles[0] == lock
	if cmd.Stdin != input {
		t.Fatal("recorder process did not receive its payload input")
	}
	if !processIsDetached {
		t.Fatal("recorder process is not detached")
	}
	if !lockIsInherited {
		t.Fatal("recorder process did not inherit its lock")
	}
	if !containsEnvironmentValue(cmd.Env, "DIU_RECORDING=1") {
		t.Fatal("recorder process did not set DIU_RECORDING")
	}
}

func containsEnvironmentValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestStartBackgroundRecorderRejectsInvalidDataDirectory(t *testing.T) {
	config := setupTestHomeConfig(t)
	config.Daemon.DataDir = filepath.Join(t.TempDir(), "file", "child")
	if err := os.WriteFile(filepath.Dir(config.Daemon.DataDir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := startBackgroundRecorder(config, strings.NewReader("payload")); err == nil {
		t.Fatal("recorder accepted an invalid data directory")
	}
}

func TestSendRecorderPayloadWritesUnixSocket(t *testing.T) {
	socketDir, err := os.MkdirTemp("/tmp", "diu-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "r")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	done := make(chan struct{})
	go func() {
		receiveRecorderPayload(t, listener)
		close(done)
	}()
	if err := sendRecorderPayload(socket, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	<-done
}

func receiveRecorderPayload(t *testing.T, listener net.Listener) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		t.Error(err)
		return
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Error(err)
		return
	}
	assertReaderContents(t, conn, "payload")
}

func assertReaderContents(t *testing.T, reader io.Reader, want string) {
	t.Helper()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	contentsMatch := string(got) == want
	if !contentsMatch {
		t.Fatalf("reader = %q, %v; want %q", got, err, want)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary payload path still exists: %v", err)
	}
}

func TestRecorderWorkerProcessHelper(t *testing.T) {
	if os.Getenv("DIU_TEST_RECORDER_WORKER") != "1" {
		return
	}
	t.Setenv("HOME", os.Getenv("DIU_TEST_RECORDER_WORKER_HOME"))
	config, err := core.LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if err := runRecorderWorker(config, nil, 0); err != nil {
		t.Fatal(err)
	}
}

func TestRecorderWorkerFallsBackWithoutDaemon(t *testing.T) {
	config := setupTestHomeConfig(t)
	output := runRecorderWorkerProcess(t, `{"tool":"test-tool","command":"test-tool run"}`)
	assertWorkerProcessSucceeded(t, output)
	assertFallbackWorkerRecord(t, config)
}

func runRecorderWorkerProcess(t *testing.T, payload string) []byte {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRecorderWorkerProcessHelper$")
	cmd.Env = append(os.Environ(), "DIU_TEST_RECORDER_WORKER=1", "DIU_TEST_RECORDER_WORKER_HOME="+os.Getenv("HOME"))
	cmd.Stdin = strings.NewReader(payload)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("recorder worker failed: %v\n%s", err, output)
	}
	return output
}

func assertWorkerProcessSucceeded(t *testing.T, output []byte) {
	t.Helper()
	if !bytes.Contains(output, []byte("PASS")) {
		t.Fatalf("worker helper output = %q", output)
	}
}

func assertFallbackWorkerRecord(t *testing.T, config *core.Config) {
	t.Helper()
	store := openTestStore(t, config)
	defer closeTestStore(t, store)
	records, err := store.GetExecutions(storage.QueryOptions{Tool: "test-tool"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("fallback stored %d records, want 1", len(records))
	}
	commandMatches := records[0].Command == "test-tool run"
	if !commandMatches {
		t.Fatalf("fallback records = %+v, %v", records, err)
	}
}
