# 对话上下文压缩

`compression` 可独立使用。Agent 通过 `Options.Compression` 启用压缩，Workspace 通过 `.agents/workspace.json` 中的 `compression` 对象启用。

~~~json
{
  "maxHistoryRounds": 10,
  "compression": {
    "contextWindow": 128000,
    "triggerPercent": 80,
    "toolResultTokens": 1024,
    "keepRecentRounds": 2,
    "maxRecentRounds": 4
  }
}
~~~

将 `contextWindow` 设置为所用模型的上下文容量。省略时只按轮数触发摘要。可选的 `summaryModel` 默认使用会话模型，两者使用同一 provider。可用 `summaryProviderOptions` 为摘要指定 provider 参数。普通请求如显式设置 `store`，摘要请求使用相同值。

- 长工具返回保留状态、错误、路径等结构化字段，以及长文本的关键片段。独立压缩结果的 `ToolArchives` 保存完整内容，模型消息只携带精简后的 `Content`。Tool Loop 的 `Result.Messages` 保留原始执行记录。工具调用参数、调用 ID 和结果配对会保留；非文本附件继续用原有内容。
- 工具裁剪之后，超过轮数上限或达到 token 占用阈值时，模型将旧轮次压成带标记的摘要。摘要包含用户目标、已做决策、关键事实和数据、未完成事项及重要约定。模型可根据相关性在配置范围内建议保留的连续近期轮数。运行时会保留系统和开发者消息、当前用户请求和最新的工具调用批次。轮次以 assistant 回复及其工具结果为单位，连续的工具执行步骤可以各占一轮。
- `compression.Measure(messages, contextWindow)` 返回估算的消息 token 数和占用百分比。可通过 `Config.CountTokens` 注入模型对应的计数器，再调用 `Compressor.Measure`。计数范围是消息，工具定义和输出预留另计。估算中的媒体成本由固定值近似。
- 独立调用 `Compressor.Compact` 时可设置 `ForceSummary` 主动生成摘要。将 `KeepRecentRounds` 显式设为 0 可仅保留摘要和系统、开发者消息。`Result.ContextMessages` 可作为下一次调用的历史使用，持续折叠此前的摘要。

Workspace 保留完整的 NDJSON 日志。生成摘要且运行正常结束后，Workspace 将摘要、近期上下文和日志位置保存到 `.agents/state/context-checkpoint.json`。此后的模型请求使用检查点和新增消息，重开后也从检查点继续。失败、取消或达到步数上限的运行会写入日志，但不会移动检查点。若当前请求或近期必保留的轮次已经很大，压缩后的占用率仍可能超过阈值。
