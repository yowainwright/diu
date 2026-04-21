package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Version    string           `json:"version"`
	Storage    StorageConfig    `json:"storage"`
	Monitoring MonitoringConfig `json:"monitoring"`
	Tools      ToolsConfig      `json:"tools"`
}

type StorageConfig struct {
	Backend        string        `json:"backend"`
	JSONFile       string        `json:"json_file"`
	BackupEnabled  bool          `json:"backup_enabled"`
	BackupInterval time.Duration `json:"backup_interval"`
	RetentionDays  int           `json:"retention_days"`
}

type MonitoringConfig struct {
	EnabledTools []string      `json:"enabled_tools"`
	Process      ProcessConfig `json:"process"`
}

type ProcessConfig struct {
	WrapperDir          string `json:"wrapper_dir"`
	AutoInstallWrappers bool   `json:"auto_install_wrappers"`
}

type ToolsConfig struct {
	Homebrew HomebrewConfig `json:"homebrew"`
	NPM      NPMConfig      `json:"npm"`
	Go       GoConfig       `json:"go"`
}

type HomebrewConfig struct {
	CellarPaths   []string `json:"cellar_paths"`
	TrackCasks    bool     `json:"track_casks"`
	TrackServices bool     `json:"track_services"`
}

type NPMConfig struct {
	TrackGlobalOnly       bool `json:"track_global_only"`
	IgnoreDevDependencies bool `json:"ignore_dev_dependencies"`
}

type GoConfig struct {
	GoPath string `json:"gopath"`
	GoBin  string `json:"gobin"`
}

func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".local", "share", "diu")

	return &Config{
		Version: "1.0",
		Storage: StorageConfig{
			Backend:        "json",
			JSONFile:       filepath.Join(dataDir, "diu.json"),
			BackupEnabled:  true,
			BackupInterval: 24 * time.Hour,
			RetentionDays:  365,
		},
		Monitoring: MonitoringConfig{
			EnabledTools: []string{"homebrew", "npm", "go", "pip", "gem", "cargo"},
			Process: ProcessConfig{
				WrapperDir:          filepath.Join(homeDir, ".local", "bin", "diu-wrappers"),
				AutoInstallWrappers: false,
			},
		},
		Tools: ToolsConfig{
			Homebrew: HomebrewConfig{
				CellarPaths:   []string{"/usr/local/Cellar", "/opt/homebrew/Cellar"},
				TrackCasks:    true,
				TrackServices: true,
			},
			NPM: NPMConfig{
				TrackGlobalOnly:       true,
				IgnoreDevDependencies: true,
			},
			Go: GoConfig{
				GoPath: os.Getenv("GOPATH"),
				GoBin:  os.Getenv("GOBIN"),
			},
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	if path == "" {
		homeDir, _ := os.UserHomeDir()
		path = filepath.Join(homeDir, ".config", "diu", "config.json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return cfg, nil
}

func (c *Config) Save(path string) error {
	if path == "" {
		homeDir, _ := os.UserHomeDir()
		path = filepath.Join(homeDir, ".config", "diu", "config.json")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return os.Rename(tmp, path)
}

func (c *Config) EnsureDirectories() error {
	dirs := []string{
		filepath.Dir(c.Storage.JSONFile),
		c.Monitoring.Process.WrapperDir,
	}

	for _, dir := range dirs {
		if dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}
	}

	return nil
}
