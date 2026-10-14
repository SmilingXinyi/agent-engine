# agent-skill

加载本地 `SKILL.md`，生成技能目录和 `get_skill` 工具。模块只依赖 SDK / loop 协议，生产代码不依赖 Agent、MCP 或模型 Provider。

## 文件格式

```markdown
---
name: mobile-popup
description: 识别并关闭手机应用中的广告弹窗。
---

先读取当前界面，定位明确的关闭控件，再操作并重新读取界面确认结果。
```

`name`、`description` 和正文必填。支持 YAML 多行描述、UTF-8 BOM 和 CRLF；允许其他 frontmatter 字段，正文保持原文。`Parse(content)` 可解析内存中的文件内容。

## 与 Agent 组合

```go
skills, err := agentskill.Load("example/.agents/skills")
if err != nil {
    return err
}
tool, err := agentskill.NewTool(skills)
if err != nil {
    return err
}
result, err := assistant.Execute(ctx, agentprot.Request{
    Model:        "your-model",
    SystemPrompt: "你是一个执行助手。\n\n" + agentskill.BuildListing(skills),
    UserPrompt:   "关闭应用中的广告弹窗。",
    Tools:        append(mcpTools, tool),
})
```

代码中的 `agentskill` 来自 `github.com/SmilingXinyi/agent-engine/agent-skill`，`agentprot` 来自 `agent/protocol`。公共技能类型直接导入 `agent-skill/protocol`，不通过根包重导出。

`Load(root)` 返回按名字排序的 `[]protocol.Skill`，包含绝对来源路径和正文快照。递归发现技能时，一旦遇到 `SKILL.md`，该目录的子目录视为技能资源，不再扫描为独立技能。根目录本身也可是一项技能；缺失目录返回空目录，解析错误和重名技能返回带文件路径的错误。目录遍历不跟随子目录符号链接。

`BuildListing(skills)` 只放名称与描述，不注入正文。`NewTool(skills)` 校验唯一名称，并固定当前快照；模型通过 `{"name":"mobile-popup"}` 按精确名称获取正文。返回结果附带来源路径和基础目录，便于理解正文中的相对资源路径。运行时不会重新扫描目录，修改文件后需重新 Load / NewTool。

磁盘文件在 Load 时读取，模型上下文在 get_skill 调用后才包含正文。工具不读取任意路径、不执行技能中的脚本、不自动加载关联文件；需要这些能力时，由调用方显式注册相应工具。多个技能根目录可分别 Load 后合并，再通过 NewTool 校验名字冲突。没有技能时，调用方可直接跳过工具注册。

相比历史实现，移除了 `Metadata` 重复字段、关键词 Match / Select / Score、固定选取 5 项的规则和 Agent Tool 适配层。技能选择交给模型或调用方，只有一套工具协议。

```bash
go test ./agent-skill/...
go test -race ./agent-skill/...
```
