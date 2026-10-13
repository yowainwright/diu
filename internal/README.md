# Internal Packages

<!-- package responsibilities from internal production entry points -->
```text
internal/
├── core/           Configuration and record types
├── daemon/         Events, sockets, HTTP API, scheduled work
├── dx/             Commands, terminal UI, process runner
├── helpers/        Collections, line parsing, cancellable waits
├── monitors/       Package inventory and command parsing
├── observability/  Logs and redaction
├── safefs/         File access and atomic writes
└── storage/        Inventory, history, backups
```

File conventions: [AGENTS.md](AGENTS.md). Tests sit beside their source.

## Recording

<!-- launch recording sequence from zshPreexecHook, runRecorderWorker, and Daemon.storeExecutions -->
```mermaid
sequenceDiagram
    participant Zsh as Interactive zsh
    participant Recorder as cmd/diu/record
    participant Daemon as internal/daemon
    participant Store as internal/storage
    participant CLI as CLI queries

    Zsh->>Recorder: Launch attempt
    Recorder->>Daemon: Send event over Unix socket
    alt Send succeeds
        Daemon->>Store: Save accepted events and update usage
    else Send fails
        Recorder->>Store: Save event and update usage
    end
    CLI->>Store: Query usage
    Store-->>CLI: Results
```

Recording is best effort.
