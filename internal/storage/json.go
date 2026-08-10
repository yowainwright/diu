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
	config         *core.Config
	filepath       string
	executionPath  string
	data           *core.StorageData
	marshalStorage func(*core.StorageData) ([]byte, error)
	mu             sync.RWMutex
}

const (
	maxBackupPathAttempts = 1000
	executionLogFormat    = "ndjson-v1"
)

func NewJSONStorage(config *core.Config) (Storage, error) {
	storagePath, err := cleanManagedPath(config.Storage.JSONFile)
	if err != nil {
		return nil, fmt.Errorf("invalid storage path: %w", err)
	}

	js := &JSONStorage{
		config:         config,
		filepath:       storagePath,
		executionPath:  ExecutionLogPath(storagePath),
		marshalStorage: marshalStorageData,
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
		return j.initializeEmptyStorage()
	}

	_, err := inspectJSONFile(j.filepath)
	return err
}

func ExecutionLogPath(manifestPath string) string {
	extension := filepath.Ext(manifestPath)
	base := strings.TrimSuffix(manifestPath, extension)
	return base + ".ndjson"
}

func (j *JSONStorage) initializeEmptyStorage() error {
	if err := ensureExecutionLog(j.executionPath); err != nil {
		return err
	}
	j.data = newStorageManifest()
	return j.save()
}

func newStorageManifest() *core.StorageData {
	metadata := newStorageMetadata()
	statistics := emptyStorageStatistics()
	packages := make(map[string]map[string]core.PackageInfo)
	return &core.StorageData{
		Version:            "1.0.0",
		ExecutionLogFormat: executionLogFormat,
		Metadata:           metadata,
		Packages:           packages,
		Statistics:         statistics,
	}
}

func newStorageMetadata() core.StorageMetadata {
	hostname, _ := os.Hostname()
	user, _ := os.UserHomeDir()
	return core.StorageMetadata{
		Created:    time.Now(),
		Hostname:   hostname,
		User:       filepath.Base(user),
		DIUVersion: core.CurrentVersion(),
	}
}

func emptyStorageStatistics() core.StorageStatistics {
	return core.StorageStatistics{
		ToolsUsed:          []string{},
		ExecutionFrequency: make(map[string]int),
	}
}

func ensureExecutionLog(path string) error {
	file, err := safefs.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to create execution log: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close execution log: %w", err)
	}
	return nil
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
	j.data.ExecutionLogFormat = executionLogFormat
	j.data.Executions = nil
	data, err := j.marshalStorage(j.data)
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

func marshalStorageData(data *core.StorageData) ([]byte, error) {
	return json.MarshalIndent(data, "", "  ")
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

func validateExecutionRecords(records []*core.ExecutionRecord) error {
	if len(records) == 0 {
		return nil
	}
	for _, record := range records {
		if record == nil {
			return fmt.Errorf("execution record cannot be nil")
		}
	}
	return nil
}

func (j *JSONStorage) addExecutionsLocked(records []*core.ExecutionRecord) error {
	if len(records) == 0 {
		return nil
	}
	if err := j.reload(); err != nil {
		return err
	}
	storedRecords := j.prepareExecutions(records)
	if err := appendExecutionRecords(j.executionPath, storedRecords); err != nil {
		return err
	}
	if err := j.save(); err != nil {
		return err
	}
	return j.compactIfLimitsExceeded()
}

func (j *JSONStorage) prepareExecutions(records []*core.ExecutionRecord) []core.ExecutionRecord {
	stored := make([]core.ExecutionRecord, 0, len(records))
	for _, record := range records {
		stored = append(stored, j.prepareExecution(record))
	}
	return stored
}

func (j *JSONStorage) prepareExecution(record *core.ExecutionRecord) core.ExecutionRecord {
	if record.ID == "" {
		record.ID = fmt.Sprintf("exec_%s_%s", time.Now().Format("20060102_150405"), generateID())
	}
	storedRecord := copyExecutionValue(*record)
	j.updateExecutionStatistics(storedRecord.Tool)
	if j.shouldUpdateInventory(storedRecord) {
		j.applyPackageEffects(storedRecord)
	}
	return storedRecord
}

func (j *JSONStorage) applyPackageEffects(record core.ExecutionRecord) {
	tool := packageToolForRecord(record)
	if removesPackages(record) {
		for _, pkg := range record.PackagesAffected {
			j.deletePackageInternal(tool, pkg)
		}
		return
	}
	for _, pkg := range record.PackagesAffected {
		j.updatePackageInternal(tool, pkg, record.Timestamp)
	}
}

func packageToolForRecord(record core.ExecutionRecord) string {
	if record.Tool != core.ToolHomebrew {
		return record.Tool
	}
	packageType, _ := record.Metadata["type"].(string)
	if packageType == "cask" || containsString(record.Args, "--cask") {
		return core.ToolHomebrewCask
	}
	return record.Tool
}

func (j *JSONStorage) updateExecutionStatistics(tool string) {
	j.data.Statistics.TotalExecutions++
	if j.data.Statistics.ExecutionFrequency == nil {
		j.data.Statistics.ExecutionFrequency = make(map[string]int)
	}
	if _, exists := j.data.Statistics.ExecutionFrequency[tool]; !exists {
		j.data.Statistics.ToolsUsed = append(j.data.Statistics.ToolsUsed, tool)
	}
	j.data.Statistics.ExecutionFrequency[tool]++
}

func (j *JSONStorage) shouldUpdateInventory(record core.ExecutionRecord) bool {
	if record.Tool == core.ToolGo || record.Tool == core.ToolPoetry {
		return false
	}
	if record.Tool != core.ToolNPM && record.Tool != core.ToolPNPM && record.Tool != core.ToolBun {
		return true
	}
	if record.Tool == core.ToolNPM && !j.config.Tools.NPM.TrackGlobalOnly {
		return true
	}
	isGlobal, exists := record.Metadata["global"].(bool)
	return exists && isGlobal
}

func removesPackages(record core.ExecutionRecord) bool {
	action, _ := record.Metadata["action"].(string)
	switch action {
	case "uninstall", "remove", "pip_uninstall", "tool_uninstall":
		return true
	default:
		return false
	}
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
			return fmt.Errorf("package cannot be nil")
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

func (j *JSONStorage) setPackage(pkg *core.PackageInfo) {
	if j.data.Packages == nil {
		j.data.Packages = make(map[string]map[string]core.PackageInfo)
	}
	if j.data.Packages[pkg.Tool] == nil {
		j.data.Packages[pkg.Tool] = make(map[string]core.PackageInfo)
	}
	stored := copyPackageValue(*pkg)
	stored.UpdatedAt = time.Now().UnixNano()
	j.data.Packages[pkg.Tool][pkg.Name] = stored
	j.clearPackageTombstone(pkg.Tool, pkg.Name)
}

func (j *JSONStorage) updatePackageInternal(tool, name string, timestamp time.Time) {
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
	pkg.UpdatedAt = time.Now().UnixNano()

	j.data.Packages[tool][name] = pkg
	j.clearPackageTombstone(tool, name)
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

func (j *JSONStorage) deletePackageInternal(tool, name string) {
	if j.data.Packages != nil && j.data.Packages[tool] != nil {
		delete(j.data.Packages[tool], name)
		if len(j.data.Packages[tool]) == 0 {
			delete(j.data.Packages, tool)
		}
	}
	if j.data.PackageTombstones == nil {
		j.data.PackageTombstones = make(map[string]map[string]int64)
	}
	if j.data.PackageTombstones[tool] == nil {
		j.data.PackageTombstones[tool] = make(map[string]int64)
	}
	j.data.PackageTombstones[tool][name] = time.Now().UnixNano()
}

func (j *JSONStorage) ReconcilePackages(scanned map[string]map[string]struct{}, startedAt time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}
		changed := j.removeMissingPackages(scanned, startedAt)
		if !changed {
			return nil
		}
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

func (j *JSONStorage) applyScannedPackage(pkg *core.PackageInfo, startedAt time.Time) {
	if j.packageTombstonedAfter(pkg.Tool, pkg.Name, startedAt) {
		return
	}
	stored := copyPackageValue(*pkg)
	current := j.packageHistoryForScan(stored.Tool, stored.Name)
	if packageUpdatedAfter(current, startedAt) {
		preserveConcurrentUsage(&stored, current)
	}
	j.setPackage(&stored)
}

func (j *JSONStorage) packageHistoryForScan(tool, name string) core.PackageInfo {
	current := j.data.Packages[tool][name]
	if tool != core.ToolGoBinary {
		return current
	}
	legacy := j.data.Packages[core.ToolGo][name]
	current.UsageCount += legacy.UsageCount
	if legacy.LastUsed.After(current.LastUsed) {
		current.LastUsed = legacy.LastUsed
	}
	current.UpdatedAt = max(current.UpdatedAt, legacy.UpdatedAt)
	return current
}

func preserveConcurrentUsage(scanned *core.PackageInfo, current core.PackageInfo) {
	if current.UsageCount > scanned.UsageCount {
		scanned.UsageCount = current.UsageCount
	}
	if current.LastUsed.After(scanned.LastUsed) {
		scanned.LastUsed = current.LastUsed
	}
}

func (j *JSONStorage) packageTombstonedAfter(tool, name string, startedAt time.Time) bool {
	deletedAt := j.data.PackageTombstones[tool][name]
	if deletedAt == 0 {
		return false
	}
	return deletedAt >= startedAt.UnixNano()
}

func (j *JSONStorage) clearPackageTombstone(tool, name string) {
	delete(j.data.PackageTombstones[tool], name)
	if len(j.data.PackageTombstones[tool]) == 0 {
		delete(j.data.PackageTombstones, tool)
	}
}

func (j *JSONStorage) prunePackageTombstones(scanned map[string]map[string]struct{}, startedAt time.Time) {
	for tool := range scanned {
		for name, deletedAt := range j.data.PackageTombstones[tool] {
			if deletedAt < startedAt.UnixNano() {
				j.clearPackageTombstone(tool, name)
			}
		}
	}
}

func (j *JSONStorage) removeMissingPackages(scanned map[string]map[string]struct{}, startedAt time.Time) bool {
	changed := false
	for tool, seen := range scanned {
		toolPackages, exists := j.data.Packages[tool]
		for name, pkg := range toolPackages {
			if _, exists := seen[name]; exists {
				continue
			}
			if packageUpdatedAfter(pkg, startedAt) {
				continue
			}
			delete(j.data.Packages[tool], name)
			changed = true
		}
		if exists && len(j.data.Packages[tool]) == 0 {
			delete(j.data.Packages, tool)
			changed = true
		}
	}
	return changed
}

func packageUpdatedAfter(pkg core.PackageInfo, startedAt time.Time) bool {
	if startedAt.IsZero() {
		return false
	}
	if pkg.UpdatedAt >= startedAt.UnixNano() {
		return true
	}
	return pkg.InstallDate.After(startedAt) || pkg.LastUsed.After(startedAt)
}

func (j *JSONStorage) UpdateStatistics() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		if err := j.reload(); err != nil {
			return err
		}
		statistics, err := j.calculateExecutionStatistics()
		if err != nil {
			return err
		}
		j.data.Statistics = statistics
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

func (j *JSONStorage) backup() error {
	backupPath, err := j.nextBackupPath(time.Now())
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(j.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal backup data: %w", err)
	}

	if err := os.WriteFile(backupPath, data, core.PrivateFileMode); err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}
	executionBackupPath := j.executionBackupPath(backupPath)
	if err := copyManagedFile(j.executionPath, executionBackupPath); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("failed to back up execution log: %w", err)
	}

	if err := j.pruneBackups(); err != nil {
		return err
	}

	return nil
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
		return j.compact(before)
	})
}

func (j *JSONStorage) pruneBackups() error {
	maxBackups := j.config.Storage.MaxBackups
	if maxBackups <= 0 {
		return nil
	}

	paths, err := filepath.Glob(j.filepath + ".backup.*")
	if err != nil {
		return fmt.Errorf("failed to list backup files: %w", err)
	}

	backups := make([]backupFile, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("failed to stat backup file %s: %w", path, err)
		}
		modTime := info.ModTime()
		backup := backupFile{path: path, modTime: modTime}
		backups = append(backups, backup)
	}
	if len(backups) <= maxBackups {
		return nil
	}

	sort.Slice(backups, func(i, k int) bool {
		if !backups[i].modTime.Equal(backups[k].modTime) {
			return backups[i].modTime.Before(backups[k].modTime)
		}
		return backups[i].path < backups[k].path
	})

	for _, backup := range backups[:len(backups)-maxBackups] {
		if err := os.Remove(backup.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove old backup %s: %w", backup.path, err)
		}
		executionBackup := j.executionBackupPath(backup.path)
		if err := os.Remove(executionBackup); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove old execution backup %s: %w", executionBackup, err)
		}
	}

	return nil
}

type backupFile struct {
	path    string
	modTime time.Time
}

func (j *JSONStorage) nextBackupPath(now time.Time) (string, error) {
	base := fmt.Sprintf("%s.backup.%s", j.filepath, now.Format("20060102_150405_000000000"))
	for i := 0; i < maxBackupPathAttempts; i++ {
		path := base
		if i > 0 {
			path = fmt.Sprintf("%s.%d", base, i)
		}

		manifestExists, err := pathExists(path)
		if err != nil {
			return "", fmt.Errorf("failed to stat backup path %s: %w", path, err)
		}
		executionPath := j.executionBackupPath(path)
		executionExists, err := pathExists(executionPath)
		if err != nil {
			return "", fmt.Errorf("failed to stat execution backup path %s: %w", executionPath, err)
		}
		if manifestExists || executionExists {
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("failed to find available backup path after %d attempts", maxBackupPathAttempts)
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (j *JSONStorage) reload() error {
	usesLog, err := storageUsesExecutionLog(j.filepath)
	if err != nil {
		return err
	}
	if !usesLog {
		if err := j.compact(time.Time{}); err != nil {
			return err
		}
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

func copyExecutionValue(record core.ExecutionRecord) core.ExecutionRecord {
	record.Args = copyStringSlice(record.Args)
	record.Environment = copyStringMap(record.Environment)
	record.PackagesAffected = copyStringSlice(record.PackagesAffected)
	record.Metadata = copyMetadataMap(record.Metadata)
	return record
}

func copyPackageValue(pkg core.PackageInfo) core.PackageInfo {
	pkg.Dependencies = copyStringSlice(pkg.Dependencies)
	return pkg
}

func copyStorageStatistics(stats core.StorageStatistics) core.StorageStatistics {
	stats.ToolsUsed = copyStringSlice(stats.ToolsUsed)
	stats.ExecutionFrequency = copyStringIntMap(stats.ExecutionFrequency)
	return stats
}

func copyStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func copyStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func copyStringIntMap(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	copy := make(map[string]int, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func copyMetadataMap(values map[string]interface{}) map[string]interface{} {
	if values == nil {
		return nil
	}
	copy := make(map[string]interface{}, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
