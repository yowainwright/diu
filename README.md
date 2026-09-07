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

# Start the optional recorder, recommended for parallel workflows
diu daemon start

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

<!-- Package row format derived from cmd/diu/diu_packages.go -->
Example output:

```text
1    homebrew        jq                                  12 uses    2026-06-20
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

Remove DIU's wrappers and shell PATH entries:

```bash
diu uninstall
```

This preserves DIU's configuration and usage history. Remove the binary separately
with Homebrew or Go after running the command.

## How It Works

`diu setup` installs lightweight wrappers in `~/.local/bin/diu-wrappers` and adds that directory to existing shell config files when possible. Each wrapper runs the original command and preserves its output and exit code.

<!-- Wrapper execution sequence derived from cmd/diu/diu_setup.go and internal/monitors/monitors_process.go -->
After the original command finishes, the wrapper records its execution:

```text
command -> DIU wrapper -> original tool -> output to your terminal
               |
               +-- daemon available --> send event in the background
               |
               +-- daemon absent ----> run diu record synchronously
               |
               `--> return the original exit code
```

The daemon is optional. When available, wrappers send events through a local Unix socket. A failed socket send also falls back to `diu record` in that background task.

<!-- Fallback wait policy derived from cmd/diu/diu_setup.go and internal/storage/storage_json.go -->
**Daemon-off tracking is best-effort.** The wrapper waits for `diu record` before returning. Recording has a shared 50 ms lock-wait budget; metadata discovery and disk work are separate, so this is not a 50 ms limit on total command time. When locks remain busy, DIU drops the event and marks contention without changing the original command's output or exit code. `diu status` and `diu diagnostics` report that signal.

For parallel commands or large command bursts, start the daemon with `diu daemon start`.

<!-- DIU event and storage flow derived from cmd/diu/diu_setup.go and internal/storage -->
History lives in a size-bounded NDJSON file; package inventory and cached statistics live in a JSON manifest. Storage applies the configured retention and size limits.

```text
daemon / diu record ----> executions.ndjson (history)
          |
          `------------> executions.json (inventory + statistics)
diu scan --------------> executions.json
```

## Commands

| Command | Use it for |
| --- | --- |
| `diu setup` | Create config, storage, shell path entries, and wrappers. |
| `diu uninstall` | Remove wrappers and shell path entries while preserving data. |
| `diu scan` | Refresh the known package inventory. |
| `diu check [search]` | Search tracked packages and see usage. |
| `diu packages` | List tracked packages, optionally filtered by tool or unused duration. |
| `diu query` | Show recorded executions. |
| `diu stats` | Summarize usage by time range, tool, and top packages. |
| `diu status` | Show daemon state, local usage, last location, and observability paths. |
| `diu diagnostics [--output FILE]` | Print or save a redacted local bug report. |
| `diu manage` | Search packages and uninstall them interactively or by flag. |
| `diu daemon start` | Start the optional local recorder/API daemon. |
| `diu config list` | Print the resolved config as JSON. |
| `diu cleanup` | Apply retention and storage limits. |
| `diu backup` | Back up inventory and execution history. |

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

## Terminal Output

<!-- ASCII output derived from cmd/diu/diu_styleguide.go and internal/dx -->
DIU uses plain ASCII status markers and progress bars. Run `diu --styleguide` to preview the terminal styles. Selected output:

```text
[ok] setup complete
[!] using fallback
[x] failed check
[i] scanned packages
[############--------] 60%
```

The activity indicator cycles through `-`, `\`, `|`, and `/`.

Results and structured data are written to stdout. Prompts, progress, warnings,
and errors are written to stderr. Color and activity stop automatically for
redirected output, `TERM=dumb`, and CI. `NO_COLOR` always disables color.

Use `DIU_COLOR=always|never` or `DIU_ACTIVITY=always|never` to override automatic
color and loader detection.

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

<!-- Local paths derived from internal/core defaults and internal/storage.ExecutionLogPath -->
| Path | Purpose |
| --- | --- |
| `~/.config/diu/config.json` | User config. |
| `~/.local/share/diu/executions.json` | Package inventory, cached statistics, and execution-log metadata. |
| `~/.local/share/diu/executions.ndjson` | Size-bounded execution history. |
| `~/.local/share/diu/diu.log` | Private, size-bounded daemon log. |
| `~/.local/share/diu/fallback-contention` | Private marker for daemon-off recorder contention. |
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

# Show current usage, last activity/location, and local paths
diu status

# Generate a redacted report to attach to a bug
diu diagnostics --output diu-diagnostics.json

# Stream the same report as JSON
diu diagnostics
```

Diagnostics remain local and are never uploaded by DIU. Reports include fallback contention signals. They exclude command history, package names, environment variables, usernames, hostnames, and absolute managed paths.

## Development

```bash
mise install
mise run setup
mise run lint
mise run test
mise run build
```

<!-- Development checks derived from .mise.toml and .custom-gcl.yml -->
Setup installs Bash for shell legibility. Lint runs Go vet, golangci-lint with legibility, shfmt, ShellCheck, and shell legibility. Use `mise run lint-shell` for shell checks alone.

Release checks:

```bash
mise run version
mise run release-preview
mise run release
```

The version and release tasks refresh tags before `svu` calculates the next
version from conventional commits. The release task requires a clean,
synchronized `main`, runs the complete preview, and pushes an annotated `v*` tag
after confirmation. The tag workflow publishes the GitHub Release, GoReleaser
artifacts, and Homebrew formula.

`HOMEBREW_TAP_GITHUB_TOKEN` is required by the tag workflow.

## Requirements

<!-- Platform requirements derived from go.mod, .mise.toml, .github/workflows/release.yml, and Go's minimum requirements -->
- macOS 12 (Monterey) or later for published binaries.
- Go 1.25.12 or later when building from source; `mise install` selects Go 1.26.6.

Newer Go toolchains can require newer macOS versions. See [Go's platform requirements](https://go.dev/wiki/MinimumRequirements).

## License

MIT

## Author

Jeffry Wainwright ([@yowainwright](https://github.com/yowainwright))
