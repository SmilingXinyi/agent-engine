# Changelog

## v0.1.0 — 2026-11-04 — first module release

- Assemble system, history, and user messages, validate complete historical tool
  batches, and delegate execution to `ai-tool-loop`.
- Forward explicit confirmation callbacks and denial policies from
  `Request.Options`. Agent no longer reads `AGENT_TOOL_CONFIRMATION`.
- Depend on `ai-sdk v0.7.0` and `ai-tool-loop v0.1.0`.
  The Go module release tag is `agent/v0.1.0`.

### Workspace migration

Applications must resolve environment variables and configuration before calling
Agent. The Xunlei entrypoint retains `off` / `continue` / `stop` environment modes
and rejects invalid values before connecting MCP. Other applications should pass
`ConfirmTool` and `ToolDenialPolicy` explicitly; process environment no longer
overrides request options.
