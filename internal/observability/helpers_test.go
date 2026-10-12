package observability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestRotatingLogWriterWriteRotatesWhenWriteExceedsRemainingSpace(t *testing.T) {
	dataDir := t.TempDir()
	path := LogPath(dataDir)
	writeRotatingLog(t, path)

	assertLogContains(t, path, "after rotation")
	previousPath := filepath.Join(dataDir, previousLogFileName)
	assertFileSize(t, previousPath, maxLocalLogBytes, "previous log")
}

func TestRotatingLogWriterWriteBoundsOversizedEntry(t *testing.T) {
	path := LogPath(t.TempDir())
	writer, err := newRotatingLogWriter(path)
	if err != nil {
		t.Fatalf("newRotatingLogWriter failed: %v", err)
	}
	data := make([]byte, maxLocalLogBytes+10)
	written, err := writer.Write(data)
	assertWriteResult(t, written, len(data), err)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	assertFileSize(t, path, maxLocalLogBytes, "bounded log")
}

func TestReadLogTailTrimsPartialLine(t *testing.T) {
	path := LogPath(t.TempDir())
	data := []byte("discarded partial line\nkept line\n")
	if err := os.WriteFile(path, data, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = file.Close() }()
	tail, err := readLogTail(file, int64(len("partial line\nkept line\n")))
	tailOK := err == nil && string(tail) == "kept line\n"
	if !tailOK {
		t.Fatalf("tail = %q, %v", tail, err)
	}
}

func writeRotatingLog(t *testing.T, path string) {
	t.Helper()

	writer, err := newRotatingLogWriter(path)
	if err != nil {
		t.Fatalf("newRotatingLogWriter failed: %v", err)
	}
	if _, err := writer.Write(make([]byte, maxLocalLogBytes)); err != nil {
		t.Fatalf("initial Write failed: %v", err)
	}
	if _, err := writer.Write([]byte("after rotation\n")); err != nil {
		t.Fatalf("rotating Write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func assertFileSize(t *testing.T, path string, size int64, label string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s stat failed: %v", label, err)
	}
	if info.Size() != size {
		t.Fatalf("%s size = %d, want %d", label, info.Size(), size)
	}
}

func assertWriteResult(t *testing.T, written, expected int, err error) {
	t.Helper()

	writeOK := err == nil && written == expected
	if !writeOK {
		t.Fatalf("Write = %d, %v", written, err)
	}
}

func assertLogContains(t *testing.T, path, target string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(data), target) {
		t.Fatalf("log = %q, want %q", data, target)
	}
}
