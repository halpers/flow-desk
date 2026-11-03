# flow-desk-agent

Python Agent 服务。规划使用 FastAPI + Pydantic 提供内网接口，LangGraph 组织任务，PostgreSQL 持久化检查点，pgvector 保存检索向量。

M1 提供工程基础；M2 已接入持久化命令队列、回调 outbox、LangGraph 只读图、PostgreSQL checkpointer 和工具结果缓存。完整目标见 [Python 详细设计](docs/design.md)，当前范围见 [M2 交付说明](../docs/m2-read-tasks.md)。

- 规划内网端口：`8000`。
- 只接受 Go 服务授权的执行、恢复、取消和索引任务。
- 业务读写通过 Go 内部工具 API；Python 数据库账号仅拥有 `agent`、`knowledge` schema 权限。
- 图执行、文档索引 worker 属于同一 Python 服务代码库，可按需扩展副本。
- 离线评测目录：`evals/datasets/`、`evals/reports/`；仅提交合成数据。

Python 3.12 创建独立 `.venv` 后执行 `pip install -r requirements.lock`；设置 `AGENT_DATABASE_URL`，以 `PYTHONPATH=src` 运行 `uvicorn flowdesk_agent.api.main:app --port 8000`。推荐使用根目录 Compose。`python scripts/export_contract.py` 不建立数据库连接即可导出 OpenAPI。

跨服务接口见 [接口契约](../docs/contracts.md)，实际阶段状态见 [实施记录](../docs/progress.md)。

M3 已添加持久化索引队列、字符向量/全文 RRF 检索、Go 复核证据、兼容 Chat Completions 的结构化模型适配与持久化预算。合成政策位于 `eval/m3-policies.json`，复跑方式及限制见 [M3 说明](../docs/m3-rag.md)。仅模型答复使用外部服务；文档索引当前使用本地词面向量基线。
