# Changelog

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
