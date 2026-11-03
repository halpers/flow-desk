# 实施记录

每阶段完成验收后提交并推送一次代码，保留远端仓库的初始历史。

| 阶段 | 状态 | 验收 |
| --- | --- | --- |
| M1 工程与契约 | 已完成 | 三服务健康、认证集成/HTTP smoke、契约生成与前端构建通过 |
| M2 只读任务 | 已完成 | 浏览器查询、SSE 回放、持久化队列/检查点、权限与配额测试通过 |
| M3 RAG 与真实模型 | 已完成 | 版本化索引/激活、检索引用、DeepSeek 实测与预算；见下方证据 |
| M4 办理与恢复 | 待开始 | 审批、幂等、取消、中断恢复 |
| M5 评测与展示 | 待开始 | 固定数据集、质量/故障/性能报告 |

M6 是可选扩展，不属于本轮默认实施范围。

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
