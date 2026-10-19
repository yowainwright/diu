package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

var Version = "dev"

func CurrentVersion() string {
	moduleVersion := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		moduleVersion = info.Main.Version
	}
	return resolveVersion(Version, moduleVersion)
}

func resolveVersion(linkedVersion, moduleVersion string) string {
	hasLinkedVersion := linkedVersion != "" && linkedVersion != "dev"
	if hasLinkedVersion {
		return linkedVersion
	}
	hasModuleVersion := moduleVersion != "" && moduleVersion != "(devel)"
	if hasModuleVersion {
		return strings.TrimPrefix(moduleVersion, "v")
	}
	return "dev"
}

const (
	ConfigVersion = "1.0"

	ToolHomebrew     = "homebrew"
	ToolNPM          = "npm"
	ToolPNPM         = "pnpm"
	ToolBun          = "bun"
	ToolGo           = "go"
	ToolCargo        = "cargo"
	ToolPipx         = "pipx"
	ToolPip          = "pip"
	ToolUV           = "uv"
	ToolPoetry       = "poetry"
	ToolGoBinary     = "go-binary"
	ToolHomebrewCask = "homebrew-cask"
	ToolExecutable   = "executable"

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

	MaxRecorderWorkers      = 4
	RecorderTimeout         = 2 * time.Second
	MaxRecorderPayloadBytes = 1024 * 1024
)

func NormalizeToolName(tool string) string {
	normalized := strings.ToLower(strings.TrimSpace(tool))
	switch normalized {
	case "brew":
		return ToolHomebrew
	case "golang":
		return ToolGo
	case "pip3", "python", "python3":
		return ToolPip
	default:
		return normalized
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

func RecorderSlotPath(dataDir string, slot int) string {
	name := fmt.Sprintf("recorder-%d.lock", slot)
	return filepath.Join(dataDir, name)
}
