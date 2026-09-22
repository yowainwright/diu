# CLI and API E2Es

Run the CLI lifecycle suite:

```sh
mise run test-e2e
```

<!-- Runner derived from ops/docker/Dockerfile.e2e, ops/docker/compose.yaml, and main_test.go -->
Docker builds the current CLI and Go test executable into an image. Tests run as UID 1000 against disposable homes. The root filesystem is read-only; writable files live in container tmpfs. The CLI service has no host mounts, Docker socket, or network. Limits: one CPU, 256 MiB memory, 128 processes, and a three-minute suite timeout. Commands have individual deadlines and process-group cleanup. The test executable refuses host execution, including accidental `go test -tags=e2e` runs.

The CLI is real. The Homebrew command and tracked tools are controlled fixtures, so arguments, input, output, exit codes, and side effects can be checked exactly without installing real user packages. Tests call the public CLI; they do not call setup or uninstall internals.

<!-- Coverage derived from cli_*_test.go -->
| Area | Assertions |
| --- | --- |
| Setup | Only DIU-owned paths and designated shell blocks change; repeat setup produces one PATH block; wrapper permissions are 0700. |
| Shells | Bash, zsh, and fish preserve stdout, stderr, stdin, working directory, environment, exit status, empty arguments, quoting, Unicode, and embedded newlines. |
| Paths | Wrapper directories containing shell metacharacters work; nested PATH selection preserves preferred tools and personal scripts; returning exec and child shims terminate. |
| Upgrade | Setup replaces old wrapper files without executing them, then the replacement runs the real fixture command. |
| Recording | Real fallback history contains the original execution; slow recorders do not retain pipes; locked storage drops events; recorder children do not recursively record; helper commands bypass PATH. |
| Orphaned install | Removing the CLI from PATH leaves wrappers able to run the original tool without filesystem changes. |
| Uninstall | Stops a real recorder; removes PID/socket files, generated wrappers, delegation cache, and shell blocks; preserves history, original tools, personal files, permissions, and symlink targets. |
| Partial failure | Shell-read failure, invalid recorder state, and a paused recorder still permit wrapper cleanup; failures remain visible as nonzero CLI exits. |
| Repeatability | Uninstall is repeatable; subsequent setup and refresh respect disabled wrapper installation; redirected HOME leaves the account home untouched. |

These Linux containers exercise shared CLI and shell behavior. They do **not** validate macOS launchd, Homebrew formula installation, macOS Bash 3.2, or Homebrew upgrade hooks. LaunchAgent unit tests remain separate. Container results must not be described as proof that those macOS integrations work.

The older API suite runs separately with `mise run docker-e2e`. Its simulated wrapper request tests HTTP ingestion only; actual wrapper execution is covered by the CLI suite above.

Container controls use standard [Docker Compose service settings](https://docs.docker.com/reference/compose-file/services/).
