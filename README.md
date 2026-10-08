# Agent Engine Workspace

This repository is structured as a Go multi-module workspace using `go.work`.

## Modules

- `ai-sdk`: Core AI SDK providing LLM model abstractions, streaming, tool calling, prompt building, schema generation, and agent execution loops.
- `examples/basic`: Basic runnable example demonstrating the use of `ai-sdk`.

## Getting Started

### Prerequisites

- Go 1.25.0+

### Workspace Setup

Initialize and sync the workspace:

```bash
go work sync
```

### Running the Example

```bash
go run ./examples/basic
```

### Running Tests

Run unit tests across the SDK:

```bash
go test -v ./ai-sdk/...
```

## AI SDK Overview

```go
package main

import (
	"context"
	"fmt"

	aisdk "github.com/SmilingXinyi/agent-engine/ai-sdk"
	"github.com/SmilingXinyi/agent-engine/ai-sdk/provider"
)

func main() {
	ctx := context.Background()

	// Initialize OpenAI or compatible provider
	p := provider.NewOpenAIProvider(provider.Config{
		APIKey:  "your-api-key",
		BaseURL: "https://api.openai.com/v1",
	})

	m, err := p.Model("gpt-4o")
	if err != nil {
		panic(err)
	}

	text, err := aisdk.GenerateText(ctx, m, "Hello!")
	if err != nil {
		panic(err)
	}

	fmt.Println(text)
}
```

## License

MIT
