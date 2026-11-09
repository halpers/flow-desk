# M7.27 任务历史批量恢复

管理员可选择1–10个已归档任务，统一预览和确认后登记恢复批次，再逐项恢复原消息、事件及检查点的存储位置。恢复不会重新执行任务或解除永久执行封存。实施结果以[实施记录](progress.md)为准；本页描述提交与恢复边界，实际验收数量由实施记录和报告记录。

## 接口与兼容性

机器源为`flow-desk-serv/flowdesk/api/runhistorybatch.api`。新增两个SessionAuth接口，仅当前内置`admin`且具备`run:observe`可用：

| 接口 | 用途 |
| --- | --- |
| `POST /api/v1/run-history-restore-batches/preview` | 按任务ID排序、无重复的1–10项`run_ids`，返回当前版本、维护代次、`can_restore`及保护原因 |
| `POST /api/v1/run-history-restore-batches` | `items`、`confirm=true`及`Idempotency-Key`登记全部原恢复意图，返回`batch_id` |

预览响应为`maximum_items/can_create/items`，每项包含`run_id/expected_version/expected_generation/can_restore/protection_reasons`。恢复资格沿用已有单项接口：最新维护操作必须为已完成的`archived`，且Agent可获取可信任务绑定。当前未归档、已有pending、无可信绑定分别阻止登记；30天归档保留期及`ArchiveCandidate`不会错误地阻止恢复。外部观察失败或任何成员无权访问时不返回部分清单。

登记正文保持与原归档批次相同结构；方向由接口确定，客户端不能提交可信租户或改写成员目标：

```json
{
  "items": [
    {"run_id": "11111111-1111-4111-8111-111111111111", "expected_version": 3, "expected_generation": 1},
    {"run_id": "22222222-2222-4222-8222-222222222222", "expected_version": 3, "expected_generation": 1}
  ],
  "confirm": true
}
```

已登记批次复用原`GET /api/v1/run-history-batches`、`GET /api/v1/run-history-batches/{batch_id}`及`POST /api/v1/run-history-batches/{batch_id}/operations/{operation_id}/resume`。批次响应追加`target`：`archived`为归档历史、`live`为恢复存储。目标由全部不可变原操作派生，不能把后来某任务的当前存储位置当作批次目标；全部成员目标必须一致。缺项或混合目标返回503 `RUN_HISTORY_BATCH_INCOMPLETE`。

原M7.26归档接口、请求类型及幂等operation `run.history.batch`保持。恢复使用独立operation `run.history.restore_batch`，不会把旧归档键解释为恢复。相同operation内同键变参继续返回409 `IDEMPOTENCY_CONFLICT`。

## 原子登记与部分完成

登记先在事务外进行最多5路只读Agent观察，再在一个Go事务中按稳定任务ID顺序验证全部当前授权、版本、维护代次及存储状态，保存不可变批次、成员关系、全部`target=live`原操作、申请审计和幂等映射。任一成员冲突、后置审计失败或提交约束失败都整体回滚；登记本身不调用Agent恢复，也不移动Go冷载荷。

实际恢复逐项复用[M7.23原维护协议](m7-run-history.md)，包括Agent可信租户绑定、执行锁、永久围栏、原操作ID/代次和不可变回执。每项完成后恢复Go载荷，并在同一事务中记完成审计和状态；跨服务调用或Go后置提交不确定时保留pending，由当前有权管理员继续原操作。批次继续接口在调用前后验证全部成员；具体单项完成事务重新检查该任务当前权限。已完成项不会再次调用Agent，未完成项可由另一位当前有权管理员继续。

响应丢失时原请求重放返回同一批次；已经提交的请求键找回不要求Agent在线，但仍验证原正文和全部成员当前授权。幂等窗口过后可通过最近批次或完整批次ID定位持久成员，不另造维护代次。

恢复完成后若任务又被归档，旧恢复批次仍表示原操作完成；重放旧恢复请求和继续已完成原项都不能再次恢复该任务。原归档批次同样不随之后恢复而回退。读取当前存储状态应使用单任务历史窗口。

## 数据与界面

迁移`2026110904_run_history_batch_direction.sql`仅增强已有延迟完整性约束：批次及成员总数须完整，关联原操作必须只有一个方向。复用已有表、模型和ServiceContext，无新Agent迁移。旧批次及原操作不可修改或删除，备份继续覆盖整库、两张批次表、原维护操作、全部热/冷载荷及永久围栏。不得通过删除或补写成员修复不一致登记。

“任务记录”保留原多选和归档入口，增加“批量恢复与批次记录”。恢复预览清楚显示当前资格，确认必须勾选“恢复存储且不重新执行任务”。创建请求冻结其路径、正文和键；即使切到最近批次，重试仍使用原方向。最近维护批次区分归档/恢复，批次详情显示原操作结果及完成数，支持逐项继续、刷新和重开。浏览器关闭只停止后续请求，已登记意图仍保留。

401/403/404清空父列表、批次详情和待重试正文，不持久保存任务载荷或请求到浏览器存储。所有字段与两种预览来自生成的OpenAPI类型，前端仅访问Go。

## 验证与复跑

数据库专项覆盖真实归档/恢复载荷、部分完成及另一个管理员继续、后置失败和混合方向约束整批回滚、同键并发唯一、确认/当前授权/方向隔离、已提交键离线找回，以及后来再次归档后旧恢复请求不改变新状态。公开脚本检查从原归档批次到恢复批次的完整HTTP流程；Chrome通过丢弃真实成功响应检查原键/原操作、刷新恢复和撤权清空。

以下命令仅用于`work/`下独立可丢弃mock栈。脚本只老化自己新创建的两个合成失败任务；不得连接服务器业务库。生成浏览器夹具会将自己的两项重新归档，夹具只用于一次浏览器恢复验收：

```powershell
python scripts/check-run-history-batch-restore.py --env-file work/retention-ci.env --output work/run-history-batch-restore.json --browser-fixture work/run-history-batch-restore-fixture.json
$env:FLOWDESK_RUN_HISTORY_RESTORE_BATCH_FIXTURE = "../work/run-history-batch-restore-fixture.json"
pnpm.cmd --dir flow-desk-web test:e2e tests/e2e/run-history-batch-restore.spec.ts
```

日常发布继续使用配套API/Agent/Web固定版本及已有IP:port或域名入口，无新端口或环境开关。存在恢复批次后应保留能够识别`live`方向的配套界面；故障优先向前修复，不回退到将全部批次标为归档的旧界面。M7.23冷载荷/永久封存与M7.25独立迁移边界仍须满足，禁止Down或重置数据卷。本阶段不增加定时任务、永久删除、业务回执清理或跨租户维护。

本地实测：Go全量157个顶层/含子用例425项及vet、4项新增数据库专项、[恢复公开API5/5](../reports/m7/run-history-batch-restore-acceptance.json)、原归档公开API5/5、Chrome恢复及归档2/2、Web4单测和类型/生产构建通过。原100项操作保持兼容，3项批次响应仅追加可选target；原路由handler/鉴权保持，新增2项；Ruff、actionlint及文档校验通过。具体边界与命令结果见[实施记录](progress.md)。
