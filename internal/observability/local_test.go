package observability

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestLocalLoggerWritesPrivateLog(t *testing.T) {
	dataDir := t.TempDir()
	logger, file, err := NewLocalLogger(dataDir)
	if err != nil {
		t.Fatalf("NewLocalLogger failed: %v", err)
	}
	logger.Print("diagnostic message")
	if err := file.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	path := LogPath(dataDir)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != core.PrivateFileMode {
		t.Fatalf("log mode = %v, want %v", info.Mode().Perm(), core.PrivateFileMode)
	}
	lines, err := ReadRecentLogs(dataDir)
	if err != nil {
		t.Fatalf("ReadRecentLogs failed: %v", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "diagnostic message") {
		t.Fatalf("recent logs = %#v", lines)
	}
}

func TestLocalLoggerRotatesOversizedLog(t *testing.T) {
	dataDir := t.TempDir()
	path := LogPath(dataDir)
	oversized := make([]byte, maxLocalLogBytes+1)
	if err := os.WriteFile(path, oversized, core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, file, err := NewLocalLogger(dataDir)
	if err != nil {
		t.Fatalf("NewLocalLogger failed: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	previousPath := filepath.Join(dataDir, previousLogFileName)
	if _, err := os.Stat(previousPath); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
	info, err := os.Stat(previousPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != core.PrivateFileMode {
		t.Fatalf("rotated log mode = %v, want %v", info.Mode().Perm(), core.PrivateFileMode)
	}
}

func TestReadRecentLogsIncludesPreviousLog(t *testing.T) {
	dataDir := t.TempDir()
	previousPath := filepath.Join(dataDir, previousLogFileName)
	if err := os.WriteFile(previousPath, []byte("before rotation\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(LogPath(dataDir), []byte("after rotation\n"), core.PrivateFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	lines, err := ReadRecentLogs(dataDir)
	if err != nil {
		t.Fatalf("ReadRecentLogs failed: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "before rotation") || !strings.Contains(joined, "after rotation") {
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
	lastContention, detected, err := ReadFallbackContention(dataDir)
	if err != nil {
		t.Fatalf("ReadFallbackContention failed: %v", err)
	}
	if !detected || lastContention.IsZero() {
		t.Fatalf("fallback contention = %v, %v", detected, lastContention)
	}
	info, err := os.Stat(FallbackContentionPath(dataDir))
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != core.PrivateFileMode {
		t.Fatalf("marker mode = %v, want %v", info.Mode().Perm(), core.PrivateFileMode)
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
	assertLogContains(t, path, "after rotation")
	previousPath := filepath.Join(dataDir, previousLogFileName)
	info, err := os.Stat(previousPath)
	if err != nil || info.Size() != maxLocalLogBytes {
		t.Fatalf("previous log = %v, %v", info, err)
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
	if err != nil || written != len(data) {
		t.Fatalf("Write = %d, %v", written, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != maxLocalLogBytes {
		t.Fatalf("bounded log = %v, %v", info, err)
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
	if err != nil || string(tail) != "kept line\n" {
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
	if len(lines) != 2 || lines[0] != "before" || lines[1] != "after" {
		t.Fatalf("recent logs = %#v", lines)
	}
}
