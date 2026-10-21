# Changelog

## Unreleased

- Rename `ChatStream` to `StreamChat`.

## v0.5.0 — 2026-10-19 — stream stall timeouts

- Add `FirstFrameTimeout` and `IdleTimeout` on `openai.Config`, `protocol.ModelRequest`,
  and `openai.ChatRequest`. Zero disables a timer. A nil request pointer inherits the
  provider value, and zero in that pointer disables it for one call.
- `Stream` and `ChatStream` wait for the first SSE frame with non-empty data, then
  for each later frame. Comments and blank data do not end or reset the first wait.
  A stall returns `ErrorTimeout` with `TimeoutKind` `first_frame` or `idle`.
  Only the first-frame timeout is retryable. `Generate` and `Chat` ignore both timers.
  The Go module release tag is `ai-sdk/v0.5.0`.

## v0.4.0 — 2026-10-19 — chat completion accumulator

- Add `openai.Accumulator`. `Add` folds Chat Completions SSE `data` payloads into
  one `chat.completion` object and keeps the full `usage` object.
  `Stream` and `ChatStream` still return the original frames and do not call it.
  The Go module release tag is `ai-sdk/v0.4.0`.

## v0.3.6 — 2026-10-16 — native chat streams

- Add `ChatStream`. It takes `ChatRequest` with `[]map[string]any` messages and
  returns the original SSE frames. `protocol.ModelRequest.Messages` stays `[]Message`.
  The Go module release tag is `ai-sdk/v0.3.6`.

## v0.3.5 — 2026-10-16 — native chat completions

- Add `ChatRequest` and `Provider.Chat`. `Messages` is `[]map[string]any`.
  The non-streaming result is a `chat.completion` object and keeps unknown fields.
- Remove `DoNative` and `NativeResponse`. Raw SSE remains on `Stream`.
  The Go module release tag is `ai-sdk/v0.3.5`.

## v0.3.0 — 2026-10-16 — pull streams preserving original SSE frames

Streaming changes are limited to ai-sdk. Workspace lifecycle callers now import
hooks and request metadata from protocol. The Go module release tag is `ai-sdk/v0.3.0`.

### Breaking streaming contract

- Replace `Client.Stream` and `StreamingProvider.Stream` with
  `Stream(ctx, ModelRequest) (protocol.Stream, error)`. Consume with
  `Next`, `Chunk`, `Err`, `Close`, and `Info`; always close the reader.
- Add `StreamChunk` with exact `Frame` bytes and parsed SSE `Event`, `ID`, `Data`.
  One upstream frame produces one chunk, including comments, empty frames,
  unknown payloads and terminal frames. Buffers remain retainable after Next.
- Remove `EventSink`, `EventSinkFunc`, `StreamEvent`, `StreamEventType`, event
  constants, `ToolCallDelta`, `Config.OnRawEvent` and `openai.RawEvent`. No aliases,
  replacement callback API or StreamNative entry point is added.
- Remove forced text/reasoning/tool/usage accumulation and semantic splitting.
  Streaming does not return ModelResponse, reconcile final content, or validate
  payload schemas. No optional normalizer or collector is added in this release.
- Add `StreamInfo` with independent CallInfo and Complete snapshots. Response
  hooks receive `ResponseInfo.Stream` instead of accumulated Response; hooks run
  once at exhaustion/failure/close and measure the full consumption lifetime.
  Opening errors also run response hooks; hook errors join terminal errors.

### SSE and lifetime

- Read synchronously without a producer goroutine or channel. Support LF, CRLF,
  CR, an initial BOM and an unterminated frame at clean EOF. HTTP read boundaries
  cannot change frame boundaries. Preserve raw bytes without re-encoding.
- Deliver protocol terminal and known failure frames before stopping. Preserve
  Chat Completions RequireDone/finish-plus-clean-EOF behavior and Responses'
  terminal-event requirement. Do not generate a synthetic finish frame.
- Close bodies on normal termination, read failure and explicit close. Close is
  idempotent and may interrupt a blocked Next; early close reports cancellation.
  Close/hook errors do not undo already observed normal termination.
- Retain request encodings, Generate and DoNative, structured transport errors,
  timeout/retry/limit defaults, and the no-replay rule after accepting a success
  response. No other providers, SSE reconnect, or application migration is added.
- Add opt-in `integration` tests using real loopback HTTP servers for both APIs,
  frame preservation, cancellation propagation and broken connections without replay.

## v0.2.1 — 2026-10-15 — message extension preservation

- Add `Message.ProviderOptions` for explicit native message-level input fields in
  normalized Generate and Stream calls. Chat Completions supports all message
  roles; Responses merges fields into the corresponding message or single tool
  result item without leaking them into function calls or other messages.
- Reject reserved-field collisions with `ErrorInvalidRequest`. Return
  `ErrorUnsupported` when there is no unique native target, including Responses
  instructions, contentless/function-call-only messages and grouped tool results.
- Add `Message.Clone` and copy nested message options in request, response and
  lifecycle snapshots. Keep native response fields in Raw/raw stream events;
  do not automatically replay response fields as request extensions.
- Publish only Go submodule tags. The unprefixed `v0.2.0` tag has been removed;
  `ai-sdk/v0.2.0` is unchanged, and this release uses `ai-sdk/v0.2.1`.

## v0.2.0 — 2026-10-15 — breaking redesign

Only `ai-sdk` is refactored. Other workspace modules and external callers are not
migrated in this change. The Go module release tag is `ai-sdk/v0.2.0`.

### Public contract

- Move lifecycle hook types and `RequestIDMetadataKey` to `protocol`; use
  `ai.NewClient(provider, protocol.Hooks{})`.
- Replace `ErrStreamingNotSupported` with a structured `protocol.Error` carrying
  `ErrorUnsupported`; preserve underlying errors through `Unwrap`.
- Add explicit reasoning and structured-output options, developer messages,
  opaque native content blocks, optional adapter capability descriptions,
  response completion state, call metadata and original response JSON.
- Isolate JSON-shaped request/response snapshots observed by lifecycle hooks.

### OpenAI provider

- Retain only `APITypeChatCompletions` and `APITypeResponsesAPI`; remove aliases.
- Replace `MaxRetries` and `RetryBackoff` with opt-in `RetryPolicy` under `Retry`.
  Zero retries means one application attempt. Negative configuration is invalid.
- Remove the default two-minute client timeout. Header timeout is configurable;
  caller context controls total lifetime. Respect an explicitly injected client.
- Preserve final HTTP failure status, headers and diagnostic body. Observe attempts
  and honor both forms of `Retry-After` without retrying consumed success streams.
- Separate non-streaming response, cumulative stream and complete SSE event limits.
- Parse complete multiline SSE events, preserve exact LF/CRLF frames through
  `OnRawEvent`, and expose exact JSON requests and native response bodies through
  `DoNative`, including final non-2xx responses.
- Emit one final normalized event after successful data/usage delivery. Accept
  clean EOF after Chat Completions finish reason unless `RequireDone` is enabled.
  Responses still requires its own terminal event.
- Preserve partial results with `Complete=false` on stream failure. Reconcile final
  Responses content, keep tool call IDs/indexes stable and report inconsistent data.
- Treat `ProviderOptions` as native top-level fields: remove thinking/reasoning alias
  guesses, reject common-field collisions and reject the SDK-only `extra_body` wrapper.

### Release policy

This repository uses Go submodule tags: `ai-sdk/vX.Y.Z`. During 0.x, breaking public
contract or behavior changes increment the minor version; compatible fixes increment
patch. Stable 1.x and later breaking changes require a major version and the matching
Go module path suffix. Consumers should pin a released version; release notes must
identify changed defaults, removed symbols and public contract changes.
