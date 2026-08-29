package storage

import (
	"bufio"
	"bytes"
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"slices"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/safefs"
)

type JSONInspection struct {
	Exists          bool
	SizeBytes       int64
	Metadata        core.StorageMetadata
	ExecutionCount  int
	PackageCount    int
	Statistics      core.StorageStatistics
	LatestExecution *core.ExecutionRecord
}

type ExecutionSummary struct {
	Total      int
	ToolCounts map[string]int
}

type jsonStorageVisitor struct {
	version            func(string)
	executionLogFormat func(string)
	execution          func(core.ExecutionRecord) error
	packageInfo        func(string, string, core.PackageInfo) error
	packageTombstones  func(map[string]map[string]int64)
	metadata           func(core.StorageMetadata)
	statistics         func(core.StorageStatistics)
}

var errStopStorageScan = errors.New("stop storage scan")

func InspectJSONFile(path string) (JSONInspection, error) {
	info, err := inspectJSONFile(path)
	cannotInspect := err != nil || info == nil
	if cannotInspect {
		return JSONInspection{}, err
	}

	inspection := JSONInspection{Exists: true, SizeBytes: info.Size()}
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
	allowPartialTail bool,
) (err error) {
	file, err := openJSONStorageFile(path)
	if err != nil {
		return err
	}
	defer func() {
		err = safefs.CloseWithError(err, file, "failed to close execution log")
	}()
	reader := bufio.NewReader(file)
	return scanNDJSONReader(reader, allowPartialTail, visit)
}

func scanNDJSONReader(reader *bufio.Reader, allowPartialTail bool, visit func(core.ExecutionRecord) error) error {
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			complete := line[len(line)-1] == '\n'
			if err := scanNDJSONExecutionLine(line, complete, allowPartialTail, visit); err != nil {
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
	complete bool,
	allowPartialTail bool,
	visit func(core.ExecutionRecord) error,
) error {
	if shouldIgnorePartialTail(complete, allowPartialTail) {
		return nil
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	if err := rejectIncompleteNDJSONLine(complete, allowPartialTail); err != nil {
		return err
	}
	return decodeAndVisitNDJSONExecution(line, visit)
}

func shouldIgnorePartialTail(complete bool, allowPartialTail bool) bool {
	if complete {
		return false
	}
	return allowPartialTail
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

func rejectIncompleteNDJSONLine(complete bool, allowPartialTail bool) error {
	if complete {
		return nil
	}
	if allowPartialTail {
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

func (j *JSONStorage) Executions() iter.Seq2[core.ExecutionRecord, error] {
	return func(yield func(core.ExecutionRecord, error) bool) {
		j.mu.RLock()
		defer j.mu.RUnlock()
		j.executionSeq()(yield)
	}
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

func (j *JSONStorage) calculateExecutionStatistics() (core.StorageStatistics, error) {
	statistics := core.StorageStatistics{
		ToolsUsed:          []string{},
		ExecutionFrequency: make(map[string]int),
	}
	dayCount := make(map[string]int)
	for record, err := range j.executionSeq() {
		if err != nil {
			return statistics, err
		}
		statistics.TotalExecutions++
		item := compactExecution{timestamp: record.Timestamp, tool: record.Tool}
		updateCompactedStatistics(&statistics, dayCount, item)
	}
	statistics.MostActiveDay = mostActiveDay(dayCount)
	return statistics, nil
}

func (j *JSONStorage) GetExecutions(opts QueryOptions) ([]*core.ExecutionRecord, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	collector := newExecutionCollector(opts)
	for record, err := range j.executionSeq() {
		if err != nil {
			return nil, err
		}
		collector.add(record)
	}
	return collector.results(), nil
}

func (j *JSONStorage) GetExecutionByID(id string) (*core.ExecutionRecord, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	for record, err := range j.executionSeq() {
		if err != nil {
			return nil, err
		}
		if record.ID != id {
			continue
		}
		found := copyExecutionValue(record)
		return &found, nil
	}
	return nil, fmt.Errorf("execution not found: %s", id)
}

func (j *JSONStorage) GetPackage(tool, name string) (*core.PackageInfo, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	var found *core.PackageInfo
	visit := findPackageVisitor(tool, name, &found)
	var visitor jsonStorageVisitor
	visitor.packageInfo = visit
	err := scanJSONStorage(j.filepath, visitor)
	if shouldReturnPackageScanError(err) {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("package not found: %s/%s", tool, name)
	}
	return found, nil
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

func (j *JSONStorage) GetPackages(tool string) ([]*core.PackageInfo, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	var packages []*core.PackageInfo
	visit := collectPackagesVisitor(tool, &packages)
	var visitor jsonStorageVisitor
	visitor.packageInfo = visit
	err := scanJSONStorage(j.filepath, visitor)
	return packages, err
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

func (j *JSONStorage) AllPackages() (map[string]map[string]*core.PackageInfo, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	packages := make(map[string]map[string]*core.PackageInfo)
	visit := collectAllPackagesVisitor(packages)
	var visitor jsonStorageVisitor
	visitor.packageInfo = visit
	err := scanJSONStorage(j.filepath, visitor)
	return packages, err
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

func (j *JSONStorage) Statistics() (*core.StorageStatistics, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	var found *core.StorageStatistics
	visit := func(statistics core.StorageStatistics) {
		copy := copyStorageStatistics(statistics)
		found = &copy
	}
	var visitor jsonStorageVisitor
	visitor.statistics = visit
	if err := scanJSONStorage(j.filepath, visitor); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("storage statistics not found")
	}
	return found, nil
}

func (j *JSONStorage) SummarizeExecutions(opts QueryOptions) (ExecutionSummary, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()

	toolCounts := make(map[string]int)
	summary := ExecutionSummary{ToolCounts: toolCounts}
	for record, err := range j.executionSeq() {
		if err != nil {
			return summary, err
		}
		if executionMatches(record, opts) {
			summary.Total++
			summary.ToolCounts[record.Tool]++
		}
	}
	return summary, nil
}

type executionCollector struct {
	opts       QueryOptions
	limit      int
	records    executionMinHeap
	allRecords []*core.ExecutionRecord
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

type executionMinHeap []*core.ExecutionRecord

func (h executionMinHeap) Len() int           { return len(h) }
func (h executionMinHeap) Less(i, j int) bool { return h[i].Timestamp.Before(h[j].Timestamp) }
func (h executionMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *executionMinHeap) Push(value any) {
	*h = append(*h, value.(*core.ExecutionRecord))
}

func (h *executionMinHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}
