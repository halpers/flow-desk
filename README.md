# FlowDesk（智办台）

企业智能工单与业务办理 Agent。用户以自然语言描述售后问题，系统查询订单、检索有效政策、生成操作方案，经确认后创建或更新工单，并保留可回放的执行记录。

## 当前交付

2026-11-02：M1 工程基础已实现，包括 React 登录工作台、go-zero 身份接口、FastAPI 健康检查、PostgreSQL 迁移与合成账号、模型/契约生成、锁文件及 Compose。Go 工程最初按要求使用 `goctl api new flowdesk` 生成。任务编排、RAG 和业务办理按后续阶段实现，具体状态见 [实施记录](docs/progress.md)。

用户已确定 Go 业务后端使用 **go-zero**，Agent 使用独立 Python 服务，数据库使用 **PostgreSQL**。前端选用 React + TypeScript，沿用参考项目的前端技术路线；向量检索设计使用 pgvector。所有代码集中存放在当前 `flow-desk/` 工作区。

## 服务划分

| 目录 | 职责 | 规划端口 |
| --- | --- | --- |
| [flow-desk-web](flow-desk-web/README.md) | React 操作台、会话、审批、工单、知识库和任务轨迹 | 5173（开发） |
| [flow-desk-serv](flow-desk-serv/README.md) | go-zero API、身份权限、业务数据、任务调度、审批、业务工具与事件流 | 8080 |
| [flow-desk-agent](flow-desk-agent/README.md) | FastAPI + LangGraph、模型调用、RAG、工具编排、持久化执行 | 8000（内网） |
| [shared/contracts](shared/contracts/README.md) | 跨服务协议与生成产物的归档位置 | — |
| [deploy](deploy/README.md) | 本地及部署拓扑说明，后续存放 Compose 和代理配置 | — |

浏览器统一访问 Go；Python 通过受控的内部工具 API 使用业务能力。Go 拥有业务事务和对外任务状态，Python 拥有图执行检查点和检索索引。

## 文档入口

从 [设计文档索引](docs/README.md) 开始，主要文件如下：

- [总体详细设计](docs/design.md)：目标、范围、架构、业务流程、状态机与关键决策。
- [跨服务与前端接口契约](docs/contracts.md)：认证、API、事件、工具、审批与错误码。
- [数据模型与一致性设计](docs/data-model.md)：表、约束、索引、事务和恢复。
- [测试、评测与交付计划](docs/quality-and-roadmap.md)：数据集、验收、非功能目标与阶段划分。
- 各服务的实现设计位于对应目录的 `docs/design.md`。

协作规范见 [AGENTS.md](AGENTS.md)。Go 规范参考 `C:/work/go/src/rwa/rwa-tokenization-serv/AGENTS.md`，适配本项目目录和业务边界，不依赖参考项目的代码、子模块或运行环境。

## 本地启动

在 PowerShell 执行 `.\scripts\start.ps1`。默认打开 `http://localhost:13000`，账号 `demo.operator`，密码读取本地 `.env` 的 `DEMO_PASSWORD`。凭据由启动脚本随机生成，不会提交 Git。

运行、生成、测试和故障排查见 [本地运行说明](docs/runtime.md)。每阶段验收通过后提交并推送一次，M6 为可选扩展。设计文档包含完整目标范围，尚未实现的内容不代表当前功能。
