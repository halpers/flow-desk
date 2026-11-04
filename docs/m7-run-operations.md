# M7.2 任务运营台

本阶段提供任务元数据列表、脱敏执行记录与受限的显式重试。原会话内容、审批和回执仍按原所有者接口访问；主管的运营权限不授予代他人确认或办理的能力。实际验收另记 progress.md。

## 列表和授权

GET /api/v1/runs 支持 scope=own（默认）和 scope=tenant、固定状态筛选、完整 run_id 定位及游标分页（默认 20、最多 50）。普通用户只能读取自己的任务；supervisor/admin 的 run:observe 可查看本租户任务元数据。仅返回 ID、状态、版本、错误分类、时间、工具次数及重试关联，不包含会话标题、用户消息、摘录或工具正文。当前团队权限在 SQL 分页前过滤，游标绑定租户、用户、范围及筛选条件。排序使用不可变 created_at/id，避免活跃任务更新时间变化造成重复分页。

GET /api/v1/runs/:run_id/trace 返回同一权限下的任务元数据及 Python 观测。Go 在调用 Agent 前后核对当前身份和资源权限。Python 仅在从 Go 获取执行上下文后绑定 run/tenant；观测查询须匹配该绑定。旧任务没有绑定或尚未启动时返回 available=false；观测服务不可用返回可辨识的 503，不伪造零用量。

观测只列节点名/状态/耗时、工具成功缓存计数、模型尝试状态/耗时/用量和检索次数。各列表最多 100 条，truncated 显示截断；累计用量独立汇总。没有记录的 usage 标记 unknown，mock 的零模型尝试单独可辨识。费用保持 unknown，不猜测供应商价格；embedding 仅统计本 run 的 query operation，不把整库建索引费用分摊到当前任务。不提供原始 prompt、模型输出、参数、检查点、内部 URL、令牌或私有推理。观测仍是 best effort，进程强杀可能缺少节点耗时。

## 受限重试

POST /api/v1/runs/:run_id/retry 使用 Idempotency-Key 和 expected_version，仅任务所有者可调用。源任务必须 failed/expired，未准备过任何 action、未接收补充输入，当前资源仍可访问。已有动作或回执时返回 RETRY_REQUIRES_REVIEW，用户查看原会话和真实回执；不把总结失败变成重新办理。审批、补充输入和写结果未知的通用恢复不属于该按钮范围。

重试创建新的 run、两条消息和新执行 outbox，复制原用户输入，保存 retry_of_run_id/depth；不更改旧终态、动作或检查点。每个源任务最多一个子任务，重复/并发请求返回同一个子任务；子任务失败后可继续重试，最多三层显式重试。新任务从当前会话所有者的当前登录会话、图版本和预算开始，重新检索当前政策并走完整审批规则。保留每会话单活跃任务、用户/租户队列配额。会话锁、源任务检查、消息、关联和 outbox 在同一 Go 事务内完成，后续写入失败全部回滚。

## 界面和交付

工作台新增“任务记录”抽屉，支持状态/范围筛选、定位、分页、查看执行记录、刷新、打开自己的会话及显式重试。任务详情展示重试来源、限制原因和用量是否已知。对他人任务只展示运营元数据。网络响应丢失时沿用同一幂等键；返回的子任务可在原会话继续查看。所有页面只调用 Go。

验收覆盖租户/所有者/团队与即时撤权、游标隔离、不可变分页、脱敏和观测不可用、并发重试、丢失响应、会话占用/配额、已准备动作/已提交回执拒绝及事务回滚。使用独立 mock 环境，不调用付费模型。Go/Python 分别追加自有迁移，三个镜像一起升级；回滚不执行数据库 Down。

## 本地复跑与限制

只在独立、合成、MODEL_PROVIDER=mock 的开发环境执行。根目录 `scripts/check-go.ps1` 从本地配置取得开发连接，每项集成测试新建并清理临时数据库；Agent 使用 AGENT_TEST_DATABASE_URL 指定可建临时 schema 的开发管理员，在 Agent 目录运行 `python -m pytest -q --tb=short`。默认 Compose 内的业务/Agent账号没有创建测试数据库的权限。

```bash
python scripts/check-run-operations.py --env-file work/disposable/.env --output work/run-operations.json
```

env-file 必须是已启动的独立 mock Compose 配置，包含 DEMO_PASSWORD 和 WEB_HOST_PORT；示例路径需替换为实际配置文件。脚本仅通过 Web→Go→Agent 的真实 HTTP 链路读取/创建合成任务，不重置数据、不更改模型配置；CI 使用自己生成并随后销毁的 Compose。脚本查询不存在的合成订单产生失败，验证记录、脱敏、同键/换键重放及四条消息，不代表重试会修复原始业务错误。

Web 目录运行 `pnpm test`、`pnpm build`。浏览器使用 FLOWDESK_E2E_ENV_FILE 和 FLOWDESK_E2E_URL 指向该独立环境，`pnpm test:e2e tests/e2e/operations.spec.ts` 验证失败任务追踪、重试响应丢失后同键获取结果、唯一子任务与页面恢复。费用保持 unknown；工具列表只统计已缓存成功结果，失败工具记录通过节点失败与 run 错误呈现；不提供跨服务精确计费或历史账本自动回填。
