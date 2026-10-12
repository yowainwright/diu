package observability

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yowainwright/diu/internal/core"
)

func TestMarkFallbackContentionIsPrivateAndReadable(t *testing.T) {
	dataDir := t.TempDir()
	if err := MarkFallbackContention(dataDir); err != nil {
		t.Fatalf("MarkFallbackContention failed: %v", err)
	}
	assertFallbackHasContention(t, dataDir)
	assertFileMode(t, FallbackContentionPath(dataDir), core.PrivateFileMode, "marker")
}

func TestMarkFallbackContentionRejectsSymlink(t *testing.T) {
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

func TestNewLocalLoggerWritesPrivateLog(t *testing.T) {
	dataDir := t.TempDir()
	writeTestLocalLog(t, dataDir, "diagnostic message")

	assertFileMode(t, LogPath(dataDir), core.PrivateFileMode, "log")
	assertRecentLogsContain(t, dataDir, "diagnostic message")
}

func TestNewLocalLoggerRotatesOversizedLog(t *testing.T) {
	dataDir := t.TempDir()
	path := LogPath(dataDir)
	oversized := make([]byte, maxLocalLogBytes+1)
	writeTestFile(t, path, oversized)
	openAndCloseLocalLogger(t, dataDir)

	previousPath := filepath.Join(dataDir, previousLogFileName)
	assertPathExists(t, previousPath, "rotated log")
	assertFileMode(t, previousPath, core.PrivateFileMode, "rotated log")
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

func TestRedactTextReplacesMatch(t *testing.T) {
	if got := RedactText("path=/private/data", map[string]string{"/private": "$ROOT"}); got != "path=$ROOT/data" {
		t.Fatalf("RedactText = %q", got)
	}
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

func assertFallbackHasContention(t *testing.T, dataDir string) {
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

func assertLinesEqual(t *testing.T, lines, expected []string) {
	t.Helper()

	if !slices.Equal(lines, expected) {
		t.Fatalf("recent logs = %#v", lines)
	}
}
