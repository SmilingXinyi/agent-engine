# Agent Engine 工作区

本项目基于 Go 1.25 的 `go.work` 多模块工作区方案重构。

## 模块结构

- `ai-sdk`: 核心 AI SDK，提供统一的模型抽象（Model）、流式生成（Streaming）、工具定义与执行（Tools）、Prompt 模板与构造（Prompt）、结构体转 JSON Schema（Schema）、记忆管理（Memory）以及 Agent 自主循环（Agent Loop）。
- `examples/basic`: 基于 `ai-sdk` 的示例程序。

## 快速开始

### 前置要求

- Go 1.25.0 或更高版本

### 工作区管理

同步工作区依赖：

```bash
go work sync
```

### 运行示例

```bash
go run ./examples/basic
```

### 运行测试

执行测试：

```bash
go test -v ./ai-sdk/...
```

## ai-sdk 模块使用说明

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

	// 初始化 OpenAI 或兼容提供商
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

## 许可证

MIT
