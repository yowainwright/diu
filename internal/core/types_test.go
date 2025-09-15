package core

import (
	"testing"
	"time"
)

func TestExecutionRecord(t *testing.T) {
	record := ExecutionRecord{
		ID:        "test-123",
		Tool:      "homebrew",
		Command:   "brew install wget",
		Args:      []string{"install", "wget"},
		Timestamp: time.Now(),
		Duration:  5 * time.Second,
		ExitCode:  0,
		WorkingDir: "/tmp",
		User:      "testuser",
		PackagesAffected: []string{"wget"},
	}

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

func TestStorageData(t *testing.T) {
	data := StorageData{
		Version: "1.0.0",
		Metadata: StorageMetadata{
			Created:     time.Now(),
			LastUpdated: time.Now(),
			Hostname:    "test-host",
			User:        "testuser",
			DIUVersion:  "0.1.0",
		},
		Executions: []ExecutionRecord{},
		Packages:   make(map[string]map[string]PackageInfo),
		Statistics: StorageStatistics{
			TotalExecutions:    0,
			ToolsUsed:          []string{},
			ExecutionFrequency: make(map[string]int),
		},
	}

	if data.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", data.Version)
	}

	if data.Metadata.User != "testuser" {
		t.Errorf("Expected user testuser, got %s", data.Metadata.User)
	}
}