# 接口与跨服务契约

M7.23 增加任务历史维护接口，机器源为 `api/runhistory.api` 和 Python `storage.history` 的 Pydantic 模型。`GET /api/v1/runs/:run_id/history` 返回当前授权任务的 live/archived/pending 状态、维护代次、可操作标记、保护原因及最近操作；Agent 暂不可用时仍可返回当前授权的持久操作及保护原因。维护写要求当前内置 admin + run:observe，调用前后复核当前会话/角色/租户/任务/团队；原归档候选不能代替提交验证。

`POST .../history/archive` 与 `POST .../history/restore` 要求 `Idempotency-Key`，正文 `expected_version>=1`、`expected_generation>=0`、`confirm=true`；200 返回 operation_id/run_id/generation/target/status 和三类 checkpoint 数量。归档仅限满30天、无业务动作/审批/输入/回执/证据/资源/重试链、无活动执行/未确认回调/未结算预算的终态任务；单任务最多50,000个事件与100,000行checkpoint。消息及事件完整内容继续读取，恢复不开放旧执行身份。`POST .../history/operations/:operation_id/resume` 无可改写的目标或代次，使用原持久操作核对；当前有权管理员可继续，不限定原申请人。

版本冲突为 VERSION_CONFLICT，存储代次变化为 RUN_HISTORY_CHANGED，已有待确认操作为 RUN_HISTORY_PENDING，受保护/超限为 RUN_HISTORY_PROTECTED/RUN_HISTORY_LIMIT（均409）。Agent调用未确认或回执无效返回503 RUN_HISTORY_UNAVAILABLE，原操作保留pending；当前授权失败沿用401/403/404且不返回部分结果。恢复后重试返回409 RUN_HISTORY_FENCED，既有LEASE_STALE等更早的执行拒绝语义保留。所有历史响应no-store；通用幂等有效期内同键重放返回原操作回执，不覆盖较新恢复。

Go事务外调用 `POST /internal/v1/runs/:run_id/history`，只接受内部服务身份及 operation_id/tenant_id/generation/target；target仅archived/live，generation严格递增。Agent在精确执行锁内验证可信run/tenant绑定和静默条件，原子移动检查点、建立永久围栏和保存不可变回执；重复原操作返回原回执，冲突拒绝。没有新SSE类型、永久删除或自动批量入口，完整恢复协议见[任务历史归档](m7-run-history.md)。

M7.25 不增加业务 HTTP/SSE 字段。Agent 启动前必须由独立 `agent-migrate` 成功完成应用和 checkpointer 迁移；启动及 `/health/ready` 只读验证当前镜像要求的完整版本序列和运行角色权限，缺失/未来/间隙版本、额外 DDL/所有权或缺少必要 DML 均拒绝就绪，运行中检查失败返回503。迁移凭据不传入运行服务，HNSW维护也使用显式迁移连接，不回退到运行 DSN。现有卷升级和首次权限收紧后的回退限制见[独立迁移契约](m7-agent-migration.md)。

M7.21新增GET /api/v1/runs/:run_id/lifecycle：当前授权任务的消息、关联业务和Agent运行/checkpoint数量、独立观察时间与保护原因。仅元数据，observation_only固定true；archive_candidate只表示规划候选，不是清理许可。服务调用前后复核当前身份/团队及任务版本；409 LIFECYCLE_CHANGED和503 LIFECYCLE_UNAVAILABLE不返回部分数据，响应no-store。内部接口使用可信服务凭据及tenant/run绑定，不创建绑定，不读私有状态，详见[生命周期盘点](m7-run-lifecycle.md)。
M7.20增加同调查批量归档预览POST /api/v1/source-investigations/:object_ref/history/archive-preview（expected_revision、严格递增且不重复的1–50个revisions），返回当前修订、最大批量和完整选中快照；只读授权、逐项资格检查及脱敏读取审计。POST同路径/history/archive-batch另外要求confirm=true及Idempotency-Key，逐项重新校验M7.19保留条件，同一事务完整移表、批量审计和唯一批量回执；data为object_ref/items，每项与单项归档回执同形。任一项不存在、已归档、受保护或当前修订变化时整批无效果。两接口no-store，当前身份/资源权限逐次复核；预览不持久化授权凭证，不替代提交时验证，旧回执仍当前授权。当前调查不变，无新迁移/S3/Python调用。

M7.19新增POST /api/v1/source-investigations/:object_ref/history/:revision/archive-metadata，当前内置管理员且有knowledge:manage，Idempotency-Key、expected_revision>0及confirm=true。仅当前调查closed、目标不属于最近3修订且changed_at至少30天的热快照可归档。完整原快照移入受保护归档表，当前调查不变，审计及回执同事务；200 data为object_ref/revision/archived_at，409 INVESTIGATION_HISTORY_ARCHIVE_PROTECTED、INVESTIGATION_CHANGED或INVESTIGATION_HISTORY_ALREADY_ARCHIVED分别表示保留保护、当前修订变化或已归档。历史项仅追加可选metadata_archived/archived_at/can_archive/archive_block_reason字段，分页及恢复联合读取两层并复核当前权限。无S3/Python调用，不物理删除内容；同键重放返回原回执。

M7.18 新增 `POST /api/v1/source-investigations/:object_ref/history/:revision/restore`：当前内置管理员且有 knowledge:manage，提交 Idempotency-Key、expected_revision>0、confirm=true。服务端在当前资源授权和对象锁下读取本租户调查的历史修订，将说明和状态精确复制为当前修订+1；操作人和时间使用本次保存者。仅允许恢复早于当前的已记录修订（含升级基线），不改写历史，不接受客户端说明/状态作为恢复源。200 data 为 restored_from_revision 和 investigation（与当前调查数据同结构）的不可变回执。当前修订变化409 INVESTIGATION_CHANGED，目标非旧修订409 INVESTIGATION_RESTORE_NOT_OLDER，未记录的目标404，其他输入400。新修订、自动快照、脱敏恢复审计和回执同事务；重放复核当前权限且返回原回执，不再次恢复。响应no-store，无S3/Python调用。

M7.17 增加 GET /api/v1/source-investigations/:object_ref/history，默认20/最大50，按revision降序读取不可变说明/状态快照及操作人、时间。当前内置管理员、knowledge:manage、租户/会话和当前资源ACL每页复核；无存储依赖，返回no-store并记录脱敏读取审计。游标独立绑定用户/租户/调查及首次读取的修订上界，15分钟原期限保留；新修订刷新后查看。迁移只回填旧记录最新修订并标记is_baseline=true，更早未记录的内容无法补回；升级后每次成功保存由数据库触发器在同一事务写入快照，幂等重放不重复追加。详细设计见[调查编辑历史](m7-investigation-history.md)。

M7.16 新增 `GET /api/v1/source-investigations?status=&limit=20&cursor=`，仅当前内置租户管理员且有 knowledge:manage 可用。status 为空表示全部，否则为 open/investigating/closed；默认20、最大50。data 为 items/has_more/next_cursor/allowed_statuses；每项只有 object_ref/revision/status/created_at/updated_by/updated_at，不读或返回说明/凭证/私有定位。按不变 created_at/id 降序逐项复核当前资源授权，隐藏项仍推进游标，空页可以有后页。独立 AES-GCM 游标绑定用户/租户/状态筛选及原15分钟期限；无效参数/篡改/跨作用域400，过期410 INVESTIGATION_CURSOR_EXPIRED须刷新首页，身份权限失败沿用401/403等。响应no-store，同一事务完成授权/最终身份复核/脱敏只读审计，审计失败整页失败，无S3/Python依赖。非快照列表，状态/权限变化需刷新；首次创建和保存仍沿用M7.15。详见[调查工作台](m7-investigation-worklist.md)。

M7.15 增加 GET/PUT /api/v1/source-investigations/:object_ref，仅当前内置租户管理员且有 knowledge:manage 可用。GET 无记录返回 revision=0 的空草稿；已有记录返回当前 status/note/revision/updated_by/updated_at/allowed_statuses。首次 PUT 需巡检给出的 investigation_token（AES-GCM、用户/租户/存储独立作用域、15 分钟）、expected_revision=0、confirm=true、status、note 和 Idempotency-Key；后续按当前修订保存，不依赖存储在线或原选择凭证。status 为 open/investigating/closed，note 去首尾空白后 1–2000 字符。每次读写/重放按私有定位提示重新检查当前已知版本 ACL/团队；名称不是可信归属。记录、脱敏审计和回执同事务，旧修订 409 INVESTIGATION_CHANGED，同键变参 409 IDEMPOTENCY_CONFLICT，选择凭证过期 410 INVENTORY_CURSOR_EXPIRED；参数错误 400。存储 key/存储身份不返回，调查不核验内容、不授予登记或删除权。详见[调查记录设计](m7-source-investigations.md)。

M7.14 增加 `GET /api/v1/documents/:document_id/versions/:version_id/archive-copies/history`，默认 20/最大 50，按 created_at/id 降序，覆盖热登记和独立归档表。每项返回 id/source_epoch/source_size/status/created_at/retired_at/metadata_archived/archived_at/can_archive/archive_block_reason，页面包含当前 source_epoch、items/has_more/next_cursor；不返回存储定位。`POST /api/v1/documents/:document_id/versions/:version_id/archive-copies/:object_id/archive-metadata` 要求 Idempotency-Key、confirm=true 和 expected_source_epoch。只归档已 retired、存在退休时间、早于当前代次、非当前对象且清理投递已 delivered 的记录；保留原始登记身份/定位/hash/时间到独立 source_object_archives，和热行移除、审计及回执同事务完成。200 返回 object_id/version_id/archived_at，409 SOURCE_METADATA_ARCHIVE_PROTECTED 或 SOURCE_CHANGED 表示保护/修订冲突，400 参数或游标错误。读取/重放均复核当前会话、knowledge:manage、库 ACL/团队。旧回执、登记查重和存储巡检通过兼容模型读取归档身份；存储标记保留，无外部对象写入。详见[副本历史归档](m7-source-history.md)。

M7.13 新增 `GET /api/v1/source-inventory?limit=20&cursor=...`，仅当前内置租户管理员可读，前缀来自可信租户身份。data 含 observed_at/content_verified=false/scanned_count/items/has_more/next_cursor，items 含 object_ref/key_kind/source_epoch/size/modified_at/association/reconciliation/document_id/version_id/registered_id/registered_status。已知版本再检查知识库 ACL/团队；不可读项省略，页可能为空但仍有后页。未知版本及无法关联版本的非标准对象仅提供指纹等列表元数据，名称不能证明归属。默认 20、最大 50；加密游标绑定用户/租户/存储及 15 分钟期限；不接受任意桶、前缀或租户参数，不自动登记或修复。独立开关 FILE_STORE_TENANT_INVENTORY_ENABLED=false，且要求原巡检开关；未启用 409 TENANT_SOURCE_INVENTORY_DISABLED，游标错误 400/过期 410 INVENTORY_CURSOR_EXPIRED，存储失败 503 SOURCE_INVENTORY_UNAVAILABLE。每页成功写 knowledge.tenant_source_inventory 只读审计，响应 no-store；前后复核当前身份，不返回原始定位。详见[租户巡检](m7-tenant-source-inventory.md)。

M7.12 新增 `POST /api/v1/documents/:document_id/versions/:version_id/source-adoptions`。请求头 Idempotency-Key，JSON 为 object_ref/adoption_token/expected_source_epoch/confirm=true；200 data 包含 object_id/object_ref/version_id/source_epoch/verified_at，source_epoch 指旧副本代次。巡检每项新增 adoption_eligible/adoption_reason/adoption_token，页面新增 adoption_enabled；选择凭证 15 分钟有效且不代表内容已验证。新登记需先恢复并验证数据库原件，服务端在事务外读取 S3 校验大小/hash，复核后原子登记/审计/回执；同键重放保持原回执并检查当前授权。409 SOURCE_ADOPTION_DISABLED/SOURCE_ADOPTION_PROTECTED/SOURCE_ALREADY_REGISTERED/SOURCE_CHANGED、410 ADOPTION_SELECTION_EXPIRED、429 SOURCE_COPY_LIMIT 需按原因处理，400 为非法输入，404 SOURCE_COPY_MISSING 表示所选副本已消失，503 SOURCE_INTEGRITY_FAILED/SOURCE_UNAVAILABLE 表示校验/读取失败。新开关默认关闭，流程和范围见[人工登记](m7-source-adoption.md)。

M7.11 的 `GET /api/v1/documents/:document_id/versions/:version_id/source-inventory?limit=20&cursor=...` 为知识管理者提供版本范围的只读 S3 对账。data 含 version_id/source_epoch/source_size/observed_at/content_verified=false、items、has_more/next_cursor；items 仅含对象指纹、登记 ID/状态、可识别的键类别/代次、观察大小/时间及对账分类。默认 20、最大 50 条，Cache-Control: no-store。加密认证游标绑定当前用户/租户/文档/版本/hash/存储，15 分钟过期；400 表示参数或游标错误，410 INVENTORY_CURSOR_EXPIRED 需刷新，409 SOURCE_CHANGED 表示观察期间或翻页前原件代次变化，409 SOURCE_INVENTORY_DISABLED 表示未显式启用，503 SOURCE_INVENTORY_UNAVAILABLE 表示存储列举/兼容性失败。每页外部 I/O 前后复核当前授权；不返回对象定位，不将失败视为空列表。分类与非快照边界见[对象巡检](m7-source-inventory.md)。

M7.10 的 `GET /api/v1/documents/:document_id/history` 返回有界完整版本历史，默认 20 条、最多 50 条，按 created_at/id 降序；data 含 document_version/items/has_more/next_cursor。游标绑定租户、用户及文档，每页重查当前会话/权限/ACL/团队。next_cursor 为空表示结束，不返回对象存储地址或原文。原 versions/retention 列表排除已归档元数据，其他语义保持。

`POST /api/v1/documents/:document_id/versions/:version_id/archive-metadata` 要求 Idempotency-Key、confirm=true 和 expected_document_version，返回 200 MetadataArchiveData（version_id/document_id/archived_at）。仅 purged 且无激活/引用/归档历史/未完成知识任务的版本可归档，冲突为 METADATA_ARCHIVE_PROTECTED 或 VERSION_CONFLICT（409）；参数/游标错误 400，当前权限不足 403，对象范围不可见 404。归档详情、热行精简、审计和幂等结果同事务；丢失响应重放返回原回执，撤权后拒绝重放。无新 SSE 类型，详细边界见[历史归档](m7-history-archive.md)。

M7.7 的保留规则、两阶段清理和索引墓碑协议见[知识版本清理](m7-retention.md)。

M7.8 的归档对象登记、恢复后副本清理及存储墓碑见[归档副本清理](m7-source-cleanup.md)。

M7.9 的知识库保留参数、策略修订和过期清理预览隔离见[保留策略](m7-retention-policy.md)。

版本：v0.1；本文保留完整目标行为契约。M1–M4 的实际交付范围分别见 [实施记录](progress.md)、[M2](m2-read-tasks.md)、[M3](m3-rag.md) 和 [M4](m4-actions.md)；机器契约在 shared/contracts。以 .api/FastAPI 源及实施记录判定当前可用范围，不能将本文全部示例当成已上线接口。

## 1. 通用约定

M6.4 团队 API 和资源范围、撤权及历史可见性语义见[团队权限契约](m6-teams.md)，机器源为 `api/teams.api`。

M6.5 新任务使用 ticket-v2，增加接手及办结意图；准备参数和事件回执从 `shared/contracts/tools/v1/actions.schema.json` 与 `events/v1/run.schema.json` 生成。历史任务恢复沿用首次 outbox 的 read-v1/ticket-v1/ticket-v2，不升级已保存的计划。精确状态、负责人和确认规则见[业务场景契约](m6-business-scenarios.md)。

- 浏览器前缀 `/api/v1`；Go 内部接口 `/internal/v1`；Python 内部接口 `/internal/v1`。
- JSON 字段使用 snake_case；路径中的 `{id}` 为文档占位，`.api` 使用框架支持的参数语法。
- ID 使用 UUID 字符串，时间使用 UTC RFC 3339。显示时按用户时区转换。
- 金额为精确十进制字符串，另给 ISO 货币码；不通过 JavaScript float 计算结算金额。
- seq、version 为限定范围的非负安全整数，单次 run 事件上限 50,000，超过时受控失败并保留历史。
- 请求头接受 `X-Request-ID`，服务端校验长度/字符并在缺失时生成；内部传递 W3C traceparent。
- M6.3 内部执行接收可选 traceparent，仅接受合法 v00 上下文；随命令持久化，重放保留首值且不改变命令身份。traceparent 不是租户、服务身份或批准凭据。指标仅在 Go 容器 9101/metrics 和 Agent /metrics 提供，不经公开浏览器路由；部署及 metadata 白名单见[M6.3](m6-deployment-monitoring.md)。
- 所有用户输入有长度、枚举、大小及数量限制。额外未知工具参数默认拒绝；HTTP API 的未知字段策略在机器契约中显式定义。
- 成功保留真实 HTTP 语义：读取/修改 200，创建资源 201，接受异步任务 202；业务失败使用对应 4xx/5xx。

成功响应：

```json
{
  "code": "OK",
  "message": "",
  "request_id": "req_example",
  "data": {"run_id": "b33f6b3e-95eb-4a53-8b6e-e33867ca10dd", "status": "queued", "version": 1}
}
```

失败响应：

```json
{
  "code": "APPROVAL_STALE",
  "message": "订单信息已变化，请重新生成方案并确认",
  "request_id": "req_example",
  "data": {"action_id": "086ce8b6-1d73-4e2b-a2bf-45eb8f11249d", "retryable": false}
}
```

列表统一为 `items`、`next_cursor`、`has_more`。`limit` 默认 20、最大 100；游标绑定过滤条件、排序和租户，采用 `(created_at,id)` 稳定排序。导出和评测结果读取独立设限。

## 2. 认证与幂等

### 2.1 浏览器身份

- 登录返回 15 分钟 access token，包含 session_id、subject、tenant_id 和必要版本信息；每个请求仍校验会话和当前权限。
- Refresh Token 为随机不透明值，服务端只存 hash，最长 8 小时；放入 HttpOnly cookie，生产使用 Secure、SameSite=Strict、Path=/api/v1/auth。
- access token 只保存在前端内存，通过 `Authorization: Bearer ...` 传递。刷新接口检查可信 Origin 和固定的客户端请求头，限制跨站请求；刷新令牌逐次轮换并检测复用。
- 本地通过开发代理形成同源访问；本地或受控网络 HTTP IP 演示的 cookie Secure 例外只能在明确 dev 配置下使用。服务器 HTTPS/IP SAN 证书和 Origin 配置见[部署手册](../deploy/server.md)。
- logout、账号停用和角色权限变化需要撤销会话或更新权限修订号；SSE 定期重验会话，失效时关闭。
- 错误 401 在前端单次刷新并重放安全请求；写请求必须复用原幂等键。403 不自动刷新或重试。

### 2.2 写请求幂等

创建会话、发送消息、确认、取消、手工业务写接口携带 `Idempotency-Key`。作用域为 `(tenant_id, actor_user_id, operation, key)`，保存规范化请求摘要与已提交响应。

同键同内容返回已有响应；同键不同内容返回 `IDEMPOTENCY_CONFLICT`。处理中返回 `REQUEST_IN_PROGRESS` 并建议稍后按相同键查询/重试。创建 run 的幂等记录和消息、run、outbox 同事务提交。

普通请求幂等记录保留 30 天；业务 action/回执保留至少 180 天。清理后不保证历史客户端键仍可重放，客户端不得复用旧键发起新业务。Agent 写工具始终以 action_id 作为更强的稳定业务身份。

### 2.3 服务间身份与委托

服务身份使用独立凭据；部署具备条件时使用 mTLS，开发凭据通过环境注入并定期轮换。Go→Python 的 execute/resume/cancel 只接受 Go executor audience，普通用户 token 无法调用。

Python→Go 的工具调用要求服务身份和短期 `X-Run-Capability` 同时有效。能力凭据包含 issuer、audience=go-tools、tenant_id、actor_user_id、run_id、lease_generation、允许的工具 scope、授权修订号、到期时间及 jti。Go 根据自己的 run/session/权限记录复核，不能只验签就执行。

能力凭据由 Go 签发，Python 无签发密钥。续租/能力续期必须匹配仍运行的执行代次；取消、等待、权限撤销或到期后停止新工具调用。服务凭据和能力令牌不写入模型提示、日志或 SSE。

## 3. 浏览器 API

### 3.1 身份与会话

| Method | Path | 行为与权限 |
| --- | --- | --- |
| POST | `/auth/login` | 账号密码登录；租户通过唯一账号关联或受控租户标识解析；限流 |
| POST | `/auth/refresh` | 轮换 refresh cookie，返回新 access token |
| POST | `/auth/logout` | 撤销当前 session，清除 cookie |
| GET | `/auth/me` | 当前用户、租户、角色、权限码 |
| POST | `/conversations` | 创建自己的会话；初始标题可选 |
| GET | `/conversations` | 仅列出自己可访问的会话 |
| GET | `/conversations/{conversation_id}` | 会话元数据及 active_run_id |
| GET | `/conversations/{conversation_id}/messages` | 分页历史消息，包含引用/动作回执关联 |
| POST | `/conversations/{conversation_id}/messages` | 原子保存用户消息并创建 run，返回 202 |

发送消息示例：

```json
{
  "client_message_id": "41358549-7393-4612-91e5-289ab7ae54f2",
  "content": "订单 FD202611020001 一直未送达，请按售后规则创建工单",
  "attachments": [],
  "context": {"order_id": "2388d4bc-1980-40cb-94df-dff2124959a6"}
}
```

context 是业务定位线索，Go 重新验证对象访问。文本最多 8,000 字符；一期会话不接受任意附件上传，attachments 必须为空，知识文件走管理员知识接口。同会话已有非终态 run 时返回 `CONVERSATION_BUSY` 和 active_run_id。

### 3.2 任务与确认

| Method | Path | 行为与权限 |
| --- | --- | --- |
| GET | `/runs/{run_id}` | 任务状态快照、最后 seq、当前消息草稿/最终答复、待输入/待确认 |
| GET | `/runs/{run_id}/events` | SSE；支持 Last-Event-ID 或 after_seq |
| POST | `/runs/{run_id}/inputs` | 回答当前 input_id，保存消息并排队恢复 |
| POST | `/runs/{run_id}/cancel` | 取消；校验 expected_version；返回当前最新状态 |
| POST | `/runs/{run_id}/retry` | 仅失败任务可新建关联 run；重新核对已产生的业务效果 |
| GET | `/runs/{run_id}/approvals` | 当前 run 的确认快照和历史决定 |
| POST | `/approvals/{approval_id}/decision` | 对 pending 确认作决定；必须为发起用户且有动作权限 |
| GET | `/runs/{run_id}/trace` | 返回脱敏节点/工具摘要、耗时、用量与错误分类 |

补充输入：

```json
{"input_id":"a7d6fe99-9008-42b8-ae10-6c13e5c92164","expected_version":4,"content":"是订单 FD202611020001"}
```

人工确认：

```json
{"decision":"approve","expected_version":2,"reason":"已核对订单与客户描述"}
```

decision 枚举 `approve/reject`，reason 最多 500 字。expected_version 指 approval 的 version；input/cancel 的 expected_version 指 run.version。服务端从审批记录取动作参数，客户端不提交可替换的工具参数。版本冲突返回 409，已过期返回 410，并要求重新读取。

### 3.3 订单、工单与审计

| Method | Path | 行为与权限 |
| --- | --- | --- |
| GET | `/orders` | 本租户订单查询；脱敏姓名/联系方式 |
| GET | `/orders/{order_id}` | 订单、商品行、当前版本 |
| GET | `/orders/{order_id}/logistics` | 物流轨迹与来源更新时间 |
| GET | `/tickets` | 本租户授权工单列表 |
| POST | `/tickets` | 手工创建工单；复用 Go 业务规则 |
| GET | `/tickets/{ticket_id}` | 工单详情、状态、负责人、关联订单 |
| POST | `/tickets/{ticket_id}/notes` | 添加内部备注；具备工单访问权限 |
| PATCH | `/tickets/{ticket_id}/assignment` | supervisor/admin 分配，校验负责人属于租户 |
| PATCH | `/tickets/{ticket_id}/status` | supervisor/admin 按合法状态图更新，expected_version 必填 |
| GET | `/tickets/{ticket_id}/events` | 分页工单时间线 |
| GET | `/audit-events` | supervisor/admin 查询脱敏审计 |

Agent 写工具和手工写 API 都进入同一 logic 业务校验，来源分别记为 agent/manual。Agent 路径附加强制 action 审批；普通表单提交由明确操作表达意图。两条路径均需要幂等键、当前授权、预期版本和审计。

### 3.4 知识管理

| Method | Path | 行为与权限 |
| --- | --- | --- |
| GET/POST | `/knowledge-bases` | 读取授权知识库 / admin 创建 |
| PATCH | `/knowledge-bases/{knowledge_base_id}` | admin 以 expected_revision 更新固定角色访问策略 |
| GET | `/knowledge-bases/{knowledge_base_id}/documents` | 文档与当前索引状态 |
| POST | `/knowledge-bases/{knowledge_base_id}/documents` | admin JSON 文本/PDF 上传，创建文档和首版本，返回 202 |
| POST | `/documents/{document_id}/versions` | admin 上传新版本，返回索引任务 |
| GET | `/documents/{document_id}/versions` | admin 读取版本元数据与索引状态 |
| GET | `/documents/{document_id}/versions/{version_id}/pages` | admin 读取已就绪版本的分页提取文本，不返回原 PDF |
| POST | `/documents/{document_id}/activate` | admin 激活 ready 版本，携带 version_id 和 expected_version |
| POST | `/documents/{document_id}/withdraw` | admin 撤回；即时停止可见性，异步清理索引 |
| GET | `/citations/{evidence_id}` | run 所有者读取仍然授权、有效的引用片段 |

M3 支持 UTF-8 Markdown/TXT，经 JSON 提交 filename/content；单文件最多 256 KiB、20,000 字符。M7.1 支持文本 PDF，经 filename/content_base64 提交，content 留空；原文件最多 2 MiB、50 页，提取正文最多 100,000 字符/512 KiB，请求 JSON 上限 4 MiB。扫描件需要先 OCR；异步错误通过版本 error_code 展示，见 [PDF 契约](m7-knowledge-files.md)。每租户 50 个知识库、每库 200 份文档、每文档 100 个版本。文档 expected_version 防止并发激活覆盖，索引回调仅标记 ready/failed，从不自动切换指针，终态结果不可覆盖。历史消息、run 快照与 SSE 回放会隐藏已撤回或失权的政策答复，已下载到客户端的既有数据无法远程删除。文本基线见 [M3](m3-rag.md)。

知识库读写权限由租户成员及固定角色/ACL 决定，不能由上传者在文档文本中定义。

## 4. SSE 协议

浏览器使用 fetch 流并携带 Bearer header；不用 URL token。Go 先校验身份和游标，再发 `text/event-stream`，设置禁缓冲、禁缓存响应头。代理单独配置长连接超时，业务请求的短超时不直接套用 SSE。

```text
id: 12
event: tool.completed
data: {"schema_version":"1","run_id":"b33f6b3e-95eb-4a53-8b6e-e33867ca10dd","seq":12,"type":"tool.completed","occurred_at":"2026-11-02T02:00:00Z","payload":{"tool_call_id":"call_example","tool_name":"order.lookup","summary":"订单状态：运输中","duration_ms":120}}

```

每条事件包含 `schema_version/run_id/seq/type/occurred_at/payload`。id 与 seq 相同；重连后仅返回 `seq > cursor`。Last-Event-ID 优先于 query after_seq，两者格式均需校验；游标大于当前尾部返回 400。每 15 秒发送无 id 的注释心跳。

| 事件 | 关键 payload |
| --- | --- |
| run.queued / run.started | status、run_version、attempt_no |
| step.started / step.completed | step_id、node、简短可见说明、耗时 |
| tool.started / tool.completed | tool_call_id、tool_name、脱敏参数/结果摘要、耗时；写工具附 action_id/receipt_id |
| message.reset | message_id、generation；说明同一草稿开始新生成 |
| message.delta | message_id、generation、chunk_index、text |
| message.completed | message_id、完整 content、citations、action_receipts；完整值覆盖草稿 |
| input.required | input_id、question、choices、expires_at、run_version |
| approval.required | approval_id、action_id、tool_name、结构化确认快照、摘要、expires_at、approval_version |
| approval.decided / action.executed | approval/action ID、决定或业务回执 |
| run.cancelling | status、run_version |
| run.succeeded / run.failed / run.cancelled / run.expired | status、run_version、错误分类或结果摘要 |

文本增量去重按 message_id/generation/chunk_index；事件去重按 run_id/seq。Python 流式生成重启时明确发布 message.reset，避免将重新生成的前缀追加到旧草稿。Go 持久化当前草稿，最终内容及引用以 message.completed 为准。

run 快照返回与同一事务视图一致的 last_event_seq 和 current_message。恢复方式是“加载快照→从该 seq 之后订阅”，避免漏事件。保留期外游标在发送 SSE 头前返回 410 `EVENT_CURSOR_EXPIRED`，前端重取快照。

前端遇到序号不连续先停止应用事件，重连补齐；遇到未知事件类型忽略其展示并刷新快照，不破坏后续连接。终态事件持久化后关闭流。权限撤销或会话过期关闭流并返回可识别的 auth 错误；客户端按身份恢复流程重取快照。

对外事件只输出事实、进度和脱敏工具摘要，不输出模型私有推理、系统提示、服务令牌、完整客户联系方式或原始敏感工具返回。

## 5. Go → Python 执行接口

| Method | Path | 说明 |
| --- | --- | --- |
| POST | `/internal/v1/executions` | 持久化 execute/resume 命令，去重后返回 202 |
| GET | `/internal/v1/executions/{dispatch_id}` | 查询命令接收、执行和检查点状态，辅助超时对账 |
| POST | `/internal/v1/executions/{run_id}/cancel` | 持久化取消意图，合作式停止 |
| POST | `/internal/v1/index-jobs` | 持久化文档索引命令，按 job_id 去重 |
| GET | `/health/live`、`/health/ready` | 存活和就绪 |

执行命令示例（业务上下文不直接携带任意 URL、SQL 或代码）：

```json
{
  "schema_version":"1",
  "dispatch_id":"bbfa231a-399e-4555-a16a-9f9a531583f2",
  "run_id":"b33f6b3e-95eb-4a53-8b6e-e33867ca10dd",
  "conversation_id":"557bfc13-9a9b-498d-b634-a21864bd663d",
  "tenant_id":"79c63f0d-eb3d-44c6-b7cb-408dc2d10735",
  "actor_user_id":"99cb8338-38f5-4931-b8ac-d07657a29176",
  "lease_generation":1,
  "command":"execute",
  "input_message_id":"41358549-7393-4612-91e5-289ab7ae54f2",
  "graph_version":"ticket-assistant-v1",
  "prompt_version":"ticket-assistant-p1",
  "model_profile":"default",
  "resume":null,
  "limits":{"max_steps":20,"max_tool_calls":12,"execution_timeout_seconds":120}
}
```

resume 命令携带已登记的 checkpoint_ref 与 input_id 或 approval_id；Python 从 Go 读取服务端快照并校验，不接受把请求中的任意字符串当作已批准工具参数。graph_thread_id 固定为 `run:{run_id}`；执行代次变化不改变它。

command 枚举 execute/resume；取消使用独立接口。接收去重键是 dispatch_id，重复内容不同返回 409。接收 202 必须在命令持久化后返回；FastAPI BackgroundTasks 单独使用不满足该契约。

## 6. Python → Go 控制与授权接口

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/internal/v1/runs/{run_id}/context` | 当前授权上下文、消息、动作回执和允许工具 |
| POST | `/internal/v1/runs/{run_id}/lease/renew` | 续租并返回允许时刷新的 run capability |
| POST | `/internal/v1/runs/{run_id}/events` | 有序批量事件与状态请求，单批最多 100 条 |
| POST | `/internal/v1/runs/{run_id}/actions` | 准备不可变业务动作，按 action_id 去重 |
| POST | `/internal/v1/actions/{action_id}/execute` | 校验并执行 approved 动作；只提交 ID，不覆盖参数 |
| GET | `/internal/v1/actions/{action_id}` | 返回当前状态和已有回执，处理超时不确定性 |
| GET | `/internal/v1/runs/{run_id}/knowledge-scope` | 返回授权知识库、有效版本、ACL 修订号 |
| POST | `/internal/v1/runs/{run_id}/knowledge/validate` | 批量复核候选 version/chunk 的当前有效性 |
| GET | `/internal/v1/index-jobs/{job_id}/source` | 专用索引 capability 授权读取对应文件 |
| POST | `/internal/v1/index-jobs/{job_id}/complete` | 版本/manifest/激活序号校验后提交索引结果 |

事件回调包含 `lease_generation`、按 run 顺序的 events，以及每条 `source_event_id`、type、payload、occurred_at。source_event_id 是 Python 落盘后稳定 UUID，重发不变化。Go 按顺序分配 seq 并原子保存状态；拒绝旧代次的新事件。

已接受的重复 source_event_id 在摘要一致时返回原 ack，不重复转移状态。不同内容冲突即拒绝。Go 自己产生的审批、动作和取消事件在同一 seq 序列内分配。

## 7. 工具协议

### 7.1 工具清单

| tool_name | 实现 | 必要参数 | 结果 | 确认 |
| --- | --- | --- | --- | --- |
| order.lookup | Go 内部工具路由 | order_no 或 order_id 二选一 | 当前订单、行项目、版本、脱敏客户信息 | 无 |
| logistics.track | Go 内部工具路由 | order_id | 状态、更新时间、轨迹、来源 | 无 |
| ticket.search | Go 内部工具路由 | order_id、可选状态 | 授权工单摘要 | 无 |
| knowledge.search | Python 本地检索 + Go 授权 | query、受限 top_k | 有效证据片段和引用 | 无 |
| ticket.create | Go action executor | order_id、title、description、category、priority、依据引用 | 工单 ID/编号、action 回执 | 必须 |
| ticket.add_note | Go action executor | ticket_id、content、expected_version | 备注 ID、工单版本、action 回执 | 必须 |
| ticket.start_processing | Go action executor | ticket_id、expected_version、reason | 当前 actor 接手、in_progress、工单版本、action 回执 | 必须 |
| ticket.resolve | Go action executor | ticket_id、expected_version、reason | 处理结论备注、resolved、工单版本、action 回执 | 必须 |

读工具统一入口：`POST /internal/v1/tools/{tool_name}/invoke`，body 包含 tool_version、tool_call_id、arguments。tool_call_id 供追踪，不替代写操作 action_id。工具注册与路由 allowlist 对应，不允许按用户提供 URL 发起通用 HTTP 请求。

### 7.2 写动作准备与执行

准备请求含 `action_id/tool_name/tool_version/arguments/evidence`。action_id 由独立的动作编号节点预先生成并在节点结束时随图状态落盘，下一个节点才调用 Go prepare。Go 根据真实业务数据补充对象版本，校验参数，产生规范化快照、摘要和 prepared action/approval。

Python 完成中断检查点后发布 approval.required，Go 原子提升 approval 为 pending 并将 run 置 waiting_approval。若中断落盘但回调丢失，Python outbox 重放；若 prepare 成功但中断未落盘，恢复时复用 action_id，不能生成第二份可执行动作。

审批快照至少含：工具及版本、对象显示信息、即将写入的字段、当前对象版本、依据版本、payload_digest 和过期时间。批准时再次校验权限；执行时再次校验状态、有效期和资源版本。

幂等执行的顺序：先按租户和动作读取已提交回执；若已有，则仅向仍有权限的调用方返回原回执；若无回执，锁定 run/action/approval，检查当前执行代次和许可后提交业务写入。旧代次不能发起新写入。

单一逻辑动作超时后，以 action_id 查询回执或重放，不创建新 action。业务对象变化返回 `APPROVAL_STALE` 并 invalidated；Agent 可重新查询事实，但必须生成新动作并重新确认。

### 7.3 引用结构

```json
{
  "document_id":"2a88451f-a9ce-474e-8da7-5b13b7ab6816",
  "version_id":"cb8368ea-5f11-4908-9237-ab4d35036d35",
  "chunk_id":"67eaa4d5-7a82-4c1a-82fb-d0fca85f2a44",
  "title":"未送达售后处理规则",
  "page":3,
  "excerpt":"核实物流异常后可创建配送异常工单。"
}
```

引用只允许来自本 run 的已验证检索结果。展示时检查文档权限；撤回内容返回受限提示，不继续泄露原片段。索引内容和业务引用共享稳定 UUID，但不共享写权限。

### 7.4 M6.1 检索 profile 与重排

当前 HTTP 路由以生成的 OpenAPI 为准；本文前述表格保留目标设计。M6.1 不新增公开 API 字段，扩展 Agent 内部供应商响应契约，见[语义 RAG](m6-semantic-rag.md)。兼容 embedding 响应及受限重排结果从 Pydantic 生成到 `shared/contracts/retrieval/v1/`。每次召回在 SQL 排名前过滤当前授权版本，重排前后均由 Go 复核证据；模型输出不产生新引用 ID。

### 7.5 M6.2 MCP 适配

MCP 提供三个既有读工具及 `actions.execute`；读参数复用 read.schema.json，执行参数来自 mcp-action.schema.json，均拒绝额外字段。Go lease 上下文的 tools 列表按当前权限返回读/写工具名，MCP 不接受调用者覆写可信身份。执行只提交稳定 action_id，沿用现有 HTTP 动作准备与 Go 审批/事务/回执。默认 HTTP 可配置切换到固定官方 SDK stdio transport；错误 structured_content 仅包含受限 code。生命周期及边界见[M6.2](m6-mcp.md)。

## 8. 错误与重试策略

M7.2 实现的任务运营接口见[任务运营台](m7-run-operations.md)。GET `/api/v1/runs` 默认 own，tenant 需要 run:observe；列表在分页前按查看者当前团队过滤。GET `/api/v1/runs/:run_id/trace` 先后两次授权，中间通过服务身份查询 Python 元数据，不返回提示词、消息或工具正文；Python `/internal/v1/runs/{run_id}/trace?tenant_id=UUID` 仅返回已由 Go 执行上下文绑定的租户/run 观测。没有绑定返回 available=false，服务失败返回 OBSERVATION_UNAVAILABLE（503）。

POST `/api/v1/runs/:run_id/retry` 仅原所有者，要求 Idempotency-Key、expected_version 及当前授权；只接受 failed/expired、无 action、无补充输入的任务。创建关联新 run，不修改原终态；同源最多一个子任务，最多三层重试。RETRY_REQUIRES_REVIEW、RETRY_LIMIT、NOT_RETRYABLE_STATE 为 409；会话/队列及版本约束沿用已有错误。GET run 增加 retry_of_run_id、retry_depth，列表另含 retried_by_run_id、can_retry、retry_block_reason。客户端把新增字段作为兼容扩展；三服务一起升级，源与生成契约仍以 .api / Pydantic 为准。

| HTTP | code | 行为 |
| --- | --- | --- |
| 400 | INVALID_ARGUMENT / FILE_UNSUPPORTED | 修正输入，不重试 |
| 401 | UNAUTHENTICATED / SESSION_EXPIRED | 浏览器刷新一次；内部服务重新建立身份 |
| 403 | PERMISSION_DENIED / CAPABILITY_REVOKED | 停止工具，不扩大权限重试 |
| 404 | RESOURCE_NOT_FOUND | 包含不可访问对象，避免跨租户枚举 |
| 409 | CONVERSATION_BUSY / VERSION_CONFLICT | 读取当前资源，避免并行提交 |
| 409 | IDEMPOTENCY_CONFLICT / REQUEST_IN_PROGRESS | 同键内容冲突需修正；处理中仅同键稍后重试 |
| 409 | APPROVAL_STALE / LEASE_STALE | 原动作失效需重新确认；旧执行器停止 |
| 410 | EVENT_CURSOR_EXPIRED / APPROVAL_EXPIRED | 快照恢复；过期动作不能批准 |
| 422 | EVIDENCE_INSUFFICIENT / OCR_UNSUPPORTED | 补充资料或人工处理 |
| 429 | RATE_LIMITED / BUDGET_EXCEEDED | 按 Retry-After 或终止本 run，遵守总预算 |
| 502/503 | UPSTREAM_UNAVAILABLE / MODEL_UNAVAILABLE | 有界指数退避，写操作先查回执 |
| 504 | EXECUTION_TIMEOUT | 保存失败原因，保留已有业务结果 |

初始网络重试最多 2 次、抖动退避，所有重试计入同一总 deadline 和工具预算。确定性参数错误、权限错误、审批冲突不盲目重试。取消失败与写结果未知分别标记，不能统一描述为“什么也没发生”。

## 9. 契约验收

M1 建立每个 API/事件/工具的机器 schema 和合成样例；生成前端类型与内部客户端；JSON Schema 校验正/反向案例。M2 起加入跨服务兼容测试、幂等重投、越权、乱序/重复事件及旧 lease_generation 测试。所有枚举由契约生成，禁止三端手写不同版本。

## M7.3 OCR 增量契约

PDF 两个上传端点增加可选 ocr_mode，缺省 off，仅允许 off/auto/force；非 PDF 只允许 off。模式参与幂等请求摘要并存入不可变版本，内部 source 返回该模式。版本响应增加 ocr_mode/ocr_page_count；分页文本可选 extraction_method，旧文本页可以不包含此字段。新 OCR manifest 必须有 text/ocr/empty 来源、连续物理页号和至少一个非空 OCR 页；原生 OCR 最多 10 页，旧文本 parser 的回调不能声称 OCR。Go 检查模式、parser、来源、页数和文本总量，冻结后的结果只能一致重放。

包含 OCR 页的版本激活必须增加 confirm_ocr=true；缺省 false，未确认返回 OCR_REVIEW_REQUIRED（409）。当前管理员授权、expected_version、审核审计及激活同事务处理。EvidenceData 的可选 extraction_method 由 Go 逐页派生并保存；模型候选不提供此字段，旧证据缺省为空。撤回后的引用沿用 EVIDENCE_REVOKED（409）等既有权限错误。固定识别错误、预算和手动复核流程见[扫描 PDF OCR](m7-ocr.md)。本阶段没有新增 SSE 类型。

## M7.4 原文件接口

版本元数据增加 source_storage（postgres/s3），source_size 始终表示原文件字节数，归档不会改变版本/hash/index_status。GET `/api/v1/documents/:document_id/versions/:version_id/source` 只允许当前租户管理员，返回标准 envelope，data 包含 filename/content_hash/source_format/content_base64；不返回存储定位。响应 Cache-Control: no-store，PDF 最多 2 MiB，TXT/MD 沿用现有限额。浏览器按附件保存；内部索引 source 端点沿用 job 绑定并通过 Go 读取有界原字节，Python 不持有 S3 凭据。

POST 同一前缀的 `/archive` 返回 202，`/restore-source` 返回 200；均要求 Idempotency-Key，body 为 `{}`，返回当前 VersionData。archive 仅 PDF、配置启用 S3，重复投递复用同代次任务；restore-source 校验完整对象并在事务内恢复数据库字节、递增 source_epoch。当前会话/角色、租户和文档绑定在外部读取前后及幂等重放时检查，成功操作有审计。SOURCE_ARCHIVE_DISABLED、SOURCE_CHANGED 为 409；SOURCE_UNAVAILABLE、SOURCE_INTEGRITY_FAILED 为 503，响应不含供应商异常或原始路径。具体恢复边界见[M7.4](m7-object-storage.md)。未新增 SSE 类型。

## M7.5 PDF 阅读方式

上传可选 pdf_layout（plain/columns/table，缺省 plain），版本元数据与内部 source 返回该不可变选择。非 plain 只接受 PDF，不合法组合返回 INVALID_ARGUMENT（400）。原生 parser 分别为 pypdf-6.21.4-columns-v1、pypdf-6.21.4-table-v1，回调必须与版本选择一致且无 OCR 来源；M7.24扩展auto/force联合行为见下节。非 plain 激活必须有 confirm_layout=true，否则 PDF_LAYOUT_REVIEW_REQUIRED（409）；审核及激活同事务审计。PDF_LAYOUT_UNSUPPORTED 为已持久化索引失败码，禁止自动降级。旧 plain/OCR manifest 与物理页码引用保持兼容，默认上传保留历史幂等摘要形状。预览仍只返回分页文本，不返回结构化单元格；详见[M7.5](m7-pdf-layout.md)。

## M7.24 OCR与版面联合契约

两个PDF上传端点的既有ocr_mode和pdf_layout可组合为auto/force与columns/table；没有新增HTTP字段或SSE类型。两个选择均参与不可变版本及幂等摘要，同键更改任何选择仍返回IDEMPOTENCY_CONFLICT。auto只对无文字页OCR，有文本页保留原生版面；force必须识别全部物理页，仍受10个OCR页限制。实际联合OCR manifest使用pypdf-6.21.4-tesseract-5.5.0-columns-v1或pypdf-6.21.4-tesseract-5.5.0-table-v1，必须含至少一页ocr及完整text/ocr/empty来源，页号连续且与原物理页一致。force不得包含text页；auto没有实际OCR时继续接受原生版面parser，不伪造OCR标记。

Go回调复核模式、对应parser、完整来源、页数及内容上限；首次结果冻结后只允许一致重放，持久错误与成功结果都复用原索引恢复路径。联合OCR版本激活同时要求当前管理员提交confirm_ocr=true及confirm_layout=true，两条审核审计与激活同事务。缺少OCR确认返回OCR_REVIEW_REQUIRED，缺少版面确认返回PDF_LAYOUT_REVIEW_REQUIRED（均409），任何一个确认不能代替另一个。引用和历史消息继续使用Go验证的物理页及extraction_method，撤回沿用EVIDENCE_REVOKED。仅支持有界规则双栏和简单表格行文本，不保证字符准确率、语义单元格或跨页表格关联；限制和固定原生样本见[M7.24](m7-ocr-layout.md)。

## M7.6 账号与角色管理

机器源为 api/accounts.api。/admin/users 支持有界游标列表与创建；PUT /admin/users/:user_id 同时更新名称/启停/多角色，POST 其 reset-password 子路由重置密码；写需 Idempotency-Key，更新需 expected_revision。/admin/roles 返回内置/自定义角色和增量权限目录，POST 创建自定义角色，PUT /admin/roles/:role_id 修改/禁用自定义角色。内置角色只读，自定义权限从目录取并集，账号至少一个内置基础角色。管理读取与重放复核当前授权；版本冲突 409、内置/自身保护及最后管理员保护 409、重复账号名 409，不暴露另一租户的账号信息。账号响应不含凭据，密码 12–72 UTF-8 字节且至少一个字母和数字；HTTP 登录仍兼容历史密码。详见[设计](m7-accounts.md)。

## M7.15 巡检调查 HTTP 契约

GET /api/v1/source-investigations/:object_ref 与 PUT 同路径，当前内置 admin + knowledge:manage；已知版本复用当前知识库 ACL/团队策略（管理员按既有规则可访问全租户团队）。响应 no-store，数据为 object_ref/revision/status/note/updated_by/updated_at/allowed_statuses。无记录的 GET 返回 revision=0、空说明，不能据此创建任意指纹。

PUT 必须有 Idempotency-Key，正文 expected_revision、status、note、confirm=true；首次额外提供租户巡检项 investigation_token 作为 selection_token。令牌15分钟、绑定当前用户/租户/存储/对象，与分页及登记凭证隔离；要求两个巡检开关。既有记录更新可省略令牌且不依赖 S3/巡检开关。note 去除首尾空白后1–2000个Unicode字符，允许换行/制表，不允许其他控制字符。状态为 open/investigating/closed，仅代表跟进进度。保存行、审计与不可变回执同事务，无下游副作用/outbox。

修订不符为 INVESTIGATION_CHANGED（409），同键变参 IDEMPOTENCY_CONFLICT（409）；首次令牌到期 INVENTORY_CURSOR_EXPIRED（410），首次巡检未启用 TENANT_SOURCE_INVENTORY_DISABLED（409），无存储配置 SOURCE_UNAVAILABLE（503），无效说明/状态/凭证400。授权遵守既有401/403/404隐藏规则。已有原请求在30天幂等有效期内同键重放返回原修订，继续复核当前资源权限，后来恢复的版本身份同样受 ACL 约束。不得据调查状态授权登记或删除，详情见[设计与验收](m7-source-investigations.md)。

## M7.26 任务历史批次

[批量归档契约](m7-run-history-batch.md)规定1–10项当前授权预览、全批意图原子登记、不可变成员及逐项原操作核对。所有HTTP定义先维护api/runhistorybatch.api再生成；跨服务执行允许已完成/待核对并存，禁止把逐项恢复描述为整批回滚。

## M7.27 任务历史批量恢复

新增独立的POST /api/v1/run-history-restore-batches/preview及POST /api/v1/run-history-restore-batches；输入结构沿用原批次预览/确认请求，使用独立run.history.restore_batch幂等作用域，不改变M7.26归档请求摘要。统一GET/list/逐项resume支持两种同方向批次，响应增加target=archived/live；不能提交混合方向成员。恢复仍需当前管理员、可信会话、完整成员当前授权及原版本/代次，全部原意图在一个Go事务登记，跨服务逐项核对原操作。只恢复存储，不解除永久执行封存；旧批次重放不覆盖后来单项操作。详见[批量恢复](m7-run-history-batch-restore.md)，实际验收状态见[实施记录](progress.md)。
