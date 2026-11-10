# M7.15 巡检调查记录

租户管理员可为当前租户巡检对象保存人工调查说明和跟进状态，也能按对象指纹查回既有记录。记录不会登记、修复、读取或删除存储文件；结束跟进不代表归属、内容完整性或可删除性已确认。

## 接口与授权

| 接口 | 行为 |
| --- | --- |
| GET /api/v1/source-investigations/:object_ref | 当前租户指纹查找；无记录返回 revision=0、空说明和可用状态，不返回私有定位 |
| PUT /api/v1/source-investigations/:object_ref | 确认后保存，必须携带 Idempotency-Key、expected_revision、status、note、confirm=true |

两条接口仅当前内置 admin 且拥有 knowledge:manage 可用，委派管理权限的非管理员不能访问。当前会话、账号、租户和已知资源 ACL 在事务内重新检查。已知版本通过既有知识库授权/团队策略核对；内置租户管理员按既有规则可访问全部团队，知识库 read_roles 仍约束该管理员。授权失败遵守现有 401/403/404 隐藏规则，错误不返回说明或部分回执。

object_ref 为小写 64 位十六进制指纹，来自租户巡检，不接受桶名、对象 key、租户、更新人或授权决定。GET 不存在记录返回空草稿，不证明对应文件存在；单独输入任意指纹不能创建记录。

首次保存 expected_revision=0，并传入租户巡检本项 investigation_token 到 selection_token。Go 用独立 AES-GCM 作用域绑定用户/租户/当前存储及观察对象，15 分钟有效；分页或登记凭证、其他用户/存储凭证及不匹配指纹均拒绝。要求 FILE_STORE_INVENTORY_ENABLED 和 FILE_STORE_TENANT_INVENTORY_ENABLED；没有新增开关或 S3 权限。凭证只证明此前受控列表观察，不证明内容或归属，文件后来消失仍可记录这一调查。

既有记录的后续保存带当前修订号，selection_token 可为空；私有定位从可信数据库记录读取，调用者不能变更。存储暂不可用或暂停巡检后，仍可按指纹读取/跟进，且继续检查当前权限。旧凭证/私有定位不作为新资源授权证明：原先未知的版本身份后来恢复时，每次访问立即按该资源当前 ACL 核对。

status 可用值由 GET 的 allowed_statuses 返回：open（待跟进）、investigating（调查中）、closed（已结束跟进）。说明去除首尾空白后为 1–2000 个 Unicode 字符，允许换行/制表符，拒绝其他控制字符；以纯文本保存和展示，不执行 HTML。允许重新打开已结束跟进，状态仅描述工作进度。

请求示例（选择凭证必须从本次真实巡检响应取得）：

```json
{
  "selection_token": "从当前巡检项取得的凭证",
  "expected_revision": 0,
  "status": "investigating",
  "note": "已核对合成导入记录，仍需补充可信备份依据。",
  "confirm": true
}
```

响应数据为 object_ref、revision、status、note、updated_by、updated_at、allowed_statuses；已保存修订从 1 递增，时间为 UTC。接口设置 Cache-Control: no-store。

## 数据与事务

迁移 2026110602_source_investigations.sql 增加 Go 自有 source_investigations：UUID 主键，同租户对象指纹唯一，存储 ID/对象 key 私有保存，创建/更新人使用同租户外键。说明、状态和 revision 受数据库约束。基于隔离临时 schema 应用迁移并由 goctl 生成基础模型，SQL 扩展只在该表自定义 model；ServiceContext 初始化模型。

同一事务先锁幂等键、当前身份和资源授权，再锁租户/对象指纹并重新读行，以 expected_revision 比较创建/更新。两个首次创建或同修订更新只允许一个新效果；另一请求为 INVESTIGATION_CHANGED（409）。调查行、knowledge.source_investigate 审计和不可变 api_idempotency 回执原子提交，后置审计/回执失败整体回滚。无外部 I/O 或异步下游动作，因此不生成 outbox。

审计仅记录对象内部 ID、修订、工作状态及未改变存储的摘要，不复制说明、凭证或私有定位。回执按既有 30 天幂等期限保存完整结果：同键同正文重放返回原修订，即使后来更新、对象消失或巡检暂停；相同键变参拒绝，重放仍检查当前权限。记录本身保存最新说明和修订；M7.15不提供列表；后续[M7.16](m7-investigation-worklist.md)已增加当前租户调查工作台。M7.17已增加[独立编辑历史](m7-investigation-history.md)，升级时最新记录作为基线并记录后续所有成功修订；M7.31另提供[默认关闭的超时调查回待办](m7-investigation-automation.md)；其他自动流转规则或调查数据删除策略尚未实现，不能将幂等回执当作永久编辑历史。

## 页面与运维

“知识库”→“租户对象巡检”，每项“调查记录”进入编辑；也可在巡检抽屉输入完整对象指纹查询既有记录。输入指纹入口在巡检接口禁用/失败时仍可使用，首次创建仍需有效巡检凭证。页面展示当前修订/更新人/时间、状态和纯文本说明；保存需核对固定快照并勾选确认。

响应未确认时禁止改写快照，原确认框“重试保存调查记录”保留同键同正文；“放弃重试并重新读取”丢弃草稿并查询服务器最新记录。冲突后先重新读取再人工合并，禁止用新键强行覆盖。重读清除旧草稿，权限失败隐藏旧说明；关闭重开也重新请求，未将调查正文写入跨用户缓存。

部署、备份、升级/回退和故障处理见[服务器手册 §25](../deploy/server.md#25-巡检调查记录)。已存在记录无需保持 ListBucket 或存储在线；新记录仍来自限定当前租户前缀的巡检。不得因“已结束跟进”跳过原件恢复、大小/SHA256 校验、独立登记或清理确认。

## 已执行验收与复跑

[公共 API 报告](../reports/m7/source-investigations-acceptance.json)通过 4/4：实际租户未知对象创建、确认和同键恢复；修改后原回执保持、旧修订和错误凭证拒绝；已知资源 ACL 撤销后读取/修改/重放拒绝；两份调查、三份审计/回执与原件/登记账本一致。只使用独立 mock/S3 栈和新建合成租户。

Chrome 新增调查用例通过，覆盖纯文本、显式确认、提交成功但丢弃响应后的同键同正文恢复、真实并发保存引起 409、重新读取、按指纹查回及模拟 403 隐藏旧说明；既有租户巡检的两个页面用例回归通过。Go 集成覆盖令牌作用域/过期/篡改、长度/控制字符、两次并发创建和编辑、同键并发、审计/回执插入及修改回滚、离线跟进、未知身份恢复后的 ACL 和当前账号/租户/会话边界。既有管理员团队策略保持。全量测试数字见[实施记录](progress.md)。

以下只在独立 mock 开发栈执行，需已安装固定项目依赖。先生成新的合成巡检夹具，再执行调查验收；测试会修改新租户说明/ACL 并恢复 ACL，禁止在服务器业务库/生产桶运行：

```bash
python scripts/configure-source-storage.py --enable-inventory --enable-tenant-inventory
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml up --detach --wait --wait-timeout 180
mkdir -p work
install -m 600 .env work/investigations.env
CGO_ENABLED=0 go -C flow-desk-serv/flowdesk test -c -o "$PWD/work/source-store.test" ./internal/client
python scripts/check-tenant-source-inventory.py --env-file work/investigations.env --output work/investigations-inventory.json --browser-fixture work/investigations-inventory-fixture.json
python scripts/check-source-investigations.py --env-file work/investigations.env --inventory-fixture work/investigations-inventory-fixture.json --output work/investigations.json --browser-fixture work/investigations-fixture.json
```

Chrome 用例读取 FLOWDESK_SOURCE_INVESTIGATIONS_FIXTURE，以及已有 FLOWDESK_E2E_ENV_FILE / FLOWDESK_E2E_URL。其候选必须是新夹具，已执行过的夹具先重建合成租户，不清空已有业务记录。CI 增加同一公开接口验收；保留 main/PR 检查和仅 v* tag 发布双仓库镜像的规则。未部署服务器或调用付费模型。
