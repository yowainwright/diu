package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yowainwright/diu/internal/core"
)

func closeStorage(t *testing.T, store Storage) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Failed to close storage: %v", err)
	}
}

func addExecution(t *testing.T, store Storage, record *core.ExecutionRecord) {
	t.Helper()
	if err := store.AddExecution(record); err != nil {
		t.Fatalf("Failed to add execution: %v", err)
	}
}

func updatePackage(t *testing.T, store Storage, pkg *core.PackageInfo) {
	t.Helper()
	if err := store.UpdatePackage(pkg); err != nil {
		t.Fatalf("Failed to update package: %v", err)
	}
}

func TestJSONStorage(t *testing.T) {
	const storageFileName = "test.json"

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:      filepath.Join(tempDir, storageFileName),
			RetentionDays: 30,
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	// Test file creation
	if _, err := os.Stat(config.Storage.JSONFile); os.IsNotExist(err) {
		t.Error("Storage file was not created")
	}

	info, err := os.Stat(config.Storage.JSONFile)
	if err != nil {
		t.Fatalf("Failed to stat storage file: %v", err)
	}
	if got := info.Mode().Perm(); got != core.PrivateFileMode {
		t.Errorf("Storage file mode = %v, want %v", got, core.PrivateFileMode)
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
	defer closeStorage(t, storage)

	record := &core.ExecutionRecord{
		Tool:             "test",
		Command:          "test command",
		Args:             []string{"arg1", "arg2"},
		Timestamp:        time.Now(),
		Duration:         5 * time.Second,
		ExitCode:         0,
		WorkingDir:       "/tmp",
		User:             "testuser",
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
	defer closeStorage(t, storage)

	// Add multiple executions
	tools := []string{"brew", "npm", "go"}
	for i, tool := range tools {
		record := &core.ExecutionRecord{
			Tool:      tool,
			Command:   tool + " test",
			Timestamp: time.Now().Add(time.Duration(-i) * time.Hour),
			ExitCode:  0,
		}
		addExecution(t, storage, record)
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
	defer closeStorage(t, storage)

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
	const storageFileName = "test.json"

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, storageFileName),
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
	addExecution(t, storage, record)

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
	if len(files) > 0 {
		info, err := os.Stat(files[0])
		if err != nil {
			t.Fatalf("Failed to stat backup file: %v", err)
		}
		if got := info.Mode().Perm(); got != core.PrivateFileMode {
			t.Errorf("Backup file mode = %v, want %v", got, core.PrivateFileMode)
		}
	}

	closeStorage(t, storage)

	// Create new storage and restore
	storage2, _ := NewJSONStorage(config)
	defer closeStorage(t, storage2)

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
	defer closeStorage(t, storage)

	// Add old and new executions
	oldRecord := &core.ExecutionRecord{
		Tool:      "old",
		Timestamp: time.Now().Add(-48 * time.Hour),
	}
	newRecord := &core.ExecutionRecord{
		Tool:      "new",
		Timestamp: time.Now(),
	}

	addExecution(t, storage, oldRecord)
	addExecution(t, storage, newRecord)

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

func TestAddExecutionEnforcesMaxExecutions(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:      filepath.Join(tempDir, "test.json"),
			MaxExecutions: 2,
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	now := time.Now()
	addExecution(t, storage, &core.ExecutionRecord{Tool: "oldest", Timestamp: now.Add(-3 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "middle", Timestamp: now.Add(-2 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "newest", Timestamp: now.Add(-1 * time.Hour)})

	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}

	if len(executions) != 2 {
		t.Fatalf("Expected 2 executions after max_executions pruning, got %d", len(executions))
	}

	if executions[0].Tool != "newest" || executions[1].Tool != "middle" {
		t.Errorf("Expected newest executions to be retained, got %s and %s", executions[0].Tool, executions[1].Tool)
	}
}

func TestAddExecutionEnforcesMaxStorageBytes(t *testing.T) {
	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:        filepath.Join(tempDir, "test.json"),
			MaxStorageBytes: 2048,
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	addExecution(t, storage, &core.ExecutionRecord{
		Tool:      "large",
		Command:   strings.Repeat("x", 4096),
		Timestamp: time.Now(),
	})

	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}
	if len(executions) != 0 {
		t.Fatalf("Expected oversized execution to be pruned, got %d executions", len(executions))
	}

	info, err := os.Stat(config.Storage.JSONFile)
	if err != nil {
		t.Fatalf("Failed to stat storage file: %v", err)
	}
	if info.Size() > config.Storage.MaxStorageBytes {
		t.Errorf("Expected storage file to be at most %d bytes, got %d", config.Storage.MaxStorageBytes, info.Size())
	}
}

func TestBackupPrunesOldBackups(t *testing.T) {
	const storageFileName = "test.json"

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile:   filepath.Join(tempDir, storageFileName),
			MaxBackups: 2,
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	for i := 0; i < 3; i++ {
		addExecution(t, storage, &core.ExecutionRecord{
			Tool:      "test",
			Command:   "test backup pruning",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
		if err := storage.Backup(); err != nil {
			t.Fatalf("Failed to create backup %d: %v", i, err)
		}
	}

	files, err := filepath.Glob(config.Storage.JSONFile + ".backup.*")
	if err != nil {
		t.Fatalf("Failed to list backups: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("Expected 2 backup files after pruning, got %d", len(files))
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
	defer closeStorage(t, storage)

	record := &core.ExecutionRecord{
		ID:        "test-id-123",
		Tool:      "test",
		Command:   "test command",
		Timestamp: time.Now(),
	}

	addExecution(t, storage, record)

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
	defer closeStorage(t, storage)

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

	updatePackage(t, storage, pkg1)
	updatePackage(t, storage, pkg2)

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
	defer closeStorage(t, storage)

	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: time.Now()})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: time.Now()})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "go", Timestamp: time.Now()})

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
	defer closeStorage(t, storage)

	today := time.Now()
	yesterday := time.Now().Add(-24 * time.Hour)

	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: today})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: today})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "npm", Timestamp: today})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "go", Timestamp: yesterday})

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
	const (
		concurrentWorkers      = 10
		recordsPerWorker       = 10
		expectedExecutionCount = concurrentWorkers * recordsPerWorker
	)

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
	defer closeStorage(t, storage)

	errs := make(chan error, concurrentWorkers)
	for i := 0; i < concurrentWorkers; i++ {
		go func(id int) {
			for j := 0; j < recordsPerWorker; j++ {
				record := &core.ExecutionRecord{
					Tool:      "test",
					Command:   "concurrent test",
					Timestamp: time.Now(),
				}
				if err := storage.AddExecution(record); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}(i)
	}

	for i := 0; i < concurrentWorkers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("Failed to add concurrent execution: %v", err)
		}
	}

	executions, err := storage.GetExecutions(QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to get executions: %v", err)
	}

	if len(executions) != expectedExecutionCount {
		t.Errorf("Expected %d executions, got %d", expectedExecutionCount, len(executions))
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
	defer closeStorage(t, storage)

	now := time.Now()
	addExecution(t, storage, &core.ExecutionRecord{Tool: "old", Timestamp: now.Add(-48 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "yesterday", Timestamp: now.Add(-24 * time.Hour)})
	addExecution(t, storage, &core.ExecutionRecord{Tool: "today", Timestamp: now})

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
	defer closeStorage(t, storage)

	addExecution(t, storage, &core.ExecutionRecord{
		Tool:             "npm",
		Timestamp:        time.Now(),
		PackagesAffected: []string{"express", "lodash"},
	})
	addExecution(t, storage, &core.ExecutionRecord{
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
	defer closeStorage(t, storage)

	updatePackage(t, storage, &core.PackageInfo{Name: "pkg1", Tool: "npm"})
	updatePackage(t, storage, &core.PackageInfo{Name: "pkg2", Tool: "go"})
	updatePackage(t, storage, &core.PackageInfo{Name: "pkg3", Tool: "npm"})

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
	defer closeStorage(t, storage)

	_, err = storage.GetPackage("npm", "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent package")
	}

	_, err = storage.GetPackage("nonexistent-tool", "pkg")
	if err == nil {
		t.Error("Expected error for nonexistent tool")
	}
}

func TestDeletePackage(t *testing.T) {
	const (
		packageName = "test-package"
		toolName    = "npm"
	)

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
	defer closeStorage(t, storage)

	if err := storage.UpdatePackage(&core.PackageInfo{Name: packageName, Tool: toolName}); err != nil {
		t.Fatalf("Failed to update package: %v", err)
	}

	if err := storage.DeletePackage(toolName, packageName); err != nil {
		t.Fatalf("Failed to delete package: %v", err)
	}

	if _, err := storage.GetPackage(toolName, packageName); err == nil {
		t.Fatal("Expected package to be deleted")
	}
}

func TestRestoreNonexistentFile(t *testing.T) {
	const storageFileName = "test.json"

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, storageFileName),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	err = storage.Restore(filepath.Join(tempDir, storageFileName+".backup.missing"))
	if err == nil {
		t.Error("Expected error for nonexistent restore file")
	}
}

func TestRestoreInvalidJSON(t *testing.T) {
	const (
		storageFileName     = "test.json"
		invalidBackupSuffix = ".backup.invalid"
	)

	tempDir := t.TempDir()
	config := &core.Config{
		Storage: core.StorageConfig{
			JSONFile: filepath.Join(tempDir, storageFileName),
		},
	}

	storage, err := NewJSONStorage(config)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer closeStorage(t, storage)

	invalidFile := filepath.Join(tempDir, storageFileName+invalidBackupSuffix)
	if err := os.WriteFile(invalidFile, []byte("not valid json"), core.PrivateFileMode); err != nil {
		t.Fatalf("Failed to write invalid restore file: %v", err)
	}

	err = storage.Restore(invalidFile)
	if err == nil {
		t.Error("Expected error for invalid JSON restore file")
	}
}
