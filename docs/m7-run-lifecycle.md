# M7.21 任务生命周期盘点

任务运营台按需盘点一项当前授权任务的消息、执行、checkpoint及关联记录，只返回数量和保护原因。此阶段没有归档、删除、压缩或定时清理动作，不改变业务数据、图状态、回执、审计或重试身份，也不承诺存储容量下降。

## 接口与授权

公开源为 `api/operations.api`：GET `/api/v1/runs/:run_id/lifecycle`，沿用 SessionAuth、conversation:use 及任务运营台的授权。所有者可读自己的任务；run:observe 允许当前租户/团队范围内的元数据，不授予会话正文权限。现有任务列表提供状态筛选和有界分页，只有打开生命周期窗口时才盘点该任务，不批量扇出调用。

Go在外部请求之前核对任务可见性，退出事务后请求Python，再锁定任务及当前授权范围重新检查。当前会话、角色、团队或资源撤权时不返回先前取得的数量；任务version变化返回409 LIFECYCLE_CHANGED。最终身份校验失败时清空结果。响应 Cache-Control: no-store，失败不返回部分业务或Agent数据。

Python GET `/internal/v1/runs/{run_id}/lifecycle?tenant_id=UUID` 仅接受既有服务凭据和UUID，使用已由可信执行建立的run_observation_scopes绑定；请求不会创建绑定。未知任务和错误租户均返回available=false、全零计数和唯一AGENT_SCOPE_UNAVAILABLE，Go将其作为保护，不能当成无历史可清理。

Python在READ ONLY REPEATABLE READ事务中，以规范 `run:<uuid>` thread ID统计checkpoint表，只COUNT记录，不反序列化state/blob、模型输出或工具参数。每条查询有2.5秒超时；Go库存查询有3秒context期限，内部HTTP最多5秒及16KiB响应。服务失败、超时或不合规协议返回503 LIFECYCLE_UNAVAILABLE；非零计数却无对应保护、缺失字段、未知字段、负数或不安全整数均拒绝。

## 返回与保护

data 包含 run（原运营元数据）、Go observed_at、minimum_age_days=30、observation_only=true、archive_candidate、protection_reasons，以及business/agent。完整机器字段见[OpenAPI](../shared/contracts/go.openapi.json)及[Agent协议](../shared/contracts/agent.openapi.json)。计数不代表对象字节数，两个服务的observed_at是独立观察时间，不是跨库原子快照。

business计数：消息、事件、动作、审批/未完成审批、待输入、真实回执、证据、投递/未完成投递、重试链关联、会话非终态任务、工单资源关联，以及业务租约是否仍有效。Go保护如下：

| 原因 | 条件 |
| --- | --- |
| RUN_NOT_TERMINAL | queued/running/waiting_input/waiting_approval/cancelling |
| MINIMUM_AGE_NOT_REACHED | 任务最后更新时间不足30天 |
| BUSINESS_LEASE_ACTIVE / CONVERSATION_ACTIVE | 业务租约有效或同会话仍有活动任务 |
| DISPATCH_UNSETTLED | run投递未delivered、死信未处理或仍有有效投递租约 |
| APPROVAL_UNSETTLED / INPUT_UNSETTLED | 仍有prepared/pending/approved审批或pending输入 |
| BUSINESS_REFERENCES | 存在动作、回执或关联工单资源 |
| EVIDENCE_REFERENCES / RETRY_CHAIN_REFERENCES | 仍有证据或前后重试关联 |

agent计数：所有命令及queued/running/waiting/有效租约、callback及未acked/有效租约、模型调用及reserved、checkpoint/blobs/writes、工具缓存、执行span、检索尝试和该run查询embedding及reserved。保护原因为COMMAND_QUEUED/RUNNING/WAITING/LEASE_ACTIVE、CALLBACK_UNACKNOWLEDGED/LEASE_ACTIVE、MODEL_RESERVED、MODEL_STATE_UNKNOWN、EMBEDDING_RESERVED、AGENT_SCOPE_UNAVAILABLE。permanent_failure callback仍算未确认；未知模型状态仍受保护。

只有当前观测中所有保护均为空且Agent available时，archive_candidate才为true。它仅用于后续归档方案的候选盘点，不是删除许可或持续资格凭证；[M7.23归档维护](m7-run-history.md)已另行实现独立授权、恢复隔离、永久执行围栏与一致备份协议，提交时仍重新核对全部保护。幂等、审计和关联业务保留不能由该标志豁免。

## 界面、验收与操作

“任务记录→任务详情→生命周期预览”展示数量、各服务采样时间和保护原因；刷新先清空旧数据，切换任务取消旧请求。401/403/404清空预览及父任务详情/缓存。窗口无归档或删除按钮。

本地验收使用work/m721-local一次性mock栈、隔离数据库/schema及合成任务，无付费模型调用或远程部署。公开接口[4/4报告](../reports/m7/run-lifecycle-acceptance.json)覆盖脱敏数量/30天保护、重复读取保留消息及状态、重试关联保护、匿名拒绝。Go及Python测试覆盖事务/当前授权/恢复保护，实际完整结果见[实施记录](progress.md)。

复跑模板（只允许work内一次性mock配置，禁止业务库）：

```powershell
flow-desk-agent/.venv/Scripts/python.exe scripts/check-run-lifecycle.py --env-file work/disposable/.env --output work/disposable/lifecycle.json
```

部署和回退见[服务器手册§31](../deploy/server.md#31-任务生命周期盘点)。本阶段无新增表、服务端配置或外部写操作。
