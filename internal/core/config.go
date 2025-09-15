package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Version    string            `json:"version"`
	Daemon     DaemonConfig      `json:"daemon"`
	Storage    StorageConfig     `json:"storage"`
	Monitoring MonitoringConfig  `json:"monitoring"`
	Tools      ToolsConfig       `json:"tools"`
	API        APIConfig         `json:"api"`
	Reporting  ReportingConfig   `json:"reporting"`
}

type DaemonConfig struct {
	Port     int    `json:"port"`
	LogLevel string `json:"log_level"`
	DataDir  string `json:"data_dir"`
	PIDFile  string `json:"pid_file"`
}

type StorageConfig struct {
	Backend        string        `json:"backend"`
	JSONFile       string        `json:"json_file"`
	BackupEnabled  bool          `json:"backup_enabled"`
	BackupInterval time.Duration `json:"backup_interval"`
	RetentionDays  int           `json:"retention_days"`
}

type MonitoringConfig struct {
	EnabledTools []string          `json:"enabled_tools"`
	Methods      []string          `json:"methods"`
	Process      ProcessConfig     `json:"process"`
	Filesystem   FilesystemConfig  `json:"filesystem"`
}

type ProcessConfig struct {
	WrapperDir          string `json:"wrapper_dir"`
	AutoInstallWrappers bool   `json:"auto_install_wrappers"`
}

type FilesystemConfig struct {
	ScanInterval time.Duration            `json:"scan_interval"`
	WatchPaths   map[string][]string      `json:"watch_paths"`
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

type APIConfig struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	CORSEnabled bool   `json:"cors_enabled"`
}

type ReportingConfig struct {
	DailySummary  bool `json:"daily_summary"`
	WeeklySummary bool `json:"weekly_summary"`
	EmailReports  bool `json:"email_reports"`
}

func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".local", "share", "diu")

	return &Config{
		Version: "1.0",
		Daemon: DaemonConfig{
			Port:     8080,
			LogLevel: "info",
			DataDir:  dataDir,
			PIDFile:  "/tmp/diu.pid",
		},
		Storage: StorageConfig{
			Backend:        "json",
			JSONFile:       filepath.Join(dataDir, "executions.json"),
			BackupEnabled:  true,
			BackupInterval: 24 * time.Hour,
			RetentionDays:  365,
		},
		Monitoring: MonitoringConfig{
			EnabledTools: []string{"homebrew", "npm", "go", "pip", "gem", "cargo"},
			Methods:      []string{"process", "filesystem"},
			Process: ProcessConfig{
				WrapperDir:          filepath.Join(homeDir, ".local", "bin", "diu-wrappers"),
				AutoInstallWrappers: true,
			},
			Filesystem: FilesystemConfig{
				ScanInterval: 30 * time.Second,
				WatchPaths: map[string][]string{
					"homebrew": {"/usr/local/bin", "/opt/homebrew/bin"},
					"npm":      {filepath.Join(homeDir, ".npm", "bin"), "/usr/local/lib/node_modules"},
				},
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
		API: APIConfig{
			Enabled:     true,
			Host:        "127.0.0.1",
			Port:        8081,
			CORSEnabled: false,
		},
		Reporting: ReportingConfig{
			DailySummary:  true,
			WeeklySummary: true,
			EmailReports:  false,
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

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) Save() error {
	homeDir, _ := os.UserHomeDir()
	path := filepath.Join(homeDir, ".config", "diu", "config.json")

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func (c *Config) EnsureDirectories() error {
	dirs := []string{
		c.Daemon.DataDir,
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