# DIU - Do I Use

> Package Manager Execution Tracker for macOS - Know what you actually use

[![Codecov](https://codecov.io/gh/yowainwright/diu/branch/main/graph/badge.svg)](https://codecov.io/gh/yowainwright/diu)

DIU tracks when package managers and global development tools are executed on macOS, storing execution data for analysis and auditing.

## Features

- Track package manager executions (Homebrew, npm, Go)
- Usage statistics and analytics
- JSON-based storage with automatic backups
<<<<<<< HEAD
- Pure Go CLI with local command parsing and ANSI styling
- Lightweight daemon process
- Extensible monitor system
- No third-party Go module dependencies
=======
- Beautiful CLI with Charm styling via Fang
- Lightweight daemon process
- Extensible monitor system
>>>>>>> main
- **Native macOS support** (Intel & Apple Silicon)

## Requirements

- macOS 10.15 or later
- Go 1.25+ (for building from source)

## Installation

### Using mise

```bash
mise install diu
mise use -g diu
```

### Using Homebrew

```bash
brew tap yowainwright/tap
brew install diu
```

### Using Go

```bash
go install github.com/yowainwright/diu/cmd/diu@latest
```

### From source

```bash
git clone https://github.com/yowainwright/diu
cd diu
mise run build
```

## Quick Start

1. Install wrappers and initialize storage:
```bash
diu setup
```

2. Open a new shell so the DIU wrapper path is loaded, then scan installed packages:
```bash
diu scan
```

3. Use your normal commands, then query package usage:
```bash
# Do I use jq?
diu check jq

# What haven't I used in 6 months?
diu check --unused 6m

# Show usage statistics
diu stats --top 10
```

## Commands

### Setup and Inventory

```bash
diu setup           # Install command wrappers
diu scan            # Scan installed packages into inventory
diu packages        # Show packages and last observed usage
diu check           # Search and browse packages interactively
diu check jq        # Bypass the UI and check matching packages
```

### Package Management

```bash
diu manage                                # Search, navigate, and uninstall interactively
diu manage --uninstall jq --tool homebrew --yes
diu manage --uninstall jq --tool homebrew --dry-run
```

### Daemon Management

The daemon is optional. Use it when you want the local HTTP API.

```bash
diu daemon start    # Start the API/socket daemon
diu daemon stop     # Stop the daemon
diu daemon restart  # Restart the daemon
diu daemon status   # Check daemon status
```

### Query Executions

```bash
diu query [options]
  --tool, -t        Filter by tool (brew, npm, go, etc.)
  --package, -p     Filter by package name
  --last, -l        Show executions in last duration (24h, 7d, 30d)
  --limit, -n       Limit number of results (default: 20)
  --format, -f      Output format (table, json, csv)
```

### Statistics

```bash
diu stats [options]
  --daily, -d       Show daily statistics
  --weekly, -w      Show weekly statistics
  --tool, -t        Statistics for specific tool
  --top             Show top N most used packages
```

## Configuration

Configuration is stored in `~/.config/diu/config.json`

Default configuration includes:
- Enabled tools: homebrew, npm, go
- Storage location: `~/.local/share/diu/executions.json`
- Retention: 365 days
- Automatic backups: enabled

## How It Works

DIU currently uses event-based wrapper monitoring:

1. **Inventory Scan**: Finds installed Homebrew, npm, and Go packages
2. **Executable Wrappers**: Creates lightweight wrappers for installed commands, so running `jq` records `jq`
3. **Optional Daemon**: If running, wrappers send events to the daemon; otherwise they record directly to storage
4. **Shell Integration**: Adds the wrapper directory to shell startup files so new shells use the wrappers

## Development

### Prerequisites

- macOS (Intel or Apple Silicon)
- Go 1.25+
- mise (for task running)
- Docker (optional, for E2E tests)

DIU's Go code uses only the Go standard library. Development and release tools such as mise, Docker, and GoReleaser are optional external tooling, not application dependencies.

### Setup

```bash
# Clone the repository
git clone https://github.com/yowainwright/diu
cd diu

# Install local development tools with mise
mise install

# Initialize the project
mise run setup

# Run tests
mise run test

# Build
mise run build
```

### Available Tasks (via mise)

```bash
mise tasks                    # List all available tasks
mise run test                 # Run tests
mise run lint                 # Run linters
mise run build                # Build the binary
mise run dev                  # Run in development mode
mise run release-snapshot     # Create a release snapshot
```

### Architecture

Built with:
- Go 1.25+
- Go standard library only; no third-party Go modules
- Local command parsing and terminal styling
- [mise](https://mise.jdx.dev) for task running
- [GoReleaser](https://goreleaser.com) for releases

## macOS Specific Details

DIU is optimized for macOS and handles both Intel and Apple Silicon architectures:

- **Homebrew paths**: Automatically detects `/usr/local` (Intel) and `/opt/homebrew` (Apple Silicon)
- **LaunchAgent support**: Can be configured to start at login
- **Native performance**: Compiled specifically for macOS

## License

MIT

## Author

Jeffry Wainwright (@yowainwright)
