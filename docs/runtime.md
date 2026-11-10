# 本地运行与验收

## 启动

需要 Docker Compose v2；Windows 可使用 WSL 内的 Docker。首次构建需要访问容器仓库、Go 和 Python/Node 包仓库。

在仓库根目录执行 PowerShell：

```powershell
.\scripts\start.ps1
.\scripts\compose.ps1 ps
```

启动脚本只在 `.env` 不存在时创建随机凭据，不覆盖已有设置。默认页面 `http://localhost:13000`，Go API `http://localhost:18080`，数据库仅绑定本机 `55432`。Python 不发布宿主机端口。已有 `.env` 可修改端口；不要提交它。

统一配置为根目录 [docker-compose.yaml](../docker-compose.yaml)。`compose.ps1` 显式加载该文件，并在 Windows 没有 Docker 命令时转发给 WSL。`start.ps1` 构建镜像，最多等待 180 秒使服务健康；失败时检查 `compose.ps1 ps --all` 和 `compose.ps1 logs --tail 100`。

已有 `.env` 且 Docker 可直接使用时，在根目录也可执行以下命令。若数据卷来自 M7.25 之前，先按[独立迁移手册](m7-agent-migration.md)补齐独立凭据、停止旧 Agent 并升级所有权；普通启动不会自动改写已有配置或所有权：

```sh
docker compose config --quiet
docker compose up --detach --build --wait --wait-timeout 180
docker compose ps --all
docker compose stop
```

配置检查使用 `--quiet`，避免把展开后的凭据打印到终端。PostgreSQL 数据保存到 `flowdesk_pgdata` 命名卷，PG18 挂载路径为 `/var/lib/postgresql`；数据库角色初始化脚本仅在空数据卷首次启动时执行。修改 `.env` 的数据库密码不会自动更新已有角色。

登录账号为 `demo.operator`、`demo.supervisor`、`demo.admin`；第二个合成租户使用 `other.` 前缀。密码是本地 `.env` 中的 `DEMO_PASSWORD`。不存在通用默认密码。迁移重复运行不重置已有账号密码。

Go 迁移使用 owner，Go 运行使用 `fd_business`（业务 DML）。`agent-migrate` 使用独立 `fd_agent_migrator`，负责 Agent 有序迁移、checkpointer setup 和运行授权；成功后才启动 `fd_agent`。Agent 启动和 readiness 只读验证精确迁移版本及权限，运行账号无 DDL、CREATE/TEMP、所有权或迁移角色成员身份，迁移元数据只读，也不能读写业务表。

新环境 bootstrap 为 `AGENT_PASSWORD` 与 `AGENT_MIGRATION_PASSWORD` 分别生成随机值。已有文件用 `python scripts/configure-agent-migration.py --env-file .env` 只追加缺少的迁移凭据，再在维护窗口执行专项手册中的 `agent-role-upgrade`；不能靠改密码、删卷或让运行进程补迁移解决。HNSW 也通过 `agent-migrate` 一次性容器维护，运行容器不获得迁移密钥。首次收紧权限后，不可直接回退到仍在启动时执行 DDL 的旧 Agent 镜像。

停止：`.\scripts\compose.ps1 stop`。该命令保留数据库。不要用 `down -v` 作为普通重启方式。

## 本机开发

- Go：1.27.1，go-zero 1.10.3，goctl 1.10.2。模块在 `flow-desk-serv/flowdesk`，依赖由 `go.mod/go.sum` 固定。
- Python：3.12，推荐本地独立 `.venv`，`pip install -r requirements.lock`。先以独立 `AGENT_MIGRATION_DATABASE_URL` 执行 `python -m flowdesk_agent.storage.migrate`；运行进程只配置 `AGENT_DATABASE_URL`、`GO_BASE_URL`、`INTERNAL_TOKEN` 等运行参数，不携带迁移或 owner 连接。推荐由 Compose 完成迁移顺序。
- Node：22.12+ 或 24，pnpm 11.25.0，使用 `pnpm install --frozen-lockfile`。
- PostgreSQL：18 + pgvector，Compose 镜像提供扩展。

本机 Go 环境变量对应 `etc/flowdesk-api.yaml`；连接 PostgreSQL 时指定 `search_path=business`。本机 Vite 代理到 `localhost:8080`；若使用 Compose 的 API，则修改 Vite 代理端口或在本机运行 Go。

## 可重跑检查

数据库已启动后，根目录执行：

```powershell
.\scripts\check-go.ps1
.\scripts\export-contracts.ps1
pnpm.cmd --dir flow-desk-web build
flow-desk-agent\.venv\Scripts\python.exe -m ruff check flow-desk-agent/src flow-desk-agent/scripts
flow-desk-agent\.venv\Scripts\python.exe scripts/smoke.py
flow-desk-agent\.venv\Scripts\python.exe scripts/check-agent.py
flow-desk-agent\.venv\Scripts\python.exe scripts/smoke-tasks.py
pnpm.cmd --dir flow-desk-web test
pnpm.cmd --dir flow-desk-web test:e2e
```

`check-go.ps1` 创建并删除唯一命名的测试数据库，不重置业务 schema。覆盖认证、事务回滚及 M2 任务、事件、授权和并发配额。`check-agent.py` 使用独立临时 schema。HTTP smoke 通过实际前端代理检查认证及三服务查询，E2E 使用本机 Chrome 验证页面和重连。详见 [M2 交付说明](m2-read-tasks.md)。

`tests/test_migration_roles.py` 另用唯一临时数据库和实际登录角色验证权限、升级回滚、checkpoint 与 HNSW。`scripts/check-agent-migration.py` 只接受仓库 `work/` 下的一次性 mock 栈配置；Python3.12 在 Windows 使用 SelectorEventLoop，兼容本地 `.venv`。命令与验收范围见[专项手册](m7-agent-migration.md)，不能用于真实服务器业务库。

契约从 `.api` 导出 Swagger，再转换 OpenAPI 3、生成 TypeScript；Python 从 FastAPI 导出 OpenAPI。第二次导出应无 diff。新增接口必须先扩展来源，禁止手改生成文件。

模型生成需本地安装 `psql`、`goose`、`goctl` 和 Python。将开发数据库 owner DSN 放入进程变量 `MODEL_DB_URL`，执行 `python flow-desk-serv/flowdesk/db/goctl_model.py`；脚本创建临时 schema，应用迁移、生成并清理，不对 business 执行重置。

## 当前边界

原文件默认保存在 PostgreSQL。可选本地 S3 沙箱的启动和外部私有 S3 配置见[文件存储部署](../deploy/source-storage.md)，下载与故障恢复设计见[M7.4](m7-object-storage.md)。只在独立 mock 环境运行合成文件验收；不要为执行脚本而修改现有真实模型配置。

阶段状态以 [实施记录](progress.md) 为准。M1–M6.5 已交付身份、持久化任务、知识检索、兼容模型、经批准的工单办理、固定评测，以及语义 RAG、MCP、部署监控和团队权限。M6.4团队成员与资源范围已实现；M7.6进一步交付基础角色、增量权限角色、多角色关联及会话撤销，`users.role`保留基础角色兼容字段。账号/角色操作见[M7.6](m7-accounts.md)。远程单机部署见[服务器手册](../deploy/server.md)。

M7.1 的 PDF 解析器随 Agent 镜像安装，无需额外解析服务或 API key。管理员上传有文本层的 PDF 后先预览文本再激活；错误状态、文件限制和页码规则见 [PDF 说明](m7-knowledge-files.md)。默认 Web 及两种 TLS 代理的请求上限与 Go 均为 4 MiB；自建代理也需保持一致。

本地 HTTP Cookie 为 HttpOnly、SameSite=Strict。生产接入 TLS 反向代理时设置 `COOKIE_SECURE=true`、调整可信 Origin 并使用非 dev 模式；非 dev 模式会拒绝关闭 Secure Cookie 的配置。新建本地配置默认 `MODEL_PROVIDER=mock`；真实模型需在 `.env` 设置 `MODEL_PROVIDER=openai-compatible`、`MODEL_BASE_URL`、`MODEL_NAME` 和 `MODEL_API_KEY`。
## M7.6 管理验收

管理员登录后打开“账号与角色”，可管理本租户账号和角色。账号/角色变更会撤销受影响账号的全部会话，旧任务凭证也失效；当前账号需由其他管理员维护。角色采用基础角色加增量权限，见[M7.6](m7-accounts.md)。

仅在可丢弃 mock 环境运行 `python scripts/check-accounts.py --env-file /path/to/disposable/.env --output work/accounts-check.json`，检查创建重放、密码重置、角色变更、会话撤销、禁用和分页。Go 集成测试自动创建并删除独立数据库；Chrome 用例为 flow-desk-web/tests/e2e/accounts.spec.ts。不要在生产运行合成验收脚本。

## M7.26 任务历史批次

运行时新增五项Go公开批次操作，仍只有浏览器→Go→Agent。Go迁移2026110903在现有一次性迁移作业执行；Agent没有新DDL或版本。批次统一登记、页面逐项推进，关闭后通过最近批次继续；M7.26本身没有后台调度，M7.30另行提供默认关闭的定时归档策略。只在可丢弃mock栈运行`python scripts/check-run-history-batch.py --env-file work/retention-ci.env --output work/run-history-batch.json`，该脚本会调整自身新建合成任务的时间，不能连接业务服务器。流程及回退见[服务器手册§36](../deploy/server.md#36-任务历史批量归档)。

M7.27[批量恢复](m7-run-history-batch-restore.md)沿用M7.26批次读取与逐项核对，新增两项恢复POST入口。Go按顺序迁移至2026110904，Agent应用版本仍为11。仅一次性mock环境运行`scripts/check-run-history-batch-restore.py`，不能在业务库运行合成任务验收；生产升级及IP:port操作见[服务器手册§37](../deploy/server.md#37-任务历史批量恢复)。

M7.28[自助改密](m7-self-password.md)无新端口、模型依赖或数据库迁移。配套发布API/Web，固定关闭会记录请求正文的原生HTTP日志；保留原metadata tracing。成功或不确定结果需显式重新登录，浏览器只保存非敏感重新认证标记。公开验收`scripts/check-self-password.py`仅运行在`work/`可丢弃mock配置，创建独立合成账号；运维见[服务器手册§38](../deploy/server.md#38-用户自助修改密码)。

M7.29[共享持久认证限流](m7-auth-rate-limits.md)由Go迁移2026111001提供，无新增环境变量、端口或Agent迁移。所有API实例须连接同一业务库并升级到支持该策略的版本；混用旧API不能获得全局一致额度。只在work/可丢弃mock环境执行双API和重启验收，服务器操作见[部署手册§39](../deploy/server.md#39-共享持久认证限流)。

M7.30[定时归档](m7-run-history-schedule.md)新增Go迁移2026111002及可选RUN_HISTORY_SCHEDULER_ENABLED=false；开启总开关并重建全部API后，仍需管理员明确保存当前租户策略。运行Agent不增加DDL。合成脚本scripts/check-run-history-schedule.py仅在work/可丢弃mock栈的独立新租户运行，CI显式开启该测试栈开关；运维按[服务器手册§40](../deploy/server.md#40-定时任务历史归档)。
