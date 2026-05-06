# Contributing

Thanks for taking the time to improve DIU. This project is intentionally small, local-first, and built with the Go standard library only.

## Project Direction

- Keep DIU focused on local package and tool usage tracking.
- Do not add third-party Go module dependencies.
- Prefer small, reviewable changes over broad rewrites.
- Keep user-facing CLI output direct and plain.
- Avoid decorative emoji in docs, templates, and CLI output.

## Development Setup

Requirements:

- macOS 10.15 or later for the primary supported environment.
- Go 1.25 or later.
- mise, Docker, GoReleaser, golangci-lint, and gosec are optional development or release tools.

Common commands:

```sh
go test ./...
go vet ./...
go build -o diu ./cmd/diu
```

The CI workflow also runs race-enabled tests, linting, builds, and security scanning.

## Pull Requests

Before opening a pull request:

- Open or reference an issue for non-trivial behavior changes.
- Keep the patch scoped to one problem.
- Add tests for new behavior and regressions.
- Update README or docs when user-facing behavior changes.
- Confirm `go test ./...` passes locally when practical.

For changes that touch wrappers, command execution, filesystem access, Unix sockets, or the HTTP API, include a short note about the security impact in the pull request.

## Coding Guidelines

- Use clear names and simple control flow.
- Prefer standard library APIs over custom parsing when available.
- Keep comments sparse and useful.
- Preserve existing JSON field names and CLI flags unless the change is intentionally breaking.
- Keep generated files, local binaries, coverage, and build artifacts out of commits.

## Reporting Security Issues

Do not report vulnerabilities in public issues. See `SECURITY.md` for private reporting guidance.
