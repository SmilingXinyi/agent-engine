# Agent Engine 工作区

本项目基于 Go 1.25 的 `go.work` 多模块工作区方案重构。

## 模块结构

- `ai-sdk`: 核心 AI SDK 模块。

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
go test ./...
```

## 许可证

MIT
