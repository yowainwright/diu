package observability

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/helpers"
	"github.com/yowainwright/diu/internal/safefs"
)

func LogPath(dataDir string) string {
	return filepath.Join(dataDir, LogFileName)
}

func FallbackContentionPath(dataDir string) string {
	return filepath.Join(dataDir, fallbackMarkerName)
}

func MarkFallbackContention(dataDir string) error {
	if err := os.MkdirAll(dataDir, core.OwnerDirectoryMode); err != nil {
		return fmt.Errorf("failed to create observability directory: %w", err)
	}
	path := FallbackContentionPath(dataDir)
	if err := ensurePrivateMarker(path); err != nil {
		return err
	}
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		return fmt.Errorf("failed to update fallback contention marker: %w", err)
	}
	return nil
}

func ReadFallbackContention(dataDir string) (time.Time, bool, error) {
	path := FallbackContentionPath(dataDir)
	info, err := safefs.Lstat(path)
	if os.IsNotExist(err) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("failed to inspect fallback contention marker: %w", err)
	}
	if err := validateMarkerFileMode(path, info); err != nil {
		return time.Time{}, false, err
	}
	return info.ModTime().UTC(), true, nil
}

func NewLocalLogger(dataDir string) (*log.Logger, io.Closer, error) {
	if err := os.MkdirAll(dataDir, core.OwnerDirectoryMode); err != nil {
		return nil, nil, fmt.Errorf("failed to create log directory: %w", err)
	}
	path := LogPath(dataDir)
	if err := rotateLog(path); err != nil {
		return nil, nil, err
	}
	writer, err := newRotatingLogWriter(path)
	if err != nil {
		return nil, nil, err
	}
	output := io.MultiWriter(writer, os.Stderr)
	logger := log.New(output, "", log.LstdFlags|log.LUTC)
	return logger, writer, nil
}

func ReadRecentLogs(dataDir string) ([]string, error) {
	current, err := readLogTailPath(LogPath(dataDir), maxDiagnosticLogSize)
	if err != nil {
		return nil, err
	}
	remaining := maxDiagnosticLogSize - int64(len(current))
	previousPath := filepath.Join(dataDir, previousLogFileName)
	previous, err := readLogTailPath(previousPath, remaining)
	if err != nil {
		return nil, err
	}
	data := joinLogTails(previous, current)
	return splitLogLines(data), nil
}

func RedactLines(lines []string, replacements map[string]string) []string {
	keys := replacementKeys(replacements)
	return helpers.Map(lines, func(line string) string {
		return redact(line, keys, replacements)
	})
}

func RedactText(value string, replacements map[string]string) string {
	return redact(value, replacementKeys(replacements), replacements)
}
