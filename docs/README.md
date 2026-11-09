# 设计文档索引

版本：v0.1；状态：M1–M5 业务实现与评测、M6.1–M6.5 本地专项已交付。实际功能及验收以 [实施记录](progress.md) 为准；开发见[本地运行](runtime.md)，服务器运维见[部署手册](../deploy/server.md)。

| 文档 | 内容 |
| --- | --- |
| [M7.23 任务历史归档与恢复](m7-run-history.md) | 热/冷载荷及检查点、显式确认、持久操作核对与永久执行封存 |
| [M7.25 Agent 独立迁移](m7-agent-migration.md) | 独立迁移账号/作业、旧卷所有权升级、运行DDL收紧与HNSW维护 |
| [M7.18 调查历史内容恢复](m7-investigation-restore.md) | 已记录旧快照人工确认恢复为新修订、并发保护及原回执重放 |
| [M7.17 调查完整编辑历史](m7-investigation-history.md) | 不可变修订快照、升级基线、当前授权及稳定分页 |
| [M7.16 巡检调查工作台](m7-investigation-worklist.md) | 状态筛选、稳定分页、逐项授权、离线查询及继续跟进 |
| [M7.15 巡检调查记录](m7-source-investigations.md) | 人工说明/跟进、修订保护、当前授权与回执恢复 |
| [M7.14 副本历史与登记归档](m7-source-history.md) | 全量副本分页、退休登记移表、旧回执兼容与升级回退 |
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
| [M7.9 知识库保留策略](m7-retention-policy.md) | 按库参数、策略修订、旧预览隔离及升级兼容 |

## 阅读与修改顺序

先阅读总体设计，再阅读接口与数据模型，最后阅读各服务实现设计。实现一期时，以质量文档中的 M1→M5 为顺序，每阶段单独验证。

接口行为、事件字段、状态枚举和数据所有权以跨服务文档为准；服务文档描述实现方式。改变跨服务语义时，先更新跨服务文档，再同步机器契约、实现和测试。

## 已确定与实施选型

- 用户已确定：FlowDesk 名称；前端与 Go/Python 两个后端；Go 使用 go-zero 并以 `goctl api new flowdesk` 生成；数据库使用 PostgreSQL；代码统一位于本工作区；协作规范见 [AGENTS.md](../AGENTS.md)。
- 本设计选型：React + TypeScript、Python + FastAPI + LangGraph、pgvector 向量检索；一期以 HTTP/JSON 与持久化事件交互。
- Go 基线：Go 1.27.1、goctl 1.10.2、go-zero 1.10.3；Python 3.12、Node 22/24。各服务依赖已由 go.sum、requirements.lock、pnpm-lock.yaml 固定。
- 各阶段的实际证据见实施记录；完整目标设计不等于当前已实现范围。
- [M7.6 账号与角色管理](m7-accounts.md)：账号启停/改密、多角色增量权限、会话撤销和管理员交接。
- [M7.10 完整历史与元数据归档](m7-history-archive.md)：完整版本分页、已清理版本详情归档与身份隔离。

- [M7.11 对象巡检与登记对账](m7-source-inventory.md)：版本范围的只读分页、未登记项、大小异常及受控列举权限。

- [M7.12 未登记副本人工核验与登记](m7-source-adoption.md)：恢复原件、显式确认、大小/hash 核验和同键回执。

M7.19 [调查历史分层归档](m7-investigation-history-archive.md)保留完整快照和恢复能力，无S3操作；升级后备份必须包含两层历史，已有冷快照时不能回退到仅读热表的旧镜像。操作及IP:port部署见[服务器手册§29](../deploy/server.md#29-调查历史分层归档)。

M7.20 [调查历史批量归档](m7-investigation-history-batch.md)支持同调查多选预览、原子确认及原键重试；复用两层历史备份，无S3操作。部署及回退见[服务器手册§30](../deploy/server.md#30-调查历史批量归档)。

M7.21 [任务生命周期盘点](m7-run-lifecycle.md)按当前授权展示消息、运行及checkpoint数量和保护原因；只读、不删除，操作见[服务器手册§31](../deploy/server.md#31-任务生命周期盘点)。

M7.22 [语义检索规模优化](m7-retrieval-scale.md)提供按租户/profile/维度维护的可选HNSW索引、默认精确及有界回退；实际基准见报告，部署与回退见[服务器手册§32](../deploy/server.md#32-可选语义检索索引)。

M7.25 [Agent独立迁移](m7-agent-migration.md)提供一次性迁移、checkpointer setup、运行权限只读门禁和旧卷显式升级；操作及跨版本回退限制见[服务器手册§33](../deploy/server.md#33-agent-独立迁移与运行权限收紧)。M7.23[任务历史归档与恢复](m7-run-history.md)已完成本地验收，操作与兼容边界见[服务器手册§34](../deploy/server.md#34-任务历史归档与恢复)。M7.24 OCR与版面联合解析仍待集成验收，以实施记录为准。
