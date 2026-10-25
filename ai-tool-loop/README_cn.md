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
    Options: protocol.RunOptions{MaxSteps: 40, MaxHistoryRounds: 10},
})
fmt.Println(result.LastResponse.Message.Text())
fmt.Println(result.StopReason, result.Usage.TotalTokens, err)
```

`Request.Tools` 接收 `[]protocol.Tool`，每个工具为 SDK `ToolDefinition` 加必填的 `Execute` 函数。输入消息、定义、参数、执行记录及 Hook 快照相互隔离；metadata 使用可复制的无环数据，函数、error 和不可见结构字段保持原值。

每一步模型调用都会带上 `Temperature`、`MaxOutputTokens`、`ToolChoice`、`Reasoning`、`OutputFormat` 和 `ProviderOptions`。这些字段与 `ai-sdk` 的 `ModelRequest` 含义相同，循环不改写、不猜测别名。消息上的 `ProviderOptions` 和 `ContentProvider` 内容随消息原样进入后续调用；模型响应里的原生字段留在 `ModelResponse.Raw`，不会被写回请求扩展。`Metadata` 会复制到每一步，其中 `run_id`、`request_id`、`step` 由循环写入并覆盖同名调用方值。流式超时只作用于 SDK 的 `Stream` / `ChatStream`，本模块仍只使用非流式 `Generate`。

## 执行与停止

流程为模型调用 → 校验整批工具调用 → 顺序执行 → 追加 assistant/tool 消息 → 再次调用模型。`MaxSteps` 包含最终回答，零值使用默认 10，负值无效。工具不会由循环自动重试。

`Options.MaxHistoryRounds` 控制每次模型请求保留的历史轮数，默认 `10`，`0` 使用默认值，负值无效。它与控制总执行次数的 `MaxSteps` 相互独立。超出上限时从最早一轮开始省略；所有 `system` 消息始终保留且不计入轮数。`user` / `developer` 输入与随后一次 assistant 回复及其全部 tool 结果组成一轮；没有新用户输入的连续 assistant 回复各算一轮，尚未得到回复的新输入也占一轮。多工具结果不会拆开，也不会留下裁剪造成的孤立 tool 消息。

裁剪在 `Request.ModelCall` 组装本轮消息时进行，只改变发给模型及 Step Hook 的请求副本。`Result.Messages`、`Steps`、`ToolCalls` 仍保留完整执行记录。最初的用户任务也可能随旧轮次被省略；必须始终保留的规则应放在 system prompt 中。窗口按轮数而非 token 或字节计数，不保证请求体一定低于模型 API 上限。

| StopReason | 行为 |
| --- | --- |
| `StopNatural` | 模型没有工具调用，error 为 nil；不代表业务任务一定完成 |
| `StopMaxSteps` | 达到模型调用上限，该轮工具不执行，error 为 nil |
| `StopError` | 模型、协议校验、确认回调、Hook 或 context 错误，或配置为拒绝即终止，返回部分执行记录及 error |

完整工具批次中的 ID、名称、重复 ID 和 JSON object 参数在任何工具执行前校验。未知工具、普通执行错误及工具 panic 会作为 `IsError` 结果回填模型。循环在各执行边界检查 context，取消后停止后续工具和模型调用。

模型返回的工具参数不是 JSON object 时，错误包含工具名、调用 ID、带引号转义的原始参数及 JSON 解码错误；工具调用校验错误还会附带 step、request ID 和模型的 `finish_reason`。校验发生在该轮工具执行之前，因此这类调用不会触发 ToolStart Hook，可通过 SDK 的 OnResponse Hook 记录原始模型调用。

`Result.LastResponse` 是最后一次模型响应，usage 保持单次值。`Result.Usage` 为累计值；`Messages` 包括输入及已产生的消息，`Steps` 保存每步响应和时间，`ToolCalls` 保存执行参数、结果、错误和时间。`Result.Clone()` 可取得独立的数据副本。截止限步或中途失败时，Messages 可能包含尚未执行完整的工具批次，本期不提供自动续接。

## 工具执行确认

`Options.ConfirmTool` 在每次已知工具执行前同步调用，适用于本地、Skill 和 MCP 工具。回调接收 `ToolConfirmationInfo`，包含 `RunID`、`Step`、`RequestID`、`Call`（名称、ID、参数）及 `Definition`。调用方可以按名称和参数直接批准，也可以等待终端或 UI 的用户决定；未配置回调时保持直接执行。

以下函数将调用方的询问函数接入确认流程。`askUser` 接收 context 和展示文本，由调用方接终端或 UI，并应在 context 取消时结束等待。

```go
func confirmationOptions(askUser func(context.Context, string) (bool, error)) protocol.RunOptions {
    return protocol.RunOptions{
        ToolDenialPolicy: protocol.ToolDenialContinue, // 生产：拒绝后反馈模型并继续
        ConfirmTool: func(ctx context.Context, info protocol.ToolConfirmationInfo) (protocol.ToolConfirmationDecision, error) {
            if info.Call.Name == "get_skill" || info.Call.Name == "read_file" {
                return protocol.ToolConfirmationDecision{Approved: true}, nil
            }
            approved, err := askUser(ctx, fmt.Sprintf("执行工具 %s？\n参数：%s", info.Call.Name, info.Call.Arguments))
            if err != nil {
                return protocol.ToolConfirmationDecision{}, err
            }
            if !approved {
                return protocol.ToolConfirmationDecision{Reason: "用户拒绝执行"}, nil
            }
            return protocol.ToolConfirmationDecision{Approved: true}, nil
        },
    }
}
```

将返回值赋给 `Request.Options`；调试时设置 `options.ToolDenialPolicy = protocol.ToolDenialStop`。策略为空时默认采用 `ToolDenialContinue`，其他非法值在运行开始前报错，不会根据环境自动切换。

- **继续策略**：拒绝的工具不执行，产生包含拒绝说明及可选 `Reason` 的 `IsError` 工具结果；同批后续工具继续处理，结果一起回填模型。单纯拒绝不导致运行返回 error。
- **停止策略**：保存当前拒绝记录和工具消息，返回 `StopError` 与 `*protocol.ToolDeniedError`；不再执行后续工具或请求模型。可用 `errors.As(err, &denied)` 识别拒绝，其中 `denied` 声明为 `*protocol.ToolDeniedError`。同批尚未处理的调用可能仍缺少结果，部分历史不能直接重放。

`ToolConfirmationDecision` 的零值表示拒绝。`ToolCallRecord.Confirmation` 和 `ToolEndInfo.Confirmation` 保存成功返回的决定；未调用确认、回调报错或 panic 时为 nil。两种策略下，拒绝均写入记录的 `Error`，并通过 `ToolEndInfo.Error` 提供可识别的 `ToolDeniedError`。

确认位于 `OnToolStart` 之后、执行器之前；未知工具、整批参数校验失败及达到 MaxSteps 上限都不触发确认。回调收到独立快照，修改参数或定义不会改变实际执行，也不会向模型添加确认配置。每次调用独立确认，不自动缓存授权或重试；直接调用 `Tool.Execute` 不经过循环的确认机制。

确认回调返回 error 或 panic 时，无论拒绝策略如何，均终止运行并保留部分记录；结束 Hooks 仍配对触发，错误链保留并合并。批准后再次检查 context，已取消时不会执行工具。回调同步运行，不启动后台确认 goroutine，因此回调自身必须响应 context 取消。

## 生命周期 Hook

`Options.Hooks` 提供 `OnRunStart/End`、`OnStepStart/End`、`OnToolStart/End`，签名均为 `func(context.Context, 对应Info) error`。Info 带相应的 RunID、Step、RequestID、输入/结果和时间信息。回调修改快照不会改写执行数据；返回错误会终止执行，panic 转为错误。

```text
RunStart → StepStart → SDK.OnRequest/OnResponse
         → ToolStart → 查找工具 → 确认（如已配置）→ 执行（如批准）→ ToolEnd → StepEnd
         → 下一步 … → RunEnd
```

验证失败尚未进入生命周期时不触发 Hooks。进入生命周期后，即使 Start Hook 失败，也会触发对应 End；结束 Hook 错误与原错误合并。普通工具错误交由模型处理，需要停止时可在 ToolEnd Hook 根据 `info.Error` 或 `info.Result.IsError` 返回错误。

每次运行自动生成 RunID，每步生成 RequestID，SDK 请求 metadata 包含 `run_id`、`request_id`、`step`。SDK 请求/响应日志继续使用 `aiprot.Hooks`，可由 RequestID 与循环记录关联。

本期仅提供非流式顺序执行，没有并行、外部工具占位、执行参数改写或续接接口；会话持久化、截图编码和业务判断由其他层负责。
