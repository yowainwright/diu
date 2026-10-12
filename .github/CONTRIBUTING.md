# Contributing

DIU tracks tool usage locally and uses only the Go standard library.

## Project Direction

- Keep DIU focused on local package and tool usage tracking.
- Do not add third-party Go module dependencies.
- Prefer small, reviewable changes over broad rewrites.
- Keep user-facing CLI output direct and plain.
- Avoid decorative emoji in docs, templates, and CLI output.

## Development Setup

<!-- Development requirements derived from go.mod, .mise.toml, and .github/workflows/ci.yml -->
Use Go 1.27.2. `mise install` selects Go and the configured build tools. Docker is needed for Docker E2E tests.

Common commands:

```sh
go test ./...
go vet ./...
go build -o diu ./cmd/diu
```

<!-- Validation tasks derived from .mise.toml and .github/workflows/ci.yml -->
Run `mise run lint` and `mise run test` for the configured linters and race tests. CI also checks both Mac architectures, Docker E2E tests, security, and the Homebrew formula.

Use `mise run test-shutdown` to run the real-process recorder shutdown test in
isolation. The full test suite and CI include it.

## Pull Requests

Before opening a pull request:

- Open or reference an issue for non-trivial behavior changes.
- Keep the patch focused on one problem.
- Add tests for new behavior and regressions.
- Update docs when user-facing behavior changes.
- Confirm `go test ./...` passes locally when practical.

If you change wrappers, command execution, filesystem access, Unix sockets, or the HTTP API, describe the security impact in the pull request.

## Coding Guidelines

- Use clear names and simple control flow.
- Use standard library parsers when available.
- Keep comments sparse and useful.
- Preserve JSON field names and CLI flags unless you intend a breaking change.
- Keep generated files, local binaries, coverage, and build artifacts out of commits.

## Reporting Security Issues

Report vulnerabilities privately. See [SECURITY.md](SECURITY.md).
