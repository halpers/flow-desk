# 设计文档索引

版本：v0.1；状态：M1–M5 业务实现与评测、M6.1–M6.5 本地专项已交付。实际功能及验收以 [实施记录](progress.md) 为准；开发见[本地运行](runtime.md)，服务器运维见[部署手册](../deploy/server.md)。

| 文档 | 内容 |
| --- | --- |
| [总体详细设计](design.md) | 产品目标、一期范围、服务架构、业务流程、状态机、部署决策 |
| [接口契约](contracts.md) | 浏览器 API、内部执行 API、工具协议、SSE、认证与错误语义 |
| [数据模型](data-model.md) | 数据所有权、字段、唯一约束、事务、租约、幂等与保留策略 |
| [质量与交付](quality-and-roadmap.md) | 评测集、测试矩阵、性能目标、交付阶段与招聘能力映射 |
| [前端详细设计](../flow-desk-web/docs/design.md) | 路由、页面交互、状态、事件恢复、确认卡片和前端测试 |
| [Go 服务详细设计](../flow-desk-serv/flowdesk/docs/design.md) | go-zero 分层、模型、事务、调度器、鉴权与配置 |
| [Python 服务详细设计](../flow-desk-agent/docs/design.md) | 图节点、工具适配、检索、检查点、模型调用与评测实现 |
| [服务器部署手册](../deploy/server.md) | Ubuntu 单机、IP:端口、HTTP/HTTPS、初始化、发布、验收、监控及恢复 |
| [协议来源](../shared/contracts/README.md) | 契约唯一来源与生成流水线 |
| [M5 评测](m5-evaluation.md) | 固定任务、评分断言、模型/检索/性能报告及复跑方式 |
| [演示与运维](demo-and-operations.md) | 正常办理、失败恢复、备份恢复和排障边界 |
| [M6 专项计划](m6-plan.md) | 语义 RAG、MCP、部署配置与本地监控的顺序及验收 |
| [M6.1 语义 RAG](m6-semantic-rag.md) | embedding 预算、索引重建、重排、配置回退与实测 |
| [M6.2 MCP](m6-mcp.md) | 官方 SDK、工具发现、stdio 生命周期、Go 授权与审批执行 |
| [M6.3 部署与监控](m6-deployment-monitoring.md) | 固定镜像发布/回滚、TLS、OTel、指标、告警与本地验收 |
| [M6.4 团队权限](m6-teams.md) | 团队成员、工单/知识范围、撤权、历史兼容与授权锁 |
| [M6.5 业务场景](m6-business-scenarios.md) | 物流建单、接手、办结、图版本兼容与固定场景 |
| [M7.1 PDF 知识文件](m7-knowledge-files.md) | 文本 PDF 上传、受限解析、持久 manifest、预览与物理页码引用 |
| [M7.2 任务运营台](m7-run-operations.md) | 授权元数据列表、脱敏执行记录、用量未知标记和受限重试 |
| [M7.3 扫描 PDF OCR](m7-ocr.md) | 中英文识别、来源、人工审核和有界进程清理 |
| [M7.4 原文件存储](m7-object-storage.md) | 可选 S3 归档、授权附件下载、事务恢复及存储回退 |
| [M7.5 规则版面](m7-pdf-layout.md) | 双栏/表格提取、人工核对和回调恢复 |
| [M7.6 账号与角色](m7-accounts.md) | 多角色、增量权限、会话撤销及管理员保护 |
| [M7.7 版本保留与清理](m7-retention.md) | 保留预览、引用保护、人工清理及永久索引墓碑 |
| [M7.8 归档副本清理](m7-source-cleanup.md) | 对象登记、恢复后清理、条件写墓碑和归档代次隔离 |

## 阅读与修改顺序

先阅读总体设计，再阅读接口与数据模型，最后阅读各服务实现设计。实现一期时，以质量文档中的 M1→M5 为顺序，每阶段单独验证。

接口行为、事件字段、状态枚举和数据所有权以跨服务文档为准；服务文档描述实现方式。改变跨服务语义时，先更新跨服务文档，再同步机器契约、实现和测试。

## 已确定与实施选型

- 用户已确定：FlowDesk 名称；前端与 Go/Python 两个后端；Go 使用 go-zero 并以 `goctl api new flowdesk` 生成；数据库使用 PostgreSQL；代码统一位于本工作区；协作规范见 [AGENTS.md](../AGENTS.md)。
- 本设计选型：React + TypeScript、Python + FastAPI + LangGraph、pgvector 向量检索；一期以 HTTP/JSON 与持久化事件交互。
- Go 基线：Go 1.27.1、goctl 1.10.2、go-zero 1.10.3；Python 3.12、Node 22/24。各服务依赖已由 go.sum、requirements.lock、pnpm-lock.yaml 固定。
- 各阶段的实际证据见实施记录；完整目标设计不等于当前已实现范围。
- [M7.6 账号与角色管理](m7-accounts.md)：账号启停/改密、多角色增量权限、会话撤销和管理员交接。
