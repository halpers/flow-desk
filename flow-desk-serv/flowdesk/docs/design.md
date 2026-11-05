# Go 业务服务详细设计

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
## M7.5 PDF 阅读方式与审核

后续 M7.7 清理流程见[版本保留与清理](../../../docs/m7-retention.md)：文档排他锁串行化激活/上传/清理，事务内复核当前授权与保护条件，原子写入 cleanup_pending、审计及 knowledge.cleanup outbox。Agent 调用在事务外；索引删除确认且投递租约有效后，清除数据库原文并置 purged。失败持续退避重试，间隔上限 30 秒。归档历史版本不可清理。

Go 保存不可变 pdf_layout，验证文件/OCR 组合、上传幂等及索引 parser/来源绑定，不在业务服务中解析 PDF。只有当前租户管理员显式提交 confirm_layout 后才可激活非 plain 版本，knowledge.layout_review 与文档更新同事务。预览与物理页码证据、撤回、S3 归档沿用现有授权边界。迁移、回调和旧请求兼容见[M7.5](../../../docs/m7-pdf-layout.md)。
