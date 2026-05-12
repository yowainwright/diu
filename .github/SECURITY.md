# Security Policy

## Supported Versions

Security fixes are handled for the latest release and the `main` branch.

## Reporting a Vulnerability

Please do not open a public issue for a vulnerability.

Use GitHub's private vulnerability reporting flow:

https://github.com/yowainwright/diu/security/advisories/new

If private reporting is unavailable, open a minimal public issue asking for a security contact without including exploit details, logs, tokens, private paths, or proof-of-concept code.

## Scope

Security-sensitive areas include:

- wrapper generation and shell integration
- command execution and uninstall flows
- JSON storage file handling
- Unix socket ingestion
- local HTTP API behavior
- permissions for generated files and directories

## Expectations

When reporting, include:

- affected version or commit
- operating system and architecture
- a concise impact description
- reproduction steps or proof of concept, shared privately
- any known mitigations

Reports will be triaged as quickly as practical. Public disclosure should wait until a fix or mitigation is available.
