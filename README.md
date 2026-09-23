# DIU .oO(...)

[![Codecov](https://codecov.io/gh/yowainwright/diu/branch/main/graph/badge.svg)](https://codecov.io/gh/yowainwright/diu)

## Do I Use?

> See which global development tools you actually use.

DIU records supported command usage locally on your Mac. Set it up, work as usual, and check which tools you use and which you might no longer need.

## Get Started

Install DIU and enable tracking:

```bash
brew install yowainwright/tap/diu
diu setup
```

<!-- Homebrew installation derived from .goreleaser.yaml and the tap's Formula/diu.rb -->
Homebrew installs the prebuilt binary for your Mac (Apple Silicon or Intel). Go is not required.

Open a new terminal window to activate tracking. DIU records usage as you work.

<!-- Setup and inventory behavior derived from cmd/diu/diu_setup.go and cmd/diu/diu_daemon.go -->
`setup` discovers installed tools and starts background tracking, including at future logins. History begins when tracking starts; give it time to reflect your habits.

When re-running setup, DIU stops the recorder before updating storage. If shutdown exceeds ten seconds, setup aborts configuration and allows one additional ten-second wait for recovery. If the recorder exits during that wait, DIU attempts to restart it and returns the original timeout error. If exit cannot be confirmed, DIU returns recovery instructions without migrating storage or starting a second recorder. If a later setup step fails, DIU also attempts to restart a recorder that was previously running and reports any restart failure alongside the setup error.

## Get Insights

<!-- Insight commands and filters derived from cmd/diu/main.go and cmd/diu/diu_packages.go -->
| What you want to know | Command |
| --- | --- |
| What is installed, and how much do I use it? | `diu check` |
| Have I been using `jq`? | `diu check jq` |
| Which tools have no recorded use in the last 90 days? | `diu packages --unused 90d` |
| What do I use most? | `diu stats` |

`diu check` opens a package browser in your terminal. Use `/` to search and `q` to quit. Add `--tool npm` or `--tool homebrew` to list one manager's packages.

<!-- Package row format derived from cmd/diu/diu_packages.go -->
For example, `diu check jq` might show:

```text
1    homebrew        jq                                  12 uses    2026-06-20
```

<!-- Unused filtering derived from cmd/diu/diu_packages.go and cmd/diu/diu_helpers.go -->
“Unused” means no recorded use in that period, including tools with no history. DIU cannot recover earlier usage or see uses that bypass its wrappers, such as a library loaded by another program. Review this history before removing a package. DIU never removes packages automatically.

## When Your Tools Change

<!-- Automatic refresh derived from internal/daemon/daemon.go, internal/core/core_config.go, and cmd/diu/diu_setup.go -->
DIU refreshes its inventory and wrappers in the background to find installed, upgraded, or removed tools. By default, each refresh starts 30 seconds after the last one finishes.

Rerun `diu setup` after upgrading DIU, moving its binary, or changing where your shell finds package managers. Upgrading the binary alone does not replace existing wrapper scripts.

## Supported Managers

| Ecosystem | Managers | What DIU tracks |
| --- | --- | --- |
| macOS | Homebrew | Formulae, casks, and wrapped executables. |
| JavaScript | npm, pnpm, Bun | Global packages and their command usage. |
| Go | Go | Installed binaries in `GOBIN` or `GOPATH/bin`. |
| Python | pip, uv, Poetry | pip packages, uv tools, and Poetry command/plugin usage. |

<!-- UV inventory scope derived from internal/monitors/monitors_python_managers.go and internal/storage/storage_json.go -->
UV inventory scans use `uv tool list`. Project commands such as `uv add`, `uv remove`, and `uv pip` remain in execution history without changing tool inventory.

For `uv tool run`, DIU attributes usage to the command's package, or the package specified by `--from`. Arguments passed to the tool are not recorded as package names.

## More Options

<details>
<summary>How background tracking works</summary>

<!-- Wrapper execution sequence derived from cmd/diu/diu_setup.go and internal/monitors/monitors_process.go -->
`diu setup` places command wrappers in `~/.local/bin/diu-wrappers` and adds that directory to existing shell config files when possible. Each wrapper runs the original tool and preserves its output and exit code.

When another installation takes precedence in `PATH`, DIU delegates to it using a private `.diu-delegates` directory beside its wrappers. That directory forwards only the selected command; existing `PATH` entries keep their order, so unrelated personal scripts remain available. Nested calls use the selected installation, while version-manager shims that explicitly return to a DIU wrapper reach its configured original without looping.

Executables installed globally through npm, pnpm, or Bun count toward their package's usage without a `--global` flag. When Homebrew and JavaScript managers share a bin directory, DIU uses the resolved executable path to distinguish their packages.

```text
command -> DIU wrapper -> original tool -> output to your terminal
               |
               +-- daemon available --> send event in the background
               |
               +-- daemon absent ----> run diu record in the background
               |
               `--> return the original exit code
```

Recording runs with detached input and output so it cannot hold a command's pipes open. Recorder subprocesses bypass tracking to avoid recording themselves. If both the recorder binary and socket are absent, wrappers run the original tool directly. Detached recording has no global concurrency cap; bursts of commands can create multiple recorder processes. Lock and socket timeouts do not impose a process limit.

Stop or start the background recorder:

```bash
diu daemon stop
diu daemon start
```

<!-- Daemon lifecycle derived from cmd/diu/diu_daemon.go -->
The recorder starts again at your next login. Inventory scans run in a separate process, one at a time, with a two-minute limit. Wrappers still record while the daemon is stopped, but automatic refresh pauses.

<!-- Fallback wait policy derived from cmd/diu/diu_setup.go and internal/storage/storage_json.go -->
Without the daemon, `diu record` waits up to 50 ms in total for locks, then drops the event if they remain busy. Metadata discovery and disk work take additional time. `diu status` reports lock contention. Failed sends to the daemon also fall back to `diu record`.

<!-- DIU event and storage flow derived from cmd/diu/diu_setup.go and internal/storage -->
Execution history stays in a local NDJSON file with configurable size and retention limits. A separate JSON file holds inventory and statistics.

</details>

<details>
<summary>Reviewing removals or uninstalling DIU</summary>

To review packages for removal, open the interactive manager:

```bash
diu manage
```

Preview an uninstall command:

```bash
diu manage --uninstall jq --tool homebrew --dry-run
# brew uninstall jq
```

Remove `--dry-run` to run it after confirmation.

To stop using DIU, run cleanup **before** removing the binary:

```bash
diu uninstall
```

<!-- Uninstall behavior derived from cmd/diu/diu_setup.go, cmd/diu/diu_uninstall.go, and cmd/diu/diu_daemon.go -->
This disables automatic wrapper installation in an existing config, stops the recorder, and removes the login service, generated wrappers, delegation cache, and shell PATH entries. Uninstall does not create a missing config. Cleanup continues after individual failures and reports them; unrelated files and usage history are preserved. Open a new terminal afterward, or run `rehash` in zsh (`hash -r` in bash).

<!-- Disabled wrapper behavior derived from configureCommandWrappers and refreshCommandWrappers in cmd/diu/diu_setup.go -->
Setting `monitoring.process.auto_install_wrappers` to `false` makes inventory refresh leave wrappers and shell configuration alone. An explicit `diu setup` with that setting removes the existing integration; `diu uninstall` always attempts cleanup.

For a Homebrew installation, then run `brew uninstall diu`. Removing only the binary does not clean up DIU's shell integration. If the binary was already removed, reinstall it to run `diu uninstall`, then remove it again.

To deliberately enable tracking again after cleanup, run `diu config set monitoring.process.auto_install_wrappers true`, then `diu setup`.

</details>

<details>
<summary>History, exports, and the local API</summary>

<!-- Command help and argument parsing derived from internal/dx/dx_cmd.go -->
Use `diu --help`, `diu help <command>`, or `diu <command> --help` for the command reference. Nested help works too: `diu help config get`. Use `--` before arguments that start with a dash, such as `diu check -- --help`.

Review individual executions or export a package list:

```bash
diu query --last 7d --limit 10
diu check --tool npm --format json --limit 0
```

<!-- Reporting flag validation derived from cmd/diu/diu_cli.go, cmd/diu/diu_query.go, and cmd/diu/diu_packages.go -->
`query` and `check` accept `--format table`, `json`, or `csv`. `packages`, `stats`, and `status` accept `table` or `json`. Tables are the default; `-f` is shorthand for `--format`. Unknown formats and negative result counts return errors. `--limit 0` returns all matching results; `stats --top 0` omits the package ranking.

<!-- JSON report shapes derived from cmd/diu/diu_query.go, cmd/diu/diu_packages.go, and cmd/diu/diu_status.go -->
JSON output contains only data, without headings or color codes. Empty execution and package lists are `[]`.

```bash
diu packages --tool npm --unused 30d --format json
diu stats --daily --top 5 --format json
diu status --format json
```

`stats` exports `total_executions`, `tool_counts`, and `top_packages`. Daily and weekly filters apply to execution counts; package rankings use lifetime usage counts. With no time filter, `most_active_day`, when present, describes all recorded tools and dates. `--top 0` produces an empty ranking array.

`status` exports recorder and storage health, counts, activity, and configured paths. `last_activity` is an RFC 3339 timestamp or `null` before any activity. Paths retain their full values instead of the table's `~` abbreviation.

The daemon also serves a local HTTP API at `http://127.0.0.1:8081/api/v1`:

```bash
curl http://127.0.0.1:8081/api/v1/health
curl http://127.0.0.1:8081/api/v1/stats
```

The API is unauthenticated. Keep `api.host` bound to `127.0.0.1` for local use.

</details>

<details>
<summary>Files and configuration</summary>

<!-- Local paths derived from internal/core defaults, internal/storage.ExecutionLogPath, and cmd/diu/diu_daemon.go -->
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
| `~/Library/LaunchAgents/io.github.yowainwright.diu.plist` | Background tracking at login. |

Common config edits:

```bash
diu config get storage.json_file
diu config set storage.retention_days 180
diu config set monitoring.enabled_tools homebrew,npm,pnpm,bun,go,pip,uv,poetry
diu config list
```

</details>

<details>
<summary>Troubleshooting</summary>

If expected usage is missing, open a new terminal window, run a tool normally, and inspect the latest recorded activity:

```bash
diu status
```

For a bug report, create a diagnostic file:

```bash
diu diagnostics --output diu-diagnostics.json
```

<!-- Diagnostic fields and redaction derived from cmd/diu/diu_diagnostics.go and internal/observability/observability_local.go -->
Diagnostics include recording and storage health plus recent daemon logs. DIU redacts known home-directory and managed-path values and never uploads the file. Review it before sharing; log messages may contain other details.

</details>

<details>
<summary>Terminal output and ASCII styles</summary>

<!-- ASCII output derived from cmd/diu/diu_styleguide.go and internal/dx -->
Run `diu --styleguide` to preview DIU's ASCII status markers and progress bars:

```text
[ok] setup complete
[!] using fallback
[x] failed check
[i] scanned packages
[############--------] 60%
```

The activity indicator cycles through `-`, `\`, `|`, and `/`.

Results go to stdout. Prompts, progress, warnings, and errors go to stderr.
Color and animation stop automatically for
redirected output, `TERM=dumb`, and CI. `NO_COLOR` always disables color.

Use `DIU_COLOR=always|never` or `DIU_ACTIVITY=always|never` to override automatic
color and animation detection.

</details>

<details>
<summary>Other installation methods</summary>

With Go:

```bash
go install github.com/yowainwright/diu/cmd/diu@latest
```

From source:

```bash
git clone https://github.com/yowainwright/diu
cd diu
mise run build
```

Then run `diu setup`. For a source build, use `./diu setup` and keep the binary in that location so the login service can find it.

</details>

<details>
<summary>Development and releases</summary>

```bash
mise install
mise run setup
mise run lint
mise run test
mise run build
```

<!-- Development checks derived from .mise.toml and .custom-gcl.yml -->
Setup installs Bash. Lint runs Go vet, golangci-lint with legibility, shfmt, ShellCheck, and shell legibility. Use `mise run lint-shell` for shell checks alone.

Use `mise run test-unit` to skip the two slow recorder shutdown tests and
`mise run test-slow` to run them separately. `mise run test` and CI include both.

<!-- Container CLI checks derived from tests/e2e, ops/docker/Dockerfile.e2e, and ops/docker/compose.yaml -->
Run `mise run test-e2e` for the real CLI lifecycle tests in Docker. These execute setup, wrappers, recording, and uninstall as a non-root user with no host mounts or network and bounded CPU, memory, and process counts. The suite refuses to execute directly on the host. See [E2E coverage](tests/e2e/README.md) for the assertions and platform limits. Both CI and tag releases require this suite to pass.

Release checks:

```bash
mise run version
mise run release-preview
mise run release
```

The version and release tasks fetch tags before `svu` calculates the next version
from conventional commits. Release requires a clean `main` synchronized with
origin. It asks for confirmation, runs the preview, then pushes an annotated
`v*` tag. The tag workflow publishes the GitHub Release and binaries. A separate job
runs the tap's shared `scripts/update-formula` to generate the binary formula and its
checksums, then opens a pull request with the formula and package metadata.
The tap's CI audits, installs, and tests the formula before that pull request is merged.

`HOMEBREW_TAP_GITHUB_TOKEN` is required by the tag workflow.

</details>

## Requirements

<!-- Platform requirements derived from go.mod, .mise.toml, .github/workflows/release.yml, and Go's minimum requirements -->
- macOS 12 (Monterey) or later for published binaries.
- Go 1.25.12 or later when building from source; `mise install` selects Go 1.26.6.

Newer Go toolchains can require newer macOS versions. See [Go's platform requirements](https://go.dev/wiki/MinimumRequirements).

## License

MIT

## Author

Jeffry Wainwright ([@yowainwright](https://github.com/yowainwright))
