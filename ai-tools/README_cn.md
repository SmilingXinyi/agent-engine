# ai-tools

内置工具库：一个 Go module，按能力划分 package。各包直接返回 `ai-tool-loop/protocol.Tool`，可独立使用、测试和验证。生产代码只依赖工具契约，不依赖 Agent、MCP SDK 或真实模型。

## 工具

| Package | 工具名 | 输入 | 输出 |
| --- | --- | --- | --- |
| `filesystem` | `read_file` | `path` | UTF-8 文件正文 |
| `filesystem` | `write_file` | `path`、`content` | JSON：`path`、`bytes_written` |
| `filesystem` | `apply_patch` | `path`、`patch` | JSON：`path`、`hunks_applied`、`bytes_written` |
| `filesystem` | `list_directory` | `path` | JSON 数组：每项包含 `name`、`type`，按名称排序 |
| `shell` | `shell` | `command` | JSON：`stdout`、`stderr`、`exit_code`、`truncated` |

所有输入必须为单个 JSON object；未知字段、缺少必填参数和错误参数类型会返回错误。

### 文件工具

`protocol.FilesystemOptions.Root` 必填且必须是已存在的目录。构造时将相对根路径按进程工作目录解析为绝对路径；工具参数 `path` 始终相对这个根，`.` 表示根目录。

每次执行使用 Go 1.25 的 `os.Root` 打开路径并释放资源，拒绝绝对路径、向上越界和指向根外的符号链接，允许指向根内的相对符号链接。不保留跨请求文件句柄，无需调用 `Close`。根目录自身在执行时重新打开；调用方应保持该目录位置稳定。此约束不隔离操作系统权限、硬链接或挂载点。

读取只接受常规文件和合法 UTF-8，默认上限 1 MiB；超限返回错误，不返回部分正文。写入创建或覆盖常规文件，允许空内容，不自动创建父目录；新文件权限为 `0600`，已有文件权限保留。默认写入上限 1 MiB，参数和大小验证在覆盖之前完成；写入不是原子替换，I/O 失败可能留下部分内容。目录列表默认最多 1000 项，超限返回错误，不返回部分列表；类型为 `file`、`directory`、`symlink` 或 `other`。

### apply_patch

局部修改已有文件时使用 `filesystem.NewPatchTool`，或在 `builtinTools` 中配置 `"apply_patch": {}`。它独立于 `write_file` 的启用状态，输入示例：

```json
{
  "path": "config.go",
  "patch": "@@ -1,3 +1,3 @@\n package config\n-timeout := 10\n+timeout := 30\n // end\n"
}
```

`patch` 使用单文件 **unified diff**，支持多个 `@@ -旧起始行,旧行数 +新起始行,新行数 @@` 修改块，行数为 1 时可省略 `,1`。块内以空格开头的行是上下文，`-` 是删除，`+` 是添加；空白上下文行也必须保留前导空格。可选 `---` / `+++` 文件头必须对应 `path`，允许标准 `a/`、`b/` 前缀。不接受 Git 命令包装、`*** Begin Patch` 格式、创建／删除／重命名或多文件补丁。将文件内容修改为空是允许的，文件本身仍保留。

先按行号精确匹配旧内容和上下文；行号偏移时，仅在剩余原文中存在唯一匹配才应用。不忽略空白，不进行模糊匹配。重复内容应补充上下文或使用准确行号。所有修改块按原文件顺序验证，不允许重叠；任何块失败、格式错误、内容未变化或结果超限，均不修改原文件。

支持 LF、CRLF 和 `\ No newline at end of file` 标记；内容逐字匹配，CRLF 文件的补丁正文需要包含对应的 `\r\n`。只有文件结尾可以使用无换行标记，不会自动转换文件换行风格。

源文件沿用 `maxReadBytes`，生成结果沿用 `maxWriteBytes`；补丁文本使用 `maxPatchBytes`，默认 2 MiB。只接受已有常规 UTF-8 文件，拒绝作为最终路径的符号链接；父路径仍受 `os.Root` 约束。所有修改块验证通过后，在同一目录写入临时文件、保留原有普通权限位，再通过 `os.Root.Rename` 替换；提交前失败会清理临时文件。替换的原子性遵循当前操作系统的 Rename 语义，不保留原文件 inode、硬链接关联、属主和扩展属性。

同一工具实例的补丁调用串行执行，等待支持 context 取消；提交前会再次检查源文件标识、权限和内容。外部写入或其他工具实例仍需由调用方协调，这不是操作系统级的条件写入事务。配置示例默认禁用 `apply_patch`，启用时设置其 `disabled` 为 false，并为文件根目录提供实际路径。

### Shell 工具

`protocol.ShellOptions.Dir` 必填。`Executable` 默认为 PATH 中的 `sh`，也可显式指定支持 `-c` 的 Shell 路径。构造时验证目录和可执行文件，执行时继承进程环境，以 `<executable> -c <command>` 运行。

默认超时 30 秒，与调用方 context 的截止时间取较早者。stdout、stderr 各保留最多 64 KiB，超过后继续排空输出并设置 `truncated`。非零退出码保留两个输出流，同时设置 `ToolResult.IsError`；启动失败、取消和超时返回 Go error。Unix 取消时终止进程组中的普通子进程；其他平台使用 `exec.CommandContext` 的直接进程终止机制。主动脱离进程组的进程不由该机制管理。

Shell 的 `Dir` 是初始工作目录，并不限制命令的文件访问范围；文件工具的 Root 约束也不会应用到 Shell。调用方通过工具选择和现有 `ConfirmTool` 实施执行策略。

## JSON 配置与组装

配置示例见 [`tools.example.json`](./tools.example.json)。内置工具配置使用独立文件，保留现有 MCP 配置的解析边界；不要把这个文件传给 `mcp-runtime` 的 `LoadConfig`。

```go
import (
    aitools "github.com/SmilingXinyi/agent-engine/ai-tools"
    toolsprot "github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

config, err := toolsprot.LoadConfig("./ai-tools/tools.example.json")
if err != nil {
    return err
}
tools, err := aitools.Build(config)
if err != nil {
    return err
}
// 将 tools 传入 agent/protocol.Request.Tools 或 ai-tool-loop/protocol.Request.Tools。
// 已启动 MCP Runtime 时，可用 append(tools, runtime.Tools...) 合并。
```

- `builtinTools` 必须显式给出 object；`{}` 表示不启用任何工具。只有列出的且 `disabled` 为 false 或省略的工具会构建，未列出的工具不启用。
- 禁用工具不验证依赖、不进入模型定义和执行列表；模型自行请求该名称时，循环按 `unknown tool` 返回错误，不会执行。直接调用工具包的构造函数意味着调用方自行选择启用该工具。
- 未知工具名（包括禁用项）、未知配置字段、null 工具项和非布尔 `disabled` 会被拒绝，以便尽早发现配置错误。
- `LoadConfig` / `ParseConfig` 只解析配置。`Build` 验证启用工具的依赖，但不执行工具；启用文件工具需给出 `filesystem.root`，启用 Shell 需给出 `shell.dir`。
- 配额字段为 0 时采用默认值，负数会报错。Shell 超时单位为秒，文件及输出大小单位为字节。相对配置路径基于进程工作目录，不基于 JSON 所在目录；不展开环境变量。
- 工具按名称排序，配置变化后由调用方重新读取并构建，在下一次请求传入新列表；不自动热更新，也不取消已经运行的调用。

也可以绕过配置组装，直接使用某个能力包：

```go
import (
    "github.com/SmilingXinyi/agent-engine/ai-tools/filesystem"
    toolsprot "github.com/SmilingXinyi/agent-engine/ai-tools/protocol"
)

readTool, err := filesystem.NewReadTool(toolsprot.FilesystemOptions{
    Root: "/tmp/workspace",
    MaxReadBytes: 128 << 10,
})
```

直接构造与配置构建得到相同的 `Tool`，复用循环已有的确认回调、取消、hooks 和执行记录。

## 扩展和验证

同一能力的工具共用 package，例如文件读写和目录列表；只有独立职责才新建目录。每个包负责 schema、参数验证和执行逻辑，公开配置及结果类型放在 `protocol/types.go`，协议行为放在 `protocol/methods.go`。不重新定义或导出别名 `Tool`，不使用 `init` 或全局注册表。

新增工具时，在能力包提供构造函数，按需扩充配置及 `Build`，并在代码旁测试实际可观察行为。平台能力需要真实后端后再引入，避免只有 schema 的占位工具。

```bash
# 从 workspace 根执行；不需要模型凭据、MCP server 或网络
go test ./ai-tools/...
go test -race ./ai-tools/...

# 也可以按能力独立验证
go test ./ai-tools/filesystem/...
go test ./ai-tools/shell/...
```

测试用临时目录验证真实读写、覆盖、列表、字节限制和符号链接边界；Shell 测试验证真实进程的目录、输出、退出码、截断、超时与取消。缺少 `sh` 时跳过 Shell 执行测试，符号链接不可用时跳过对应场景。组装测试通过假的模型 Provider 与真实循环验证工具结果回传及禁用工具无法执行。

无需模型即可使用命令行示例验证配置和单个工具：

```bash
# 默认配置只启用读取和列目录；从 workspace 根执行
go run ./examples/builtin-tools/main.go
go run ./examples/builtin-tools/main.go -tool read_file -arguments '{"path":"AGENTS.md"}'

# 返回 disabled or not configured，命令不会执行
go run ./examples/builtin-tools/main.go -tool shell -arguments '{"command":"echo hello"}'
```
