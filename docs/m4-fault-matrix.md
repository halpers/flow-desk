# M4 故障验证证据

以下均为 2026-11-03 实际执行通过的测试。Go 使用可丢弃数据库，Agent 使用隔离 schema；进程重启和浏览器验收使用本地合成租户。没有在真实客户环境注入故障。

| 故障 | 证据与实际断言 |
| --- | --- |
| 接受任务后浏览器丢失响应 | Go TestMessageIdempotencyAndTransactionalOutbox：相同消息/请求键返回同一个 run，原子事件与 outbox |
| Go 未收到 Agent 202 | Go TestOutboxLostAcceptanceResponseReusesDispatch：接收后强制断开 HTTP，两次投递 payload/dispatch 相同，持久化重试后 delivered；Agent test_command_fencing_and_ordered_callback_recovery 验证接收去重 |
| prepare 后崩溃 | Agent test_checkpoint_recovers_same_action_and_receipt_after_two_crashes：替换检查点连接后使用原 action_id 再准备；Go TestActionApprovalReceiptReplayAndSummaryFailure 验证准备快照唯一 |
| 检查点成功，等待回调丢失 | Go waitApproval helper 重放相同等待事件且无新能力令牌仍获得原 ack；Agent callback outbox 测试重投、顺序和过期 claimant；HTTP 验证 waiting 后重启仍为同一确认 |
| 业务 COMMIT 后响应丢失 | Go TestActionApprovalReceiptReplayAndSummaryFailure 返回完全相同回执且仅一张工单；Agent 崩溃测试在效果后 checkpoint 前停止，恢复使用原 action_id |
| 业务成功，总结失败 | 同上 Go 用例提交 MODEL_OUTPUT_INVALID 后 run failed，消息和快照仍有回执；Agent 崩溃测试确认 receipt 在 finalize 前已经持久化 |
| 确认/取消竞争 | Go TestCancelDecisionRaceAndCommittedEffects：并发请求只获得合法状态，旧版本取消冲突后显式重试，不允许取消后写入 |
| 已提交效果后取消 | Go TestCancellationPreservesCommittedReceipt：cancelled 快照和消息保留回执，重复执行返回原对象，没有第二份效果 |
| 执行前资源或政策变化 | Go TestActionResourceChangeAndTransactionRollback、TestPolicyAndPermissionRevocationBeforeExecution：资源版本变化/政策撤回使动作 invalidated，无新工单；后续审计失败回滚全部业务写 |
| 旧执行代次回调/写入 | Go 拒绝旧代次 ExecuteAction 和新 source 回调；Agent test_generation_fence_and_durable_cancellation_block_old_worker 验证所有权代次不能倒退 |
| 执行锁连接断开 | 同上 Agent 用例：连接关闭前替代连接无法取得锁，关闭后可取得；旧连接无法再查询/写检查点，取消记录阻止恢复排队 |
| 文档回调乱序、角色/ACL 撤销 | M3 两个 Go 知识用例和 Agent 检索测试；M4 执行前禁用 actor/撤回依据测试拒绝新效果 |
| SSE 重复/拆包/断线 | 3 个前端解析用例；workspace E2E 中断流、刷新后结果一致；actions E2E 处理等待/恢复和确认响应丢失 |
| 模型限流、输出持续非法 | Agent test_model_protocol_retry_cache_budget_and_invalid_output：429 有界重试、输出修复次数与预算限制；M4 真实回执不依赖模型总结成功 |

测试来源：[Go 动作](../flow-desk-serv/flowdesk/internal/logic/actions_test.go)、[Go 任务](../flow-desk-serv/flowdesk/internal/logic/tasks_test.go)、[Agent 恢复](../flow-desk-agent/tests/test_actions_recovery.py)、[Agent outbox](../flow-desk-agent/tests/test_durable_runtime.py)、[浏览器](../flow-desk-web/tests/e2e/actions.spec.ts)。
