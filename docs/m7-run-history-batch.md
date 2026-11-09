# M7.26 任务历史批量归档

本阶段增加管理员手动多选任务、统一预览与确认，以及持久批次核对。实现与验收状态以[实施记录](progress.md)为准。[M7.27](m7-run-history-batch-restore.md)增加独立恢复批次；定时任务、永久删除和业务回执清理继续保持独立范围。

## 契约与事务

- `POST /api/v1/run-history-batches/preview` 接受按任务ID排序、无重复的1–10个任务，逐项返回当前版本、维护代次、归档资格与保护原因。任何项无权访问时不返回部分清单；Agent观察失败时整体失败。
- `POST /api/v1/run-history-batches` 接受预览的任务ID、期望版本及期望维护代次，要求`confirm=true`与`Idempotency-Key`。服务端重新观察并授权全部任务，在一个Go事务中保存不可变批次、全部待确认维护意图、成员关系、申请审计及幂等映射。任一项冲突或受保护时整个登记回滚；此时没有Agent归档调用。
- `GET /api/v1/run-history-batches/{batch_id}`返回批次和原操作状态；每次重新检查当前管理员、会话、租户及所有成员任务可见性。批次回执反映原操作，不将后来单项恢复解释为旧批次未完成。
- `GET /api/v1/run-history-batches`提供当前租户、当前有权查看全部成员的最近批次，有界游标分页；无权批次可被过滤，空页仍可能有后续页。
- `POST /api/v1/run-history-batches/{batch_id}/operations/{operation_id}/resume`仅核对该批次已有的一项操作。先检查全部成员授权，再在数据库事务外复用M7.23原操作协议，完成后重新授权全部成员。可由另一位当前有权管理员继续。

批次登记原子，跨服务执行逐项推进。某项失败时已完成项保持完成，其余项保留原操作与维护代次；页面明确显示已完成数和待核对数，不承诺跨服务整批回滚。浏览器一次处理一项，使用服务端返回的原操作身份；关闭或刷新可通过最近批次继续。响应丢失不创建第二批或第二项操作。批次与成员不可修改、替换或删除，维护围栏及原回执沿用M7.23。

## 权限和资源上限

仅当前`admin`且具备`run:observe`的用户可操作。预览、批次登记、读取、分页及单项继续都验证当前身份；所有成员授权成功后才返回清单，执行前后均复核。模型和浏览器不能声明可信归档资格、生成维护代次或指定内部租户。预览与登记使用至多5个并行只读观察；单项推进只有一次现有Agent归档调用，避免一次HTTP请求执行整个批次。

沿用满30天、终态、无活动执行及业务/证据/资源关联等保护，单任务事件及checkpoint上限保持。批次只保存身份与操作关系，不复制消息、正文、检查点或私有推理。完整备份须包含新批次表和全部M7.23热/冷表、封存及回执。迁移仅改变Go自有schema，运行Agent无需DDL权限。

## 管理员流程与恢复

进入任务记录，选择租户范围及需要归档的任务，打开批量预览。移除受保护项后重新预览；核对全部候选并勾选确认后登记批次，逐项显示归档进度。刷新或关闭后从最近批次重新进入；也可按批次ID定位。某项未确认时先恢复Agent/API连接，再继续原项；不得通过删除保护记录或另造代次绕过保护。

批次完成只说明各原操作完成。需要核对当前存储状态或恢复原文时进入单项“历史归档与恢复”。恢复后执行身份仍永久封存。旧应用回退仍须满足M7.23及M7.25的冷表/围栏/独立迁移兼容边界。


## 验证与复跑

Go的`TestRunHistoryBatch*`使用独立测试数据库；公开脚本只调整自己新建的合成任务时间。复跑必须使用`work/`下可丢弃mock环境，禁止连接服务器业务库。

```powershell
python scripts/check-run-history-batch.py --env-file work/retention-ci.env --output work/run-history-batch.json --browser-fixture work/run-history-batch-fixture.json
$env:FLOWDESK_RUN_HISTORY_BATCH_FIXTURE = "../work/run-history-batch-fixture.json"
pnpm.cmd --dir flow-desk-web test:e2e tests/e2e/run-history-batch.spec.ts
```

登记正文示例（任务ID须已排序，版本及维护代次来自当前预览；`Idempotency-Key`使用请求头）：

```json
{
  "items": [
    {"run_id": "11111111-1111-4111-8111-111111111111", "expected_version": 3, "expected_generation": 0},
    {"run_id": "22222222-2222-4222-8222-222222222222", "expected_version": 3, "expected_generation": 0}
  ],
  "confirm": true
}
```

本地实测：Go全量153个顶层/含子用例421项、5项批次数据库专项、[公开API5/5](../reports/m7/run-history-batch-acceptance.json)、原单项公开API4/4、Chrome批次1/1、Web4单测及类型/生产构建通过。浏览器两次丢弃真实成功响应，验证刷新后的持久部分完成与只推进剩余项；后置数据库失败、Agent离线及当前权限变化由数据库测试验证。具体实施边界见[实施记录](progress.md)。
