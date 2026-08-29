package observability

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

const (
	LogFileName          = "diu.log"
	previousLogFileName  = "diu.log.previous"
	fallbackMarkerName   = "fallback-contention"
	maxLocalLogBytes     = 4 << 20
	maxDiagnosticLogSize = 128 << 10
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

func ensurePrivateMarker(path string) error {
	if err := validatePrivateMarker(path); err != nil {
		return err
	}
	file, err := safefs.OpenFile(path, os.O_CREATE|os.O_WRONLY, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to open fallback contention marker: %w", err)
	}
	chmodErr := file.Chmod(core.PrivateFileMode)
	closeErr := file.Close()
	return errors.Join(chmodErr, closeErr)
}

func validatePrivateMarker(path string) error {
	info, err := safefs.Lstat(path)
	if err == nil {
		return validateMarkerFileMode(path, info)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect fallback contention marker: %w", err)
	}
	return nil
}

func validateMarkerFileMode(path string, info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("fallback contention marker is not a regular file: %s", path)
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
	if !info.Mode().IsRegular() {
		return time.Time{}, false, fmt.Errorf("fallback contention marker is not a regular file: %s", path)
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

type rotatingLogWriter struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func newRotatingLogWriter(path string) (*rotatingLogWriter, error) {
	file, size, err := openLog(path)
	if err != nil {
		return nil, err
	}
	return &rotatingLogWriter{path: path, file: file, size: size}, nil
}

func openLog(path string) (*os.File, int64, error) {
	flags := os.O_CREATE | os.O_APPEND | os.O_WRONLY
	file, err := safefs.OpenFile(path, flags, core.PrivateFileMode)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open local log: %w", err)
	}
	if err := file.Chmod(core.PrivateFileMode); err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("failed to secure local log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("failed to stat local log: %w", err)
	}
	return file, info.Size(), nil
}

func (w *rotatingLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalSize := len(data)
	boundedData := boundedLogWrite(data)
	if w.size+int64(len(boundedData)) > maxLocalLogBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	written, err := w.file.Write(boundedData)
	w.size += int64(written)
	if err != nil {
		return written, err
	}
	return originalSize, nil
}

func boundedLogWrite(data []byte) []byte {
	if len(data) <= maxLocalLogBytes {
		return data
	}
	return data[:maxLocalLogBytes]
}

func (w *rotatingLogWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	if err := rotateCurrentLog(w.path); err != nil {
		return err
	}
	file, size, err := openLog(w.path)
	if err != nil {
		return err
	}
	w.file = file
	w.size = size
	return nil
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

func rotateLog(path string) error {
	info, err := safefs.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect local log: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("local log is not a regular file: %s", path)
	}
	if info.Size() <= maxLocalLogBytes {
		return nil
	}
	return rotateCurrentLog(path)
}

func rotateCurrentLog(path string) error {
	previousPath := filepath.Join(filepath.Dir(path), previousLogFileName)
	removeErr := os.Remove(previousPath)
	shouldReturnRemoveErr := removeErr != nil && !os.IsNotExist(removeErr)
	if shouldReturnRemoveErr {
		return fmt.Errorf("failed to remove previous local log: %w", removeErr)
	}
	if err := os.Rename(path, previousPath); err != nil {
		return fmt.Errorf("failed to rotate local log: %w", err)
	}
	if err := os.Chmod(previousPath, core.PrivateFileMode); err != nil {
		return fmt.Errorf("failed to secure previous local log: %w", err)
	}
	return nil
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

func readLogTailPath(path string, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, nil
	}
	info, err := safefs.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to inspect local log: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("local log is not a regular file: %s", path)
	}
	file, err := safefs.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open local log: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	return readLogTail(file, limit)
}

func readLogTail(file *os.File, limit int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat local log: %w", err)
	}
	offset := max(info.Size()-limit, 0)
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek local log: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, fmt.Errorf("failed to read local log: %w", err)
	}
	if offset > 0 {
		data = trimPartialLine(data)
	}
	return data, nil
}

func joinLogTails(previous, current []byte) []byte {
	if len(previous) == 0 {
		return current
	}
	currentEmpty := len(current) == 0
	previousComplete := previous[len(previous)-1] == '\n'
	canAppend := currentEmpty || previousComplete
	if canAppend {
		return append(previous, current...)
	}
	withNewline := append(previous, '\n')
	return append(withNewline, current...)
}

func splitLogLines(data []byte) []string {
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func trimPartialLine(data []byte) []byte {
	lineEnd := bytes.IndexByte(data, '\n')
	if lineEnd < 0 {
		return nil
	}
	return data[lineEnd+1:]
}

func RedactLines(lines []string, replacements map[string]string) []string {
	keys := replacementKeys(replacements)
	redacted := make([]string, len(lines))
	for index, line := range lines {
		redacted[index] = redact(line, keys, replacements)
	}
	return redacted
}

func RedactText(value string, replacements map[string]string) string {
	return redact(value, replacementKeys(replacements), replacements)
}

func replacementKeys(replacements map[string]string) []string {
	keys := make([]string, 0, len(replacements))
	for key := range replacements {
		if key != "" {
			keys = append(keys, key)
		}
	}
	slices.SortStableFunc(keys, func(a, b string) int {
		return len(b) - len(a)
	})
	return keys
}

func redact(value string, keys []string, replacements map[string]string) string {
	for _, key := range keys {
		value = strings.ReplaceAll(value, key, replacements[key])
	}
	return value
}
