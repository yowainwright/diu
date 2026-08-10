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
		if closeErr := file.Close(); err == nil && closeErr != nil {
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
