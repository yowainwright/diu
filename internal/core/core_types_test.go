package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExecutionRecord(t *testing.T) {
	record := sampleExecutionRecord()
	assertExecutionRecordFields(t, record)
}

func sampleExecutionRecord() ExecutionRecord {
	return ExecutionRecord{
		ID:               "test-123",
		Tool:             "homebrew",
		Command:          "brew install wget",
		Args:             []string{"install", "wget"},
		Timestamp:        time.Now(),
		Duration:         5 * time.Second,
		ExitCode:         0,
		WorkingDir:       "/tmp",
		User:             "testuser",
		PackagesAffected: []string{"wget"},
	}
}

func assertExecutionRecordFields(t *testing.T, record ExecutionRecord) {
	t.Helper()
	if record.Tool != "homebrew" {
		t.Errorf("Expected tool to be homebrew, got %s", record.Tool)
	}
	if len(record.PackagesAffected) != 1 {
		t.Errorf("Expected 1 package affected, got %d", len(record.PackagesAffected))
	}
	if record.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", record.ExitCode)
	}
}

func TestPackageInfo(t *testing.T) {
	pkg := PackageInfo{
		Name:        "wget",
		Version:     "1.21.3",
		Tool:        "homebrew",
		InstallDate: time.Now().Add(-24 * time.Hour),
		LastUsed:    time.Now(),
		UsageCount:  5,
		Path:        "/usr/local/bin/wget",
	}

	if pkg.Name != "wget" {
		t.Errorf("Expected package name wget, got %s", pkg.Name)
	}

	if pkg.UsageCount != 5 {
		t.Errorf("Expected usage count 5, got %d", pkg.UsageCount)
	}
}

func TestExecutionRecordJSONUsesDurationMilliseconds(t *testing.T) {
	record := durationExecutionRecord()
	data := marshalExecutionRecord(t, record)
	raw := unmarshalRawRecord(t, data)
	assertDurationMilliseconds(t, raw, 1500)

	decoded := unmarshalExecutionRecord(t, data)
	assertDurationRoundTrip(t, decoded, record)
}

func durationExecutionRecord() ExecutionRecord {
	return ExecutionRecord{
		Tool:      ToolNPM,
		Command:   "npm install express",
		Args:      []string{"install", "express"},
		Timestamp: time.Now(),
		Duration:  1500 * time.Millisecond,
		ExitCode:  0,
	}
}

func marshalExecutionRecord(t *testing.T, record ExecutionRecord) []byte {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	return data
}

func unmarshalRawRecord(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw failed: %v", err)
	}
	return raw
}

func assertDurationMilliseconds(t *testing.T, raw map[string]interface{}, expected int64) {
	t.Helper()
	fieldName := "duration_ms"
	if raw[fieldName] != float64(expected) {
		t.Errorf("Expected %s %d, got %v", fieldName, expected, raw[fieldName])
	}
}

func unmarshalExecutionRecord(t *testing.T, data []byte) ExecutionRecord {
	t.Helper()
	var decoded ExecutionRecord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal record failed: %v", err)
	}
	return decoded
}

func assertDurationRoundTrip(t *testing.T, decoded, record ExecutionRecord) {
	t.Helper()
	if decoded.Duration != record.Duration {
		t.Errorf("Expected duration %s, got %s", record.Duration, decoded.Duration)
	}
}

func TestNormalizeToolName(t *testing.T) {
	for input, expected := range normalizeToolNameCases() {
		if got := NormalizeToolName(input); got != expected {
			t.Errorf("NormalizeToolName(%q) = %q, want %q", input, got, expected)
		}
	}
}

func normalizeToolNameCases() map[string]string {
	return map[string]string{
		"brew":     ToolHomebrew,
		"Homebrew": ToolHomebrew,
		"golang":   ToolGo,
		" npm ":    ToolNPM,
		"pip3":     ToolPip,
		"python3":  ToolPip,
	}
}

func TestStorageData(t *testing.T) {
	data := sampleStorageData()
	assertStorageDataFields(t, data)
}

func sampleStorageData() StorageData {
	return StorageData{
		Version: "1.0.0",
		Metadata: StorageMetadata{
			Created:     time.Now(),
			LastUpdated: time.Now(),
			Hostname:    "test-host",
			User:        "testuser",
			DIUVersion:  Version,
		},
		Executions: []ExecutionRecord{},
		Packages:   make(map[string]map[string]PackageInfo),
		Statistics: StorageStatistics{
			TotalExecutions:    0,
			ToolsUsed:          []string{},
			ExecutionFrequency: make(map[string]int),
		},
	}
}

func assertStorageDataFields(t *testing.T, data StorageData) {
	t.Helper()
	if data.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", data.Version)
	}
	if data.Metadata.User != "testuser" {
		t.Errorf("Expected user testuser, got %s", data.Metadata.User)
	}
}
