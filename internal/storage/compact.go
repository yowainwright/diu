package storage

import (
	"bufio"
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/fn"
)

const (
	compactionHeadroomDivisor = 10
	byteHeadroomThreshold     = 1 << 20
	recordHeadroomThreshold   = 1000
)

type compactStorageState struct {
	version    string
	metadata   core.StorageMetadata
	packages   map[string]map[string]core.PackageInfo
	tombstones map[string]map[string]int64
}

type compactExecution struct {
	timestamp time.Time
	tool      string
	data      []byte
}

type compactExecutionHeap []compactExecution

func (h compactExecutionHeap) Len() int           { return len(h) }
func (h compactExecutionHeap) Less(i, j int) bool { return h[i].timestamp.Before(h[j].timestamp) }
func (h compactExecutionHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *compactExecutionHeap) Push(value any) {
	*h = append(*h, value.(compactExecution))
}

func (h *compactExecutionHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

type compactionCollector struct {
	records    compactExecutionHeap
	totalBytes int64
	maxBytes   int64
	maxRecords int
	cutoff     time.Time
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
	if !before.IsZero() || retentionDays <= 0 {
		return before
	}
	return time.Now().AddDate(0, 0, -retentionDays)
}

func compactedLimit(limit, threshold int64) int64 {
	if limit <= threshold {
		return limit
	}
	return limit - limit/compactionHeadroomDivisor
}

func compactedRecordLimit(limit int) int {
	return int(compactedLimit(int64(limit), recordHeadroomThreshold))
}

func (c *compactionCollector) add(record core.ExecutionRecord) error {
	if !c.cutoff.IsZero() && !record.Timestamp.After(c.cutoff) {
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
	if err := writeCompactedStorage(j.executionPath, records); err != nil {
		return err
	}
	statistics := compactedStatistics(records)
	manifest := state.manifest(statistics)
	j.data = manifest
	return j.save()
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
	if !exceedsBytes && !exceedsRecords {
		return nil
	}
	return j.compact(time.Time{})
}

func (j *JSONStorage) compactIfNeeded() error {
	exceedsRetention, err := j.executionRetentionExceeded()
	if err != nil {
		return err
	}
	if exceedsRetention {
		return j.compact(time.Time{})
	}
	return j.compactIfLimitsExceeded()
}

func (j *JSONStorage) executionRetentionExceeded() (bool, error) {
	cutoff := compactionCutoff(j.config.Storage.RetentionDays, time.Time{})
	if cutoff.IsZero() {
		return false, nil
	}
	err := scanNDJSONExecutions(j.executionPath, func(record core.ExecutionRecord) error {
		if !record.Timestamp.After(cutoff) {
			return errStopStorageScan
		}
		return nil
	})
	if errors.Is(err, errStopStorageScan) {
		return true, nil
	}
	return false, err
}

func (j *JSONStorage) executionBytesExceeded(size int64) bool {
	limit := j.config.Storage.MaxStorageBytes
	return limit > 0 && size > limit
}

func (j *JSONStorage) executionCountExceeded() bool {
	limit := j.config.Storage.MaxExecutions
	return limit > 0 && j.data.Statistics.TotalExecutions > limit
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
	statistics.MostActiveDay = fn.MaxValueKey(dayCount)
	return statistics
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

func writeCompactedStorage(path string, records []compactExecution) error {
	file, tempPath, err := createCompactionFile(path)
	if err != nil {
		return err
	}
	if err := commitCompactedStorage(file, tempPath, path, records); err != nil {
		discardTempFile(file, tempPath)
		return err
	}
	return nil
}

func commitCompactedStorage(file *os.File, tempPath, path string, records []compactExecution) error {
	if err := writeCompactionFile(file, records); err != nil {
		return err
	}
	return commitTempFile(file, tempPath, path)
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
