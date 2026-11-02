# 本地运行与验收

## 启动

需要 Docker Compose v2；Windows 可使用 WSL 内的 Docker。首次构建需要访问容器仓库、Go 和 Python/Node 包仓库。

在仓库根目录执行 PowerShell：

```powershell
.\scripts\start.ps1
.\scripts\compose.ps1 ps
```

启动脚本只在 `.env` 不存在时创建随机凭据，不覆盖已有设置。默认页面 `http://localhost:13000`，Go API `http://localhost:18080`，数据库仅绑定本机 `55432`。Python 不发布宿主机端口。已有 `.env` 可修改端口；不要提交它。

登录账号为 `demo.operator`、`demo.supervisor`、`demo.admin`；第二个合成租户使用 `other.` 前缀。密码是本地 `.env` 中的 `DEMO_PASSWORD`。不存在通用默认密码。迁移重复运行不重置已有账号密码。

迁移使用 owner，Go 使用 `fd_business`（业务 DML），Python 使用 `fd_agent`（agent/knowledge）。一期 Python 账号负责自己 schema 的建表；正式部署应将迁移账号和只读/写运行账号进一步拆开。

停止：`.\scripts\compose.ps1 stop`。该命令保留数据库。不要用 `down -v` 作为普通重启方式。

## 本机开发

- Go：1.27.1，go-zero 1.10.3，goctl 1.10.2。模块在 `flow-desk-serv/flowdesk`，依赖由 `go.mod/go.sum` 固定。
- Python：3.12，推荐本地独立 `.venv`，`pip install -r requirements.lock`。固定安装结果包含 LangGraph 和 PostgreSQL checkpointer；后续阶段才接入执行图。
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
```

`check-go.ps1` 创建并删除唯一命名的测试数据库，不重置业务 schema。覆盖 refresh 轮换/复用撤销、退出、跨租户外键以及后续写失败的事务回滚。HTTP smoke 通过实际前端代理检查登录、Cookie、Origin、轮换和退出。

契约从 `.api` 导出 Swagger，再转换 OpenAPI 3、生成 TypeScript；Python 从 FastAPI 导出 OpenAPI。第二次导出应无 diff。新增接口必须先扩展来源，禁止手改生成文件。

模型生成需本地安装 `psql`、`goose`、`goctl` 和 Python。将开发数据库 owner DSN 放入进程变量 `MODEL_DB_URL`，执行 `python flow-desk-serv/flowdesk/db/goctl_model.py`；脚本创建临时 schema，应用迁移、生成并清理，不对 business 执行重置。

## 当前边界

阶段状态以 [实施记录](progress.md) 为准。M1 只提供工程、登录身份、数据库迁移和契约，页面中的任务/知识入口在后续阶段接入。M1 使用代码定义的三个固定角色，`users.role` 保存单一角色；多角色关联表属于后续团队权限扩展。

本地 HTTP Cookie 为 HttpOnly、SameSite=Strict。生产接入 TLS 反向代理时设置 `COOKIE_SECURE=true`、调整可信 Origin 并使用非 dev 模式；非 dev 模式会拒绝关闭 Secure Cookie 的配置。模型配置默认为 mock，不会在 M1 启动时调用外部模型。
