# Agent Demo Package

Runnable demos for `agent-engine` covering execute, stream, tools, skills, thinking, tool env injection, chat, and MCP.

## Prerequisites

1. Copy `.env.example` to `.env` at the repo root.
2. Set at least:
   - `AGENT_LLM_MODEL`
   - `AGENT_LLM_API_KEY`
3. Optionally set `AGENT_LLM_PROVIDER`, `AGENT_LLM_BASE_URL`, and `AGENT_LLM_DISABLE_THINKING=true`.

## Modes

| Mode | What it shows |
| --- | --- |
| `hello` | Non-streaming `Engine.Execute` |
| `stream` | Streaming `Engine.Stream` with live chunks |
| `tools` | Custom tool + `StdoutToolEventHandler` |
| `skills` | `skill.LoadSkills` + `prompt.BuildSystemPrompt` |
| `skilltool` | `skill.NewTool` (`get_skill`) + `skill.BuildListing` |
| `thinking` | Reasoning/thinking stream via `OnThinking` |
| `toolenv` | `agent.WithToolEnv` parameter injection into tools |
| `chat` | Multi-turn streaming REPL with history and tools |
| `mcp` | Load MCP config, start runtime, merge tools |

## Run

From the repo root:

```bash
go run ./examples/demo -mode=hello
go run ./examples/demo -mode=stream
go run ./examples/demo -mode=tools
go run ./examples/demo -mode=skills
go run ./examples/demo -mode=skilltool
go run ./examples/demo -mode=thinking
go run ./examples/demo -mode=toolenv
go run ./examples/demo -mode=chat
```

Custom prompt:

```bash
go run ./examples/demo -mode=tools -prompt="Call get_datetime and tell me the result."
```

### Skills

Sample skills live in `examples/demo/skills/`. Override the root with `-skills`:

```bash
go run ./examples/demo -mode=skills -skills=./examples/demo/skills -prompt="Please summarize this article: ..."
go run ./examples/demo -mode=skilltool -skills=./examples/demo/skills -prompt="Use summarize on: ..."
```

### Thinking

Works best with models that expose reasoning tokens. Thinking chunks print to stderr:

```bash
go run ./examples/demo -mode=thinking
```

### Tool environment injection

Demonstrates injecting hidden parameters (tenant, region) into tool calls:

```bash
go run ./examples/demo -mode=toolenv
```

### MCP

1. Copy `mcp.example.json` to a local config (for example `mcp.json`).
2. Point it at a real MCP server.
3. Run:

```bash
go run ./examples/demo -mode=mcp -mcp-config=./examples/demo/mcp.json
```

The default `mcp.example.json` uses an stdio example server via `npx` and requires Node.js.
