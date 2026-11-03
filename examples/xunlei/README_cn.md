# 迅雷视觉 Agent

从仓库根目录运行，关闭逐工具询问并限制实际请求最多 10 条 messages：

```bash
AGENT_TOOL_CONFIRMATION=off go run ./examples/xunlei/{main,desktop,logging,confirmation}.go -history-messages 10
```

视觉模型直接观察截图，通过 MCP 工具完成迅雷下载任务。模型必须同时支持图像输入和工具调用。示例读取 MCP 配置，连接所有未禁用的服务，将其工具提供给模型。

[`prompt.md`](prompt.md) 是唯一的 system prompt 来源，程序通过 `go:embed` 原样加载。操作方式、下载目标与结束条件均由该文件定义。UserPrompt 只提供本次运行的显示器索引、预期文件名和完整下载 URL；截图附加消息只标注图片，不追加操作指令。

## MCP 与模型配置

默认读取 `examples/.agents/mcp.json`，可通过 `-config` 指定其他文件。配置示例：

```json
{
  "mcpServers": {
    "desktop-agent-remote": {
      "disabledTools": [
        "uia_list_windows", "uia_dump", "uia_find",
        "uia_element_at", "uia_invoke", "uia_set_value"
      ]
    },
    "tiny-ocr-mcp": { "disabled": true },
    "tiny-image": { "disabled": false },
    "xunlei-download": { "type": "sse", "url": "https://your-mcp-server.example/sse" }
  }
}
```

这是配置的节选；保留现有服务器的 `command`、`args`、`url` 等连接字段。入口不维护服务名白名单，也不强制要求桌面服务或特定启动参数。新增服务只需写入配置，重启后便会加载；`disabled: true` 跳过整个服务，`disabledTools` 屏蔽该服务的指定工具。`disabledTools` 填写原始工具名，不带服务名前缀。该配置由使用同一文件的入口共享，无需启动的服务可禁用，或为迅雷指定单独的配置文件。

日志 `[mcp start] servers=...` 列出本次准备连接的服务，`[tool registered] ...` 列出实际发现并提供给模型的工具。

迅雷官方 SSE 接口需配置 `type: "sse"`。省略 `type` 时，`command` 使用 stdio，`url` 使用 Streamable HTTP；也可显式设置 `type: "stdio"` 或 `type: "http"`。SSE 与 Streamable HTTP 均支持 `headers`。

根目录 `.env` 或进程环境配置：

```dotenv
AGENT_LLM_MODEL=支持图像和工具调用的模型
AGENT_LLM_BASE_URL=兼容OpenAI的服务地址
AGENT_LLM_API_KEY=
AGENT_TOOL_CONFIRMATION=off
```

进程环境优先于 `.env`。模型名兼容 `AGENT_MODEL`，密钥兼容 `AGENT_LLM_TOKEN`；API Key 可选。各 MCP 服务按自身连接配置启动，任一启用服务连接失败时程序报错。

运行前创建本地 `download-url.txt`，保存一行完整下载 URL，或通过 `-url-file` 指定其他文件；该本地文件已加入 gitignore。程序原样保留签名和查询参数，从 Content-Disposition 参数和 URL 路径提取可能的文件名，供视觉模型检查对应任务。

## 截图与上下文

模型直接调用原生 MCP 工具，按照工具 schema 选择截图模式与参数。`-display` 指定本次目标显示器，作为任务数据提供给模型。程序仅将工具返回的图片适配为模型的图像输入，不自动转换格式、不缩放，也不修改截图工具的定义或执行器。

`-history-messages 10` 按实际发给模型的消息条数计数，包含 system、原始任务、assistant、tool 及附加的截图 user 消息，最多 10 条，不是 10 轮。始终保留 system 与原始任务；旧 assistant/tool 批次整体移除，所以实际条数可能小于 10。只发送最新截图；完整运行记录仍保留。若最新完整工具批次本身无法放进预算则报错，不拆散工具调用或静默丢弃最新结果。该参数最小为 5。

日志 `[vision request] messages=... limit=10` 显示真正发送的消息数量。`[step ...] history_messages=...` 是裁剪前的内部历史数量，可能大于 10。此消息上限由迅雷示例的视觉适配器实现，不改变其他示例或通用 loop 的按轮裁剪行为。

模型自然结束后，程序记录执行结束及循环累计的 `loop_tokens`。程序不再启动额外的截图或模型核验流程；自然结束仅表示模型停止调用工具，不单独证明下载成功。

## 执行确认、参数与日志

入口在加载 `.env` 后一次性解析 `AGENT_TOOL_CONFIRMATION`，将确认回调和拒绝策略传给 Agent；Agent 本身不读取环境变量。非法值在连接 MCP 或调用模型前报错。`off`、未设置或留空时不询问。`continue` / `stop` 启用终端确认，显示工具名和参数：输入 `y` / `yes` 并回车批准，其他输入或直接回车拒绝。`continue` 拒绝后反馈模型继续，`stop` 拒绝即终止；EOF、读取错误、Ctrl+C 或总超时均终止等待。

```bash
AGENT_TOOL_CONFIRMATION=off go run ./examples/xunlei/{main,desktop,logging,confirmation}.go \
  -config examples/.agents/mcp.json \
  -env .env \
  -url-file examples/xunlei/download-url.txt \
  -display 0 -history-messages 10 -max-steps 40 -timeout 10m
```

日志同时输出到 console 和 `examples/xunlei/run.log`，追加运行开始/结束时间、模型调用、工具参数、结果、usage、确认决定与截图耗时。图片 base64 仅显示字节数；长链接和签名参数仍可能出现在工具调用日志。`-log-file` 可换路径，空字符串仅输出 console。

`[tool start] status=pending` 表示进入工具处理，可能等待确认；`[tool decision]` 表示批准或拒绝，`[tool end]` 表示处理结束。工具耗时包含确认等待。`[run end] tool_calls=...` 包含拒绝记录，不能据此判断任务成功。

```bash
go test ./examples/xunlei/*.go
```

测试使用模拟模型与工具，不连接远程桌面。运行程序须使用显式源文件列表，避免 `*.go` 包含测试文件导致 `go run` 报错。
