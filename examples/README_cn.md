# 示例使用说明

所有命令均从仓库根目录运行。手机与桌面示例共用 `examples/.agents/mcp.json` 配置。

## 迅雷 Agent

运行 `go run ./examples/xunlei/*.go`，由 Agent 通过 `desktop-agent-remote` 启动远程迅雷、点击顶部“新建”、填入配置的链接并点击下载，通过 `tiny-ocr-mcp` 对最新截图核验对应任务出现在列表。配置、参数和流程见 [迅雷示例](xunlei/README_cn.md)。

## 手机 Agent 与服务验证

### 服务验证

MCP 配置位于 `examples/.agents/mcp.json`，当前通过 stdio 启动 `@mobilenext/mobile-mcp`。

在仓库根目录验证 MCP 连接、工具发现及可用设备：

```bash
go run ./examples/verify-mcp.go
```

传入设备 ID，继续读取当前屏幕元素：

```bash
go run ./examples/verify-mcp.go -device YOUR_DEVICE_ID
```

也可用 `-config` 指定其他 MCP JSON 文件。当前验证入口针对配置中的 `mobile-mcp`，不依赖大模型。

Android 真机需要电脑安装 `adb`，手机开启 USB 调试并允许这台电脑。先用 `adb devices -l` 检查：`device` 表示可以调试，`unauthorized` 表示需要在手机上允许调试授权。验证入口在设备列表为空或工具报错时返回非零退出码。

### 运行 Agent

在根目录 `.env` 设置 `AGENT_LLM_MODEL` 和 `AGENT_LLM_BASE_URL`；`AGENT_LLM_API_KEY` 可选，服务需要鉴权时再填写。未提供 Key 时不发送 Authorization 头。然后执行：

```bash
go run ./examples/run-agent/main.go
```

入口读取 `examples/prompt.md` 作为 UserPrompt，加载 `examples/.agents/mcp.json` 并执行真实模型和手机工具调用。默认最多请求模型 40 次，总超时 15 分钟。输出包含 RunID、每轮 RequestID、工具名和参数、工具结果与耗时、每步 usage、停止原因和最终回复。手机示例通过 ToolEnd Hook 在 `INJECT_EVENTS` 权限错误时立即停止。Ctrl+C 会取消当前执行并触发结束 Hook。

可通过 `-prompt`、`-config`、`-env`、`-max-steps`、`-timeout` 调整。进程环境变量优先于 `.env`；模型名也支持 `AGENT_MODEL`，密钥也支持 `AGENT_LLM_TOKEN`。

可选加载本地技能目录：

```bash
go run ./examples/run-agent/main.go -skills examples/.agents/skills
```

示例目录包含 `mobile-popup` 技能。设置 `-skills` 后，目录元数据加入 SystemPrompt，`get_skill` 与 MCP 工具一起注册；正文由模型按需读取。不传该参数时保持原有执行方式。默认调用上限仍为 40，总超时仍为 15 分钟。

### 本地测试

```bash
go test ./ai-sdk/... ./ai-tool-loop/... ./agent/... ./agent-skill/... ./mcp-runtime/...
go test ./examples/run-agent/*.go
go test ./examples/verify-mcp.go
```

示例测试使用本地模拟模型及 MCP HTTP 服务器，不使用真实 API Key、付费模型或手机。`StopNatural` 只表示模型停止请求工具，业务是否完成仍需结合最终回复和实际工具结果判断。达到调用上限时，示例以非零退出码报告未完成。

技能集成测试覆盖：目录进入 system → 模型请求 get_skill → 正文回传 → MCP 工具执行 → 最终回答。

## 远程桌面 Agent

在仓库根目录运行，每条命令发送一个任务：

```bash
go run ./examples/remote-desktop-agent/main.go ./examples/remote-desktop-agent/ocr-tool.go -task "查看远程桌面当前打开的应用，并告诉我结果"
```

入口读取根目录 `.env` 中的 `AGENT_LLM_MODEL` 和 `AGENT_LLM_BASE_URL`（必填），使用兼容 OpenAI Chat Completions 的模型服务。`AGENT_LLM_API_KEY` 可选，服务需要鉴权时再填写；未提供 Key 时不发送 Authorization 头。模型需支持工具调用；使用桌面截图时还需支持图像输入。进程环境变量优先于 `.env`，也兼容 `AGENT_MODEL`、`AGENT_LLM_TOKEN`。

默认使用 [`examples/.agents/mcp.json`](.agents/mcp.json)，启动 `desktop-agent-remote` 和已启用的 `tiny-image` 图片服务。请确保其中的 MCP 可执行文件路径、`--remote` 地址有效，并且远程桌面服务已经启动。`mobile-mcp` 等无关服务不会启动。

`tiny-image` 使用本地可执行文件 `./examples/tiny-image-mcp-darwin-arm64`，图片存入 `examples/.tiny-image/`（已加入 gitignore）。请从仓库根目录运行。该文件需具有执行权限；首次运行若被 macOS 拦截，需要先允许打开此文件。

启用图片服务后，每次 `capture_screen` 都在返回模型之前自动执行：截图 → `tiny-image_upload_image` → `tiny-image_resize_image`（WebP、quality=75、1920×1920 边界内等比缩放、不放大）→ 返回处理后的图像。模型第一次观察截图时就收到压缩、缩放后的 WebP，无需额外请求模型决定或执行压缩。

程序将原始截图解码为权限为 0600 的本地临时文件，通过 stdio 的 `upload_image.path` 导入；预处理结束或失败后删除临时文件。这样无需在图片服务的 JSON-RPC 入站帧里携带原图 base64，避免触发其 10 MiB 入站限制。tiny-image 本身仍有 20 MiB 原图上传大小限制。

tiny-image 仅在结果不超过 4 MB 时附带图像；未附带时，程序依次以 1280×1280 / quality=65、960×960 / quality=55 重试，始终从原图处理。预处理失败会返回工具错误，不退回发送原图。返回内容包含图片 ID、原图及处理后尺寸和原截图其他文字，prompt 提醒模型根据尺寸比例及截图起点换算桌面坐标。日志 `[image preprocess]` 显示预处理前后尺寸和字节数。原图保存在本地图片目录，可通过 ID 再处理，无需模型复制 base64。未配置或禁用 tiny-image 时保持直接返回截图的方式。

该服务已设置 `"maxLineLength": 67108864`，将本地 stdio 单条入站 JSON-RPC 消息上限提高到 64 MiB，以接收较大的桌面截图。省略此字段或设为 `0` 时使用默认 16 MiB。该设置不改变模型 API 的请求体上限。

执行逻辑：观察桌面 → 根据模型请求调用远程桌面工具 → 把结果及截图交回模型 → 继续操作，直到模型给出最终回复。终端输出调用进度和最终结果。默认最多请求模型 40 次，不设总执行时限；Ctrl+C 可取消。`-timeout 0` 表示不限时，显式传入正时长（例如 `-timeout 30m`）才会设置任务总超时，负值无效。达到调用上限或发生执行错误时返回非零退出码。模型停止调用工具并不保证业务任务成功，需要结合最终回复与实际界面判断。

`[model result]` 在工具调用校验前输出每轮模型耗时、`finish_reason`、单轮 input/output/total token 及请求 ID；`[model tool call]` 输出模型返回的调用 ID、工具名、参数字节数和带引号转义的 `raw_arguments`，包括最终被校验拦截的调用。空参数显示为 `raw_arguments=""`，`null` 显示为 `raw_arguments="null"`。最终汇总的 tokens 仍是所有调用累计值。

`[model text]` 打印每轮模型返回的正文，保留原始换行；正文为空时显示“模型未返回正文”。模型返回 reasoning 内容时，另外通过 `[model reasoning]` 打印，便于区分正文与模型返回的推理内容。

`-history-rounds` 默认 `10`，控制 ai-tool-loop 每次请求保留的历史轮数。超出时整轮省略最早内容，system prompt 始终保留；该限制与 `-max-steps` 的总执行次数独立。初始用户任务也可能随旧轮次被省略。完整执行记录仍保留。

轮次裁剪后，示例只把窗口内最近两张 MCP 截图转换成 user 图像消息，并放在同批所有工具结果之后。窗口内更早的截图替换为带工具名和调用 ID 的“历史截图已省略”文字占位，其他文字和工具调用结构保留，不生成画面摘要。过期轮次的截图和工具结果一起从模型输入中移除；完整执行记录不变。终端通过 `[images] retained=2 omitted=N` 显示窗口内保留及省略的截图数量。两张大图本身仍可能超过模型 API 的请求体上限。

需要调整配置或执行时长时：

```bash
go run ./examples/remote-desktop-agent/main.go ./examples/remote-desktop-agent/ocr-tool.go \
  -config examples/.agents/mcp.json \
  -server desktop-agent-remote \
  -env .env \
  -max-steps 80 \
  -history-rounds 10 \
  -timeout 30m \
  -task "打开浏览器，搜索 Go 官方文档，并停留在文档首页"
```

本地测试（模拟模型与 MCP，不会操作真实桌面或调用付费模型）：

```bash
go test ./examples/remote-desktop-agent/*.go
```
