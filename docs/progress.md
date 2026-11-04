# 实施记录

每阶段完成验收后提交并推送一次代码，保留远端仓库的初始历史。

| 阶段 | 状态 | 验收 |
| --- | --- | --- |
| M1 工程与契约 | 已完成 | 三服务健康、认证集成/HTTP smoke、契约生成与前端构建通过 |
| M2 只读任务 | 已完成 | 浏览器查询、SSE 回放、持久化队列/检查点、权限与配额测试通过 |
| M3 RAG 与真实模型 | 已完成 | 版本化索引/激活、检索引用、DeepSeek 实测与预算；见下方证据 |
| M4 办理与恢复 | 已完成 | 审批绑定快照、真实回执、取消/补充输入、重启恢复和故障验收 |
| M5 评测与展示 | 已完成 | 100 条固定任务、真实留出/对比/trace、性能和备份恢复实测 |
| M6 专项 | 扩展实施中 | M6.1–M6.4 已完成本地验收；M6.5 业务场景待实施 |

M6.1–M6.3 已按用户选定范围完成本地验收，未连接或部署服务器。设计及验收见 [M6 专项计划](m6-plan.md)和[M6.3 运维说明](m6-deployment-monitoring.md)。

## M6.4 团队与权限验收（2026-11-04）

- Go 新增团队、成员和任务工单资源记录；工单和知识库支持单团队范围，保留角色权限与租户边界。管理员在浏览器维护成员及资源范围；成员只能列出自己的团队。
- 当前成员资格用于列表、详情、事件、搜索、审批、执行、幂等响应及引用复核；失权的助手消息、审批快照、回执和 SSE 内容隐藏。执行租约刷新也重新检查任务工单权限，旧回执保持不可变。
- 23 项 Go 数据库集成和 vet、19 项 Agent Linux 回归、前端构建/3 项单测通过；原 3 项与新增 1 项 Chrome 用例分别通过。新增用例实测跨独立浏览器会话移除成员后的工单可见性。
- 追加两项迁移及默认 goctl 模型/API 生成、契约导出完成；旧搜索历史采用保守资源关联。范围、并发授权锁与升级限制见[设计](m6-teams.md)，实际命令和结果见[报告](../reports/m6/teams.md)。全部使用合成数据与 mock 模型，未执行服务器上线。

## M6.3 部署与监控验收（2026-11-04）

- 固定完整 SHA 镜像发布按迁移及 Agent/API/Web readiness 推进；保留成功 current/previous、未完成 pending 和发布锁。迁移失败测试验证不继续启动 Web、不改写成功状态，之后可显式回滚；不执行数据库 Down 或删除卷。
- 已发布 M6.1（474bc6f）→M6.2（d015a8e）→回滚 M6.1，三个步骤各通过 TLS 登录/查询并核对实际 API 镜像；全过程使用显式 mock override，见[发布报告](../reports/m6/release-local.json)。完成后恢复当前源码服务。
- TLS 下真实 Go/MCP 办理 4/4：补充输入、待审批重启恢复后原 action 建单、批准备注、拒绝和取消，以及外租户拒绝；重启到完成 6437ms，见[TLS 办理报告](../reports/m6/acceptance-tls-actions.json)。使用确定性模型与真实合成业务事务。
- OTel 串联 Go dispatch、持久 traceparent、Agent 节点和 Go/MCP 工具；Collector 执行属性白名单、清空状态消息和删除事件。两项指标 scrape、Grafana 数据源及 12 个面板查询、共享 trace、脱敏 canary、停止 Agent 后告警触发和恢复均通过，见[监控报告](../reports/m6/monitoring-local.json)。
- Agent 完整 Linux 隔离 schema 回归 19/19（9.67 秒）、Go 数据库集成及 vet、Ruff lint/format、pip check、契约导出、actionlint、promtool 和 Collector 配置检查通过。CI 新增监控/TLS 配置校验；镜像一次构建后向双仓库推送，对每个标签设置超时和有界重试。
- 保留首次演练的[配置失败记录](../reports/m6/release-invalid-environment.json)：Windows→WSL 未传递 mock 环境导致真实聊天配置被调用，3 次 MODEL_UNAVAILABLE，无 usage、费用 unknown。已修正为显式 Compose override，发起任务前断言实际容器 mock 且密钥为空；失败记录不计入通过结果。
- Jaeger 当前为临时内存存储，Grafana 使用已验收的 Prometheus 数据源，trace 由 Jaeger UI/v3 API 查询。正式域名证书、持久 trace 存储、监控访问控制、外部告警通知及真实服务器上线仍需独立实施，本阶段未执行。

## M6.2 MCP 验收（2026-11-03）

- 官方 MCP SDK 2.3.0 提供真实工具发现和调用；固定 stdio 子进程，不接受模型提供的命令/服务地址/身份。三个读工具与只接受 action_id 的执行工具仍通过 Go 当前授权、批准及幂等回执。
- 两项真实 SDK 协议/stdio 测试通过；完整 Linux Agent 回归 16/16（8.56 秒）、Go 数据库集成及 vet 通过。供应商依赖按现有版本约束安装，Windows/Linux 锁定兼容，pip check 和契约导出通过。
- 本地 MCP 链路 4/4，通过补充输入、待审批重启恢复、原 action 建单、备注、拒绝/取消与外租户拒绝。该链路采用 mock 模型和真实业务事务，未调用付费模型。报告见[MCP 验收](../reports/m6/acceptance-mcp.json)，协议/业务测试边界见[M6.2 说明](m6-mcp.md)。

## M6.1 语义 RAG 验收（2026-11-03）

- 独立兼容 embedding、租户/profile 缓存、持久预算/用量、语义 manifest、索引旧 owner 围栏及配置回退已实现；可选重排仅使用 Go 已验证候选，重排后再次复核权限。
- 24 条独立同义问题 Recall@5：字符基线 11/24，语义 23/24；加入重排首位命中 23/24，P95 2266ms。保留未召回的 defect 问题与一次评测器配置失败记录，不把小样本结果视为生产准确率。原始数据、usage 和 unknown 费用见[M6 报告](../reports/m6/summary.md)。
- 两个合成租户重建 27 个现有版本，本地开启 semantic/chat；真实 HTTP 政策问答 10/10，通过引用、跨租户拒绝、撤回后快照/SSE 隐藏断言。新文档自动建立语义索引；已有版本升级不改变 Go 激活状态。
- 数据和响应 schema 通过追加迁移及 Pydantic 导出生成，公开 API 无新增字段。使用方法、预算、回退及尚未实现的 PDF/OCR/近似索引见[M6.1 说明](m6-semantic-rag.md)。
- Linux 容器完整 Agent 回归 14/14（隔离 schema，4.86 秒），Ruff lint/格式、契约导出通过；旧基线测试显式指定 baseline，不受本地 semantic 开关影响。

## M1 验收（2026-11-02）

- `scripts/start.ps1`：三服务镜像成功构建；owner 迁移和六个合成账号初始化；Go/Python/PostgreSQL 健康检查通过，前端由 Nginx 提供。
- `scripts/check-go.ps1`：4 个真实数据库集成用例通过，覆盖轮换与复用撤销、退出与租户外键、后续写失败回滚、并发重放；`go vet ./...` 通过。
- `scripts/smoke.py`：真实前端代理的身份、Origin、Cookie、轮换、权限与退出检查通过。
- `scripts/export-contracts.ps1`、前端 build、Python Ruff 和 `pip check` 通过。模型生成在临时 schema 内完成并清理。
- 本阶段将固定角色实现为 `users.role`，没有提前实现多角色管理；LangGraph/checkpointer 只锁定依赖，尚未提供执行任务能力。未执行模型质量或性能评测。

提交主题：`feat(m1): bootstrap three services, identity and generated contracts`。提交号由 `git log` 查询，避免在提交内容中自引用。

## M2 验收（2026-11-02）

- 两个合成租户各有 50 个订单和物流记录，金额保持 NUMERIC/十进制字符串。
- 会话、消息、run、初始事件、outbox 和请求幂等记录同事务保存。每会话一个活跃任务，每用户最多 2 个、每租户最多 10 个活跃任务；每次任务最多 12 次读工具调用、120 秒期限。
- Python 使用 PostgreSQL 命令队列、回调 outbox、工具结果缓存和 LangGraph 检查点；执行锁与 checkpointer 共用连接。重放使用稳定 dispatch/source/tool_call ID，失去所有权的 worker 不能提交失败结论。
- `scripts/check-go.ps1`：8 个真实数据库集成用例及 `go vet` 通过。新增用例覆盖幂等/原子 outbox、并发/配额、事件冲突/回滚/快照和租户/对象/即时权限撤销。
- `scripts/check-agent.py`：3 个用例通过，包括独立 schema 中的命令接管、回调顺序与重投，以及更换检查点连接后复用已持久化工具事实。
- 前端 3 个 SSE 解析/校验用例、生产构建及 Chrome E2E 通过。E2E 主动中断一次事件请求，验证查询、中文输入、重连、刷新后的结果和退出清理。
- `scripts/smoke-tasks.py` 和 M1 `scripts/smoke.py` 通过，覆盖真实三服务链路、SSE Last-Event-ID 回放、租户隔离与服务凭据边界。
- 当前使用确定性假模型，尚无政策检索、真实模型、写工具或审批。M3 真实模型接口已按用户选择确定为兼容 OpenAI 协议。

实现细节及可重跑命令见 [M2 交付说明](m2-read-tasks.md)。

## M3 验收（2026-11-03）

- 管理员可创建固定角色 ACL 知识库、上传 UTF-8 TXT/Markdown、查看索引状态、追加版本、按 expected_version 激活或撤回。上传和 outbox 原子写入；索引命令持久化、租约代次隔离，丢失回调恢复不重复分块，旧回调不自动改变激活版本。
- 租户/有效版本在全文和向量排序前过滤，RRF 融合候选；Go 登记并复核 run_evidence，拒绝伪造片段、外 run 引用和已失权引用。撤回后引用、消息/run 快照及 SSE 回放隐藏政策答复。
- 接入用户配置的 DeepSeek `deepseek-chat` / JSON mode；Pydantic 验证计划与答复，引用 ID 必须来自当次证据。每次调用尝试持久化预算预留和 usage，恢复复用成功结果，密钥只在本地环境配置。
- `scripts/check-go.ps1`：10 个数据库集成用例及 vet 通过；新增激活/幂等、伪造证据、撤回/回放、ACL 和租户用例。
- `scripts/check-agent.py`：6 个用例通过；新增混合检索权限过滤、索引回调丢失、模型限流/输出失败/缓存/预算，以及检查点 pipeline 与所有权检查的互斥回归。验收中发现并修复了共享连接并发进入 pipeline 的问题。
- Ruff、格式检查、pip check、前端生产构建与 3 个 SSE 单测通过。2 个 Chrome E2E 通过：上传/激活/引用/撤回/刷新，以及订单查询中断 SSE 后重连和退出。当前会话按 tenant/user 保存在 sessionStorage，刷新不受其他并行会话创建影响。
- 固定 10 条合成政策问答：mock 10/10；真实 DeepSeek 10/10，均验证预期关键词、文档版本引用、外租户拒绝和撤回后快照/回放。报告分别见 [mock](../reports/m3/acceptance-mock.json) 与 [真实模型](../reports/m3/acceptance-openai-compatible.json)，包含实际 tokens。费用 unknown，不猜测供应商价格；这不是 M5 留出集质量结论。
- 当前检索向量是 256 维字符 n-gram 词面基线，不是语义 embedding；PDF/OCR、对象存储、索引物理删除和语义 embedding 尚未实现。具体边界、端点及复跑方法见 [M3 说明](m3-rag.md)。

提交主题：`feat(m3): add versioned knowledge, cited retrieval and budgeted model calls`。

## M4 验收（2026-11-03）

- 建单、备注、指派和工单状态维护均由 Go 执行业务事务。Agent 写入必须先准备不可变动作快照，经当前会话所有者批准后，按 action_id 执行；Go 再检查当前权限、资源版本和政策证据。工单、事件、审计、回执和公开进度同事务保存。
- 补充输入、待审批、批准/拒绝、取消和 24 小时等待超时有持久化状态；批准/补充回复与恢复 outbox 原子写入。Python 中断前保存检查点，等待时释放 worker；恢复沿用同一图线程，执行代次和数据库执行锁隔离旧 worker。
- `scripts/check-go.ps1`：18 个真实数据库集成用例和 vet 通过。覆盖未批准禁止写、动作不可变、回执字节一致重放、确认/取消竞争、已提交效果后取消、输入次序、权限/依据撤销、资源变化、事务回滚及丢失 202 后原 dispatch 重投。
- `scripts/check-agent.py`：9 个用例通过；新增 prepare 后和业务效果后两次崩溃恢复、补充输入恢复、代次不能倒退、执行锁连接断开与持久化取消。Ruff、格式及依赖检查通过。
- 前端生产构建、3 个 SSE 单测和 3 个 Chrome E2E 通过。办理用例实际丢弃已提交审批的 HTTP 响应，验证重新读取状态、唯一回执、刷新恢复、备注详情/事件，以及拒绝/取消后无新增效果。
- `scripts/smoke-actions.py --restart-agent`：4/4 真实 HTTP 链路通过，在待审批状态重启 Agent 后批准，验证原审批/原 action、唯一建单/备注效果、跨租户拒绝、拒绝与取消。报告见 [M4 mock 验收](../reports/m4/acceptance-mock.json)。本阶段恢复验收使用确定性模型；不代表真实模型写入质量评分。
- 验收修复了 JSONB 重排字段引起的审批摘要不一致、备注参数中路径字段不能由 JSON 自动解码，以及带修饰语的建单意图未匹配。详细端点、约束、边界与故障映射见 [M4 说明](m4-actions.md)和[故障矩阵](m4-fault-matrix.md)。

提交主题：`feat(m4): add approved ticket actions, receipts and durable recovery`。

## M5 验收（2026-11-03）

- 固定 JSONL 100 条合成任务，按类别分为事实 20、政策 20、补全 15、办理 20、权限/恶意内容 15、恢复 10；60/20/20 开发、回归、留出划分。Schema、唯一 ID、类别数量、SHA256 与留出任务 ID 校验通过。
- M4 mock 基线开发/回归完成 58/80；修复答复遗漏已查工单、mock 无关引用和恶意文字回显后，同一 80 条为 80/80，完整 mock 为 100/100。真实 DeepSeek 原 20 条留出为 20/20。最终工具断言 91/91、19/19；引用支持断言 23/23、5/5；未观测到未批准写、重复效果或定义的禁止标记回显。
- 保留评分器契约错误的原始 draft，以及并行浏览器写入导致计数干扰的整轮无效报告和核对证据；最终 mock 轮次独立运行。没有丢弃产品失败项或把 mock 分数当成真实模型质量。
- 相同 20 个政策问题变体：词面与 RRF Recall@5 均 20/20，没有测得召回提升。向量依旧是字符词面基线。真实评分 run 的 40 次模型调用使用 input 34,841 / output 2,658 tokens；费用 unknown；单次调用 P50/P95 1,152/1,991ms，含模拟批准、轮询及校验的任务 P50/P95 10,860/13,484ms。
- 独立临时数据库和原生 Windows Go API：单租户 10,000 订单/10,000 工单、20 并发、1,000 请求，P95 32ms、错误 0；临时库/进程清理成功。2 租户/5 actor/10 活跃任务的 202→run.started SSE P95 563ms；重启 Agent→原审批任务完成 5,203ms，均达到初始目标。
- PostgreSQL 一致性快照 dump 恢复到新临时库，13 张关键表行数与内容摘要一致，临时库清理成功。备份仅位于 Git 忽略目录。节点/工具/模型 metadata trace 已导出，不含提示词、工具正文或私有推理；trace_id 使用稳定 run_id，没有部署 OTel collector。
- Agent 10 个数据库集成用例通过，含新的追踪失败/中断与隐私字段断言；Ruff lint/format、pip check、前端生产构建/3 个 SSE 单测/3 个 Chrome E2E 通过。Go 代码沿用 M4 已通过的 18 个数据库集成用例及 vet；本阶段没有修改 Go 行为或 API 契约。工作台会话列表改为独立滚动，发送/状态文案覆盖业务任务。
- `scripts/report-evaluation.py` 的质量、权限/重复/标记、检索、性能、恢复和数据集门槛通过。报告见 [M5 汇总](../reports/m5/summary.md)，复跑及局限见 [M5 说明](m5-evaluation.md)，演示/排障见[运维手册](demo-and-operations.md)。这是本机固定合成样本的单次实测，真实留出未覆盖全部子场景；M6 未实施。

提交主题：`feat(m5): add fixed evaluations, traces and measured delivery reports`。
