# Agent Engine Workspace

This repository is structured as a Go multi-module workspace using `go.work`.

## Modules

- `ai-sdk`: Core AI SDK module.
- `ai-tool-loop`: Sequential model/tool execution, lifecycle hooks and execution records. See [usage](./ai-tool-loop/README_cn.md).
- `agent`: Prompt/history assembly over AI Tool Loop. See [usage](./agent/README_cn.md).
- `mcp-runtime`: MCP stdio/Streamable HTTP connections and tool discovery. See [usage](./mcp-runtime/README_cn.md).

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
go test ./agent/... ./ai-sdk/... ./ai-tool-loop/... ./mcp-runtime/...
```

## License

MIT
