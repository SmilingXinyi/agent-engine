# Agent Engine 工作区

本项目基于 Go 1.25 的 `go.work` 多模块工作区方案重构。

## 模块结构

- `ai-sdk`: 核心 AI SDK 模块。
- `ai-tool-loop`: 顺序模型/工具循环、生命周期 Hook 和执行记录，参见[使用说明](./ai-tool-loop/README_cn.md)。
- `agent`: Prompt 与历史上下文组织，委托 AI Tool Loop 执行，参见[使用说明](./agent/README_cn.md)。
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
go test ./agent/... ./ai-sdk/... ./ai-tool-loop/... ./mcp-runtime/...
```

## 许可证

MIT
