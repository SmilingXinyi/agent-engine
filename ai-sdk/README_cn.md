# AI SDK

`ai-sdk` 是独立 Go 模块，负责一次模型调用。`ModelRequest` 统一表达消息、工具和模型参数；
`Generate` 返回统一结果，`Stream` 按原始 SSE 帧交付上游输出。它不执行工具，不管理历史，
也不要求接入 agent 或 MCP。Chat Completions 的非流式原生对象使用 `Chat`。

## 安装和模块边界

要求 Go 1.25+，只有标准库依赖。

使用 `go get github.com/SmilingXinyi/agent-engine/ai-sdk@v0.5.0` 固定版本。
Go 子模块发布标签为 `ai-sdk/v0.5.0`。这一版为 `Stream` 和 `ChatStream` 增加
`FirstFrameTimeout` 与 `IdleTimeout`，零值关闭。超时返回 `ErrorTimeout`，并用
`TimeoutKind` 区分 `first_frame` 和 `idle`。`Generate` 与 `Chat` 不使用这两项。
`openai.Accumulator` 仍把 Chat Completions 的 SSE `data` 拼成一份 `chat.completion`。
`ChatStream` 仍返回原始帧，原生消息仍放在 `ChatRequest.Messages`。`Chat` 继续负责非流式
`chat.completion`。本地开发可以在调用项目的 `go.work` 中引用此模块，或通过 `go.mod` 的
`replace` 指向本地 `ai-sdk` 目录。

- `protocol/types.go`：公共类型、接口和常量，包括错误、hooks 和能力说明。
- `protocol/methods.go`：消息构造、结果提取、快照复制和错误链行为。
- 模块根目录：一次调用的生命周期。
- `providers/openai`：HTTP、SSE 和两种 OpenAI API 格式的适配；原生扩展也在这里。

在 `ai-sdk` 目录独立验证：

```sh
GOWORK=off go test -race -count=1 ./...
GOWORK=off go vet ./...
GOWORK=off go test -race -tags=integration -count=1 ./...
```

`integration` 测试启动真实本地 HTTP 服务，需要允许绑定回环端口；覆盖两种 API 的
原始帧交付、分块传输、取消传到服务端及断连不重放，不调用外部模型厂商。

## 最小调用

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"

    ai "github.com/SmilingXinyi/agent-engine/ai-sdk"
    "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"
    "github.com/SmilingXinyi/agent-engine/ai-sdk/providers/openai"
)

func main() {
    provider, err := openai.New(openai.Config{
        BaseURL: "https://api.openai.com/v1",
        APIKey: os.Getenv("OPENAI_API_KEY"),
        APIType: openai.APITypeChatCompletions,
        ResponseHeaderTimeout: 30 * time.Second,
    })
    if err != nil { panic(err) }

    client := ai.NewClient(provider, protocol.Hooks{})
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    response, err := client.Generate(ctx, protocol.ModelRequest{
        Model: "your-model",
        Messages: []protocol.Message{
            protocol.TextMessage(protocol.RoleUser, "用一句话解释依赖倒置。"),
        },
    })
    if err != nil { panic(err) }
    fmt.Println(response.Message.Text())
}
```

`provider.Generate` / `provider.Stream` 也可以直接使用；只有需要生命周期 hooks
时才需要 `Client`。`Provider` 不要求流式能力，支持流的实现额外实现
`protocol.StreamingProvider`。调用方也可以实现自己的 provider，无需修改模块根目录。

## 统一参数和原生参数

常用参数放在 `ModelRequest`，例如 `Temperature`、`MaxOutputTokens`、`ToolChoice`，
以及明确的推理强度和输出格式：

```go
request := protocol.ModelRequest{
    Model: "your-model",
    Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "输出答案对象。")},
    Reasoning: &protocol.ReasoningOptions{Effort: "low"},
    OutputFormat: &protocol.OutputFormat{
        Type: protocol.OutputJSONSchema,
        Name: "answer",
        Strict: true,
        Schema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`),
    },
}
```

该片段需要 `encoding/json`。`OutputText`、`OutputJSON`、`OutputJSONSchema` 分别对应
普通文本、JSON 对象和 JSON Schema。adapter 负责映射两种 API 的字段格式，实际模型
是否支持这些选项仍由上游决定。Chat Completions 的输出 token 上限映射为 `max_tokens`。
其他原生顶层字段放在 `ProviderOptions`；需要直接传原生消息对象时使用 `Chat`。

`ProviderOptions` 是**上游 HTTP JSON 顶层字段**，不做别名猜测或跨厂商转换。
与公共字段冲突时返回 `ErrorInvalidRequest`。上游 `metadata` 可以放在这里；
`ModelRequest.Metadata` 用于本地关联信息，不自动发送给上游。

例如配置 DeepSeek 兼容端点：

```go
provider, err := openai.New(openai.Config{
    Name: "deepseek",
    BaseURL: "https://api.deepseek.com",
    APIKey: os.Getenv("DEEPSEEK_API_KEY"),
})
request := protocol.ModelRequest{
    Model: "your-deepseek-model",
    Messages: []protocol.Message{protocol.TextMessage(protocol.RoleUser, "你好")},
    ProviderOptions: map[string]any{
        "thinking": map[string]any{"type": "disabled"},
    },
}
```

只有上游模型支持该原生 `thinking` 字段时才使用它。其他厂商按其 HTTP API
要求填写字段，不自动改写为 `reasoning`。Python 等 SDK 的 `extra_body` 是封装参数，
不能直接作为 HTTP 字段使用；本模块会拒绝它，请把其中的字段直接放到 `ProviderOptions`。

`Generate` 的统一返回值只表达一个模型结果，`ModelRequest` 调用仍限制单个候选。
消息顶层扩展通过下文的 `Message.ProviderOptions` 传递。需要按原生消息对象调用
Chat Completions 时使用 `Chat`。未知原生内容块也可放在
`ContentPart{Type: protocol.ContentProvider, Data: ...}` 中，其 JSON 对象按原样结构编码，
不会变成可跨厂商复用的内容。

## 消息级扩展字段

`ModelRequest.ProviderOptions` 控制请求顶层字段；`Message.ProviderOptions` 控制某条
原生消息对象的字段；`ContentProvider` 承载原生内容块。这三个层级不会相互合并。
例如某个兼容服务支持消息级 `vendor_extension` 时，可以继续使用统一调用接口：

```go
message := protocol.TextMessage(protocol.RoleUser, "你好")
message.ProviderOptions = map[string]any{
    "vendor_extension": map[string]any{"enabled": true},
}
request := protocol.ModelRequest{Model: "your-model", Messages: []protocol.Message{message}}
```

实际字段由上游 API 决定，adapter 不猜测别名或保证跨厂商兼容。Generate 和 Stream
使用相同编码。Chat Completions 合并到对应的 `messages[]` 对象，支持所有已定义角色；
Responses 合并到对应的 `input[]` 消息对象，或单个 `function_call_output` 对象。
assistant 同时包含文本和工具调用时，Responses 扩展只加到消息对象，不加到工具调用。

- Chat Completions 保留 `role`、`name`、`content`、`reasoning_content`、`tool_calls`、
  `tool_call_id` 给公共字段映射。Responses 保留 `type`、`role`、`name`、`content`、
  `call_id`、`output`、`arguments`。使用扩展覆盖这些字段会被拒绝。
- Responses 将 system/developer 消息转换成 `instructions`，无法表达消息级扩展；
  此时返回 `ErrorUnsupported`。
- Responses 无内容的消息、只有工具调用的消息没有对应的原生 message 对象，带扩展时
  返回 `ErrorUnsupported`。函数调用扩展没有对应的统一消息对象。
- 一条工具消息含多个工具结果时，扩展没有唯一对应对象，返回 `ErrorUnsupported`；
  可拆成每条一个工具结果的消息后分别配置。
- 输入扩展按 JSON 形状深拷贝，生命周期回调修改它们不会改变调用或其他观察者。
- 响应中的厂商原生字段仍保存在 `ModelResponse.Raw` 和 `StreamChunk` 中，不会自动填入
  `Message.ProviderOptions` 或在下一轮调用中回放。`Message.Metadata` 仍是本地信息。

## 按原始 SSE 帧读取

从 v0.2.1 迁移：删除 `EventSink` / `EventSinkFunc`、`StreamEvent` / `StreamEventType`、
事件常量、`ToolCallDelta`、`Config.OnRawEvent` 和 `openai.RawEvent`。不保留兼容别名，
也不新增 `StreamNative`。旧三参数 Stream 调用改为下面的迭代形式；流式 response hook
从 `ResponseInfo.Response` 改为读取 `ResponseInfo.Stream`。

`Client.Stream(ctx, request)` 和 `provider.Stream(ctx, request)` 返回 `protocol.Stream`。
SDK 在打开时完成请求编码、鉴权、重试和 HTTP 状态检查，成功响应体直到 `Next()` 才开始读取。
下面的片段用于返回 `error` 的函数，`writer` 为一个 `io.Writer`：

```go
stream, err := client.Stream(ctx, request)
if err != nil { return err }
defer stream.Close()

for stream.Next() {
    chunk := stream.Chunk()
    if _, err := writer.Write(chunk.Frame); err != nil {
        return errors.Join(err, stream.Close())
    }
}
return stream.Err()
```

该片段还需导入 `errors`。主接口不使用 sink、channel 或后台读取 goroutine，不累积文本、
推理、工具参数和 usage，不返回聚合后的 `ModelResponse`。本轮不提供内容归一化或聚合器。

| 接口 | 含义 |
|---|---|
| `Next()` | 同步读取下一帧；有帧返回 true，结束或出错返回 false |
| `Chunk()` | 当前帧；仅在最近一次 `Next()` 返回 true 后读取 |
| `Err()` | 打开成功后的读取、协议结束、关闭及 response hook 错误 |
| `Close()` | 幂等释放资源；提前关闭通过 `Err()` 报告取消，返回值包含关闭和 hook 错误 |
| `Info()` | 独立的 `CallInfo` 与 `Complete` 快照，不包含模型内容 |

`StreamChunk` 的字段：

- `Frame`：完整原始 SSE 字节，包含换行和帧分隔符，适用于保真保存或转发。
- `Event`、`ID`：本帧的 `event`、`id` 字段；不会继承上一帧的 ID。
- `Data`：按 SSE 规则用 LF 合并多行 `data:`，移除冒号后最多一个空格，不保证是 JSON。

一次 `Next()` 对应一个帧，不因同一帧包含多种模型内容而拆分。注释、空帧、未知字段、
未知事件、非 JSON 数据和协议终止帧都会保留。只读取响应开头到协议终止的帧；不会越过终止帧
继续读取或制造额外 finish chunk。支持 LF、CRLF、CR、开头的 UTF-8 BOM 和正常 EOF 时
最后一个未封口的帧；BOM 仅在字段解析时忽略，`Frame` 保留它。

不同 HTTP 读取分块不会改变 SSE 帧边界。`Frame` 不代表 TCP 分包或 HTTP chunked 分块，
也不包含 HTTP headers。`Frame` 和 `Data` 可在后续 `Next()` 后继续持有或修改，SDK 不覆写它们。

## 完成、错误和关闭

- Chat Completions 的 `[DONE]` 原样交付一次后结束；默认也接受明确 `finish_reason` 后的
  正常 EOF。不能在 finish_reason 到来时提前停读，因为后面可能还有 usage。
  `RequireDone=true` 可以要求必须有 `[DONE]`。
- Responses 的 `response.completed` / `response.incomplete` 终止帧原样交付后结束；
  单独 `[DONE]` 仍交付，但通过 `Err()` 报告 `ErrorStreamIncomplete`。
- 已识别的 `error` / `response.failed` 等失败帧原样交付，再通过 `Err()` 报告 `ErrorUpstream`。
  未知和非 JSON 数据不做模型内容校验；SDK 只探测已知终止与失败标记。
- 缺少终止依据、读取错误、体积超限、超时和取消都不会标记为正常完成。
  非 EOF 读取失败时，未组成帧的残缺片段不会被伪装成完整帧交付。
- `Info().Complete` 仅表示上游正常结束，不表示业务答案完整。上游已正常结束后，
  response hook 或响应体关闭错误不会改写它；仍需检查 `Err()`。
- 无论正常结束、失败或提前退出，都必须 `Close()`。正常结束和失败会自动释放响应体；
  `Close()` 同时确保 Client 的生命周期 hooks 已执行。提前 break 时检查 `Close()` 的返回值和 `Err()`。
- `Next()` 和 `Chunk()` 串行使用，`Close()` 可以从另一 goroutine 中断阻塞读取；context
  取消也可以中断读取。SDK 不自动重连或重放已经打开的成功响应。

## 原生 Chat Completions

原生消息使用 `ChatRequest`。`Messages` 是 `[]map[string]any`，不会和
`protocol.ModelRequest` 的 `[]Message` 混在同一个字段里。

`Chat` 是非流式调用，返回 `map[string]any`，`object` 为 `chat.completion`，未知字段保留。
`ChatStream` 强制 `stream: true`，按原始 SSE 帧返回，用法与 `Stream` 的 reader 相同。
两者都不注入 `stream_options`。`Chat` 在 `Stream` 为 true 时拒绝。

```go
response, err := provider.Chat(ctx, openai.ChatRequest{
    Model: "your-model",
    Messages: []map[string]any{
        {"role": "user", "content": "你好", "vendor_extension": true},
    },
})

stream, err := provider.ChatStream(ctx, openai.ChatRequest{
    Model: "your-model",
    Messages: []map[string]any{
        {"role": "user", "content": "你好"},
    },
})
```

`Generate` 和 `Stream` 仍使用 `protocol.ModelRequest`。`Generate` 的 `Raw` 保留上游原始响应 JSON，包括未知字段。

流本身不累计正文。需要把 Chat Completions 的 `data` 拼回去时，使用 `openai.Accumulator`：
`Add` 接收每一帧的 `Data`，`[DONE]` 和空数据会被忽略，坏 JSON 返回错误。`Finished` 表示见过非空
`finish_reason`，之后的 usage 帧仍应继续 `Add`。`Completion` 返回 `object` 为 `chat.completion` 的对象，
`usage` 保留全部字段。`Usage` 只返回 `prompt_tokens` 和 `completion_tokens`，缺省为 0。
只读取 `choices[0]`，不处理 `tool_calls`。它不会改变 `Stream` 或 `Info().Complete`。

## 错误和观测

```go
var failure *protocol.Error
if errors.As(err, &failure) {
    fmt.Println(failure.Code, failure.CallInfo.StatusCode, failure.CallInfo.Attempts)
}
if errors.Is(err, context.DeadlineExceeded) {
    // 调用方 deadline 或注入的 HTTPClient 超时。
}
```

错误提供 `Code`、`Provider`、`Message`、`Retryable`、`CallInfo`、诊断 `Body` 和底层
`Cause`。`Retryable` 表示可能是暂时性失败，不授权重新播放已经对外输出的流。
HTTP 失败保留最后一次响应的状态、headers 和错误 body，body 最多读取 `MaxResponseBytes`。

`Generate` 通过 `ModelResponse.CallInfo` 提供尝试次数和最终 HTTP 状态、headers；
流式调用使用 `stream.Info().CallInfo`。打开失败时读取 `protocol.Error.CallInfo`。

`OnRequest` 在请求前执行。`OnResponse` 对 Generate 在返回时执行；对 Stream 在耗尽、
失败或关闭时执行且只执行一次，Duration 覆盖整个消费周期。打开失败也会触发 response hook，
request hook 拒绝或 provider 不支持流式时不会触发。
流式 hook 使用 `ResponseInfo.Stream` 摘要，`ResponseInfo.Response` 保持零值；
Generate 继续使用 `ResponseInfo.Response`，其 Stream 为 nil。

Hook 参数的消息、JSON 扩展、headers 和 Stream 摘要均为独立快照；JSON 形状递归复制，
应用自定义指针仍然共享。Hook 错误合并到调用错误中，panic 原样传播。

逐次 HTTP 观测通过 `Config.OnAttempt`，可获得尝试编号、状态码、错误分类、是否重试和
等待时长。HTTP 错误的完整 body 在最终错误中提供，attempt callback 不读取 body。

## 超时、重试和体积限制

| 配置 | 默认行为 |
|---|---|
| HTTPClient 总超时 | 不设；用 context 控制整个调用 |
| ResponseHeaderTimeout | 0，不设响应头等待超时；只作用于默认 transport |
| Retry.MaxRetries | 0，不自动重试 |
| Retry.Backoff / MaxBackoff | 启用重试时为 200ms / 5s，指数退避 |
| MaxResponseBytes | 16 MiB，非流式及 HTTP 错误 body |
| MaxStreamBytes | 0，不限制累计流字节 |
| MaxEventBytes | 1 MiB，单个 SSE 帧，包含换行和其他 framing |
| RequireDone | false，只影响 Chat Completions |
| FirstFrameTimeout | 0，关闭。只用于 Stream / ChatStream，等待第一个非空 data 帧 |
| IdleTimeout | 0，关闭。见过非空 data 之后，限制 Next 里的下一次等待 |

注入 `HTTPClient` 时由它决定 transport 和超时，不能同时配置 `ResponseHeaderTimeout`。
响应头超时不限制响应体读取。流式的首帧和空闲时限用上面两项，不替代调用方 context 的总时限。
注释帧和空 data 不会结束首帧等待，也不会把首帧截止时间往后推。空闲计时在每个非空 data 帧之后、
下一次 `Next` 阻塞时重新开始，两次 `Next` 之间的调用方处理时间不算空闲。
`ModelRequest` 和 `ChatRequest` 上的对应指针：`nil` 沿用 Provider，`0` 关闭这一次，正数覆盖。
首帧超时的 `Retryable` 为真，空闲超时为假。`Generate` 和 `Chat` 忽略这两项。配置中的负数均为错误，不用负值表达“关闭”。

需要重试时显式配置 `Retry: openai.RetryPolicy{MaxRetries: 2}`。仅对暂时性网络错误和
HTTP 408、429、5xx 进行重试；永久错误及已取消的 context 不重试。HTTP `Retry-After`
支持秒数和日期，它是等待下限，可以超过 `MaxBackoff`，context 可提前终止等待。
收到成功 HTTP 响应后，body 读取、SSE 读取和生命周期 hook 失败都不会重放请求。
尝试次数统计应用层发送，不包含注入的 HTTP transport 内部可能发生的透明重试。

## 能力范围

`protocol.CapabilityProvider` 是可选接口。`Capabilities()` 表达 adapter 已实现的
编码能力，不保证具体模型支持全部能力。

当前 OpenAI adapter 支持文本、图片、文件、工具请求、推理输出和原生内容块。
Chat Completions 额外支持用户的内联 WAV/MP3 音频输入；Responses adapter 不支持
统一音频输入。工具结果在统一接口中目前只支持文本。Responses 的 reasoning summary
是输出摘要，不作为普通历史输入回放。上游音频输出和富内容工具结果不在统一接口中。
