package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

type JSONStorage struct {
	config   *core.Config
	filepath string
	data     *core.StorageData
	mu       sync.RWMutex
}

func NewJSONStorage(config *core.Config) (Storage, error) {
	js := &JSONStorage{
		config:   config,
		filepath: config.Storage.JSONFile,
	}
	return js, js.Initialize(config)
}

func (j *JSONStorage) Initialize(config *core.Config) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	dir := filepath.Dir(j.filepath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}

	if _, err := os.Stat(j.filepath); os.IsNotExist(err) {
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
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.save()
}

func (j *JSONStorage) load() error {
	data, err := os.ReadFile(j.filepath)
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
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
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

	if j.data.Packages == nil {
		j.data.Packages = make(map[string]map[string]core.PackageInfo)
	}

	if j.data.Packages[pkg.Tool] == nil {
		j.data.Packages[pkg.Tool] = make(map[string]core.PackageInfo)
	}

	j.data.Packages[pkg.Tool][pkg.Name] = *pkg
	return j.save()
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

	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}

	return nil
}

func (j *JSONStorage) Restore(path string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := os.ReadFile(path)
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

	var kept []core.ExecutionRecord
	for _, exec := range j.data.Executions {
		if exec.Timestamp.After(before) {
			kept = append(kept, exec)
		}
	}

	j.data.Executions = kept
	j.data.Statistics.TotalExecutions = len(kept)

	return j.save()
}

func generateID() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}