# flow-desk-agent

Python Agent 服务。规划使用 FastAPI + Pydantic 提供内网接口，LangGraph 组织任务，PostgreSQL 持久化检查点，pgvector 保存检索向量。

M1 已实现 FastAPI 入口、PostgreSQL 连接池、存活/就绪接口、锁文件及容器。LangGraph 和 PostgreSQL checkpointer 依赖已锁定，执行图在 M2 后接入。完整目标见 [Python 详细设计](docs/design.md)。

- 规划内网端口：`8000`。
- 只接受 Go 服务授权的执行、恢复、取消和索引任务。
- 业务读写通过 Go 内部工具 API；Python 数据库账号仅拥有 `agent`、`knowledge` schema 权限。
- 图执行、文档索引 worker 属于同一 Python 服务代码库，可按需扩展副本。
- 离线评测目录：`evals/datasets/`、`evals/reports/`；仅提交合成数据。

Python 3.12 创建独立 `.venv` 后执行 `pip install -r requirements.lock`；设置 `AGENT_DATABASE_URL`，以 `PYTHONPATH=src` 运行 `uvicorn flowdesk_agent.api.main:app --port 8000`。推荐使用根目录 Compose。`python scripts/export_contract.py` 不建立数据库连接即可导出 OpenAPI。

跨服务接口见 [接口契约](../docs/contracts.md)，实际阶段状态见 [实施记录](../docs/progress.md)。
