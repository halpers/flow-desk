# M6.5 物流跟进与售后办结验收

日期：2026-11-04。环境：本机 Windows Go、WSL Docker/PostgreSQL、Chrome、开发 TLS。采用确定性模型和合成业务数据；每次 HTTP 验收前断言实际容器模型为 mock、聊天及 embedding 密钥为空。本阶段没有调用付费模型或部署服务器。

交付新增 `ticket.start_processing`、`ticket.resolve`；沿用准备→精确批准→Go 当前授权及版本复核→业务事务→唯一回执。办结原子保存处理结论与状态，接手使用可信当前 actor；模型不能指定身份或任意状态。新任务采用 ticket-v2，既有图和已批准动作保持原版本恢复。

| 验证 | 实际结果 |
| --- | --- |
| `scripts/check-go.ps1` | 27 项真实数据库集成测试及 vet 通过，最终 27.542 秒；新增事务回滚、并发版本竞争、原回执重放及三种图版本恢复 |
| Linux Agent 完整回归 | 23/23，13.87 秒；两个新动作各验证 prepare 后和业务提交后崩溃恢复；另覆盖歧义、错误编号更正及兼容模型版本校验 |
| MCP 协议/stdio | 现有两项测试增加新工具发现及精确业务错误断言；修复 SDK 上下文包装 ToolError 后误报结果未知 |
| 固定 TLS/MCP HTTP 场景 | [最终报告](business-scenarios.json) 12/12，包含物流建单、接手、办结、多工单补充、拒绝、取消、非法状态、角色拒绝、版本冲突、团队/租户拒绝及他人负责工单拒绝 |
| 待审批重启 | 原审批/动作保留，重启到完成 7,984ms；丢弃批准响应后按同一幂等键重发，状态只变化一次 |
| 前端静态与单测 | TypeScript/生产构建通过，SSE 单测 3/3 |
| Chrome 浏览器回归 | 5/5，约 1.5 分钟；新增物流建单→接手→办结用例 30.4 秒，验证待审批刷新、实际丢失批准响应、三张回执及唯一处理记录 |
| 契约与数据 | 有序追加迁移、临时 schema 模型生成并清理、契约导出；保留历史回执且不执行破坏性 Down |
| 文档及静态检查 | Ruff lint/format、Go formatting、JSON 与 Markdown 本地链接/代码块检查通过 |

数据集是 [m6-business.jsonl](../../flow-desk-agent/eval/datasets/m6-business.jsonl)，报告记录 SHA256、每项耗时和业务 ID。验收脚本 `scripts/check-business-scenarios.py` 从公开 Go API 办理，MCP 真实调用 Go，业务效果落入 PostgreSQL；没有从 Python 直接写业务表。CI 也执行相同 12 场景，使用默认 HTTP transport。

保留[首轮 10/12](business-scenarios-first.json)和[第二轮 11/12](business-scenarios-second.json)：首轮取消夹具的工单未指派，系统正确拒绝，已补齐夹具；版本冲突被 MCP 误报为 MCP_RESULT_UNKNOWN，第二轮明确记录了该错误，修复后最终整轮通过。未丢弃失败场景或将 mock 分数视为真实模型质量。

当前场景每任务只办理一个动作。实际物流催件、退款、赔付、通知，以及真实模型对新意图的质量评估需另行接入或执行。v2 可以恢复旧图，旧镜像不支持 v2；回退前应停止新任务并完成或取消 v2 等待任务，保留既有业务结果。
