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

	if config.Storage.Backend != "json" {
		t.Errorf("Expected storage backend json, got %s", config.Storage.Backend)
	}

	if config.Storage.RetentionDays != 365 {
		t.Errorf("Expected retention days 365, got %d", config.Storage.RetentionDays)
	}

	if len(config.Monitoring.EnabledTools) == 0 {
		t.Error("Expected enabled tools to be configured")
	}
}

func TestLoadConfig(t *testing.T) {
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
	config.Storage.RetentionDays = 90

	err := config.Save(configPath)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Config file was not created")
	}

	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if loaded.Storage.RetentionDays != 90 {
		t.Errorf("Expected retention days 90, got %d", loaded.Storage.RetentionDays)
	}
}

func TestLoadConfigMergesDefaults(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	data := []byte(`{"storage":{"retention_days":90}}`)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("Failed to seed config: %v", err)
	}

	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if loaded.Storage.RetentionDays != 90 {
		t.Errorf("Expected retention days 90, got %d", loaded.Storage.RetentionDays)
	}
	if loaded.Storage.JSONFile == "" {
		t.Fatal("Expected storage json file default to be preserved")
	}
	if len(loaded.Monitoring.EnabledTools) == 0 {
		t.Fatal("Expected enabled tools defaults to be preserved")
	}
	if loaded.Monitoring.Process.WrapperDir == "" {
		t.Fatal("Expected process wrapper dir default to be preserved")
	}
}

func TestEnsureDirectories(t *testing.T) {
	tempDir := t.TempDir()

	config := DefaultConfig()
	config.Storage.JSONFile = filepath.Join(tempDir, "storage", "diu.json")
	config.Monitoring.Process.WrapperDir = filepath.Join(tempDir, "wrappers")

	err := config.EnsureDirectories()
	if err != nil {
		t.Fatalf("Failed to create directories: %v", err)
	}

	dirs := []string{
		filepath.Dir(config.Storage.JSONFile),
		config.Monitoring.Process.WrapperDir,
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("Directory %s was not created", dir)
		}
	}
}
