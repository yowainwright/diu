# DIU .oO(...)

[![Codecov](https://codecov.io/gh/yowainwright/diu/branch/main/graph/badge.svg)](https://codecov.io/gh/yowainwright/diu)

## Do I Use?

> Know which global development tools you **actually** use

DIU tracks package-manager commands and global CLI tools from Homebrew, npm, pnpm, Bun, Go, pip, uv, and Poetry. It keeps a small local JSON inventory so you can answer questions like:

- Did I use `jq` recently?
- Which global JavaScript or Python packages have I not touched in months?
- What are my most-used command-line tools?
- What would DIU uninstall before I actually run it?

DIU is macOS-first, written in Go, and uses only the Go standard library at runtime.

## Supported Managers

| Ecosystem | Managers | What DIU tracks |
| --- | --- | --- |
| macOS | Homebrew | Formulae, casks, and wrapped executables. |
| JavaScript | npm, pnpm, Bun | Global packages and their command usage. |
| Go | Go | Installed binaries in `GOBIN` or `GOPATH/bin`. |
| Python | pip, uv, Poetry | pip packages, uv tools, and Poetry command/plugin usage. |

## Quick Start

```bash
# Install command wrappers and create local storage
diu setup

# Open a new shell so the wrapper path is active
exec "$SHELL" -l

# Scan currently installed global tools
diu scan

# Use your tools normally
jq --version
npm --version
uv tool run ruff --version

# Ask DIU what it has seen
diu check jq
diu stats --weekly --top 5
```

## Install

```bash
# Homebrew
brew install yowainwright/tap/diu

# Go
go install github.com/yowainwright/diu/cmd/diu@latest
```

From source:

```bash
git clone https://github.com/yowainwright/diu
cd diu
mise run build
```

## Common Examples

Check a package:

```bash
diu check jq
```

Example output:

```text
homebrew  jq  used 12 times  last: 2026-06-20
```

Find packages that have not been used recently:

```bash
diu packages --unused 6mo
diu check --unused 90d --format csv
```

Review recent executions:

```bash
diu query --last 7d --limit 10
diu query --tool npm --package eslint --format json
diu query --tool uv --last 24h
```

Preview an uninstall command before running it:

```bash
diu manage --uninstall jq --tool homebrew --dry-run
# brew uninstall jq
```

Uninstall after confirmation:

```bash
diu manage --uninstall jq --tool homebrew
```

Skip confirmation when scripting:

```bash
diu manage --uninstall typescript --tool npm --yes
diu manage --uninstall tsx --tool pnpm --yes
diu manage --uninstall ruff --tool pip --yes
```

## How It Works

`diu setup` installs lightweight wrappers in `~/.local/bin/diu-wrappers` and adds that directory to existing shell config files when possible. The wrapper runs the original command, preserves its output and exit code, then records the execution in the background.

```mermaid
sequenceDiagram
    participant You
    participant Wrapper as DIU wrapper
    participant Tool as Original tool
    participant Recorder as DIU recorder
    participant Store as Local JSON store

    You->>Wrapper: jq --version
    Wrapper->>Tool: jq --version
    Tool-->>Wrapper: output and exit code
    Wrapper-->>You: same output and exit code
    Wrapper->>Recorder: execution event
    Recorder->>Store: append usage data
```

The daemon is optional. When it is running, wrappers send events to a local Unix socket. When it is not running, wrappers fall back to `diu record`.

```mermaid
flowchart LR
    command["brew / npm / pnpm / bun / go / pip / uv / poetry / wrapped executable"] --> wrapper["DIU wrapper"]
    wrapper --> original["Original executable"]
    wrapper --> daemon{"Daemon running?"}
    daemon -- yes --> socket["Unix socket"]
    daemon -- no --> record["diu record"]
    socket --> storage[("~/.local/share/diu/executions.json")]
    record --> storage
    scan["diu scan"] --> inventory["Package inventory"]
    inventory --> storage
    storage --> cli["check / query / stats / packages / manage"]
```

## Commands

| Command | Use it for |
| --- | --- |
| `diu setup` | Create config, storage, shell path entries, and wrappers. |
| `diu scan` | Refresh the known package inventory. |
| `diu check [search]` | Search tracked packages and see usage. |
| `diu packages` | List tracked packages, optionally filtered by tool or unused duration. |
| `diu query` | Show recorded executions. |
| `diu stats` | Summarize usage by time range, tool, and top packages. |
| `diu manage` | Search packages and uninstall them interactively or by flag. |
| `diu daemon start` | Start the optional local recorder/API daemon. |
| `diu config list` | Print the resolved config as JSON. |
| `diu cleanup` | Apply retention and storage limits. |
| `diu backup` | Create a manual JSON storage backup. |

Useful filters:

```bash
diu check rip --tool homebrew --limit 5
diu packages --tool npm
diu packages --tool pip
diu packages --unused 30d
diu query --tool poetry --last 24h --format csv
diu stats --daily
diu stats --tool uv --top 20
```

## Local API

The local API is unauthenticated and intended for local development use. Keep `api.host` bound to `127.0.0.1` unless you deliberately want other processes on your network to reach it.

Start the daemon:

```bash
diu daemon start
```

Default base URL:

```text
http://127.0.0.1:8081/api/v1
```

Examples:

```bash
curl http://127.0.0.1:8081/api/v1/health
curl "http://127.0.0.1:8081/api/v1/executions?tool=homebrew&limit=10"
curl "http://127.0.0.1:8081/api/v1/packages?tool=pnpm"
curl http://127.0.0.1:8081/api/v1/stats
```

Record an event manually:

```bash
curl -X POST http://127.0.0.1:8081/api/v1/executions \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "uv",
    "command": "uv tool install ruff",
    "args": ["tool", "install", "ruff"],
    "exit_code": 0,
    "duration_ms": 5432,
    "user": "jeff"
  }'
```

## Files

| Path | Purpose |
| --- | --- |
| `~/.config/diu/config.json` | User config. |
| `~/.local/share/diu/executions.json` | Execution history, package inventory, and stats. |
| `~/.local/share/diu/diu.pid` | Daemon PID file. |
| `~/.local/share/diu/diu.sock` | Daemon Unix socket. |
| `~/.local/bin/diu-wrappers` | Generated command wrappers. |

Common config edits:

```bash
diu config get storage.json_file
diu config set storage.retention_days 180
diu config set monitoring.enabled_tools homebrew,npm,pnpm,bun,go,pip,uv,poetry
diu config list
```

## Troubleshooting

```bash
# The wrapper path is not active in this shell
exec "$SHELL" -l

# Rebuild wrappers after installing new global tools
diu setup
diu scan

# Check daemon state
diu daemon status

# Run the daemon in the foreground for logs
DIU_DAEMON_FOREGROUND=1 diu daemon start
```

## Development

```bash
mise install
mise run setup
mise run test
mise run build
```

Release checks:

<!-- release flow from release automation config -->

```bash
mise run release-preview
```

Release Please opens release PRs from conventional commits on `main`. Merging
the release PR updates the changelog and version, creates the `v*` tag, and
lets the tag workflow publish the GitHub Release, GoReleaser artifacts, and
Homebrew formula.

Required for full automation:

- `RELEASE_PLEASE_TOKEN`
- `HOMEBREW_TAP_GITHUB_TOKEN`

## Requirements

- macOS 10.15 or later
- Go 1.25+ when building from source

## License

MIT

## Author

Jeffry Wainwright ([@yowainwright](https://github.com/yowainwright))
