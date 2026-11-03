# M4 办理与恢复实现约定

本阶段已实现以下流程；验收结果见 [实施记录](progress.md)。

## 动作和事务

支持 ticket.create、ticket.add_note。Python 只提出参数，Go 根据可信 run/actor/tenant 检查权限、订单/工单版本、有效依据和参数，生成不可变 display_snapshot 与摘要。动作与审批一一对应，action_id 在独立图节点先保存。相同 ID 不同内容返回冲突。审批为发起操作员本人确认，24 小时有效。

批准和恢复 outbox 同事务；恢复使用新 dispatch_id/lease_generation。执行只提交 action_id，不接受客户端替换参数。执行事务依次锁 run、action、approval、订单/工单及有效政策，重新检查权限、期限和版本，然后保存业务对象、回执、审计和 action.executed 事件。已存在且有当前读取权限的回执可安全重放；旧代次不能发起新效果。资源变化使旧动作失效，用户须重新发起并确认。

取消直接在 Go 事务中阻止新业务写并终结任务，失效待执行动作，写入取消 outbox；因为一期业务效果全部在同一个数据库内，不需要等待外部退款等异步补偿。与执行竞争时以 run 行锁串行化。取消前已提交的回执仍出现在快照和页面，绝不宣称已撤销业务。

## 持久化等待

图先分配稳定 action_id/input_id，再准备动作，调用 interrupt 保存检查点。worker 检查到持久化 interrupt 后才把 run.waiting_approval/run.waiting_input 写入 callback outbox，Go 验证对应准备记录并切换等待状态。等待不占执行线程。回调丢失可由原命令恢复补发。

输入和审批 API 校验 expected_version、本人归属和幂等键。批准或补充输入将任务置为 queued，更新代次、120 秒活跃期限并原子创建恢复 outbox。最多补充三次输入；等待 24 小时后过期。运行状态、当前输入、审批和真实业务回执均从 Go 快照恢复。Python 的取消表和命令队列均持久化，单 run PostgreSQL 执行锁与 checkpointer 共用连接。

## 工单

租户内工作人员可查询工单、手工创建和添加内部备注；主管/管理员可分配同租户已启用工作人员和变更 open/in_progress/resolved/closed 状态。closed 不接受备注；状态变化和分配均要求 expected_version。Agent 和表单共用业务逻辑与审计，Agent 额外强制动作审批。页面显示原始用户内容和服务端快照，使用文本渲染。

## 验收要求

真实 PostgreSQL 隔离 schema 验证审批/取消竞争、重复 action、提交后响应丢失、后续写失败回滚、资源/政策变化、权限撤销与旧代次。浏览器验证补充订单号、批准建单、添加备注、拒绝、取消以及刷新恢复。普通功能验收使用 mock；真实模型结果另列，不替代确定性故障测试。

## 当前协议与边界

Go 内部动作端点是 `/internal/v1/runs/{run_id}/actions`、`.../actions/{action_id}/execute` 和 `.../receipt`。准备请求为 action_id/tool_name/arguments/evidence_ids；执行只使用路径 action_id。写工具 schema 在 [actions.schema.json](../shared/contracts/tools/v1/actions.schema.json)，读工具新增 ticket.search。公开 run 快照增加 pending_inputs、approvals、action_receipts。

Go 执行命令仅包含 dispatch_id、run_id、lease_generation、command 和 graph_version；新命令使用 ticket-v1，执行器保留 read-v1 的兼容读取。resume_data 从续租接口读取，包含 input/input_id 或 decision/action_id；不从模型或浏览器携带动作参数。取消命令包含独立 dispatch_id、run_id、lease_generation。本文覆盖的精确字段优先于 contracts.md 中保留的完整目标信封示例。

图缺少订单号时追问；有多张工单时要求明确工单编号。标题、分类和优先级使用受限模板，描述/备注来自用户输入，快照中展示精确值。每个 run 最多一个动作；需要多次办理时分别发起任务。等待恢复仍要求发起会话的凭据家族有效；退出后不能沿用原任务获得新的写权限。

当前没有外部退款、短信或物流写入，因此取消可在数据库事务中直接终结；不暗示已实现外部副作用补偿。手动 retry 接口和复杂跨任务自动重试未实现；失败后的已有业务回执可直接查看，避免把“重新生成总结”误当成重新建单。

复跑：先将本地 MODEL_PROVIDER 设为 mock 并重建 Agent，执行 `scripts/check-go.ps1`、`flow-desk-agent/.venv/Scripts/python.exe scripts/check-agent.py`、`pnpm.cmd --dir flow-desk-web test:e2e` 和 `flow-desk-agent/.venv/Scripts/python.exe scripts/smoke-actions.py --restart-agent`。最后一个命令只重启本地 Agent，并在合成租户追加测试工单；不会清空既有数据。实际报告见 [HTTP 验收](../reports/m4/acceptance-mock.json)。

故障验证由真实数据库测试、HTTP 传输故障注入、持久化检查点及浏览器测试组合构成，具体对应见 [故障矩阵](m4-fault-matrix.md)，不等同生产混沌测试。
