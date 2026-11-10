# Go 业务服务详细设计

M7.26 [任务历史批量归档](../../../docs/m7-run-history-batch.md)把最多10个当前授权候选的原维护意图、不可变批次/成员及审计同事务登记；业务逻辑复用M7.23的prepare/Resume，不在数据库事务内调用Agent。预览最多5路并行只读观察；已登记键恢复不依赖Agent。单项继续前后复核完整批次，单项事务继续执行自己的当前授权、载荷原子移动及原回执协议。读取与分页不返回部分无权清单，批次总状态由原操作推导。

M7.23 `runhistory` 用例先在当前管理员、任务/团队可见性和保留条件事务中保存原申请/幂等身份，再事务外调用Agent；收到严格匹配回执后重检当前身份及Go条件，同事务迁移消息/事件载荷、完成审计和操作回执。RunHistoryOperationsModel及两类冷表模型各自持有SQL；原消息/来源事件/SSE查询使用透明热冷视图，身份、序号、时间与hash保持。永久run/载荷围栏及共享执行授权阻止旧能力、回调和重试，恢复不解除。失败保留pending，由当前有权管理员使用原操作核对；见[任务历史设计](../../../docs/m7-run-history.md)。

M7.21 runlifecycle用例在内部观测前后复用当前任务授权，RunsModel集中持有只读关联统计。严格内部协议、任务version隔离、最终身份检查及no-store保证失败不返回部分数据；没有新迁移、幂等写或删除端点。详见[生命周期设计](../../../docs/m7-run-lifecycle.md)。
M7.20增加同调查1–50个严格递增修订的服务端预览和原子批量归档；预览非授权凭证，提交重检当前身份/资源/修订和每项目标资格。批量独立幂等作用域，一条审计/回执，任一目标或提交失败整批回滚，无新迁移或外部IO，见[批量设计](../../../docs/m7-investigation-history-batch.md)。

M7.19增加受当前管理员/资源授权及expected_revision保护的单快照归档。对象锁+父FOR UPDATE下同事务复制完整原快照、移出热表、审计、最终复核及回执；延迟约束阻止只复制未移出的提交。历史FOR SHARE保证跨层分页和标记一致，恢复联合单行读取，当前调查不变，无外部IO。迁移与模型/测试见[分层归档](../../../docs/m7-investigation-history-archive.md)。

M7.18 investigationrestore 用例在原幂等/当前授权/对象锁事务内读取本租户旧快照，使用现有UpdateNoteTx和捕获触发器保存新修订，恢复审计保留来源与新修订。普通保存共享锁序；新写与旧回执均当前授权，任何失败返回零结果。无新迁移或外部I/O，见[恢复设计](../../../docs/m7-investigation-restore.md)。

M7.17 调查修订由迁移中的数据库触发器在现有Go保存事务内自动捕获，无重复的应用层追加。SourceInvestigationRevisionsModel由goctl生成/初始化，custom model持有首修订及有界列表查询；investigationhistory用例按当前身份/资源→调查FOR SHARE→查询→最终身份→读取审计执行。页游标固定首次修订上界且按最后已读修订推进，不返回私有定位；全部失败清空结果。迁移锁等待旧写者，旧最新值作为基线，详情见[编辑历史](../../../docs/m7-investigation-history.md)。

M7.16 sourceinvestigationlist 用例与 SourceInvestigationsModel.ListPageTx 提供当前租户按状态的稳定分页。默认20/最大50，只查 limit+1 元数据；逐项复用调查资源授权，游标按已扫描行推进，绑定当前用户/租户/筛选与原15分钟期限。管理员、最终会话复核及只读审计使用同一事务，失败清空整页，无外部 I/O。迁移2026110603仅增加索引，详见[工作台设计](../../../docs/m7-investigation-worklist.md)。

M7.15 SourceInvestigationsModel 通过隔离 schema/goctl 生成并在 ServiceContext 初始化；sourceinvestigations 用例统一复核内置管理员、会话和当前资源授权。首次使用租户巡检签发的独立加密选择凭证，后续按可信记录定位授权，可在暂停巡检后跟进。幂等键→身份/资源→对象事务锁后比较 expected_revision；说明、审计和回执同事务，无 S3 I/O 或 outbox。回执授权失败返回空数据，详见[巡检调查设计](../../../docs/m7-source-investigations.md)。

M7.14 sourcehistory 用例通过 SourceObjectsModel 查询热/历史投影，SourceObjectArchivesModel 保留完整登记身份。归档、热行删除、审计和回执使用同一事务；不调用外部存储。生成器的基础 Insert 会省略 created_at，因此自定义 InsertTx 显式写入原始创建时间，保持分页位置；原生成文件不手改。所有登记写者在版本锁下检查两表，旧 cleanup/adoption 回执保持当前授权。详见[副本历史](../../../docs/m7-source-history.md)。

M7.13 tenantsourceinventory 增加内置租户管理员的只读对象发现。ListTenantObjects 的固定前缀仅来自当前可信 tenant，复用原有有界 S3 列举协议。document_versions 使用不含原文的身份投影，source_objects 按租户/存储/当前页 keys 查询；已知文档继续锁定并检查库 ACL/团队。外部 I/O 前后验证当前身份，后置资源检查和页面审计同事务。无新数据库迁移、业务写入或 Python 接口，详见[租户巡检](../../../docs/m7-tenant-source-inventory.md)。

M7.12 sourceadoption 用例复用现有登记模型和幂等事务。巡检为当前有资格的标准旧副本签发与分页游标不同作用域的 AES-GCM 选择凭证。首次幂等事务可直接返回已提交回执；新请求复核后以内部准备信号回滚，退出所有数据库锁再读取 S3；最终幂等事务重新复核并提交登记/审计/回执。事务内无外部 I/O，成功重放不再读 S3，仍校验当前知识库权限与登记身份。新登记要求 FILE_STORE_ADOPTION_ENABLED 和 FILE_STORE_INVENTORY_ENABLED，独立于清理；详情见[人工登记](../../../docs/m7-source-adoption.md)。

M7.11 新增可选 SourceInventory 适配器与 sourceinventory 用例。S3 ListObjectsV2 固定可信版本前缀，单次 limit+1、最多 51 对象及 256KiB 响应，继承 5 秒超时、不跟随重定向、不重试；严格检查范围、编码、顺序、重复及元数据。游标 AES-GCM 加密认证并绑定作用域，源代次改变失效。source_objects 模型仅查询当前页登记；逻辑在 I/O 后事务复核权限与原件，审计成功才返回。无对象写入、自动认领或修复；配置、协议边界见[巡检设计](../../../docs/m7-source-inventory.md)。

M7.10 增加授权完整版本历史与 purged 详情归档；API 源为 api/knowledge.api，模型层分别持有 document_versions 查询/CAS 和 version_metadata_archives 写入/读取。业务层按文档→权限范围→版本顺序锁定，归档详情、热行精简、审计和幂等结果使用同一非空事务。完整历史以租户/文档/创建时间/版本 ID 定位，每页最多读取 51 条判断 has_more，不加载原文或存储定位。具体边界与回退见[跨服务设计](../../../docs/m7-history-archive.md)。

M7.4 的原文件适配器位于 internal/client/sources.go，归档/下载/恢复用例由 internal/logic/knowledgesources.go 编排。Go 是唯一存储凭据持有者，Python 和浏览器通过已有/新增授权 API 读取。数据库字节与 S3 定位的切换、代次和外部事务边界见[跨服务数据模型](../../../docs/data-model.md)及[M7.4](../../../docs/m7-object-storage.md)。

框架：go-zero；模块根：`flow-desk-serv/flowdesk/`。Go 默认脚手架已生成，本文业务模块、接口和数据库部分属于待实现规格。

## 0. 已生成工程与初始化方式

2026-11-02，在 `flow-desk-serv/` 执行用户指定命令：

```sh
goctl api new flowdesk
```

生成器版本 goctl 1.10.2，Go module 版本 Go 1.27.1；依赖固定 go-zero 1.10.3，并已补齐 go.sum。保留生成器文件头及 handler/logic/svc/config/types 默认分层。配置端口调整为设计约定的 8080。

当前 `.api` 只有 `/from/:name` 演示路由，logic 尚无实际业务处理；现有可执行入口不表示 M1～M5 已完成。后续在该工程上扩展 API 再生成，不手写另一套框架骨架。

## 1. 模块职责

| 模块 | logic 分组 | 主要依赖 |
| --- | --- | --- |
| 身份会话 | auth | UsersModel、SessionsModel、密码哈希、权限解析 |
| 会话消息 | conversation | ConversationsModel、MessagesModel、RunsModel |
| 任务控制 | run | RunsModel、RunAttemptsModel、RunEventsModel、OutboxEventsModel |
| 人工确认 | approval | ActionsModel、ApprovalsModel、ActionReceiptsModel |
| 订单物流 | order | OrdersModel、OrderItemsModel、LogisticsEventsModel、适配器 |
| 工单办理 | ticket | TicketsModel、TicketNotesModel、TicketEventsModel |
| 知识元数据 | knowledge | 知识库/ACL/文档/版本/索引任务模型、文件适配器 |
| 内部工具 | tool | 参数验证、scope、对应业务 logic 的无状态函数 |
| 审计 | audit | AuditEventsModel、脱敏映射 |
| 调度 | run 内的调度 use case | Outbox、租约、PythonClient、时钟与配置 |

公共数据库实体和 API DTO 分开。生成表模型放 `db/model/`；HTTP DTO 来自 `.api`；领域状态/纯映射放 `internal/domain/`，实施时按需创建。共享函数不形成包揽多表的 Store/Repository。

## 2. go-zero 分层

```text
HTTP route
  → middleware（认证、可信上下文、trace、限流）
  → handler（解析和输出）
  → logic（用例、权限、事务、错误映射）
  → ServiceContext 中预初始化的 table models / clients
```

ServiceContext 计划字段包括 Conn、各表 Model、PythonClient、LogisticsClient、FileStore、CapabilitySigner、PermissionResolver、Clock 和配置。构造函数负责连接/依赖初始化；不实现“创建工单”或“批准并执行”等工作流。

handlers 使用 context 传递请求取消与超时；后台 worker 使用服务生命周期 context 和持久化任务 deadline，不能继承已结束的浏览器请求 context。

## 3. API-first 与权限资源

后续将 `flowdesk.api` 演示定义替换为业务顶层定义，并导入 `api/auth.api`、`conversation.api`、`run.api`、`approval.api`、`order.api`、`ticket.api`、`knowledge.api`、`internal.api`。这些业务片段由 M1 创建，当前 api 目录为空骨架。

接口先在 `.api` 声明，使用稳定全局唯一 `@handler`。沿用参考项目的机器可解析 doc 风格，例如：

```text
@doc "创建工单 | permission=ticket:create | permission_name=创建工单 | permission_type=action | auth=required"
```

公共登录显式 `permission=- | auth=public`；内部路由显式 `auth=service` 或 `auth=run_capability`。生成权限映射时校验注解缺失/冲突，固定角色引用权限码，不另维护一套手写路由清单。

对外 API 中间件：RequestID/Trace → JWT 验证 → Session 校验 → TrustedTenant/Actor 上下文 → 权限码校验 → logic 对象级授权。内部执行回调/工具另设服务身份与 capability 校验，不与用户 JWT 混用。

现有 API 的校验与后续再生成命令（从模块根执行）：

```sh
goctl api validate --api flowdesk.api
goctl api go -api flowdesk.api -dir .
```

OpenAPI 导出、前端类型与 Python 工具客户端的具体命令由 M1 对锁定 goctl 版本验证后脚本化。禁止在设计阶段复制未经验证的生成产物。

## 4. 主要用例实现

### 4.1 SendMessage

1. 校验输入和幂等键，读取当前 session/tenant/actor。
2. 在事务内检查会话归属及归档状态；生成 message/run ID。
3. 写用户消息和 queued run，数据库活动 run 唯一约束处理并发冲突。
4. 写 run.queued 事件并更新 last_event_seq，写 run.execute outbox。
5. 同事务保存幂等响应；提交后返回 202。

如果在提交后 HTTP 响应丢失，客户端复用原键获取原 run；不能由 handler 再调用模型生成一个新任务。

### 4.2 PrepareAction / PublishApproval

PrepareAction 检查工具 scope、参数和引用有效性，从数据库加载真实对象版本，规范化快照，插入 action/approval 的 prepared 记录。相同 action_id 和摘要返回原记录，不同摘要冲突。

Python 中断 checkpoint 保存后发送 approval.required；Go 在同一事务将 prepared 变为 pending、run 变为 waiting_approval、保存 checkpoint_ref 和事件。该事件必须来自当前执行代次。已处于同一 pending 状态的重复事件返回原 ack。

### 4.3 DecideApproval

按 run→action→approval 锁顺序校验：本人发起、仍有权限、pending、expected_version、未到期、摘要未变。approve 保存决定，将 run 置 queued 并写 resume outbox；reject 保存拒绝并将 run 终结 cancelled。两种均保存事件和审计。

决定成功不表示业务已执行，前端继续展示“已确认，等待办理”。业务动作完成以 action_receipt 为依据。

### 4.4 ExecuteAction

1. 验证服务身份和 run capability，按 action_id 查询已有回执。
2. 已执行时复核读取权限并返回原结果。
3. 未执行时开始事务，按统一顺序锁 run/action/approval/业务对象。
4. 检查 running 状态、lease_generation、发起用户当前权限、批准快照/期限及业务对象版本。
5. 调用各表 Model 的 Tx 方法写工单或备注、写回执、审计、工单时间线与 run 事件。
6. 所有写操作使用同一个 sqlx.Session；检查受影响行数后提交。

发生工具响应超时时，Python 重试同一 action；Go 不在模型请求里重新猜测是否已写入。业务写后模型总结失败时，run 可以 failed，但任务快照仍明确展示已成功创建的工单。

### 4.5 CancelRun

取消事务与动作事务竞争同一 run 锁。queued/waiting 直接 cancelled，running→cancelling；使未执行审批失效，写 cancel outbox。异步停止确认后再终结。已执行回执保持不变。

### 4.6 IngestAgentEvents

校验代次与 source_event_id。重复事件摘要一致返回已有 seq；新事件在锁定 run 后验证合法状态转移。message.delta 同步更新草稿，message.completed 固定最终答复。run.succeeded 必须检查声明的写操作存在真实回执，不能由任意文本完成证明。

## 5. 调度器和 Outbox

调度器可以与 Go HTTP 运行在同一进程，使用独立 goroutine 和优雅退出控制。后续拆 worker 进程复用同一代码，不增加新的业务服务。

基本循环：小批量 SKIP LOCKED claim →提交 lease→并发受限投递→记录接收结果/下一次可用时间。单批初始 10、轮询间隔 500ms、Go→Python 接收请求超时 5s；这些是待压测配置。

run 调度首次领取时分配 lease_generation 和 dispatch_id；相同命令重投保留两者。Go→Python 202 只证明持久化接收，运行进度由心跳和事件推进。失联时先查询 execution 状态并请求停止旧代次，满足执行隔离要求后才产生新恢复代次。

租约恢复不得让调度线程无限等待网络。重试和对账都有最大次数，超过后进入 failed/dead_letter 并暴露可诊断错误。工作认领和业务事务都保持短事务。

## 6. SQL 模型接口示例

以下是命名设计，具体签名由生成类型确定：

| 所属 Model | 自定义操作 |
| --- | --- |
| RunsModel | FindOneByTenant、FindOneForUpdateTx、UpdateStatusTx、UpdateLeaseTx、UpdateEventSequenceTx |
| RunEventsModel | InsertIfAbsentTx、ListAfterSequence |
| ApprovalsModel | FindOneForUpdateTx、UpdateDecisionTx、UpdateInvalidatedTx |
| ActionReceiptsModel | FindOneByAction、InsertTx |
| TicketsModel | InsertTx、FindOneForUpdateTx、UpdateStatusTx、UpdateAssignmentTx |
| OutboxEventsModel | ListClaimableForUpdateTx、UpdateClaimTx、UpdateDelivered、UpdateRetry |

每个表模型只操作本表。跨表 JOIN 的只读投影放在查询主表的 custom model；跨表写协调留在用例 logic。禁止提供让 logic 传任意 SQL 的通用 ExecTx/QueryTx。

## 7. 数据迁移与模型生成

M1 需建立 `db/migrations/`、`model-tables.conf`、`goctl-model.sh`、`goose-up.sh` 和 `goctl.yaml`。生成脚本要求 psql/goose/goctl，完整工作如下：

1. 校验连接属于明确开发环境，创建唯一随机临时 schema。
2. 用 goose 应用所有迁移，并配置可预测的 search_path。
3. 从表清单生成普通模型；一期默认普通模型，缓存模型仅在有明确一致性方案时启用。
4. 保持 NUMERIC/BYTEA 类型映射，审查唯一键、部分索引和自定义扩展差异。
5. 正常和失败退出均清理临时 schema，不触碰已有业务 schema。

Go 的 business schema 迁移与 Python schema 独立。运行期账号没有创建/删除 schema 权限。初始 schema 数据需明确区分合成演示种子与实际生产初始化。

## 8. SSE 与网关行为

使用 go-zero 路由承载专用流式 handler。实施时验证锁定版本的 SSE/timeout 配置；不能直接套用普通 JSON 响应包装器。handler 校验 auth/cursor 后写流头，循环查询已提交事件，设置心跳和断开检测。

每次批量读取限制数量和累计字节，慢客户端超过缓冲上限时断开并允许补拉；不会阻塞整个 run 执行。连接 context 结束仅停止读取。定期检查会话授权，撤权后关闭连接。

反向代理关闭流缓冲，独立配置读超时；普通 API 有短 timeout，模型执行通过异步任务 deadline 控制。SSE 到期断开需自动重连与快照恢复。

## 9. 配置与依赖

| 配置组 | 项目 |
| --- | --- |
| HTTP | Host、Port、普通请求 timeout、SSE 心跳/连接限制 |
| Database | DSN、pool 上限、连接寿命、statement timeout |
| Auth | JWT issuer/key ID、access/refresh TTL、cookie、会话校验 |
| Capability | 签名密钥引用、audience、TTL、工具 scope |
| AgentClient | 内部 endpoint、服务身份、接收 timeout |
| Scheduler | 批量、并发、轮询、lease TTL、重试、dead-letter |
| Storage | 本地根目录或对象存储配置、文件大小/MIME/页数 |
| Limits | 用户/租户并发、任务预算、分页、事件保留 |
| Telemetry | service name、trace exporter、脱敏、采样与指标开关 |

YAML 的环境插值需显式启用并验证 go-zero 配置加载选项；文档不得暗示任意 `${VAR}` 会自动展开。启动检查缺失配置并脱敏报错。

一期进程内令牌桶可保护单副本接口，登录失败计数和会话由数据库持久化；启用多副本前需补集中式额度或网关限流，避免每副本独立额度被放大。

## 10. 验证与交付

- logic 单元测试：状态转移、当前权限、过期确认、预算和错误映射。
- 数据库集成：真实 PostgreSQL 隔离 schema，验证行锁、唯一约束、共享 session 回滚和幂等。
- 契约测试：Go 工具结果满足 JSON Schema，生成接口/客户端无差异。
- 故障测试：提交后响应丢失、旧代次回调、确认/取消竞态、SSE 重放、模型总结失败但业务成功。
- 实施后运行 gofmt、go test、go vet；依赖数据库的套件显式启用并隔离，报告实际执行结果。

代码实现与验收顺序遵循 [质量与交付计划](../../../docs/quality-and-roadmap.md)。
## M7.9 保留策略修订

M7.9 的[知识库保留策略](../../../docs/m7-retention-policy.md)由 KnowledgeBasesModel 保存三个独立字段；新库 InsertTx 使用数据库默认策略，更新在知识库排他锁下复核当前 ACL/团队/会话并按策略修订 CAS，审计和幂等结果同事务保存。预览及版本清理持共享锁读取策略，旧预览拒绝，新字段零值省略保留旧请求摘要。已确认清理继续使用既有 outbox/Agent 协议，不跨写或重置索引。

## M7.8 原文件副本清理

M7.8 的对象登记和清理见[归档副本清理](../../../docs/m7-source-cleanup.md)。SourceObjectsModel 在 ServiceContext 初始化，SQL 由模型负责；归档先登记带 epoch 的对象身份再上传。清理请求在当前授权、PG 原件完整性及代次校验下，同事务登记状态/审计/outbox；worker 在事务外执行受条件写保护的零字节替换，提交前验证租约。默认关闭，桶版本控制或兼容能力不满足时拒绝操作，不提供任意 key 删除接口。

## M7.5 PDF 阅读方式与审核

后续 M7.7 清理流程见[版本保留与清理](../../../docs/m7-retention.md)：文档排他锁串行化激活/上传/清理，事务内复核当前授权与保护条件，原子写入 cleanup_pending、审计及 knowledge.cleanup outbox。Agent 调用在事务外；索引删除确认且投递租约有效后，清除数据库原文并置 purged。失败持续退避重试，间隔上限 30 秒。归档历史版本不可清理。

Go 保存不可变 pdf_layout，验证文件/OCR 组合、上传幂等及索引 parser/来源绑定，不在业务服务中解析 PDF。只有当前租户管理员显式提交 confirm_layout 后才可激活非 plain 版本，knowledge.layout_review 与文档更新同事务。预览与物理页码证据、撤回、S3 归档沿用现有授权边界。迁移、回调和旧请求兼容见[M7.5](../../../docs/m7-pdf-layout.md)。

## M7.24 联合OCR的可信来源与审核

PDF的auto/force可与columns/table联合，迁移2026110902只放开对应CHECK约束。两个选择参与上传幂等，内部source传递原选择；Go不接受浏览器或模型提供parser/页内容。回调必须匹配联合parser和完整text/ocr/empty来源，force不得混入text页，实际OCR/空白处理不超过10页；原生auto和旧parser继续接受既有形状。首次成功/错误冻结、索引代次与一致重放保持。实际联合OCR版本同时要求当前管理员confirm_ocr与confirm_layout，两项审计和激活同事务，失败全部回滚；引用仍由当前ACL和不可变物理页复核，撤回后拒绝。完整有限支持与回退边界见[M7.24](../../../docs/m7-ocr-layout.md)。

M7.27[批量恢复](../../../docs/m7-run-history-batch-restore.md)复用现有批次与原维护操作，新增独立恢复入口及幂等作用域，不改变旧归档请求摘要。全成员当前授权、全部原意图原子登记、逐项跨服务核对与原回执恢复保持；延迟约束拒绝混合目标，恢复不解除永久执行封存。

M7.28[自助改密](../../../docs/m7-self-password.md)新增SessionAuth本人认证端点。可信会话/原密码预检查后，bcrypt在事务外完成，提交按租户授权排他锁和用户锁复核原凭据、auth_version及精确活跃session；密码/版本、全会话撤销及固定审计同事务。不存密码指纹或幂等摘要；独立可信账号和全局内存限流，固定禁用原生请求正文日志。

M7.29以AuthRateLimitsModel的自有表方法和logic事务实现共享认证GCRA：固定全局桶→可信账号桶锁序、DB时间、原子双桶消耗、有界回收及2秒期限。登录handler调用逻辑后才认证；本人改密验证当前可信身份后、bcrypt前消耗。ServiceContext只初始化模型；删除原内存limiter。详见[专项设计](../../../docs/m7-auth-rate-limits.md)。

M7.30[定时归档](../../../docs/m7-run-history-schedule.md)以RunHistorySchedulesModel持有策略、领取及CAS SQL，RunsModel持有单候选/保留期查询。独立5秒调度循环不阻塞既有业务outbox；默认总开关false，每轮30秒上限且只领一个策略。后台专用grant由数据库策略/租约构造，不修改通用HTTP身份校验；每次Agent调用前后复核启用、修订、token/期限、当前owner权限/auth_version和任务/团队。复用prepareHistoryAuthorizedTx/resumeRunHistory的原保护与原子载荷迁移，意图及策略绑定同事务，失败只继续原操作。

M7.31[调查自动回待办](../../../docs/m7-investigation-automation.md)的GET/PUT源为api/investigationautomation.api。Go拥有策略表、持续授权和全部自动流转事务，SourceInvestigationsModel扩展有界候选和资格核对，worker仅调用logic。现有快照触发器捕获新修订，scheduled审计与策略进度原子提交；不创建HTTP身份，不调用Agent或S3。
