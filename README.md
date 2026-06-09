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

## Project Structure

- `pkg/agent`: Core engine interfaces and implementation.
- `pkg/model/goai`: LLM adapter using the `goai` library.
- `pkg/tools`: Built-in tools including MCP, Prompt management, and Skills.
- `test`: Integration tests and usage examples.

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
