# Workspace

Terminal-independent project runtime for a single local user on macOS and Linux. Workspace assembles Agent, built-in tools, skills and MCP, and owns durable conversation history. It does not parse commands or read terminal input.

The [CLI module](../cli/README.md) owns command dispatch, inline editing, approval prompts and output. Its session opens a workspace, calls Execute/History/Reset, and closes it on exit.

## Project configuration

~~~text
.agents/
  workspace.json
  tools.json
  mcp.json
  skills/<name>/SKILL.md
  state/
    history.ndjson
    workspace.lock
~~~

All fields in `workspace.json` are optional:

~~~json
{
  "model": "your-model",
  "baseURL": "https://your-provider.example/v1",
  "apiType": "chat_completions",
  "apiKeyEnv": "PROJECT_API_KEY",
  "systemPrompt": "Follow project conventions.",
  "maxSteps": 40,
  "maxHistoryRounds": 10,
  "timeoutSeconds": 900
}
~~~

Model fallback is `AGENT_MODEL`, then `AGENT_LLM_MODEL`; base URL falls back to `AGENT_LLM_BASE_URL`. API type is `chat_completions` by default, or `responses_api`. `apiKeyEnv` names an environment variable; absent that setting, read `AGENT_LLM_TOKEN` then `AGENT_LLM_API_KEY`. Authentication is optional. No `.env` file is automatically loaded. Zero limits use the defaults shown above; negative limits are invalid.

System context includes the workspace description, configured prompt, root `AGENTS.md` and skill metadata. Skill bodies are fetched through `get_skill`. Nothing is inherited from other projects. Configuration is a fixed snapshot until reopening. Missing MCP/skills are empty; malformed files fail startup. Workspace and tools configuration files are required.

`tools.json` uses the [existing built-in configuration](../ai-tools/README_cn.md). Only listed, enabled tools are available. Initialization explicitly lists file tools and Shell. File root and initial Shell directory must match the project root; empty settings or `"."` bind to that root. Disabled tools do not validate dependencies.

`mcp.json` uses the [existing MCP configuration](../mcp-runtime/README_cn.md), independently of tools configuration. Enabled stdio servers default to the project directory. Relative `dir` values and relative commands containing a path separator resolve against the project root; bare commands use PATH. Arguments are interpreted by the subprocess in its working directory. Startup failure closes resources already created.

The default `ApprovalWrites` policy automatically permits only local `read_file`, `list_directory` and `get_skill`. Writes, patches, Shell and every MCP tool require the caller’s approval callback. `ApprovalAll` confirms every call; `ApprovalAuto` permits enabled calls. Denial becomes a tool error for the model. A missing confirmation callback denies protected calls.

File tools enforce their existing root boundary. Shell and MCP retain the process's permissions and environment; a working directory is not a sandbox. Workspace separation isolates configuration and context, not the operating system.

## Durable NDJSON history

`.agents/state/history.ndjson` is the sole source of history. Every newline-terminated JSON event has `version`, increasing `sequence`, `time`, `type`, optional `run_id` and `data`. Types are `run_started`, `user_message`, `model_message`, `tool_started`, `tool_result`, `run_ended` and `context_reset`. SDK messages, raw tool arguments and large JSON extension numbers are preserved.

User input is synced before model calls; model responses before dispatch; tool starts after approval and before execution; results immediately after return. Each event appends only new data. Write or sync failure prevents further calls until reopening. Model configuration, authentication headers and credentials are not serialized; user/model text and tool output are stored as supplied.

Replay truncates an uncommitted final fragment without a newline and reports the repair. Corrupt interior records, unknown versions or invalid ordering fail without overwriting history. No compaction, rotation, or size limit is implemented.

Interrupted batches are completed with error results: calls without an execution-start record are marked unexecuted; started calls without a recorded result are marked unknown and require verification before retrying. Completed results are retained. Recovery never replays tools or calls the model. Cancellation, model limits and execution errors also leave a valid paired history. External effects and history are not one transaction; there is no exactly-once guarantee or rollback on reset.

One OS file lock permits one process per workspace. Different projects can run separately; locks release on process exit, and the lock file remains. State files/directories reject symlinks. Do not edit or replace active state files; this does not defend against an external writer with the same local permissions. New directories use `0700`, files `0600`.

## Go API and tests

The root package exposes `Init`, `Open`, `Execute`, `History`, `Reset` and `Close`. Import public configuration and event types from `workspace/protocol`. `Execute` returns the existing loop result. `OpenOptions` accepts a Provider, approval policy/callback, loop hooks and warning callback, with no terminal dependency. An injected Provider remains caller-owned; missing approval callbacks deny protected calls.

History returns owned snapshots; while executing, a tool batch may still be pending. Concurrent Execute/Reset calls fail as busy. Idempotent, concurrency-safe Close cancels and waits for execution, then releases MCP, history and the lock. Do not synchronously call Close from execution hooks, since it waits for that execution.

~~~bash
go test ./workspace/...
go test -race ./workspace/... ./ai-tools/...
go test ./agent/... ./agent-skill/... ./ai-sdk/... ./ai-tool-loop/... ./ai-tools/... ./mcp-runtime/... ./workspace/...
~~~

Tests use fake Providers, temporary directories, controlled stdio MCP processes and process crashes, with no live model or external MCP server. Shell tests explicitly skip when `sh` is missing; symlink tests skip if unsupported. See [中文说明](./README_cn.md) for detailed configuration and examples.
