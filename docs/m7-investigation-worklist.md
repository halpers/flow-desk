# M7.16 巡检调查工作台

管理员按跟进状态分页查找当前租户已保存的调查，直接打开原编辑器继续处理。无需存储在线或记忆对象指纹；调查记录和对象登记/清理权限保持独立。首次创建仍按 [M7.15](m7-source-investigations.md)从有效租户巡检项进入，本阶段不新增任意指纹创建入口。

## 公开接口

`GET /api/v1/source-investigations?status=&limit=20&cursor=`，响应使用通用 code/request_id/data 信封，设置 Cache-Control: no-store。浏览器只调用 Go；Python HTTP、工具和 SSE 契约没有变更。

| 参数 | 行为 |
| --- | --- |
| status | 省略或空表示全部；否则为 open、investigating、closed，分别是待跟进、调查中、已结束跟进 |
| limit | 省略或 0 使用 20，最大 50；负数或超过上限返回 400 |
| cursor | 首页为空；后页使用原响应 next_cursor，不跨用户、租户、筛选或其他接口复用 |

data 返回 items、has_more、next_cursor、allowed_statuses。items 每项仅含 object_ref、revision、status、created_at、updated_by、updated_at；时间为 UTC RFC3339。列表不读取说明正文，也不返回说明、选择凭证、存储 ID 或对象 key。allowed_statuses 是页面选项的服务端来源；客户端只负责中文标签。没有全局总数，页面序号不代表授权后记录总量。

仅当前内置租户管理员且有 knowledge:manage 可用；委派权限不替代内置管理员身份。身份、租户和权限来自可信会话，不接受请求指定租户。每条记录用私有定位中的版本线索核对当前资源授权；已知版本及后来恢复身份的版本受当前知识库 ACL 控制，沿用内置管理员可访问本租户所有团队的既有规则。未知版本线索不证明归属，也不授予后续登记或删除权。

参数、游标篡改、跨作用域或过长游标为 400；过期为 410 INVESTIGATION_CURSOR_EXPIRED，应刷新第一页。当前会话/角色/权限失效按现有 401/403 等语义拒绝；单条资源不可访问时省略该项。数据库或审计失败整页失败，不返回部分内容，前端隐藏旧列表。

## 分页与权限

按不变的 created_at DESC、id DESC 排序，以时间戳和 UUID 共同定位。修改说明/状态不移动创建位置；同时间戳不会遗漏。新建记录通常位于已读范围之前，需刷新第一页；本接口不提供一致快照，翻页期间状态/权限改变或记录移入当前筛选时，同样需刷新复核。

每次只查询 limit+1 条元数据，取前 limit 条再逐条授权。has_more 和 next_cursor 根据最后一条已扫描记录产生，而不是最后一条可见记录。因此受 ACL 隐藏后 items 可以为空且 has_more=true，应继续下一页；不能用可见项数小于 limit 判断结束。next_cursor 为空、has_more=false 表示扫描结束。

复用现有 AES-GCM 游标实现，但作用域独立为 source-investigation-list-v1，绑定可信租户、用户和 status。密文封装最后扫描时间戳/UUID及15分钟到期时间；翻页保留最初到期时间，不续期。不依赖当前存储 ID、存储配置、对象是否存在或巡检开关；调查列表游标不能作为首次保存的选择凭证。

## 数据与事务

迁移 [2026110603](../flow-desk-serv/flowdesk/db/migrations/2026110603_source_investigation_list.sql)仅为 Go 自有 source_investigations 增加两个索引：tenant_id/created_at/id，以及 tenant_id/status/created_at/id。无新表或字段。隔离临时 schema 应用全部迁移并完成 goctl 模型生成；基础生成模型不需手工变更，元数据投影及参数化查询位于该表 custom model。

同一非空事务完成当前管理员授权、有界查询、逐项资源授权、最终会话复核及 knowledge.source_investigation_list 审计。审计仅记录扫描/可见数量及状态筛选，不复制说明、游标、指纹或私有定位。审计失败回滚，不把失败记录为成功读取。业务记录、对象登记和字节保持；无 S3/Python I/O、outbox 或自动状态流转。

## 页面与运维

“知识库”→“调查工作台”，选择全部/待跟进/调查中/已结束跟进，用上一页/下一页浏览；刷新返回第一页。入口独立于“租户对象巡检”的请求结果，存储离线或巡检暂停仍可查询已保存调查。

“继续跟进”重新授权读取当前说明，复用既有编辑器；保存需核对固定修订/说明/状态并勾选确认，成功后重取当前工作台页面。同键恢复、冲突人工合并、撤权隐藏和旧回执不变沿用 M7.15。列表不缓存说明正文；加载/失败时隐藏旧列表并禁用下一页。M7.17在原编辑器增加[独立编辑历史](m7-investigation-history.md)，工作台列表本身仍只有元数据；没有批量办理或自动认领/修复。

部署、IP:port、迁移/备份、故障及回退按[服务器手册 §26](../deploy/server.md#26-巡检调查工作台)。只查询已有记录不需要新增环境变量或任何 S3 权限；首次创建仍按第23/25节准备巡检。仅做本地验收，未部署真实服务器或调用付费模型。

## 已执行验收与复跑

[公共接口报告](../reports/m7/investigation-worklist-acceptance.json)通过 4/4：113 条调查、同时间戳分页及默认/自定义页大小；三种状态与 SQL 数量一致；非法参数、游标跨用户/筛选、普通账号和其他租户隔离；真实 ACL 隐藏造成空页后仍可继续；每个成功页一条脱敏审计、失败无成功审计，说明/修订/时间及副本账本不变。分页专用条目为新建合成租户的数据库测试元数据，不对应实际对象或归属结论。

Go 全量 108 项顶层测试（含子用例230）及 vet 通过。新增集成覆盖稳定分页期间编辑/插入、原游标期限、过期410/篡改/跨作用域、当前管理员/会话/租户、已知及后来恢复版本 ACL、空页推进、无存储/开关关闭、后置撤销会话和审计失败时不返回部分页面。Linux Agent 隔离 schema 49/49、Web 单测4/4、类型检查/生产构建、新脚本 Ruff 通过；现有前端大包提示仍在。

Chrome 相关4/4通过：新工作台113条/6页无重复、筛选、显式确认保存、成功后刷新、修订及说明重新读取、拒绝刷新隐藏旧列表；既有调查编辑的丢失响应/并发冲突流程与租户巡检/普通账号无入口回归。未声称运行其余浏览器用例。

复跑前创建独立 mock/S3 Compose 开发栈，按[调查记录](m7-source-investigations.md#已执行验收与复跑)准备合成巡检夹具和 work/investigations.env，并编译 Linux work/source-store.test。以下命令读取该隔离配置，会新建/修改合成租户，不在服务器业务库或生产桶执行。先重新生成巡检夹具，再执行本脚本，不能复用已由浏览器改写过的夹具：

```bash
python scripts/check-tenant-source-inventory.py --env-file work/investigations.env --output work/worklist-inventory.json --browser-fixture work/worklist-inventory-fixture.json
python scripts/check-investigation-worklist.py --env-file work/investigations.env --inventory-fixture work/worklist-inventory-fixture.json --output work/investigation-worklist.json --browser-fixture work/investigation-worklist-fixture.json
FLOWDESK_E2E_ENV_FILE="$PWD/work/investigations.env" FLOWDESK_E2E_URL="http://localhost:3000" FLOWDESK_INVESTIGATION_WORKLIST_FIXTURE="$PWD/work/investigation-worklist-fixture.json" pnpm --dir flow-desk-web test:e2e tests/e2e/investigation-worklist.spec.ts
```

浏览器地址应匹配隔离栈 WEB_HOST_PORT。本次使用独立 work/m716-local 配置及33000入口，所有依赖保持锁定版本；验收栈结束后清理，原运行栈及根 .env 保留。CI 在已有调查验收之后执行同一公开接口脚本；main/PR 做检查，只有 v* tag 发布双仓库镜像。
