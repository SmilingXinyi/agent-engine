# AI SDK

A standalone Go module for one model call. It provides a common model contract,
optional streaming, lifecycle observations, and an OpenAI-compatible adapter for
Chat Completions and Responses. It does not execute tools or manage conversations.

See [中文使用说明](README_cn.md) for runnable examples, native HTTP/SSE access,
errors, completion semantics, timeouts, retry policy and provider extensions.
See [CHANGELOG](CHANGELOG.md) for release notes, including the v0.2.0 breaking redesign and v0.2.1 message extensions.

Requires Go 1.25+. The module has no third-party dependencies and can be tested
independently of the repository workspace:

```sh
cd ai-sdk
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Install v0.2.1 with:

```sh
go get github.com/SmilingXinyi/agent-engine/ai-sdk@v0.2.1
```

Go module release tags use the submodule prefix `ai-sdk/vX.Y.Z`. During 0.x,
breaking changes increment the minor version.
