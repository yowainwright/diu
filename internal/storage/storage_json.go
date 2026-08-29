package storage

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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

func NewJSONStorage(config *core.Config) (*JSONStorage, error) {
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

	if err := j.ensureStorageDirectory(); err != nil {
		return err
	}
	return j.loadOrCreateStorage()
}

func (j *JSONStorage) ensureStorageDirectory() error {
	dir := filepath.Dir(j.filepath)
	if err := os.MkdirAll(dir, core.OwnerDirectoryMode); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}
	return nil
}

func (j *JSONStorage) loadOrCreateStorage() error {
	if _, err := os.Stat(j.filepath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat storage file: %w", err)
		}
		return j.initializeEmptyStorage()
	}

	usesLog, err := storageUsesExecutionLog(j.filepath)
	hasCurrentLogFormat := err == nil && usesLog
	if !hasCurrentLogFormat {
		return err
	}
	if err := j.ensureCurrentExecutionLog(); err != nil {
		return err
	}
	j.data = nil
	return nil
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
		Executions:         []core.ExecutionRecord{},
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
	j.data.Executions = []core.ExecutionRecord{}
	data, err := j.marshalStorage(j.data)
	if err != nil {
		return fmt.Errorf("failed to marshal storage data: %w", err)
	}

	return writeFileAtomically(j.filepath, data)
}

func writeFileAtomically(path string, data []byte) error {
	tempPath := path + ".tmp"
	file, err := safefs.OpenFile(tempPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to write storage file: %w", err)
	}
	if err := writeAll(file, data); err != nil {
		discardTempFile(file, tempPath)
		return fmt.Errorf("failed to write storage file: %w", err)
	}
	if err := commitTempFile(file, tempPath, path); err != nil {
		return fmt.Errorf("failed to replace storage file: %w", err)
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func commitTempFile(file *os.File, tempPath, destination string) error {
	if err := file.Sync(); err != nil {
		discardTempFile(file, tempPath)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	return syncDirectory(filepath.Dir(destination))
}

func syncDirectory(path string) (err error) {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open storage directory: %w", err)
	}
	defer func() {
		err = safefs.CloseWithError(err, dir, "failed to close storage directory")
	}()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("failed to sync storage directory: %w", err)
	}
	return nil
}

func discardTempFile(file *os.File, path string) {
	_ = file.Close()
	_ = os.Remove(path)
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
			return ErrNilExecutionRecord
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
	j.applyExecutions(storedRecords)
	if err := j.save(); err != nil {
		return err
	}
	return j.compactIfLimitsExceeded()
}

func (j *JSONStorage) prepareExecutions(records []*core.ExecutionRecord) []core.ExecutionRecord {
	stored := make([]core.ExecutionRecord, len(records))
	for index, record := range records {
		stored[index] = j.prepareExecution(record)
	}
	return stored
}

func (j *JSONStorage) prepareExecution(record *core.ExecutionRecord) core.ExecutionRecord {
	if record.ID == "" {
		record.ID = fmt.Sprintf("exec_%s_%s", time.Now().Format("20060102_150405"), generateID())
	}
	return copyExecutionValue(*record)
}

func (j *JSONStorage) applyExecutions(records []core.ExecutionRecord) {
	for _, record := range records {
		j.applyExecution(record)
	}
}

func (j *JSONStorage) applyExecution(record core.ExecutionRecord) {
	j.updateExecutionStatistics(record.Tool)
	if j.shouldUpdateInventory(record) {
		j.applyPackageEffects(record)
	}
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
	hasCaskType := packageType == "cask"
	hasCaskFlag := slices.Contains(record.Args, "--cask")
	isCask := hasCaskType || hasCaskFlag
	if isCask {
		return core.ToolHomebrewCask
	}
	return record.Tool
}

func (j *JSONStorage) updateExecutionStatistics(tool string) {
	statistics := &j.data.Statistics
	statistics.TotalExecutions++
	if statistics.ExecutionFrequency == nil {
		statistics.ExecutionFrequency = make(map[string]int)
	}
	if _, exists := statistics.ExecutionFrequency[tool]; !exists {
		statistics.ToolsUsed = append(statistics.ToolsUsed, tool)
	}
	statistics.ExecutionFrequency[tool]++
}

func (j *JSONStorage) shouldUpdateInventory(record core.ExecutionRecord) bool {
	if skipInventoryTool(record.Tool) {
		return false
	}
	if !isJSManager(record.Tool) {
		return true
	}
	if j.tracksLocalNPM(record.Tool) {
		return true
	}
	return executionWasGlobal(record)
}

func skipInventoryTool(tool string) bool {
	switch tool {
	case core.ToolGo, core.ToolPoetry:
		return true
	default:
		return false
	}
}

func isJSManager(tool string) bool {
	switch tool {
	case core.ToolNPM, core.ToolPNPM, core.ToolBun:
		return true
	default:
		return false
	}
}

func (j *JSONStorage) tracksLocalNPM(tool string) bool {
	if tool != core.ToolNPM {
		return false
	}
	npmConfig := j.config.Tools.NPM
	return !npmConfig.ShouldTrackGlobalOnly
}

func executionWasGlobal(record core.ExecutionRecord) bool {
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

func (j *JSONStorage) setPackage(pkg *core.PackageInfo) {
	stored := copyPackageValue(*pkg)
	stored.UpdatedAt = time.Now().UnixNano()
	toolPackages := j.ensureToolPackages(pkg.Tool)
	toolPackages[pkg.Name] = stored
	j.clearPackageTombstone(pkg.Tool, pkg.Name)
}

func (j *JSONStorage) updatePackageInternal(tool, name string, timestamp time.Time) {
	toolPackages := j.ensureToolPackages(tool)
	pkg, exists := toolPackages[name]
	pkg = packageUsageForUpdate(pkg, exists, tool, name, timestamp)
	pkg.UpdatedAt = time.Now().UnixNano()
	toolPackages[name] = pkg
	j.clearPackageTombstone(tool, name)
}

func (j *JSONStorage) ensureToolPackages(tool string) map[string]core.PackageInfo {
	if j.data.Packages == nil {
		j.data.Packages = make(map[string]map[string]core.PackageInfo)
	}
	if j.data.Packages[tool] == nil {
		j.data.Packages[tool] = make(map[string]core.PackageInfo)
	}
	return j.data.Packages[tool]
}

func packageUsageForUpdate(pkg core.PackageInfo, exists bool, tool, name string, timestamp time.Time) core.PackageInfo {
	if !exists {
		return core.PackageInfo{
			Name:        name,
			Tool:        tool,
			InstallDate: timestamp,
			LastUsed:    timestamp,
			UsageCount:  1,
		}
	}
	pkg.LastUsed = timestamp
	pkg.UsageCount++
	return pkg
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
	if j.data.Packages != nil {
		j.deleteInventoryPackage(tool, name)
	}
	if j.data.PackageTombstones == nil {
		j.data.PackageTombstones = make(map[string]map[string]int64)
	}
	tombstones := j.data.PackageTombstones
	if tombstones[tool] == nil {
		tombstones[tool] = make(map[string]int64)
	}
	toolTombstones := tombstones[tool]
	toolTombstones[name] = time.Now().UnixNano()
}

func (j *JSONStorage) deleteInventoryPackage(tool, name string) {
	packages := j.data.Packages
	toolPackages := packages[tool]
	if toolPackages == nil {
		return
	}
	delete(toolPackages, name)
	if len(toolPackages) == 0 {
		delete(packages, tool)
	}
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
	packages := j.data.Packages
	toolPackages := packages[tool]
	current := toolPackages[name]
	if tool != core.ToolGoBinary {
		return current
	}
	goPackages := packages[core.ToolGo]
	legacy := goPackages[name]
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
	tombstones := j.data.PackageTombstones
	toolTombstones := tombstones[tool]
	deletedAt := toolTombstones[name]
	if deletedAt == 0 {
		return false
	}
	return deletedAt >= startedAt.UnixNano()
}

func (j *JSONStorage) clearPackageTombstone(tool, name string) {
	tombstones := j.data.PackageTombstones
	toolTombstones := tombstones[tool]
	delete(toolTombstones, name)
	if len(toolTombstones) == 0 {
		delete(tombstones, tool)
	}
}

func (j *JSONStorage) prunePackageTombstones(scanned map[string]map[string]struct{}, startedAt time.Time) {
	for tool := range scanned {
		j.pruneToolPackageTombstones(tool, startedAt)
	}
}

func (j *JSONStorage) pruneToolPackageTombstones(tool string, startedAt time.Time) {
	tombstones := j.data.PackageTombstones
	toolTombstones := tombstones[tool]
	for name, deletedAt := range toolTombstones {
		if deletedAt < startedAt.UnixNano() {
			j.clearPackageTombstone(tool, name)
		}
	}
}

func (j *JSONStorage) removeMissingPackages(scanned map[string]map[string]struct{}, startedAt time.Time) bool {
	changed := false
	for tool, seen := range scanned {
		if j.removeMissingToolPackages(tool, seen, startedAt) {
			changed = true
		}
		if j.removeEmptyToolPackages(tool) {
			changed = true
		}
	}
	return changed
}

func (j *JSONStorage) removeMissingToolPackages(
	tool string,
	seen map[string]struct{},
	startedAt time.Time,
) bool {
	changed := false
	for name, pkg := range j.data.Packages[tool] {
		if packageShouldRemain(name, pkg, seen, startedAt) {
			continue
		}
		delete(j.data.Packages[tool], name)
		changed = true
	}
	return changed
}

func (j *JSONStorage) removeEmptyToolPackages(tool string) bool {
	_, exists := j.data.Packages[tool]
	empty := len(j.data.Packages[tool]) == 0
	removeTool := exists && empty
	if removeTool {
		delete(j.data.Packages, tool)
		return true
	}
	return false
}

func packageShouldRemain(
	name string,
	pkg core.PackageInfo,
	seen map[string]struct{},
	startedAt time.Time,
) bool {
	if _, exists := seen[name]; exists {
		return true
	}
	return packageUpdatedAfter(pkg, startedAt)
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
	if err := j.writeBackupManifest(backupPath); err != nil {
		return err
	}
	if err := j.copyBackupExecutionLog(backupPath); err != nil {
		return err
	}
	return j.pruneBackups()
}

func (j *JSONStorage) writeBackupManifest(path string) error {
	data, err := json.MarshalIndent(j.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal backup data: %w", err)
	}
	if err := os.WriteFile(path, data, core.PrivateFileMode); err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}
	return nil
}

func (j *JSONStorage) copyBackupExecutionLog(backupPath string) error {
	executionBackupPath := j.executionBackupPath(backupPath)
	if err := copyManagedFile(j.executionPath, executionBackupPath); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("failed to back up execution log: %w", err)
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

func (j *JSONStorage) Prepare() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.withFileLock(func() error {
		usesLog, err := storageUsesExecutionLog(j.filepath)
		if err != nil {
			return err
		}
		if !usesLog {
			return j.compact(time.Time{})
		}
		if err := j.load(); err != nil {
			return err
		}
		if err := j.ensureCurrentExecutionLog(); err != nil {
			return err
		}
		return j.compactIfLimitsExceeded()
	})
}

func (j *JSONStorage) ensureCurrentExecutionLog() error {
	info, err := inspectJSONFile(j.executionPath)
	if err != nil {
		return err
	}
	if info != nil {
		return nil
	}
	if err := ensureExecutionLog(j.executionPath); err != nil {
		return err
	}
	if j.data == nil {
		if err := j.load(); err != nil {
			return err
		}
	}
	j.data.Statistics = emptyStorageStatistics()
	return j.save()
}

func (j *JSONStorage) pruneBackups() error {
	maxBackups := j.config.Storage.MaxBackups
	if maxBackups <= 0 {
		return nil
	}
	backups, err := j.backupFiles()
	if err != nil {
		return err
	}
	if len(backups) <= maxBackups {
		return nil
	}
	sortBackupFiles(backups)
	oldBackups := backups[:len(backups)-maxBackups]
	return j.removeBackupFiles(oldBackups)
}

func (j *JSONStorage) backupFiles() ([]backupFile, error) {
	paths, err := filepath.Glob(j.filepath + ".backup.*")
	if err != nil {
		return nil, fmt.Errorf("failed to list backup files: %w", err)
	}
	backups := make([]backupFile, 0, len(paths))
	for _, path := range paths {
		backup, ok, err := statBackupFile(path)
		if err != nil {
			return nil, err
		}
		if ok {
			backups = append(backups, backup)
		}
	}
	return backups, nil
}

func statBackupFile(path string) (backupFile, bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return backupFile{}, false, nil
	}
	if err != nil {
		return backupFile{}, false, fmt.Errorf("failed to stat backup file %s: %w", path, err)
	}
	modTime := info.ModTime()
	backup := backupFile{path: path, modTime: modTime}
	return backup, true, nil
}

func sortBackupFiles(backups []backupFile) {
	slices.SortFunc(backups, func(a, b backupFile) int {
		if order := a.modTime.Compare(b.modTime); order != 0 {
			return order
		}
		return strings.Compare(a.path, b.path)
	})
}

func (j *JSONStorage) removeBackupFiles(backups []backupFile) error {
	for _, backup := range backups {
		if err := removeBackupFile(backup.path, "old backup"); err != nil {
			return err
		}
		executionBackup := j.executionBackupPath(backup.path)
		if err := removeBackupFile(executionBackup, "old execution backup"); err != nil {
			return err
		}
	}
	return nil
}

func removeBackupFile(path, label string) error {
	err := os.Remove(path)
	removed := err == nil || os.IsNotExist(err)
	if !removed {
		return fmt.Errorf("failed to remove %s %s: %w", label, path, err)
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
		path := backupPathCandidate(base, i)
		available, err := j.backupPathAvailable(path)
		if err != nil {
			return "", err
		}
		if available {
			return path, nil
		}
	}
	return "", fmt.Errorf("failed to find available backup path after %d attempts", maxBackupPathAttempts)
}

func backupPathCandidate(base string, attempt int) string {
	if attempt == 0 {
		return base
	}
	return fmt.Sprintf("%s.%d", base, attempt)
}

func (j *JSONStorage) backupPathAvailable(path string) (bool, error) {
	manifestExists, err := pathExists(path)
	if err != nil {
		return false, fmt.Errorf("failed to stat backup path %s: %w", path, err)
	}
	executionPath := j.executionBackupPath(path)
	executionExists, err := pathExists(executionPath)
	if err != nil {
		return false, fmt.Errorf("failed to stat execution backup path %s: %w", executionPath, err)
	}
	if manifestExists {
		return false, nil
	}
	if executionExists {
		return false, nil
	}
	return true, nil
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
		return nil
	}
	if err := j.load(); err != nil {
		return err
	}
	return j.ensureCurrentExecutionLog()
}

func (j *JSONStorage) withFileLock(fn func() error) (err error) {
	lockPath := j.filepath + ".lock"
	lockFile, err := safefs.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, core.PrivateFileMode)
	if err != nil {
		return fmt.Errorf("failed to open storage lock: %w", err)
	}
	defer func() {
		err = safefs.CloseWithError(err, lockFile, "failed to close storage lock")
	}()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to lock storage: %w", err)
	}

	return fn()
}

func cleanManagedPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", ErrEmptyPath
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
