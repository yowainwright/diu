package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

const storageCommitJournalSuffix = ".commit"

type storageCommitJournal struct {
	ManifestTemp    string `json:"manifest_temp"`
	ExecutionTemp   string `json:"execution_temp"`
	ManifestBackup  string `json:"manifest_backup"`
	ExecutionBackup string `json:"execution_backup"`
}

func (j *JSONStorage) commitPreparedStorage(executionTemp string) error {
	commit, err := j.prepareStorageCommit(executionTemp)
	if err != nil {
		_ = os.Remove(executionTemp)
		return err
	}
	if err := j.writeStorageCommitJournal(commit); err != nil {
		removeStorageCommitFiles(commit)
		return err
	}
	if err := j.applyStorageCommit(commit); err != nil {
		return j.rollbackStorageCommit(commit, err)
	}
	return j.removeStorageCommitJournal(commit)
}

func (j *JSONStorage) prepareStorageCommit(executionTemp string) (storageCommitJournal, error) {
	manifestTemp, err := j.prepareManifestTemp()
	if err != nil {
		return storageCommitJournal{}, err
	}
	manifestBackup, err := prepareFileCopy(j.filepath, "manifest-backup")
	if err != nil {
		_ = os.Remove(manifestTemp)
		return storageCommitJournal{}, err
	}
	executionBackup, err := prepareFileCopy(j.executionPath, "execution-backup")
	if err != nil {
		_ = os.Remove(manifestTemp)
		_ = os.Remove(manifestBackup)
		return storageCommitJournal{}, err
	}
	return storageCommitJournal{manifestTemp, executionTemp, manifestBackup, executionBackup}, nil
}

func (j *JSONStorage) prepareManifestTemp() (string, error) {
	j.data.Metadata.LastUpdated = time.Now()
	j.data.ExecutionLogFormat = executionLogFormat
	j.data.Executions = []core.ExecutionRecord{}
	data, err := j.marshalStorage(j.data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal storage data: %w", err)
	}
	return prepareTempFile(j.filepath, "manifest", data)
}

func prepareTempFile(path, label string, data []byte) (string, error) {
	file, tempPath, err := createStorageTempFile(path, label)
	if err != nil {
		return "", err
	}
	if err := writePreparedFile(file, tempPath, data); err != nil {
		return "", err
	}
	return tempPath, nil
}

func createStorageTempFile(path, label string) (*os.File, string, error) {
	pattern := "." + filepath.Base(path) + "." + label + "-*"
	file, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create storage temp file: %w", err)
	}
	if err := file.Chmod(core.PrivateFileMode); err != nil {
		discardTempFile(file, file.Name())
		return nil, "", fmt.Errorf("failed to secure storage temp file: %w", err)
	}
	return file, file.Name(), nil
}

func writePreparedFile(file *os.File, tempPath string, data []byte) error {
	if err := writeAll(file, data); err != nil {
		discardTempFile(file, tempPath)
		return fmt.Errorf("failed to write storage temp file: %w", err)
	}
	return closePreparedFile(file, tempPath)
}

func closePreparedFile(file *os.File, tempPath string) error {
	if err := file.Sync(); err != nil {
		discardTempFile(file, tempPath)
		return fmt.Errorf("failed to sync storage temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to close storage temp file: %w", err)
	}
	return nil
}

func prepareFileCopy(source, label string) (string, error) {
	file, tempPath, err := createStorageTempFile(source, label)
	if err != nil {
		return "", err
	}
	if err := copyManagedFileToOpenFile(source, file); err != nil {
		discardTempFile(file, tempPath)
		return "", err
	}
	if err := closePreparedFile(file, tempPath); err != nil {
		return "", err
	}
	return tempPath, nil
}

func (j *JSONStorage) applyStorageCommit(commit storageCommitJournal) error {
	if err := replacePreparedFile(commit.ManifestTemp, j.filepath); err != nil {
		return err
	}
	if err := replacePreparedFile(commit.ExecutionTemp, j.executionPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(j.filepath))
}

func replacePreparedFile(tempPath, destination string) error {
	exists, err := pathExists(tempPath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return fmt.Errorf("failed to replace storage file: %w", err)
	}
	return nil
}

func (j *JSONStorage) rollbackStorageCommit(commit storageCommitJournal, cause error) error {
	if err := j.restoreStorageCommitBackups(commit); err != nil {
		return fmt.Errorf("%w; failed to roll back storage commit: %v", cause, err)
	}
	_ = j.removeStorageCommitJournal(commit)
	return cause
}

func (j *JSONStorage) restoreStorageCommitBackups(commit storageCommitJournal) error {
	if err := replacePreparedFile(commit.ManifestBackup, j.filepath); err != nil {
		return err
	}
	if err := replacePreparedFile(commit.ExecutionBackup, j.executionPath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(j.filepath))
}

func (j *JSONStorage) recoverPendingStorageCommit() error {
	commit, err := j.readStorageCommitJournal()
	noCommit := commit == nil
	shouldReturn := err != nil || noCommit
	if shouldReturn {
		return err
	}
	if err := j.applyStorageCommit(*commit); err != nil {
		return fmt.Errorf("failed to recover pending storage commit: %w", err)
	}
	return j.removeStorageCommitJournal(*commit)
}

func (j *JSONStorage) readStorageCommitJournal() (*storageCommitJournal, error) {
	data, err := readManagedFile(j.storageCommitJournalPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read storage commit journal: %w", err)
	}
	var commit storageCommitJournal
	if err := json.Unmarshal(data, &commit); err != nil {
		return nil, fmt.Errorf("failed to decode storage commit journal: %w", err)
	}
	return &commit, nil
}

func (j *JSONStorage) writeStorageCommitJournal(commit storageCommitJournal) error {
	data, err := json.Marshal(commit)
	if err != nil {
		return fmt.Errorf("failed to encode storage commit journal: %w", err)
	}
	return writeFileAtomically(j.storageCommitJournalPath(), data)
}

func (j *JSONStorage) removeStorageCommitJournal(commit storageCommitJournal) error {
	removeStorageCommitFiles(commit)
	if err := removeBackupFile(j.storageCommitJournalPath(), "storage commit journal"); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(j.filepath))
}

func removeStorageCommitFiles(commit storageCommitJournal) {
	_ = os.Remove(commit.ManifestTemp)
	_ = os.Remove(commit.ExecutionTemp)
	_ = os.Remove(commit.ManifestBackup)
	_ = os.Remove(commit.ExecutionBackup)
}

func (j *JSONStorage) storageCommitJournalPath() string {
	return j.filepath + storageCommitJournalSuffix
}
