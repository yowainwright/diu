package storage

import (
	"sync"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

type Storage interface {
	Initialize(config *core.Config) error
	Close() error

	AddExecution(record *core.ExecutionRecord) error
	AddExecutions(records []*core.ExecutionRecord) error
	GetExecutions(opts QueryOptions) ([]*core.ExecutionRecord, error)

	UpdatePackage(pkg *core.PackageInfo) error
	GetPackage(tool, name string) (*core.PackageInfo, error)
	GetPackages(tool string) ([]*core.PackageInfo, error)
	AllPackages() (map[string]map[string]*core.PackageInfo, error)
	DeletePackage(tool, name string) error

	Statistics() (*core.StorageStatistics, error)

	Prepare() error
	Backup() error
	Restore(path string) error
	Cleanup(before time.Time) error
}

type QueryOptions struct {
	Tool      string
	Package   string
	Since     *time.Time
	Until     *time.Time
	Limit     int
	Offset    int
	SortBy    string
	SortOrder string
}

type JSONStorage struct {
	config         *core.Config
	filepath       string
	executionPath  string
	data           *core.StorageData
	marshalStorage func(*core.StorageData) ([]byte, error)
	lockWait       *time.Duration
	mu             sync.RWMutex
}

type backupFile struct {
	path    string
	modTime time.Time
}

type JSONInspection struct {
	HasFile         bool
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

type executionCollector struct {
	opts       QueryOptions
	limit      int
	records    executionMinHeap
	allRecords []*core.ExecutionRecord
}

type executionMinHeap []*core.ExecutionRecord

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

type compactionCollector struct {
	records    compactExecutionHeap
	totalBytes int64
	maxBytes   int64
	maxRecords int
	cutoff     time.Time
}

type storageCommitJournal struct {
	ManifestTemp  string `json:"manifest_temp"`
	ExecutionTemp string `json:"execution_temp"`
}
