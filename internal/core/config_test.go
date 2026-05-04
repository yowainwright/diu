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

	if config.Storage.Backend != "json" {
		t.Errorf("Expected storage backend json, got %s", config.Storage.Backend)
	}

	if config.Storage.RetentionDays != 365 {
		t.Errorf("Expected retention days 365, got %d", config.Storage.RetentionDays)
	}

	if len(config.Monitoring.EnabledTools) == 0 {
		t.Error("Expected enabled tools to be configured")
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

func TestEnsureDirectories(t *testing.T) {
	tempDir := t.TempDir()

	config := DefaultConfig()
	config.Daemon.DataDir = filepath.Join(tempDir, "data")
	config.Storage.JSONFile = filepath.Join(tempDir, "storage", "exec.json")
	config.Monitoring.Process.WrapperDir = filepath.Join(tempDir, "wrappers")

	err := config.EnsureDirectories()
	if err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}

	// Verify directories exist
	dirs := []string{
		config.Daemon.DataDir,
		filepath.Dir(config.Storage.JSONFile),
		config.Monitoring.Process.WrapperDir,
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("Directory %s was not created", dir)
		}
	}
}