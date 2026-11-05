# 跨服务契约归档

M7.10 在 Go api/knowledge.api 增加完整版本历史分页和已清理版本元数据归档，生成 Go 类型、OpenAPI、浏览器类型及 Agent 内的 Go 契约副本；无新 Python HTTP 或 SSE schema。历史游标不能代替当前授权，归档命令绑定文档修订和幂等键，详见[历史归档契约](../../docs/m7-history-archive.md)。

当前行为基线是 [接口契约](../../docs/contracts.md)。M1 起按以下来源生成机器可读契约，避免三端各维护一份类型：

| 内容 | 实施后的权威来源 | 产物 |
| --- | --- | --- |
| Go 浏览器和内部工具 API | `flow-desk-serv/flowdesk/flowdesk.api`、`api/*.api` | OpenAPI、前端类型、Python 工具 client |
| Python 执行 API | `flow-desk-agent/src/flowdesk_agent/api/` 的 Pydantic/FastAPI 定义 | OpenAPI、Go client |
| SSE 与批量回调事件 | 本目录 `events/v1/*.schema.json` | TS/Python/Go 校验模型及契约样例 |
| 工具参数及结果 | 本目录 `tools/v1/*.schema.json`，与 Go 路由关联 | Python 工具注册、参数校验、协议测试 |

M1 已生成 go.swagger.json、go.openapi.json、agent.openapi.json 及前端 generated.ts；在根目录运行 scripts/export-contracts.ps1 重建。事件和工具随对应阶段新增版本化 Schema 和正反向样例；当前没有将设计中的全部工具伪装为已实现契约。

M2 新增 `events/v1/run.schema.json` 和 `tools/v1/read.schema.json`。`scripts/generate-runtime.py` 派生状态/事件/工具常量，并将权威 Schema 分发到各服务构建上下文。Python 的读工具结果验证使用生成 Go OpenAPI 中对应路由的响应定义；前端业务类型直接从生成路径类型提取。goctl 当前内联响应定义，因此不假设 OpenAPI 一定存在具名 components.schemas。

兼容策略：v1 只增加可选字段；删除字段、改变含义、修改枚举或改变鉴权语义需要显式兼容评估，必要时发布 v2。生成产物标明来源和工具版本；禁止手改产物。

M6 的 retrieval/v1 定义供应商 embedding 响应和受限重排输出。权威来源是 Python 的 EmbeddingResponse / Ranking，由 Agent 契约导出脚本生成；运行时使用同一 Pydantic 模型验证，不手改 JSON 产物。

M7.1 增加 retrieval/v1/pdf-manifest.schema.json，来源为 Python PDFManifest；它只描述内部提取结果，不是新增公网 Agent 接口。PDF 的源文件由 Go 保存，页码随证据和消息引用传递；事件 v1 中 page_number 为可选字段，兼容既有无页码事件。Go HTTP 文本/PDF 上传及管理员分页预览仍由 .api 生成。

M7.2 增加 Go operations.api 的列表/追踪/重试，Python AgentTraceData 的内部脱敏观测响应。生成到各自 OpenAPI 和浏览器类型；没有新增 SSE 事件，也没有把执行账本或检查点作为通用 JSON 导出。两个服务的观测字段需保持一致，真实 HTTP 验收验证 Go 严格解码 Python 返回值。

M6.5 对 v1 追加两个工具名、相应严格参数和可选回执结果字段，既有事件仍可验证。新增工具名要求 Go/Python/Web 一同升级，旧消费者可能拒绝新枚举；原图版本继续保留，恢复不改写旧动作。回退旧镜像前需停止接收新任务并妥善完成 ticket-v2 等待任务，不能把旧版本能启动视为新动作也可恢复。

M7.3 扩展 PDFManifest 到文本/OCR 两个固定解析版本，增加可选逐页 extraction_method；旧文本页可省略。Go .api 增加可选 ocr_mode/confirm_ocr、版本统计和证据来源，随后统一生成。业务证据来源只由 Go 从已冻结页派生，不能由模型候选提供；没有新增 SSE 事件。

M7.5 增加可选上传 pdf_layout/激活 confirm_layout、版本/内部 source 的阅读方式及两个固定 layout parser 枚举。PDFManifest 仍由 Pydantic 导出，旧文本/OCR 结果格式继续可读；旧消费者不识别新 parser，因此需要 api/agent/web 一同升级，回退须先处理非 plain 索引及活跃版本。兼容评估和操作见[版面说明](../../docs/m7-pdf-layout.md)及[服务器手册](../../deploy/server.md)，不把追加枚举当成双向兼容。
M7.6 管理接口的源为 Go api/accounts.api，账号/角色响应及前端类型由统一导出流程生成；没有新增 SSE 或 Agent HTTP 字段。权限目录由服务端返回，基础角色仍用于团队/知识 ACL 投影；见[账号管理契约](../../docs/m7-accounts.md)。
