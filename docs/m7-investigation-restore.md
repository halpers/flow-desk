# M7.18 调查历史内容恢复

管理员从已记录的旧修订恢复说明和状态，保存为一个新修订。保留所有原快照，本次保存使用当前操作者和数据库时间。沿用[M7.17 历史](m7-investigation-history.md)、[当前编辑](m7-source-investigations.md)和[工作台](m7-investigation-worklist.md)的租户及资源授权；恢复只改变人工调查内容，不证明归属，不执行存储登记、修复或清理。

## 接口与数据

`POST /api/v1/source-investigations/:object_ref/history/:revision/restore`，要求 Idempotency-Key。仅当前内置管理员且有 knowledge:manage 可调用，逐次检查当前会话、租户及已知/后来恢复版本的库ACL，保留管理员跨本租户团队的现有规则。对象指纹不是授权凭证。无S3/Python调用、outbox、配置开关或新表/迁移；Go模型新增带可信租户/调查/修订条件的有界单行查询。

请求只包含目标路径、期望当前修订和显式确认，说明/状态只从该调查的数据库快照读取：

```json
{"expected_revision": 4, "confirm": true}
```

仅接受正整数修订及有效指纹/请求键；目标必须早于当前修订且实际存在。升级基线可以恢复；基线之前未保存的修订仍不存在，不按旧回执补造。即使历史说明和当前内容相同，确认恢复仍产生一个新修订，唯一效果由请求键而非内容去重。

200 响应沿用 code/request_id/data 信封并设置 Cache-Control: no-store。data 包含 restored_from_revision 和 investigation；后者为保存后的 object_ref/revision/status/note/updated_by/updated_at/allowed_statuses。未返回存储定位、历史表ID或选择凭证。新快照的 is_baseline=false，精确复制源说明（含旧基线的合法首尾空格）和状态，不沿用源操作人/时间。永久审计保留来源修订与新修订；当前回执按既有30天期限保留。

| 情况 | 响应和处理 |
| --- | --- |
| 期望修订与当前不符 | 409 INVESTIGATION_CHANGED；重新读取并核对 |
| 目标不早于当前 | 409 INVESTIGATION_RESTORE_NOT_OLDER；选择已记录旧修订 |
| 调查/目标不存在 | 404 RESOURCE_NOT_FOUND；不返回任何部分说明 |
| 未确认/无效参数 | 400 INVALID_ARGUMENT；不能靠客户端按钮替代服务端校验 |
| 同键改变目标/期望正文 | 409 IDEMPOTENCY_CONFLICT；原请求只恢复原回执 |
| 请求键过期 | 410 IDEMPOTENCY_EXPIRED；重新读取后确认，不能换键盲重发 |
| 当前身份/资源权限不足 | 按现有401/403/404边界拒绝新写和旧回执，界面清空说明 |

## 事务、并发与恢复

同一非空 sqlx.Session 内：幂等作用域锁→当前管理员/会话→调查查找和资源授权→与普通编辑相同的对象事务锁→重读并检查不变定位/expected_revision→按租户和调查读取旧快照→当前调查revision+1→M7.17触发器追加快照→knowledge.source_investigation_restore审计→最终权限/身份复核→不可变回执。

新修订、快照、审计和回执原子提交；快照/审计/回执失败及读取源之后的会话失效全部回滚。对象锁与普通保存一致，同期普通编辑/不同键恢复只允许一个期望修订写者成功；同键并发返回同一回执。审计只记录源/目标修订，不复制说明、指纹、私有定位或确认凭证。

响应丢失后原键原正文重试，仍检查当前权限并直接返回原回执；之后别人已保存的新内容不会被旧恢复请求覆盖。回执显示的是当时保存结果，前端成功后重新读取当前记录，避免把旧回执当作当前内容。撤销恢复本身也应重新核对历史并确认一个新修订，不能删除已产生快照或手改revision。

## 界面与运维

“知识库→调查工作台→继续跟进→查看编辑历史”中，旧修订提供“恢复此修订”，当前修订禁用。确认窗口显示来源说明/状态、期望当前修订和预期新修订，并说明编辑器未保存内容会丢弃；勾选确认后才发送。历史正文按纯文本展示。历史窗口打开期间父编辑器禁止编辑、刷新、关闭及发起保存；确认期间禁止历史翻页/刷新/关闭。

确认请求固定原路径、正文和幂等键。响应不确定可原键重试，也可“放弃重试并重新读取”，后者不会撤销已提交效果。取消/放弃回到最新历史首页；409不自动合并或提交第二次，必须重新读取。401/403/404清空历史和父编辑器说明；成功关闭历史、刷新工作台并重新读取当前记录。使用本地状态，不跨身份共享说明缓存。

发布、IP:port操作、故障及备份回退见[服务器手册§28](../deploy/server.md#28-调查历史内容恢复)。从M7.17升级无需新迁移，较早版本必须先具备§27的历史表和保护触发器。数据库整体备份包含当前记录、全部历史、审计与幂等回执；禁止按旧快照UPDATE数据库实现“恢复”。

## 已执行验收

Go全量119项顶层测试（含子用例279）与vet通过。新增32项（含子用例）覆盖精确内容/状态与新操作者/时间、真实旧库基线迁移及缺失历史、离线无存储依赖、后续修改后的旧回执、请求键操作隔离、同键/不同键及普通编辑竞争、真实快照失败/审计/回执故障回滚、读源后失效无部分响应、普通/委派账号、会话/角色/启停/外租户及当前/后来恢复身份的ACL复核。

[恢复公开接口报告](../reports/m7/investigation-restore-acceptance.json)4/4：服务端读取源、逐字段快照不变、后续编辑后的同键恢复、确认/修订/参数/租户边界、ACL撤销同时拒绝新写与旧回执，以及两次成功效果对应两条脱敏审计和回执、原件及副本账本保持。既有租户巡检、调查、工作台、历史接口各4/4，全部使用独立mock/S3合成租户。

Chrome相关5/5通过：新增恢复用例涵盖纯文本/取消/显式确认、已成功响应丢弃、相同键正文重试、其间更新到修订6后重放仍返回修订5但编辑器显示6、确认期间409及重读后恢复、403清空父子说明；另含历史分页/普通编辑、工作台分页/筛选及两项租户巡检。工作台夹具根据历史验收新增记录后只读统计更新至114项，再单独复验通过。未声称运行其他浏览器场景。

Linux Agent49/49、Web单测4/4、前端类型及生产/容器构建、新脚本Ruff通过；现有前端大包提示仍在。goctl和契约导出已执行，保留原86个API操作，新增1个；Python只同步生成的Go契约。CI在既有历史验收后运行同一恢复脚本，main/PR检查和仅v* tag发布策略保持。

## 复跑

按[历史验收前提](m7-investigation-history.md#已执行验收与复跑)准备独立mock/S3栈、work目录配置及Linux work/source-store.test；不可在服务器业务库执行合成写入/ACL故障验收。恢复脚本需要仍有未保存调查的未知版本对象，重复执行请另建合成租户，不清空现有记录。

```bash
python scripts/check-tenant-source-inventory.py --env-file work/investigations.env --output work/restore-inventory.json --browser-fixture work/restore-inventory-fixture.json
python scripts/check-investigation-restore.py --env-file work/investigations.env --inventory-fixture work/restore-inventory-fixture.json --output work/investigation-restore.json --browser-fixture work/investigation-restore-fixture.json
FLOWDESK_E2E_ENV_FILE="$PWD/work/investigations.env" FLOWDESK_E2E_URL="http://localhost:3000" FLOWDESK_INVESTIGATION_RESTORE_FIXTURE="$PWD/work/investigation-restore-fixture.json" pnpm --dir flow-desk-web test:e2e tests/e2e/investigation-restore.spec.ts
```

入口端口匹配独立栈WEB_HOST_PORT。本次为work/m718-local、33000入口；结束后清理独立栈，保留原开发环境及根.env。历史内容自动合并、保留清理、自动流转/认领/修复、跨租户/未知租户目录和其他生命周期增强仍为后续范围。未部署真实服务器或调用付费模型。
