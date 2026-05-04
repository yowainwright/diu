package storage

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

type JSONStorage struct {
	config   *core.Config
	filepath string
	data     *core.StorageData
	mu       sync.RWMutex
}

func NewJSONStorage(config *core.Config) (Storage, error) {
	storagePath, err := cleanManagedPath(config.Storage.JSONFile)
	if err != nil {
		return nil, fmt.Errorf("invalid storage path: %w", err)
	}

	js := &JSONStorage{
		config:   config,
		filepath: storagePath,
	}
	return js, js.Initialize(config)
}

func (j *JSONStorage) Initialize(config *core.Config) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	dir := filepath.Dir(j.filepath)
	if err := os.MkdirAll(dir, core.OwnerDirectoryMode); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}

	if _, err := os.Stat(j.filepath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat storage file: %w", err)
		}
		hostname, _ := os.Hostname()
		user, _ := os.UserHomeDir()
		j.data = &core.StorageData{
			Version: "1.0.0",
			Metadata: core.StorageMetadata{
				Created:     time.Now(),
				LastUpdated: time.Now(),
				Hostname:    hostname,
				User:        filepath.Base(user),
				DIUVersion:  "0.1.0",
			},
			Executions: []core.ExecutionRecord{},
			Packages:   make(map[string]map[string]core.PackageInfo),
			Statistics: core.StorageStatistics{
				TotalExecutions:    0,
				ToolsUsed:          []string{},
				MostActiveDay:      "",
				ExecutionFrequency: make(map[string]int),
			},
		}
		return j.save()
	}

	return j.load()
}

func (j *JSONStorage) Close() error {
	return nil
}

func (j *JSONStorage) load() error {
	data, err := readManagedFile(j.filepath)
	if err != nil {
		return fmt.Errorf("failed to read storage file: %w", err)
	}

	var storage core.StorageData
	if err := json.Unmarshal(data, &storage); err != nil {
		return fmt.Errorf("failed to unmarshal storage data: %w", err)
	}

	j.data = &storage
	return nil
}

func (j *JSONStorage) save() error {
	j.data.Metadata.LastUpdated = time.Now()

	data, err := json.MarshalIndent(j.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal storage data: %w", err)
	}

	tempFile := j.filepath + ".tmp"
	if err := os.WriteFile(tempFile, data, core.PrivateFileMode); err != nil {
		return fmt.Errorf("failed to write storage file: %w", err)
	}

	if err := os.Rename(tempFile, j.filepath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func (j *JSONStorage) AddExecution(record *core.ExecutionRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}

		if record.ID == "" {
			record.ID = fmt.Sprintf("exec_%s_%s", time.Now().Format("20060102_150405"), generateID())
		}

		j.data.Executions = append(j.data.Executions, *record)
		j.data.Statistics.TotalExecutions++

		if _, exists := j.data.Statistics.ExecutionFrequency[record.Tool]; !exists {
			j.data.Statistics.ExecutionFrequency[record.Tool] = 0
			j.data.Statistics.ToolsUsed = append(j.data.Statistics.ToolsUsed, record.Tool)
		}
		j.data.Statistics.ExecutionFrequency[record.Tool]++

		for _, pkg := range record.PackagesAffected {
			if err := j.updatePackageInternal(record.Tool, pkg, record.Timestamp); err != nil {
				return err
			}
		}

		return j.save()
	})
}

func (j *JSONStorage) GetExecutions(opts QueryOptions) ([]*core.ExecutionRecord, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	var results []*core.ExecutionRecord

	for i := range j.data.Executions {
		exec := &j.data.Executions[i]

		if opts.Tool != "" && exec.Tool != opts.Tool {
			continue
		}

		if opts.Package != "" {
			found := false
			for _, pkg := range exec.PackagesAffected {
				if pkg == opts.Package {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		if opts.Since != nil && exec.Timestamp.Before(*opts.Since) {
			continue
		}

		if opts.Until != nil && exec.Timestamp.After(*opts.Until) {
			continue
		}

		results = append(results, exec)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

func (j *JSONStorage) GetExecutionByID(id string) (*core.ExecutionRecord, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	for i := range j.data.Executions {
		if j.data.Executions[i].ID == id {
			return &j.data.Executions[i], nil
		}
	}

	return nil, fmt.Errorf("execution not found: %s", id)
}

func (j *JSONStorage) UpdatePackage(pkg *core.PackageInfo) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}

		if j.data.Packages == nil {
			j.data.Packages = make(map[string]map[string]core.PackageInfo)
		}

		if j.data.Packages[pkg.Tool] == nil {
			j.data.Packages[pkg.Tool] = make(map[string]core.PackageInfo)
		}

		j.data.Packages[pkg.Tool][pkg.Name] = *pkg
		return j.save()
	})
}

func (j *JSONStorage) updatePackageInternal(tool, name string, timestamp time.Time) error {
	if j.data.Packages == nil {
		j.data.Packages = make(map[string]map[string]core.PackageInfo)
	}

	if j.data.Packages[tool] == nil {
		j.data.Packages[tool] = make(map[string]core.PackageInfo)
	}

	pkg, exists := j.data.Packages[tool][name]
	if !exists {
		pkg = core.PackageInfo{
			Name:        name,
			Tool:        tool,
			InstallDate: timestamp,
			LastUsed:    timestamp,
			UsageCount:  1,
		}
	} else {
		pkg.LastUsed = timestamp
		pkg.UsageCount++
	}

	j.data.Packages[tool][name] = pkg
	return nil
}

func (j *JSONStorage) GetPackage(tool, name string) (*core.PackageInfo, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	if j.data.Packages == nil || j.data.Packages[tool] == nil {
		return nil, fmt.Errorf("package not found: %s/%s", tool, name)
	}

	pkg, exists := j.data.Packages[tool][name]
	if !exists {
		return nil, fmt.Errorf("package not found: %s/%s", tool, name)
	}

	return &pkg, nil
}

func (j *JSONStorage) GetPackages(tool string) ([]*core.PackageInfo, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	var results []*core.PackageInfo

	if tool == "" {
		for _, toolPackages := range j.data.Packages {
			for _, pkg := range toolPackages {
				p := pkg
				results = append(results, &p)
			}
		}
	} else {
		if j.data.Packages[tool] != nil {
			for _, pkg := range j.data.Packages[tool] {
				p := pkg
				results = append(results, &p)
			}
		}
	}

	return results, nil
}

func (j *JSONStorage) GetAllPackages() (map[string]map[string]*core.PackageInfo, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	result := make(map[string]map[string]*core.PackageInfo)
	for tool, packages := range j.data.Packages {
		result[tool] = make(map[string]*core.PackageInfo)
		for name, pkg := range packages {
			p := pkg
			result[tool][name] = &p
		}
	}

	return result, nil
}

func (j *JSONStorage) DeletePackage(tool, name string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}
		if j.data.Packages == nil || j.data.Packages[tool] == nil {
			return nil
		}
		delete(j.data.Packages[tool], name)
		if len(j.data.Packages[tool]) == 0 {
			delete(j.data.Packages, tool)
		}
		return j.save()
	})
}

func (j *JSONStorage) GetStatistics() (*core.StorageStatistics, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	stats := j.data.Statistics
	return &stats, nil
}

func (j *JSONStorage) UpdateStatistics() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	dayCount := make(map[string]int)
	for _, exec := range j.data.Executions {
		day := exec.Timestamp.Format("2006-01-02")
		dayCount[day]++
	}

	maxCount := 0
	mostActiveDay := ""
	for day, count := range dayCount {
		if count > maxCount {
			maxCount = count
			mostActiveDay = day
		}
	}

	j.data.Statistics.MostActiveDay = mostActiveDay
	return j.save()
}

func (j *JSONStorage) Backup() error {
	j.mu.RLock()
	defer j.mu.RUnlock()

	backupPath := fmt.Sprintf("%s.backup.%s", j.filepath, time.Now().Format("20060102_150405"))

	data, err := json.MarshalIndent(j.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal backup data: %w", err)
	}

	if err := os.WriteFile(backupPath, data, core.PrivateFileMode); err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}

	return nil
}

func (j *JSONStorage) Restore(path string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	restorePath, err := j.cleanRestorePath(path)
	if err != nil {
		return err
	}

	data, err := readManagedFile(restorePath)
	if err != nil {
		return fmt.Errorf("failed to read restore file: %w", err)
	}

	var storage core.StorageData
	if err := json.Unmarshal(data, &storage); err != nil {
		return fmt.Errorf("failed to unmarshal restore data: %w", err)
	}

	j.data = &storage
	return j.save()
}

func (j *JSONStorage) Cleanup(before time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}

		var kept []core.ExecutionRecord
		for _, exec := range j.data.Executions {
			if exec.Timestamp.After(before) {
				kept = append(kept, exec)
			}
		}

		j.data.Executions = kept
		j.data.Statistics.TotalExecutions = len(kept)

		return j.save()
	})
}

func (j *JSONStorage) reload() error {
	if _, err := os.Stat(j.filepath); err != nil {
		return err
	}
	return j.load()
}

func (j *JSONStorage) withFileLock(fn func() error) (err error) {
	lockPath := j.filepath + ".lock"
	lockFile, err := safefs.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to open storage lock: %w", err)
	}
	defer func() {
		if closeErr := lockFile.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close storage lock: %w", closeErr)
		}
	}()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to lock storage: %w", err)
	}

	if err := fn(); err != nil {
		unlockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		if unlockErr != nil {
			return fmt.Errorf("%w; additionally failed to unlock storage: %v", err, unlockErr)
		}
		return err
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("failed to unlock storage: %w", err)
	}

	return nil
}

func cleanManagedPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		absPath, err := filepath.Abs(cleanPath)
		if err != nil {
			return "", err
		}
		cleanPath = absPath
	}
	return cleanPath, nil
}

func readManagedFile(path string) ([]byte, error) {
	cleanPath, err := cleanManagedPath(path)
	if err != nil {
		return nil, err
	}

	info, err := safefs.Lstat(cleanPath)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("path cannot be a symlink: %s", cleanPath)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file: %s", cleanPath)
	}

	// #nosec G304 -- DIU normalizes the path and verifies it is a regular managed file before reading.
	return safefs.ReadFile(cleanPath)
}

func (j *JSONStorage) cleanRestorePath(path string) (string, error) {
	restorePath, err := cleanManagedPath(path)
	if err != nil {
		return "", err
	}

	storageDir := filepath.Dir(j.filepath)
	if filepath.Dir(restorePath) != storageDir {
		return "", fmt.Errorf("restore file must be in storage directory: %s", storageDir)
	}

	backupPrefix := filepath.Base(j.filepath) + ".backup."
	if !strings.HasPrefix(filepath.Base(restorePath), backupPrefix) {
		return "", fmt.Errorf("restore file must be a backup for %s", filepath.Base(j.filepath))
	}

	return restorePath, nil
}

func generateID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err == nil {
		for i, v := range b {
			b[i] = charset[int(v)%len(charset)]
		}
		return string(b)
	}
	return fmt.Sprintf("%06x", time.Now().UnixNano()&0xFFFFFF)
}
