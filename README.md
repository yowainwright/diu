# DIU .oO(...)

[![Codecov](https://codecov.io/gh/yowainwright/diu/branch/main/graph/badge.svg)](https://codecov.io/gh/yowainwright/diu)

## Do I Use?

> See which global development tools you actually use.

Set up DIU, use your tools normally, and come back when you want to know what you use and what you might no longer need. DIU records supported command usage locally on your Mac.

```text
set up DIU -> work as usual -> check your usage
```

## Get Started

Install DIU and enable tracking:

```bash
brew install yowainwright/tap/diu
diu setup
```

Open a new terminal window to activate tracking. Then carry on with your usual work. There is no daily command to run or usage to log by hand.

<!-- Setup and inventory behavior derived from cmd/diu/diu_setup.go and cmd/diu/diu_daemon.go -->
`setup` discovers your installed tools, starts background tracking, and registers it to start when you log in to your Mac. Usage history begins when tracking is active, so give it time to reflect your habits.

## Get Insights

<!-- Insight commands and filters derived from cmd/diu/main.go and cmd/diu/diu_packages.go -->
| What you want to know | Command |
| --- | --- |
| What is installed, and how much do I use it? | `diu check` |
| Have I been using `jq`? | `diu check jq` |
| Which tools have no recorded use in the last 90 days? | `diu packages --unused 90d` |
| What do I use most? | `diu stats` |

`diu check` opens a searchable package browser in your terminal. Use `/` to search and `q` to quit. Add `--tool npm` or `--tool homebrew` to narrow a check to one package manager.

<!-- Package row format derived from cmd/diu/diu_packages.go -->
For example, `diu check jq` might show:

```text
1    homebrew        jq                                  12 uses    2026-06-20
```

<!-- Unused filtering derived from cmd/diu/diu_packages.go and cmd/diu/diu_helpers.go -->
“Unused” means DIU has no recorded use in that period, including tools with no history yet. It cannot recover usage from before setup or see uses that bypass its command wrappers, such as a library loaded by another program. Use the history to guide your decisions; DIU does not remove packages automatically.

## When Your Tools Change

<!-- Automatic refresh derived from internal/daemon/daemon.go, internal/core/core_config.go, and cmd/diu/diu_setup.go -->
DIU refreshes its inventory and command wrappers in the background as you install, upgrade, or remove tools. By default, it checks again 30 seconds after each refresh finishes. You do not need to rerun setup or scan during normal use.

Rerun `diu setup` if you move the DIU binary or change where your shell finds package managers.

## Supported Managers

| Ecosystem | Managers | What DIU tracks |
| --- | --- | --- |
| macOS | Homebrew | Formulae, casks, and wrapped executables. |
| JavaScript | npm, pnpm, Bun | Global packages and their command usage. |
| Go | Go | Installed binaries in `GOBIN` or `GOPATH/bin`. |
| Python | pip, uv, Poetry | pip packages, uv tools, and Poetry command/plugin usage. |

## More Options

<details>
<summary>How background tracking works</summary>

<!-- Wrapper execution sequence derived from cmd/diu/diu_setup.go and internal/monitors/monitors_process.go -->
`diu setup` places command wrappers in `~/.local/bin/diu-wrappers` and adds that directory to existing shell config files when possible. Each wrapper runs the original tool and preserves its output and exit code.

```text
command -> DIU wrapper -> original tool -> output to your terminal
               |
               +-- daemon available --> send event in the background
               |
               +-- daemon absent ----> run diu record synchronously
               |
               `--> return the original exit code
```

Setup starts the background recorder. These commands pause or resume it:

```bash
diu daemon stop
diu daemon start
```

<!-- Daemon lifecycle derived from cmd/diu/diu_daemon.go -->
The recorder starts again at your next login, including after a Mac restart. It refreshes tools in a separate process so scans do not block event recording. Refreshes run one at a time and have a two-minute limit. Command wrappers can still record while the daemon is stopped; automatic refresh resumes when it starts again.

<!-- Fallback wait policy derived from cmd/diu/diu_setup.go and internal/storage/storage_json.go -->
Without the daemon, recording waits for locks for up to 50 ms in total and drops events if locks remain busy. Metadata discovery and disk work take additional time. `diu status` reports detected contention. A failed send to the daemon also falls back to `diu record`.

<!-- DIU event and storage flow derived from cmd/diu/diu_setup.go and internal/storage -->
Execution history is stored in a size-bounded NDJSON file; inventory and statistics live in a JSON manifest. Both stay local, subject to configured retention and storage limits.

</details>

<details>
<summary>Reviewing removals or uninstalling DIU</summary>

To review packages for removal, open the interactive manager:

```bash
diu manage
```

Or preview one uninstall command before confirming it:

```bash
diu manage --uninstall jq --tool homebrew --dry-run
# brew uninstall jq
```

Remove `--dry-run` to run it after confirmation.

To stop using DIU:

```bash
diu uninstall
```

This stops background tracking and removes the login service, wrappers, and shell PATH entries while preserving your configuration and usage history. Remove the DIU binary separately with the method you used to install it.

</details>

<details>
<summary>History, exports, and the local API</summary>

Use `diu --help` or `diu <command> --help` for the full command reference.

Review individual executions or export a package list:

```bash
diu query --last 7d --limit 10
diu check --tool npm --format json --limit 0
```

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

For a bug report, generate a redacted diagnostic file:

```bash
diu diagnostics --output diu-diagnostics.json
```

Diagnostics remain local and are never uploaded by DIU. They include recording and storage health, but exclude command history, package names, environment variables, usernames, hostnames, and absolute managed paths.

</details>

<details>
<summary>Terminal output and ASCII styles</summary>

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
