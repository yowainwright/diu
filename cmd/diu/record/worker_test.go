package record

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

func TestWriteRecorderPayloadWritesAllBytes(t *testing.T) {
	var output bytes.Buffer
	if err := writeRecorderPayload(&output, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if output.String() != "payload" {
		t.Fatalf("payload = %q", output.String())
	}
}

func TestWriteRecorderPayloadReportsWriteFailures(t *testing.T) {
	writeErr := errors.New("write failed")
	for _, test := range []struct {
		writer recorderPayloadWriter
		want   error
	}{
		{recorderPayloadWriter{err: writeErr}, writeErr},
		{recorderPayloadWriter{size: 1, err: writeErr}, writeErr},
		{recorderPayloadWriter{}, io.ErrShortWrite},
		{recorderPayloadWriter{size: 1}, io.ErrShortWrite},
	} {
		if err := writeRecorderPayload(test.writer, []byte("payload")); !errors.Is(err, test.want) {
			t.Fatalf("write error = %v, want %v", err, test.want)
		}
	}
}

type recorderPayloadWriter struct {
	size int
	err  error
}

func (w recorderPayloadWriter) Write(payload []byte) (int, error) {
	return min(w.size, len(payload)), w.err
}

func TestReadRecorderPayloadRejectsOversizeInput(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), core.MaxRecorderPayloadBytes+1)
	if _, err := readRecorderPayload(bytes.NewReader(payload)); err == nil {
		t.Fatal("oversized worker payload was accepted")
	}
}

func TestReadRecorderPayloadReturnsReaderError(t *testing.T) {
	want := errors.New("read failed")
	if _, err := readRecorderPayload(recorderErrorReader{err: want}); !errors.Is(err, want) {
		t.Fatalf("read error = %v, want %v", err, want)
	}
}

func TestSendRecorderPayloadWritesUnixSocket(t *testing.T) {
	socketDir, err := os.MkdirTemp(".", "sock-")
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

func TestRecorderDeadlineRequiresPrivateProcessGroup(t *testing.T) {
	stop, err := recorderDeadline()
	if err == nil {
		stop()
		return
	}
	if err.Error() != "recorder requires a private process group" {
		t.Fatalf("deadline error = %v", err)
	}
}

func TestRecorderDeadlineHelper(t *testing.T) {
	if os.Getenv("DIU_TEST_RECORDER_DEADLINE") != "1" {
		return
	}
	stop, err := recorderDeadline()
	if err != nil {
		t.Fatal(err)
	}
	stop()
}

func TestRecorderDeadlineAcceptsPrivateProcessGroup(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRecorderDeadlineHelper$")
	cmd.Env = append(os.Environ(), "DIU_TEST_RECORDER_DEADLINE=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("private group deadline failed: %v\n%s", err, output)
	}
}
