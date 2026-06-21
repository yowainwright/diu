package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Version != "1.0" {
		t.Errorf("Expected version 1.0, got %s", config.Version)
	}

	if config.Daemon.Port != 8080 {
		t.Errorf("Expected daemon port 8080, got %d", config.Daemon.Port)
	}

	if config.Daemon.PIDFile != filepath.Join(config.Daemon.DataDir, DefaultPIDFileName) {
		t.Errorf("Expected PID file under data dir, got %s", config.Daemon.PIDFile)
	}

	if config.Daemon.SocketPath != filepath.Join(config.Daemon.DataDir, DefaultSocketFileName) {
		t.Errorf("Expected socket path under data dir, got %s", config.Daemon.SocketPath)
	}

	if config.Storage.Backend != "json" {
		t.Errorf("Expected storage backend json, got %s", config.Storage.Backend)
	}

	if config.Storage.RetentionDays != 365 {
		t.Errorf("Expected retention days 365, got %d", config.Storage.RetentionDays)
	}

	if config.Storage.MaxExecutions != 50000 {
		t.Errorf("Expected max executions 50000, got %d", config.Storage.MaxExecutions)
	}

	if config.Storage.MaxStorageBytes != 10*1024*1024 {
		t.Errorf("Expected max storage bytes 10485760, got %d", config.Storage.MaxStorageBytes)
	}

	if config.Storage.MaxBackups != 7 {
		t.Errorf("Expected max backups 7, got %d", config.Storage.MaxBackups)
	}

	if len(config.Monitoring.EnabledTools) == 0 {
		t.Error("Expected enabled tools to be configured")
	}
	for _, tool := range []string{ToolPNPM, ToolBun, ToolPip, ToolUV, ToolPoetry} {
		if !containsString(config.Monitoring.EnabledTools, tool) {
			t.Errorf("Expected %s to be enabled by default, got %#v", tool, config.Monitoring.EnabledTools)
		}
	}

	if !config.API.Enabled {
		t.Error("Expected API to be enabled by default")
	}
}

func TestLoadConfig(t *testing.T) {
	// Test loading non-existent config returns default
	config, err := LoadConfig("/non/existent/path")
	if err != nil {
		t.Errorf("Expected no error for non-existent config, got %v", err)
	}

	if config == nil {
		t.Error("Expected default config, got nil")
	}
}

func TestLoadConfigAppliesDefaultsForMissingFields(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	customStoragePath := filepath.Join(tempDir, "custom.json")

	data := []byte(`{"storage":{"json_file":"` + customStoragePath + `"}}`)
	if err := os.WriteFile(configPath, data, PrivateFileMode); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if config.Storage.JSONFile != customStoragePath {
		t.Errorf("Expected storage path %s, got %s", customStoragePath, config.Storage.JSONFile)
	}

	if config.Storage.RetentionDays != DefaultRetentionDays {
		t.Errorf("Expected retention days default %d, got %d", DefaultRetentionDays, config.Storage.RetentionDays)
	}

	if config.Storage.MaxExecutions != DefaultMaxExecutions {
		t.Errorf("Expected max executions default %d, got %d", DefaultMaxExecutions, config.Storage.MaxExecutions)
	}

	if config.Storage.MaxStorageBytes != DefaultMaxStorageBytes {
		t.Errorf("Expected max storage bytes default %d, got %d", DefaultMaxStorageBytes, config.Storage.MaxStorageBytes)
	}

	if config.Storage.MaxBackups != DefaultMaxBackups {
		t.Errorf("Expected max backups default %d, got %d", DefaultMaxBackups, config.Storage.MaxBackups)
	}

	if len(config.Monitoring.Filesystem.WatchPaths) == 0 {
		t.Error("Expected default watch paths for missing watch_paths config")
	}
}

func TestLoadConfigHonorsEmptyWatchPathsOverride(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	data := []byte(`{"monitoring":{"filesystem":{"watch_paths":{}}}}`)
	if err := os.WriteFile(configPath, data, PrivateFileMode); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if len(config.Monitoring.Filesystem.WatchPaths) != 0 {
		t.Errorf("Expected empty watch paths override, got %#v", config.Monitoring.Filesystem.WatchPaths)
	}
}

func TestLoadConfigHonorsPartialWatchPathsOverride(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	data := []byte(`{"monitoring":{"filesystem":{"watch_paths":{"npm":["/custom/npm"]}}}}`)
	if err := os.WriteFile(configPath, data, PrivateFileMode); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	watchPaths := config.Monitoring.Filesystem.WatchPaths
	if len(watchPaths) != 1 {
		t.Fatalf("Expected one watch_paths key, got %#v", watchPaths)
	}
	if paths := watchPaths[ToolNPM]; len(paths) != 1 || paths[0] != "/custom/npm" {
		t.Errorf("Expected custom npm watch path, got %#v", paths)
	}
	if _, ok := watchPaths[ToolHomebrew]; ok {
		t.Errorf("Expected homebrew watch paths to remain disabled, got %#v", watchPaths[ToolHomebrew])
	}
}

func TestConfigSave(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	config := DefaultConfig()
	config.Daemon.Port = 9090

	err := config.SaveTo(configPath)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Config file was not created")
	}

	// Load and verify
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if loaded.Daemon.Port != 9090 {
		t.Errorf("Expected port 9090, got %d", loaded.Daemon.Port)
	}
}

func TestConfigSaveUsesDefaultPath(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	config := DefaultConfig()
	config.Daemon.Port = 9091
	if err := config.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Daemon.Port != 9091 {
		t.Fatalf("Loaded daemon port = %d, want 9091", loaded.Daemon.Port)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	tempDir := t.TempDir()
	malformedPath := filepath.Join(tempDir, "malformed.json")
	if err := os.WriteFile(malformedPath, []byte("{"), PrivateFileMode); err != nil {
		t.Fatalf("Failed to write malformed config: %v", err)
	}
	if _, err := LoadConfig(malformedPath); err == nil {
		t.Fatal("Expected malformed config to fail")
	}

	if _, err := LoadConfig(tempDir); err == nil {
		t.Fatal("Expected directory config path to fail")
	}
}

func TestEnsureDirectories(t *testing.T) {
	tempDir := t.TempDir()

	config := DefaultConfig()
	config.Daemon.DataDir = filepath.Join(tempDir, "data")
	config.Daemon.PIDFile = filepath.Join(tempDir, "run", "diu.pid")
	config.Daemon.SocketPath = filepath.Join(tempDir, "socket", "diu.sock")
	config.Storage.JSONFile = filepath.Join(tempDir, "storage", "exec.json")
	config.Monitoring.Process.WrapperDir = filepath.Join(tempDir, "wrappers")

	err := config.EnsureDirectories()
	if err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}

	// Verify directories exist
	dirs := []string{
		config.Daemon.DataDir,
		filepath.Dir(config.Daemon.PIDFile),
		filepath.Dir(config.Daemon.SocketPath),
		filepath.Dir(config.Storage.JSONFile),
		config.Monitoring.Process.WrapperDir,
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("Directory %s was not created", dir)
		}
	}
}

func TestShellEscapeString(t *testing.T) {
	got := ShellEscapeString(`a\b"$c`)
	want := `a\\b\"\$c`
	if got != want {
		t.Fatalf("ShellEscapeString = %q, want %q", got, want)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
