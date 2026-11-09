# 部署说明

Agent 的应用迁移和 checkpointer 升级已拆为独立一次性作业；运行角色无 DDL。新环境随 bootstrap 初始化，已有卷首次收紧权限前须按[服务器手册§33](server.md#33-agent-独立迁移与运行权限收紧)和[专项手册](../docs/m7-agent-migration.md)备份、补齐独立凭据并升级所有权。

退休登记可在完成副本清理后移入历史表，完整身份及旧回执继续保留；升级迁移、人工确认、备份和旧版本回退限制见[服务器手册 §24](server.md#24-完整副本历史与退休登记归档)。

原文件默认使用 PostgreSQL；启用外部私有 S3 或本地 SeaweedFS 沙箱，以及备份/恢复/回退步骤，见[原文件存储手册](source-storage.md)。

规则双栏与表格阅读方式的升级、人工核对及旧版本回退条件见[服务器手册第 15 节](server.md)，能力限制和固定样本证据见[M7.5](../docs/m7-pdf-layout.md)。

运维上服务器请从 **[单机服务器部署手册（IP:端口）](server.md)** 开始：包含 Ubuntu 环境准备、随机凭据初始化、GHCR/阿里云镜像、HTTP/HTTPS IP 入口、健康/登录验收、监控、升级回滚、备份恢复与排障。域名为可选配置。服务器发布使用 `release.py --server`，HTTPS 再加 `--tls`。

以下内容描述本地开发拓扑；服务器端口由 `compose.server.yaml` 或 `compose.server-tls.yaml` 覆盖。

根目录 [docker-compose.yaml](../docker-compose.yaml) 是统一运行配置，提供服务构建、PostgreSQL 角色初始化、健康检查、依赖顺序和数据库持久化。PowerShell 启动入口为 `scripts/start.ps1`，操作步骤见 [本地运行说明](../docs/runtime.md)。镜像自动发布和配置方法见 [CI/CD](ci-cd.md)。固定版本发布/回滚、TLS override 及监控启动见 [M6.3 运维说明](../docs/m6-deployment-monitoring.md)，当前只完成本地验收，未部署服务器。

## 1. 本地拓扑

| Compose 服务 | 默认宿主机端口 | 职责及访问范围 | 持久化 |
| --- | --- | --- | --- |
| `web` | 127.0.0.1:13000 → 80 | Nginx 静态资源，代理 `/api/` 到 Go | 无 |
| `api` | 127.0.0.1:18080 → 8080 | Go API、业务工具和调度 | PostgreSQL `business` |
| `agent` | 不发布；容器内 8000 | FastAPI、图执行、知识检索；可访问外部模型 | PostgreSQL `agent` / `knowledge` |
| `migrate` | 不发布 | 一次性 Go 迁移与合成演示账号初始化，成功后退出 | PostgreSQL `business` |
| `agent-migrate` | 不发布 | 一次性 Agent/checkpointer 迁移及运行授权；独立迁移账号 | PostgreSQL `agent` / `knowledge` |
| `agent-role-upgrade` | 不发布 | maintenance profile 下显式 owner-only 旧卷所有权升级，不随普通启动执行 | 保留原有数据及密码 |
| `postgres` | 127.0.0.1:55432 → 5432 | PostgreSQL 18 + pgvector，仅本机和容器网络 | `flowdesk_pgdata` 命名卷 |

生产 Web 构建为静态资源，经同一 TLS 入口代理 Go；Python、数据库和内部 API 使用网络隔离和服务身份。Go 容器中的 Python endpoint 使用服务 DNS，不能配置成容器自身的 localhost。

## 2. 依赖及启动顺序

1. 验证所有必填环境变量、运行时基线和依赖锁文件。
2. 启动 PostgreSQL；空数据卷首次启动时初始化角色、schema 和 pgvector 扩展。
3. 数据库健康后执行 `migrate`；幂等应用 Go 迁移并初始化合成演示账号。
4. Go 迁移成功后执行 `agent-migrate`，以独立 `fd_agent_migrator` 初始化/升级 checkpointer、应用 Agent 迁移并授予必要运行 DML。
5. 迁移成功后启动 Python；`fd_agent` 只读检查精确版本和权限后通过 readiness，不执行 DDL。
6. 启动 Go API/调度，readiness 通过后启动 Web。
7. Web 健康后启动命令返回，随后可执行小规模 smoke test。

Compose 用 healthcheck 和两个一次性迁移作业控制顺序；`release.py` 同样在启动 Agent 前显式执行两个作业。任何迁移失败均停止后续发布。Go 使用独立业务运行账号，Agent 运行账号只保留自身表必要 DML、schema USAGE 和只读迁移元数据，不持有 migrator/owner 凭据。

长期运行的四个服务使用 `restart: unless-stopped`，迁移任务不自动重启。启动依赖只控制启动顺序；运行中的依赖故障由应用重试和持久化恢复处理。不要通过删除卷重启已有数据库。

## 3. 配置与密钥

[.env.example](../.env.example) 提供端口、数据库角色密码、JWT 和内部服务凭据、模型参数以及可选发布镜像变量。真实 `.env` 被 Git 忽略；启动脚本仅在文件不存在时生成随机本地凭据。模型密钥不参与镜像构建或流水线测试。

`AGENT_MIGRATION_PASSWORD` 独立于 `AGENT_PASSWORD`。已有 `.env` 缺项时使用 `scripts/configure-agent-migration.py` 显式追加，不覆盖现有字节/密钥；已有数据库还须执行所有权升级，不能只追加变量后直接启动。已有角色密码不会被迁移或初始化脚本重置。

模型名称、embedding 维度、价格版本、graph/prompt 版本必须可追踪；运行时依赖固定到镜像/锁文件，避免 latest 镜像标签改变检查点或数据库结构。

## 4. 健康与关闭

live 表示事件循环/服务进程存活；ready 表示数据库、迁移和接收能力可用。外部模型临时失败记录为依赖故障并限制新任务，不将失败转化为无限进程重启。

优雅关闭：停止接受新任务→停止 claim→给活跃节点有限完成窗口→保存 checkpoint/outbox→释放执行锁和连接。Go 停止 SSE 后客户端重连；Python 未完命令留在持久化队列，重启按租约和执行隔离规则恢复。

## 5. 发布、回滚与备份

数据库使用向前兼容迁移；先部署能兼容旧字段的服务，再开放新能力。包含破坏性 schema、graph 或 checkpoint 变更时提供专门迁移计划；回滚应用不自动执行破坏性 Down。

M7.25 是运行权限兼容边界：升级后不能直接回退到仍在 Agent 启动中执行 setup/migrate 的旧镜像，也不能临时恢复运行 DDL/owner 权限。优先修复并向前发布兼容版本；首次升级失败需回到升级前环境时，按备份手册恢复到新空库后另行切换。HNSW create/status/drop 使用固定版本 Agent 镜像的 `agent-migrate` 一次性容器，命令见[专项手册](../docs/m7-agent-migration.md)。

升级 graph/prompt/model profile 保留任务原版本快照。旧图版本执行器保留到活动任务结束，或通过显式验证的迁移恢复；不能让滚动发布随机改变已批准动作。

备份数据库和原文件；向量索引可重建但回执/审计不能丢失。至少进行一次恢复演练并记录恢复时间、丢失窗口及索引重建结果。

## 6. Windows 开发

默认使用完整 Compose：`scripts/start.ps1` 构建并等待服务健康；`scripts/compose.ps1` 优先调用 Windows Docker，无该命令时转发到 WSL Docker。Go 与前端也可在宿主机开发，端口配置见运行说明。Go 生成脚本需要 psql/goose/goctl，可由项目专用开发容器提供。

PowerShell 包装脚本原样转发 Compose 参数；示例统一使用完整参数 `--detach`。
账号交接、初始化密码重置、启停和角色升级/回退见[服务器手册 §16](server.md#16-账号与角色管理升级及交接)。本阶段无新增配置；角色调整通过 Go 管理 API 同事务撤销会话，禁止直接改数据库权限投影。
