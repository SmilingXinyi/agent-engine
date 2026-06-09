# Agent Engine

一个轻量级、可移植且易于扩展的 Go 语言 AI Agent 引擎。

## 特性

- **可移植引擎**: 与会话和工作区状态解耦，易于集成到任何 Go 应用程序中。
- **流式支持**: 支持实时文本生成的流式输出。
- **工具调用 (Tool Calling)**: 原生支持工具定义和自动执行。
- **模型上下文协议 (MCP)**: 集成 MCP 以扩展 Agent 能力。
- **事件驱动**: 提供完善的工具执行生命周期事件回调。
- **灵活适配**: 通过 `goai` 库内置支持多种 LLM 供应商。

## 安装

```bash
go get github.com/SmilingXinyi/agent-engine
```

## 快速开始

### 1. 配置

根据 `.env.example` 创建 `.env` 文件：

```env
AGENT_LLM_PROVIDER=openai
AGENT_LLM_MODEL=gpt-4o
AGENT_LLM_API_KEY=your_api_key
# 可选: AGENT_LLM_BASE_URL=https://api.openai.com/v1
```

### 2. 基本用法 (Hello World)

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

	// 创建引擎，使用 goai 作为模型客户端工厂
	engine := agent.NewEngineWithClientFactory(goai.NewClientFactory())
	
	ctx := context.Background()
	response, err := engine.Execute(ctx, agent.Request{
		ModelConfig: agent.ModelConfig{
			Provider: os.Getenv("AGENT_LLM_PROVIDER"),
			APIKey:   os.Getenv("AGENT_LLM_API_KEY"),
			BaseURL:  os.Getenv("AGENT_LLM_BASE_URL"),
			Name:     os.Getenv("AGENT_LLM_MODEL"),
		},
		UserPrompt: "你好，世界！",
	})

	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("回复: %s\n", response.Text)
}
```

## 项目结构

- `pkg/agent`: 核心引擎接口与实现。
- `pkg/model/goai`: 使用 `goai` 库实现的 LLM 适配器。
- `pkg/tools`: 内置工具，包括 MCP、Prompt 管理和 Skill 系统。
- `test`: 集成测试与使用示例。

## 测试

运行所有测试：
```bash
go test ./...
```

运行真实 LLM 请求的集成测试：
1. 在 `.env` 中设置 `AGENT_ENGINE_INTEGRATION_TEST=1`。
2. 运行 `go test -v ./test/...`。

## 开源协议

MIT
