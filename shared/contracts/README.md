# 跨服务契约归档

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

M6.5 对 v1 追加两个工具名、相应严格参数和可选回执结果字段，既有事件仍可验证。新增工具名要求 Go/Python/Web 一同升级，旧消费者可能拒绝新枚举；原图版本继续保留，恢复不改写旧动作。回退旧镜像前需停止接收新任务并妥善完成 ticket-v2 等待任务，不能把旧版本能启动视为新动作也可恢复。
