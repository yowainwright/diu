# DIU .oO(...)

[![Codecov](https://codecov.io/gh/yowainwright/diu/branch/main/graph/badge.svg)](https://codecov.io/gh/yowainwright/diu)

## Do I Use?

> See which global development tools you actually use.

DIU records launch attempts for global tools at your zsh prompt.

## Get Started

<!-- setup workflow and shell integration from setupProject and installZshShellIntegration -->
```bash
brew install yowainwright/tap/diu
diu setup
```

Supports Apple Silicon and Intel Macs; Go is not required. Open a new interactive zsh terminal after setup to activate tracking.

<!-- launch coverage and recorded fields from the zsh preexec hook and ExecutionRecord -->
## Tracking Coverage

Executable matches count as launch attempts, even when shell control flow skips them. DIU does not record arguments, exit status, duration, working directory, or environment.

Commands inside scripts, other applications, and other shells are not observed. Attempts may be dropped when busy. Missing history means “not seen,” not “never used.”

## Get Insights

| Action | Command |
| --- | --- |
| Browse packages | `diu check` |
| Check `jq` | `diu check jq` |
| Unused for 90 days | `diu packages --unused 90d` |
| Usage statistics | `diu stats` |

In the browser, use `/` to search and `q` to quit. Filter by manager with `--tool npm` or `--tool homebrew`.

“Unused” includes tools with no history. DIU never removes packages automatically.

<!-- inventory refresh and executable-map behavior from refreshInventoryProcess and scanPackages -->
## When Your Tools Change

Inventory refreshes in the background, 30 seconds after each refresh finishes. Run `diu scan` to refresh immediately.

Rerun `diu setup` after upgrading DIU, moving its binary, or changing package-manager paths.

`diu uninstall` disables tracking. A later scan keeps it disabled; `diu setup` enables it again.

<!-- supported package managers from DefaultEnabledTools and discoverExecutables -->
## Supported Managers

| Ecosystem | Managers | Inventory |
| --- | --- | --- |
| macOS | Homebrew | Formulae and casks |
| JavaScript | npm, pnpm, Bun | Global packages |
| Go | Go | Binaries in `GOBIN` or `GOPATH/bin` |
| Python | pip, uv, Poetry | pip packages and uv tools |

UV tools count when launched directly. Commands through `uv` do not produce launch records.

## License

MIT

## Author

Jeffry Wainwright ([@yowainwright](https://github.com/yowainwright))
