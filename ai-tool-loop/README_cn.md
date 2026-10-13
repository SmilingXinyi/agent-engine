# ai-tool-loop

通过 `ai-sdk.Client` 执行顺序模型/工具循环。模型调用和工具执行只有一套实现；`agent` 负责组织 Prompt/History，`mcp-runtime` 负责连接与工具适配。

```text
example → agent → ai-tool-loop → ai-sdk
example → mcp-runtime → ai-tool-loop/protocol
```

最小调用（`client` 为已配置好的 `*ai.Client`）：

```go
import (
    "context"
    "fmt"

    aiprot "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
    loop "github.com/SmilingXinyi/agent-engine/ai-tool-loop"
    "github.com/SmilingXinyi/agent-engine/ai-tool-loop/protocol"
)

result, err := loop.New(client).Run(context.Background(), protocol.Request{
    Model: "your-model",
    Messages: []aiprot.Message{aiprot.TextMessage(aiprot.RoleUser, "hello")},
    Options: protocol.RunOptions{MaxSteps: 10},
})
fmt.Println(result.LastResponse.Message.Text())
fmt.Println(result.StopReason, result.Usage.TotalTokens, err)
```

`Request.Tools` 接收 `[]protocol.Tool`，每个工具为 SDK `ToolDefinition` 加必填的 `Execute` 函数。输入消息、定义、参数、执行记录及 Hook 快照相互隔离；metadata 使用可复制的无环数据，函数、error 和不可见结构字段保持原值。

## 执行与停止

流程为模型调用 → 校验整批工具调用 → 顺序执行 → 追加 assistant/tool 消息 → 再次调用模型。`MaxSteps` 包含最终回答，零值使用默认 10，负值无效。工具不会由循环自动重试。

| StopReason | 行为 |
| --- | --- |
| `StopNatural` | 模型没有工具调用，error 为 nil；不代表业务任务一定完成 |
| `StopMaxSteps` | 达到模型调用上限，该轮工具不执行，error 为 nil |
| `StopError` | 模型、协议校验、Hook 或 context 错误，返回部分执行记录及 error |

完整工具批次中的 ID、名称、重复 ID 和 JSON object 参数在任何工具执行前校验。未知工具、普通执行错误及工具 panic 会作为 `IsError` 结果回填模型。循环在各执行边界检查 context，取消后停止后续工具和模型调用。

`Result.LastResponse` 是最后一次模型响应，usage 保持单次值。`Result.Usage` 为累计值；`Messages` 包括输入及已产生的消息，`Steps` 保存每步响应和时间，`ToolCalls` 保存执行参数、结果、错误和时间。`Result.Clone()` 可取得独立的数据副本。截止限步或中途失败时，Messages 可能包含尚未执行完整的工具批次，本期不提供自动续接。

## 生命周期 Hook

`Options.Hooks` 提供 `OnRunStart/End`、`OnStepStart/End`、`OnToolStart/End`，签名均为 `func(context.Context, 对应Info) error`。Info 带相应的 RunID、Step、RequestID、输入/结果和时间信息。回调修改快照不会改写执行数据；返回错误会终止执行，panic 转为错误。

```text
RunStart → StepStart → SDK.OnRequest/OnResponse
         → ToolStart → 执行 → ToolEnd → StepEnd
         → 下一步 … → RunEnd
```

验证失败尚未进入生命周期时不触发 Hooks。进入生命周期后，即使 Start Hook 失败，也会触发对应 End；结束 Hook 错误与原错误合并。普通工具错误交由模型处理，需要停止时可在 ToolEnd Hook 根据 `info.Error` 或 `info.Result.IsError` 返回错误。

每次运行自动生成 RunID，每步生成 RequestID，SDK 请求 metadata 包含 `run_id`、`request_id`、`step`。SDK 请求/响应日志继续使用 `ai.Hooks`，可由 RequestID 与循环记录关联。

本期仅提供非流式顺序执行，没有并行、外部工具占位、执行参数改写或续接接口；会话持久化、截图编码和业务判断由其他层负责。
