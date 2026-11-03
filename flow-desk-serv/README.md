# flow-desk-serv

Go 业务服务，使用 **go-zero**。Go module、API 定义和应用命令位于 `flowdesk/`。

最初通过 `goctl api new flowdesk` 生成工程，M1 已接入健康检查、登录/刷新/退出/身份接口、三个固定角色、租户隔离约束、goose 迁移和 goctl PostgreSQL 模型生成。业务设计见 [Go 详细设计](flowdesk/docs/design.md)。

M2 已提供会话/消息/任务、订单/物流、幂等 outbox、短期工具能力和持久化 SSE。实现边界和验收见 [M2 交付说明](../docs/m2-read-tasks.md)。

- 规划 HTTP 端口：`8080`。
- 对外入口：`/api/v1`；Agent 工具与回调：`/internal/v1`。
- Go 拥有用户、业务、审批、任务状态和对外事件；Python 通过内部 API 使用这些数据。
- 后续 Go、goctl 和测试命令均在 `flowdesk/` 中执行。

## 初始化来源

首次生成命令在本目录 `flow-desk-serv/` 执行：

```sh
goctl api new flowdesk
```

该命令已执行，后续开发直接修改现有工程。当前基线为 Go 1.27.1、goctl 1.10.2、go-zero 1.10.3，依赖校验记录在 go.sum。

## 启动与验证

在 `flowdesk/` 中执行：

```sh
go run flowdesk.go -f etc/flowdesk-api.yaml
```

需要设置 YAML 引用的数据库、JWT、服务令牌、可信 Origin 和 Cookie 环境变量；推荐通过根目录 Compose 启动。端口为 8080，宿主机默认映射 18080。默认演示路由已移除，存活/就绪接口为 `/health/live` 和 `/health/ready`。

已有工程可执行：

```sh
goctl api validate --api flowdesk.api
go test ./...
go vet ./...
```

受限 Windows 环境可将 GOCACHE 设置为 `flowdesk/.gocache`，该目录已忽略。根目录 `scripts/check-go.ps1` 设置本地连接并运行真实 PostgreSQL 集成测试；测试自动创建/删除自己的数据库。

2026-11-02 已验证认证轮换/复用撤销、退出、跨租户外键拒绝、事务回滚及 Go 静态检查。运行和模型/契约生成流程见 [本地运行说明](../docs/runtime.md)。后续业务阶段的验收另记 [实施记录](../docs/progress.md)。

请先阅读 [服务规范](AGENTS.md)、[总体设计](../docs/design.md) 和 [数据模型](../docs/data-model.md)。
