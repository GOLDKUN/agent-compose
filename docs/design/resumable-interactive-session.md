# 可恢复、可寻址的 Agent 交互会话设计

## 目标

在不破坏现有 `AttachAgentRun` 客户端语义的前提下，支持 Web 刷新、网络重连以及 IM/Web 切换后继续同一个 Agent 交互会话。`waiting_human` 等上层业务状态不属于 agent-compose 领域模型。

## 当前边界

`RunAgent`/`StartAgentRun` 面向非交互执行；`AttachAgentRun` 的首帧携带完整 `RunAgentRequest` 并创建新 run，输入和 runtime interaction 绑定在该 RPC 生命周期内。断开连接会请求取消运行。因此现有 API 不支持按 `run_id` 恢复交互，也不能让 `StartAgentRun` 创建的普通后台 run 获得输入通道。

## 核心模型

```text
Run
 └── InteractiveSession
      ├── RuntimeInteraction
      ├── 单写者输入队列
      ├── 输出事件/续传游标
      └── 短生命周期 Attachment
```

`Run` 继续负责持久化状态和 sandbox；`InteractiveSession` 负责 runtime 交互生命周期；`Attachment` 仅负责连接收发。runtime interaction 不应由 RPC 调用栈独占。

## 兼容协议方向

保留 `AttachAgentRun` RPC。对 `AttachAgentRunStart` 增加可选字段：

- `run_id` 为空：保持现有创建新交互运行的行为；
- `run_id` 非空：连接已有 InteractiveSession，不创建新的 run/sandbox；
- `disconnect_policy` 默认保持当前断开即取消语义；新客户端可选择 detach，使 session 继续运行。

这是 protobuf 向后兼容扩展，不改变旧客户端生成代码或现有首帧语义。

## 生命周期与并发

第一阶段只允许一个 attachment 持有输入租约，多个 attachment 可订阅输出。输入复用现有 `client_frame_id` 做幂等去重，并由 session 内部单写者队列保证顺序。终态、超时、取消和空闲清理必须由 session manager 统一处理。

## 输出恢复

attachment 应支持从事件序号继续订阅。初期可复用现有 `ListRunEvents`/`FollowRunLogs`，后续将历史补发与实时订阅合并，避免刷新时丢事件。

## 分阶段交付

1. 引入 session manager，支持 attach/detach 和 human message；session 仅在 daemon 生命周期内有效。
2. 为 `StartAgentRun` 增加可选 interactive 启动选项，使后台提交的 run 可被 attach。
3. 持久化 session 元数据，补充事件续传、租约、多实例 ownership，并按 Docker/BoxLite/Microsandbox 声明能力差异。

## 非目标

不新增 `SendHumanMessage` 平行 RPC；不把 `waiting_human` 加入公共 `RunStatus`；不改变现有 CLI `run -it`/`exec -it` 的断开和取消行为。
