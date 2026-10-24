# 迅雷 Agent

从仓库根目录运行：

```bash
go run ./examples/xunlei/*.go
```

入口直接使用本仓库的 `agent`、`ai-sdk` 和 `mcp-runtime`：由模型观察远程桌面、选择工具并执行“启动迅雷 → 点击顶部新建 → 填入链接 → 点击下载 → 核查下载任务”。操作依据每次返回的窗口/UIA/OCR 信息，不保存或复用坐标。

## 配置

根目录 `.env` 或进程环境需要配置：

```dotenv
AGENT_LLM_MODEL=你的模型名
AGENT_LLM_BASE_URL=你的兼容OpenAI服务地址
AGENT_LLM_API_KEY=
```

模型需要支持工具调用。截图在本地交给 OCR，模型接收文字、识别框和中心坐标，因此无需图像输入能力。进程环境变量优先于 `.env`；也支持 `AGENT_MODEL`、`AGENT_LLM_TOKEN` 作为备用变量名。

`download-url.txt` 已放入本次指定的完整下载链接；文件只包含一行原始 URL。程序保留其查询参数，并从 Content-Disposition 参数及 URL 路径提取可能的文件名，供模型和最终截图核验。可通过 `-url-file` 指定其他链接文件。

新建任务输入框优先通过 `uia_set_value` 的 ValuePattern 录入；不可用时聚焦输入框、清空并用 `type_text` 输入。随后点击“立即下载”或实际界面的下载按钮。遇到重复任务时先检查已有任务；链接失效、鉴权错误等按界面结果报告，不反复提交。运行会调用真实模型并在远程迅雷中创建下载任务。

读取 `examples/.agents/mcp.json`，只启动 `desktop-agent-remote` 和 `tiny-ocr-mcp`。桌面配置必须明确包含非空 `--remote` 参数，远程 Windows 桌面服务需已启动，两个本机 MCP 文件需具备执行权限并通过 macOS 首次打开授权。

`observe_desktop` 每次获取指定显示器的完整原始截图，将截图数据保存为权限 0600 的临时文件，再通过 `tiny-ocr-mcp_ocr(image_path)` 识别同一张截图。结束或失败时删除临时文件。图片 base64 不进入模型历史，也不作为 OCR MCP 大消息传输；本流程无需启动 tiny-image 或 mobile-mcp。

优先通过 UIA 定位“新建”并调用 InvokePattern；UIA 不可用时，使用原始截图 OCR 的坐标移动光标、点击。截图未缩放，按照本次显示器起点和桌面工具的物理像素约定处理坐标。

提示词按“定位窗口 → 打开任务框 → 录入并确认 → 提交一次 → 核验”五个阶段推进。允许独立查询或已确定的操作与后续观察在同一轮按顺序执行；需要先确认焦点或内容的写入、提交分开进行。同批工具出错不会自动取消后续调用，因此不会将未经确认的聚焦、写入和下载直接打包。

任务框打开后操作绑定该框，避免重新聚焦主窗口使任务框消失。UIA 查询限定次数，找到 selector 后复用并直接 Invoke/SetValue；键盘兜底先确认输入焦点，内容落入其他应用立即停止。同一阶段连续两次无进展即报告阻碍。坐标诊断只允许一次候选映射验证，具体位置和屏幕参数不写入提示词。这些是对模型的流程约束，实际效果仍需运行验证。

## 运行参数和结果

```bash
go run ./examples/xunlei/*.go \
  -config examples/.agents/mcp.json \
  -env .env \
  -url-file examples/xunlei/download-url.txt \
  -log-file examples/xunlei/run.log \
  -display 0 \
  -max-steps 40 \
  -timeout 10m
```

`-display` 选择要观察的远程显示器，迅雷需位于该显示器上。Ctrl+C 取消执行；日志显示模型调用、工具名、耗时和错误状态。

日志同时输出到 console 和 `examples/xunlei/run.log`，文件按次追加，每次执行带开始时间和结束状态；可通过 `-log-file` 换路径，传空字符串则只输出 console。日志文件已加入 gitignore。

`[tool start]` 标记实际进入执行的工具调用，包含轮次、请求 ID、调用 ID 和格式化参数；`[tool end]` 显示工具错误标志、具体执行错误和耗时；`[tool content]` 完整打印实际返回的窗口信息、UIA 匹配/selector/Pattern/空结果及 OCR 文字和识别框。图像 base64 只显示字节数，不淹没日志。`[observe capture ...]` 与 `[observe OCR ...]` 分别显示截图和 OCR 阶段，`[verify ...]` 记录结束后的独立截图核验结果。

`[model tool call] status=requested` 仅代表模型提出调用；只有出现 `[tool start]` 才表示实际开始执行。`[run end]` 汇总真实调用记录数、停止原因和 token 用量；模型已提出却未执行的最后一批调用另标为 `[tool not executed]`，例如达到 `max-steps` 时不执行最后一轮提出的工具调用。

模型 response 日志与 `remote-desktop-agent` 一致：`[model result]` 输出轮次、请求 ID、finish_reason、耗时和 input/output/total tokens；`[model text]` 保留正文及原始换行，正文为空时明确提示；模型返回 reasoning 内容时通过 `[model reasoning]` 输出；`[model tool call]` 输出调用 ID、工具名、参数字节数和转义后的 `raw_arguments`。这些日志在工具调用校验前打印，校验失败的调用也可看到模型原始返回。

模型自然停止后，程序独立重新截图并 OCR，最多观察三次，要求对应文件名出现在界面、链接输入框特征不再可见，且没有检测到下载失败/链接失效/鉴权失败等提示，才报告任务已出现在列表。该检查不证明文件已经完成传输；模型还应检查目标行的实际任务状态。错误提示使用整个可见画面做保守判断，其他任务报错也可能导致“未确认”；OCR 漏识别或文件名被截断同样可能无法通过核验。

模型错误、达到步数上限、超时或最终 OCR 无法确认均以非零退出码结束。长链接和签名参数可能出现在 response 工具调用日志中。
