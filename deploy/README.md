# 部署规划

M1 已提供根目录 Compose、各服务 Dockerfile、PostgreSQL 角色初始化、Nginx 同源代理和启动脚本。操作步骤见 [本地运行说明](../docs/runtime.md)。以下拓扑也包含后续业务阶段的目标组件，实际状态见实施记录。

## 1. 本地拓扑

| 组件 | 宿主机规划端口 | 访问范围 | 持久化 |
| --- | --- | --- | --- |
| Web 开发服务器 | 5173 | 本机；代理 /api/v1 到 Go | 无 |
| Go API/worker | 8080 | Web 代理；内部路由限制可信网络 | business 数据、文件卷 |
| Python API/worker | 8000 | Go 和运维检查；默认不向公网暴露 | agent/knowledge 数据 |
| PostgreSQL + pgvector | 5432 | 开发可绑定 127.0.0.1，生产仅内网 | 独立数据库卷 |
| 可选 OTel collector | 按所选后端确定 | 内网 | 按监控后端配置 |

生产 Web 构建为静态资源，经同一 TLS 入口代理 Go；Python、数据库和内部 API 使用网络隔离和服务身份。Go 容器中的 Python endpoint 使用服务 DNS，不能配置成容器自身的 localhost。

## 2. 依赖及启动顺序

1. 验证所有必填环境变量、运行时基线和依赖锁文件。
2. 启动 PostgreSQL，健康后运行一次性 Go/Python 迁移任务，创建 pgvector 所需结构。
3. 以受控命令导入合成演示种子，明确禁止对实际业务库重复执行破坏性初始化。
4. 启动 Python，检查数据库、checkpointer 和索引结构兼容。
5. 启动 Go API/调度，readiness 通过后开放入口。
6. 启动前端或静态代理，执行小规模 smoke test。

当前 Compose 利用 healthcheck 和一次性 Go migration job 依赖。Go 使用独立业务运行账号；Python 当前在启动时以 Agent 角色迁移自身 schema。生产应将 Python 迁移拆为独立作业，收回运行角色 DDL 权限。

## 3. 配置与密钥

M1 在根或 deploy 中提供 `.env.example`，仅包含变量名和非敏感占位。真实 `.env` 被 Git 忽略。变量覆盖 Go/Python DSN、内部服务凭据、JWT/capability 签名密钥引用、模型/embedding key、文件根目录和运行限制。

模型名称、embedding 维度、价格版本、graph/prompt 版本必须可追踪；运行时依赖固定到镜像/锁文件，避免 latest 镜像标签改变检查点或数据库结构。

## 4. 健康与关闭

live 表示事件循环/服务进程存活；ready 表示数据库、迁移和接收能力可用。外部模型临时失败记录为依赖故障并限制新任务，不将失败转化为无限进程重启。

优雅关闭：停止接受新任务→停止 claim→给活跃节点有限完成窗口→保存 checkpoint/outbox→释放执行锁和连接。Go 停止 SSE 后客户端重连；Python 未完命令留在持久化队列，重启按租约和执行隔离规则恢复。

## 5. 发布、回滚与备份

数据库使用向前兼容迁移；先部署能兼容旧字段的服务，再开放新能力。包含破坏性 schema、graph 或 checkpoint 变更时提供专门迁移计划；回滚应用不自动执行破坏性 Down。

升级 graph/prompt/model profile 保留任务原版本快照。旧图版本执行器保留到活动任务结束，或通过显式验证的迁移恢复；不能让滚动发布随机改变已批准动作。

备份数据库和原文件；向量索引可重建但回执/审计不能丢失。至少进行一次恢复演练并记录恢复时间、丢失窗口及索引重建结果。

## 6. Windows 开发

Go 与前端可在宿主机运行，PostgreSQL/Python 可选容器运行；以团队统一方案作为 README 的默认路径。PS 与 Bash 脚本分别说明工作目录和依赖；Go 生成脚本需要 psql/goose/goctl，可由项目专用开发容器提供，避免要求修改参考 RWA 环境。

只有在实际 Dockerfile/Compose/脚本提交且验证后，README 才添加 `docker compose up --build` 等“一键启动”命令。
