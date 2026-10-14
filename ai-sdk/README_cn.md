# AI SDK

`ai-sdk` 是独立 Go 模块，负责一次模型调用。统一接口适用于聊天、文本生成、
工具调用请求、流式输出和结构化输出；OpenAI provider 的原生接口适用于模型代理、
厂商扩展和需要保留完整协议的服务。它不执行工具，不管理历史，也不要求接入 agent 或 MCP。

## 安装和模块边界

要求 Go 1.25+，只有标准库依赖。

使用 `go get github.com/SmilingXinyi/agent-engine/ai-sdk@v0.2.1` 固定版本。
Go 子模块发布标签为 `ai-sdk/v0.2.1`，不使用仓库根目录的裸版本标签。
本地开发可以在调用项目的 `go.work` 中引用此模块，或通过
`go.mod` 的 `replace` 指向本地 `ai-sdk` 目录。

- `protocol/types.go`：公共类型、接口和常量，包括错误、hooks 和能力说明。
- `protocol/methods.go`：消息构造、结果提取、快照复制和错误链行为。
- 模块根目录：一次调用的生命周期。
- `providers/openai`：HTTP、SSE 和两种 OpenAI API 格式的适配；原生扩展也在这里。

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
是否支持这些选项仍由上游决定。Chat Completions 的输出 token 上限映射为 `max_tokens`；
需要不同原生字段的服务可使用 `DoNative`。

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

统一返回值只表达一个模型结果。多个候选、厂商内置工具、加密推理项目
等需要完整原生协议的场景使用 `DoNative`。消息顶层扩展通过下文的
`Message.ProviderOptions` 传递。未知原生内容块也可放在
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
  此时返回 `ErrorUnsupported`，需要保留原生消息格式时使用 `DoNative`。
- Responses 无内容的消息、只有工具调用的消息没有对应的原生 message 对象，带扩展时
  返回 `ErrorUnsupported`。函数调用扩展属于另一种原生对象，可通过 `DoNative` 表达。
- 一条工具消息含多个工具结果时，扩展没有唯一对应对象，返回 `ErrorUnsupported`；
  可拆成每条一个工具结果的消息后分别配置。
- 输入扩展按 JSON 形状深拷贝，生命周期回调修改它们不会改变调用或其他观察者。
- 响应中的厂商原生字段仍保存在 `ModelResponse.Raw` 和 `OnRawEvent` 中，不会自动填入
  `Message.ProviderOptions` 或在下一轮调用中回放。`Message.Metadata` 仍是本地信息。

## 流式调用和完成状态

```go
response, err := client.Stream(ctx, request, protocol.EventSinkFunc(
    func(ctx context.Context, event protocol.StreamEvent) error {
        switch event.Type {
        case protocol.EventTextDelta:
            fmt.Print(event.Delta)
        case protocol.EventFinish:
            fmt.Printf("\n结束原因：%s\n", event.FinishReason)
        }
        return nil
    },
))
```

- 所有回调同步执行，其错误立即终止调用；流开始后不自动重试。
- 成功的 provider 流在所有内容、工具参数和 usage 后恰好发出一次 `EventFinish`。
- `Stream` 返回部分结果和错误时，`Complete=false`；部分结果可用于展示和诊断。
- `Complete=true` 表示 provider 完成了结果读取和交付。`FinishLength` 也是已结束的
  模型调用，但不表示业务答案已经完整。不要用文本非空或 `FinishStop` 单独推断成功。
- Client 的 response hook 可以在上游调用成功后返回错误。这时 `Complete` 保持上游
  完成状态，调用方仍需处理返回的错误。
- 工具调用的 `Index` 在本次流内稳定。`Name` 和 `ArgumentsDelta` 是追加片段，
  `ID` 在获知时提供；按索引累积后使用最终 `Message.ToolCalls()`。
- Chat Completions 支持 `[DONE]`，也接受明确 `finish_reason` 后的正常 EOF；中途
  EOF、读取错误和解析失败仍然报错。`RequireDone=true` 可以强制要求结束标记。
- Responses 必须收到 `response.completed` 或 `response.incomplete` 等终止事件，
  单独的 `[DONE]` 不代表 Responses 已经完成。上游 failed 状态返回错误。

两种 SSE 解析器共享完整事件读取，支持 LF、CRLF、多行 `data:` 和 EOF 时最后一个未封口的
事件。不同 HTTP 读取分块不影响事件语义。空流、缺少终止依据的流不会被视为成功。

## 原生 HTTP 和原始 SSE

`DoNative` 接受完整 JSON 对象，其请求体字节不会重新编码或注入公共字段：

```go
native, err := provider.DoNative(ctx, json.RawMessage(`{
    "model":"your-model",
    "stream":true,
    "messages":[{"role":"user","content":"你好","vendor_extension":true}]
}`))
if err != nil { return err }
defer native.Body.Close()
// native.StatusCode、native.Headers、native.Attempts 可用于构建代理响应。
_, err = io.Copy(writer, native.Body)
return err
```

该片段用于返回 `error` 的函数，需导入 `encoding/json` 和 `io`。原生接口返回最终 HTTP
响应，包括非 2xx；传输失败才通过返回值提供 SDK 错误。调用方拥有响应体，必须关闭它。
它使用相同的显式重试策略，但不执行归一化、结果累积、默认体积限制或 `OnRawEvent`；
读取策略、体积限制和成功判定由原生调用方负责。

需要统一结果，同时观察上游未知字段或原始 SSE 时，在 config 设置：

```go
OnRawEvent: func(ctx context.Context, event openai.RawEvent) error {
    // event.Frame 包含原始 SSE 字节和换行，包括注释及 [DONE]。
    // event.Data 是按 SSE 规则合并的 data 内容，不保证是 JSON。
    // event.Name、event.ID 是该帧内的 event 和 id 字段。
    return record(event.Frame)
},
```

原始观察发生在该帧归一化之前，回调收到独立缓冲区，可以保留或修改它。
`Frame` 保留上游响应体里的 SSE 字节，不代表 TCP/HTTP 传输分块，也不包含 HTTP headers。
与统一事件同时消费时，调用方应明确哪个用于转发，哪个用于监控，避免重复发送。
非流式 `ModelResponse.Raw` 保留完整响应 JSON，包括未知字段。

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

`ModelResponse.CallInfo` 在成功或部分失败时提供尝试次数和最终 HTTP 状态、headers。
`protocol.Hooks.OnResponse` 可以读取这些信息。Hook 参数的消息、JSON 扩展、headers
等均为独立快照；`map[string]any` / `[]any` 等 JSON 形状会递归复制，应用自定义指针仍然共享。
Hook 错误可以阻止或终止调用，panic 原样传播。

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

注入 `HTTPClient` 时由它决定 transport 和超时，不能同时配置 `ResponseHeaderTimeout`。
后者不限制响应体读取，也不充当首个事件或流中空闲时限；这些需求由调用方的 context
或自定义 client/transport 实现。配置中的负数均为错误，不用负值表达“关闭”。

需要重试时显式配置 `Retry: openai.RetryPolicy{MaxRetries: 2}`。仅对暂时性网络错误和
HTTP 408、429、5xx 进行重试；永久错误及已取消的 context 不重试。HTTP `Retry-After`
支持秒数和日期，它是等待下限，可以超过 `MaxBackoff`，context 可提前终止等待。
收到成功 HTTP 响应后，body 读取、SSE 解析和回调失败都不会重放请求。
尝试次数统计应用层发送，不包含注入的 HTTP transport 内部可能发生的透明重试。

## 能力范围

`protocol.CapabilityProvider` 是可选接口。`Capabilities()` 表达 adapter 已实现的
编码能力，不保证具体模型支持全部能力。

当前 OpenAI adapter 支持文本、图片、文件、工具请求、推理输出和原生内容块。
Chat Completions 额外支持用户的内联 WAV/MP3 音频输入；Responses adapter 不支持
统一音频输入。工具结果在统一接口中目前只支持文本。Responses 的 reasoning summary
是输出摘要，不作为普通历史输入回放；完整原生 reasoning 项目通过 `DoNative` 处理。
需要上游音频输出、富内容工具结果或未归一化的服务能力时，可使用原生接口。
