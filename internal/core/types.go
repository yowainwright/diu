package core

import (
	"time"
)

type ExecutionRecord struct {
	ID               string                 `json:"id"`
	Tool             string                 `json:"tool"`
	Command          string                 `json:"command"`
	Args             []string               `json:"args"`
	Timestamp        time.Time              `json:"timestamp"`
	Duration         time.Duration          `json:"duration_ms"`
	ExitCode         int                    `json:"exit_code"`
	WorkingDir       string                 `json:"working_dir"`
	User             string                 `json:"user"`
	Environment      map[string]string      `json:"environment,omitempty"`
	PackagesAffected []string               `json:"packages_affected,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

type PackageInfo struct {
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	Tool         string    `json:"tool"`
	InstallDate  time.Time `json:"install_date"`
	LastUsed     time.Time `json:"last_used"`
	UsageCount   int       `json:"usage_count"`
	Path         string    `json:"path,omitempty"`
	Dependencies []string  `json:"dependencies,omitempty"`
}

type StorageData struct {
	Version    string                          `json:"version"`
	Metadata   StorageMetadata                 `json:"metadata"`
	Executions []ExecutionRecord               `json:"executions"`
	Packages   map[string]map[string]PackageInfo `json:"packages"`
	Statistics StorageStatistics               `json:"statistics"`
}

type StorageMetadata struct {
	Created     time.Time `json:"created"`
	LastUpdated time.Time `json:"last_updated"`
	Hostname    string    `json:"hostname"`
	User        string    `json:"user"`
	DIUVersion  string    `json:"diu_version"`
}

type StorageStatistics struct {
	TotalExecutions    int            `json:"total_executions"`
	ToolsUsed          []string       `json:"tools_used"`
	MostActiveDay      string         `json:"most_active_day"`
	ExecutionFrequency map[string]int `json:"execution_frequency"`
}

type QueryOptions struct {
	Tool     string
	Package  string
	Since    time.Time
	Last     time.Duration
	Limit    int
	Format   string
}

type StatsOptions struct {
	Daily  bool
	Weekly bool
	Tool   string
	Top    int
}

type PackageOptions struct {
	Tool   string
	Unused time.Duration
	Size   bool
}