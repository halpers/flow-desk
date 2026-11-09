# flow-desk-agent

Python Agent 服务。规划使用 FastAPI + Pydantic 提供内网接口，LangGraph 组织任务，PostgreSQL 持久化检查点，pgvector 保存检索向量。

M1 提供工程基础；M2 已接入持久化命令队列、回调 outbox、LangGraph 只读图、PostgreSQL checkpointer 和工具结果缓存。完整目标见 [Python 详细设计](docs/design.md)，当前范围见 [M2 交付说明](../docs/m2-read-tasks.md)。

- 规划内网端口：`8000`。
- 只接受 Go 服务授权的执行、恢复、取消和索引任务。
- 业务读写通过 Go 内部工具 API；Python 数据库账号仅拥有 `agent`、`knowledge` schema 权限。
- 图执行、文档索引 worker 属于同一 Python 服务代码库，可按需扩展副本。
- 离线评测目录：`evals/datasets/`、`evals/reports/`；仅提交合成数据。

Python 3.12 创建独立 `.venv` 后执行 `pip install -r requirements.lock`。先在独立迁移环境设置 `AGENT_MIGRATION_DATABASE_URL`，以 `PYTHONPATH=src` 执行 `python -m flowdesk_agent.storage.migrate`；运行环境只设置 `AGENT_DATABASE_URL` 等运行参数，不携带迁移/owner凭据，再运行 `uvicorn flowdesk_agent.api.main:app --port 8000`。推荐根目录Compose自动完成迁移门禁；运行Agent无DDL，旧卷升级及HNSW维护见[专项手册](../docs/m7-agent-migration.md)。`python scripts/export_contract.py` 不建立数据库连接即可导出 OpenAPI。

跨服务接口见 [接口契约](../docs/contracts.md)，实际阶段状态见 [实施记录](../docs/progress.md)。

M3 已添加持久化索引队列、字符向量/全文 RRF 检索、Go 复核证据、兼容 Chat Completions 的结构化模型适配与持久化预算。合成政策位于 `eval/m3-policies.json`，历史基线及限制见 [M3 说明](../docs/m3-rag.md)。M6.1 增加独立兼容 embeddings 接口、租户隔离的持久缓存/预算、语义 profile 重建及可选模型重排，部署默认仍为词面基线；切换与回退见[语义 RAG](../docs/m6-semantic-rag.md)。

M4 的 `ticket-v1` 图支持补充信息与审批中断、稳定 action_id、Go 回执及持久化取消。等待时先保存检查点，再通过 callback outbox 通知 Go；恢复仍使用同一 run 图线程。执行后的回执单独保存，总结失败不会抹去业务效果。仍兼容已存在的 `read-v1` 命令。验证方式见 [M4 交付说明](../docs/m4-actions.md)与[故障证据](../docs/m4-fault-matrix.md)。

M5 的固定数据位于 `eval/datasets/tasks-v1.jsonl`，执行和报告脚本位于根目录 `scripts/`。节点 metadata trace 与模型调用耗时保存在 Agent 自有表，不保存提示词或推理到 trace；预算和业务写入授权仍分别由模型适配与 Go 执行。复跑和限制见 [M5 说明](../docs/m5-evaluation.md)。

M6.2 的官方 SDK stdio MCP 提供三个读工具及已批准 action_id 执行，复用 Go 权限/审批与稳定回执；默认 `TOOL_TRANSPORT=http`，可配置 mcp。边界及本地验收见[MCP 说明](../docs/m6-mcp.md)。

M6.3 可选启用 OTLP HTTP exporter，持久 traceparent 串联调度、图节点与 Go 工具；内部 /metrics 输出固定标签的聚合账本与检索延迟。Collector 执行 metadata 白名单及事件清理，配置和本地验收见[部署与监控](../docs/m6-deployment-monitoring.md)。
