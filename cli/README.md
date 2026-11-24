# Agent Engine CLI

[简体中文](./README_cn.md) · [Project home](../README.md)

Configure a model and tools for each project, then pick up conversations from your terminal. The CLI supports macOS and Linux on amd64 and arm64.

## Install

```sh
curl -fsSL https://github.com/SmilingXinyi/agent-engine/releases/latest/download/install.sh | sh
```

The installer selects an archive from the latest stable release, verifies it against `SHA256SUMS`, and installs to `~/.local/bin`. For a custom destination, use `sh -s -- --install-dir "$HOME/bin"` after the pipe. For manual installation, download the matching `agent-engine-cli_<version>_<os>_<arch>.tar.gz`, verify the checksum, and extract the binary. [GitHub Releases](https://github.com/SmilingXinyi/agent-engine/releases)

The stable release must include `install.sh`. To run the current checkout, build from the repository root with Go 1.25.0 or later, then use `/tmp/agent-engine` in the examples below.

```sh
go build -o /tmp/agent-engine ./cli/cmd/agent-engine
```

## Start a conversation

```sh
export PATH="$HOME/.local/bin:$PATH"
cd /path/to/project

agent-engine init
export AGENT_LLM_MODEL='your-model'
export AGENT_LLM_BASE_URL='https://your-provider.example/v1'
# Set AGENT_LLM_API_KEY if your endpoint requires authentication.

agent-engine chat
```

`init` creates YAML templates in an existing directory and preserves existing files. For project-specific settings, run `agent-engine configure` before chatting. It analyzes selected project files with the configured model, previews proposed changes and missing inputs, and writes after approval. Existing MCP server settings are preserved. Add `.agents/state/` to your project's version-control ignore rules.

## Commands and shortcuts

| Command | Purpose |
| --- | --- |
| `agent-engine help chat` | See command options |
| `agent-engine chat [directory]` | Open the workspace, restore context, and wait for input |
| `agent-engine -c [directory]` | Display saved messages and tool summaries before chatting. Also available as `chat -c` |
| `agent-engine chat --verbose [directory]` | Show call IDs, available exit codes, and result previews |

Commands default to the current directory. Place options before the directory argument. Each turn shows why it ended and how much time went to the model, tools, and user input. The CLI displays an estimated context size and provider-reported token usage separately. Model replies appear as complete messages.

| In chat | Purpose |
| --- | --- |
| `/help` | Show commands and shortcuts |
| `/history` | Show full message JSON since the last reset |
| `/reset` | Confirm a fresh context and retain the audit log |
| `/exit` | Close the session and release MCP connections and the workspace lock |

<details>
<summary>Terminal editing</summary>

| Keys | Action |
| --- | --- |
| Enter | Submit the full input |
| Ctrl+J / Ctrl+O / Alt+Enter | Insert a newline |
| ↑ / ↓ | Move across lines or recall history at input boundaries |
| Ctrl+R | Search history. Press Enter once to recall and again to submit |
| Ctrl+C | Clear the draft or exit on empty input. During a turn, cancel it and press again to request exit |
| Ctrl+D | Finish input on an empty line or delete a character while editing |

The terminal editor supports Unicode and multiline paste. Shift+Enter depends on terminal key mapping. Approval prompts use a separate editor and keep answers out of recall history. Ctrl+C during approval interrupts the active execution. Pipes, file redirection, and `TERM=dumb` use line-by-line input.

</details>

## Project configuration

| Path | Purpose |
| --- | --- |
| `.agents/workspace.yaml` | Model, API, retries, run limits, and compression |
| `.agents/prompt.md` | Project-specific system instructions |
| `.agents/tools.yaml` | Enabled built-in tools and their limits |
| `.agents/mcp.yaml` | MCP server definitions under `mcpServers` |
| `.agents/skills/<name>/SKILL.md` | Local skill |
| `.agents/.subagents/<role>/` | Subagent role configuration |
| `.agents/state/` | Conversation journal, optional compression checkpoint, and workspace lock |

```yaml
# .agents/workspace.yaml
model: your-model
baseURL: https://your-provider.example/v1
apiType: chat_completions
modelMaxRetries: 2
maxSteps: 40
maxHistoryRounds: 10
timeoutSeconds: 900
```

- Fields are optional. `model` falls back to `AGENT_MODEL` then `AGENT_LLM_MODEL`, and `baseURL` falls back to `AGENT_LLM_BASE_URL`. `apiType` defaults to `chat_completions` and also accepts `responses_api`.
- Use `apiKeyEnv` to name the credential variable. When omitted, the CLI checks `AGENT_LLM_TOKEN` then `AGENT_LLM_API_KEY`. Load any `.env` file before starting the CLI.
- `modelMaxRetries` sets the number of additional attempts for transient model failures. Set it to `0` to disable retries. Use `reasoningEffort` for reasoning effort and `providerOptions` for provider-specific settings.
- `maxHistoryRounds` limits complete rounds sent to the model. Add `compression: {}` to trim long tool results and summarize older turns. [Compression configuration](../compression/README_cn.md)

The system context includes the workspace description, `prompt.md`, root `AGENTS.md`, and skill metadata in that order. Delegation instructions follow when subagents are enabled. Reopen the workspace to load changes. File tools are scoped to the project root. Shell starts in the project root. Shell defaults to 30 seconds, subject to the time remaining in the turn. Add `ask_user: {}` under `builtinTools` to enable interactive questions. [Workspace configuration](../workspace/README.md#project-configuration) · [Built-in tools](../ai-tools/README_cn.md) · [MCP configuration](../mcp-runtime/README_cn.md)

**Upgrading from JSON.** Convert `workspace.json`, `tools.json`, `mcp.json`, and subagent configuration files to `.yaml`. Move any legacy `systemPrompt` into `prompt.md`. Existing NDJSON history and compression checkpoints can still be loaded.

## Approvals and recovery

| Mode | Behavior |
| --- | --- |
| `--approval writes` (default) | Ask before writes, patches, Shell, and MCP calls |
| `--approval all` | Ask before every tool call |
| `--approval auto` | Execute enabled tools automatically |

Shell approval shows the model’s goal and the full command separately. The result line repeats the goal. Enter `y` or `yes` to approve. A denial is returned to the model as a tool result. Shell and MCP run with the CLI process's permissions. [Workspace tool boundaries](../workspace/README_cn.md#确认与资源边界)

One process at a time can open a workspace. Reopening after interruption retains recorded results and marks calls as unexecuted or of unknown outcome. Verify external effects before retrying a call with an unknown outcome. `/reset` retains the audit log, and files and external effects remain as they were before the reset. [NDJSON history and recovery](../workspace/README.md#durable-ndjson-history)

## Development and embedding

```sh
go test ./cli/... ./workspace/...
go test -race ./cli/... ./workspace/... ./ai-tools/...
sh cli/scripts/install_test.sh
```

For embedding, call `cli.Run(ctx, args, cliprot.Options{Input: input, Output: output, Signals: signals})` with `cliprot` imported from `cli/protocol`. Input and output are required. The caller registers the optional signal channel. `chat` and `configure` own and close input, while output stays caller-owned. Use the workspace Go API to manage sessions directly. [Workspace API](../workspace/README.md#go-api-and-tests) · [Workspace-wide tests](../README.md#development-and-releases)

## Publishing

A `cli/v*` tag triggers race and smoke tests on macOS and Linux, then publishes the four platform archives, `SHA256SUMS`, and the installer. Public SDK releases use `ai-sdk/v*` tags. Use the manual workflow to publish an existing CLI tag from that tag's source. Suffixes such as `-rc.1` produce prereleases. [Release workflow](../.github/workflows/cli-release.yml)

Run the packaging script from the repository root to build an archive locally.

```sh
bash cli/scripts/package-release.sh darwin arm64 v1.2.3 /tmp/cli-release
```

<details>
<summary>Push a tag or publish manually</summary>

Replace the example version with the version to release.

```sh
git tag -a cli/v1.2.3 -m 'Release CLI v1.2.3'
git push origin refs/tags/cli/v1.2.3

# Publish an existing tag again.
gh workflow run cli-release.yml --ref main -f tag=cli/v1.2.3
```

Archives contain only the binary. A failed workflow can be rerun from Actions. Same-name release assets are replaced.

</details>
