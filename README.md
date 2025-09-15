# DIU - Do I Use

> Package Manager Execution Tracker for macOS - Know what you actually use

DIU tracks when package managers and global development tools are executed on macOS, storing execution data for analysis and auditing.

## Features

- 🔍 Track package manager executions (Homebrew, npm, Go, pip, gem, cargo)
- 📊 Usage statistics and analytics
- 🗂️ JSON-based storage with automatic backups
- 🎨 Beautiful CLI with Charm styling via Fang
- 🚀 Lightweight daemon process
- 🔌 Extensible monitor system
- 🍎 **Native macOS support** (Intel & Apple Silicon)

## Requirements

- macOS 10.15 or later
- Go 1.22+ (for building from source)

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

1. Start the DIU daemon:
```bash
diu daemon start
```

2. Check daemon status:
```bash
diu daemon status
```

3. Query your package usage:
```bash
# Do I use docker?
diu query --package docker --last 90d

# What haven't I used in 6 months?
diu packages --unused 6m

# Show usage statistics
diu stats --top 10
```

## Commands

### Daemon Management

```bash
diu daemon start    # Start the daemon
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

### Package Management

```bash
diu packages [options]
  --tool, -t        Filter by tool
  --unused, -u      Show packages not used in duration
```

## Configuration

Configuration is stored in `~/.config/diu/config.json`

Default configuration includes:
- Enabled tools: homebrew, npm, go, pip, gem, cargo
- Storage location: `~/.local/share/diu/executions.json`
- Retention: 365 days
- Automatic backups: enabled

## How It Works

DIU uses multiple monitoring strategies:

1. **Process Monitoring**: Creates lightweight wrapper scripts that track executions
2. **File System Monitoring**: Watches package directories for changes
3. **Shell Integration**: Optional shell hooks for command tracking

## Development

### Prerequisites

- macOS (Intel or Apple Silicon)
- Go 1.22+
- mise (for task running)
- Docker (optional, for E2E tests)

### Setup

```bash
# Clone the repository
git clone https://github.com/yowainwright/diu
cd diu

# Install dependencies with mise
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
- Go 1.22+
- [Cobra](https://github.com/spf13/cobra) for CLI
- [Fang](https://github.com/charmbracelet/fang) for beautiful CLI styling
- [Lipgloss](https://github.com/charmbracelet/lipgloss) for terminal UI
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
