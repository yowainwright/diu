# Go package files

- Use `<package>.go` for package behavior and its public entry points.
- Use `types.go` for structs, interfaces, and other type declarations.
- Use `constants.go` for shared constants.
- Use `helpers.go` for private supporting functions and methods.
- Create supporting files only when needed. Keep each package flat.
- Package tests match their source filename and stay beside it, including checks of private state.
- Tests integrating components through public interfaces go in `tests/integration`. E2Es go in `tests/e2e`.
- Name tests `Test<Method><Scenario>` and order them like the source methods. Put test support functions below the tests they support.
- Additional filenames or subpackages require user approval before creation.
- Keep command parsing and terminal output in `cmd`; put package behavior in `internal`.
- Follow Google's [package structure guidance](https://google.github.io/styleguide/go/best-practices#package-structure).

The filename allowlist is DIU policy. Existing packages adopt it when reorganized; `fs-lint.json` records the enforced packages.
