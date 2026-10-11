# DIU .oO(...)

[![Codecov](https://codecov.io/gh/yowainwright/diu/branch/main/graph/badge.svg)](https://codecov.io/gh/yowainwright/diu)

## Do I Use?

> See which global development tools you actually use.

DIU records local launch attempts for matched package executables submitted at a zsh prompt. Set it up, work as usual, and check which tools you use and which you might no longer need.

## Get Started

<!-- setup workflow and shell integration from setupProject and installZshShellIntegration -->
Install DIU and enable tracking:

```bash
brew install yowainwright/tap/diu
diu setup
```

Homebrew installs the prebuilt binary for your Mac (Apple Silicon or Intel). Go is not required.

Open a new interactive zsh terminal to activate tracking. Setup installs a listener in `.zshrc` and starts background inventory refresh.

History begins when tracking starts; give it time to reflect your habits.

When re-running setup, DIU stops the recorder before updating storage. If shutdown exceeds ten seconds, setup aborts configuration and allows one additional ten-second wait for recovery. If the recorder exits during that wait, DIU attempts to restart it and returns the original timeout error. If exit cannot be confirmed, DIU returns recovery instructions without migrating storage or starting a second recorder. If a later setup step fails, DIU also attempts to restart a recorder that was previously running and reports any restart failure alongside the setup error.

<!-- launch coverage and recorded fields from the zsh preexec hook and ExecutionRecord -->
## Tracking Coverage

DIU matches executable names in command lines submitted at an interactive zsh prompt. A match records a launch attempt, even if shell control flow later skips that command. Arguments, exit status, duration, working directory, and environment are not recorded.

DIU does not observe commands inside scripts, launches from other applications, or commands entered in other shells. Treat missing history as “not seen,” not proof that a tool was never used.

## Get Insights

| What you want to know | Command |
| --- | --- |
| What is installed, and how much do I use it? | `diu check` |
| Have I been using `jq`? | `diu check jq` |
| Which tools have no recorded use in the last 90 days? | `diu packages --unused 90d` |
| What do I use most? | `diu stats` |

`diu check` opens a package browser in your terminal. Use `/` to search and `q` to quit. Add `--tool npm` or `--tool homebrew` to list one manager's packages.

For example, `diu check jq` might show:

```text
1    homebrew        jq                                  12 uses    2026-06-20
```

“Unused” means no recorded launch attempt in that period, including tools with no history. Review this history before removing a package. DIU never removes packages automatically.

<!-- inventory refresh and executable-map behavior from refreshInventoryProcess and scanPackages -->
## When Your Tools Change

DIU refreshes its inventory and executable map in the background to find installed, upgraded, or removed tools. By default, each refresh starts 30 seconds after the last one finishes. Run `diu scan` to refresh them immediately.

Rerun `diu setup` after upgrading DIU, moving its binary, or changing package-manager paths. Setup refreshes the zsh listener and its executable map; it does not change command resolution through `PATH`.

<!-- supported package managers from DefaultEnabledTools and discoverExecutables -->
## Supported Managers

| Ecosystem | Managers | What DIU tracks |
| --- | --- | --- |
| macOS | Homebrew | Formulae, casks, and matched executable launch attempts. |
| JavaScript | npm, pnpm, Bun | Global packages and matched executable launch attempts. |
| Go | Go | Binaries in `GOBIN` or `GOPATH/bin` and matched launch attempts. |
| Python | pip, uv, Poetry | pip packages, uv tools, and matched executable launch attempts. |

UV inventory scans use `uv tool list`. Project commands such as `uv add`, `uv remove`, and `uv pip` remain in execution history without changing tool inventory.

For `uv tool run`, DIU attributes usage to the command's package, or the package specified by `--from`. Arguments passed to the tool are not recorded as package names.


## License

MIT

## Author

Jeffry Wainwright ([@yowainwright](https://github.com/yowainwright))
