package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

func appendExecutionRecords(path string, records []core.ExecutionRecord) (err error) {
	data, err := encodeExecutionRecords(records)
	if err != nil {
		return err
	}
	file, err := safefs.OpenFile(path, os.O_APPEND|os.O_WRONLY, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to open execution log: %w", err)
	}
	defer func() {
		closeErr := file.Close()
		shouldReturnCloseErr := err == nil && closeErr != nil
		if shouldReturnCloseErr {
			err = fmt.Errorf("failed to close execution log: %w", closeErr)
		}
	}()
	written, err := file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to append executions: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("failed to append executions: %w", io.ErrShortWrite)
	}
	return nil
}

func (j *JSONStorage) prepareAppendedExecutionLog(records []core.ExecutionRecord) (string, error) {
	data, err := encodeExecutionRecords(records)
	if err != nil {
		return "", err
	}
	file, tempPath, err := createStorageTempFile(j.executionPath, "execution")
	if err != nil {
		return "", err
	}
	if err := copyManagedFileToOpenFile(j.executionPath, file); err != nil {
		discardTempFile(file, tempPath)
		return "", err
	}
	return prepareAppendedExecutionFile(file, tempPath, data)
}

func prepareAppendedExecutionFile(file *os.File, tempPath string, data []byte) (string, error) {
	if err := writePreparedFile(file, tempPath, data); err != nil {
		return "", err
	}
	return tempPath, nil
}

func prepareExecutionLogCopy(source string) (string, error) {
	if err := validateExecutionLog(source); err != nil {
		return "", err
	}
	return prepareFileCopy(source, "execution")
}

func encodeExecutionRecords(records []core.ExecutionRecord) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return nil, fmt.Errorf("failed to encode execution: %w", err)
		}
	}
	return buffer.Bytes(), nil
}
