# Workspace

单机单用户的项目执行环境。每个项目目录拥有独立的模型配置、MCP、skills 和一份可继续的对话上下文；只支持 macOS、Linux。CLI 组合现有模块，不引入第二套 Agent 或工具循环。

## 下载 CLI

从 [GitHub Releases](https://github.com/SmilingXinyi/agent-engine/releases) 下载 `workspace/v0.1.0` 对应平台的压缩包：

| 平台 | 架构 | 文件后缀 |
| --- | --- | --- |
| macOS | Apple Silicon | `darwin_arm64.tar.gz` |
| macOS | Intel | `darwin_amd64.tar.gz` |
| Linux | x86-64 | `linux_amd64.tar.gz` |
| Linux | ARM64 | `linux_arm64.tar.gz` |

文件名格式为 `agent-engine-workspace_v0.1.0_<系统>_<架构>.tar.gz`。每包包含 `agent-engine`、`README.md` 和 `README_cn.md`，无需安装 Go。Release 同时提供 `SHA256SUMS` 校验文件。

以 Apple Silicon 为例，下载并解压后执行：

~~~bash
tar -xzf agent-engine-workspace_v0.1.0_darwin_arm64.tar.gz
./agent-engine init /path/to/project
export AGENT_LLM_MODEL='your-model'
export AGENT_LLM_BASE_URL='https://your-provider.example/v1'
export AGENT_LLM_API_KEY='your-key'
./agent-engine chat /path/to/project
~~~

产物是独立的 Workspace Agent CLI，支持本页描述的配置、工具、上下文持久化和恢复能力；模型服务、凭据、外部 MCP 服务器及其运行环境仍由用户配置。

## 从源码运行

从仓库根目录运行，目标项目目录须已存在：

~~~bash
go run ./workspace/cmd/agent-engine init /path/to/project
export AGENT_LLM_MODEL='your-model'
export AGENT_LLM_BASE_URL='https://your-provider.example/v1'
export AGENT_LLM_API_KEY='your-key'  # 接口无需认证时可不设置
go run ./workspace/cmd/agent-engine chat /path/to/project

# 明确选择自动执行；flags 必须放在目录参数之前
go run ./workspace/cmd/agent-engine chat --approval auto /path/to/project

# 构建独立程序
go build -o /tmp/agent-engine ./workspace/cmd/agent-engine
~~~

省略目录时使用当前目录，不搜索父目录。`init` 不覆盖已有文件，也不调用模型、执行工具或连接 MCP。请在项目版本控制忽略规则中加入 `.agents/state/`。

`chat` 自动恢复同一份上下文，等待用户输入，不自动续跑未完成任务。首版显示完整模型响应与工具进度，不提供流式输出。

| 命令 | 行为 |
| --- | --- |
| `/help` | 查看命令 |
| `/history` | 展示最近一次重置后的完整消息 JSON，包括工具调用和结果 |
| `/reset` | 确认后清空当前上下文，追加重置事件，保留原始审计记录 |
| `/exit` | 退出并释放 MCP 和 workspace 锁 |

执行时 Ctrl+C 取消当前运行并返回提示符，再次 Ctrl+C 请求退出；非交互输入时，空闲 Ctrl+C 或 EOF 退出。取消模型、Shell 或 MCP 调用依赖后端响应 context。工具确认的 EOF 或读取失败不会批准执行。

## 交互输入

当前源码版本在 stdin、stdout 均为终端且 `TERM` 不是 `dumb` 时自动启用 [Bubbline](https://github.com/knz/bubbline)，提供局部多行编辑，不占用全屏。已发布的 `workspace/v0.1.0` 仍使用旧版单行输入，需要从源码重新构建才能体验这些功能。

| 按键 | 行为 |
| --- | --- |
| Enter | 提交整段输入 |
| Ctrl+J / Ctrl+O / Alt+Enter | 插入换行 |
| 上下键 | 跨行移动；到达输入边界后召回历史 |
| Ctrl+R | 搜索输入历史；Enter 取回结果，再按 Enter 提交 |
| Ctrl+C | 清空非空草稿；输入为空时退出 |
| Ctrl+D | 当前为空行时结束输入，其他位置按编辑器规则删除字符 |

支持中文和多行粘贴，括号粘贴模式下的换行不会自动提交。Shift+Enter 是否能产生独立按键取决于终端；这版不启用扩展键盘协议，可使用上述换行键，或让终端将 Shift+Enter 映射为 Ctrl+J（LF）。

输入历史只从当前 workspace 的 NDJSON 中提取用户消息，不保存第二份历史文件；重开可召回，`/reset` 后清空可召回内容。工具确认和重置确认不读取对话历史、不保存确认答案；工具确认中的 Ctrl+C 直接中断当前执行。

管道、文件重定向或 `TERM=dumb` 自动沿用逐行输入，不输出编辑器控制序列。无论哪种模式都只有一个输入所有者，编辑结束或取消后恢复终端状态，运行中的工具和模型不会与编辑器同时读取 stdin。

## 目录与配置

~~~text
project/
  AGENTS.md
  .agents/
    workspace.json
    tools.json
    mcp.json
    skills/<skill-name>/SKILL.md
    state/
      history.ndjson
      workspace.lock
~~~

`workspace.json` 的字段全部可选：

~~~json
{
  "model": "your-model",
  "baseURL": "https://your-provider.example/v1",
  "apiType": "chat_completions",
  "apiKeyEnv": "PROJECT_API_KEY",
  "systemPrompt": "先检查项目约定，再进行修改。",
  "maxSteps": 40,
  "maxHistoryRounds": 10,
  "timeoutSeconds": 900
}
~~~

- 未指定 `model` 时按 `AGENT_MODEL`、`AGENT_LLM_MODEL` 顺序取值；`baseURL` 回退至 `AGENT_LLM_BASE_URL`。
- `apiType` 支持 `chat_completions`（默认）和 `responses_api`。
- `apiKeyEnv` 是环境变量名称，不是密钥。未指定时依次读取 `AGENT_LLM_TOKEN`、`AGENT_LLM_API_KEY`，允许无密钥。指定名称后不回退其他密钥。
- 限制为 0 时使用上面的默认值，负数报错。模型输入按完整轮次裁剪，磁盘历史不裁剪。CLI 不自动加载 `.env`；使用进程环境或调用方自己的凭据加载方式。
- 系统提示由基础 workspace 说明、`systemPrompt`、根目录 `AGENTS.md` 和本地 skills 元数据目录组成。不向上或递归查找其他 `AGENTS.md`；Skill 正文通过 `get_skill` 按需读取。
- 配置、指令与 skills 在打开时固定快照，修改后退出重开。缺失的 `mcp.json` 或 skills 目录视为空；存在但格式无效则打开失败。workspace 和内置工具配置文件必需。

`tools.json` 使用 [ai-tools 的配置格式](../ai-tools/README_cn.md)。只有显式列出的、非禁用工具可见且可执行。初始化显式列出文件工具与 Shell；它们仍受确认策略控制。文件根目录和 Shell 初始目录强制绑定项目根目录；可省略或设为 `""` / `"."`，冲突配置报错。其他工具配额沿用 ai-tools 默认值。

`mcp.json` 使用 [mcp-runtime 配置格式](../mcp-runtime/README_cn.md)：

~~~json
{
  "mcpServers": {
    "local": {
      "command": "./bin/my-mcp-server",
      "args": [],
      "env": { "PROJECT_MODE": "local" }
    }
  }
}
~~~

stdio 默认工作目录是项目根目录；相对 `dir` 和包含路径分隔符的相对 `command` 按项目根目录解析，裸命令名沿用 PATH。参数由子进程按自身工作目录解释。MCP 配置与内置工具配置分开解析；打开时显式连接启用的服务器，失败时关闭已连接资源。

## 确认与资源边界

`--approval writes` 是默认策略：内置 `read_file`、`list_directory` 和本地 `get_skill` 直接执行；写入、补丁、Shell、所有 MCP 调用逐次确认。即使 MCP 工具名字与未启用的内置读取工具相同，也需要确认。`all` 确认每个工具；`auto` 自动执行已启用工具。只有 `y` / `yes` 批准，拒绝作为工具错误反馈模型，允许它继续处理。

文件工具使用已有根目录限制。Shell 工作目录不是沙箱，MCP 和 Shell 继承服务进程的权限与环境。不同 workspace 是配置与上下文隔离，不是操作系统隔离。

同一 workspace 通过操作系统文件锁限制为单进程打开，不同 workspace 可以分别运行；锁文件不删除，进程退出会释放锁。不要移动、替换或手工编辑正在使用的状态目录。状态目录及两个状态文件拒绝符号链接。该限制不防御具有相同本机权限的并发外部写入。

## NDJSON 与中断恢复

历史唯一来源是 `.agents/state/history.ndjson`，每行一条 JSON 事件并以换行提交。例如：

~~~json
{"version":1,"sequence":1,"time":"2026-11-04T00:00:00Z","type":"run_started","run_id":"run_example","data":{}}
{"version":1,"sequence":2,"time":"2026-11-04T00:00:01Z","type":"user_message","run_id":"run_example","data":{"message":{"role":"user","parts":[{"type":"text","text":"你好"}]}}}
~~~

事件类型为 `run_started`、`user_message`、`model_message`、`tool_started`、`tool_result`、`run_ended`、`context_reset`。消息直接复用 SDK 协议；工具参数的原始 JSON 和扩展数据的大整数不会经过 float64 转换。错误诊断只记录通用运行状态，不序列化模型配置、HTTP headers 或认证信息；用户消息、模型消息及工具输出会原样进入历史。

用户消息在请求模型前同步落盘，模型消息在分发工具前落盘。批准后，实际工具执行前记录 `tool_started`，返回结果后立即同步记录。每条事件只写增量，不重写全量历史；写入或同步失败立即阻止该实例后续调用，必须退出重开。新建目录权限 `0700`、配置及状态文件 `0600`，已有文件权限不改写。

重开按序号重放。无换行的末尾片段被提示并截断；中间损坏、未知版本、非法事件顺序会报错，保留原记录。首版没有自动压缩、轮转或历史大小限制。

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

## 发布流程

推送 `workspace/v*` tag 自动触发 `.github/workflows/workspace-release.yml`。工作流先在 Linux 和 macOS 上运行完整工作区 race 测试及 CLI 冒烟验证，再交叉构建四个平台包。全部成功后创建 Release 草稿、上传压缩包及校验文件，最后公开 Release；其他模块的 tag 不触发此流程。

~~~bash
git tag -a workspace/v0.1.0 -m 'Release workspace CLI v0.1.0'
git push origin refs/tags/workspace/v0.1.0
~~~

本地可复用同一打包脚本（在仓库根目录执行）：

~~~bash
bash workspace/scripts/package-release.sh darwin arm64 v0.1.0 /tmp/workspace-release
~~~

构建关闭 CGO，不打包本机 `.agents` 配置、凭据或对话历史。工作流仅在发布 job 获得 `contents: write` 权限；失败后可在 Actions 页面重跑，已有同名附件会被替换。带版本后缀的 tag（例如 `workspace/v0.2.0-rc.1`）会发布为 prerelease。

也可在 Actions 页面使用 Run workflow，填写已有的 workspace tag；或运行 `gh workflow run workspace-release.yml --ref main -f tag=workspace/v0.1.0`。手动发布仍检出指定 tag 的源码进行测试与打包，不使用 main 的应用代码，不修改 tag。
