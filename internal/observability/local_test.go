package observability

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestLocalLoggerWritesPrivateLog(t *testing.T) {
	dataDir := t.TempDir()
	writeTestLocalLog(t, dataDir, "diagnostic message")

	assertFileMode(t, LogPath(dataDir), core.PrivateFileMode, "log")
	assertRecentLogsContain(t, dataDir, "diagnostic message")
}

func writeTestLocalLog(t *testing.T, dataDir, message string) {
	t.Helper()

	logger, file, err := NewLocalLogger(dataDir)
	if err != nil {
		t.Fatalf("NewLocalLogger failed: %v", err)
	}
	logger.Print(message)
	if err := file.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func assertFileMode(t *testing.T, path string, mode os.FileMode, label string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %v, want %v", label, info.Mode().Perm(), mode)
	}
}

func assertRecentLogsContain(t *testing.T, dataDir, target string) {
	t.Helper()

	lines, err := ReadRecentLogs(dataDir)
	if err != nil {
		t.Fatalf("ReadRecentLogs failed: %v", err)
	}
	hasSingleLine := len(lines) == 1
	hasTarget := hasSingleLine && strings.Contains(lines[0], target)
	if !hasTarget {
		t.Fatalf("recent logs = %#v", lines)
	}
}

func TestLocalLoggerRotatesOversizedLog(t *testing.T) {
	dataDir := t.TempDir()
	path := LogPath(dataDir)
	oversized := make([]byte, maxLocalLogBytes+1)
	writeTestFile(t, path, oversized)
	openAndCloseLocalLogger(t, dataDir)

	previousPath := filepath.Join(dataDir, previousLogFileName)
	assertPathExists(t, previousPath, "rotated log")
	assertFileMode(t, previousPath, core.PrivateFileMode, "rotated log")
}

func openAndCloseLocalLogger(t *testing.T, dataDir string) {
	t.Helper()

	_, file, err := NewLocalLogger(dataDir)
	if err != nil {
		t.Fatalf("NewLocalLogger failed: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func assertPathExists(t *testing.T, path, label string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s missing: %v", label, err)
	}
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func TestReadRecentLogsIncludesPreviousLog(t *testing.T) {
	dataDir := t.TempDir()
	previousPath := filepath.Join(dataDir, previousLogFileName)
	writeTestFile(t, previousPath, []byte("before rotation\n"))
	writeTestFile(t, LogPath(dataDir), []byte("after rotation\n"))

	lines, err := ReadRecentLogs(dataDir)
	if err != nil {
		t.Fatalf("ReadRecentLogs failed: %v", err)
	}
	joined := strings.Join(lines, "\n")
	hasPreviousLog := strings.Contains(joined, "before rotation")
	hasCurrentLog := strings.Contains(joined, "after rotation")
	hasBothLogs := hasPreviousLog && hasCurrentLog
	if !hasBothLogs {
		t.Fatalf("recent logs = %q", joined)
	}
}

func TestReadRecentLogsRejectsSymlink(t *testing.T) {
	dataDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("private"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Symlink(target, LogPath(dataDir)); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}
	if _, err := ReadRecentLogs(dataDir); err == nil {
		t.Fatal("ReadRecentLogs accepted a symlink")
	}
}

func TestRedactLinesUsesLongestMatchFirst(t *testing.T) {
	replacements := map[string]string{
		"/Users/example":          "$HOME",
		"/Users/example/diu/data": "$DATA_DIR",
	}
	lines := []string{"error in /Users/example/diu/data/diu.log"}
	redacted := RedactLines(lines, replacements)
	if redacted[0] != "error in $DATA_DIR/diu.log" {
		t.Fatalf("redacted line = %q", redacted[0])
	}
}

func TestFallbackContentionMarkerIsPrivateAndReadable(t *testing.T) {
	dataDir := t.TempDir()
	if err := MarkFallbackContention(dataDir); err != nil {
		t.Fatalf("MarkFallbackContention failed: %v", err)
	}
	assertFallbackContentionDetected(t, dataDir)
	assertFileMode(t, FallbackContentionPath(dataDir), core.PrivateFileMode, "marker")
}

func assertFallbackContentionDetected(t *testing.T, dataDir string) {
	t.Helper()

	lastContention, detected, err := ReadFallbackContention(dataDir)
	if err != nil {
		t.Fatalf("ReadFallbackContention failed: %v", err)
	}
	hasContention := detected && !lastContention.IsZero()
	if !hasContention {
		t.Fatalf("fallback contention = %v, %v", detected, lastContention)
	}
}

func TestFallbackContentionMarkerRejectsSymlink(t *testing.T) {
	dataDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Symlink(target, FallbackContentionPath(dataDir)); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}
	if err := MarkFallbackContention(dataDir); err == nil {
		t.Fatal("MarkFallbackContention accepted a symlink")
	}
}

func TestRotatingWriterRotatesWhenWriteExceedsRemainingSpace(t *testing.T) {
	dataDir := t.TempDir()
	path := LogPath(dataDir)
	writeRotatingLog(t, path)

	assertLogContains(t, path, "after rotation")
	previousPath := filepath.Join(dataDir, previousLogFileName)
	assertFileSize(t, previousPath, maxLocalLogBytes, "previous log")
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

func TestOversizedLogWriteIsBounded(t *testing.T) {
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

func assertWriteResult(t *testing.T, written, expected int, err error) {
	t.Helper()

	writeOK := err == nil && written == expected
	if !writeOK {
		t.Fatalf("Write = %d, %v", written, err)
	}
}

func TestLogTailTrimsPartialLineAndRedactsText(t *testing.T) {
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
	if got := RedactText("path=/private/data", map[string]string{"/private": "$ROOT"}); got != "path=$ROOT/data" {
		t.Fatalf("RedactText = %q", got)
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

func TestReadRecentLogsSeparatesUnterminatedRotatedLog(t *testing.T) {
	dataDir := t.TempDir()
	previousPath := filepath.Join(dataDir, previousLogFileName)
	if err := os.WriteFile(previousPath, []byte("before"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(LogPath(dataDir), []byte("after\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	lines, err := ReadRecentLogs(dataDir)
	if err != nil {
		t.Fatalf("ReadRecentLogs failed: %v", err)
	}
	assertLinesEqual(t, lines, []string{"before", "after"})
}

func assertLinesEqual(t *testing.T, lines, expected []string) {
	t.Helper()

	if !slices.Equal(lines, expected) {
		t.Fatalf("recent logs = %#v", lines)
	}
}
