# FlowDesk（智办台）

企业智能工单与业务办理 Agent。用户以自然语言描述售后问题，系统查询订单、检索有效政策、生成操作方案，经确认后创建或更新工单，并保留可回放的执行记录。

## 当前交付

M7 增加 PDF/OCR、规则版面、S3 原件归档、任务运营台和账号角色管理。[版本保留与清理](docs/m7-retention.md)提供人工预览确认、最近版本/引用保护及可恢复的数据库原文与索引清理。[归档副本清理](docs/m7-source-cleanup.md)可在原件恢复至数据库后，人工清除已登记旧 S3 副本的文件字节并保留零字节防重写标记；默认关闭。

[知识库保留策略](docs/m7-retention-policy.md)支持按库配置保留版本数和最短天数，默认 3 个、30 天。策略保存后旧清理预览失效，仍需逐版本人工确认。

[完整历史与元数据归档](docs/m7-history-archive.md)支持按页查看全部版本，并将已清理、无引用且无归档存储历史的版本详情移入独立归档表。热列表保留日常版本，历史详情仍可查询；身份、防重放记录和审计继续保留。

M6.1–M6.5 已完成本地专项实施：语义检索、MCP、固定镜像发布/回滚、TLS/监控、团队权限，以及物流建单→接手处理→记录结论办结。新动作必须精确批准并由 Go 校验当前权限、状态、负责人及版本；固定 TLS/MCP 场景 12/12 通过，见[业务验收报告](reports/m6/business-scenarios.md)和[团队权限报告](reports/m6/teams.md)。未部署到真实服务器。

M1–M4 已交付：登录与租户权限、持久化任务/SSE、版本化知识与有效引用、兼容 OpenAI 协议的模型接入，以及经批准的建单/备注、幂等回执、取消和重启恢复。Go 工程最初按要求使用 `goctl api new flowdesk` 生成。M5 的固定任务、质量/性能报告及实际证据见 [实施记录](docs/progress.md)和[评测报告](reports/m5/summary.md)。

用户已确定 Go 业务后端使用 **go-zero**，Agent 使用独立 Python 服务，数据库使用 **PostgreSQL**。前端选用 React + TypeScript；向量检索设计使用 pgvector。所有代码集中存放在当前 `flow-desk/` 工作区。

## 服务划分

| 目录 | 职责 | 规划端口 |
| --- | --- | --- |
| [flow-desk-web](flow-desk-web/README.md) | React 操作台、会话、审批、工单、知识库和任务轨迹 | 5173（开发） |
| [flow-desk-serv](flow-desk-serv/README.md) | go-zero API、身份权限、业务数据、任务调度、审批、业务工具与事件流 | 8080 |
| [flow-desk-agent](flow-desk-agent/README.md) | FastAPI + LangGraph、模型调用、RAG、工具编排、持久化执行 | 8000（内网） |
| [shared/contracts](shared/contracts/README.md) | 跨服务协议与生成产物的归档位置 | — |
| [deploy](deploy/README.md) | 部署拓扑说明、PostgreSQL 初始化和镜像发布说明 | — |

浏览器统一访问 Go；Python 通过受控的内部工具 API 使用业务能力。Go 拥有业务事务和对外任务状态，Python 拥有图执行检查点和检索索引。

## 文档入口

从 [设计文档索引](docs/README.md) 开始，主要文件如下：

- [总体详细设计](docs/design.md)：目标、范围、架构、业务流程、状态机与关键决策。
- [跨服务与前端接口契约](docs/contracts.md)：认证、API、事件、工具、审批与错误码。
- [数据模型与一致性设计](docs/data-model.md)：表、约束、索引、事务和恢复。
- [测试、评测与交付计划](docs/quality-and-roadmap.md)：数据集、验收、非功能目标与阶段划分。
- 各服务的实现设计位于对应目录的 `docs/design.md`。

协作规范见 [AGENTS.md](AGENTS.md)，涵盖项目目录、服务边界和代码约定。

## 本地启动

运维部署到 Linux 服务器请按[服务器部署手册（IP:端口）](deploy/server.md)操作，默认支持 HTTP IP 入口，也支持配置 IP 证书的 HTTPS；无需先配置域名。

在 PowerShell 执行 `.\scripts\start.ps1`。默认打开 `http://localhost:13000`，账号 `demo.operator`，密码读取本地 `.env` 的 `DEMO_PASSWORD`。凭据由启动脚本随机生成，不会提交 Git。

根目录 [docker-compose.yaml](docker-compose.yaml) 编排 PostgreSQL/pgvector、一次性迁移、Python Agent、Go API 和 Web；启动脚本等待服务健康后返回。已有 `.env` 且本机可使用 Docker 时，也可执行 `docker compose up --detach --build --wait`。各服务默认端口及操作见 [部署说明](deploy/README.md)。

[CI/CD](deploy/ci-cd.md) 在 PR 和 `main` 推送时执行测试与 Compose 启动检查；仅推送 `v*` tag 且检查通过后，才将三个服务镜像发布到 GHCR 和阿里云 ACR，同时保留版本和完整 SHA 标签。手动运行只检查，不发布；`latest` 不再更新。发布步骤见 CI/CD 文档。阿里云推送凭据使用仓库 Secrets `ACR_USERNAME`、`ACR_PASSWORD`；公网地址和命名空间已配置默认值。

运行、生成、测试和故障排查见 [本地运行说明](docs/runtime.md)。M6.1–M6.5 已按[专项计划](docs/m6-plan.md)完成本地验收并分别提交。设计文档包含完整目标范围，尚未实现的内容不代表当前功能。

## 评测与演示

固定 100 条合成任务按 60/20/20 划分开发、回归和留出集，分别报告 mock 功能断言与 DeepSeek 真实留出结果。复跑步骤见 [M5 评测](docs/m5-evaluation.md)，查询→检索→确认→真实工单及失败恢复的展示步骤见[演示与运维](docs/demo-and-operations.md)。模型费用未知时标为 unknown，密钥仅放在本地 `.env`。

M6.1 增加独立语义 embedding、全文 RRF 与可选重排，保留字符基线及配置回退；配置和索引升级见[语义 RAG](docs/m6-semantic-rag.md)。M6.2 提供官方 SDK 的[MCP 工具发现与调用](docs/m6-mcp.md)，保持 Go 授权与审批事务。M6.3 提供[固定镜像发布/回滚、TLS 与本地监控](docs/m6-deployment-monitoring.md)。实测见[M6 报告](reports/m6/summary.md)。团队共享权限已在 M6.4 交付。

M7.1 增加[文本 PDF 上传、提取文本预览与物理页码引用](docs/m7-knowledge-files.md)，保留版本激活、权限及撤回校验。M7.3 增加[中英文扫描 PDF OCR](docs/m7-ocr.md)，识别后由管理员核对并确认激活。M7.4 增加[可选 S3 原文件归档、授权下载与恢复](docs/m7-object-storage.md)，默认仍存 PostgreSQL，运维步骤见[文件存储部署](deploy/source-storage.md)。M7.5 增加[规则双栏阅读顺序和表格文本行列对齐](docs/m7-pdf-layout.md)，需要选择方式并核对后激活。任意复杂版面还原和正式服务器上线仍待后续实施。性能数字仅适用于报告中的本机合成环境。

M7.2 增加[任务运营台](docs/m7-run-operations.md)：任务筛选、脱敏节点/工具/模型用量记录，以及没有准备过业务动作的失败任务重试。主管可查看当前授权范围内的租户任务元数据，原会话和办理权限仍归所有者。

M7.6 增加[账号与角色管理](docs/m7-accounts.md)：新增/启停账号、重置密码、自定义增量角色及多角色分配。变更撤销相关会话，保留最后一个管理员；自定义权限继续受团队和知识库 ACL 约束。运维交接和回退见[服务器手册 §16](deploy/server.md#16-账号与角色管理升级及交接)。
