# Agent Engine CLI

A local, single-user CLI for macOS and Linux. Each project owns independent model settings, MCP servers, skills, and one durable conversation. This module composes the existing Agent and tool loop.

The `cli` module owns commands, terminal input/output and the workspace session lifecycle. The [workspace module](../workspace/README.md) remains terminal-independent. `cmd/agent-engine` only wires process signals, standard streams and exit codes.

## Download the CLI

Download a `cli/v0.1.0` archive from [GitHub Releases](https://github.com/SmilingXinyi/agent-engine/releases). Supported targets are `darwin_arm64` (Apple Silicon), `darwin_amd64` (Intel macOS), `linux_amd64` (x86-64) and `linux_arm64`. Archives are named `agent-engine-cli_v0.1.0_<os>_<arch>.tar.gz` and contain `agent-engine` plus English and Chinese READMEs. `SHA256SUMS` is provided with the release.

For example, on Apple Silicon:

~~~bash
tar -xzf agent-engine-cli_v0.1.0_darwin_arm64.tar.gz
./agent-engine init /path/to/project
export AGENT_LLM_MODEL='your-model'
export AGENT_LLM_BASE_URL='https://your-provider.example/v1'
export AGENT_LLM_API_KEY='your-key'
./agent-engine chat /path/to/project
~~~

No Go installation is required. The executable is the standalone Workspace Agent CLI with project configuration, tools and persistent conversation support; configure your own model service, credentials, and external MCP server executables/runtimes.

## Run from source

Run from the repository root; the project directory must already exist:

~~~bash
go run ./cli/cmd/agent-engine init /path/to/project
export AGENT_LLM_MODEL='your-model'
export AGENT_LLM_BASE_URL='https://your-provider.example/v1'
export AGENT_LLM_API_KEY='your-key'
go run ./cli/cmd/agent-engine chat /path/to/project

# Flags precede the directory. Explicitly opt into unattended execution:
go run ./cli/cmd/agent-engine chat --approval auto /path/to/project
go build -o /tmp/agent-engine ./cli/cmd/agent-engine
~~~

The default directory is the current directory; no parent search occurs. Initialization preserves existing files and runs no model, tools, or MCP servers. Add `.agents/state/` to your project's version-control ignore rules.

Commands: `/help`, `/history` (current messages as JSON), `/reset` (confirmed context reset; audit events retained), `/exit`. Reopening restores the context but waits for a new prompt. Ctrl+C cancels an active execution; another interrupt requests exit. In plain input mode, an idle interrupt or EOF exits and releases resources. Responses are complete messages, not streaming output.

## Interactive editing

Current source builds automatically use [Bubbline](https://github.com/knz/bubbline) when both stdin and stdout are terminals and `TERM` is not `dumb`. Editing stays inline rather than taking over the screen.

| Keys | Behavior |
| --- | --- |
| Enter | Submit the whole input |
| Ctrl+J / Ctrl+O / Alt+Enter | Insert a newline |
| Up / Down | Move across lines; recall history at input boundaries |
| Ctrl+R | Search input history; Enter recalls the match, another Enter submits |
| Ctrl+C | Clear a draft; exit when input is empty |
| Ctrl+D | Finish input on an empty line; otherwise use the editor's deletion behavior |

Unicode and bracketed multiline paste are supported; pasted newlines do not submit the prompt. Shift+Enter depends on terminal key encoding. This integration does not enable an extended keyboard protocol; use a supported newline key or configure the terminal to send Ctrl+J (LF) for Shift+Enter.

Recall history comes only from user messages in the current workspace's NDJSON, with no extra history file. It survives reopening and clears after `/reset`. Approval/reset prompts have independent editors without conversation history, and their answers are not added to recall history. Ctrl+C during tool approval interrupts that execution immediately.

Pipes, redirected files or `TERM=dumb` retain plain line-by-line input with no editor escape sequences. A single owner reads stdin in each phase; editing or cancellation restores terminal state before returning to execution.

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

Default `--approval writes` automatically permits only local `read_file`, `list_directory` and `get_skill`. Writes, patches, Shell and every MCP tool require `y`/`yes`. `all` confirms every call; `auto` permits enabled calls. Denial becomes a tool error for the model. Confirmation EOF or read failure never approves execution.

File tools enforce their existing root boundary. Shell and MCP retain the process's permissions and environment; a working directory is not a sandbox. Workspace separation isolates configuration and context, not the operating system.

## Durable NDJSON history

`.agents/state/history.ndjson` is the sole source of history. Every newline-terminated JSON event has `version`, increasing `sequence`, `time`, `type`, optional `run_id` and `data`. Types are `run_started`, `user_message`, `model_message`, `tool_started`, `tool_result`, `run_ended` and `context_reset`. SDK messages, raw tool arguments and large JSON extension numbers are preserved.

User input is synced before model calls; model responses before dispatch; tool starts after approval and before execution; results immediately after return. Each event appends only new data. Write or sync failure prevents further calls until reopening. Model configuration, authentication headers and credentials are not serialized; user/model text and tool output are stored as supplied.

Replay truncates an uncommitted final fragment without a newline and reports the repair. Corrupt interior records, unknown versions or invalid ordering fail without overwriting history. No compaction, rotation, or size limit is implemented.

Interrupted batches are completed with error results: calls without an execution-start record are marked unexecuted; started calls without a recorded result are marked unknown and require verification before retrying. Completed results are retained. Recovery never replays tools or calls the model. Cancellation, model limits and execution errors also leave a valid paired history. External effects and history are not one transaction; there is no exactly-once guarantee or rollback on reset.

One OS file lock permits one process per workspace. Different projects can run separately; locks release on process exit, and the lock file remains. State files/directories reject symlinks. Do not edit or replace active state files; this does not defend against an external writer with the same local permissions. New directories use `0700`, files `0600`.

## Workspace API and tests

The root package exposes `Init`, `Open`, `Execute`, `History`, `Reset` and `Close`. Import public configuration and event types from `workspace/protocol`. `Execute` returns the existing loop result. `OpenOptions` accepts a Provider, approval policy/callback, loop hooks and warning callback, with no terminal dependency. An injected Provider remains caller-owned; missing approval callbacks deny protected calls.

History returns owned snapshots; while executing, a tool batch may still be pending. Concurrent Execute/Reset calls fail as busy. Idempotent, concurrency-safe Close cancels and waits for execution, then releases MCP, history and the lock. Do not synchronously call Close from execution hooks, since it waits for that execution.

~~~bash
go test ./cli/... ./workspace/...
go test -race ./cli/... ./workspace/... ./ai-tools/...
go test ./agent/... ./agent-skill/... ./ai-sdk/... ./ai-tool-loop/... ./ai-tools/... ./mcp-runtime/... ./workspace/... ./cli/...
~~~

Tests use fake Providers, temporary directories, controlled stdio MCP processes and process crashes, with no live model or external MCP server. Shell tests explicitly skip when `sh` is missing; symlink tests skip if unsupported. See [中文说明](./README_cn.md) for detailed configuration and examples.

## Publishing

Only `ai-sdk` and `cli` receive new semantic version tags. `ai-sdk/v*` identifies the independently consumed SDK; only `cli/v*` publishes GitHub Releases and binary assets. Other modules remain internal workspace dependencies, built from the same repository checkout. Their existing local replacements are retained; installing the CLI with `go install ...@version` is not supported. Build from this checkout or use a release archive.

Pushing a `cli/v*` tag triggers `.github/workflows/cli-release.yml`. The workflow runs all workspace race tests and native CLI smoke checks on Linux and macOS, cross-compiles four archives, then uploads them and `SHA256SUMS` to a draft GitHub Release before publishing it. Other module tags do not trigger this workflow.

~~~bash
git tag -a cli/v0.1.0 -m 'Release CLI v0.1.0'
git push origin refs/tags/cli/v0.1.0

# Reuse the same packaging step locally from the repository root:
bash cli/scripts/package-release.sh darwin arm64 v0.1.0 /tmp/cli-release
~~~

CGO is disabled; project configurations, credentials and conversation histories are never packaged. Only the release job has `contents: write`. Failed runs can be rerun; same-name assets are replaced. Version suffixes such as `cli/v0.2.0-rc.1` produce prereleases.

To publish an existing tag manually, use Run workflow in Actions or `gh workflow run cli-release.yml --ref main -f tag=cli/v0.1.0`. Manual runs also check out and test the specified tag, without moving it or packaging main's application code.

## Embedding the command runner

Call `cli.Run(ctx, args, cliprot.Options{Input: input, Output: output, Signals: signals})`, importing `cli/protocol` as `cliprot`. Input and Output are required; Signals is optional and caller-registered. Run returns errors without exiting the process. Chat closes Input and its workspace when it returns; init and argument validation leave Input open. Output always remains caller-owned.

The source entry moved from `workspace/cmd/agent-engine` to `cli/cmd/agent-engine`. New releases use `cli/v*` tags and `agent-engine-cli_*` archives. Legacy `workspace/v*`, `agent/v*` and `ai-tool-loop/v*` tags and Releases have been removed.
