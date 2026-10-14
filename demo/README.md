# Go SSE 最小示例

仅使用 Go 标准库，直接请求兼容 OpenAI Chat Completions 协议的接口。

在仓库根目录运行，替换下面的地址、密钥和模型名：

```sh
export LLM_BASE_URL='https://your-provider.example/v1'
export LLM_API_KEY='your-api-key'
export LLM_MODEL='your-model'
go run ./demo/main.go
```

`LLM_BASE_URL` 是 API 基础地址，程序会追加 `/chat/completions`。请求发送固定提示
“请用一句话介绍自己。”，设置 `stream: true`，整个请求最多等待 5 分钟。

程序按完整 SSE 事件逐帧打印上游原始字节，支持 LF、CRLF、多行 `data:`，保留注释、
`event`、`id` 和未知字段。打印 `[DONE]` 所在帧后结束，不拆分语义事件、不聚合回答。
如果上游直接关闭连接，会打印 EOF 前剩余字节；本示例不据此判断模型回答是否完整。

HTTP 错误或读取失败时，错误输出到 stderr，程序以非零状态退出。
