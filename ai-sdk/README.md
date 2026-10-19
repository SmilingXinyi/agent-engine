# AI SDK

A standalone Go module for one model call. `ModelRequest` provides a common input
contract; `Generate` returns a normalized result and `Stream` delivers original
SSE frames. The OpenAI-compatible provider supports Chat Completions and Responses.
The SDK does not execute tools or manage conversations.

Install v0.4.0 with:

```sh
go get github.com/SmilingXinyi/agent-engine/ai-sdk@v0.4.0
```

v0.4.0 adds `openai.Accumulator` for folding Chat Completions SSE data into one
`chat.completion` object. `ChatStream` still returns the original frames, and
`Chat` remains the non-streaming object call. Go submodule release tags use
`ai-sdk/vX.Y.Z`. See [CHANGELOG](CHANGELOG.md) for the migration notes. Local
development can use a Go workspace or a `replace` directive.

Requires Go 1.25+, with only standard-library dependencies. Verify independently:

```sh
cd ai-sdk
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Run real loopback HTTP integration tests with the explicit `integration` tag:

```sh
GOWORK=off go test -race -tags=integration -count=1 ./...
```

These tests require permission to bind local sockets. They cover both APIs,
exact frames over chunked HTTP, cancellation reaching the server, and broken
connections without replay. They do not call external model providers.

## Pull streaming

Build the provider and client as described in the [Chinese guide](README_cn.md).
Both `Client.Stream` and `provider.Stream` accept `protocol.ModelRequest` and return
`protocol.Stream`. Opening validates HTTP status without consuming a successful
body. In an error-returning function with an `io.Writer` named `writer`:

```go
stream, err := client.Stream(ctx, request)
if err != nil {
    return err
}
defer stream.Close()

for stream.Next() {
    chunk := stream.Chunk()
    if _, err := writer.Write(chunk.Frame); err != nil {
        return errors.Join(err, stream.Close())
    }
}
return stream.Err()
```

Import `errors` for the example. There is no sink, producer goroutine, channel,
or semantic event splitting. The reader does not accumulate a response.
`openai.Accumulator` is optional and only folds Chat Completions `data` payloads.
`Generate` remains available for the normalized result.

- Each successful `Next()` exposes one original SSE frame via `Chunk()`.
  `Frame` preserves exact bytes, including delimiters, comments, empty frames,
  unknown fields and terminal frames. `Event` and `ID` describe this frame only.
  `Data` joins SSE data lines with LF; it can be non-JSON. Payload schemas are not
  validated. Frames are never merged, split, re-encoded, or replaced with a
  synthetic finish chunk.
- LF, CRLF, CR, initial UTF-8 BOM and a final unterminated frame at clean EOF are
  supported. HTTP read boundaries do not determine SSE boundaries. Retained chunk
  buffers are not overwritten by later reads; `Info()` returns independent headers.
- Chat Completions delivers `[DONE]` before stopping. By default, an explicit
  finish reason followed by clean EOF also succeeds; `RequireDone` can require
  the sentinel. A finish reason alone does not stop reading subsequent usage.
- Responses delivers its completed/incomplete terminal frame before stopping.
  A bare `[DONE]` is visible but reports `ErrorStreamIncomplete`. Known upstream
  failure frames are visible before `Err()` reports `ErrorUpstream`. Unknown or
  non-JSON data remains opaque. Bytes after the protocol terminal are not read
  as further events.
- `Info()` contains `CallInfo` and `Complete`, never accumulated model content.
  Truncation, read failures, limits, timeout and cancellation do not complete a
  stream. Close or hook failures do not undo observed normal termination; always
  check `Err()`.
- Always close, even on early exit. Close is idempotent and releases the body;
  early close records cancellation in `Err()`. Its return value reports close
  and response-hook failures. `Next`/`Chunk` are serial; concurrent `Close` and
  context cancellation can interrupt a blocked read. No automatic reconnect or
  replay occurs after a successful HTTP response has been accepted.

## Lifecycle and migration

`OnRequest` runs before sending. `OnResponse` runs once on exhaustion, failure or
close, including opening failures; its duration covers consumption. Request-hook
rejections and unsupported streaming skip response hooks. Streaming observations
use `ResponseInfo.Stream`; `ResponseInfo.Response` is unused. Generate observations
continue using `ResponseInfo.Response` with a nil Stream. Snapshots isolate request
extensions and headers; hook errors join the call error and panics propagate.

Replace the old three-argument Stream call with the reader loop above. `EventSink`,
`EventSinkFunc`, `StreamEvent`, `StreamEventType`, `ToolCallDelta`, their event
constants, `Config.OnRawEvent` and `openai.RawEvent` are removed without aliases.
There is no `StreamNative` entry point. Request encoding, provider options,
transport timeouts, explicit retries and byte-limit defaults remain unchanged.

The [Chinese guide](README_cn.md) includes Generate examples, message extensions,
provider configuration, native HTTP access and the full timeout/limit table.
