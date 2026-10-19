# agent

组织 Prompt 和历史消息，并委托独立的 `tool-loop` 执行非流式模型/工具循环。`Model` 必填，`UserPrompt` 与有效的 `History` 至少提供一项，`SystemPrompt` 可以为空。消息顺序为 `system → history → user`。

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/SmilingXinyi/agent-engine/agent"
    "github.com/SmilingXinyi/agent-engine/agent/protocol"
    ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
    aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
    "github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
)

func main() {
    provider, err := openai.New(openai.Config{
        BaseURL: os.Getenv("AGENT_LLM_BASE_URL"),
        APIKey:  os.Getenv("AGENT_LLM_TOKEN"),
    })
    if err != nil {
        panic(err)
    }

    assistant := agent.New(ai.NewClient(provider, aiprot.Hooks{}))
    response, err := assistant.Execute(context.Background(), protocol.Request{
        Model:        os.Getenv("AGENT_MODEL"),
        SystemPrompt: "你是一个简洁的助手。",
        UserPrompt:   "请用一句话介绍 Go 语言。",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(response.LastResponse.Message.Text())
}
```

返回值为 `tool-loop/protocol.Result`。`LastResponse` 保留最后一次模型响应及其单次 usage；`Usage` 累计全部模型调用。`Messages`、`Steps`、`ToolCalls` 保存执行记录，`StopReason` 描述循环停止原因。Agent 不保存跨请求历史，调用方通过 `History` 传入 SDK 消息。Provider 和模型请求/响应 hooks 仍由 SDK Client 配置。

历史中的工具调用必须由 assistant 发起，并有 ID、名称匹配的 tool 结果；整批结果完成后才能出现下一条对话消息。孤立结果、重复结果、缺失结果、错误角色和 nil 工具内容会在调用模型前被拒绝。`developer` 与 `system`、`user`、`assistant` 一样是普通对话角色。Agent 不裁剪历史、不提取或覆盖其中的 system 消息。失败或 MaxSteps 返回的部分记录可能包含未完成工具批次，调用方不能直接把它当作完整历史重放。

`Temperature`、`MaxOutputTokens`、`ToolChoice`、`Reasoning`、`OutputFormat`、`ProviderOptions` 和 `Metadata` 会原样交给 tool-loop，并出现在每一次模型调用上。消息级扩展放在历史或当轮消息的 `ProviderOptions` 上。

## Skill 工具

调用方使用 [`agent-skill`](../agent-skill/README_cn.md) 的 `Load`、`BuildListing` 和 `NewTool`，将目录拼入 SystemPrompt，将 get_skill 加入 Tools。Agent 不依赖 Skill 模块，也不自动按关键词选择技能。

## MCP 工具

使用独立的 [`mcp-runtime`](../mcp-runtime/README_cn.md) 模块连接 MCP 服务器，将 `runtime.Tools` 传入 `Request.Tools`。Agent 不依赖 MCP SDK，也可以接收自行定义的 `tool-loop/protocol.Tool`。

执行顺序为：模型请求 → 顺序执行模型指定的工具 → 将 assistant 工具调用消息和 tool 结果追加到上下文 → 再次请求模型，直到生成最终回复。

`Request.Options` 使用 `tool-loop/protocol.RunOptions`：`Options.MaxSteps` 是模型请求次数上限，包含最终回复；默认 `10`。达到上限仍有工具调用时返回 `StopMaxSteps` 和 nil error，该轮工具不执行。没有工具调用时为 `StopNatural`，它不代表业务任务必然成功。工具执行错误及 panic 作为 `IsError` 结果回传模型；非法工具调用参数、context 取消、模型请求错误及生命周期 Hook 错误会返回 `StopError` 和部分执行记录。

`Options.Hooks` 支持运行、每步及工具的开始/结束回调；返回错误可停止循环。回调接收独立数据快照。详见 [`tool-loop`](../tool-loop/README_cn.md)。原 `Request.MaxSteps`、`agent/protocol.Tool` 和 SDK 响应返回契约已移除，无兼容别名。

在工作区根目录运行测试：

```bash
go test ./agent/... ./ai-sdk/... ./tool-loop/... ./mcp-runtime/...
```
