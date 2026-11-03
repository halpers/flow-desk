# M6.2 MCP 工具适配

固定使用官方 Python SDK `mcp==2.3.0`，同版本 `mcp-types` 与其依赖锁定在 Agent requirements.lock；Windows 的 pywin32 带平台条件，Linux 安装已验证。接口依据[官方低层 Server](https://py.sdk.modelcontextprotocol.io/advanced/low-level-server/)与[Client transport](https://py.sdk.modelcontextprotocol.io/client/transports/)文档，实现工具发现与调用。

## 传输与生命周期

根目录 `.env` 的 `TOOL_TRANSPORT=http` 为默认值，`mcp` 切换读工具和已批准动作执行的网络适配；改变后重建 Agent 容器加载。控制接口、知识 scope、证据验证和动作准备继续通过已有 Go HTTP 接口。

Agent 为每次未缓存调用启动固定模块 `flowdesk_agent.tools.mcp_server`，通过官方 SDK stdio transport 通信；不是将普通 HTTP client 改名为 MCP。客户端先发现工具再调用，SDK 负责协议协商。每次调用最长 20 秒，离开上下文时关闭并清理子进程。此版本不复用长连接，进程启动开销包含在工具耗时中。

子进程仅获得运行所需系统变量、Go 地址、服务身份及受信任的 run/dispatch/generation；不获得模型密钥、数据库密码或用户选择的命令。stdout 专用于 MCP，客户端不输出子进程错误正文。MCP 不新增公开端口、远程第三方服务器或 OAuth 入口。

## 工具与授权

工具发现从 Go 当前租约上下文过滤 `order.lookup`、`logistics.track`、`ticket.search`，参数复用[只读工具契约](../shared/contracts/tools/v1/read.schema.json)。Go 的 tools 列表按当前角色权限生成；每次调用重新授权，不能将先前发现视为永久许可。

有办理权限时还提供 `actions.execute`，只接受[现有 action_id](../shared/contracts/tools/v1/mcp-action.schema.json)。调用不能携带 actor、tenant、approved 或覆盖动作参数。Go 仍检查当前用户、run 代次、批准、资源/证据版本，在事务中写业务和不可变回执。MCP 失败或超时不授权重新生成 action_id。

输入 schema 在服务器端再次验证；未知工具与额外字段拒绝。结果包装为 MCP structured_content，Go 响应/回执仍按生成 OpenAPI 校验。错误只返回受限错误码，不反射参数或上游正文。图原有稳定 tool_call_id、回调 outbox、缓存、检查点和所有权 guard 保留；恢复命中缓存不启动子进程。

## 验收与回退

真实 SDK 内存 client/server 测试覆盖发现、schema、未知工具、额外租户字段、外租户对象、未批准写、同 action 回执重放和撤权；stdio 子进程测试连接受控 HTTP 供应商并检查凭据范围。MCP 协议测试使用假 Go 供应商；Go 的真实 PostgreSQL 回执/审批事务由原有集成用例及本次完整回归验证。

本地三服务验收另外通过 MCP 完成补充订单→检查点中断→重启 Agent→批准→真实建单，以及备注、拒绝和取消、跨租户拒绝，见[HTTP 报告](../reports/m6/acceptance-mcp.json)。使用确定性模型，不消耗付费模型调用；不将协议 mock 回执重放误称为真实链路重复业务提交。

```powershell
./scripts/compose.ps1 --file deploy/compose.mcp-acceptance.yaml up --detach --no-deps --wait api agent
python scripts/smoke-actions.py --restart-agent --transport mcp --profile mock --output reports/m6/acceptance-mcp.json
./scripts/compose.ps1 up --detach --no-deps --wait api agent
```

最后一条恢复根目录配置。验收会创建本地合成工单，仅用于开发数据。`--transport`/`--profile` 是报告标签，应与实际部署配置一致。回退将 `TOOL_TRANSPORT=http` 并重建 Agent；业务表、动作和检查点无需回滚。
