# M6 专项实施与验收

M6 按顺序实施：M6.1 语义 RAG，M6.2 MCP，M6.3 部署与监控，M6.4 团队权限，M6.5 业务场景扩展。M6.4/M6.5 根据用户继续实施的指示纳入范围。本文是实施设计，状态和实测结果以 [实施记录](progress.md) 为准。

## M6.1 语义 RAG

采用用户选择的独立兼容 embeddings 接口，使用 `EMBEDDING_BASE_URL`、`EMBEDDING_MODEL`、`EMBEDDING_API_KEY`；不复用聊天密钥或假定 DeepSeek 聊天接口提供向量。`EMBEDDING_DIMENSIONS=0` 使用服务原生维度，正整数才发送 dimensions。接口依据 [OpenAI 官方 embeddings 文档](https://developers.openai.com/api/reference/resources/embeddings/methods/create)，供应商差异通过实际联调验证。

保留 `char-ngram-v1` 原索引和稳定 chunk ID；在 Python-owned schema 新增语义索引、向量、缓存及用量账本。profile 由 endpoint、model、请求维度和显式 revision 生成，不包含密钥。向量使用无固定维度的 pgvector 列，同一 profile 内校验实际维度，拒绝 NaN、零向量、索引缺项和模型维度漂移。

调用先记录持久化预算，再在数据库事务外访问供应商；按租户/profile/文本 hash 复用缓存，逐次尝试记录实际 usage、错误和耗时，不保存原始请求、密钥或私有推理。索引处理在每次外部请求前续租，写入时继续校验原 job 代次。升级已有版本时只重建 Python 索引，不改变 Go 的文档激活状态或业务表。

`RETRIEVAL_PROFILE=baseline` 是默认值；`semantic` 启用新向量召回与原全文 RRF。每次查询先从 Go 获取当前允许版本，在 SQL 排名前过滤租户、版本和 profile，最后仍由 Go 复核引用。语义索引不完整时明确拒绝，不能混用旧向量。切回 baseline 可回退检索配置，不删除任何索引。

可选 `RERANK_PROVIDER=chat` 使用当前兼容聊天模型对有限候选评分，复用同一 run 的模型预算与恢复缓存。候选仅来自已授权召回，输出只能引用传入的候选 ID；重排失败记录元数据并保留融合顺序，不扩大候选范围。

验收覆盖：HTTP 格式、乱序返回、坏向量、429/超时、租户缓存隔离、持久化预算、索引重放/旧 owner、profile 隔离、索引缺失、权限撤回，以及重排伪造 ID。增加独立的同义改写与相似政策干扰集，按 baseline / semantic / semantic+rerank 报告 Recall@5、MRR、延迟和实际用量；不复用 M5 留出集调参，不保证一定测得提升。

## M6.2 MCP

使用官方 MCP SDK 的固定版本提供真实的工具发现和调用适配，复用已有权威工具 schema。工具上下文从服务身份和 Go run capability 注入；tenant、actor、权限和批准决定不进入模型参数。读工具先接入，写工具只接受已批准的 action_id，并继续通过 Go 事务、当前授权和幂等回执执行。

验收使用真实 MCP client/server，覆盖协议协商、工具列表和 schema、授权/未知工具拒绝、跨租户、未批准写、重复执行与回执一致。MCP 不成为新的业务数据所有者。

## M6.3 部署与监控

用户已选择先完成部署配置及本地监控验收，暂不上服务器。在现有双仓库发布基础上增加按固定版本部署的配置，说明 TLS、服务身份和回滚策略；只使用本地合成数据验收，不执行远程上线。

接入 OTel 和指标收集，串联 Go HTTP、调度、Agent 节点和模型/工具 metadata；不采集提示词、文档、用户正文和凭据。监控队列、活跃任务、工具/模型错误、Token、索引失败与检索耗时，提供可运行的告警和仪表盘配置。

验收包含发布镜像启动、TLS 入口、登录和办理 smoke、升级/回滚、故障恢复、指标抓取、告警触发和 trace 脱敏。仅完成镜像发布不能标记为目标服务器已上线。

## 交付规则

M6.4 的资源范围、成员管理、撤权与历史兼容契约见[团队权限设计](m6-teams.md)。M6.5 的物流建单、工单接手及售后办结见[业务场景契约](m6-business-scenarios.md)，复用精确批准、当前授权、稳定 action_id、不可变回执和检查点恢复。固定合成数据位于 `flow-desk-agent/eval/datasets/m6-business.jsonl`，通过 `scripts/check-business-scenarios.py` 验收。

每个专项分别更新设计、必要的机器契约、配置样例、测试与实际报告；检查暂存区不包含密钥后自动提交并推送。确定性 CI 不访问付费模型；真实服务评测在显式配置后独立运行，未配置价格仍记录费用 unknown。
