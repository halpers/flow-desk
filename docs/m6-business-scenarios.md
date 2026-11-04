# M6.5 物流跟进与售后办结

在现有建单/备注基础上，新增需要人工精确确认的接手和办结动作，形成“查询物流/政策 → 建单 → 接手处理 → 记录结论并办结”的演示。每次任务最多一个动作，多个步骤由独立任务确认。实际交付和评测结果见 [progress.md](progress.md)。

| 工具 | 参数 | Go 执行约束与效果 |
| --- | --- | --- |
| ticket.start_processing | ticket_id、expected_version、reason | ticket:manage；当前团队可访问；仅 open；他人已指派时仅管理员可接手。将负责人设为当前可信 actor、状态设为 in_progress，version 加一，记录事件和审计 |
| ticket.resolve | ticket_id、expected_version、reason | ticket:manage；当前团队可访问；仅 in_progress；当前负责人或管理员。原子写入处理结论备注及 resolved 状态，version 加一，记录事件、审计和唯一回执 |

reason 最长 4,000 字且非空。模型不能传入 tenant、actor、assignee、批准决定或任意目标状态。准备、批准和执行检查当前权限与资源版本；工单移动团队、状态改变、负责人改变或依据撤回后不沿用旧方案写入。拒绝/取消不产生业务效果，同一 action_id 在崩溃恢复或丢失响应后只返回原回执。

新任务使用 ticket-v2 图；read-v1/ticket-v1 保留兼容。Go 初次执行 outbox 保存图版本，后续补充/审批恢复从该任务最初的命令读取版本，不因升级切换图。Python 仅对 v2 解析新的计划枚举与物流工单类别；同订单多工单时要求用户给出完整工单编号，禁止默认选择第一条。没有可访问工单时解释并停止办理。

前端审批卡展示动作、工单编号、原/目标状态、负责人及处理结论；回执可跳转工单详情和处理记录。固定合成场景验证接手/办结、角色/团队拒绝、非法状态、并发版本、歧义补充、拒绝/取消、审批等待重启及原回执重放。实际退款、赔付、外部物流催件和发送通知需要独立业务集成，本阶段没有这些外部效果。

验收数据源为 `flow-desk-agent/eval/datasets/m6-business.jsonl`，测试只使用合成账号和订单。先通过显式 mock override 启动，脚本会在发送任务前断言实际容器的模型为 mock、模型密钥为空，并核对工具 transport：

```powershell
./scripts/compose.ps1 -f deploy/compose.monitoring.yaml -f deploy/compose.tls.yaml -f deploy/compose.mcp-acceptance.yaml up --detach --build --wait --wait-timeout 180
flow-desk-agent/.venv/Scripts/python.exe scripts/check-business-scenarios.py --tls --transport mcp --restart-agent
```

Linux CI 使用默认 HTTP transport，通过相同脚本执行 12 个场景，省略 TLS 与重启参数。报告保留数据集 SHA256、每项结果/耗时和业务 ID；它验证确定性编排和真实事务，不代表真实模型理解能力的质量评分。M5 留出集与真实模型质量报告保持独立。

兼容与回退：新工具和回执枚举需要三服务同步升级。v2 可继续恢复旧任务，但旧镜像不支持 ticket-v2；回退前停止新任务并完成或取消 v2 等待任务，保留已完成回执。追加迁移不执行破坏性 Down，也不删除历史数据。
