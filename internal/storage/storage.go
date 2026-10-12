package storage

import (
	"errors"
	"fmt"
	"iter"
	"path/filepath"
	"strings"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

var (
	ErrExecutionLogNotFound          = errors.New("execution log not found")
	ErrUnsupportedExecutionLogFormat = errors.New("unsupported execution log format")
	ErrEmptyPath                     = errors.New("path cannot be empty")
	ErrNilExecutionRecord            = errors.New("execution record cannot be nil")
	ErrNilPackage                    = errors.New("package cannot be nil")
)

func NewJSONStorage(config *core.Config) (*JSONStorage, error) {
	return newJSONStorage(config, nil)
}

func NewJSONStorageWithLockWait(config *core.Config, wait time.Duration) (*JSONStorage, error) {
	return newJSONStorage(config, &wait)
}

func (j *JSONStorage) Initialize(config *core.Config) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if err := j.ensureStorageDirectory(); err != nil {
		return err
	}
	return j.withFileLock(j.loadOrCreateStorage)
}

func ExecutionLogPath(manifestPath string) string {
	extension := filepath.Ext(manifestPath)
	base := strings.TrimSuffix(manifestPath, extension)
	logPath := base + ".ndjson"
	if logPath == manifestPath {
		return base + ".executions.ndjson"
	}
	return logPath
}

func (j *JSONStorage) Close() error {
	return nil
}

func (j *JSONStorage) AddExecution(record *core.ExecutionRecord) error {
	records := []*core.ExecutionRecord{record}
	return j.AddExecutions(records)
}

func (j *JSONStorage) AddExecutions(records []*core.ExecutionRecord) error {
	if err := validateExecutionRecords(records); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.withFileLock(func() error {
		return j.addExecutionsLocked(records)
	})
}

func (j *JSONStorage) UpdatePackage(pkg *core.PackageInfo) error {
	packages := []*core.PackageInfo{pkg}
	return j.UpdatePackages(packages)
}

func (j *JSONStorage) UpdatePackages(packages []*core.PackageInfo) error {
	if len(packages) == 0 {
		return nil
	}
	for _, pkg := range packages {
		if pkg == nil {
			return ErrNilPackage
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}

		for _, pkg := range packages {
			j.setPackage(pkg)
		}
		return j.save()
	})
}

func (j *JSONStorage) DeletePackage(tool, name string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}
		j.deletePackageInternal(tool, name)
		return j.save()
	})
}

func (j *JSONStorage) ApplyPackageScan(
	packages []*core.PackageInfo,
	scanned map[string]map[string]struct{},
	startedAt time.Time,
) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}
		for _, pkg := range packages {
			j.applyScannedPackage(pkg, startedAt)
		}
		j.removeMissingPackages(scanned, startedAt)
		j.prunePackageTombstones(scanned, startedAt)
		return j.save()
	})
}

func (j *JSONStorage) Backup() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}
		return j.backup()
	})
}

func (j *JSONStorage) Restore(path string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		restorePath, err := j.cleanRestorePath(path)
		if err != nil {
			return err
		}

		return j.restoreBackup(restorePath)
	})
}

func (j *JSONStorage) Cleanup(before time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}
		return j.compact(before)
	})
}

func (j *JSONStorage) Prepare() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}
		return j.compactIfLimitsExceeded()
	})
}

func InspectJSONFile(path string) (JSONInspection, error) {
	storagePath, err := cleanManagedPath(path)
	if err != nil {
		return JSONInspection{}, err
	}
	info, err := inspectJSONFile(storagePath)
	cannotInspect := err != nil || info == nil
	if cannotInspect {
		return JSONInspection{}, err
	}
	storage := JSONStorage{
		filepath:      storagePath,
		executionPath: ExecutionLogPath(storagePath),
	}
	var inspection JSONInspection
	err = storage.withFileLock(func() error {
		if err := storage.recoverPendingStorageCommit(); err != nil {
			return err
		}
		inspection, err = inspectCurrentJSONFile(storagePath)
		return err
	})
	return inspection, err
}

func (j *JSONStorage) Executions() iter.Seq2[core.ExecutionRecord, error] {
	return func(yield func(core.ExecutionRecord, error) bool) {
		err := j.withConsistentRead(func() error {
			j.executionSeq()(yield)
			return nil
		})
		if err != nil {
			yield(core.ExecutionRecord{}, err)
		}
	}
}

func (j *JSONStorage) GetExecutions(opts QueryOptions) ([]*core.ExecutionRecord, error) {
	collector := newExecutionCollector(opts)
	err := j.withConsistentRead(func() error {
		for record, err := range j.executionSeq() {
			if err != nil {
				return err
			}
			collector.add(record)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return collector.results(), nil
}

func (j *JSONStorage) GetPackage(tool, name string) (*core.PackageInfo, error) {
	var found *core.PackageInfo
	visit := findPackageVisitor(tool, name, &found)
	var visitor jsonStorageVisitor
	visitor.packageInfo = visit
	err := j.withConsistentRead(func() error {
		return scanJSONStorage(j.filepath, visitor)
	})
	if shouldReturnPackageScanError(err) {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("package not found: %s/%s", tool, name)
	}
	return found, nil
}

func (j *JSONStorage) GetPackages(tool string) ([]*core.PackageInfo, error) {
	var packages []*core.PackageInfo
	visit := collectPackagesVisitor(tool, &packages)
	var visitor jsonStorageVisitor
	visitor.packageInfo = visit
	err := j.withConsistentRead(func() error {
		return scanJSONStorage(j.filepath, visitor)
	})
	return packages, err
}

func (j *JSONStorage) AllPackages() (map[string]map[string]*core.PackageInfo, error) {
	packages := make(map[string]map[string]*core.PackageInfo)
	visit := collectAllPackagesVisitor(packages)
	var visitor jsonStorageVisitor
	visitor.packageInfo = visit
	err := j.withConsistentRead(func() error {
		return scanJSONStorage(j.filepath, visitor)
	})
	return packages, err
}

func (j *JSONStorage) Statistics() (*core.StorageStatistics, error) {
	var found *core.StorageStatistics
	visit := func(statistics core.StorageStatistics) {
		copy := copyStorageStatistics(statistics)
		found = &copy
	}
	var visitor jsonStorageVisitor
	visitor.statistics = visit
	if err := j.withConsistentRead(func() error {
		return scanJSONStorage(j.filepath, visitor)
	}); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("storage statistics not found")
	}
	return found, nil
}

func (j *JSONStorage) SummarizeExecutions(opts QueryOptions) (ExecutionSummary, error) {
	toolCounts := make(map[string]int)
	summary := ExecutionSummary{ToolCounts: toolCounts}
	err := j.withConsistentRead(func() error {
		for record, err := range j.executionSeq() {
			if err != nil {
				return err
			}
			if executionMatches(record, opts) {
				summary.Total++
				summary.ToolCounts[record.Tool]++
			}
		}
		return nil
	})
	return summary, err
}
