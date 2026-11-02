# 实施记录

每阶段完成验收后提交并推送一次代码，保留远端仓库的初始历史。

| 阶段 | 状态 | 验收 |
| --- | --- | --- |
| M1 工程与契约 | 已完成 | 三服务健康、认证集成/HTTP smoke、契约生成与前端构建通过 |
| M2 只读任务 | 已完成 | 浏览器查询、SSE 回放、持久化队列/检查点、权限与配额测试通过 |
| M3 RAG 与真实模型 | 待开始 | 版本化索引、引用、模型与预算 |
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
