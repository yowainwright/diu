package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

func (j *JSONStorage) executionBackupPath(manifestBackup string) string {
	suffix := strings.TrimPrefix(manifestBackup, j.filepath)
	return j.executionPath + suffix
}

func copyManagedFile(source, destination string) (err error) {
	input, err := safefs.OpenFile(source, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() {
		err = closeWithError(err, input)
	}()
	output, err := safefs.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, core.PrivateFileMode)
	if err != nil {
		return err
	}
	defer func() {
		err = closeWithError(err, output)
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return nil
}

func closeWithError(current error, file *os.File) error {
	closeErr := file.Close()
	shouldReturnCloseErr := current == nil && closeErr != nil
	if shouldReturnCloseErr {
		return closeErr
	}
	return current
}

func (j *JSONStorage) restoreBackup(path string) error {
	data, err := readManagedFile(path)
	if err != nil {
		return fmt.Errorf("failed to read restore file: %w", err)
	}
	var restored core.StorageData
	if err := json.Unmarshal(data, &restored); err != nil {
		return fmt.Errorf("failed to unmarshal restore data: %w", err)
	}
	if restored.ExecutionLogFormat == "" {
		return j.restoreLegacyBackup(path)
	}
	if restored.ExecutionLogFormat != executionLogFormat {
		return fmt.Errorf("%w: %s", ErrUnsupportedExecutionLogFormat, restored.ExecutionLogFormat)
	}
	executionBackup := j.executionBackupPath(path)
	if err := replaceExecutionLog(executionBackup, j.executionPath); err != nil {
		return fmt.Errorf("failed to restore execution log: %w", err)
	}
	j.data = &restored
	return j.save()
}

func (j *JSONStorage) restoreLegacyBackup(path string) error {
	state, records, err := j.collectLegacyCompaction(path)
	if err != nil {
		return err
	}
	if err := writeCompactedStorage(j.executionPath, records); err != nil {
		return err
	}
	statistics := compactedStatistics(records)
	j.data = state.manifest(statistics)
	return j.save()
}

func replaceExecutionLog(source, destination string) error {
	if err := validateExecutionLog(source); err != nil {
		return err
	}
	file, tempPath, err := createCompactionFile(destination)
	if err != nil {
		return err
	}
	if err := copyExecutionLog(file, source); err != nil {
		discardTempFile(file, tempPath)
		return err
	}
	return commitTempFile(file, tempPath, destination)
}

func validateExecutionLog(path string) error {
	return scanNDJSONExecutions(path, nil)
}

func copyExecutionLog(destination *os.File, source string) (err error) {
	input, err := safefs.OpenFile(source, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() {
		err = closeWithError(err, input)
	}()
	if _, err := io.Copy(destination, input); err != nil {
		return fmt.Errorf("failed to copy execution log: %w", err)
	}
	return nil
}
