# Agent Engine

A lightweight, portable, and extensible AI Agent engine written in Go.

## Features

- **Portable Engine**: Decoupled from session and workspace state, making it easy to integrate into any Go application.
- **Streaming Support**: Real-time text generation with streaming chunks.
- **Tool Calling**: Native support for tool definitions and execution.
- **Model Context Protocol (MCP)**: Integration with MCP for extended capabilities.
- **Event Driven**: Comprehensive event handlers for tool execution lifecycle.
- **Flexible Adapters**: Built-in support for multiple LLM providers via `goai`.

## Installation

```bash
go get github.com/SmilingXinyi/agent-engine
```

## Quick Start

### 1. Configuration

Create a `.env` file based on `.env.example`:

```env
AGENT_LLM_PROVIDER=openai
AGENT_LLM_MODEL=gpt-4o
AGENT_LLM_API_KEY=your_api_key
# Optional: AGENT_LLM_BASE_URL=https://api.openai.com/v1
```

### 2. Basic Usage (Hello World)

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/SmilingXinyi/agent-engine/pkg/agent"
	"github.com/SmilingXinyi/agent-engine/pkg/model/goai"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	engine := agent.NewEngineWithClientFactory(goai.NewClientFactory())
	
	ctx := context.Background()
	response, err := engine.Execute(ctx, agent.Request{
		ModelConfig: agent.ModelConfig{
			Provider: os.Getenv("AGENT_LLM_PROVIDER"),
			APIKey:   os.Getenv("AGENT_LLM_API_KEY"),
			BaseURL:  os.Getenv("AGENT_LLM_BASE_URL"),
			Name:     os.Getenv("AGENT_LLM_MODEL"),
		},
		UserPrompt: "Hello, world!",
	})

	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Response: %s\n", response.Text)
}
```

## Tool Call Logging

Keep tool logs in `RunOptions.EventHandler` only. Do not duplicate logging in tool handlers or by iterating `response.ToolCalls` after the request completes.

```go
options := agent.RunOptions{
    EventHandler: agent.StdoutToolEventHandler(),
}
```

Lifecycle messages:

- Start: `calling tool: <name>`
- Success: `tool done: <name>`
- Failure: `tool error: <name> <err>`

Use `response.ToolCalls` for persistence or auditing, not as a second logging path.

## Project Structure

- `pkg/agent`: Core engine interfaces and implementation.
- `pkg/model/goai`: LLM adapter using the `goai` library.
- `pkg/tools`: Built-in tools including MCP, Prompt management, and Skills.
- `examples/demo`: Runnable demo package for execute, stream, tools, skills, chat, and MCP.
- `test`: Integration tests and usage examples.

## Demo Package

After configuring `.env`, run the demo package:

```bash
go run ./examples/demo -mode=hello
go run ./examples/demo -mode=stream
go run ./examples/demo -mode=tools
go run ./examples/demo -mode=skills
go run ./examples/demo -mode=chat
```

See `examples/demo/README.md` for MCP setup and prompt overrides.

## Testing

Run all tests:
```bash
go test ./...
```

To run integration tests with real LLM requests:
1. Set `AGENT_ENGINE_INTEGRATION_TEST=1` in your `.env`.
2. Run `go test -v ./test/...`.

## License

MIT
