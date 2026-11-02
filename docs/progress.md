# 实施记录

每阶段完成验收后提交并推送一次代码，保留远端仓库的初始历史。

| 阶段 | 状态 | 验收 |
| --- | --- | --- |
| M1 工程与契约 | 已完成 | 三服务健康、认证集成/HTTP smoke、契约生成与前端构建通过 |
| M2 只读任务 | 待开始 | 消息/任务/工具、SSE、队列与权限 |
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
