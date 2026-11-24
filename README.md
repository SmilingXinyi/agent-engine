# Agent Engine

[简体中文](./README_cn.md)

Work on local projects from your terminal with a model you configure. Agent Engine saves settings and conversation history per project and supports file tools, Shell, Skills, and MCP on macOS and Linux.

## Quick start

Install the CLI to `~/.local/bin`. The stable release must include `install.sh`. [GitHub Releases](https://github.com/SmilingXinyi/agent-engine/releases)

```sh
curl -fsSL https://github.com/SmilingXinyi/agent-engine/releases/latest/download/install.sh | sh
```

Enter an existing project directory and set your model and API endpoint.

```sh
export PATH="$HOME/.local/bin:$PATH"
cd /path/to/project

agent-engine init
export AGENT_LLM_MODEL='your-model'
export AGENT_LLM_BASE_URL='https://your-provider.example/v1'
# Set AGENT_LLM_API_KEY if your endpoint requires authentication.

agent-engine chat
```

Run `agent-engine configure` before chatting to review and apply project-specific settings. Use `agent-engine -c` to display the saved transcript and continue the conversation. File writes, patches, Shell, and MCP calls require approval by default. [CLI guide](./cli/README.md)

## Architecture

`cli → workspace → agent → ai-tool-loop → ai-sdk`. Workspace manages configuration, resources, and history. Agent assembles the context, and the tool loop drives model and tool execution.

| Module | Responsibility |
| --- | --- |
| [cli](./cli/README.md) | Commands, terminal interaction, and approvals |
| [workspace](./workspace/README.md) | Project configuration, MCP and Skills assembly, and NDJSON history |
| [agent](./agent/README_cn.md) | Prompt and conversation context assembly |
| [ai-tool-loop](./ai-tool-loop/README_cn.md) | Model and tool execution with lifecycle hooks and records |
| [ai-sdk](./ai-sdk/README.md) | Model requests, streaming, and provider adapters |
| [ai-tools](./ai-tools/README_cn.md) | File, Shell, user question, and subagent tools |
| [agent-skill](./agent-skill/README_cn.md) | Local Skills and on-demand instructions |
| [mcp-runtime](./mcp-runtime/README_cn.md) | MCP connections, tool discovery, and execution |
| [compression](./compression/README_cn.md) | Context estimates, tool result trimming, and conversation summaries |

## Development and releases

Use Go 1.25.0 or later. Build the CLI or run the workspace suite from the repository root. Run `go work sync` when synchronizing changes to module dependencies. [Coding guidelines](./AGENTS.md)

```sh
go build -o /tmp/agent-engine ./cli/cmd/agent-engine

go test ./compression/... ./agent/... ./agent-skill/... ./ai-sdk/... \
  ./ai-tool-loop/... ./ai-tools/... ./mcp-runtime/... ./workspace/... ./cli/...
```

The remote desktop example uses the tool loop to inspect a remote desktop from a task prompt. [Examples and setup](./examples/README_cn.md)

```sh
go run ./examples/remote-desktop-agent/main.go ./examples/remote-desktop-agent/ocr-tool.go \
  -task "Inspect the remote desktop and report which app is open"
```

The public SDK uses `ai-sdk/v*` tags. Pushing a `cli/v*` tag triggers a CLI binary release. The remaining modules build from the same checkout. [CLI publishing](./cli/README.md#publishing)

## License

MIT
