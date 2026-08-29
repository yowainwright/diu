package storage

import (
	"time"

	"github.com/yowainwright/diu/internal/core"
)

type Storage interface {
	Initialize(config *core.Config) error
	Close() error

	AddExecution(record *core.ExecutionRecord) error
	AddExecutions(records []*core.ExecutionRecord) error
	GetExecutions(opts QueryOptions) ([]*core.ExecutionRecord, error)
	GetExecutionByID(id string) (*core.ExecutionRecord, error)

	UpdatePackage(pkg *core.PackageInfo) error
	GetPackage(tool, name string) (*core.PackageInfo, error)
	GetPackages(tool string) ([]*core.PackageInfo, error)
	AllPackages() (map[string]map[string]*core.PackageInfo, error)
	DeletePackage(tool, name string) error

	Statistics() (*core.StorageStatistics, error)
	UpdateStatistics() error

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
