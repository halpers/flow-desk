# M2 只读任务交付

当前可以从工作台创建会话，用订单号查询订单和物流，并在刷新、进度连接中断后恢复结果。执行图为 `read-v1`，模型为确定性 `mock`。

## 数据和执行

Go 在同一 PostgreSQL 事务中保存用户消息、助手占位消息、任务、`run.queued`、投递 outbox 和请求幂等响应。发送消息时先锁会话，检查 client_message_id 重放，再检查活跃任务与租户配额；拒绝的请求不留下半份消息。会话和消息分页分别按创建时间/ID、消息 position 排序，游标绑定租户、调用者和过滤条件。

调度器短事务领取命令，释放事务后调用 Agent，成功才标记 delivered。网络失败以同一个 dispatch_id 有界重投；Agent 对已保存的相同命令返回当前状态，改变内容则拒绝。Go outbox 尝试最多 8 次，Agent 回调最多 20 次，最终状态仍受 Go 的 120 秒任务期限控制。

Agent 领取命令后持有 run 对应的 PostgreSQL session advisory lock，并在同一连接上读写 LangGraph 检查点。命令租约与回调领取均有 owner fencing；回调按运行代次和位置顺序发送。回调先入库再发 HTTP，响应丢失后重投原始请求体。读工具有稳定调用 ID 与结果缓存，节点恢复复用首次成功的事实。

图为理解/路由 → 订单与物流工具 → 保存答复。只有 `order.lookup`、`logistics.track` 两个读工具。缺少订单号时答复要求补充；办理意图会明确提示尚未执行写操作。M2 不提供完整多轮补充信息状态机，该能力随 M4 中断/恢复交付。

## 授权与事件

浏览器只访问 Go。内部路由要求独立服务令牌；工具和新事件还要求 Go 签发的短期 run capability。Go 再读当前用户、会话家族、租户、运行状态、代次与期限，服务令牌和 capability 均不进入图状态或模型输入。账号停用后，新工具调用即被拒绝；正常 refresh 轮换保持已启动任务所属家族的连续性。

每个 run 的事件序号在锁定 run 的事务内分配，消息最终内容与 `message.completed` 同事务提交。快照以 share lock 与写者协调，确保 last_event_seq 和消息对应同一提交状态。已存在的完全相同回调可在任务终态后获取原 ACK，新增终态后事件则拒绝。

SSE 每帧带 `id`、`event` 和 JSON `data`，支持 `Last-Event-ID` 及 `after_seq`；定期心跳和身份复验。前端校验共享 JSON Schema、运行 ID 与序号，去重重复帧，遇到序列缺口或历史过期用事务快照恢复。主动退出或切换会话会终止旧连接。

契约源为 `.api`、FastAPI/Pydantic、`shared/contracts/events/v1/run.schema.json` 和 `shared/contracts/tools/v1/read.schema.json`。`scripts/export-contracts.ps1` 生成三端协议产物；Go/Python 工具校验和前端 SSE 校验均使用生成或分发的契约。

## 可重跑验收

先按 [本地运行说明](runtime.md) 启动服务，再在根目录执行：

```powershell
.\scripts\check-go.ps1
flow-desk-agent\.venv\Scripts\python.exe scripts/check-agent.py
flow-desk-agent\.venv\Scripts\python.exe scripts/smoke.py
flow-desk-agent\.venv\Scripts\python.exe scripts/smoke-tasks.py
pnpm.cmd --dir flow-desk-web test
pnpm.cmd --dir flow-desk-web build
pnpm.cmd --dir flow-desk-web test:e2e
```

Python 检查使用独立临时 schema，Go 检查使用可丢弃数据库，均清理自己创建的资源。E2E 使用本机 Chrome；HTTP/E2E 仅新增合成会话，不清空业务数据。截图和浏览器测试产物已忽略，报告不保存 Cookie、token 或密码。

M2 没有真实模型质量、外部模型成本或性能压测结论。完整办理、取消、审批以及全面故障注入按后续阶段执行。
