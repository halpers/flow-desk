# 演示与运维手册

当前可运行能力和测量以[实施记录](progress.md)、[M4 行为](m4-actions.md)与 [M5 报告](../reports/m5/summary.md)为准。完整设计中的未实现部分不作为演示承诺。

## 正常演示

启动 `./scripts/start.ps1`，浏览器打开 `http://localhost:13000`。使用本地 `.env` 的合成账号密码，不在截图或录屏展示配置文件。

1. `demo.operator` 登录，查询“FD202611020002 的状态、金额和物流”，展示有来源时间的事实和 SSE 进度。
2. 管理员在知识库确认“退货期限政策”为 ready 且已激活；操作员提问“退货期限政策规定签收后多久可以申请退货”，打开引用查看有效版本。
3. 新会话输入“请创建售后工单，跟进配送问题”，补充订单号。等待出现不可编辑的审批快照，展示此时尚无新工单。
4. 批准一次，刷新页面，打开唯一回执进入真实工单；展示关联订单和时间线。为该工单添加备注再次确认，展示版本及事件变化。
5. 分别拒绝、取消一项新提案，展示没有新增工单。操作员无法指派/关闭工单；主管登录才可管理。其他租户不能读取该对象。

## 失败恢复演示

浏览器 E2E `actions.spec.ts` 会先完成审批请求，再主动丢弃响应，随后按服务端状态恢复显示。HTTP 验收 `scripts/smoke-actions.py --restart-agent` 在等待审批时重启 Agent，再批准原 action，校验没有第二份效果。

业务提交后工具响应/检查点丢失，以及总结失败的更深层故障使用数据库隔离测试演示：Go `TestActionApprovalReceiptReplayAndSummaryFailure` 和 Agent `test_checkpoint_recovers_same_action_and_receipt_after_two_crashes`。它们实际断言一次效果、原回执重放、回执先于总结保存；没有给生产接口增加“制造失败”的开关。复跑完整测试见 `scripts/check-go.ps1` 和 `scripts/check-agent.py`，具体故障映射见[M4 故障矩阵](m4-fault-matrix.md)。

## 配置、升级与恢复

环境配置、数据库密码、JWT/服务令牌和模型 key 均保留在忽略的 `.env`。Go运行角色限制在business；Agent运行角色只有必要DML/schema USAGE，由独立fd_agent_migrator在一次性agent-migrate作业中完成应用及checkpointer迁移。owner仅用于初始化、备份及明确维护，运行容器不接收owner或迁移凭据。新旧卷升级、只读门禁及回退限制见[独立迁移手册](m7-agent-migration.md)。

升级前先备份，记录代码/镜像和迁移版本；先部署兼容旧字段的程序，再开放新接口。禁止自动执行 destructive Down。当前保留 `read-v1`/`ticket-v1`/`ticket-v2` 命令兼容，但没有提供任意图版本迁移器；变更图结构或模型 profile 前先处理活跃/待确认任务，不能假定旧检查点可跨任意版本恢复。Linux 运维的 IP 入口、固定镜像和备份命令见[服务器部署手册](../deploy/server.md)。

本地演练命令 `flow-desk-agent/.venv/Scripts/python.exe scripts/check-backup.py` 使用一致性快照，恢复到新建临时库并验证内容，再清理临时库。dump 只留在 `storage/backups`，不会进入 Git；演示备份不等同异地备份、加密或生产 RPO/RTO 承诺。真正恢复应始终先进入新数据库，验证角色、约束、回执、检查点和索引，再显式切换服务配置。不要覆盖原库或用 `compose down --volumes` 排障。

## 队列与排障

先用 `./scripts/compose.ps1 ps` 查看健康，再按 request_id、run_id、dispatch_id 查 metadata trace。模型/工具错误码、run 快照和真实回执优先于自然语言总结。总结失败但有回执时，业务可能已完成，应按回执查对象，不能重新创建新 action。

Go `outbox_events` 和 Agent `callback_outbox` 负责重试；等待审批不会占工作线程。持久化消息重试仍使用原 dispatch/source ID，不得手工改 ID 后重发写入。`permanent_failure`、重复冲突或持续过期应先保留证据并查授权/版本/协议，当前没有通用管理重投页面。不要直接编辑对方拥有的数据库表“修状态”。

知识索引失败时先检查文件限制、版本状态和索引命令，重新提交文档应产生新版本；撤回不等于物理删除。备份恢复同时包含业务和 Agent/knowledge，避免只恢复工单却丢失对应动作和检查点。模型外部故障不应通过反复重启清空预算。

## 招聘展示证据

可展示 go-zero 生成源与模型、跨表事务/租户及团队授权、LangGraph 中断/恢复、RAG 过滤/有效引用、审批/幂等回执、固定评测/改进、MCP 和语义 embedding。引用[M5](../reports/m5/summary.md)及[M6 实测报告](../reports/m6/summary.md)中的分子/分母和环境，不把 mock 得分写成真实模型得分，也不填写未实施的训练经验或远程上线经历。
