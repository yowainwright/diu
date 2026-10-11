# CLI and API end-to-end tests

Run the CLI lifecycle suite:

```sh
mise run test-e2e
```

<!-- Runner derived from ops/docker/Dockerfile.e2e, ops/docker/compose.yaml, and main_test.go -->
Docker builds the current CLI and Go test executable into an image. Tests run as UID 1000 against disposable homes. The root filesystem is read-only; writable files live in container tmpfs. The CLI service has no host mounts, Docker socket, or network. Limits: one CPU, 256 MiB memory, 128 processes, and a three-minute suite timeout. Commands have individual deadlines and process-group cleanup. The test executable refuses host execution, including accidental `go test -tags=e2e` runs.

Recorder E2Es hold all four production file-lock slots and use a non-reading Unix socket to keep four real workers busy. They confirm excess launch events are dropped while slots are occupied, a later event is accepted after a slot frees, and slots release after work ends. A separate fake Homebrew lookup blocks a recorder to check that the two-second deadline leaves command pipes free. The four-job bound is per configured data directory.

The shell handoff test uses a recorder fixture blocked on a FIFO before admission. It counts child processes during compound-command bursts, checks that the prompt remains responsive, and verifies capacity is reused after a handoff exits. The handoff limit is four per shell and survives executable-map reloads.

The CLI is real except for the stalled recorder fixture in the shell handoff test. Package-manager commands and tracked tools are controlled fixtures, so arguments, input, output, exit codes, and side effects can be checked exactly without installing real user packages. Tests call the public CLI; they do not call setup or uninstall internals.

<!-- Coverage derived from cli_*_test.go -->
| Area | Assertions |
| --- | --- |
| Setup | Repeated setup installs one managed zsh listener and source block; filesystem snapshots confirm that changes stay within DIU-owned paths and leave Bash and fish startup files untouched. |
| Shell tracking | Interactive zsh recognizes quoted, escaped, and path-qualified launches, plus `command`, `env`, and `sudo` across pipelines, control flow, and groups; arguments and redirection targets do not become launches; open shells receive refreshed executable maps; uv project arguments are excluded while direct uv-installed executables count. |
| Recording | Background and interactive launch attempts record executable and package-use metadata; handoff limits bound shell launchers, shared admission slots bound workers, excess events drop, and capacity is reused after work ends. |
| Uninstall | Stops a real recorder, removes PID/socket files and the managed zsh integration, preserves history and personal files, and reports recorder shutdown failures; a later scan does not revive tracking in an open shell. |
| Repeatability | Setup and uninstall are repeatable; uninstall without config leaves the filesystem unchanged, while orphan cleanup does not recreate config. |

These Linux containers exercise shared CLI and shell behavior. They do **not** validate macOS launchd, Homebrew formula installation, or Homebrew upgrade hooks. LaunchAgent unit tests remain separate. Container results must not be described as proof that those macOS integrations work.

The API suite runs separately with `mise run docker-e2e`. Its simulated launch payload tests HTTP ingestion; interactive zsh listener behavior is covered by the CLI suite above.

Container controls use standard [Docker Compose service settings](https://docs.docker.com/reference/compose-file/services/).
