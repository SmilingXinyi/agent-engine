# Agent Engine Workspace

This repository is structured as a Go multi-module workspace using `go.work`.

## Modules

- `ai-sdk`: Core AI SDK module.

## Coding Guidelines

Please refer to [AGENTS.md](./AGENTS.md) for code naming conventions and project guidelines.

## Getting Started

### Prerequisites

- Go 1.25.0+

### Workspace Setup

Initialize and sync the workspace:

```bash
go work sync
```

### Running Tests

Run tests across the workspace:

```bash
go test ./...
```

## License

MIT
