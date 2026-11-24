# Agent Engine

[English](./README.md)

在 macOS 或 Linux 的终端里，用自己的模型处理本地项目。Agent Engine 会为每个项目保存独立的配置和对话，支持文件工具、Shell、Skills 和 MCP。

## 快速开始

安装 CLI，默认位置为 `~/.local/bin`。稳定版 Release 需包含 `install.sh`。 [GitHub Releases](https://github.com/SmilingXinyi/agent-engine/releases)

```sh
curl -fsSL https://github.com/SmilingXinyi/agent-engine/releases/latest/download/install.sh | sh
```

进入一个已有的项目目录。将示例中的模型名和 API 地址换成你的配置。

```sh
export PATH="$HOME/.local/bin:$PATH"
cd /path/to/project

agent-engine init
export AGENT_LLM_MODEL='your-model'
export AGENT_LLM_BASE_URL='https://your-provider.example/v1'
# 接口需要认证时设置 AGENT_LLM_API_KEY

agent-engine chat
```

想根据项目生成配置，可以在对话前运行 `agent-engine configure`，预览并确认建议。重新打开时用 `agent-engine -c` 回显历史并继续对话。写文件、打补丁、Shell 和 MCP 调用默认逐次确认。 [CLI 使用手册](./cli/README_cn.md)

## 架构

`cli → workspace → agent → ai-tool-loop → ai-sdk`。Workspace 管理配置、资源和会话历史，Agent 负责组装上下文，工具执行由同一套循环处理。

| 模块 | 职责 |
| --- | --- |
| [cli](./cli/README_cn.md) | 命令、终端输入输出和审批 |
| [workspace](./workspace/README_cn.md) | 项目配置、MCP 和 Skills 装配、NDJSON 历史 |
| [agent](./agent/README_cn.md) | 组装 Prompt 和对话上下文 |
| [ai-tool-loop](./ai-tool-loop/README_cn.md) | 驱动模型与工具循环，记录执行过程 |
| [ai-sdk](./ai-sdk/README_cn.md) | 模型请求、流式响应和供应商接口 |
| [ai-tools](./ai-tools/README_cn.md) | 文件、Shell、用户提问及子代理工具 |
| [agent-skill](./agent-skill/README_cn.md) | 加载本地 Skills，按需提供指令 |
| [mcp-runtime](./mcp-runtime/README_cn.md) | MCP 连接、工具发现与调用 |
| [compression](./compression/README_cn.md) | 上下文估算、工具结果裁剪和历史摘要 |

<details>
<summary>历史实现的取舍</summary>

| 历史能力 | 当前处理 | 原因 |
| --- | --- | --- |
| Skill frontmatter、目录和 get_skill | 重构为独立 agent-skill，目录只展示元数据，正文按需进入模型上下文 | 保留渐进式上下文，适合中文及复杂描述 |
| Skill 关键词评分、前 5 项、每次调用重扫目录 | 移除；精确名称查询和固定快照 | 避免误选、隐藏策略与不稳定输入 |
| MCP stdio / HTTP、环境变量、headers、分页、连接释放 | 保留现有实现；直接提供 loop Tool | 去掉 Engine Tool / Loop Tool 双向适配 |
| MCP schema 校验 | 补齐根 type 校验；HTTP header 名和值提前校验 | 启动时发现错误，避免执行期间才失败 |
| MCP JSON 参数经 map[string]any 转换 | 改为保留原始 JSON 值 | 防止大整数精度丢失 |
| MCP legacy SSE | 通过显式 type 配置接入官方 SDK SSEClientTransport | 兼容旧服务，不按 URL 猜测协议或自动切换 |
| MCP 重连参数、Streamable HTTP 常驻 GET SSE | 暂不引入 | 有具体服务需求后再扩展，不扩大默认配置 |
| Engine 历史工具调用/结果校验 | 重写到 Agent 的上下文校验中 | 不将孤立、错配、未完成的工具批次传给模型 |
| Engine 工厂、重复消息类型、生命周期桥接、DebugSink | 由 SDK Client / Hooks 和 loop 记录承担 | 一种协议、一套循环、一套可观测性，不建第二个抽象层 |
| Engine 按消息数裁剪、隐式 system 覆盖、工具环境注入 | ai-tool-loop 按完整轮次裁剪模型输入，system 始终保留；其他能力暂不引入 | 保持工具批次完整及完整执行记录；消息与参数保持显式输入 |
| Engine 会话持久化 | 由 workspace 编排模块提供项目独立的 NDJSON 历史及中断恢复 | Agent 和 loop 保持无状态 |
| Engine 流式、外部工具续接、参数改写 | 保持当前范围，暂缓 | 这些能力需要独立契约与真实调用场景 |

MCP 图片等非文本结果目前仍序列化为 JSON 文本。这与旧实现相同，不是视觉输入适配；截图体积和上下文预算应在明确模型格式后单独处理。不能仅按消息数量切片来解决 token 成本。

</details>

## 开发与发布

使用 Go 1.25.0 或更新版本。从仓库根目录构建 CLI，或运行全工作区测试。更新模块依赖后可用 `go work sync` 同步。 [开发规范](./AGENTS.md)

```sh
go build -o /tmp/agent-engine ./cli/cmd/agent-engine

go test ./compression/... ./agent/... ./agent-skill/... ./ai-sdk/... \
  ./ai-tool-loop/... ./ai-tools/... ./mcp-runtime/... ./workspace/... ./cli/...
```

远程桌面 Agent 示例使用项目的工具循环，通过任务提示检查远程桌面。 [示例与配置](./examples/README_cn.md)

```sh
go run ./examples/remote-desktop-agent/main.go ./examples/remote-desktop-agent/ocr-tool.go \
  -task "查看远程桌面当前打开的应用"
```

公开 SDK 使用 `ai-sdk/v*` 标签。推送 `cli/v*` 标签触发 CLI 二进制发布，其余模块随仓库源码构建。 [CLI 发布流程](./cli/README_cn.md#发布流程)

## 许可证

MIT
