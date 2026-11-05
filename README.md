# Agent Engine Workspace

This repository is structured as a Go multi-module workspace using `go.work`.

## Modules

- `ai-sdk`: Core AI SDK module.
- `ai-tool-loop`: Sequential model/tool execution, lifecycle hooks and execution records. See [usage](./ai-tool-loop/README_cn.md).
- `ai-tools`: Independently testable built-in filesystem and Shell tools, with explicit JSON enable/disable configuration. See [usage](./ai-tools/README_cn.md).
- `agent`: Prompt/history assembly over AI Tool Loop. See [usage](./agent/README_cn.md).
- `agent-skill`: Local SKILL.md loading, metadata listings and on-demand instruction tools. See [usage](./agent-skill/README_cn.md).
- `mcp-runtime`: MCP stdio, Streamable HTTP and legacy SSE connections and tool discovery. See [usage](./mcp-runtime/README_cn.md).
- `workspace`: Single-user local CLI, project-specific MCP/skills and durable NDJSON conversation history. See [usage](./workspace/README.md).

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

### Local Project Workspace

On macOS or Linux, initialize an existing project directory and start a persistent conversation:

```bash
go run ./workspace/cmd/agent-engine init /path/to/project
export AGENT_LLM_MODEL='your-model'
export AGENT_LLM_BASE_URL='https://your-provider.example/v1'
export AGENT_LLM_API_KEY='your-key'
go run ./workspace/cmd/agent-engine chat /path/to/project
```

Each project owns its configuration and `.agents/state/history.ndjson`. Reopening resumes the same context. Writes, Shell and MCP calls require confirmation by default.

### Remote Desktop Agent

```bash
go run ./examples/remote-desktop-agent/main.go ./examples/remote-desktop-agent/ocr-tool.go -task "Inspect the remote desktop and report which app is open"
```

See [example configuration and options](./examples/README_cn.md).

### Running Tests

Run tests across the workspace:

```bash
go test ./agent/... ./agent-skill/... ./ai-sdk/... ./ai-tool-loop/... ./ai-tools/... ./mcp-runtime/... ./workspace/...
```

## License

MIT
