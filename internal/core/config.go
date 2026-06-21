package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yowainwright/diu/internal/safefs"
)

type Config struct {
	Version    string           `json:"version"`
	Daemon     DaemonConfig     `json:"daemon"`
	Storage    StorageConfig    `json:"storage"`
	Monitoring MonitoringConfig `json:"monitoring"`
	Tools      ToolsConfig      `json:"tools"`
	API        APIConfig        `json:"api"`
	Reporting  ReportingConfig  `json:"reporting"`
}

type DaemonConfig struct {
	Port       int    `json:"port"`
	LogLevel   string `json:"log_level"`
	DataDir    string `json:"data_dir"`
	PIDFile    string `json:"pid_file"`
	SocketPath string `json:"socket_path"`
}

type StorageConfig struct {
	Backend         string        `json:"backend"`
	JSONFile        string        `json:"json_file"`
	BackupEnabled   bool          `json:"backup_enabled"`
	BackupInterval  time.Duration `json:"backup_interval"`
	RetentionDays   int           `json:"retention_days"`
	MaxExecutions   int           `json:"max_executions"`
	MaxStorageBytes int64         `json:"max_storage_bytes"`
	MaxBackups      int           `json:"max_backups"`
}

type MonitoringConfig struct {
	EnabledTools []string         `json:"enabled_tools"`
	Methods      []string         `json:"methods"`
	Process      ProcessConfig    `json:"process"`
	Filesystem   FilesystemConfig `json:"filesystem"`
}

type ProcessConfig struct {
	WrapperDir          string `json:"wrapper_dir"`
	AutoInstallWrappers bool   `json:"auto_install_wrappers"`
}

type FilesystemConfig struct {
	ScanInterval time.Duration       `json:"scan_interval"`
	WatchPaths   map[string][]string `json:"watch_paths"`
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
	homeDir := os.Getenv("HOME")
	if dir, err := os.UserHomeDir(); err == nil {
		homeDir = dir
	}
	dataDir := DefaultDataDir()

	return &Config{
		Version: ConfigVersion,
		Daemon: DaemonConfig{
			Port:       DefaultDaemonPort,
			LogLevel:   DefaultLogLevel,
			DataDir:    dataDir,
			PIDFile:    DefaultPIDFilePath(dataDir),
			SocketPath: DefaultSocketPath(dataDir),
		},
		Storage: StorageConfig{
			Backend:         StorageBackendJSON,
			JSONFile:        filepath.Join(dataDir, "executions.json"),
			BackupEnabled:   true,
			BackupInterval:  24 * time.Hour,
			RetentionDays:   DefaultRetentionDays,
			MaxExecutions:   DefaultMaxExecutions,
			MaxStorageBytes: DefaultMaxStorageBytes,
			MaxBackups:      DefaultMaxBackups,
		},
		Monitoring: MonitoringConfig{
			EnabledTools: DefaultEnabledTools,
			Methods:      DefaultMonitorMethods,
			Process: ProcessConfig{
				WrapperDir:          filepath.Join(homeDir, ".local", "bin", "diu-wrappers"),
				AutoInstallWrappers: true,
			},
			Filesystem: FilesystemConfig{
				ScanInterval: 30 * time.Second,
				WatchPaths: map[string][]string{
					ToolHomebrew: HomebrewBinPaths,
					ToolNPM:      {filepath.Join(homeDir, ".npm", "bin"), "/usr/local/lib/node_modules"},
					ToolPNPM:     {filepath.Join(homeDir, "Library", "pnpm"), filepath.Join(homeDir, ".local", "share", "pnpm")},
					ToolBun:      {filepath.Join(homeDir, ".bun", "bin")},
				},
			},
		},
		Tools: ToolsConfig{
			Homebrew: HomebrewConfig{
				CellarPaths:   HomebrewCellarPaths,
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
			Host:        DefaultAPIHost,
			Port:        DefaultAPIPort,
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

	data, err := safefs.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	cfg := DefaultConfig()
	defaultWatchPaths := cfg.Monitoring.Filesystem.WatchPaths
	cfg.Monitoring.Filesystem.WatchPaths = nil
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	if cfg.Monitoring.Filesystem.WatchPaths == nil {
		cfg.Monitoring.Filesystem.WatchPaths = defaultWatchPaths
	}

	return cfg, nil
}

func (c *Config) Save() error {
	homeDir, _ := os.UserHomeDir()
	path := filepath.Join(homeDir, ".config", "diu", "config.json")
	return c.SaveTo(path)
}

func (c *Config) SaveTo(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, OwnerDirectoryMode); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, PrivateFileMode); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func (c *Config) EnsureDirectories() error {
	dirs := []string{
		c.Daemon.DataDir,
		filepath.Dir(c.Daemon.PIDFile),
		filepath.Dir(c.Daemon.SocketPath),
		filepath.Dir(c.Storage.JSONFile),
		c.Monitoring.Process.WrapperDir,
	}

	for _, dir := range dirs {
		if dir != "" {
			if err := os.MkdirAll(dir, OwnerDirectoryMode); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}
	}

	return nil
}
