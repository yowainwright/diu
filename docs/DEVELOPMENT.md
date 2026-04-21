# DIU Development Guide

## Architecture Overview

DIU is a CLI-first tool with shell integration and local JSON storage:

```
┌──────────────────────────────────────────────────────┐
│                    CLI Interface                      │
│                  (Cobra + Fang)                       │
├──────────────────────────────────────────────────────┤
│               Shell Hook Management                   │
│          (setup, teardown, interactive shells)        │
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
├── integration/           # Process-level integration tests
├── internal/              # Private packages
│   ├── core/             # Core types and config
│   ├── monitors/         # Package manager monitors
│   ├── shell/            # Shell hook setup and teardown
│   ├── storage/          # Storage implementations
│   └── wrappers/         # Wrapper generation
├── pkg/                   # Public packages
│   └── models/           # Shared data models
├── e2e/                   # Docker-only black-box tests
├── docs/                  # Documentation
├── configs/              # Configuration files
├── Dockerfile.e2e         # Docker E2E runner
└── docker-compose.yml     # Docker E2E entrypoint
```

## Getting Started

### Prerequisites

- Go 1.25 or higher
- Docker with Compose support (for E2E tests)
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

1. Build the binary:
```bash
go build -o diu ./cmd/diu
```

2. Exercise the CLI locally:
```bash
./diu setup
./diu scan
./diu list
./diu stats
```

### Running with Docker

Run Docker E2E tests:
```bash
docker compose run --build --rm e2e
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

Update the CLI scan wiring in `cmd/diu/main.go` so the new monitor is included in discovery flows.

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
go test ./integration/...
```

### E2E Tests

Run Docker E2E tests:
```bash
docker compose run --build --rm e2e
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

### Inspecting Storage

View the current storage file:
```bash
cat ~/.local/share/diu/diu.json | jq .
```

### Inspecting Config

View the current config:
```bash
diu config list
```

### Inspecting Shell Hooks

Check hook status:
```bash
diu status
```

Review injected shell config:
```bash
sed -n '/# diu shell hooks/,/# end diu shell hooks/p' ~/.zshrc
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

#### Hooks not recording commands
- Restart or re-source the shell after `diu setup`
- Confirm the shell config contains the `# diu shell hooks` block
- Verify `diu` is on `PATH` inside the interactive shell session

#### Scan not finding packages
- Check the underlying tool is on `PATH`
- Run `diu scan --tool <tool>` to isolate one monitor at a time
- Inspect `diu config list` to confirm the tool is enabled

#### Storage looks wrong
- Inspect `~/.local/share/diu/diu.json`
- Remove the file and rerun `diu scan` to rebuild local state

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
