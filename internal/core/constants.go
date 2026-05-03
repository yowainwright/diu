package core

import (
	"strings"
	"time"
)

const (
	Version       = "0.1.0"
	ConfigVersion = "1.0"

	ToolHomebrew = "homebrew"
	ToolNPM      = "npm"
	ToolGo       = "go"
	ToolPip      = "pip"
	ToolGem      = "gem"
	ToolCargo    = "cargo"
	ToolGoBinary = "go-binary"

	DefaultDaemonPort      = 8080
	DefaultAPIPort         = 8081
	DefaultAPIHost         = "127.0.0.1"
	DefaultLogLevel        = "info"
	DefaultRetentionDays   = 365
	DefaultEventBuffer     = 100
	DefaultShutdownTimeout = 5 * time.Second

	DefaultPIDFile    = "/tmp/diu.pid"
	DefaultSocketPath = "/tmp/diu.sock"

	StorageBackendJSON = "json"

	MonitorMethodProcess    = "process"
	MonitorMethodFilesystem = "filesystem"
)

var (
	DefaultEnabledTools = []string{
		ToolHomebrew,
		ToolNPM,
		ToolGo,
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

func NormalizeToolName(tool string) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "brew":
		return ToolHomebrew
	case "golang":
		return ToolGo
	default:
		return strings.ToLower(strings.TrimSpace(tool))
	}
}
