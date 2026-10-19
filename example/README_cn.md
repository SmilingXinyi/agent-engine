# 服务验证

MCP 配置位于 `.agents/mcp.json`，当前通过 stdio 启动 `@mobilenext/mobile-mcp`。

在仓库根目录验证 MCP 连接、工具发现及可用设备：

```bash
go run ./example/verify-mcp.go
```

传入设备 ID，继续读取当前屏幕元素：

```bash
go run ./example/verify-mcp.go -device YOUR_DEVICE_ID
```

也可用 `-config` 指定其他 MCP JSON 文件。当前验证入口针对配置中的 `mobile-mcp`，不依赖大模型。

Android 真机需要电脑安装 `adb`，手机开启 USB 调试并允许这台电脑。先用 `adb devices -l` 检查：`device` 表示可以调试，`unauthorized` 表示需要在手机上允许调试授权。验证入口在设备列表为空或工具报错时返回非零退出码。

## 运行 Agent

在根目录 `.env` 设置 `AGENT_LLM_MODEL`、`AGENT_LLM_API_KEY` 和 `AGENT_LLM_BASE_URL`，然后执行：

```bash
go run ./example/run-agent/main.go
```

入口读取 `example/prompt.md` 作为 UserPrompt，加载 `.agents/mcp.json` 并执行真实模型和手机工具调用。默认最多请求模型 40 次，总超时 15 分钟。输出包含 RunID、每轮 RequestID、工具名和参数、工具结果与耗时、每步 usage、停止原因和最终回复。手机示例通过 ToolEnd Hook 在 `INJECT_EVENTS` 权限错误时立即停止。Ctrl+C 会取消当前执行并触发结束 Hook。

可通过 `-prompt`、`-config`、`-env`、`-max-steps`、`-timeout` 调整。进程环境变量优先于 `.env`；模型名也支持 `AGENT_MODEL`，密钥也支持 `AGENT_LLM_TOKEN`。

可选加载本地技能目录：

```bash
go run ./example/run-agent/main.go -skills example/.agents/skills
```

示例目录包含 `mobile-popup` 技能。设置 `-skills` 后，目录元数据加入 SystemPrompt，`get_skill` 与 MCP 工具一起注册；正文由模型按需读取。不传该参数时保持原有执行方式。默认调用上限仍为 40，总超时仍为 15 分钟。

## 本地测试

```bash
go test ./ai-sdk/... ./ai-tool-loop/... ./agent/... ./agent-skill/... ./mcp-runtime/...
go test ./example/run-agent/*.go
go test ./example/verify-mcp.go
```

示例测试使用本地模拟模型及 MCP HTTP 服务器，不使用真实 API Key、付费模型或手机。`StopNatural` 只表示模型停止请求工具，业务是否完成仍需结合最终回复和实际工具结果判断。达到调用上限时，示例以非零退出码报告未完成。

技能集成测试覆盖：目录进入 system → 模型请求 get_skill → 正文回传 → MCP 工具执行 → 最终回答。
