package core

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

var Version = "0.1.1"

const (
	ConfigVersion = "1.0"

	ToolHomebrew = "homebrew"
	ToolNPM      = "npm"
	ToolPNPM     = "pnpm"
	ToolBun      = "bun"
	ToolGo       = "go"
	ToolPip      = "pip"
	ToolUV       = "uv"
	ToolPoetry   = "poetry"
	ToolGem      = "gem"
	ToolCargo    = "cargo"
	ToolGoBinary = "go-binary"

	DefaultDaemonPort        = 8080
	DefaultAPIPort           = 8081
	DefaultAPIHost           = "127.0.0.1"
	DefaultLogLevel          = "info"
	DefaultRetentionDays     = 365
	DefaultMaxExecutions     = 50000
	DefaultMaxStorageBytes   = 10 * 1024 * 1024
	DefaultMaxBackups        = 7
	DefaultEventBuffer       = 100
	DefaultShutdownTimeout   = 5 * time.Second
	DefaultSocketReadTimeout = 30 * time.Second

	OwnerDirectoryMode  = 0o700
	PrivateFileMode     = 0o600
	OwnerExecutableMode = 0o700
	ExecutableModeMask  = 0o111

	DefaultPIDFileName    = "diu.pid"
	DefaultSocketFileName = "diu.sock"

	StorageBackendJSON = "json"

	MonitorMethodProcess    = "process"
	MonitorMethodFilesystem = "filesystem"
)

var (
	DefaultEnabledTools = []string{
		ToolHomebrew,
		ToolNPM,
		ToolPNPM,
		ToolBun,
		ToolGo,
		ToolPip,
		ToolUV,
		ToolPoetry,
	}

	DefaultMonitorMethods = []string{
		MonitorMethodProcess,
	}

	HomebrewCellarPaths = []string{
		"/usr/local/Cellar",
		"/opt/homebrew/Cellar",
	}

	HomebrewBinPaths = []string{
		"/usr/local/bin",
		"/opt/homebrew/bin",
	}
)

func ShellEscapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `$`, `\$`)
	s = strings.ReplaceAll(s, "`", "\\`")
	return s
}

func NormalizeToolName(tool string) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "brew":
		return ToolHomebrew
	case "golang":
		return ToolGo
	case "pip3", "python", "python3":
		return ToolPip
	default:
		return strings.ToLower(strings.TrimSpace(tool))
	}
}

func DefaultDataDir() string {
	homeDir := os.Getenv("HOME")
	if dir, err := os.UserHomeDir(); err == nil {
		homeDir = dir
	}
	if homeDir == "" {
		return filepath.Join(os.TempDir(), "diu")
	}
	return filepath.Join(homeDir, ".local", "share", "diu")
}

func DefaultPIDFilePath(dataDir string) string {
	return filepath.Join(dataDir, DefaultPIDFileName)
}

func DefaultSocketPath(dataDir string) string {
	return filepath.Join(dataDir, DefaultSocketFileName)
}
