# Connect 传输支持矩阵

本矩阵说明 daemon 的 Connect 控制面 RPC 所需的传输方式。“Unary”表示请求/响应 RPC，“server-stream”表示 daemon 发送多个响应，“bidi”表示 `RunAttach`/`ExecAttach` 一类双方都可能发送消息的交互调用。

| 传输方式 | Unary | Server-stream | Bidi attach | 部署说明 |
| --- | --- | --- | --- | --- |
| Unix socket | 支持 | 支持 | 支持 | 配置后 CLI 使用本地 socket，不经过 HTTP 代理。 |
| HTTP/1.1 | 支持 | 支持（需要流式响应） | 不支持 | 代理必须保留分块响应并逐个事件 flush；交互式 attach 需要 HTTP/2。 |
| h2c（明文 HTTP/2） | 支持 | 支持 | 支持 | 仅在可信网络或外层隧道提供传输保护时使用。 |
| TLS h2（HTTPS） | 支持 | 支持 | 支持 | 直接暴露 TCP 时推荐；若由代理终止 TLS，bidi 必须端到端保留 HTTP/2。 |

## 代理要求

- 关闭 server stream 的响应 buffering，并让数据到达时立即 flush（使用代理的 streaming/no-buffer 模式）。
- 将 idle/read timeout 设置为长于预期的 stream 或 attach 会话。CLI 的 timeout 为 0 表示等待完成，并不表示关闭代理超时。
- 保留 Connect 协议头和 HTTP/2 stream 语义。仅支持 HTTP/1 的上游无法承载 bidi attach。

请求的组合不可用时，daemon 返回标明操作的 Connect `CodeUnimplemented` 或传输错误。这与 HTTP/1 代理下 unary 调用成功不同：代理可以支持状态/列表操作，但仍会阻止 attach。
