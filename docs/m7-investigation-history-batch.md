# M7.20 调查历史批量归档

管理员可以在同一调查内选择多个旧修订，先逐项预览完整内容，再确认整批归档。沿用[M7.19分层归档](m7-investigation-history-archive.md)的当前内置管理员/knowledge:manage、会话/租户/资源ACL、closed调查、最近3修订和原时间30天保护。任一项失去资格整批无效果，不自动跳过冲突项。完整快照继续可分页查询与恢复，当前调查说明、状态、修订和时间不变。

## 接口与快照

两个端点都设置 Cache-Control: no-store，并按当前可信身份授权，未暴露S3定位、选择凭证或历史表ID。没有新配置、迁移、S3/Python调用、后台任务或outbox，复用M7.19保护表和延迟约束。

`POST /api/v1/source-investigations/:object_ref/history/archive-preview`：

```json
{"expected_revision":115,"revisions":[101,102]}
```

选择必须是1–50个严格递增、互不重复的正整数修订。服务端在父记录FOR SHARE下核对当前修订及所有目标，返回 object_ref/current_revision/maximum_items/items；items含原说明、状态、操作人/时间、基线标记及 can_archive=true，按修订升序。预览记录独立的脱敏读取审计，不移表、不产生业务回执。返回任何错误时不包含部分预览。

预览不锁定未来资格、不持久化授权令牌。之后别人归档任一目标、编辑/重新开启调查或撤销权限，都会使原预览失效；提交仍逐次完整验证。客户端不得把预览当成永久授权，也不依赖预览接口产生隐藏业务状态。

`POST /api/v1/source-investigations/:object_ref/history/archive-batch`另要求 Idempotency-Key：

```json
{"expected_revision":115,"revisions":[101,102],"confirm":true}
```

200 data为 object_ref/items，每项为 object_ref/revision/archived_at，与单项归档回执同形，顺序与输入一致。一批只有一个不可变批量回执和一条批量审计，不冒充多次单项请求；幂等operation为 knowledge.source_investigation_archive_batch，与单项作用域独立。审计仅含数量及当前修订，原内容/私有定位/指纹不写入摘要。读取审计operation为 knowledge.source_investigation_archive_preview。

| 情况 | 整批结果 |
| --- | --- |
| 数量/顺序/重复/正整数/指纹/确认或请求键无效 | 400 INVALID_ARGUMENT |
| 当前修订变化 | 409 INVESTIGATION_CHANGED |
| 任一选中快照不存在 | 404 RESOURCE_NOT_FOUND |
| 任一选中快照已归档 | 409 INVESTIGATION_HISTORY_ALREADY_ARCHIVED |
| 任一快照受状态/最近修订/30天保护 | 409 INVESTIGATION_HISTORY_ARCHIVE_PROTECTED |
| 同键改变对象、期望修订或选择 | 409 IDEMPOTENCY_CONFLICT |
| 原键超过既有30天恢复期限 | 410 IDEMPOTENCY_EXPIRED；先读取当前状态 |
| 当前权限不足 | 沿用401/403/资源404；新批次、预览和旧回执都拒绝 |

失败不返回成功子项或偷偷执行剩余项；修改选择必须重新预览核对。响应未确认时原键原选择重试；已完成批次之后调查即使再次修改，当前授权仍允许时也只返回原回执，不改写当前数据。回执期限与永久快照保留相互独立。

## 事务与并发

同一非空sqlx.Session：幂等锁→当前身份/资源授权→原调查对象锁→父行FOR UPDATE→定位及expected_revision复核→有界读取选择和归档标记、逐项保留条件→依次完整复制原快照并条件移出热表→一条批量审计→最终身份/资源授权→批量回执→提交时数据库延迟约束。原九字段及身份完全保留，归档只追加当前归档人/数据库时间。

所有目标在首次移动前全部验证，后续任一写入/删除/审计/回执/身份检查或提交失败时，已移动的早期目标也一起回滚。数据库不能提交只复制未移出的目标。父行锁与历史/预览FOR SHARE互斥，读取不会看到一批中的部分目标已移表、其他目标尚未处理。

同键并发返回一个批次的相同回执；不同键重叠目标只有一个批次成功，失败批次的非重叠目标仍不移动；不重叠批次可依次完成。与单项归档共享同一对象锁；与普通编辑/恢复竞争时，编辑先完成会使旧expected_revision失效，归档先完成则后续编辑/冷恢复继续正常生成新修订。

## 前端与运维

“知识库→调查工作台→继续跟进→查看编辑历史”每项提供选择框，受保护或已归档项禁用。选择仅限当前页，翻页/刷新清空，避免确认不可见的旧选择。“批量归档选中修订”读取服务端预览，逐项纯文本显示说明、状态、原操作者/时间，勾选后“确认批量归档”。未保存草稿不会混入快照。

预览读取失败时确认按钮禁用；确认期间父编辑器及历史导航锁定。固定路径/正文/幂等键用于响应不确定时“重试批量归档”，取消或放弃重试回到最新历史首页，不撤销已提交效果。409重新读取核对，401/403/404清空父子说明；成功提示原回执数量后重新读取真实当前调查并刷新工作台，旧回执不会覆盖较新显示。归档项仍可单项“恢复此修订”。

升级从M7.19无需新迁移，较早版本必须先完成2026110605。沿用IP:port/域名入口，配套镜像、备份及回退见[服务器手册§30](../deploy/server.md#30-调查历史批量归档)。M7.19镜像理解两层历史，但没有批量接口/原键重试；回退前需确认在途批次并保留回执。已有冷快照时仍不能回退仅读热表的更早版本。

## 已执行验收

新增42项含子用例的真实PostgreSQL测试：精确预览/纯读取、完整快照、保护条件/严格选择/最大50项、混合无效目标整批拒绝、早期目标写入后后续失败及真实提交约束失败回滚、审计/回执/最终身份回滚、预览失效无部分响应、同键/重叠/不重叠/单项/编辑/恢复并发、真实锁等待后整批读取、冷恢复与旧回执、当前身份和ACL撤销。全量Go137顶层/含子用例361项和vet通过。

[批量归档接口报告](../reports/m7/investigation-history-batch-acceptance.json)4/4、单项归档4/4和旧租户巡检/调查/工作台/历史/恢复接口各4/4通过；按CI相同顺序再次验证单项→批量夹具兼容。Chrome新增批量以及原历史/恢复共3项通过；新增用例覆盖翻页清空选择、精确纯文本预览、取消、丢失响应重试、较新编辑保持、冷恢复、409重读、预览/执行两处403清空。Agent49项Linux隔离schema、前端构建/4单测、契约生成、Ruff及文档检查通过。既有包体积提示保留，其他E2E或真实模型评测未运行，未部署服务器。

验收脚本会在合成inventory租户追加旧时间修订并模拟ACL，只允许work内的一次性mock配置和M7.19合成归档夹具，禁止指向服务器业务库：

```powershell
flow-desk-agent/.venv/Scripts/python.exe scripts/check-investigation-history-batch.py --env-file work/disposable/.env --inventory-fixture work/disposable-archive-fixture.json --output work/disposable-batch.json --browser-fixture work/disposable-batch-fixture.json
```

CI连续验收的登录共享既有限流额度，单项/批量归档验收对明确的429 RATE_LIMITED执行最多6次登录尝试，按Retry-After进行0.5–2秒有界等待；不重试其他状态或网络失败，不改服务端限流。已在Linux容器主动触发真实429后连续执行两项验收，均4/4；6项独立恢复/边界测试通过，可运行：

```powershell
flow-desk-agent/.venv/Scripts/python.exe -m unittest discover -s scripts/tests -v
```

这是模板；实际执行使用work/m720-local/.env。本阶段独立提交，main/PR检查、v* tag才发布GHCR及阿里云镜像；没有新增跨调查批次、定时任务或物理清理。
