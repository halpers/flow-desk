# 数据模型与一致性设计

M7.20无新迁移，批量归档复用两层历史/完整原件及延迟提交约束。原模型增加可信租户/调查及1–50修订的有界选择读取，父行FOR SHARE预览、FOR UPDATE整批移表与历史读取互斥，批量审计/唯一回执同事务，当前调查不变，详见[批量归档](m7-investigation-history-batch.md)。

M7.19迁移2026110605引入Go自有source_investigation_revision_archives，完整原快照移表并保留UUID/修订/内容/原操作者时间，追加归档操作者时间。数据库验证精确原件和同事务热行移出；热UPDATE/任意删除及两层TRUNCATE继续受保护，冷快照不可变。M7.17的热历史不可删除规则仅增加精确移表例外；历史/恢复联合读取、父行锁隔离、整库备份和旧读者回退限制见[分层归档](m7-investigation-history-archive.md)。

M7.18 无新迁移。恢复从source_investigation_revisions按可信租户/调查/源修订单行读取，source_investigations在普通对象锁下比较expected_revision并保存revision+1，由已有触发器追加快照。来源与新修订由knowledge.source_investigation_restore审计记录，回执operation独立于普通编辑；同事务及同键授权恢复不重复效果。所有旧快照保留，见[历史恢复](m7-investigation-restore.md)。

M7.17 迁移2026110604增加 Go 自有 source_investigation_revisions，按租户/调查/修订唯一，同租户外键关联调查和操作人。旧最新行复制为基线，原操作人/时间保持；迁移写锁覆盖基线与触发器安装，之后每次INSERT/UPDATE在同事务自动捕获。快照不可覆盖/删除/截断，调查身份固定、修订逐次加一；原保存、审计和幂等回执失败时一起回滚。读取按锁定上界分页并复核当前授权，所有表/约束/触发器需一致备份，详见[历史设计](m7-investigation-history.md)。

M7.16 迁移2026110603只新增 source_investigations 的 (tenant_id,created_at DESC,id DESC) 与 (tenant_id,status,created_at DESC,id DESC) 两个分页索引，不改表字段。custom model 仅投影授权所需私有 key 和列表元数据，不读说明。当前授权、分页与脱敏只读审计同事务；创建时间/ID保持不变，状态筛选不是快照，详见[调查工作台](m7-investigation-worklist.md)。

M7.15 迁移 2026110602 增加 Go 自有 source_investigations。tenant_id/object_ref 唯一；私有 store_id/object_key 与创建身份固定，最新说明和状态通过期望修订更新，同租户创建/更新人外键。单对象事务锁、审计和幂等回执保证不覆盖、不重复提交；没有下游动作或 outbox。每次用可信定位的版本线索核对当前资源授权，身份线索不证明归属。调查、回执、审计和用户身份须纳入同一数据库备份；无自动删除或永久编辑历史，见[调查设计](m7-source-investigations.md)。

M7.14 迁移 2026110601 增加 Go 自有 source_object_archives，保留退休登记的全部原字段并增加 archived_by/archived_at。归档事务先锁文档、授权、版本和对象，只移动非当前代次且清理投递已完成的 retired 行；插入历史表、删除热行、审计和回执原子提交。身份/定位查重、旧回执和存储巡检联合读两表，日常副本预览仅读热表。完整历史用不变 created_at/id 分页。两表和回执、审计须同一备份；不删除 S3 防重写标记，详见[完整副本历史](m7-source-history.md)。

M7.12 无新迁移。手工登记复用 Go 自有 source_objects 与 api_idempotency/audit_events：按文档→ACL/团队→版本→对象登记锁序复核资格，原子插入旧副本登记、knowledge.source_adopt 审计和回执。读取 S3 前退出首个检查事务，完成有界大小/SHA256 核验后新事务重查，不保留跨请求锁或私有正文。副本 ID 沿用版本 namespace + 存储指纹/key 的确定性 UUID，唯一约束和当前版本锁防止重复效果；副本使用旧代次，数据库原件代次/内容/定位不变。回执在对象后来 retired 后仍保留，不能通过删除登记或回执撤销认领，见[登记设计](m7-source-adoption.md)。

M7.11 不增加表或迁移。Go 在当前授权事务内按 tenant_id/version_id/store_id 和本页键集合查询 source_objects，将外部列表元数据与登记记录对账；成功页面写入 knowledge.source_inventory 审计，仅保存对象版本、数量和未登记计数。外部请求不持数据库锁，结束后重新锁定版本及权限范围并复核 source_epoch/当前定位。未登记对象不自动补写账本，失败或撤权结果不发布，不修改原件、清理状态或 Python 索引。详见[巡检设计](m7-source-inventory.md)。

M7.10 迁移 `2026110507_metadata_archive.sql` 增加 Go 自有 version_metadata_archives（版本/租户/文档身份、文件名、parser、错误码、片段数、OCR/版面、归档人/时间），并在 document_versions 增加 metadata_archived。归档表复合外键指向原版本及当前租户用户；归档事务先锁文档、ACL/团队、版本，再复核资格、插入详情、精简热行和记录审计/幂等回执。热行约束要求归档后为 purged，且文件名/parser/错误码为空、片段数归零、OCR/版面为默认值；原租户/文档/版本/job/hash/创建时间、source 元数据及清理状态不变。

热查询用 NOT metadata_archived 过滤及部分索引；完整历史由原版本按 created_at/id 定位，并在同一 Go 授权事务内左联归档详情。归档时间与原创建时间独立，翻页顺序不因归档改变。回执、引用、审计、Python 墓碑和 source_objects 不迁移或删除；身份锚点和历史表仍会增长，此功能不等于数据库压缩或完整生命周期数据擦除。归档表须纳入同一数据库备份，禁止 Down 删除后再回退旧应用。

M6.5 的 `2026110403_business_scenarios.sql` 只扩展 actions 的工具 CHECK，不新增跨服务表。接手在一个事务内更新负责人、状态及 version；办结同事务写入带 source_action_id 的处理结论、状态、事件、审计及唯一回执。恢复图版本读取 Go 首次执行 outbox，Python 继续拥有检查点；不可变历史保留，迁移 Down 不删除已经产生的新动作。

M6.4 新增 `teams`、`team_members`、`run_ticket_resources`，在 `tickets` 和 `knowledge_bases` 增加可空的 `team_id`。成员与资源采用 `(tenant_id,id)` 复合外键；成员变更更新团队 revision，资源范围变更更新工单 version 或知识库 acl_revision。历史回填与授权锁语义见[团队设计](m6-teams.md)。固定角色仍存于 users.role，自定义多角色尚未实现。

版本：v0.1；本文描述完整逻辑模型及约束。M1–M4 已实现身份、会话/任务、知识版本/证据、动作/审批/输入、工单/备注/事件、回执和审计迁移；users.role 保存单一固定角色，扩展多角色关联表留待团队权限专项。完整字段以有序迁移为准，实际范围见[实施记录](progress.md)与 [M4 约束](m4-actions.md)。下文包含尚未实现的完整目标，不应直接当作当前表结构使用。

## 1. 数据库与公共字段

PostgreSQL 单实例分 `business`、`agent`、`knowledge` 三个 schema；Go、Python、迁移程序使用不同角色。跨服务通过 API 获取对方事实，不通过数据库 JOIN 绕过授权。需要扩展到不同数据库时维持相同 API 边界。

Go 所有表采用单列 UUID 主键 id；租户表另设 `UNIQUE(tenant_id,id)`，供同租户复合外键使用。tenant_id 非空并指向 tenants；所有租户对象关系使用 `(tenant_id,object_id)` 外键，阻止跨租户关联。避免将复合主键支持问题留给 goctl 生成器。

公共字段：`created_at/updated_at TIMESTAMPTZ`；并发修改对象加 `version BIGINT`；枚举用受 CHECK 约束的 TEXT 并在代码中使用命名常量。业务编号与主键分离；敏感业务字段按最小需要保存及脱敏。

金额使用 `NUMERIC(20,2)` 和货币码；本例使用两位小数货币，增加其他币种前扩展金额精度规则。API 返回精确字符串。状态时间以数据库时钟比较，避免多 worker 本地时钟偏差。

软删除只用于确需保留引用的实体。会话关闭、文档撤回等使用明确状态，不用一个 deleted 标记隐含所有业务语义。

## 2. 身份与授权表（Go）

| 表 | 关键字段 | 约束/查询 |
| --- | --- | --- |
| tenants | code、name、status | code 全局唯一 |
| users | tenant_id、account、password_hash、display_name、status、permission_revision | `(tenant_id,account)` 唯一；状态变化更新权限版本 |
| roles | tenant_id、code、name | 固定角色种子，`(tenant_id,code)` 唯一 |
| user_roles | tenant_id、user_id、role_id | 三者组合唯一，同租户复合 FK |
| sessions | tenant_id、user_id、refresh_hash、refresh_family_id、expires_at、revoked_at、permission_revision | refresh_hash 唯一；按 user 和过期时间索引 |

密码使用成熟密码哈希实现；认证实现阶段验证算法及参数。角色权限集合由代码版本化映射，Go 响应当前权限码；模型/前端不提交自己的权限列表。Refresh 轮换在同一事务内撤销旧值并创建新值，检测复用时撤销该 family。

## 3. 会话与消息（Go）

### conversations

字段：tenant_id、owner_user_id、title、status（active/archived）、created_at、updated_at、version。

索引：`(tenant_id,owner_user_id,updated_at DESC,id DESC)`。会话详情通过 owner 或独立明确授权读取；团队工单角色不自动获得会话内容权限。

### messages

字段：tenant_id、conversation_id、run_id（用户消息创建时与 run 原子关联）、role（user/assistant）、client_message_id、content、status（streaming/final/aborted）、generation、last_chunk_index、citations JSONB、action_receipts JSONB、created_at、updated_at。

约束：用户消息的 `(tenant_id,conversation_id,client_message_id)` 唯一；助手消息的 `(tenant_id,run_id,role)` 部分唯一。content 初始限制 32,000 字符，用户输入另限 8,000。JSON 引用必须由验证后的工具结果构建，不存随意模型对象。

消息正文是页面快照的权威展示源。每个有效 message.delta 更新草稿和事件须在同一 Go 事务内完成；message.completed 覆盖草稿并固定引用。失败时保留已展示草稿但标为 aborted，不能伪装成完整结论。

## 4. 任务、执行与事件（Go）

### runs

| 字段 | 说明 |
| --- | --- |
| id、tenant_id、conversation_id、actor_user_id | 对象归属与发起人 |
| input_message_id、retry_of_run_id | 输入与显式重试来源 |
| status、version、last_event_seq | 状态条件更新与一致快照游标 |
| lease_generation、lease_owner、lease_expires_at | 当前执行器围栏与租约 |
| graph_version、prompt_version、model_profile_version | 可重现的执行配置快照 |
| step_count、tool_call_count、input_tokens、output_tokens、cost_estimate | 累计预算和用量 |
| active_execution_ms、deadline_at、wait_expires_at | 执行期限和等待期限 |
| result_message_id、error_code、error_summary | 终态回执或脱敏原因 |
| started_at、finished_at、created_at、updated_at | 时间线 |

关键约束：同一 `(tenant_id,conversation_id)` 至多一个非终态 run，以明确活动状态集合建立部分唯一索引。status 必须在总体设计枚举中；版本/seq/预算非负。禁止终态回到活动态。

索引：`(status,lease_expires_at,id)` 用于过期扫描；`(tenant_id,conversation_id,created_at DESC,id DESC)` 用于历史；`(tenant_id,actor_user_id,created_at DESC,id DESC)` 用于本人任务。

### run_attempts

字段：tenant_id、run_id、lease_generation、dispatch_id、command（execute/resume）、checkpoint_ref、worker_id、state、started_at、finished_at、error_code、created_at。

唯一键：dispatch_id；`(tenant_id,run_id,lease_generation)`。同一次代次网络重投使用原 dispatch_id。恢复动作消耗新代次，Go 和 Python 均验证代次与当前所有者。

### run_events

字段：tenant_id、run_id、seq、source（go/agent）、source_event_id、source_payload_hash、type、schema_version、payload JSONB、occurred_at、created_at。

唯一键：`(tenant_id,run_id,seq)`；`(tenant_id,run_id,source,source_event_id)`。按 run+seq 范围读取，无需另设只按时间排序的事件索引。事件去重先于分配 seq；所有新事件在锁定 run 行后更新 last_event_seq。

事件、消息草稿/最终内容和公开状态的相关修改同事务提交。SSE 只读取提交结果，不直接转发尚未确认的 Python 网络输出。

### pending_inputs

字段：tenant_id、run_id、question、choices JSONB、status（pending/answered/expired/cancelled）、answer_message_id、checkpoint_ref、expires_at、version。一个 run 只允许一个 pending input。

## 5. 动作、确认与幂等回执（Go）

### actions

字段：tenant_id、run_id、tool_name、tool_version、arguments JSONB、payload_digest、resource_versions JSONB、evidence_versions JSONB、status、expires_at、version、created_at。

id 为 Python 在副作用前生成并落盘的稳定 action_id。Go 对参数规范化、补充资源版本后生成摘要。arguments/资源版本/依据版本在 prepared 之后不可变；修改必须创建新 action。

### approvals

字段：tenant_id、run_id、action_id、requester_user_id、status（prepared/pending/approved/rejected/expired/invalidated）、payload_digest、display_snapshot JSONB、checkpoint_ref、decided_by、decision_reason、decided_at、expires_at、version。

约束：`(tenant_id,action_id)` 唯一；与 action、run 同租户。审批是普通操作人确认，不包含一期未实现的双人复核语义。

### action_receipts

字段：tenant_id、action_id、tool_name、business_object_type、business_object_id、result JSONB、executed_by、executed_at。

唯一键：`(tenant_id,action_id)`。回执和实际业务变化、审计在同一 Go 数据库事务中提交。结果中保存足够重放响应的脱敏业务字段，而非动态依赖工单当前状态。

写工具在回执已存在且调用方仍有读取权限时返回同一结果；否则验证未过期批准、精确摘要、资源版本、任务状态和当前 lease_generation 后执行。幂等记录无需通过模型判断。

## 6. 订单与工单（Go）

| 表 | 关键字段 | 约束/索引 |
| --- | --- | --- |
| orders | tenant_id、order_no、customer_ref、masked_customer_name、status、total_amount、currency、placed_at、version | `(tenant_id,order_no)` 唯一；租户+时间分页 |
| order_items | tenant_id、order_id、sku、title、quantity、unit_amount | 订单复合 FK，数量和金额 CHECK |
| logistics_events | tenant_id、order_id、provider_event_id、occurred_at、status、description、source_updated_at | `(tenant_id,provider_event_id)` 唯一；订单+事件时间 |
| tickets | tenant_id、ticket_no、order_id、title、description、category、priority、status、assignee_user_id、created_by、source_action_id、version | 编号租户内唯一；source_action_id 非空时租户内唯一 |
| ticket_notes | tenant_id、ticket_id、content、created_by、source_action_id | action 来源唯一，内部备注长度限制 |
| ticket_events | tenant_id、ticket_id、event_type、actor_user_id、before_value、after_value、action_id | 工单+创建时间/id，保存状态及分配历史 |

工单按 `(tenant_id,status,updated_at DESC,id DESC)` 和 `(tenant_id,order_id,created_at DESC,id DESC)` 查询。仅为实用查询建立索引，避免对每个筛选组合建索引。

同一订单可能存在多张不同问题工单，因此不设置“每订单只一张工单”的错误全局唯一约束。创建前向工作人员展示现有相似工单；一次逻辑创建由 action_id 或手工幂等键防重。

订单/物流在一期由合成种子与模拟适配器提供。不得把真实客户数据作为示例提交，也不把区块链或钱包专属表迁入本项目。

## 7. 知识元数据与索引（Go / Python）

以下表格保留完整目标模型。M3 实际落地采用 `knowledge_bases.read_roles` JSONB 固定角色 ACL；`documents.version` 作为激活/撤回并发保护；不可变正文暂存 `document_versions.content`，该表同时保存 job_id/index_status。Go outbox 负责投递，Python `agent.knowledge_jobs` 负责持久化 claim 和回调恢复。Python 的 `knowledge_manifests`、`knowledge_chunks`、`model_calls` 暂位于 agent schema，vector 扩展类型仍在 knowledge schema。Go `run_evidence` 保存已复核的运行证据。物理分离 schema 和对象存储可后续独立迁移，当前账户写权限仍按服务分隔。细节与边界见 [M3 说明](m3-rag.md)。

### Go business 元数据

| 表 | 关键字段 | 语义 |
| --- | --- | --- |
| knowledge_bases | tenant_id、name、status、acl_revision | 知识库权限和当前授权版本 |
| knowledge_base_acl | tenant_id、knowledge_base_id、role_id、can_read、can_manage | 固定角色授权，同租户 FK |
| documents | tenant_id、knowledge_base_id、title、status、active_version_id、activation_sequence、version | 有效版本指针和并发激活保护 |
| document_versions | tenant_id、document_id、version_no、content_hash、storage_key、mime_type、file_size、status、manifest_id | `(tenant_id,document_id,version_no)` 唯一 |
| index_jobs | tenant_id、document_version_id、activation_sequence、status、dispatch_id、attempt_count、error_code | 可重投、可取消的索引任务 |

文件按服务端生成 storage_key 保存；版本创建后内容不可变。active_version_id 复合外键保证同租户，在迁移中处理 documents 与 versions 的创建顺序。内容相同只在明确的 document/tenant 作用域复用，不暴露跨租户文件存在性。

索引任务状态：queued/running/ready/failed/cancelled。文档版本状态：uploaded/indexing/ready/failed/withdrawn；仅 active_version_id 指向的 ready 版本可用于当前政策。新版本失败不撤回旧版本；withdraw 会立即更新 Go 权限可见性。

### Python knowledge 索引

M7.1 实际迁移在 Go `document_versions` 追加 source_format、source_bytes（BYTEA）、page_texts（JSONB）及 parser_version，run_evidence 追加 page_number。PDF 原字节的 SHA256 固定为版本 content_hash；派生正文在首次成功索引时冻结。Goctl BYTEA 映射由生成脚本修正为 []byte，禁止直接编辑生成模型。Python 的 knowledge_manifests 保存同一分页结果和 parser_version，knowledge_chunks 保存 page_number，knowledge_jobs 保存可恢复的终止错误码。文本旧行通过默认值兼容，Go/Python 各自迁移自有表，见 [PDF 数据契约](m7-knowledge-files.md)。

| 表 | 关键字段 |
| --- | --- |
| index_manifests | tenant_id、knowledge_base_id、document_id、version_id、embedding_model、dimension、splitter_version、content_hash、state、chunk_count |
| chunks | tenant_id、knowledge_base_id、version_id、manifest_id、chunk_id、position、page、heading_path、content、token_count、lexical_text、embedding |

chunk_id 是稳定 UUID，基于版本与分块序号生成或写入一次后复用。`(tenant_id,version_id,position,manifest_id)` 唯一。向量维度固定到一个 embedding collection/model namespace，模型或维度变化创建新 collection 并重建，不能混写。

lexical_text 保存 Python 分词后的词项，用 PostgreSQL `simple` 配置建立全文索引；中文质量需要评测，不能假设数据库默认英文分词即可满足中文召回。索引包括租户/知识库/版本过滤、向量检索和词项检索所需结构，具体 vector 类型及索引参数由实现压测确定。

流程：Python 提交 ready manifest→回调 Go→Go 验证版本/激活序号并切换指针→Python 后续清理旧 manifest。Go 和 Python 无跨库事务；检索候选最终经过 Go 复核，消除未激活、已撤回和 ACL 变化的可见性窗口。

## 8. 可靠消息与审计（Go）

### outbox_events

字段：tenant_id、topic、aggregate_id、dispatch_id、payload JSONB、status、available_at、lease_owner、lease_expires_at、attempt_count、last_error_code、created_at、delivered_at。

topic 包含 run.execute、run.resume、run.cancel、knowledge.index。唯一 dispatch_id，调度索引 `(status,available_at,id)` 仅覆盖未完成项。claim 使用短事务和 SKIP LOCKED，外部 HTTP 在事务外执行。

达到重试上限进入 dead_letter，记录可诊断原因；管理员重投复用原 dispatch_id，不修改不可变 payload。业务状态、审批决定和对应 outbox 同事务落盘，避免已确认但恢复命令丢失。

### api_idempotency

字段：tenant_id、actor_user_id、operation、key_hash、request_hash、status、response_status、response_body、expires_at。唯一键为租户、操作者、操作名和 key_hash；保存的响应须脱敏，含令牌响应不进入此通用缓存。

### audit_events

字段：tenant_id、actor_user_id、service_identity、action、resource_type、resource_id、run_id、action_id、request_id、trace_id、outcome、error_code、redacted_summary、created_at。

成功的业务变更审计和业务数据同事务；失败请求审计在失败事务外追加，以免随回滚消失。审计不保存密码、令牌、原始个人信息、完整模型提示或模型私有推理。后台清理与导出也留痕。

## 9. Python 执行数据

| agent 表/集合 | 内容与约束 |
| --- | --- |
| execution_commands | dispatch_id 唯一、run_id、lease_generation、command、可信上下文、状态和时间；接收先落盘；M6.3 追加 traceparent，仅用于恢复 trace 父上下文，不参与命令 hash |
| execution_owners | run_id 唯一、current_generation、worker_id、ownership_version；配合数据库执行锁 |
| checkpoint tables | 由已锁定版本的 PostgreSQL checkpointer 管理；thread_id 固定 run:{UUID} |
| callback_outbox | source_event_id 唯一、run_id、代次、稳定 payload/hash、顺序、重试和 ack |
| index_commands | job_id/dispatch_id 唯一、版本和索引阶段、错误及重试状态 |

Python 自有迁移与 checkpointer 的 setup/升级通过受控迁移任务完成。运行时账号不给任意 DDL 权限；启动前验证迁移版本。不得从 Go goose 迁移越权修改 Python 框架表。

执行锁保证同 run 图只有一个检查点写入者；checkpoint 适配必须校验所有权。Go 业务动作由租约代次额外围栏。该并发保证需作为 M4 故障测试的硬验收，不能只依赖框架“支持恢复”的说明。

## 10. 关键事务边界与锁顺序

M6.1 的 Python 追加迁移还创建 embedding 调用账本/租户缓存、语义 manifest/向量和检索尝试 metadata。profile 区分供应商 endpoint、模型、请求维度及 revision；实际维度受约束，不能混用。预算在 HTTP 前事务预留；语义 manifest/向量提交必须通过当前索引 job 代次围栏。此升级不修改 Go 业务表或文档激活指针。完整字段以 Python 迁移为准，行为见[语义 RAG](m6-semantic-rag.md)。

统一业务写锁顺序：run（存在时）→action→approval→业务主对象→关联表。手工业务写没有 run/action，但同样按主对象到关联表顺序。读取已有回执可先做只读检查，进入写事务后需重新验证。

| 用例 | 同一事务内必须完成 | 事务外完成 |
| --- | --- | --- |
| 发送消息 | 幂等记录、会话权限复核、消息、queued run、初始事件、outbox | Python 投递 |
| 待确认发布 | 验证 checkpoint 引用、prepared→pending、run waiting_approval、事件 | SSE 推送 |
| 确认 | 版本/权限/有效期核对、决定、run queued、事件、resume outbox | Python 恢复 |
| 执行 action | run/代次校验、批准及版本复核、业务写、回执、审计、事件 | 模型继续总结 |
| 取消 | 阻止新写、失效未执行确认、状态、事件、cancel outbox | 执行器停止 |
| 文档激活 | job/版本/manifest/激活序号校验、active 指针、审计 | 清理旧向量 |

外部请求耗时不占用数据库锁。PostgreSQL 默认 READ COMMITTED 配合显式行锁、唯一约束和版本条件更新；一致快照读取使用短 REPEATABLE READ 事务或同等单语句快照，避免状态和 last_event_seq 对不上。

## 11. 保留、清理与恢复

初始保留策略：run 事件终态后 7 天；消息和完整 trace 30 天；业务 action 回执及审计至少 180 天；普通幂等记录 30 天。等待输入最长 24 小时、pending approval 15 分钟，批准后执行还必须在原动作期限内完成。

清理任务跳过未终态 run、未交付 outbox 和待处理动作。事件清理保留 run 快照及最终消息，旧游标返回 410。Checkpoint 仅在任务终结且恢复窗口结束后清理；框架数据和 Go 数据分别由所属服务清理。

文档撤回后即时限制读取，异步清理全文和向量；历史引用只保留授权允许的元数据，必要时移除摘录。消息和审计保留策略需在真实业务接入前按业务要求配置。

备份覆盖 PostgreSQL 与原文件存储，定期演练“先恢复元数据和原文件→核对 manifest→缺失索引重建”。向量可以重建，业务回执和动作账本不能用模型重新生成。恢复过程禁止将两个未知并发执行器同时置为 active。

## 12. 迁移与生成交付

M7.2 的 `2026110405_run_operations.sql` 在 Go 的 runs 增加 retry_of_run_id/depth；租户复合外键及部分唯一索引保证同租户关联和每源一个子任务，depth 限 0–3。列表索引覆盖租户/所有者和 created_at/id 排序，权限过滤通过 run_ticket_resources 与当前团队成员关系完成。Go 在会话→源 run 锁下将新 run、消息、queued 事件、outbox、审计和幂等记录同事务提交。Python 自有迁移增加 run_observation_scopes，只有可信执行授权后写入不可变的 run/tenant 绑定；观测读取不访问业务表。回退不执行 Down，不删除重试来源或观测绑定。

M1 建立生成器、迁移脚本和模型表清单。迁移按依赖拆为 identity、conversation/run、orders/tickets、actions/outbox、knowledge metadata 等文件，日期加两位顺序号。Goose 记录已应用版本，后续只新增迁移。

所有业务表生成 go-zero 基础模型，并在各表 custom model 扩展本文的锁查询和条件更新。新增跨表事务由 owning logic 组织，禁止在表模型中协调其他表写操作。

必须覆盖的数据库集成案例：跨租户外键拒绝、同会话并发发起、重复 action、并发确认/取消、回执提交后响应丢失、后续步骤失败回滚、旧文档索引回调乱序、旧 lease_generation 被拒。测试只使用隔离 schema/可丢弃数据库。

## M7.9 按库保留策略

M7.9 的 `2026110506_retention_policy.sql` 给 knowledge_bases 增加 retention_keep_latest（默认 3，1–100）、retention_minimum_age_days（默认 30，1–3650）、retention_revision（默认 1，正整数）。历史行/旧 INSERT 使用默认值。策略写入由知识库排他锁和 expected_revision CAS 保护，参数/修订/审计/幂等结果同事务提交；不改变 acl_revision。清理预览和提交持知识库共享锁，执行前验证 expected_policy_revision；已确认命令不因后续策略变更取消。旧清理请求省略字段时保留原摘要，仅在初始策略下可新建命令，已提交命令仍能重放。见[保留策略](m7-retention-policy.md)。

## M7.8 归档副本登记与清理

后续 M7.8 的 `2026110505_source_objects.sql` 新增 Go 所有的 source_objects，按租户/版本保存对象身份、存储指纹、对象键、hash、大小、归档代次和 registered/cleanup_pending/retired 状态。存储指纹/对象键唯一，版本使用租户复合外键；旧 s3 行回填当前定位，迁移前已丢失定位的孤儿不自动登记。上传前登记意图，限制每版本最多 100 个未 retired 副本；列表优先展示未 retired 项，最多返回 101 条，历史元数据继续保留在数据库。

恢复递增 source_epoch，新归档键包含代次。确认清理时版本/对象锁下原子保存状态、审计和 knowledge.source_cleanup outbox；条件写零字节对象墓碑在事务外，提交退休状态前复核当前定位及有效投递租约。此流程保留数据库原件，M7.7 的归档历史保护仍适用。完整协议见[归档副本清理](m7-source-cleanup.md)。

## M7.7 版本清理状态

Go 迁移 2026110504_knowledge_retention.sql 为 document_versions 增加 cleanup_state（retained/cleanup_pending/purged），仅 purged 可清空正文、原始字节及页文本，保留原 source_size、hash 和版本身份；引用和 outbox 新增版本过滤索引。Python knowledge_tombstones 永久绑定 version/tenant/job/hash，同事务清理语义向量、语义 manifest、词法片段及基础 manifest，保留无正文 job。两服务不跨写，协议见[版本保留与清理](m7-retention.md)。

## M7.3 OCR 版本和引用来源

Go 迁移 2026110406_knowledge_ocr.sql 为 document_versions 增加 ocr_mode（默认 off，枚举及非 PDF 约束），为 run_evidence 增加 extraction_method（旧行为默认空）。逐页方法保存于既有 page_texts JSONB，摘要查询只统计 OCR 页数，不加载原文件。确认激活在业务事务中记录 knowledge.ocr_review 审计，绑定版本、当前 actor 和租户；不保存识别置信度或原生 stderr。Python 沿用 knowledge_manifests.page_texts/parser_version 及 knowledge_jobs 的代次/错误持久化，不访问 Go 表。模式随版本不可变，变更需另建版本；不执行 Down 清理审核或引用来源。

## M7.4 原文件定位和归档代次

迁移 2026110501_source_storage.sql 增加 source_storage/size/store_id/key/epoch；历史行按原字节填充 size，旧版 INSERT 的 size 由 BEFORE INSERT 触发器填充。约束确保 PDF 原字节为 1 字节至 2 MiB：postgres 时长度与 size 相同且定位为空；s3 时原字节为空且指纹/键非空。TXT/MD 仍保留原 content。Go 自有 outbox 增加 knowledge.archive，dispatch_id 由版本和 source_epoch 确定。

上传事务同时保存原文件、索引任务及可选归档任务；网络操作在事务外进行。切换定位时先锁版本，再校验并锁 outbox 当前 attempt/有效 lease，CAS 更新同 epoch 的数据库行。恢复只在原对象长度/hash 正确且当前授权有效时写回字节、递增 epoch；旧归档 payload 自动失效。死信再投递保留递增 attempt_count，每 8 次失败进入死信，禁止重置隔离代次。归档和恢复不改内容 hash、提取 manifest、激活指针或引用。无自动对象删除/GC；Down 保留全部原文件定位和审计。
## M7.5 不可变 PDF 阅读方式

Go 迁移 `2026110502_pdf_layout.sql` 为 document_versions 增加 pdf_layout，默认 plain，数据库约束仅允许 plain/columns/table；非 plain 必须为 PDF 且 ocr_mode=off。选择通过上传幂等摘要和内部 source 传递，改变方式须另建版本；旧 INSERT 采用数据库默认值，现有 Go InsertTx 对空值归一化。原件 hash、存储定位和物理页号不变，首次索引冻结新 parser 与分页文本。Python 复用既有 manifest、错误及租约代次表，不直接访问 Go 表。激活事务记录 knowledge.layout_review，绑定当前管理员/租户/版本；回退保留模式及审计，不执行 Down。
## M7.6 账号与角色投影

迁移 2026110503_accounts_roles.sql 新增 roles/user_roles，使用租户复合外键、角色名大小写不敏感唯一索引和内置角色部分唯一索引。用户新增 revision/auth_version/extra_permissions；users.role 保留有效基础角色投影，extra_permissions 为启用自定义角色的权限并集。旧账号回填关联，新租户及新账号 INSERT 由触发器初始化基础角色。

管理写在租户授权排他锁下更新用户/绑定/投影，递增版本并撤销全部 sessions，同时记录审计和幂等结果。角色修改会逐个刷新关联用户；保护最后一个启用的基础 admin，并拒绝自身账号/关联角色修改。密码只有 bcrypt hash，幂等摘要采用服务端 HMAC。Down 保留身份历史；禁止手工写投影绕过 API。并发顺序和范围见[账号设计](m7-accounts.md)。
