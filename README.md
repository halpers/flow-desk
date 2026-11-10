# FlowDesk（智办台）

[OCR与版面联合处理](docs/m7-ocr-layout.md)支持扫描双栏先左后右、简单表格保持同行及原生/扫描混合页，激活同时要求核对OCR和阅读顺序。支持范围和已知误识别见专项说明；配套版本升级与回退见[服务器手册§35](deploy/server.md#35-ocr与版面联合处理)。

[Agent 独立迁移](docs/m7-agent-migration.md)将建表、checkpointer 升级和 HNSW 维护交给一次性迁移服务；运行 Agent 只保留必要读写权限。已有数据卷首次升级须先执行[服务器手册§33](deploy/server.md#33-agent-独立迁移与运行权限收紧)的所有权升级。

[语义检索规模优化](docs/m7-retrieval-scale.md)支持显式维护租户隔离的HNSW索引；默认精确检索，未就绪或候选不足时回退。合成规模基准及召回差异见专项文档。

[任务生命周期盘点](docs/m7-run-lifecycle.md)可从任务详情只读查看消息、执行及 checkpoint 数量与保留保护原因。另由[任务历史归档与恢复](docs/m7-run-history.md)提供管理员显式确认，将符合 30 天及关联保护条件的终态只读任务完整转入历史存储并按需恢复；消息、事件回放和计数保持，恢复后执行身份仍永久封存。维护核对、备份及配套版本限制见[服务器手册§34](deploy/server.md#34-任务历史归档与恢复)。
[调查历史批量归档](docs/m7-investigation-history-batch.md)支持当前页多选、逐项预览和整批确认，任一项冲突整批回滚，响应不确定可原键恢复回执。保留完整历史和单项恢复能力，部署操作见[服务器手册§30](deploy/server.md#30-调查历史批量归档)。

[调查历史分层归档](docs/m7-investigation-history-archive.md)支持逐项确认，将已结束调查中满30天且不在最近3个修订内的旧快照移入独立归档表。完整内容仍可分页查看与恢复，当前调查不变；减少热表行数，历史总量继续保留。升级、备份和回退见[服务器手册§29](deploy/server.md#29-调查历史分层归档)。

企业智能工单与业务办理 Agent。用户以自然语言描述售后问题，系统查询订单、检索有效政策、生成操作方案，经确认后创建或更新工单，并保留可回放的执行记录。

## 当前交付

[调查历史内容恢复](docs/m7-investigation-restore.md)支持核对旧快照并显式确认，将说明和状态保存为新修订。已有历史继续保留，响应丢失可原键重试；升级及操作见[服务器手册§28](deploy/server.md#28-调查历史内容恢复)。

M7 增加 PDF/OCR、规则版面、S3 原件归档、任务运营台和账号角色管理。[版本保留与清理](docs/m7-retention.md)提供人工预览确认、最近版本/引用保护及可恢复的数据库原文与索引清理。[归档副本清理](docs/m7-source-cleanup.md)可在原件恢复至数据库后，人工清除已登记旧 S3 副本的文件字节并保留零字节防重写标记；默认关闭。

[知识库保留策略](docs/m7-retention-policy.md)支持按库配置保留版本数和最短天数，默认 3 个、30 天。策略保存后旧清理预览失效，仍需逐版本人工确认。

[完整历史与元数据归档](docs/m7-history-archive.md)支持按页查看全部版本，并将已清理、无引用且无归档存储历史的版本详情移入独立归档表。热列表保留日常版本，历史详情仍可查询；身份、防重放记录和审计继续保留。

[完整副本历史与退休登记归档](docs/m7-source-history.md)覆盖全部已登记副本，并支持将已完成清理的旧登记移入独立历史表。对象身份、旧回执和存储标记继续保留；操作及升级回退见[服务器手册 §24](deploy/server.md#24-完整副本历史与退休登记归档)。

[租户对象巡检](docs/m7-tenant-source-inventory.md)可发现当前租户前缀中版本记录未知或非标准的对象，只向内置管理员开放；已知版本继续受知识库权限控制。结果供人工核对，默认关闭，不自动登记或删除。

[巡检调查记录](docs/m7-source-investigations.md)支持人工说明、跟进状态、修订冲突保护及同键响应恢复；对象消失或巡检暂停后仍可按指纹跟进。操作与备份见[服务器手册 §25](deploy/server.md#25-巡检调查记录)。

[巡检调查工作台](docs/m7-investigation-worklist.md)提供当前租户调查的状态筛选、分页和继续跟进入口，存储离线或巡检暂停仍可查询。部署升级与故障处理见[服务器手册 §26](deploy/server.md#26-巡检调查工作台)。

[调查完整编辑历史](docs/m7-investigation-history.md)保留升级后的每次成功修订，按页查看说明、状态、操作人和时间；旧记录仅以升级时最新内容作为基线，更早修改无法补回。部署与备份见[服务器手册 §27](deploy/server.md#27-调查完整编辑历史)。

[对象巡检与登记对账](docs/m7-source-inventory.md)支持按已知 PDF 版本分页观察 S3 对象，标出未登记副本和大小异常；只读、默认关闭，启用步骤见[服务器手册 §21](deploy/server.md#21-s3-对象巡检与登记对账)。 [人工核验与登记](docs/m7-source-adoption.md)可在恢复原件后，对标准旧副本逐项确认并校验 SHA256，匹配后登记；独立开关默认关闭。

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

根目录 [docker-compose.yaml](docker-compose.yaml) 依次编排 PostgreSQL/pgvector、Go 迁移、独立 Agent 迁移、Python Agent、Go API 和 Web；启动脚本等待服务健康后返回。新环境随机生成独立迁移凭据；已有 `.env` 和数据卷先按上述手册升级，再执行 `docker compose up --detach --build --wait`。各服务默认端口及操作见 [部署说明](deploy/README.md)。

[CI/CD](deploy/ci-cd.md) 在 PR 和 `main` 推送时执行测试与 Compose 启动检查；仅推送 `v*` tag 且检查通过后，才将三个服务镜像发布到 GHCR 和阿里云 ACR，同时保留版本和完整 SHA 标签。手动运行只检查，不发布；`latest` 不再更新。发布步骤见 CI/CD 文档。阿里云推送凭据使用仓库 Secrets `ACR_USERNAME`、`ACR_PASSWORD`；公网地址和命名空间已配置默认值。

运行、生成、测试和故障排查见 [本地运行说明](docs/runtime.md)。M6.1–M6.5 已按[专项计划](docs/m6-plan.md)完成本地验收并分别提交。设计文档包含完整目标范围，尚未实现的内容不代表当前功能。

## 评测与演示

固定 100 条合成任务按 60/20/20 划分开发、回归和留出集，分别报告 mock 功能断言与 DeepSeek 真实留出结果。复跑步骤见 [M5 评测](docs/m5-evaluation.md)，查询→检索→确认→真实工单及失败恢复的展示步骤见[演示与运维](docs/demo-and-operations.md)。模型费用未知时标为 unknown，密钥仅放在本地 `.env`。

M6.1 增加独立语义 embedding、全文 RRF 与可选重排，保留字符基线及配置回退；配置和索引升级见[语义 RAG](docs/m6-semantic-rag.md)。M6.2 提供官方 SDK 的[MCP 工具发现与调用](docs/m6-mcp.md)，保持 Go 授权与审批事务。M6.3 提供[固定镜像发布/回滚、TLS 与本地监控](docs/m6-deployment-monitoring.md)。实测见[M6 报告](reports/m6/summary.md)。团队共享权限已在 M6.4 交付。

M7.1 增加[文本 PDF 上传、提取文本预览与物理页码引用](docs/m7-knowledge-files.md)，保留版本激活、权限及撤回校验。M7.3 增加[中英文扫描 PDF OCR](docs/m7-ocr.md)，识别后由管理员核对并确认激活。M7.4 增加[可选 S3 原文件归档、授权下载与恢复](docs/m7-object-storage.md)，默认仍存 PostgreSQL，运维步骤见[文件存储部署](deploy/source-storage.md)。M7.5 增加[规则双栏阅读顺序和表格文本行列对齐](docs/m7-pdf-layout.md)，M7.24扩展为可与auto/force OCR联合使用，需要核对文字和版面后激活。任意复杂版面、跨页合并表格、手写和正式服务器上线仍待后续实施。性能数字仅适用于报告中的本机合成环境。

M7.2 增加[任务运营台](docs/m7-run-operations.md)：任务筛选、脱敏节点/工具/模型用量记录，以及没有准备过业务动作的失败任务重试。主管可查看当前授权范围内的租户任务元数据，原会话和办理权限仍归所有者。

M7.6 增加[账号与角色管理](docs/m7-accounts.md)：新增/启停账号、重置密码、自定义增量角色及多角色分配。变更撤销相关会话，保留最后一个管理员；自定义权限继续受团队和知识库 ACL 约束。运维交接和回退见[服务器手册 §16](deploy/server.md#16-账号与角色管理升级及交接)。

M7.26 增加[任务历史批量归档](docs/m7-run-history-batch.md)：管理员多选至多10项，统一预览确认，逐项查看结果；刷新后从最近批次继续。消息、事件和检查点完整保留，执行身份永久封存，单项恢复后旧批次回执不重复归档。部署与恢复步骤见[服务器手册§36](deploy/server.md#36-任务历史批量归档)。

M7.27增加[任务历史批量恢复](docs/m7-run-history-batch-restore.md)：管理员统一预览确认后登记恢复批次，按原操作逐项恢复，可从最近批次继续未完成项。恢复只还原存储，旧执行身份仍永久封存。运维见[服务器手册§37](deploy/server.md#37-任务历史批量恢复)。

M7.28增加[用户自助修改密码](docs/m7-self-password.md)：当前登录用户验证原密码后修改本人凭据，全部会话同事务撤销，成功或结果不确定时转到重新登录。管理员重置密码仍使用账号管理入口，运维见[服务器手册§38](deploy/server.md#38-用户自助修改密码)。

M7.29提供[PostgreSQL共享持久认证限流](docs/m7-auth-rate-limits.md)：登录与本人改密在多个API实例间共享原额度、重启保持，429返回Retry-After，数据库失败时拒绝后续认证业务。

[定时任务历史归档](docs/m7-run-history-schedule.md)增加默认关闭的调度总开关及租户策略，管理员明确授权保留期/间隔后自动有界处理首次归档候选。多实例和重启复用原操作，持续权限失效即停止，人工恢复后不再自动选中。部署、暂停及备份恢复见[服务器手册§40](deploy/server.md#40-定时任务历史归档)。

[超时调查自动回待办](docs/m7-investigation-automation.md)提供默认关闭的持续授权规则：调查中超过阈值未更新时回到待跟进，保留原说明与完整编辑历史。升级、启用和暂停见[服务器手册§41](deploy/server.md#41-超时调查自动回待办)。
