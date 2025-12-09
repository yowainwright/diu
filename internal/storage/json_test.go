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

func TestGetExecutionByID(t *testing.T) {
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
		ID:        "test-id-123",
		Tool:      "test",
		Command:   "test command",
		Timestamp: time.Now(),
	}

	storage.AddExecution(record)

	retrieved, err := storage.GetExecutionByID("test-id-123")
	if err != nil {
		t.Fatalf("Failed to get execution by ID: %v", err)
	}

	if retrieved.Tool != "test" {
		t.Errorf("Expected tool 'test', got %s", retrieved.Tool)
	}

	_, err = storage.GetExecutionByID("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent ID")
	}
}

func TestGetAllPackages(t *testing.T) {
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

	pkg1 := &core.PackageInfo{
		Name:        "package1",
		Version:     "1.0.0",
		Tool:        "npm",
		InstallDate: time.Now(),
	}
	pkg2 := &core.PackageInfo{
		Name:        "package2",
		Version:     "2.0.0",
		Tool:        "go",
		InstallDate: time.Now(),
	}

	storage.UpdatePackage(pkg1)
	storage.UpdatePackage(pkg2)

	allPackages, err := storage.GetAllPackages()
	if err != nil {
		t.Fatalf("Failed to get all packages: %v", err)
	}

	if len(allPackages) != 2 {
		t.Errorf("Expected 2 tool groups, got %d", len(allPackages))
	}

	if allPackages["npm"]["package1"] == nil {
		t.Error("Expected npm/package1 to exist")
	}
	if allPackages["go"]["package2"] == nil {
		t.Error("Expected go/package2 to exist")
	}
}

func TestGetStatistics(t *testing.T) {
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

	storage.AddExecution(&core.ExecutionRecord{Tool: "npm", Timestamp: time.Now()})
	storage.AddExecution(&core.ExecutionRecord{Tool: "npm", Timestamp: time.Now()})
	storage.AddExecution(&core.ExecutionRecord{Tool: "go", Timestamp: time.Now()})

	stats, err := storage.GetStatistics()
	if err != nil {
		t.Fatalf("Failed to get statistics: %v", err)
	}

	if stats.TotalExecutions != 3 {
		t.Errorf("Expected 3 total executions, got %d", stats.TotalExecutions)
	}

	if stats.ExecutionFrequency["npm"] != 2 {
		t.Errorf("Expected npm frequency 2, got %d", stats.ExecutionFrequency["npm"])
	}

	if stats.ExecutionFrequency["go"] != 1 {
		t.Errorf("Expected go frequency 1, got %d", stats.ExecutionFrequency["go"])
	}
}

func TestUpdateStatistics(t *testing.T) {
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

	today := time.Now()
	yesterday := time.Now().Add(-24 * time.Hour)

	storage.AddExecution(&core.ExecutionRecord{Tool: "npm", Timestamp: today})
	storage.AddExecution(&core.ExecutionRecord{Tool: "npm", Timestamp: today})
	storage.AddExecution(&core.ExecutionRecord{Tool: "npm", Timestamp: today})
	storage.AddExecution(&core.ExecutionRecord{Tool: "go", Timestamp: yesterday})

	err = storage.UpdateStatistics()
	if err != nil {
		t.Fatalf("Failed to update statistics: %v", err)
	}

	stats, _ := storage.GetStatistics()
	expectedDay := today.Format("2006-01-02")
	if stats.MostActiveDay != expectedDay {
		t.Errorf("Expected most active day %s, got %s", expectedDay, stats.MostActiveDay)
	}
}

func TestConcurrentAccess(t *testing.T) {
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

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				record := &core.ExecutionRecord{
					Tool:      "test",
					Command:   "concurrent test",
					Timestamp: time.Now(),
				}
				storage.AddExecution(record)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}

	if len(executions) != 100 {
		t.Errorf("Expected 100 executions, got %d", len(executions))
	}
}

func TestQueryOptionsTimeFiltering(t *testing.T) {
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

	now := time.Now()
	storage.AddExecution(&core.ExecutionRecord{Tool: "old", Timestamp: now.Add(-48 * time.Hour)})
	storage.AddExecution(&core.ExecutionRecord{Tool: "yesterday", Timestamp: now.Add(-24 * time.Hour)})
	storage.AddExecution(&core.ExecutionRecord{Tool: "today", Timestamp: now})

	since := now.Add(-30 * time.Hour)
	results, _ := storage.GetExecutions(QueryOptions{Since: &since})
	if len(results) != 2 {
		t.Errorf("Expected 2 results with Since filter, got %d", len(results))
	}

	until := now.Add(-12 * time.Hour)
	results, _ = storage.GetExecutions(QueryOptions{Until: &until})
	if len(results) != 2 {
		t.Errorf("Expected 2 results with Until filter, got %d", len(results))
	}

	results, _ = storage.GetExecutions(QueryOptions{Since: &since, Until: &until})
	if len(results) != 1 {
		t.Errorf("Expected 1 result with Since+Until filter, got %d", len(results))
	}
}

func TestQueryOptionsPackageFiltering(t *testing.T) {
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

	storage.AddExecution(&core.ExecutionRecord{
		Tool:             "npm",
		Timestamp:        time.Now(),
		PackagesAffected: []string{"express", "lodash"},
	})
	storage.AddExecution(&core.ExecutionRecord{
		Tool:             "npm",
		Timestamp:        time.Now(),
		PackagesAffected: []string{"react"},
	})

	results, _ := storage.GetExecutions(QueryOptions{Package: "express"})
	if len(results) != 1 {
		t.Errorf("Expected 1 result with package filter, got %d", len(results))
	}

	results, _ = storage.GetExecutions(QueryOptions{Package: "nonexistent"})
	if len(results) != 0 {
		t.Errorf("Expected 0 results for nonexistent package, got %d", len(results))
	}
}

func TestGetPackagesAllTools(t *testing.T) {
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

	storage.UpdatePackage(&core.PackageInfo{Name: "pkg1", Tool: "npm"})
	storage.UpdatePackage(&core.PackageInfo{Name: "pkg2", Tool: "go"})
	storage.UpdatePackage(&core.PackageInfo{Name: "pkg3", Tool: "npm"})

	results, err := storage.GetPackages("")
	if err != nil {
		t.Fatalf("Failed to get all packages: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 packages, got %d", len(results))
	}
}

func TestPackageNotFound(t *testing.T) {
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

	_, err = storage.GetPackage("npm", "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent package")
	}

	_, err = storage.GetPackage("nonexistent-tool", "pkg")
	if err == nil {
		t.Error("Expected error for nonexistent tool")
	}
}

func TestRestoreNonexistentFile(t *testing.T) {
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

	err = storage.Restore("/nonexistent/path/file.json")
	if err == nil {
		t.Error("Expected error for nonexistent restore file")
	}
}

func TestRestoreInvalidJSON(t *testing.T) {
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

	invalidFile := filepath.Join(tempDir, "invalid.json")
	os.WriteFile(invalidFile, []byte("not valid json"), 0644)

	err = storage.Restore(invalidFile)
	if err == nil {
		t.Error("Expected error for invalid JSON restore file")
	}
}