# Agent Engine CLI

[English](./README.md) · [项目首页](../README_cn.md)

在终端里为每个项目配置模型和工具，并随时继续上次对话。CLI 支持 macOS 和 Linux 的 amd64、arm64 架构。

## 安装

```sh
curl -fsSL https://github.com/SmilingXinyi/agent-engine/releases/latest/download/install.sh | sh
```

安装脚本从最新稳定版下载对应架构的归档，验证 `SHA256SUMS` 后安装到 `~/.local/bin`。自定义路径时，把管道右侧改为 `sh -s -- --install-dir "$HOME/bin"`。也可以下载 `agent-engine-cli_<版本>_<系统>_<架构>.tar.gz`，校验后解压运行其中的 `agent-engine`。 [GitHub Releases](https://github.com/SmilingXinyi/agent-engine/releases)

稳定版需提供 `install.sh`。从源码使用当前分支时，在仓库根目录用 Go 1.25.0 或更新版本构建，再用 `/tmp/agent-engine` 执行下文的命令。

```sh
go build -o /tmp/agent-engine ./cli/cmd/agent-engine
```

## 开始对话

```sh
export PATH="$HOME/.local/bin:$PATH"
cd /path/to/project

agent-engine init
export AGENT_LLM_MODEL='your-model'
export AGENT_LLM_BASE_URL='https://your-provider.example/v1'
# 接口需要认证时设置 AGENT_LLM_API_KEY

agent-engine chat
```

`init` 在已有目录创建 YAML 模板并保留原有文件。想按项目生成配置，在 `chat` 前运行 `agent-engine configure`。它会用已配置的模型分析选定的项目资料，预览配置差异和待补充信息，经确认后写入。已有 MCP 服务器配置会保留。将 `.agents/state/` 加入项目的版本控制忽略规则。

## 命令与快捷键

| 命令 | 用途 |
| --- | --- |
| `agent-engine help chat` | 查看子命令参数 |
| `agent-engine chat [目录]` | 打开工作区并恢复上下文，等待新输入 |
| `agent-engine -c [目录]` | 回显历史消息和工具摘要，再继续对话。也可用 `chat -c` |
| `agent-engine chat --verbose [目录]` | 显示调用 ID、可用的退出码和工具结果预览 |

目录默认为当前目录。选项放在目录参数前。每轮会显示停止原因、模型与工具耗时、等待用户耗时及 token 用量。上下文大小是估算值，供应商报告的用量单独显示。模型回复以完整消息显示。

| 对话命令 | 用途 |
| --- | --- |
| `/help` | 查看对话命令和按键 |
| `/history` | 查看最近一次重置后的完整消息 JSON |
| `/reset` | 经确认后开启新上下文，保留审计历史 |
| `/exit` | 关闭会话，释放 MCP 连接和工作区锁 |

<details>
<summary>终端输入与快捷键</summary>

| 按键 | 用途 |
| --- | --- |
| Enter | 提交整段输入 |
| Ctrl+J / Ctrl+O / Alt+Enter | 插入换行 |
| ↑ / ↓ | 跨行移动，在输入边界召回历史 |
| Ctrl+R | 搜索历史。第一次 Enter 取回，第二次提交 |
| Ctrl+C | 清空草稿。输入为空时退出。运行中取消当前轮，再按一次请求退出 |
| Ctrl+D | 空行时结束输入，其他位置按编辑器规则删除字符 |

交互编辑支持中文和多行粘贴。Shift+Enter 取决于终端的按键映射。确认提示使用独立编辑器，回答不会进入输入历史。审批中的 Ctrl+C 中断当前执行。管道、文件重定向和 `TERM=dumb` 使用逐行输入。

</details>

## 项目配置

| 路径 | 内容 |
| --- | --- |
| `.agents/workspace.yaml` | 模型、API、重试、运行限额和压缩 |
| `.agents/prompt.md` | 项目特定的系统指令 |
| `.agents/tools.yaml` | 显式启用的内置工具及其限额 |
| `.agents/mcp.yaml` | 通过 `mcpServers` 配置的 MCP 服务器 |
| `.agents/skills/<name>/SKILL.md` | 本地 Skill |
| `.agents/.subagents/<role>/` | 子代理角色配置 |
| `.agents/state/` | 会话历史、可选的压缩检查点和工作区锁 |

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

- 字段可省略。`model` 依次回退到 `AGENT_MODEL` 和 `AGENT_LLM_MODEL`，`baseURL` 回退到 `AGENT_LLM_BASE_URL`。`apiType` 可设为默认的 `chat_completions` 或 `responses_api`。
- 凭据可通过 `apiKeyEnv` 指定环境变量名。省略该字段时依次读取 `AGENT_LLM_TOKEN` 和 `AGENT_LLM_API_KEY`。如使用 `.env`，请先在进程外加载。
- `modelMaxRetries` 是暂时性模型请求失败后的追加尝试次数，设为 `0` 关闭重试。推理强度和供应商原生参数分别用 `reasoningEffort` 与 `providerOptions`。
- `maxHistoryRounds` 默认限制发给模型的完整轮数。添加 `compression: {}` 可启用工具结果裁剪和历史摘要。 [压缩配置](../compression/README_cn.md)

系统上下文按顺序包含工作区说明、`prompt.md`、项目根目录的 `AGENTS.md` 和 Skill 元数据。启用子代理时再附加委派说明。修改后重新打开会话以加载配置。文件工具在项目根目录内操作，Shell 从项目根目录启动。Shell 默认单次 30 秒，有效时限受当前轮剩余时间约束。在 `builtinTools` 添加 `ask_user: {}` 可启用交互式提问。 [Workspace 配置](../workspace/README_cn.md#目录与配置) · [内置工具](../ai-tools/README_cn.md) · [MCP 配置](../mcp-runtime/README_cn.md)

**从 JSON 升级。** 将 `workspace.json`、`tools.json`、`mcp.json` 及子代理配置转换成对应的 `.yaml` 文件。旧的 `systemPrompt` 文字移入 `prompt.md`。已有 NDJSON 历史和压缩检查点可继续读取。

## 审批与历史恢复

| 审批模式 | 行为 |
| --- | --- |
| `--approval writes`（默认） | 文件写入、打补丁、Shell 和 MCP 调用逐次确认 |
| `--approval all` | 所有工具调用逐次确认 |
| `--approval auto` | 自动执行已启用的工具 |

Shell 审批会分别显示模型提供的目标和待执行的完整命令，目标也会出现在结果行。输入 `y` 或 `yes` 批准。拒绝会作为工具结果反馈给模型。Shell 和 MCP 以 CLI 进程的权限运行。 [工具的执行范围](../workspace/README_cn.md#确认与资源边界)

同一工作区一次只能由一个进程打开。意外退出后，重开会保留已记录的结果，并标记未执行或结果未知的调用。若结果未知，请先核实外部操作的状态再重试。`/reset` 保留审计记录，文件和外部操作不会回滚。 [NDJSON 与中断恢复](../workspace/README_cn.md#ndjson-与中断恢复)

## 开发与嵌入

```sh
go test ./cli/... ./workspace/...
go test -race ./cli/... ./workspace/... ./ai-tools/...
sh cli/scripts/install_test.sh
```

嵌入 CLI 时，调用 `cli.Run(ctx, args, cliprot.Options{Input: input, Output: output, Signals: signals})`，其中 `cliprot` 是 `cli/protocol`。输入和输出必填，信号通道由调用方注册。`chat` 和 `configure` 接管并关闭输入，输出始终由调用方管理。需要直接管理会话时使用 Workspace Go API。 [Workspace Go API](../workspace/README_cn.md#go-api-与验证) · [全工作区测试](../README_cn.md#开发与发布)

## 发布流程

`cli/v*` 标签触发 macOS 和 Linux 上的竞态与冒烟测试，随后构建四种架构归档，发布二进制、`SHA256SUMS` 和安装脚本。公开 SDK 使用 `ai-sdk/v*` 标签。已有 CLI 标签可通过 Actions 手动发布，工作流检出指定标签进行构建。带 `-rc.1` 等后缀的版本发布为预发行版。 [发布工作流](../.github/workflows/cli-release.yml)

从仓库根目录复用打包脚本。

```sh
bash cli/scripts/package-release.sh darwin arm64 v1.2.3 /tmp/cli-release
```

<details>
<summary>推送标签与手动发布</summary>

将示例版本替换为本次要发布的版本。

```sh
git tag -a cli/v1.2.3 -m 'Release CLI v1.2.3'
git push origin refs/tags/cli/v1.2.3

# 为已存在的标签重新触发发布
gh workflow run cli-release.yml --ref main -f tag=cli/v1.2.3
```

归档只包含二进制。发布失败时可在 Actions 中重跑工作流，同名附件会被替换。

</details>
