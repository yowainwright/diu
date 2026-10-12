package storage

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/yowainwright/diu/internal/core"
)

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
