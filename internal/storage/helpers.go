package storage

import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

func newJSONStorage(config *core.Config, wait *time.Duration) (*JSONStorage, error) {
	storagePath, err := cleanManagedPath(config.Storage.JSONFile)
	if err != nil {
		return nil, fmt.Errorf("invalid storage path: %w", err)
	}

	js := &JSONStorage{
		config:         config,
		filepath:       storagePath,
		executionPath:  ExecutionLogPath(storagePath),
		marshalStorage: marshalStorageData,
		lockWait:       wait,
	}
	return js, js.Initialize(config)
}

func (j *JSONStorage) ensureStorageDirectory() error {
	dir := filepath.Dir(j.filepath)
	if err := os.MkdirAll(dir, core.OwnerDirectoryMode); err != nil {
		return fmt.Errorf("failed to create storage directory: %w", err)
	}
	return nil
}

func (j *JSONStorage) loadOrCreateStorage() error {
	exists, err := pathExists(j.filepath)
	if err != nil {
		return fmt.Errorf("failed to stat storage file: %w", err)
	}
	if !exists {
		return j.initializeEmptyStorage()
	}
	if err := j.recoverPendingStorageCommit(); err != nil {
		return err
	}
	return j.loadCurrentStorage()
}

func (j *JSONStorage) loadCurrentStorage() error {
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

func validateExecutionRecords(records []*core.ExecutionRecord) error {
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
	executionTemp, err := j.prepareAppendedExecutionLog(storedRecords)
	if err != nil {
		return err
	}
	j.applyExecutions(storedRecords)
	if err := j.commitPreparedStorage(executionTemp); err != nil {
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
		if record.ExitCode != 0 {
			return
		}
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
	if record.Tool == core.ToolUV {
		return isUVToolExecution(record)
	}
	if !isJSManager(record.Tool) {
		return true
	}
	if j.tracksLocalNPM(record.Tool) {
		return true
	}
	executable, _ := record.Metadata["executable"].(string)
	tracksExecutable := executable != "" || executionWasGlobal(record)
	return tracksExecutable
}

func isUVToolExecution(record core.ExecutionRecord) bool {
	executable, _ := record.Metadata["executable"].(string)
	subcommand, _ := record.Metadata["subcommand"].(string)
	isToolExecution := executable != "" || subcommand == "tool"
	return isToolExecution
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
	if timestamp.After(pkg.LastUsed) {
		pkg.LastUsed = timestamp
	}
	pkg.UsageCount++
	return pkg
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
	if err := j.recoverPendingStorageCommit(); err != nil {
		return err
	}
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

	if err := j.lockStorageFile(lockFile); err != nil {
		return fmt.Errorf("failed to lock storage: %w", err)
	}

	return fn()
}

func (j *JSONStorage) lockStorageFile(file *os.File) error {
	if j.lockWait == nil {
		return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
	}
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	isContention := errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
	if !isContention {
		return err
	}
	return j.waitForStorageLock(file)
}

func (j *JSONStorage) waitForStorageLock(file *os.File) error {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), *j.lockWait)
	defer cancel()
	err := lockStorageWithContext(ctx, file)
	*j.lockWait = max(0, *j.lockWait-time.Since(started))
	return err
}

func lockStorageWithContext(ctx context.Context, file *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		isContention := errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
		if !isContention {
			return err
		}
		if err := waitForStorageLockRetry(ctx); err != nil {
			return err
		}
	}
}

func waitForStorageLockRetry(ctx context.Context) error {
	timer := time.NewTimer(storageLockRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
	record.Environment = maps.Clone(record.Environment)
	record.PackagesAffected = copyStringSlice(record.PackagesAffected)
	record.Metadata = maps.Clone(record.Metadata)
	return record
}

func copyPackageValue(pkg core.PackageInfo) core.PackageInfo {
	pkg.Dependencies = copyStringSlice(pkg.Dependencies)
	return pkg
}

func copyStorageStatistics(stats core.StorageStatistics) core.StorageStatistics {
	stats.ToolsUsed = copyStringSlice(stats.ToolsUsed)
	stats.ExecutionFrequency = maps.Clone(stats.ExecutionFrequency)
	return stats
}

func copyStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

var errStopStorageScan = errors.New("stop storage scan")

func inspectCurrentJSONFile(path string) (JSONInspection, error) {
	info, err := inspectJSONFile(path)
	if err != nil {
		return JSONInspection{}, err
	}
	if info == nil {
		return JSONInspection{}, nil
	}
	inspection := JSONInspection{HasFile: true, SizeBytes: info.Size()}
	visitor := inspectionVisitor(&inspection)
	if err := scanJSONStorage(path, visitor); err != nil {
		return inspection, err
	}
	if err := inspectExecutionLogIfUsed(path, &inspection, visitor.execution); err != nil {
		return inspection, err
	}
	return inspection, nil
}

func inspectExecutionLogIfUsed(
	path string,
	inspection *JSONInspection,
	visit func(core.ExecutionRecord) error,
) error {
	usesLog, err := storageUsesExecutionLog(path)
	if err != nil {
		return err
	}
	if !usesLog {
		return nil
	}
	return inspectExecutionLog(path, inspection, visit)
}

func inspectExecutionLog(
	manifestPath string,
	inspection *JSONInspection,
	visit func(core.ExecutionRecord) error,
) error {
	inspection.ExecutionCount = 0
	inspection.LatestExecution = nil
	logPath := ExecutionLogPath(manifestPath)
	logInfo, err := inspectJSONFile(logPath)
	if err != nil {
		return err
	}
	if logInfo == nil {
		return nil
	}
	inspection.SizeBytes += logInfo.Size()
	return scanReadableNDJSONExecutions(logPath, visit)
}

func inspectJSONFile(path string) (os.FileInfo, error) {
	cleanPath, err := cleanManagedPath(path)
	if err != nil {
		return nil, err
	}
	info, err := safefs.Lstat(cleanPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("storage is not a regular file: %s", cleanPath)
	}
	return info, nil
}

func inspectionVisitor(inspection *JSONInspection) jsonStorageVisitor {
	var visitor jsonStorageVisitor
	visitor.execution = func(record core.ExecutionRecord) error {
		inspection.ExecutionCount++
		updateLatestExecution(inspection, record)
		return nil
	}
	visitor.packageInfo = func(string, string, core.PackageInfo) error {
		inspection.PackageCount++
		return nil
	}
	visitor.metadata = func(metadata core.StorageMetadata) {
		inspection.Metadata = metadata
	}
	visitor.statistics = func(statistics core.StorageStatistics) {
		inspection.Statistics = statistics
	}
	return visitor
}

func updateLatestExecution(inspection *JSONInspection, record core.ExecutionRecord) {
	if inspection.LatestExecution != nil {
		if !record.Timestamp.After(inspection.LatestExecution.Timestamp) {
			return
		}
	}
	latest := copyExecutionValue(record)
	inspection.LatestExecution = &latest
}

func scanJSONStorage(path string, visitor jsonStorageVisitor) (err error) {
	file, err := openJSONStorageFile(path)
	if err != nil {
		return err
	}
	defer func() {
		err = safefs.CloseWithError(err, file, "failed to close storage file")
	}()

	decoder := json.NewDecoder(file)
	if err := scanStorageObject(decoder, visitor); err != nil {
		return err
	}
	return ensureStorageEOF(decoder)
}

func openJSONStorageFile(path string) (*os.File, error) {
	cleanPath, err := cleanManagedPath(path)
	if err != nil {
		return nil, err
	}
	info, err := safefs.Lstat(cleanPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("storage is not a regular file: %s", cleanPath)
	}
	file, err := safefs.OpenFile(cleanPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open storage file: %w", err)
	}
	return file, nil
}

func scanStorageObject(decoder *json.Decoder, visitor jsonStorageVisitor) error {
	if err := expectJSONDelimiter(decoder, '{'); err != nil {
		return fmt.Errorf("failed to decode storage object: %w", err)
	}
	for decoder.More() {
		field, err := nextJSONField(decoder)
		if err != nil {
			return err
		}
		if err := scanStorageField(decoder, field, visitor); err != nil {
			return err
		}
	}
	return expectJSONDelimiter(decoder, '}')
}

func nextJSONField(decoder *json.Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	field, ok := token.(string)
	if !ok {
		return "", fmt.Errorf("storage field name is not a string")
	}
	return field, nil
}

func scanStorageField(decoder *json.Decoder, field string, visitor jsonStorageVisitor) error {
	switch field {
	case "version":
		return scanVersion(decoder, visitor.version)
	case "execution_log_format":
		return scanExecutionLogFormat(decoder, visitor.executionLogFormat)
	case "executions":
		return scanExecutions(decoder, visitor.execution)
	case "packages":
		return scanPackages(decoder, visitor.packageInfo)
	case "package_tombstones":
		return scanPackageTombstones(decoder, visitor.packageTombstones)
	case "metadata":
		return scanMetadata(decoder, visitor.metadata)
	case "statistics":
		return scanStatistics(decoder, visitor.statistics)
	default:
		return skipJSONValue(decoder)
	}
}

func scanExecutionLogFormat(decoder *json.Decoder, visit func(string)) error {
	if visit == nil {
		return skipJSONValue(decoder)
	}
	var format string
	if err := decoder.Decode(&format); err != nil {
		return fmt.Errorf("failed to decode execution log format: %w", err)
	}
	visit(format)
	return nil
}

func scanVersion(decoder *json.Decoder, visit func(string)) error {
	if visit == nil {
		return skipJSONValue(decoder)
	}
	var version string
	if err := decoder.Decode(&version); err != nil {
		return fmt.Errorf("failed to decode storage version: %w", err)
	}
	visit(version)
	return nil
}

func scanExecutions(decoder *json.Decoder, visit func(core.ExecutionRecord) error) error {
	if visit == nil {
		return skipJSONValue(decoder)
	}
	present, err := beginJSONCollection(decoder, '[')
	if err != nil {
		return fmt.Errorf("failed to decode executions: %w", err)
	}
	if !present {
		return nil
	}
	if err := scanExecutionRecords(decoder, visit); err != nil {
		return err
	}
	return expectJSONDelimiter(decoder, ']')
}

func scanExecutionRecords(decoder *json.Decoder, visit func(core.ExecutionRecord) error) error {
	for decoder.More() {
		var record core.ExecutionRecord
		if err := decoder.Decode(&record); err != nil {
			return fmt.Errorf("failed to decode execution: %w", err)
		}
		if err := visit(record); err != nil {
			return err
		}
	}
	return nil
}

func scanPackages(decoder *json.Decoder, visit func(string, string, core.PackageInfo) error) error {
	if visit == nil {
		return skipJSONValue(decoder)
	}
	present, err := beginJSONCollection(decoder, '{')
	if err != nil {
		return fmt.Errorf("failed to decode packages: %w", err)
	}
	if !present {
		return nil
	}
	if err := scanPackageTools(decoder, visit); err != nil {
		return err
	}
	return expectJSONDelimiter(decoder, '}')
}

func scanPackageTools(decoder *json.Decoder, visit func(string, string, core.PackageInfo) error) error {
	for decoder.More() {
		tool, err := nextJSONField(decoder)
		if err != nil {
			return err
		}
		if err := scanToolPackages(decoder, tool, visit); err != nil {
			return err
		}
	}
	return nil
}

func scanToolPackages(decoder *json.Decoder, tool string, visit func(string, string, core.PackageInfo) error) error {
	present, err := beginJSONCollection(decoder, '{')
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	for decoder.More() {
		if err := scanToolPackage(decoder, tool, visit); err != nil {
			return err
		}
	}
	return expectJSONDelimiter(decoder, '}')
}

func scanToolPackage(decoder *json.Decoder, tool string, visit func(string, string, core.PackageInfo) error) error {
	name, err := nextJSONField(decoder)
	if err != nil {
		return err
	}
	var pkg core.PackageInfo
	if err := decoder.Decode(&pkg); err != nil {
		return fmt.Errorf("failed to decode package %s/%s: %w", tool, name, err)
	}
	if err := visit(tool, name, pkg); err != nil {
		return err
	}
	return nil
}

func scanMetadata(decoder *json.Decoder, visit func(core.StorageMetadata)) error {
	if visit == nil {
		return skipJSONValue(decoder)
	}
	var metadata core.StorageMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return fmt.Errorf("failed to decode storage metadata: %w", err)
	}
	visit(metadata)
	return nil
}

func scanPackageTombstones(decoder *json.Decoder, visit func(map[string]map[string]int64)) error {
	if visit == nil {
		return skipJSONValue(decoder)
	}
	var tombstones map[string]map[string]int64
	if err := decoder.Decode(&tombstones); err != nil {
		return fmt.Errorf("failed to decode package tombstones: %w", err)
	}
	visit(tombstones)
	return nil
}

func scanStatistics(decoder *json.Decoder, visit func(core.StorageStatistics)) error {
	if visit == nil {
		return skipJSONValue(decoder)
	}
	var statistics core.StorageStatistics
	if err := decoder.Decode(&statistics); err != nil {
		return fmt.Errorf("failed to decode storage statistics: %w", err)
	}
	visit(statistics)
	return nil
}

func expectJSONDelimiter(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != expected {
		return fmt.Errorf("expected %q, got %q", expected, token)
	}
	return nil
}

func beginJSONCollection(decoder *json.Decoder, expected json.Delim) (bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	if token == nil {
		return false, nil
	}
	if token != expected {
		return false, fmt.Errorf("expected %q, got %q", expected, token)
	}
	return true, nil
}

func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delimiter == '{' {
		return skipJSONObject(decoder)
	}
	if delimiter == '[' {
		return skipJSONArray(decoder)
	}
	return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
}

func skipJSONObject(decoder *json.Decoder) error {
	for decoder.More() {
		if _, err := nextJSONField(decoder); err != nil {
			return err
		}
		if err := skipJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectJSONDelimiter(decoder, '}')
}

func skipJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := skipJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectJSONDelimiter(decoder, ']')
}

func ensureStorageEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("storage contains trailing JSON data")
}

func storageUsesExecutionLog(path string) (bool, error) {
	format, found, err := readExecutionLogFormat(path)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if format == "" {
		return false, nil
	}
	if format != executionLogFormat {
		return false, fmt.Errorf("%w: %s", ErrUnsupportedExecutionLogFormat, format)
	}
	return true, nil
}

func readExecutionLogFormat(path string) (format string, found bool, err error) {
	file, err := openJSONStorageFile(path)
	if err != nil {
		return "", false, err
	}
	defer func() {
		err = safefs.CloseWithError(err, file, "failed to close storage file")
	}()
	decoder := json.NewDecoder(file)
	if err := expectJSONDelimiter(decoder, '{'); err != nil {
		return "", false, fmt.Errorf("failed to decode storage object: %w", err)
	}
	return decodeExecutionLogFormat(decoder)
}

func decodeExecutionLogFormat(decoder *json.Decoder) (format string, found bool, err error) {
	for decoder.More() {
		field, err := nextJSONField(decoder)
		if err != nil {
			return "", false, err
		}
		if field == "execution_log_format" {
			format, err := decodeExecutionLogFormatValue(decoder)
			return format, true, err
		}
		if err := skipJSONValue(decoder); err != nil {
			return "", false, err
		}
	}
	return "", false, nil
}

func decodeExecutionLogFormatValue(decoder *json.Decoder) (string, error) {
	var format string
	if err := decoder.Decode(&format); err != nil {
		return "", fmt.Errorf("failed to decode execution log format: %w", err)
	}
	return format, nil
}

func scanNDJSONExecutions(path string, visit func(core.ExecutionRecord) error) (err error) {
	tolerateTail := false
	return scanNDJSONExecutionsWithTail(path, visit, tolerateTail)
}

func scanReadableNDJSONExecutions(path string, visit func(core.ExecutionRecord) error) (err error) {
	tolerateTail := true
	return scanNDJSONExecutionsWithTail(path, visit, tolerateTail)
}

func scanNDJSONExecutionsWithTail(
	path string,
	visit func(core.ExecutionRecord) error,
	shouldToleratePartialTail bool,
) (err error) {
	file, err := openJSONStorageFile(path)
	if err != nil {
		return err
	}
	defer func() {
		err = safefs.CloseWithError(err, file, "failed to close execution log")
	}()
	reader := bufio.NewReader(file)
	return scanNDJSONReader(reader, shouldToleratePartialTail, visit)
}

func scanNDJSONReader(reader *bufio.Reader, shouldToleratePartialTail bool, visit func(core.ExecutionRecord) error) error {
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			isComplete := line[len(line)-1] == '\n'
			if err := scanNDJSONExecutionLine(line, isComplete, shouldToleratePartialTail, visit); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("failed to read execution log: %w", readErr)
		}
	}
}

func scanNDJSONExecutionLine(
	line []byte,
	isComplete bool,
	shouldToleratePartialTail bool,
	visit func(core.ExecutionRecord) error,
) error {
	if shouldIgnorePartialTail(isComplete, shouldToleratePartialTail) {
		return nil
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	if err := rejectIncompleteNDJSONLine(isComplete, shouldToleratePartialTail); err != nil {
		return err
	}
	return decodeAndVisitNDJSONExecution(line, visit)
}

func shouldIgnorePartialTail(isComplete bool, shouldToleratePartialTail bool) bool {
	if isComplete {
		return false
	}
	return shouldToleratePartialTail
}

func decodeAndVisitNDJSONExecution(line []byte, visit func(core.ExecutionRecord) error) error {
	record, err := decodeNDJSONExecution(line)
	if err != nil {
		return err
	}
	return visitExecutionRecord(record, visit)
}

func visitExecutionRecord(record core.ExecutionRecord, visit func(core.ExecutionRecord) error) error {
	if visit == nil {
		return nil
	}
	return visit(record)
}

func rejectIncompleteNDJSONLine(isComplete bool, shouldToleratePartialTail bool) error {
	if isComplete {
		return nil
	}
	if shouldToleratePartialTail {
		return nil
	}
	return fmt.Errorf("failed to decode execution log: %w", io.ErrUnexpectedEOF)
}

func decodeNDJSONExecution(line []byte) (core.ExecutionRecord, error) {
	var record core.ExecutionRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return core.ExecutionRecord{}, fmt.Errorf("failed to decode execution log: %w", err)
	}
	return record, nil
}

func (j *JSONStorage) scanExecutionRecords(visit func(core.ExecutionRecord) error) error {
	usesLog, err := storageUsesExecutionLog(j.filepath)
	if err != nil {
		return err
	}
	if usesLog {
		return scanReadableNDJSONExecutions(j.executionPath, visit)
	}
	var visitor jsonStorageVisitor
	visitor.execution = visit
	return scanJSONStorage(j.filepath, visitor)
}

func (j *JSONStorage) withConsistentRead(fn func() error) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.withFileLock(func() error {
		if err := j.recoverPendingStorageCommit(); err != nil {
			return err
		}
		return fn()
	})
}

func (j *JSONStorage) executionSeq() iter.Seq2[core.ExecutionRecord, error] {
	return func(yield func(core.ExecutionRecord, error) bool) {
		err := j.scanExecutionRecords(func(record core.ExecutionRecord) error {
			if !yield(record, nil) {
				return errStopStorageScan
			}
			return nil
		})
		if err == nil {
			return
		}
		if errors.Is(err, errStopStorageScan) {
			return
		}
		yield(core.ExecutionRecord{}, err)
	}
}

func findPackageVisitor(tool, name string, found **core.PackageInfo) func(string, string, core.PackageInfo) error {
	return func(packageTool, packageName string, pkg core.PackageInfo) error {
		if packageTool != tool {
			return nil
		}
		if packageName != name {
			return nil
		}
		copy := copyPackageValue(pkg)
		*found = &copy
		return errStopStorageScan
	}
}

func shouldReturnPackageScanError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, errStopStorageScan)
}

func collectPackagesVisitor(tool string, packages *[]*core.PackageInfo) func(string, string, core.PackageInfo) error {
	return func(packageTool, _ string, pkg core.PackageInfo) error {
		filterMismatch := tool != "" && packageTool != tool
		if filterMismatch {
			return nil
		}
		copy := copyPackageValue(pkg)
		*packages = append(*packages, &copy)
		return nil
	}
}

func collectAllPackagesVisitor(packages map[string]map[string]*core.PackageInfo) func(string, string, core.PackageInfo) error {
	return func(tool, name string, pkg core.PackageInfo) error {
		if packages[tool] == nil {
			packages[tool] = make(map[string]*core.PackageInfo)
		}
		copy := copyPackageValue(pkg)
		packages[tool][name] = &copy
		return nil
	}
}

func newExecutionCollector(opts QueryOptions) *executionCollector {
	limit := 0
	if opts.Limit > 0 {
		limit = opts.Limit + max(opts.Offset, 0)
	}
	collector := &executionCollector{opts: opts, limit: limit}
	heap.Init(&collector.records)
	return collector
}

func (c *executionCollector) add(record core.ExecutionRecord) {
	if !executionMatches(record, c.opts) {
		return
	}
	copy := copyExecutionValue(record)
	if c.limit == 0 {
		c.allRecords = append(c.allRecords, &copy)
		return
	}
	if c.records.Len() < c.limit {
		heap.Push(&c.records, &copy)
		return
	}
	if copy.Timestamp.After(c.records[0].Timestamp) {
		heap.Pop(&c.records)
		heap.Push(&c.records, &copy)
	}
}

func (c *executionCollector) results() []*core.ExecutionRecord {
	results := c.allRecords
	if c.limit > 0 {
		results = append([]*core.ExecutionRecord(nil), c.records...)
	}
	slices.SortFunc(results, func(a, b *core.ExecutionRecord) int {
		return b.Timestamp.Compare(a.Timestamp)
	})
	offset := min(max(c.opts.Offset, 0), len(results))
	results = results[offset:]
	return limitExecutionResults(results, c.opts.Limit)
}

func limitExecutionResults(results []*core.ExecutionRecord, limit int) []*core.ExecutionRecord {
	if limit <= 0 {
		return results
	}
	if len(results) <= limit {
		return results
	}
	return results[:limit]
}

func executionMatches(record core.ExecutionRecord, opts QueryOptions) bool {
	if !executionToolMatches(record, opts.Tool) {
		return false
	}
	if !executionPackageMatches(record, opts.Package) {
		return false
	}
	if !executionSinceMatches(record, opts.Since) {
		return false
	}
	return executionUntilMatches(record, opts.Until)
}

func executionToolMatches(record core.ExecutionRecord, tool string) bool {
	if tool == "" {
		return true
	}
	return record.Tool == tool
}

func executionPackageMatches(record core.ExecutionRecord, pkg string) bool {
	if pkg == "" {
		return true
	}
	return slices.Contains(record.PackagesAffected, pkg)
}

func executionSinceMatches(record core.ExecutionRecord, since *time.Time) bool {
	if since == nil {
		return true
	}
	return !record.Timestamp.Before(*since)
}

func executionUntilMatches(record core.ExecutionRecord, until *time.Time) bool {
	if until == nil {
		return true
	}
	return !record.Timestamp.After(*until)
}

func (h executionMinHeap) Len() int { return len(h) }

func (h executionMinHeap) Less(i, j int) bool { return h[i].Timestamp.Before(h[j].Timestamp) }

func (h executionMinHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *executionMinHeap) Push(value any) {
	*h = append(*h, value.(*core.ExecutionRecord))
}

func (h *executionMinHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func (h compactExecutionHeap) Len() int { return len(h) }

func (h compactExecutionHeap) Less(i, j int) bool { return h[i].timestamp.Before(h[j].timestamp) }

func (h compactExecutionHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *compactExecutionHeap) Push(value any) {
	*h = append(*h, value.(compactExecution))
}

func (h *compactExecutionHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func newCompactionCollector(config *core.Config, before time.Time) *compactionCollector {
	cutoff := compactionCutoff(config.Storage.RetentionDays, before)
	maxBytes := compactedLimit(config.Storage.MaxStorageBytes, byteHeadroomThreshold)
	maxRecords := compactedRecordLimit(config.Storage.MaxExecutions)
	collector := &compactionCollector{
		maxBytes:   maxBytes,
		maxRecords: maxRecords,
		cutoff:     cutoff,
	}
	heap.Init(&collector.records)
	return collector
}

func newUnboundedCompactionCollector() *compactionCollector {
	collector := &compactionCollector{}
	heap.Init(&collector.records)
	return collector
}

func compactionCutoff(retentionDays int, before time.Time) time.Time {
	hasExplicitCutoff := !before.IsZero()
	retentionDisabled := retentionDays <= 0
	useProvidedCutoff := hasExplicitCutoff || retentionDisabled
	if useProvidedCutoff {
		return before
	}
	return time.Now().AddDate(0, 0, -retentionDays)
}

func compactedLimit(limit, threshold int64) int64 {
	if limit <= threshold {
		return limit
	}
	headroom := limit / compactionHeadroomDivisor
	compacted := limit - headroom
	return compacted
}

func compactedRecordLimit(limit int) int {
	return int(compactedLimit(int64(limit), recordHeadroomThreshold))
}

func (c *compactionCollector) add(record core.ExecutionRecord) error {
	hasCutoff := !c.cutoff.IsZero()
	withinRetention := !hasCutoff || record.Timestamp.After(c.cutoff)
	if !withinRetention {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal execution during compaction: %w", err)
	}
	item := compactExecution{
		timestamp: record.Timestamp,
		tool:      record.Tool,
		data:      data,
	}
	heap.Push(&c.records, item)
	c.totalBytes += compactExecutionSize(item)
	c.prune()
	return nil
}

func (c *compactionCollector) prune() {
	for c.exceedsLimits() {
		removed := heap.Pop(&c.records).(compactExecution)
		c.totalBytes -= compactExecutionSize(removed)
	}
}

func (c *compactionCollector) exceedsLimits() bool {
	tooMany := c.maxRecords > 0 && c.records.Len() > c.maxRecords
	tooLarge := c.maxBytes > 0 && c.totalBytes > c.maxBytes
	return tooMany || tooLarge
}

func (c *compactionCollector) sortedRecords() []compactExecution {
	records := append([]compactExecution(nil), c.records...)
	slices.SortFunc(records, func(a, b compactExecution) int {
		return a.timestamp.Compare(b.timestamp)
	})
	return records
}

func compactExecutionSize(record compactExecution) int64 {
	return int64(len(record.data) + 1)
}

func (j *JSONStorage) compact(before time.Time) error {
	state, records, err := j.collectCompaction(before)
	if err != nil {
		return err
	}
	executionTemp, err := prepareCompactedStorage(j.executionPath, records)
	if err != nil {
		return err
	}
	statistics := compactedStatistics(records)
	manifest := state.manifest(statistics)
	j.data = manifest
	return j.commitPreparedStorage(executionTemp)
}

func (j *JSONStorage) collectCompaction(before time.Time) (compactStorageState, []compactExecution, error) {
	state := newCompactStorageState()
	collector := newCompactionCollector(j.config, before)
	visitor := compactionVisitor(&state, collector)
	usesLog, err := storageUsesExecutionLog(j.filepath)
	if err != nil {
		return state, nil, err
	}
	if usesLog {
		err = j.collectCurrentStorage(visitor, collector)
	} else {
		err = scanJSONStorage(j.filepath, visitor)
	}
	if err != nil {
		return state, nil, fmt.Errorf("failed to scan storage for compaction: %w", err)
	}
	records := collector.sortedRecords()
	return state, records, nil
}

func (j *JSONStorage) collectCurrentStorage(
	visitor jsonStorageVisitor,
	collector *compactionCollector,
) error {
	visitor.execution = nil
	if err := scanJSONStorage(j.filepath, visitor); err != nil {
		return err
	}
	if err := j.ensureCurrentExecutionLog(); err != nil {
		return err
	}
	return scanNDJSONExecutions(j.executionPath, collector.add)
}

func (j *JSONStorage) collectLegacyCompaction(path string) (compactStorageState, []compactExecution, error) {
	state := newCompactStorageState()
	collector := newUnboundedCompactionCollector()
	visitor := compactionVisitor(&state, collector)
	if err := scanJSONStorage(path, visitor); err != nil {
		return state, nil, fmt.Errorf("failed to scan legacy storage: %w", err)
	}
	records := collector.sortedRecords()
	return state, records, nil
}

func (j *JSONStorage) compactIfLimitsExceeded() error {
	info, err := inspectJSONFile(j.executionPath)
	if err != nil {
		return err
	}
	if info == nil {
		return fmt.Errorf("%w: %s", ErrExecutionLogNotFound, j.executionPath)
	}
	exceedsBytes := j.executionBytesExceeded(info.Size())
	exceedsRecords := j.executionCountExceeded()
	withinLimits := !exceedsBytes && !exceedsRecords
	if withinLimits {
		return nil
	}
	return j.compact(time.Time{})
}

func (j *JSONStorage) executionBytesExceeded(size int64) bool {
	limit := j.config.Storage.MaxStorageBytes
	hasLimit := limit > 0
	exceeded := size > limit
	shouldCompact := hasLimit && exceeded
	return shouldCompact
}

func (j *JSONStorage) executionCountExceeded() bool {
	limit := j.config.Storage.MaxExecutions
	hasLimit := limit > 0
	exceeded := j.data.Statistics.TotalExecutions > limit
	shouldCompact := hasLimit && exceeded
	return shouldCompact
}

func newCompactStorageState() compactStorageState {
	packages := make(map[string]map[string]core.PackageInfo)
	state := compactStorageState{
		version:  "1.0.0",
		packages: packages,
	}
	return state
}

func (s compactStorageState) manifest(statistics core.StorageStatistics) *core.StorageData {
	s.metadata.LastUpdated = time.Now()
	manifest := &core.StorageData{
		Version:            s.version,
		ExecutionLogFormat: executionLogFormat,
		Metadata:           s.metadata,
		Packages:           s.packages,
		PackageTombstones:  s.tombstones,
		Statistics:         statistics,
	}
	return manifest
}

func compactionVisitor(state *compactStorageState, collector *compactionCollector) jsonStorageVisitor {
	var visitor jsonStorageVisitor
	visitor.version = func(version string) { state.version = version }
	visitor.metadata = func(metadata core.StorageMetadata) { state.metadata = metadata }
	visitor.execution = collector.add
	visitor.packageInfo = compactPackageVisitor(state.packages)
	visitor.packageTombstones = func(tombstones map[string]map[string]int64) {
		state.tombstones = tombstones
	}
	return visitor
}

func compactPackageVisitor(packages map[string]map[string]core.PackageInfo) func(string, string, core.PackageInfo) error {
	return func(tool, name string, pkg core.PackageInfo) error {
		if packages[tool] == nil {
			packages[tool] = make(map[string]core.PackageInfo)
		}
		packages[tool][name] = pkg
		return nil
	}
}

func compactedStatistics(records []compactExecution) core.StorageStatistics {
	frequency := make(map[string]int)
	statistics := core.StorageStatistics{
		TotalExecutions:    len(records),
		ToolsUsed:          []string{},
		ExecutionFrequency: frequency,
	}
	dayCount := make(map[string]int)
	for _, record := range records {
		updateCompactedStatistics(&statistics, dayCount, record)
	}
	statistics.MostActiveDay = mostActiveDay(dayCount)
	return statistics
}

func mostActiveDay(dayCount map[string]int) string {
	var bestDay string
	var bestCount int
	isFirst := true
	for day, count := range dayCount {
		if shouldReplaceMostActiveDay(isFirst, day, count, bestDay, bestCount) {
			bestDay, bestCount, isFirst = day, count, false
		}
	}
	return bestDay
}

func shouldReplaceMostActiveDay(isFirst bool, day string, count int, bestDay string, bestCount int) bool {
	if isFirst {
		return true
	}
	if count > bestCount {
		return true
	}
	if count != bestCount {
		return false
	}
	return day < bestDay
}

func updateCompactedStatistics(
	statistics *core.StorageStatistics,
	dayCount map[string]int,
	record compactExecution,
) {
	if record.tool != "" {
		if statistics.ExecutionFrequency[record.tool] == 0 {
			statistics.ToolsUsed = append(statistics.ToolsUsed, record.tool)
		}
		statistics.ExecutionFrequency[record.tool]++
	}
	if !record.timestamp.IsZero() {
		day := record.timestamp.Format("2006-01-02")
		dayCount[day]++
	}
}

func prepareCompactedStorage(path string, records []compactExecution) (string, error) {
	file, tempPath, err := createCompactionFile(path)
	if err != nil {
		return "", err
	}
	if err := writeCompactionFile(file, records); err != nil {
		discardTempFile(file, tempPath)
		return "", err
	}
	if err := closePreparedFile(file, tempPath); err != nil {
		return "", err
	}
	return tempPath, nil
}

func createCompactionFile(path string) (*os.File, string, error) {
	pattern := "." + filepath.Base(path) + ".compact-*"
	file, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create compacted storage: %w", err)
	}
	if err := file.Chmod(core.PrivateFileMode); err != nil {
		discardTempFile(file, file.Name())
		return nil, "", fmt.Errorf("failed to secure compacted storage: %w", err)
	}
	return file, file.Name(), nil
}

func writeCompactionFile(file *os.File, records []compactExecution) error {
	writer := bufio.NewWriter(file)
	for _, record := range records {
		if _, err := writer.Write(record.data); err != nil {
			return fmt.Errorf("failed to write compacted storage: %w", err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			return fmt.Errorf("failed to write compacted storage: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush compacted storage: %w", err)
	}
	return nil
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
		return err
	}
	return j.removeStorageCommitJournal(commit)
}

func (j *JSONStorage) prepareStorageCommit(executionTemp string) (storageCommitJournal, error) {
	manifestTemp, err := j.prepareManifestTemp()
	if err != nil {
		return storageCommitJournal{}, err
	}
	return storageCommitJournal{manifestTemp, executionTemp}, nil
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
}

func (j *JSONStorage) storageCommitJournalPath() string {
	return j.filepath + storageCommitJournalSuffix
}

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
		err = safefs.CloseWithError(err, input, "")
	}()
	output, err := safefs.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, core.PrivateFileMode)
	if err != nil {
		return err
	}
	defer func() {
		err = safefs.CloseWithError(err, output, "")
		if err != nil {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return nil
}

func copyManagedFileToOpenFile(source string, destination *os.File) (err error) {
	input, err := safefs.OpenFile(source, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() {
		err = safefs.CloseWithError(err, input, "")
	}()
	if _, err := io.Copy(destination, input); err != nil {
		return err
	}
	return nil
}

func (j *JSONStorage) restoreBackup(path string) error {
	restored, err := readStorageBackup(path)
	if err != nil {
		return err
	}
	if restored.ExecutionLogFormat == "" {
		return j.restoreLegacyBackup(path)
	}
	if restored.ExecutionLogFormat != executionLogFormat {
		return fmt.Errorf("%w: %s", ErrUnsupportedExecutionLogFormat, restored.ExecutionLogFormat)
	}
	j.data = restored
	return j.restoreExecutionBackup(path)
}

func readStorageBackup(path string) (*core.StorageData, error) {
	data, err := readManagedFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read restore file: %w", err)
	}
	var restored core.StorageData
	if err := json.Unmarshal(data, &restored); err != nil {
		return nil, fmt.Errorf("failed to unmarshal restore data: %w", err)
	}
	return &restored, nil
}

func (j *JSONStorage) restoreExecutionBackup(path string) error {
	executionBackup := j.executionBackupPath(path)
	executionTemp, err := prepareExecutionLogCopy(executionBackup)
	if err != nil {
		return fmt.Errorf("failed to restore execution log: %w", err)
	}
	return j.commitPreparedStorage(executionTemp)
}

func (j *JSONStorage) restoreLegacyBackup(path string) error {
	state, records, err := j.collectLegacyCompaction(path)
	if err != nil {
		return err
	}
	executionTemp, err := prepareCompactedStorage(j.executionPath, records)
	if err != nil {
		return err
	}
	statistics := compactedStatistics(records)
	j.data = state.manifest(statistics)
	return j.commitPreparedStorage(executionTemp)
}

func validateExecutionLog(path string) error {
	return scanNDJSONExecutions(path, nil)
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
