# M7.19 调查历史分层归档

当前内置管理员可以逐项确认归档已结束调查的旧快照。完整内容移入 PostgreSQL 独立归档表，保留原身份、说明、状态、操作人、时间及基线标记；当前调查不变。历史分页和内容恢复继续读取全部快照。目的是减少热表行数，数据库历史总量仍随修订增长；没有物理删除、自动清理、批量归档或空间回收承诺。

## 接口与保留条件

`POST /api/v1/source-investigations/:object_ref/history/:revision/archive-metadata`，要求 `Idempotency-Key`。可信当前租户、有效会话、内置管理员和 knowledge:manage 每次复核；当前已知或后来恢复版本的知识库ACL仍生效，保持管理员跨本租户团队的既有规则。指纹和旧回执不是授权凭证。

```json
{"expected_revision":113,"confirm":true}
```

仅当当前调查为 closed、目标是热快照、不在最近3个修订内且原 changed_at 已满30天才允许归档。这里“最近3个”按调查修订号而非时间计算；目标必须满足 revision ≤ current_revision − 3。年龄按原快照时间计算，不按归档时间、升级时间或当前记录时间计算。保留升级基线的原始时间，因此旧基线符合条件时也可归档。

200沿用公共信封，data含 object_ref、revision、archived_at，Cache-Control: no-store。不接受客户端提供快照正文，不返回私有对象定位、归档表ID或操作者凭证。不增加S3/Python调用、outbox、配置开关或后台任务。

| 情况 | 结果 |
| --- | --- |
| 调查未结束、最近3修订或未满30天 | 409 INVESTIGATION_HISTORY_ARCHIVE_PROTECTED |
| 当前修订与 expected_revision 不同 | 409 INVESTIGATION_CHANGED；重读后再确认 |
| 新键归档已归档快照 | 409 INVESTIGATION_HISTORY_ALREADY_ARCHIVED |
| 调查/快照不存在 | 404 RESOURCE_NOT_FOUND |
| 无效指纹、修订、请求键或未确认 | 400 INVALID_ARGUMENT |
| 同键改变目标或正文 | 409 IDEMPOTENCY_CONFLICT |
| 旧键超过现有30天恢复期限 | 410 IDEMPOTENCY_EXPIRED；先重新读取 |
| 当前身份或资源权限撤销 | 按现有401/403/404拒绝，包括旧回执和冷历史 |

历史GET仅追加可选 metadata_archived、archived_at、can_archive、archive_block_reason。原因按 already_archived、investigation_active、keep_latest、minimum_age 的顺序展示；can_archive只是读取时预览，提交时重新校验。未实现该接口的旧客户端仍可忽略这些字段。热/冷联合读取按原修订降序，原游标上界、用户/租户/调查范围及15分钟期限不变，归档不改变 first_recorded_revision。旧基线之前未记录的内容仍不可补回。原恢复接口可从任一层读取完整旧快照并生成新的热修订。

## 表、保护与事务

迁移2026110605增加 Go 自有 source_investigation_revision_archives，沿用原快照UUID、租户/调查/修订唯一约束及同租户外键，另存 archived_by/archived_at。模型使用隔离schema迁移和goctl生成；time/bool映射已核对。热/冷联合查询由原历史主模型负责，归档表写入/元数据查询由其自有模型负责，无跨服务直接写表。

数据库升级持有当前调查→热历史的表锁，安装保护后才放行写入。热历史UPDATE仍拒绝；DELETE只允许完整相同快照已存在于受保护归档表的情况。归档INSERT验证完整原快照，延迟约束在提交时确认其已离开热表。只复制不移出、伪造/改写快照都会失败；冷表拒绝UPDATE/DELETE/TRUNCATE，热表仍拒绝TRUNCATE。旧捕获触发器继续在每次成功编辑/恢复时追加热历史。

同一非空sqlx.Session：幂等作用域锁→当前身份/资源授权→原调查对象锁→父记录FOR UPDATE→定位/期望修订复核→保留条件→复制原快照入冷表→条件移出热表→脱敏审计→最终当前身份/资源复核→不可变回执→提交时延迟约束。写入、移出、审计、回执和提交任何一步失败全部回滚，错误返回不含部分回执。父行FOR UPDATE与历史读FOR SHARE互斥，避免READ COMMITTED下分页与归档标记的两个查询看到半次移表。

同键同正文并发只有一份效果、返回相同回执；不同键同目标只有一份成功；不同旧目标可依次成功，当前调查修订始终不变。与普通编辑/恢复共享对象锁：编辑先提交时旧期望归档409，归档先提交时后续编辑正常生成新修订。原键重放在当前授权后返回原回执，即使后来调查已重新开启或修订增长，也不重复归档或覆盖较新内容。

审计 operation 为 knowledge.source_investigation_archive，仅记录目标/当前修订等元数据，不复制说明、指纹、存储定位或凭证。永久冷快照不依赖幂等回执的30天期限；回执过期不会删除历史。

## 操作界面与部署

“知识库→调查工作台→继续跟进→查看编辑历史”展示保留原因及“已归档”标记。符合条件的“归档此修订”打开确认窗，展示原内容/状态/操作者/时间、目标和期望当前修订，勾选后提交。正文按纯文本渲染；未保存草稿不作为归档内容。父编辑器在历史打开时锁定，确认期间禁止翻页/刷新/关闭。

请求固定目标、正文和幂等键，响应不确定可原键重试；放弃重试只重新读取，不能撤销已提交归档。409不自动改键或合并，先重新核对。成功提示回执后重新读取真实当前调查并刷新工作台，避免旧回执覆盖较新编辑；401/403/404清空父子历史内容。归档后“恢复此修订”仍可使用。

无需新端口、域名或S3权限，支持既有IP:port入口。升级必须执行新迁移并一起发布配套镜像，完整备份包括两层历史、当前记录、审计/回执及所有约束/触发器。存在冷快照后，M7.17/18仅读热历史的旧镜像不再是完整历史/恢复的兼容回退目标；不得靠Down、删表、禁用触发器或复制覆盖单表回退。具体维护窗口、只读核对和整库恢复见[服务器手册§29](../deploy/server.md#29-调查历史分层归档)。本次只完成本地验收，未部署服务器。

## 已执行验收

新增40项含子用例的Go数据库测试，覆盖原始九字段精确保留、当前记录不变、离线归档/冷恢复、升级基线/空格保留、最新三项/年龄/状态保护、请求校验、同键/同目标/不同目标及编辑/恢复并发、真实数据库不可变/延迟约束、写入/删除/审计/回执/最终身份和提交失败回滚、113修订稳定分页，以及实际观察父行锁等待后读取完整归档。普通/委派账号、会话撤销、角色/启停、外租户及ACL撤销同时拒绝新归档、旧回执、冷历史和冷恢复。全量Go128项顶层/含子用例319项与vet通过。

[归档公共接口报告](../reports/m7/investigation-history-archive-acceptance.json)4/4通过：完整身份/内容移表，113修订分页、归档后恢复和后续编辑的原回执，参数/权限边界及原件/副本账本不变。既有租户巡检、调查、工作台、历史、恢复接口各4/4回归通过。Chrome新增归档及原历史/恢复共3项通过，覆盖丢失响应原键重试、中途编辑、冷恢复、409重读和403清空。前端构建/4单测、Agent49项隔离schema Linux测试、契约导出及文档检查通过。既有大包体积提示仍在；没有运行其余浏览器流程或真实模型评测。

验收脚本仅用于work下的一次性mock配置与合成inventory租户，会创建旧时间修订并临时模拟ACL，禁止指向服务器业务库：

```powershell
flow-desk-agent/.venv/Scripts/python.exe scripts/check-investigation-history-archive.py --env-file work/disposable/.env --inventory-fixture work/disposable-inventory.json --output work/disposable-archive.json --browser-fixture work/disposable-archive-fixture.json
```

上述命令为模板；实际本地运行使用work/m719-local/.env和新建合成租户。CI在原恢复验收后运行同一脚本，main/PR仍只检查，v* tag才发布双仓库镜像。
