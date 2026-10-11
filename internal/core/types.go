package core

import (
	"encoding/json"
	"time"
)

const ExecutionKindLaunchAttempt = "launch_attempt"

type ExecutionRecord struct {
	ID               string                 `json:"id"`
	Kind             string                 `json:"kind,omitempty"`
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

type executionRecordFields ExecutionRecord

type executionRecordJSON struct {
	*executionRecordFields
	DurationMS int64 `json:"duration_ms"`
}

type launchAttemptRecordJSON struct {
	ID               string                 `json:"id,omitempty"`
	Kind             string                 `json:"kind"`
	Tool             string                 `json:"tool"`
	Command          string                 `json:"command"`
	Timestamp        time.Time              `json:"timestamp"`
	PackagesAffected []string               `json:"packages_affected,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

func (r ExecutionRecord) MarshalJSON() ([]byte, error) {
	if r.Kind == ExecutionKindLaunchAttempt {
		return marshalLaunchAttemptRecord(r)
	}
	return marshalCompletedExecutionRecord(r)
}

func marshalLaunchAttemptRecord(record ExecutionRecord) ([]byte, error) {
	return json.Marshal(launchAttemptRecordJSON{
		ID:               record.ID,
		Kind:             record.Kind,
		Tool:             record.Tool,
		Command:          record.Command,
		Timestamp:        record.Timestamp,
		PackagesAffected: record.PackagesAffected,
		Metadata:         record.Metadata,
	})
}

func marshalCompletedExecutionRecord(record ExecutionRecord) ([]byte, error) {
	return json.Marshal(executionRecordJSON{
		executionRecordFields: (*executionRecordFields)(&record),
		DurationMS:            record.Duration.Milliseconds(),
	})
}

func (r *ExecutionRecord) UnmarshalJSON(data []byte) error {
	var fields executionRecordFields
	raw := executionRecordJSON{executionRecordFields: &fields}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = ExecutionRecord(fields)
	r.Duration = time.Duration(raw.DurationMS) * time.Millisecond
	return nil
}

type PackageInfo struct {
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	Tool         string    `json:"tool"`
	InstallDate  time.Time `json:"install_date"`
	LastUsed     time.Time `json:"last_used"`
	UsageCount   int       `json:"usage_count"`
	Path         string    `json:"path,omitempty"`
	Fingerprint  string    `json:"fingerprint,omitempty"`
	SizeBytes    int64     `json:"size_bytes,omitempty"`
	ModifiedAt   int64     `json:"modified_at_unix_nano,omitempty"`
	UpdatedAt    int64     `json:"updated_at_unix_nano,omitempty"`
	Dependencies []string  `json:"dependencies,omitempty"`
}

type StorageData struct {
	Version            string                            `json:"version"`
	ExecutionLogFormat string                            `json:"execution_log_format,omitempty"`
	Metadata           StorageMetadata                   `json:"metadata"`
	Executions         []ExecutionRecord                 `json:"executions"`
	Packages           map[string]map[string]PackageInfo `json:"packages"`
	PackageTombstones  map[string]map[string]int64       `json:"package_tombstones,omitempty"`
	Statistics         StorageStatistics                 `json:"statistics"`
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
