# Workspace

面向 macOS、Linux 单机单用户的项目运行环境。负责组装 Agent、内置工具、Skill、MCP，以及持久化对话上下文；不解析命令、不读取终端输入。

[CLI 模块](../cli/README_cn.md) 负责命令入口、多行编辑、审批提示与输出展示。CLI 会话创建 workspace，通过 Execute/History/Reset 调用能力，退出时 Close 释放资源。

## 目录与配置

现有项目需将 `workspace.json`、`tools.json`、`mcp.json` 转换为对应的 `.yaml` 文件。子代理配置也使用 `.yaml`。已有会话状态可以继续读取。

~~~text
project/
  AGENTS.md
  .agents/
    workspace.yaml
    prompt.md
    tools.yaml
    mcp.yaml
    skills/<skill-name>/SKILL.md
    state/
      history.ndjson
      workspace.lock
~~~

`workspace.yaml` 的字段全部可选。

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

- 未指定 `model` 时按 `AGENT_MODEL`、`AGENT_LLM_MODEL` 顺序取值；`baseURL` 回退至 `AGENT_LLM_BASE_URL`。
- `apiType` 支持 `chat_completions`（默认）和 `responses_api`。
- `apiKeyEnv` 是环境变量名称，不是密钥。未指定时依次读取 `AGENT_LLM_TOKEN`、`AGENT_LLM_API_KEY`，允许无密钥。指定名称后不回退其他密钥。
- `modelMaxRetries` 控制暂时性模型请求失败后的追加尝试次数，默认 2 次，设为 0 关闭重试。
- `reasoningEffort` 设置模型支持的推理强度，`providerOptions` 可传供应商原生字段。
- `maxHistoryRounds` 默认 10，默认作为发送给模型的完整轮数上限。添加 `compression: {}` 可启用工具结果裁剪和旧对话摘要，启用后轮数上限作为摘要触发条件。[压缩配置](../compression/README_cn.md)
- CLI 从进程环境读取凭据，也可由调用方自行加载。
- 系统提示由基础 workspace 说明、`.agents/prompt.md` 原文、根目录 `AGENTS.md` 和本地 skills 元数据目录组成。不向上或递归查找其他 `AGENTS.md`；Skill 正文通过 `get_skill` 按需读取。
- `prompt.md` 可选，缺失或为空时不追加自定义提示词，其他读取错误会导致打开失败。初始化创建空文件，不覆盖已有内容。迁移时把旧配置中的 `systemPrompt` 移到 `.agents/prompt.md`。
- 配置、指令与 skills 在打开时固定快照，修改后退出重开。缺失的 `mcp.yaml` 或 skills 目录视为空；存在但格式无效则打开失败。workspace 和内置工具配置文件必需。

`tools.yaml` 沿用 [ai-tools 的配置字段](../ai-tools/README_cn.md)。只有显式列出的、非禁用工具可见且可执行。初始化显式列出文件工具与 Shell；它们仍受确认策略控制。文件根目录和 Shell 初始目录强制绑定项目根目录；可省略或设为 `""` / `"."`，冲突配置报错。其他工具配额沿用 ai-tools 默认值。

在 `tools.yaml` 设置 `shell.timeoutSeconds` 可调整 Shell 时限，默认 30 秒，受当前轮次剩余时间约束。在 `builtinTools` 添加 `ask_user: {}` 可让模型在交互终端向用户提问。

`mcp.yaml` 使用 `mcpServers` 映射，字段参见 [mcp-runtime 配置](../mcp-runtime/README_cn.md)。

~~~yaml
mcpServers:
  local:
    command: ./bin/my-mcp-server
    args: []
    env:
      PROJECT_MODE: local
~~~

stdio 默认工作目录是项目根目录；相对 `dir` 和包含路径分隔符的相对 `command` 按项目根目录解析，裸命令名沿用 PATH。参数由子进程按自身工作目录解释。MCP 配置与内置工具配置分开解析；打开时显式连接启用的服务器，失败时关闭已连接资源。

## 子 Agent 配置

`init` 创建 `.agents/.subagents/`。每个一级子目录注册一个内部工具，角色名可用小写字母、数字、下划线和连字符，最长 55 个字符。目录不存在或为空时不注册。下面的目录注册 `subagent_repo_research`，配置修改在重开 Workspace 后生效。

| `.agents/.subagents/repo_research/` 中的文件 | 内容示例 |
| --- | --- |
| `workspace.yaml` | `{"description":"查证仓库中的实现并提供文件和行号"}` |
| `prompt.md` | `先定位入口和调用方，再总结发现。列出支持结论的文件和行号。` |
| `tools.yaml` | `{"builtinTools":{"read_file":{},"list_directory":{}}}` |

`workspace.yaml` 可选填 `model`，默认沿用父 Workspace 模型。`prompt.md` 可省略，子运行会读取项目根目录 `AGENTS.md` 中的规则。`tools.yaml` 从父 Workspace 已启用的 `read_file` 和 `list_directory` 中至少选一个，沿用它们的项目根目录和限制。

父模型根据职责说明委派，填写任务及必要背景。每次启动新的子运行，父轮同步等待。在所有角色间每轮最多委派两次，每次最多 6 次模型调用和 60 秒。默认审批模式直接允许只读委派，全量审批模式分别确认委派和子工具调用。委派参数和最终结果随父对话保存在历史中，工具结果元数据包含子运行 ID 和用量。子模型的中间消息留在本次运行中。

## 确认与资源边界

`ApprovalWrites` 是默认策略：内置 `read_file`、`list_directory` 和本地 `get_skill` 直接执行；写入、补丁、Shell、所有 MCP 调用逐次确认。即使 MCP 工具名字与未启用的内置读取工具相同，也需要确认。`ApprovalAll` 确认每个工具；`ApprovalAuto` 自动执行已启用工具。确认由调用方的 ConfirmTool 回调提供；未提供回调时拒绝需要确认的调用。拒绝作为工具错误反馈模型，允许它继续处理。

文件工具使用已有根目录限制。Shell 工作目录不是沙箱，MCP 和 Shell 继承服务进程的权限与环境。不同 workspace 是配置与上下文隔离，不是操作系统隔离。

同一 workspace 通过操作系统文件锁限制为单进程打开，不同 workspace 可以分别运行；锁文件不删除，进程退出会释放锁。不要移动、替换或手工编辑正在使用的状态目录。状态目录及两个状态文件拒绝符号链接。该限制不防御具有相同本机权限的并发外部写入。

## NDJSON 与中断恢复

完整执行历史保存在 `.agents/state/history.ndjson`，每行一条 JSON 事件并以换行提交。例如：

~~~json
{"version":1,"sequence":1,"time":"2026-11-04T00:00:00Z","type":"run_started","run_id":"run_example","data":{}}
{"version":1,"sequence":2,"time":"2026-11-04T00:00:01Z","type":"user_message","run_id":"run_example","data":{"message":{"role":"user","parts":[{"type":"text","text":"你好"}]}}}
~~~

事件类型为 `run_started`、`user_message`、`model_message`、`tool_started`、`tool_result`、`run_ended`、`context_reset`。消息直接复用 SDK 协议；工具参数的原始 JSON 和扩展数据的大整数不会经过 float64 转换。错误诊断只记录通用运行状态，不序列化模型配置、HTTP headers 或认证信息；用户消息、模型消息及工具输出会原样进入历史。

用户消息在请求模型前同步落盘，模型消息在分发工具前落盘。批准后，实际工具执行前记录 `tool_started`，返回结果后立即同步记录。每条事件只写增量，不重写全量历史；写入或同步失败立即阻止该实例后续调用，必须退出重开。新建目录权限 `0700`、配置及状态文件 `0600`，已有文件权限不改写。

重开按序号重放。无换行的末尾片段被提示并截断；中间损坏、未知版本、非法事件顺序会报错，保留原记录。启用压缩后，正常结束的运行将摘要、近期上下文和日志位置保存到 `.agents/state/context-checkpoint.json`。后续请求和重开后的请求从此处继续。完整日志仍可供 `/history` 和中断恢复使用。检查点无效时，请恢复检查点或用 `/reset` 开始新对话。

未完成运行通过追加错误工具结果修复调用配对：

- 未记录实际执行开始：明确标记“未执行”。
- 已记录开始但没有持久化结果：明确标记“结果未知”，提醒核实外部副作用。
- 已完成结果保持原样；不会自动重放任何工具，也不自动请求模型。

取消、最大步数结束和错误返回也会完成缺失的结果配对。历史与外部副作用不是同一事务，不提供恰好一次执行保证；`/reset` 也不会回滚文件或外部操作。

## Go API 与验证

~~~go
instance, err := workspace.Open(ctx, projectDirectory, workspaceprot.OpenOptions{
    ConfirmTool: approvalCallback,
    Warn: func(message string) { log.Print(message) },
})
if err != nil {
    return err
}
defer instance.Close()
result, err := instance.Execute(ctx, "检查项目")
~~~

`workspace` 导入 `github.com/SmilingXinyi/agent-engine/workspace`；`workspaceprot` 导入其 `/protocol`。可通过 `OpenOptions.Provider` 注入现有 SDK Provider，调用方仍需指定模型；注入 Provider 的资源归调用方管理。未提供确认回调时，所有需要确认的调用默认拒绝。

`History()` 返回独立消息快照，运行中可能暂含未配对的工具批次；`Reset(ctx)` 清空当前上下文。并发 Execute/Reset 返回忙错误；`Close()` 取消运行、等待结束、关闭 MCP、释放日志和锁，可重复或并发调用。不要在执行 hook 内同步调用 Close，因为它会等待当前执行结束。其他观察回调也应遵循现有 loop 的 context 与错误契约。

~~~bash
go test ./workspace/...
go test -race ./workspace/... ./ai-tools/...
go test ./agent/... ./agent-skill/... ./ai-sdk/... ./ai-tool-loop/... ./ai-tools/... ./mcp-runtime/... ./workspace/...
~~~

测试不依赖真实模型或外部 MCP 服务：使用假 Provider、临时目录、受控 stdio MCP 子进程及真实进程退出验证恢复、锁和资源释放。Shell 相关测试缺少 `sh` 时明确跳过；符号链接不可用时跳过对应边界测试。
