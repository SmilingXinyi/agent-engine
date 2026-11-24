# Workspace

Terminal-independent project runtime for a single local user on macOS and Linux. Workspace assembles Agent, built-in tools, skills and MCP, and owns durable conversation history. It does not parse commands or read terminal input.

The [CLI module](../cli/README.md) owns command dispatch, inline editing, approval prompts and output. Its session opens a workspace, calls Execute/History/Reset, and closes it on exit.

## Project configuration

For existing projects, convert `workspace.json`, `tools.json` and `mcp.json` to the corresponding `.yaml` files. Apply the same change to subagent configuration files. Keep state files in their existing formats.

~~~text
.agents/
  workspace.yaml
  prompt.md
  tools.yaml
  mcp.yaml
  skills/<name>/SKILL.md
  state/
    history.ndjson
    workspace.lock
~~~

All fields in `workspace.yaml` are optional.

~~~yaml
model: your-model
baseURL: https://your-provider.example/v1
apiType: chat_completions
apiKeyEnv: PROJECT_API_KEY
modelMaxRetries: 2
maxSteps: 40
maxHistoryRounds: 10
timeoutSeconds: 900
~~~

Model fallback is `AGENT_MODEL`, then `AGENT_LLM_MODEL`; base URL falls back to `AGENT_LLM_BASE_URL`. API type is `chat_completions` by default, or `responses_api`. `apiKeyEnv` names an environment variable; absent that setting, read `AGENT_LLM_TOKEN` then `AGENT_LLM_API_KEY`. Authentication is optional. No `.env` file is automatically loaded. Zero limits use the defaults shown above; negative limits are invalid.

Set `modelMaxRetries` to the number of additional attempts for a transient model request failure. The default is two, and zero disables retries. Set `reasoningEffort` for providers that accept reasoning effort or use `providerOptions` for provider-specific fields.

By default, `maxHistoryRounds` limits the number of complete rounds sent to the model. Add `compression: {}` to opt into trimming long tool results and summarizing older rounds when the limit is exceeded. Set `compression.contextWindow` to also trigger summaries by token occupancy. [Compression configuration](../compression/README_cn.md)

System context includes, in order, the workspace description, the verbatim contents of `.agents/prompt.md`, root `AGENTS.md` and skill metadata. Skill bodies are fetched through `get_skill`. Nothing is inherited from other projects. Configuration, prompt, project instructions and skills are fixed snapshots until reopening. Missing MCP/skills are empty; malformed files fail startup. Workspace and tools configuration files are required.

`prompt.md` is optional: missing or empty files add no custom prompt; other read errors fail startup. Initialization creates an empty file without overwriting existing contents. Move any legacy `systemPrompt` value into `.agents/prompt.md` during migration.

`tools.yaml` uses the field names from the [built-in configuration](../ai-tools/README_cn.md). Only listed, enabled tools are available. Initialization explicitly lists file tools and Shell. File root and initial Shell directory must match the project root; empty settings or `"."` bind to that root. Disabled tools do not validate dependencies.

Set `shell.timeoutSeconds` in `tools.yaml` to control individual Shell calls. The default is 30 seconds, subject to the remaining turn budget. Add `ask_user: {}` under `builtinTools` to let the model ask a question in an interactive CLI terminal.

`mcp.yaml` uses the `mcpServers` mapping and field names from the [MCP configuration](../mcp-runtime/README_cn.md), independently of tools configuration. Enabled stdio servers default to the project directory. Relative `dir` values and relative commands containing a path separator resolve against the project root; bare commands use PATH. Arguments are interpreted by the subprocess in its working directory. Startup failure closes resources already created.

The default `ApprovalWrites` policy automatically permits only local `read_file`, `list_directory` and `get_skill`. Writes, patches, Shell and every MCP tool require the caller’s approval callback. `ApprovalAll` confirms every call; `ApprovalAuto` permits enabled calls. Denial becomes a tool error for the model. A missing confirmation callback denies protected calls.

File tools enforce their existing root boundary. Shell and MCP retain the process's permissions and environment; a working directory is not a sandbox. Workspace separation isolates configuration and context, not the operating system.

## Subagents

Each immediate directory under `.agents/.subagents/` exposes one `subagent_<directory>` tool after reopening the workspace. Role names accept up to 55 lowercase ASCII letters, digits, underscores, and hyphens. For example, create `.agents/.subagents/repo_research/` with these files:

| File | Example |
| --- | --- |
| `workspace.yaml` | `{"description":"Research repository behavior and cite file paths and lines"}` |
| `prompt.md` | `Find the entry point, trace its callers, and cite the evidence.` |
| `tools.yaml` | `{"builtinTools":{"read_file":{},"list_directory":{}}}` |

The optional `model` field defaults to the workspace model. The subagent uses its own prompt plus the root `AGENTS.md`. Select at least one enabled local read-only tool from the parent workspace. Each delegation starts with fresh history and runs synchronously. A turn can delegate twice across all roles, with up to six model calls and 60 seconds per delegation. Full approval mode prompts for delegation and child tool calls. The parent history keeps the delegation request, bounded result, and child run ID and usage metadata.

## Durable NDJSON history

`.agents/state/history.ndjson` is the source of the full execution history. Every newline-terminated JSON event has `version`, increasing `sequence`, `time`, `type`, optional `run_id` and `data`. Types are `run_started`, `user_message`, `model_message`, `tool_started`, `tool_result`, `run_ended` and `context_reset`. SDK messages, raw tool arguments and large JSON extension numbers are preserved.

User input is synced before model calls; model responses before dispatch; tool starts after approval and before execution; results immediately after return. Each event appends only new data. Write or sync failure prevents further calls until reopening. Model configuration, authentication headers and credentials are not serialized; user/model text and tool output are stored as supplied.

Replay truncates an uncommitted final fragment without a newline and reports the repair. Corrupt interior records, unknown versions or invalid ordering fail without overwriting history. When compression is enabled, `.agents/state/context-checkpoint.json` saves the summary, recent context and journal position after a completed run. Later requests, including after reopening, use the checkpoint and newer messages. The journal remains available for `/history` and recovery. If the checkpoint is invalid, restore it or use `/reset` to start a fresh conversation.

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
