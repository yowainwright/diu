# diu — Do I Use

> Know what global packages you actually use

`diu` tracks globally installed packages across package managers — what's installed, what version, when it was last updated, and when it was last used.

## How it works

`diu` installs lightweight shell function hooks for `brew`, `npm`, `go`, `pip`, `pip3`, `cargo`, and `gem`. After each install or upgrade command, the hook calls `diu record` to update the local JSON cache. Run `diu scan` at any time to pull current version info from the package managers themselves.

```
shell hook (brew install wget)
  → diu record --tool brew --exit-code 0 -- install wget
    → upserts ~/.local/share/diu/diu.json
```

No daemon. No background process. No network calls.

## Installation

```bash
go install github.com/yowainwright/diu/cmd/diu@latest
```

Or build from source:

```bash
git clone https://github.com/yowainwright/diu
cd diu
go build -o diu ./cmd/diu
```

## Quick start

```bash
# 1. Inject shell hooks into ~/.zshrc / ~/.bashrc
diu setup

# 2. Populate the cache with what's currently installed
diu scan

# 3. See what's tracked
diu list

# 4. Check status at any time
diu status
```

Restart your shell (or `source ~/.zshrc`) after `setup` for hooks to take effect.

## Commands

### Setup

```bash
diu setup       # inject shell hooks into ~/.zshrc and ~/.bashrc
diu teardown    # remove shell hooks
diu status      # show hook status and storage stats
```

### Discovery

```bash
diu scan                # scan all enabled tools for installed packages + versions
diu scan --tool brew    # scan only homebrew
```

### Viewing data

```bash
diu list                        # all tracked packages
diu list --tool npm             # filter by tool
diu list --unused 90d           # packages not used in 90 days
diu list --format json          # JSON output

diu stats                       # usage frequency by tool
diu stats --top 10              # top 10 packages by use count
diu stats --tool homebrew       # filter by tool
```

### Maintenance

```bash
diu cleanup                     # remove records older than retention_days (default 365)
diu cleanup --before 90d        # remove records older than 90 days
```

### Config

```bash
diu config list                         # print full config as JSON
diu config get storage.json_file        # get a specific value
diu config set storage.retention_days 180
```

## Package data

Each tracked package stores:

| Field | Description |
|---|---|
| `name` | Package name |
| `version` | Installed version (populated by `scan`) |
| `tool` | Package manager (homebrew, npm, go, pip, cargo, gem) |
| `install_date` | When first seen |
| `last_updated` | When version last changed (detected by `scan`) or last upgraded |
| `last_used` | When last install/upgrade command was recorded |
| `usage_count` | Number of recorded installs/upgrades |

## Configuration

Config lives at `~/.config/diu/config.json` and is auto-created with defaults on first run.

Key settings:

```json
{
  "storage": {
    "json_file": "~/.local/share/diu/diu.json",
    "retention_days": 365
  },
  "monitoring": {
    "enabled_tools": ["homebrew", "npm", "go", "pip", "gem", "cargo"]
  }
}
```

## Supported tools

| Tool | Hook triggers on | Scan uses |
|---|---|---|
| `brew` | `install`, `upgrade`, `reinstall` | `brew list --json=v2` |
| `npm` | `install -g`, `update -g` | `npm list -g --json` |
| `go` | `install`, `get` | GOBIN directory scan |
| `pip` / `pip3` | `install` | — |
| `cargo` | `install` | — |
| `gem` | `install`, `update` | — |

## Development

```bash
mise run test       # run unit tests
mise run build      # build binary
mise run lint       # run linters
mise run scan       # build + diu scan
mise run unused     # show packages unused for 6+ months
```

## Requirements

- macOS (Intel or Apple Silicon)
- Go 1.25+
- bash or zsh

## License

MIT
