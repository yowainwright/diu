package core

import (
	"encoding/json"
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

type executionRecordJSON struct {
	ID               string                 `json:"id"`
	Tool             string                 `json:"tool"`
	Command          string                 `json:"command"`
	Args             []string               `json:"args"`
	Timestamp        time.Time              `json:"timestamp"`
	DurationMS       int64                  `json:"duration_ms"`
	ExitCode         int                    `json:"exit_code"`
	WorkingDir       string                 `json:"working_dir"`
	User             string                 `json:"user"`
	Environment      map[string]string      `json:"environment,omitempty"`
	PackagesAffected []string               `json:"packages_affected,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

func (r ExecutionRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(executionRecordJSON{
		ID:               r.ID,
		Tool:             r.Tool,
		Command:          r.Command,
		Args:             r.Args,
		Timestamp:        r.Timestamp,
		DurationMS:       r.Duration.Milliseconds(),
		ExitCode:         r.ExitCode,
		WorkingDir:       r.WorkingDir,
		User:             r.User,
		Environment:      r.Environment,
		PackagesAffected: r.PackagesAffected,
		Metadata:         r.Metadata,
	})
}

func (r *ExecutionRecord) UnmarshalJSON(data []byte) error {
	var raw executionRecordJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	r.ID = raw.ID
	r.Tool = raw.Tool
	r.Command = raw.Command
	r.Args = raw.Args
	r.Timestamp = raw.Timestamp
	r.Duration = durationFromJSONMilliseconds(raw.DurationMS)
	r.ExitCode = raw.ExitCode
	r.WorkingDir = raw.WorkingDir
	r.User = raw.User
	r.Environment = raw.Environment
	r.PackagesAffected = raw.PackagesAffected
	r.Metadata = raw.Metadata
	return nil
}

func durationFromJSONMilliseconds(value int64) time.Duration {
	if value > int64((24*time.Hour)/time.Millisecond) {
		return time.Duration(value)
	}
	return time.Duration(value) * time.Millisecond
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
	Version    string                            `json:"version"`
	Metadata   StorageMetadata                   `json:"metadata"`
	Executions []ExecutionRecord                 `json:"executions"`
	Packages   map[string]map[string]PackageInfo `json:"packages"`
	Statistics StorageStatistics                 `json:"statistics"`
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
	Tool    string
	Package string
	Since   time.Time
	Last    time.Duration
	Limit   int
	Format  string
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
