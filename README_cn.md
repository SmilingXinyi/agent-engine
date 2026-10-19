# Agent Engine 工作区

本项目基于 Go 1.25 的 `go.work` 多模块工作区方案重构。

## 模块结构

- `ai-sdk`: 核心 AI SDK 模块。
- `tool-loop`: 顺序模型/工具循环、生命周期 Hook 和执行记录，参见[使用说明](./tool-loop/README_cn.md)。
- `agent`: Prompt 与历史上下文组织，委托 tool-loop 执行，参见[使用说明](./agent/README_cn.md)。
- `agent-skill`: 本地 SKILL.md 解析、技能目录和按需读取工具，参见[使用说明](./agent-skill/README_cn.md)。
- `mcp-runtime`: MCP stdio / Streamable HTTP 连接、工具发现及执行适配，参见[使用说明](./mcp-runtime/README_cn.md)。

## 开发规范

详细代码风格与命名约定规范请参考 [AGENTS.md](./AGENTS.md)。

## 快速开始

### 前置要求

- Go 1.25.0 或更高版本

### 工作区管理

同步工作区依赖：

```bash
go work sync
```

### 运行测试

执行测试：

```bash
go test ./agent/... ./agent-skill/... ./ai-sdk/... ./tool-loop/... ./mcp-runtime/...
```

## 历史实现的取舍

`out/` 是历史参考代码。新框架保持单向依赖：`agent → tool-loop → ai-sdk`，`agent-skill` 与 `mcp-runtime` 提供 loop 工具，由调用方组装。Agent 不加载目录、连接 MCP、创建 Provider 或保存会话。

| 历史能力 | 当前处理 | 原因 |
| --- | --- | --- |
| Skill frontmatter、目录和 get_skill | 重构为独立 agent-skill，目录只展示元数据，正文按需进入模型上下文 | 保留渐进式上下文，适合中文及复杂描述 |
| Skill 关键词评分、前 5 项、每次调用重扫目录 | 移除；精确名称查询和固定快照 | 避免误选、隐藏策略与不稳定输入 |
| MCP stdio / HTTP、环境变量、headers、分页、连接释放 | 保留现有实现；直接提供 loop Tool | 去掉 Engine Tool / Loop Tool 双向适配 |
| MCP schema 校验 | 补齐根 type 校验；HTTP header 名和值提前校验 | 启动时发现错误，避免执行期间才失败 |
| MCP JSON 参数经 map[string]any 转换 | 改为保留原始 JSON 值 | 防止大整数精度丢失 |
| MCP legacy SSE、重连参数、常驻 GET SSE | 暂不引入；当前 stdio 和 Streamable HTTP 足够 | 有具体服务需求后再扩展传输，不扩大默认配置 |
| Engine 历史工具调用/结果校验 | 重写到 Agent 的上下文校验中 | 不将孤立、错配、未完成的工具批次传给模型 |
| Engine 工厂、重复消息类型、生命周期桥接、DebugSink | 由 SDK Client / Hooks 和 loop 记录承担 | 一种协议、一套循环、一套可观测性，不建第二个抽象层 |
| Engine 按消息数裁剪、隐式 system 覆盖、工具环境注入 | 暂不引入 | 裁剪需要预算策略并保持工具批次完整；消息与参数保持显式输入 |
| Engine 流式、外部工具续接、参数改写、会话持久化 | 保持当前范围，暂缓 | 这些能力需要独立契约与真实调用场景 |

MCP 图片等非文本结果目前仍序列化为 JSON 文本。这与旧实现相同，不是视觉输入适配；截图体积和上下文预算应在明确模型格式后单独处理。不能仅按消息数量切片来解决 token 成本。

## 许可证

MIT
