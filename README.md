# DIU .oO(...)

[![Codecov](https://codecov.io/gh/yowainwright/diu/branch/main/graph/badge.svg)](https://codecov.io/gh/yowainwright/diu)

DIU, Do I Use, is a macOS CLI that tracks global development tool usage in zsh.

## Quick Start

<!-- setup workflow and shell integration from setupProject and installZshShellIntegration -->
```bash
brew install yowainwright/tap/diu
diu setup
```

Supports Apple Silicon and Intel Macs. Open a new zsh terminal after setup.

## Why was this built?

We install a lot of tools on a macs every day. But, do we use them? DUI aims to provide a dead simple way to know what we've installed and used  on our macs. 

## Commands

<!-- public commands and subcommands from rootApplicationCommands and internal/dx.Command.handleBuiltIn -->

### `diu setup`

```sh
diu setup
```

Enable zsh tracking and start DIU at login.

Final success output:

```text
[ok] DIU setup completed
```

Example `.zshrc` change, using `/Users/you` as the home directory:

```diff
--- ~/.zshrc
+++ ~/.zshrc after diu setup
@@
+# >>> DIU shell integration >>>
+source '/Users/you/.local/share/diu/shell-integration.zsh'
+# <<< DIU shell integration <<<
```

### `diu check`

```sh
diu check --tool homebrew --limit 2
```

```text
1    homebrew        bat                                 18 uses    2026-10-12
2    homebrew        ripgrep                             0 uses     never
```

```sh
diu check --tool homebrew --limit 2 --format csv
```

```csv
tool,name,version,usage_count,last_used,path
homebrew,bat,0.24.0,18,2026-10-12,/opt/homebrew/bin/bat
homebrew,ripgrep,14.1.1,0,never,/opt/homebrew/bin/ripgrep
```

### `diu packages`

```sh
diu packages --tool homebrew
```

```text
Tracked Packages


homebrew
  bat (0.24.0) - used 18 times, last: 2026-10-12
  ripgrep (14.1.1) - used 0 times, last: never
```

```sh
diu packages --unused 90d
```

```text
Tracked Packages


npm
  eslint (9.0.0) - used 7 times, last: 2026-06-20

homebrew
  ripgrep (14.1.1) - used 0 times, last: never
```

### `diu query`

```sh
diu query --limit 2
```

```text
Execution History

2026-10-12 10:30:00 [homebrew] bat
  Packages: bat

2026-06-20 09:15:00 [npm] eslint
  Packages: eslint
```

```sh
diu query --package bat --last 180d --limit 2
```

```text
Execution History

2026-10-12 10:30:00 [homebrew] bat
  Packages: bat

2026-05-01 12:00:00 [homebrew] bat
  Packages: bat
```

### `diu stats`

```sh
diu stats --top 2
```

```text
DIU Statistics

Total executions: 25

Tool usage:
  homebrew: 18
  npm: 7

Top 2 packages:
  1. bat (homebrew) - used 18 times
  2. eslint (npm) - used 7 times
```

```sh
diu stats --daily --top 0
```

```text
DIU Statistics (Last 24 Hours)

Total executions: 4

Tool usage:
  homebrew: 3
  npm: 1
```

```sh
diu stats --weekly --top 0
```

```text
DIU Statistics (Last 7 Days)

Total executions: 12

Tool usage:
  homebrew: 8
  npm: 4
```

### `diu manage`

```sh
diu manage --uninstall ripgrep --tool homebrew --dry-run
```

```text
brew uninstall ripgrep
```

```sh
diu manage --uninstall ripgrep --tool homebrew --yes
```

Final success output:

```text
[ok] ripgrep uninstalled
```

### `diu scan`

```sh
diu scan
```

Refresh the installed package list.

Example success output:

```text
[ok] 3 packages scanned
```

### `diu status`

```sh
diu status
```

Show tracking status, usage counts, and local paths.

Example output:

```text
DIU Status

FIELD                VALUE
Daemon               running
Storage health       ready
Executions           25
Tracked packages     3
Last recorded        2026-10-12 03:30:00 PDT
Last tool            homebrew
Last location        none
Fallback contention  none
Storage manifest     ~/.local/share/diu/executions.json
Execution history    ~/.local/share/diu/executions.ndjson
Logs                 ~/.local/share/diu/diu.log
```

### `diu diagnostics`

```sh
diu diagnostics --output ~/diu-report.json
```

```text
[ok] Diagnostic report written to /Users/you/diu-report.json
```

### `diu backup`

```sh
diu backup
```

Back up your package list and usage history.

Success output:

```text
[ok] Backup created
```

### `diu cleanup`

```sh
diu cleanup
```

Trim usage history using your configured retention and storage limits.

Success output:

```text
[ok] Cleanup completed
```

### `diu daemon`

```sh
diu daemon start
```

```text
[ok] DIU daemon started
```

```sh
diu daemon status
```

```text
DIU daemon is running
  PID: 12345
```

```sh
diu daemon restart
```

```text
[ok] DIU daemon stopped
[ok] DIU daemon started
```

```sh
diu daemon stop
```

```text
[ok] DIU daemon stopped
```

### `diu config`

```sh
diu config get storage.max_executions
```

```text
50000
```

```sh
diu config set storage.max_executions 10000
```

```text
[ok] Configuration updated
```

```sh
diu config list
```

Storage excerpt:

```json
{
  "storage": {
    "retention_days": 365,
    "max_executions": 10000,
    "max_storage_bytes": 10485760,
    "max_backups": 7
  }
}
```

Restart the daemon to apply changes.

### `diu uninstall`

```sh
diu uninstall
```

Disable tracking and startup at login. Keep your configuration and history.

Final success output:

```text
[ok] DIU setup removed; configuration and usage data preserved
```

Example `.zshrc` change:

```diff
--- ~/.zshrc
+++ ~/.zshrc after diu uninstall
@@
-# >>> DIU shell integration >>>
-source '/Users/you/.local/share/diu/shell-integration.zsh'
-# <<< DIU shell integration <<<
```

### `diu help`

```sh
diu help query
```

```text
Query execution history

Usage: diu query

Flags:
  -t, --tool             Filter by tool (brew, npm, go, etc.)
  -p, --package          Filter by package name
  -l, --last             Show executions in last duration (e.g., 24h, 7d)
  -n, --limit            Limit number of results
  -f, --format           Output format (table, json, csv)
```

```sh
diu backup --help
```

```text
Create manual backup

Usage: diu backup
```

### `diu --version`

```sh
diu --version
```

Show the installed version and build information.

Output format:

```text
diu <version> (commit <commit>, built <date>)
```

<!-- supported package managers from defaultMonitoringConfig and discoverExecutables -->
## Supported Managers

| Ecosystem | Managers | Inventory |
| --- | --- | --- |
| macOS | Homebrew | Formulae and casks |
| JavaScript | npm, pnpm, Bun | Global packages |
| Go | Go | Binaries in `GOBIN` or `GOPATH/bin` |
| Rust | Cargo | Installed CLI crates and their binaries |
| Python | pip, uv | pip packages and uv tools |

## License

[MIT](./LICENSE)

## Author

Jeffry Wainwright ([@yowainwright](https://github.com/yowainwright))
