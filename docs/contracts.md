# 接口与跨服务契约

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
