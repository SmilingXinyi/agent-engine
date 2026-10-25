# mcp-runtime

与历史 `out/mcp-runtime` 相比，本模块直接输出 `ai-tool-loop/protocol.Tool`，去掉旧 Engine / Loop 的类型桥接。支持 stdio、Streamable HTTP、legacy SSE、自定义 HTTPClient / headers、环境变量覆盖、分页发现、名称前缀和连接释放；另有整服务 / 原始工具名禁用。

在连接前校验 HTTP header 名称和控制字符。发现工具时要求 schema 为 JSON object，根 `type` 若存在也必须为 `object`。执行参数保留原始 JSON 值，避免大整数经 float64 转换后改变。非文本 MCP 内容仍作为 JSON 文本返回，视觉消息转换需要单独的模型适配。

使用官方 MCP Go SDK 连接服务器、发现工具并适配为 `ai-tool-loop/protocol.Tool`。生产代码只依赖循环的工具契约，不依赖 Agent；端到端测试使用 Agent 验证组合行为。调用方持有 Runtime，可在多次 Agent 请求间复用；用完后调用 `Close` 释放连接和子进程。

工具名统一为 `{server_name}_{tool_name}`，例如 `filesystem_read_file`。实际 MCP 请求仍使用服务器原始工具名。模型可见名称限定为 1–64 个字母、数字、下划线或连字符；重名会返回错误。

这里的 `server_name` 是 JSON `mcpServers` 中的 key，与 MCP 服务器握手时报告的名称无关。

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/SmilingXinyi/agent-engine/agent"
    agentprot "github.com/SmilingXinyi/agent-engine/agent/protocol"
    ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
    aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
    loopprot "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
    "github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
    mcpruntime "github.com/SmilingXinyi/agent-engine/mcp-runtime"
    mcpprot "github.com/SmilingXinyi/agent-engine/mcp-runtime/protocol"
)

func main() {
    ctx := context.Background()
    config := mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{
        "filesystem": {
            Command: "npx",
            Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp/workspace"},
        },
    }}
    runtime, err := mcpruntime.Start(ctx, config)
    if err != nil {
        panic(err)
    }
    defer runtime.Close()

    provider, err := openai.New(openai.Config{
        BaseURL: os.Getenv("AGENT_LLM_BASE_URL"),
        APIKey: os.Getenv("AGENT_LLM_TOKEN"),
    })
    if err != nil {
        panic(err)
    }
    assistant := agent.New(ai.NewClient(provider, aiprot.Hooks{}))
    response, err := assistant.Execute(ctx, agentprot.Request{
        Model: os.Getenv("AGENT_MODEL"),
        SystemPrompt: "你是一个助手，可以使用工具读取文件。",
        UserPrompt: "列出 /tmp/workspace 目录中的文件。",
        Tools: runtime.Tools,
        Options: loopprot.RunOptions{MaxSteps: 10},
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(response.LastResponse.Message.Text())
}
```

stdio 的 `Env` 会覆盖继承的环境变量，`Dir` 指定子进程工作目录。`MaxLineLength` 设置单条入站 JSON-RPC 消息的字节上限；省略或为 `0` 时使用 SDK 默认值（16 MiB），负数会被拒绝。该参数仅支持 stdio，不能用于 Streamable HTTP 或 SSE。连接关闭时先关闭子进程 stdin，等待 5 秒；仍未退出则发送 SIGTERM，再等待 5 秒后强制终止并回收子进程。远程服务器配置如下：

```go
config := mcpprot.Config{Servers: map[string]mcpprot.ServerConfig{
    "remote": {
        URL: "https://mcp.example.com/mcp",
        Headers: map[string]string{"Authorization": "Bearer your-token"},
    },
}}
```

## MCP JSON 文件

建议文件名为 `mcp.json`，顶层使用 `mcpServers`，以服务器名作为 key。仓库提供了完整的 [`mcp.example.json`](./mcp.example.json)：

```json
{
  "mcpServers": {
    "filesystem": {
      "disabledTools": ["write_file", "edit_file"],
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/workspace"],
      "maxLineLength": 67108864,
      "env": {},
      "dir": "/tmp"
    },
    "remote": {
      "disabled": true,
      "url": "https://mcp.example.com/mcp",
      "headers": {
        "Authorization": "Bearer your-token"
      }
    }
  }
}
```

| 字段 | 用途 |
| --- | --- |
| `mcpServers` | 服务器对象；key 用于工具名前缀 |
| `type` | 可省略；`stdio` 使用子进程，`http` 使用 Streamable HTTP，`sse` 使用 legacy HTTP+SSE。省略时按 `command` / `url` 推断为 stdio / Streamable HTTP |
| `disabled` | 默认 `false`；`true` 时不连接、不启动子进程，也不暴露任何工具 |
| `disabledTools` | 默认空数组；指定此服务器要禁用的原始工具名，精确匹配 |
| `command` | stdio 子进程命令，与 `url` 互斥 |
| `args` | 命令参数字符串数组，可省略 |
| `env` | 子进程环境变量覆盖，值为字符串，可省略 |
| `dir` | 子进程工作目录，可省略；相对路径基于当前进程工作目录 |
| `maxLineLength` | stdio 单条入站消息上限，单位为字节；`0` 或省略使用默认 16 MiB，`67108864` 为 64 MiB；不允许负数 |
| `url` | Streamable HTTP 或 SSE 地址，仅接受 HTTP / HTTPS，与 `command` 互斥 |
| `headers` | HTTP 请求头，值为字符串，可省略 |

旧版 SSE 服务需显式指定 `type: "sse"`，例如：

```json
{
  "mcpServers": {
    "xunlei-download": {
      "type": "sse",
      "url": "https://your-mcp-server.example/sse"
    }
  }
}
```

SSE 使用官方 SDK 的 `SSEClientTransport`，先建立 GET 事件流，再向服务返回的会话地址 POST 消息；与 Streamable HTTP 共用 `headers` 和 `HTTPClient` 配置。不会根据 URL 路径猜测协议，也不会自动切换协议。未知 `type`、`stdio` 搭配 `url`、`http` / `sse` 搭配 `command` 都会在连接前报错。

读取文件后，直接把配置传给 Runtime：

```go
config, err := mcpprot.LoadConfig("./mcp.json")
if err != nil {
    return err
}
runtime, err := mcpruntime.Start(ctx, config)
if err != nil {
    return err
}
defer runtime.Close()
// 将 runtime.Tools 传给 agentprot.Request.Tools。
```

已有 JSON 字符串可继续使用 `mcpprot.ParseConfig(content)`。两个函数只读取、解析和校验配置，不连接服务器或执行命令。解析兼容旧顶层字段 `servers`，但不能与 `mcpServers` 同时出现；缺少顶层对象、未知字段和字段类型错误会返回错误。显式的 `{"mcpServers":{}}` 表示不启用任何服务器。

JSON 字符串按原样读取，不展开 `${ENV_VAR}`。`ServerConfig.HTTPClient` 仅通过 Go 代码配置，可注入代理、TLS 和超时策略。

通过 `command` 启动、再使用 `--remote` 连接桌面服务的 MCP，客户端与本地子进程之间仍是 stdio，因此也适用 `maxLineLength`。该限制包含整条 JSON 消息及其中的 base64 数据；base64 通常使图片体积增加约三分之一。调大此值不会改变模型 API 的请求体上限或图像 token 规则。修改后需要重新启动 Runtime 才会生效。

例如 `filesystem.disabledTools: ["write_file"]` 排除模型可见的 `filesystem_write_file`。禁用工具不会进入 `runtime.Tools`，Agent 也不会执行它，即使模型返回了该工具名。`disabledTools` 不支持通配符，不存在的工具名不影响其他工具。`disabled: true` 的服务器可以不填写 `command` 或 `url`。

禁用设置在 `Start` 时生效；修改配置后需要关闭旧 Runtime 并重新启动。

Runtime 在启动时读取完整的工具列表；新增或删除服务器工具后需要重新启动 Runtime。启动失败会关闭已经连接的服务器。MCP 文本结果直接回传，结构化结果和其他内容以 JSON 文本回传；图片、音频不会作为模型多媒体输入发送。本次接入包含 MCP tools，Agent 仍使用非流式模式。

在工作区根目录执行本地测试，无需真实模型或外部 MCP 服务：

```bash
go test ./agent/... ./ai-sdk/... ./ai-tool-loop/... ./mcp-runtime/...
```
