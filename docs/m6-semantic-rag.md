# M6.1 语义检索与重排

M6.1 在字符基线之外增加独立兼容 embeddings 接口、语义索引和可选模型重排。实际比较见[报告](../reports/m6/summary.md)，专项顺序见[M6 计划](m6-plan.md)。PDF/OCR 和近似向量索引仍未实现。

## 配置与切换

在忽略提交的根目录 `.env` 配置 `EMBEDDING_BASE_URL`、`EMBEDDING_MODEL`、`EMBEDDING_API_KEY`。服务追加 `/embeddings`，依据 [OpenAI embeddings 接口](https://developers.openai.com/api/reference/resources/embeddings/methods/create)发送文本数组和 `encoding_format=float`。`EMBEDDING_DIMENSIONS=0` 不发送 dimensions；正整数请求指定维度。响应严格验证索引、有限数值、非零向量和一致维度。实际供应商兼容性由联调确认。

默认 `RETRIEVAL_PROFILE=baseline`、`RERANK_PROVIDER=none`。配置 embedding 后的新索引任务同时产生基线与语义索引；已有知识版本需要重建。先启动配置后的 Agent，再使用可信运维身份指定租户：

```powershell
./scripts/compose.ps1 exec -T agent python -m flowdesk_agent.reindex --tenant-id <tenant-uuid> --all
```

也可重复传入 `--version-id <version-uuid>`。命令检查版本的租户归属，只修改 Python 拥有的索引，不改变文档激活状态。重复执行相同 manifest/profile 不重复请求 embedding。

重建全部允许版本后，设置 `RETRIEVAL_PROFILE=semantic`，可选 `RERANK_PROVIDER=chat`，重建 Agent 容器加载环境：

```powershell
./scripts/compose.ps1 up --detach --no-deps --wait agent
```

回退时改为 baseline/none 并执行相同命令；旧索引保留。改变 endpoint、model、维度或 `EMBEDDING_REVISION` 会生成新 profile，必须先重建再启用。缺少当前 profile 的索引返回 `RETRIEVAL_INDEX_NOT_READY`；不静默混用模型。Compose 示例始终默认基线，不会仅因配置密钥而切换已有查询。

## 权限、事务与预算

先从 Go 获取当前 scope，在 SQL 排名前过滤 tenant、有效版本和 profile。pgvector 使用变长 vector 列与维度约束，按 cosine 精确扫描；尚无 HNSW 性能结论。全文与向量通过 RRF 融合。候选经过 Go 证据复核后才进入重排；重排后再次确认权限和引用，防止调用期间撤权。模型不能扩展候选 ID 或生成租户/批准决定。

embedding 缓存按 tenant/profile/文本 hash 隔离。请求前在短事务中预留持久预算，HTTP 在事务外执行；每次尝试记录耗时、usage、错误与 unknown 费用，不存原文或密钥。单次最多 16 段、每段 24,000 UTF-8 bytes；索引 operation 最多 200 次调用/4 MB，查询 operation 最多 8 次/24,000 bytes，最多两次 HTTP 尝试，每次超时 12 秒。重放不能重置预算。超限需运维检查，不会无限重试。

索引每次外部请求前续租，提交时锁定并检查原 job 代次。语义 manifest 和向量同事务提交。旧执行者不能发布语义索引。

重排最多五个已验证候选，每段最多 600 UTF-8 bytes，输出最多 512 tokens。复用当前 run 的聊天模型预算和成功缓存；坏 ID、错误输出或预算失败记录 `rerank_fallback`，保留 RRF 顺序。没有候选时不调用模型。

## 契约与数据

Pydantic 源定义通过 `scripts/export-contracts.ps1` 生成[embedding 响应](../shared/contracts/retrieval/v1/embedding-response.schema.json)和[重排响应](../shared/contracts/retrieval/v1/rerank-response.schema.json)。浏览器/Go HTTP API 无新增字段。

Agent 新增 `embedding_calls`、`embedding_cache`、`semantic_manifests`、`semantic_vectors` 和 `retrieval_attempts`；均在 Python 自有 schema，通过追加迁移创建。chunk ID 和 Go 证据协议保持稳定。未实现索引物理清理，撤回由 Go 当前授权即时限制访问。

## 验证与复跑

确定性测试使用假 HTTP 供应商和隔离 schema；默认 `KnowledgeIndex` 不自动加载本地密钥。运行服务才显式注入 embedder，CI 不访问付费接口。

独立比较命令为 `python scripts/evaluate-semantic.py --real --rerank`；需要可连接的开发数据库和已配置的两个模型接口。它创建并清理独立 schema，报告供应商实测和合成过滤断言，不替代 Go 授权端到端测试。默认不加 `--real` 只跑基线。

端到端命令为 `python scripts/smoke-rag.py --profile openai-compatible --output reports/m6/acceptance-semantic.json`，需已运行的合成本地栈；覆盖十条政策问答、引用、跨租户拒绝和撤回回放。此命令会激活并撤回合成测试文档，请勿用于生产数据。用量和限制见[原始报告](../reports/m6/retrieval-real.json)。
