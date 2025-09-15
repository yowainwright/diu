# DIU (Do I Use) - Package Manager Execution Tracker

## Project Overview

**Purpose:** A cross-platform tool that tracks when package managers and global development tools are executed, storing execution data in structured JSON format for analysis and auditing. Know what you actually use.

**Target Platform:** Primary focus on macOS, designed for future cross-platform expansion.

**Tagline:** "diu - do I use this package?"

## 1. System Architecture

### 1.1 High-Level Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   CLI Interface │    │   Web Dashboard  │    │  Shell Aliases  │
│     (Query)     │    │   (Optional)     │    │   (Wrappers)    │
└─────────┬───────┘    └────────┬─────────┘    └─────────┬───────┘
          │                     │                        │
          └─────────────────────┼────────────────────────┘
                                │
                    ┌───────────▼───────────┐
                    │     DIU Daemon        │
                    │   (Core Service)      │
                    └───────────┬───────────┘
                                │
                    ┌───────────▼───────────┐
                    │    Storage Engine     │
                    │    (JSON + SQLite)    │
                    └───────────────────────┘
```

### 1.2 Component Architecture

```
diu/
├── cmd/
│   ├── diu/              # Main daemon
│   ├── diu-query/        # CLI query tool
│   └── diu-setup/        # Installation/setup tool
├── internal/
│   ├── core/
│   │   ├── daemon.go     # Main daemon logic
│   │   ├── config.go     # Configuration management
│   │   └── types.go      # Core data structures
│   ├── monitors/
│   │   ├── base.go       # Monitor interface
│   │   ├── homebrew.go   # Homebrew monitor
│   │   ├── npm.go        # NPM monitor
│   │   ├── go.go         # Go toolchain monitor
│   │   └── process.go    # Process execution monitor
│   ├── storage/
│   │   ├── json.go       # JSON storage backend
│   │   ├── sqlite.go     # SQLite backend (future)
│   │   └── interface.go  # Storage interface
│   ├── wrappers/
│   │   ├── generator.go  # Shell wrapper generation
│   │   └── templates.go  # Wrapper templates
│   └── api/
│       ├── server.go     # HTTP API server
│       └── handlers.go   # API endpoints
├── pkg/
│   ├── client/
│   │   └── client.go     # Go client library
│   └── models/
│       └── execution.go  # Shared data models
├── web/                  # Web dashboard (future)
├── scripts/
│   ├── install.sh        # Installation script
│   └── wrappers/         # Generated shell wrappers
└── configs/
    └── default.yaml      # Default configuration
```

## 2. Core Data Models

### 2.1 Execution Record

```go
type ExecutionRecord struct {
    ID           string            `json:"id"`
    Tool         string            `json:"tool"`          // e.g., "homebrew", "npm"
    Command      string            `json:"command"`       // Full command executed
    Args         []string          `json:"args"`          // Command arguments
    Timestamp    time.Time         `json:"timestamp"`
    Duration     time.Duration     `json:"duration_ms"`
    ExitCode     int               `json:"exit_code"`
    WorkingDir   string            `json:"working_dir"`
    User         string            `json:"user"`
    Environment  map[string]string `json:"environment"`   // Relevant env vars
    PackagesAffected []string      `json:"packages_affected,omitempty"`
    Metadata     map[string]interface{} `json:"metadata,omitempty"`
}
```

### 2.2 Package Information

```go
type PackageInfo struct {
    Name        string    `json:"name"`
    Version     string    `json:"version"`
    Tool        string    `json:"tool"`
    InstallDate time.Time `json:"install_date"`
    LastUsed    time.Time `json:"last_used"`
    UsageCount  int       `json:"usage_count"`
    Path        string    `json:"path,omitempty"`
    Dependencies []string `json:"dependencies,omitempty"`
}
```

### 2.3 Storage Schema

```json
{
  "version": "1.0.0",
  "metadata": {
    "created": "2025-09-14T10:00:00Z",
    "last_updated": "2025-09-14T15:30:00Z",
    "hostname": "macbook-pro.local",
    "user": "username",
    "diu_version": "0.1.0"
  },
  "executions": [
    {
      "id": "exec_20250914_150001_abc123",
      "tool": "homebrew",
      "command": "brew install wget",
      "args": ["install", "wget"],
      "timestamp": "2025-09-14T15:00:01Z",
      "duration_ms": 45230,
      "exit_code": 0,
      "working_dir": "/Users/username/projects",
      "user": "username",
      "packages_affected": ["wget"],
      "metadata": {
        "brew_version": "4.1.15",
        "formulae_updated": ["wget"]
      }
    }
  ],
  "packages": {
    "homebrew": {
      "wget": {
        "name": "wget",
        "version": "1.21.3",
        "tool": "homebrew",
        "install_date": "2025-09-14T15:00:01Z",
        "last_used": "2025-09-14T15:00:01Z",
        "usage_count": 1,
        "path": "/usr/local/bin/wget"
      }
    },
    "npm": {},
    "go": {}
  },
  "statistics": {
    "total_executions": 1,
    "tools_used": ["homebrew"],
    "most_active_day": "2025-09-14",
    "execution_frequency": {
      "homebrew": 1,
      "npm": 0
    }
  }
}
```

## 3. Monitoring Strategies

### 3.1 Monitor Interface

```go
type Monitor interface {
    Name() string
    Initialize(config *Config) error
    Start(ctx context.Context, eventChan chan<- ExecutionRecord) error
    Stop() error
    GetInstalledPackages() ([]PackageInfo, error)
    ParseCommand(cmd string, args []string) (*ExecutionRecord, error)
}
```

### 3.2 Monitoring Methods

#### Method 1: Process Monitoring (Primary)
- Monitor process execution via OS APIs
- Use exec.Command wrappers
- **Pros:** Accurate, captures all executions
- **Cons:** Requires wrapper installation

#### Method 2: File System Monitoring (Secondary)
- Watch package manager directories for changes
- Monitor binary modification times
- **Pros:** Passive monitoring, no wrappers needed
- **Cons:** Less accurate, misses failed executions

#### Method 3: Shell Hook Integration (Optional)
- Integrate with shell preexec hooks
- zsh/bash integration
- **Pros:** Captures exact command lines
- **Cons:** Shell-dependent, user setup required

### 3.3 Package Manager Specific Monitoring

#### Homebrew Monitor

```go
type HomebrewMonitor struct {
    binaryPath    string
    cellarPath    string
    formulaeCache map[string]PackageInfo
}

// Monitors:
// - /usr/local/bin/brew executions
// - /usr/local/Cellar/ changes
// - brew --cache directory
```

#### NPM Monitor

```go
type NPMMonitor struct {
    globalPath    string
    packageJSON   string
    binPath       string
}

// Monitors:
// - npm global commands
// - ~/.npm/ directory changes
// - Global node_modules
```

## 4. Configuration System

### 4.1 Configuration Structure

```yaml
# ~/.config/diu/config.yaml
version: "1.0"

# Core settings
daemon:
  port: 8080
  log_level: "info"
  data_dir: "~/.local/share/diu"
  pid_file: "/tmp/diu.pid"

# Storage configuration
storage:
  backend: "json"  # json, sqlite
  json_file: "~/.local/share/diu/executions.json"
  backup_enabled: true
  backup_interval: "24h"
  retention_days: 365

# Monitoring configuration
monitoring:
  enabled_tools:
    - homebrew
    - npm
    - go
    - pip
    - gem
    - cargo

  # Method priorities: process, filesystem, hooks
  methods: ["process", "filesystem"]

  # Process monitoring
  process:
    wrapper_dir: "~/.local/bin/diu-wrappers"
    auto_install_wrappers: true

  # File system monitoring
  filesystem:
    scan_interval: "30s"
    watch_paths:
      homebrew: ["/usr/local/bin", "/opt/homebrew/bin"]
      npm: ["~/.npm/bin", "/usr/local/lib/node_modules"]

# Tool-specific configurations
tools:
  homebrew:
    cellar_paths: ["/usr/local/Cellar", "/opt/homebrew/Cellar"]
    track_casks: true
    track_services: true

  npm:
    track_global_only: true
    ignore_dev_dependencies: true

  go:
    gopath: "$GOPATH"
    gobin: "$GOBIN"

# API configuration
api:
  enabled: true
  host: "127.0.0.1"
  port: 8081
  cors_enabled: false

# Reporting
reporting:
  daily_summary: true
  weekly_summary: true
  email_reports: false
```

## 5. Implementation Specification

### 5.1 Phase 1: Core Foundation (v0.1.0)

**Timeline:** 2-3 weeks

**Deliverables:**
- ✓ Basic daemon architecture
- ✓ JSON storage backend
- ✓ Homebrew monitor (process wrapper method)
- ✓ NPM monitor (process wrapper method)
- ✓ CLI query interface
- ✓ Configuration system
- ✓ Installation script

**Key Features:**

```bash
# Installation
curl -sSL install.diu.dev | bash

# Start daemon
diu daemon start

# Query executions
diu query --tool homebrew --last 24h
diu query --package wget --usage-count
diu stats --weekly
```

### 5.2 Phase 2: Enhanced Monitoring (v0.2.0)

**Timeline:** 2-3 weeks

**Deliverables:**
- ✓ Go toolchain monitor
- ✓ Python pip/pipx monitor
- ✓ File system monitoring fallback
- ✓ Shell hook integration
- ✓ Package dependency tracking
- ✓ Export functionality (CSV, JSON)

### 5.3 Phase 3: Advanced Features (v0.3.0)

**Timeline:** 3-4 weeks

**Deliverables:**
- ✓ Web dashboard
- ✓ HTTP API
- ✓ SQLite backend option
- ✓ Advanced analytics
- ✓ Report generation
- ✓ Multi-user support

### 5.4 Phase 4: Platform Expansion (v0.4.0)

**Timeline:** 4-5 weeks

**Deliverables:**
- ✓ Linux support
- ✓ Windows support (Chocolatey, winget)
- ✓ Docker integration
- ✓ CI/CD integrations
- ✓ Cloud sync capabilities

## 6. Technical Specifications

### 6.1 Performance Requirements

- **Startup Time:** < 100ms daemon startup
- **Memory Usage:** < 50MB steady state
- **CPU Impact:** < 1% during monitoring
- **Storage Growth:** ~1MB per 1000 executions
- **Query Response:** < 50ms for basic queries

### 6.2 Reliability Requirements

- **Uptime:** 99.9% daemon availability
- **Data Integrity:** Atomic writes, backup on corruption
- **Error Recovery:** Auto-restart on crashes
- **Graceful Degradation:** Continue without failed monitors

### 6.3 Security Considerations

- **Permissions:** Run with minimal required permissions
- **Data Privacy:** No sensitive command arguments logged
- **Network Security:** Local-only API by default
- **File Security:** Secure storage permissions (600)

### 6.4 Development Standards

- **Language:** Go 1.21+
- **Dependencies:** Minimal external dependencies
- **Testing:** >90% test coverage
- **Documentation:** Comprehensive godoc
- **CI/CD:** GitHub Actions for testing/releases
- **Versioning:** Semantic versioning

## 7. API Specification

### 7.1 REST API Endpoints

```
GET    /api/v1/executions              # List executions
GET    /api/v1/executions/{id}         # Get specific execution
POST   /api/v1/executions              # Add execution (internal)
GET    /api/v1/packages                # List packages
GET    /api/v1/packages/{tool}/{name}  # Get package details
GET    /api/v1/tools                   # List monitored tools
GET    /api/v1/stats                   # Get statistics
GET    /api/v1/health                  # Health check
```

### 7.2 CLI Interface

```bash
# Daemon management
diu daemon [start|stop|restart|status]

# Querying
diu query [options]
  --tool string        Filter by tool (brew, npm, go, etc.)
  --package string     Filter by package name
  --since duration     Show executions since duration ago
  --last duration      Show executions in last duration
  --limit int          Limit number of results
  --format string      Output format (table, json, csv)

# Statistics
diu stats [options]
  --daily             Show daily statistics
  --weekly            Show weekly statistics
  --tool string       Statistics for specific tool
  --top int           Show top N most used packages

# Package information
diu packages [options]
  --tool string       Filter by tool
  --unused duration   Show packages not used in duration
  --size             Include size information

# Configuration
diu config [get|set|list]
diu config set daemon.log_level debug
diu config get monitoring.enabled_tools

# Maintenance
diu cleanup          # Clean old executions based on retention
diu backup           # Create manual backup
diu restore FILE     # Restore from backup
diu export FILE      # Export data
```

## 8. Testing Strategy

### 8.1 Unit Testing

- All core functions unit tested
- Mock file system operations
- Mock process executions
- Configuration validation tests

### 8.2 Integration Testing

- End-to-end execution tracking
- Storage backend testing
- API endpoint testing
- Multi-tool monitoring scenarios

### 8.3 Performance Testing

- Memory leak detection
- CPU usage profiling
- Storage growth testing
- Concurrent execution handling

### 8.4 Platform Testing

- macOS versions (10.15+)
- Different shell environments
- Package manager versions
- Permission scenarios

## 9. Deployment and Distribution

### 9.1 Installation Methods

#### Primary: Shell Script Install

```bash
curl -sSL https://install.diu.dev | bash
```

#### Homebrew Formula

```bash
brew install diu/tap/diu
```

#### Direct Binary Download

```bash
# GitHub releases
wget https://github.com/username/diu/releases/latest/download/diu-darwin-amd64
```

### 9.2 Package Structure

```
diu-0.1.0-darwin-amd64/
├── bin/
│   ├── diu                # Main binary
│   ├── diu-query         # Query CLI
│   └── diu-setup         # Setup utility
├── configs/
│   └── default.yaml      # Default configuration
├── scripts/
│   ├── install.sh        # Installation script
│   └── diu.plist         # macOS LaunchAgent
└── docs/
    ├── README.md
    └── USAGE.md
```

## 10. Future Enhancements

### 10.1 Advanced Analytics

- Machine learning usage prediction
- Anomaly detection for unusual package usage
- Performance impact analysis
- Security vulnerability tracking

### 10.2 Integration Capabilities

- IDE plugins (VS Code, JetBrains)
- CI/CD pipeline integration
- Slack/Discord notifications
- Prometheus metrics export

### 10.3 Enterprise Features

- Team collaboration features
- Centralized logging
- Policy enforcement
- Audit trail compliance

## 11. Branding & Messaging

### 11.1 Core Value Proposition

"Stop wondering what you actually use. DIU tracks your package manager activity so you know which tools matter."

### 11.2 Use Cases

- **Developers:** "Which brew packages can I safely uninstall?"
- **Teams:** "What tools does our team actually depend on?"
- **DevOps:** "Audit package usage across development environments"
- **Personal:** "Clean up my development environment efficiently"

### 11.3 Key Commands

```bash
# The question that inspired the tool
diu query --package docker --last 90d  # Do I use docker?

# Quick cleanup insights
diu packages --unused 6m  # What haven't I used in 6 months?

# Team insights
diu stats --top 10  # What are our most-used tools?
```