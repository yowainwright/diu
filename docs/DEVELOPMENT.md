# DIU Development Guide

## Architecture Overview

DIU follows a modular architecture with clear separation of concerns:

```
┌──────────────────────────────────────────────────────┐
│                    CLI Interface                      │
│                  (Cobra + Fang)                       │
├──────────────────────────────────────────────────────┤
│                    Daemon Core                        │
│              (Event Processing, API)                  │
├──────────────────────────────────────────────────────┤
│                Monitor Registry                       │
│         (Homebrew, NPM, Go, Pip, etc.)               │
├──────────────────────────────────────────────────────┤
│                 Storage Layer                         │
│              (JSON, SQLite future)                    │
└──────────────────────────────────────────────────────┘
```

## Project Structure

```
diu/
├── cmd/                    # Entry points
│   └── diu/               # Main CLI application
├── internal/              # Private packages
│   ├── core/             # Core types and config
│   ├── daemon/           # Daemon service
│   ├── monitors/         # Package manager monitors
│   ├── storage/          # Storage implementations
│   └── wrappers/         # Wrapper generation
├── pkg/                   # Public packages
│   └── models/           # Shared data models
├── e2e/                   # End-to-end tests
├── docs/                  # Documentation
├── configs/              # Configuration files
└── scripts/              # Build and install scripts
```

## Getting Started

### Prerequisites

- Go 1.22 or higher
- Docker and Docker Compose (for E2E tests)
- Make (optional, for convenience commands)

### Setup

1. Clone the repository:
```bash
git clone https://github.com/yowainwright/diu
cd diu
```

2. Install dependencies:
```bash
go mod download
```

3. Build the project:
```bash
go build -o diu ./cmd/diu
```

4. Run tests:
```bash
go test ./...
```

## Development Workflow

### Running Locally

1. Start the daemon:
```bash
./diu daemon start
```

2. In another terminal, interact with the CLI:
```bash
./diu query --last 24h
./diu stats
```

### Running with Docker

1. Build and start services:
```bash
docker-compose up --build
```

2. Run E2E tests:
```bash
docker-compose --profile e2e up e2e-tests
```

3. Access the API:
```bash
curl http://localhost:8081/api/v1/health
```

## Adding a New Monitor

To add support for a new package manager:

### 1. Create the Monitor Implementation

Create `internal/monitors/newpm.go`:

```go
package monitors

import (
    "context"
    "github.com/yowainwright/diu/internal/core"
)

type NewPMMonitor struct {
    *ProcessMonitor
    // Add specific fields
}

func NewNewPMMonitor() Monitor {
    return &NewPMMonitor{
        ProcessMonitor: NewProcessMonitor("newpm", "newpm"),
    }
}

func (m *NewPMMonitor) Initialize(config *core.Config) error {
    if err := m.ProcessMonitor.Initialize(config); err != nil {
        return err
    }
    // Add specific initialization
    return nil
}

func (m *NewPMMonitor) ParseCommand(cmd string, args []string) (*core.ExecutionRecord, error) {
    record := &core.ExecutionRecord{
        Tool:     "newpm",
        Command:  cmd,
        Args:     args,
        Metadata: make(map[string]interface{}),
    }

    // Parse command-specific logic
    // Extract packages affected
    // Set metadata

    return record, nil
}

func (m *NewPMMonitor) GetInstalledPackages() ([]*core.PackageInfo, error) {
    // Implement package discovery
    return nil, nil
}
```

### 2. Register the Monitor

Update `internal/daemon/daemon.go`:

```go
case "newpm":
    monitor = monitors.NewNewPMMonitor()
```

### 3. Add Configuration

Update `internal/core/config.go` to add tool-specific config if needed.

### 4. Write Tests

Create `internal/monitors/newpm_test.go`:

```go
func TestNewPMMonitor(t *testing.T) {
    monitor := NewNewPMMonitor()
    // Test initialization
    // Test command parsing
    // Test package discovery
}
```

## Testing

### Unit Tests

Run all unit tests:
```bash
go test ./internal/... ./pkg/...
```

Run with coverage:
```bash
go test -cover ./...
```

Generate coverage report:
```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Integration Tests

Run integration tests:
```bash
go test ./internal/monitors -tags=integration
```

### E2E Tests

Run E2E tests with Docker:
```bash
docker-compose --profile e2e up --build e2e-tests
```

Run E2E tests locally (requires running daemon):
```bash
go test ./e2e/...
```

### Test Data

Test fixtures are located in `testdata/` directories within each package.

## Code Style

### Go Standards

- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Use `gofmt` for formatting
- Use `golint` for linting
- Use `go vet` for static analysis

### Pre-commit Checks

Run before committing:
```bash
gofmt -w .
go vet ./...
golangci-lint run
go test ./...
```

### Naming Conventions

- Interfaces: Suffix with `-er` (e.g., `Monitor`, `Storage`)
- Implementations: Descriptive names (e.g., `HomebrewMonitor`, `JSONStorage`)
- Test files: `*_test.go`
- Test functions: `Test<FunctionName>`

## Debugging

### Enable Debug Logging

Set log level in config:
```json
{
  "daemon": {
    "log_level": "debug"
  }
}
```

Or via environment variable:
```bash
DIU_LOG_LEVEL=debug diu daemon start
```

### Inspecting Storage

View storage file:
```bash
cat ~/.local/share/diu/executions.json | jq .
```

Query specific executions:
```bash
diu query --format json | jq '.[] | select(.tool == "npm")'
```

### API Debugging

Use curl with verbose output:
```bash
curl -v http://localhost:8081/api/v1/health
```

Monitor API logs:
```bash
docker-compose logs -f diu
```

## Performance Profiling

### CPU Profiling

```go
import _ "net/http/pprof"

// In daemon.go
go func() {
    log.Println(http.ListenAndServe("localhost:6060", nil))
}()
```

Then profile:
```bash
go tool pprof http://localhost:6060/debug/pprof/profile
```

### Memory Profiling

```bash
go tool pprof http://localhost:6060/debug/pprof/heap
```

## Building for Release

### Local Build

```bash
go build -ldflags="-s -w" -o diu ./cmd/diu
```

### Cross-platform Build

```bash
# macOS
GOOS=darwin GOARCH=amd64 go build -o diu-darwin-amd64 ./cmd/diu
GOOS=darwin GOARCH=arm64 go build -o diu-darwin-arm64 ./cmd/diu

# Linux
GOOS=linux GOARCH=amd64 go build -o diu-linux-amd64 ./cmd/diu
GOOS=linux GOARCH=arm64 go build -o diu-linux-arm64 ./cmd/diu

# Windows
GOOS=windows GOARCH=amd64 go build -o diu-windows-amd64.exe ./cmd/diu
```

### Using GoReleaser

```bash
goreleaser release --snapshot --clean
```

## Contributing

### Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Add tests for new functionality
5. Ensure all tests pass
6. Update documentation if needed
7. Commit your changes (`git commit -m 'Add amazing feature'`)
8. Push to the branch (`git push origin feature/amazing-feature`)
9. Open a Pull Request

### Commit Message Format

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

<body>

<footer>
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `test`: Test changes
- `refactor`: Code refactoring
- `perf`: Performance improvements
- `ci`: CI/CD changes
- `chore`: Other changes

Example:
```
feat(monitors): add support for cargo package manager

- Implement CargoMonitor with parse command support
- Add package discovery for installed crates
- Include Cargo.toml parsing for dependencies

Closes #123
```

## Troubleshooting

### Common Issues

#### Daemon won't start
- Check if port 8080/8081 is already in use
- Verify PID file doesn't exist: `rm /tmp/diu.pid`
- Check logs: `diu daemon start --log-level=debug`

#### Wrappers not working
- Ensure wrapper directory is in PATH
- Check wrapper permissions: `ls -la ~/.local/bin/diu-wrappers/`
- Regenerate wrappers: `diu setup wrappers`

#### Storage corruption
- Backup current data: `diu backup`
- Reset storage: `rm ~/.local/share/diu/executions.json`
- Restore from backup if needed: `diu restore <backup-file>`

### Getting Help

- Check existing issues: https://github.com/yowainwright/diu/issues
- Join discussions: https://github.com/yowainwright/diu/discussions
- Read the docs: https://github.com/yowainwright/diu/wiki

## Resources

- [Go Documentation](https://golang.org/doc/)
- [Cobra Documentation](https://github.com/spf13/cobra)
- [Fang Documentation](https://github.com/charmbracelet/fang)
- [Lipgloss Documentation](https://github.com/charmbracelet/lipgloss)
- [Docker Documentation](https://docs.docker.com/)