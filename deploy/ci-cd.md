# CI/CD 与镜像交付

[GitHub Actions 工作流](../.github/workflows/ci-cd.yaml) 在 PR、`main` 推送、`v*` 标签和手动触发时运行。所有引用的 Action 固定到官方版本的完整提交 SHA。

## 验证与发布

验证任务使用 Ubuntu 24.04、Go 模块声明的版本、Python 3.12、Node 24 和前端声明的 pnpm 版本，执行：

1. Go 格式检查、`go vet`、一次性数据库中的集成测试。
2. 在临时测试库初始化 `knowledge` schema 和 pgvector 扩展，执行 Agent Ruff 静态检查和临时 schema 测试。
3. 前端锁文件安装、单元测试和 TypeScript/Vite 构建。
4. 生成临时随机 `.env`，验证 Compose，构建并启动完整服务，通过 Web 代理检查认证、Cookie 和权限。

测试库是 runner 上的临时 PostgreSQL，测试使用合成数据；模型使用 mock，不需要业务数据库或真实模型密钥。无论成功失败，清理临时 Compose 容器和卷。

验证通过后，`main`、`v*` 标签或在这些 ref 上手动执行会构建 `linux/amd64` 的 API、Agent、Web 镜像。PR 仅验证，不登录或推送镜像。Go 镜像同时包含 API 与迁移程序，Compose 的 `migrate` 和 `api` 使用同一发布镜像。

每个镜像单次构建，输出到 GHCR 和阿里云 ACR；任一仓库失败，发布任务失败。跨仓库推送没有原子性，失败时可能已有一个仓库完成，应重跑失败任务。

## 仓库配置

在 [GitHub Actions 仓库设置](https://github.com/halpers/flow-desk/settings/secrets/actions) 配置以下项：

| 类型 | 名称 | 值或用途 |
| --- | --- | --- |
| Secret | `ACR_USERNAME` | 阿里云容器镜像仓库登录用户名 |
| Secret | `ACR_PASSWORD` | 阿里云容器镜像仓库访问凭据密码 |
| Variable，可选 | `ACR_REGISTRY` | 默认 `crpi-1bbvvu1xvdtg7e0p.cn-hangzhou.personal.cr.aliyuncs.com` |
| Variable，可选 | `ACR_NAMESPACE` | 默认 `flow-desk` |

ACR 使用仓库访问凭据密码，配置说明见[阿里云个人版镜像推送文档](https://www.alibabacloud.com/help/en/acr/user-guide/use-a-container-registry-personal-edition-instance-to-push-and-pull-images)。GitHub 托管 runner 使用公网域名；带 `-vpc` 的内网域名不适用。请在 ACR 控制台确认公网访问已开启，并在 `flow-desk` 命名空间创建或授权自动创建 `flow-desk-api`、`flow-desk-agent`、`flow-desk-web` 三个仓库。

GHCR 使用 Actions 自动提供的 `GITHUB_TOKEN` 和任务级 `packages: write` 权限，无需额外 PAT。已有 GHCR 包需允许本仓库 Actions 写入；参见 [GitHub 镜像发布说明](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)。缺少 ACR 凭据时，发布任务在登录前明确失败；测试任务仍可独立完成。

## 镜像命名与标签

| 服务 | GitHub Container Registry | 阿里云 ACR |
| --- | --- | --- |
| API / migrate | `ghcr.io/halpers/flow-desk-api` | `crpi-1bbvvu1xvdtg7e0p.cn-hangzhou.personal.cr.aliyuncs.com/flow-desk/flow-desk-api` |
| Agent | `ghcr.io/halpers/flow-desk-agent` | `crpi-1bbvvu1xvdtg7e0p.cn-hangzhou.personal.cr.aliyuncs.com/flow-desk/flow-desk-agent` |
| Web | `ghcr.io/halpers/flow-desk-web` | `crpi-1bbvvu1xvdtg7e0p.cn-hangzhou.personal.cr.aliyuncs.com/flow-desk/flow-desk-web` |

两个仓库使用相同标签：每次发布都有 `sha-<完整提交 SHA>`；`main` 额外更新 `latest`；`v1.0.0` 等标签推送额外生成同名版本标签，不更新 `latest`。发布摘要记录镜像地址和 digest。部署和回滚优先选择确定的 SHA 标签或 digest。

## 使用已发布镜像

目标主机先完成 Docker Compose 安装、仓库登录及本地 `.env` 配置。私有 GHCR 拉取需使用具有包读取权限的凭据；阿里云使用 ACR 访问凭据。登录密码通过交互或标准输入传递。

在目标机器的 `.env` 设置三个变量，并为它们选择同一个已成功发布的 SHA 或版本标签：

```dotenv
FLOWDESK_API_IMAGE=ghcr.io/halpers/flow-desk-api:v1.0.0
FLOWDESK_AGENT_IMAGE=ghcr.io/halpers/flow-desk-agent:v1.0.0
FLOWDESK_WEB_IMAGE=ghcr.io/halpers/flow-desk-web:v1.0.0
```

示例 `v1.0.0` 需要先实际打标签并完成发布。也可以将前缀换成上表阿里云地址，三个服务必须采用同一发布版本。随后在根目录执行：

```sh
docker compose config --quiet
docker compose pull api agent web
docker compose up --detach --no-build --wait --wait-timeout 180
docker compose ps --all
```

Windows/WSL 可将 `docker compose` 替换为 `.\scripts\compose.ps1`。`migrate` 使用拉取的 API 镜像，并在 Agent/API 启动前运行。使用发布镜像时采用 `--no-build`；`scripts/start.ps1` 面向本地源码构建。

当前 CD 交付到镜像仓库。尚未配置目标服务器、SSH 凭据或自动上线作业；目标机执行上述拉取和更新命令。应用镜像回滚需要确认数据库迁移兼容，不自动回滚数据库 schema。
