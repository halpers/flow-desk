# M7.17 调查完整编辑历史

调查记录增加按修订号分页查阅的不可变快照，保留每次成功保存的说明、状态、操作人和时间。用户可逐项核对相邻修订的完整内容；不提供自动合并、历史回滚或覆盖历史的操作。当前编辑与修订保护继续沿用 [M7.15](m7-source-investigations.md)，入口同时适用于[M7.16工作台](m7-investigation-worklist.md)。

## 记录起点与数据

迁移 [2026110604](../flow-desk-serv/flowdesk/db/migrations/2026110604_source_investigation_revisions.sql)增加 Go 自有 source_investigation_revisions。每条快照含租户/调查身份、revision、status、note、changed_by、changed_at 和 is_baseline；租户/调查/修订唯一，同租户外键关联调查和操作人。没有存储地址或模型推理。唯一索引支持同一调查按 revision 降序有界查询，无额外全文检索。

旧版本只保存最新内容，不能完整还原过去修改。迁移仅复制每条现存调查的最新修订，is_baseline=true，保留原 updated_by/updated_at；更早修订不伪造，也不依赖有期限的幂等回执补全。界面标识“升级基线”，first_recorded_revision 大于1时说明更早内容未记录。新建调查从修订1开始，之后每次成功保存均记录；历史快照独立于30天幂等回执，不因回执到期而删除。

迁移事务先以 SHARE ROW EXCLUSIVE 锁等待在途调查写者，覆盖基线复制与触发器安装，避免升级期间漏记。AFTER INSERT/UPDATE 触发器从实际调查行写入完整快照，保留数据库保存的操作人和时间；更新必须 revision+1，并保持调查/租户/对象指纹、私有定位及创建身份/时间不变。捕获函数固定迁移时的 schema 搜索路径，隔离模型生成和服务器 business schema 各自独立。

快照表 UPDATE/DELETE/TRUNCATE 均由触发器拒绝；普通 API 无修改或删除历史入口。数据库维护账号仍属于可信管理边界，不把触发器声称为防御恶意数据库所有者的机制。备份恢复保留表、约束和触发器，不在原业务库禁用保护或手工覆盖旧快照。

## 保存事务与恢复

保持原 Go 保存路径及幂等授权：当前管理员/资源授权→对象事务锁→expected_revision 比较→调查行保存→触发器追加快照→脱敏修改审计→不可变回执，全部在同一事务。快照写入、后置审计或回执失败都会回滚最新调查及新快照。没有外部存储/Python调用或新outbox。

同键同正文重放直接读取原回执并复核当前权限，不执行 UPDATE，因此不会重复追加；同键变参和旧修订仍按原409拒绝。两个同修订写者只允许一个新效果；已经确认的旧回执不会被新快照或当前说明改写。保留触发器时，M7.15/16的合法保存也会自动捕获历史。

## 查询接口

`GET /api/v1/source-investigations/:object_ref/history?limit=20&cursor=`，使用通用 code/request_id/data 信封和 Cache-Control: no-store。默认20、最大50，省略或0使用默认；无效参数、篡改/过长/跨作用域游标为400，过期410 INVESTIGATION_HISTORY_CURSOR_EXPIRED须刷新首页。

| data 字段 | 含义 |
| --- | --- |
| object_ref | 当前授权调查的对象指纹 |
| current_revision | 本页读取时的最新调查修订 |
| snapshot_revision | 首次进入该分页会话的修订上界 |
| first_recorded_revision | 实际记录的最早修订；升级前缺失范围不会补造 |
| items | 按revision降序的 revision/status/note/changed_by/changed_at/is_baseline |
| has_more / next_cursor | 是否还有更早修订及对应不透明游标 |

仅当前内置租户管理员且有 knowledge:manage 可读；委派管理权限不替代内置管理员身份。使用可信会话/租户查询调查，复核私有定位中已知或后来恢复版本的当前库ACL，沿用内置管理员跨本租户团队的现有规则。不接受客户端或模型给出的租户、操作人、存储定位或权限决定。对象不属于当前租户、资源不可访问或调查不存在时按既有403/404边界拒绝，失败不返回任何历史正文。

读事务按身份/资源授权→调查行FOR SHARE→查询首修订/limit+1条快照→最终会话复核→脱敏读取审计执行，保持与保存者一致的锁顺序。权限或审计失败返回空结果，不泄露部分页面。成功审计 knowledge.source_investigation_history 只含数量和修订上界，不复制说明、游标、指纹或私有定位。读取无S3/Python依赖，巡检暂停、对象消失或移除存储配置后仍可用。

AES-GCM游标使用独立 source-investigation-history-v1 作用域，绑定当前用户/租户/调查，封装首次修订上界、最后已读修订及15分钟原期限。按不可变revision定位；后续新增修订不进入已开始的翻页会话，刷新后展示。每页继续核对当前权限，即使旧游标仍有效也不能越权。不是对业务当前状态的长期授权快照。

## 页面与运维

打开任意已保存调查，点击“查看编辑历史”，支持上一页/下一页和“刷新编辑历史”回首页。按修订展示完整纯文本说明、状态、操作人和UTC时间，单独标识升级基线及缺失范围。当前修订超过分页上界时提示刷新。历史只读，修改返回当前编辑器重新核对并显式确认；保存成功后再次进入历史重新读取。

说明不用跨请求共享缓存；每次进入/翻页均可取消请求，加载和失败隐藏旧历史、禁用下一页，关闭后销毁数据。HTML按普通文本显示，不执行脚本。部署、IP:port、维护窗口、备份及回退见[服务器手册§27](../deploy/server.md#27-调查完整编辑历史)。无新环境变量、端口或S3权限；未部署真实服务器或调用付费模型。

## 已执行验收与复跑

Go全量113项顶层测试（含子用例247）及vet通过。新增覆盖113修订分页期间保存新修订、固定上界和原游标期限、精确说明/状态/操作人/时间、旧回执不重复、快照和调查身份不可改、旧数据库基线迁移及在途写者等待、当前账号/会话/角色/租户/ACL/后来恢复身份、错误游标和后置失败无部分结果。原调查回滚/并发/幂等用例增加快照数断言，并新增插入/更新的真实快照写入失败回滚。

[公开接口报告](../reports/m7/investigation-history-acceptance.json)通过4/4：113次实际保存的完整分页，第114次保存不改变原上界，刷新及原回执恢复；参数/对象/租户/普通账号/库ACL边界；快照/副本账本不变及逐页脱敏审计。既有租户巡检、调查记录、调查工作台公开验收各4/4。全部为独立mock/S3栈的新合成租户，不重置现有业务库。

Chrome相关5/5通过：新历史用例浏览114修订/6页、纯文本、翻页/刷新，显式确认后第115修订及旧说明保留，403隐藏旧历史、关闭重开重新授权；另含既有工作台、调查响应丢失/冲突和租户巡检回归。Linux Agent隔离schema49/49，Web单测4/4、类型检查/生产构建、新脚本Ruff通过；现有前端大包提示仍在，未声称运行其他浏览器用例。

复跑先按[原调查验收前提](m7-source-investigations.md#已执行验收与复跑)建立独立mock/S3开发栈、work/investigations.env，并编译Linux work/source-store.test。以下脚本会创建合成租户/修订并短暂模拟ACL；不可在服务器业务库/生产桶执行。浏览器夹具已用后须重新生成新合成租户，不清空旧业务记录：

```bash
python scripts/check-tenant-source-inventory.py --env-file work/investigations.env --output work/history-inventory.json --browser-fixture work/history-inventory-fixture.json
python scripts/check-investigation-history.py --env-file work/investigations.env --inventory-fixture work/history-inventory-fixture.json --output work/investigation-history.json --browser-fixture work/investigation-history-fixture.json
FLOWDESK_E2E_ENV_FILE="$PWD/work/investigations.env" FLOWDESK_E2E_URL="http://localhost:3000" FLOWDESK_INVESTIGATION_HISTORY_FIXTURE="$PWD/work/investigation-history-fixture.json" pnpm --dir flow-desk-web test:e2e tests/e2e/investigation-history.spec.ts
```

浏览器地址匹配隔离栈WEB_HOST_PORT；本次使用work/m717-local及33000入口。CI在工作台验收后运行同一历史公开脚本，保留main/PR检查、仅v* tag发布双仓库镜像。独立验收栈结束后清理，根.env与原运行栈保留。自动调查流转、历史内容恢复/合并、历史保留清理及其他增强仍属后续范围。
