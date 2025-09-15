package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func TestJSONStorage(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:      filepath.Join(tempDir, "test.json"),
			RetentionDays: 30,
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Test file creation
	if _, err := os.Stat(config.Storage.JSONFile); os.IsNotExist(err) {
		t.Error("Storage file was not created")
	}
}

func TestAddExecution(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	record := &core.ExecutionRecord{
		Tool:       "test",
		Command:    "test command",
		Args:       []string{"arg1", "arg2"},
		Timestamp:  time.Now(),
		Duration:   5 * time.Second,
		ExitCode:   0,
		WorkingDir: "/tmp",
		User:       "testuser",
		PackagesAffected: []string{"package1"},
	}

	err = storage.AddExecution(record)
	if err != nil {
		t.Fatalf("Failed to add execution: %v", err)
	}

	// Verify execution was added
	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}

	if len(executions) != 1 {
		t.Errorf("Expected 1 execution, got %d", len(executions))
	}

	if executions[0].Tool != "test" {
		t.Errorf("Expected tool 'test', got %s", executions[0].Tool)
	}
}

func TestGetExecutions(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Add multiple executions
	tools := []string{"brew", "npm", "go"}
	for i, tool := range tools {
		record := &core.ExecutionRecord{
			Tool:      tool,
			Command:   tool + " test",
			Timestamp: time.Now().Add(time.Duration(-i) * time.Hour),
			ExitCode:  0,
		}
		storage.AddExecution(record)
	}

	// Test filtering by tool
	brewExecs, err := storage.GetExecutions(QueryOptions{Tool: "brew"})
	if err != nil {
		t.Fatalf("Failed to query executions: %v", err)
	}

	if len(brewExecs) != 1 {
		t.Errorf("Expected 1 brew execution, got %d", len(brewExecs))
	}

	// Test limit
	limited, err := storage.GetExecutions(QueryOptions{Limit: 2})
	if err != nil {
		t.Fatalf("Failed to query with limit: %v", err)
	}

	if len(limited) != 2 {
		t.Errorf("Expected 2 executions with limit, got %d", len(limited))
	}
}

func TestPackageManagement(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	pkg := &core.PackageInfo{
		Name:        "test-package",
		Version:     "1.0.0",
		Tool:        "npm",
		InstallDate: time.Now(),
		LastUsed:    time.Now(),
		UsageCount:  5,
	}

	err = storage.UpdatePackage(pkg)
	if err != nil {
		t.Fatalf("Failed to update package: %v", err)
	}

	// Get package
	retrieved, err := storage.GetPackage("npm", "test-package")
	if err != nil {
		t.Fatalf("Failed to get package: %v", err)
	}

	if retrieved.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", retrieved.Version)
	}

	// Get all packages for tool
	packages, err := storage.GetPackages("npm")
	if err != nil {
		t.Fatalf("Failed to get packages: %v", err)
	}

	if len(packages) != 1 {
		t.Errorf("Expected 1 package, got %d", len(packages))
	}
}

func TestBackupRestore(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Add test data
	record := &core.ExecutionRecord{
		Tool:      "test",
		Command:   "test backup",
		Timestamp: time.Now(),
	}
	storage.AddExecution(record)

	// Create backup
	err = storage.Backup()
	if err != nil {
		t.Fatalf("Failed to create backup: %v", err)
	}

	// Verify backup file exists
	files, _ := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if len(files) == 0 {
		t.Error("Backup file was not created")
	}

	storage.Close()

	// Create new storage and restore
	storage2, _ := NewJSONStorage(config)
	defer storage2.Close()

	if len(files) > 0 {
		err = storage2.Restore(files[0])
		if err != nil {
			t.Fatalf("Failed to restore backup: %v", err)
		}

		executions, _ := storage2.GetExecutions(QueryOptions{})
		if len(executions) != 1 {
			t.Error("Backup restore failed to restore executions")
		}
	}
}

func TestCleanup(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, "test.json"),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	// Add old and new executions
	oldRecord := &core.ExecutionRecord{
		Tool:      "old",
		Timestamp: time.Now().Add(-48 * time.Hour),
	}
	newRecord := &core.ExecutionRecord{
		Tool:      "new",
		Timestamp: time.Now(),
	}

	storage.AddExecution(oldRecord)
	storage.AddExecution(newRecord)

	// Cleanup records older than 24 hours
	err = storage.Cleanup(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("Failed to cleanup: %v", err)
	}

	executions, _ := storage.GetExecutions(QueryOptions{})
	if len(executions) != 1 {
		t.Errorf("Expected 1 execution after cleanup, got %d", len(executions))
	}

	if executions[0].Tool != "new" {
		t.Error("Wrong execution retained after cleanup")
	}
}