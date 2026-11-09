# CI/CD 与镜像交付

[GitHub Actions 工作流](../.github/workflows/ci-cd.yaml) 在 PR、`main` 推送、`v*` 标签和手动触发时运行。所有引用的 Action 固定到官方版本的完整提交 SHA。

| 触发方式 | 检查与 Compose 验收 | 构建并推送发布镜像 |
| --- | --- | --- |
| PR | 执行 | 跳过 |
| 推送 `main` | 执行 | 跳过 |
| 推送 `v*` tag，例如 `v0.1.0` | 执行 | 检查通过后发布到 GHCR 和阿里云 |
| 手动 Run workflow（包括选择 tag） | 执行 | 跳过 |

检查保留用于及时发现问题；只有推送版本 tag 才进入镜像发布。日常检查仍会构建 Compose 所需的本地镜像进行启动验收，不会登录镜像仓库或推送发布镜像。其他分支的单独 push 不触发本工作流；存在 PR 时通过 PR 事件检查。触发器和 job 条件语义见 [GitHub 工作流语法](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax)。

## 验证与发布

验证任务使用 Ubuntu 24.04、Go 模块声明的版本、Python 3.12、Node 24 和前端声明的 pnpm 版本，执行：

1. Go 格式检查、`go vet`、一次性数据库中的集成测试。
2. 在临时测试库初始化 `knowledge` schema 和 pgvector 扩展，执行 Agent Ruff 静态检查和隔离测试；迁移角色专项使用唯一临时数据库和实际登录角色验证权限、所有权升级回滚、checkpoint 与 HNSW。
3. 前端锁文件安装、单元测试和 TypeScript/Vite 构建。
4. 生成含独立 Agent 迁移密码的临时随机 `.env`，验证 Compose，依次完成 Go/Agent 一次性迁移再启动运行服务，通过 Web 代理检查认证、Cookie 和权限。
5. 执行固定业务场景验收，验证服务器 HTTP/HTTPS Compose 入口配置、Prometheus 规则和 Collector 配置。
6. 基础 PostgreSQL 文件兼容/OCR 验收后，启动固定 digest 的私有 SeaweedFS，执行 S3 归档、授权下载、恢复及再次归档验收；只使用本轮随机存储凭据。

测试库是 runner 上的临时 PostgreSQL，测试使用合成数据；模型使用 mock，不需要业务数据库或真实模型密钥。无论成功失败，清理临时 Compose 容器和卷。

仅在 `push` 事件且 ref 为 `refs/tags/v*` 时，验证通过后构建 `linux/amd64` 的 API、Agent、Web 发布镜像。Go 镜像同时包含 API 与迁移程序，Compose 的 `migrate` 和 `api` 使用同一发布镜像。Agent 镜像同时包含运行 API、独立迁移和显式维护 CLI；`agent-migrate`、`agent-role-upgrade` 与 `agent` 使用同一目标发布版本，凭据及入口各自隔离，不增加第四种发布镜像。

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
| Agent / agent-migrate / agent-role-upgrade | `ghcr.io/halpers/flow-desk-agent` | `crpi-1bbvvu1xvdtg7e0p.cn-hangzhou.personal.cr.aliyuncs.com/flow-desk/flow-desk-agent` |
| Web | `ghcr.io/halpers/flow-desk-web` | `crpi-1bbvvu1xvdtg7e0p.cn-hangzhou.personal.cr.aliyuncs.com/flow-desk/flow-desk-web` |

两个仓库使用相同标签：每次版本发布同时生成 `sha-<完整提交 SHA>` 和 tag 同名版本标签，例如 `v0.1.0`。不再生成或更新 `latest`；仓库中已有的历史 `latest` 保持原值。发布摘要记录镜像地址和 digest。部署和回滚优先选择确定的 SHA 标签或 digest。

## 发布一个版本

在已同步远端的 `main` 上确认目标代码检查通过，选择尚未使用的新版本号。以下为命令示例，需将 `v0.1.0` 替换为本次版本；此文档不会自动创建或推送 tag：

```sh
git pull --ff-only origin main
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
git rev-parse "v0.1.0^{commit}"
```

tag 推送会重新执行检查，全部通过后发布三服务镜像。到 Actions 查看**该 tag 对应运行**，确认检查和三个 publish job 全部成功，再将最后一条命令得到的完整提交 SHA 交给运维。`main` 的检查通过只代表代码通过验收，不代表该提交已有发布镜像。

已发布 tag 不应移动或覆盖；修改代码后使用新版本号。若某仓库推送暂时失败，重跑该 tag 运行中失败的 publish job；无需移动 tag 或重复发布相同版本。手动新建 workflow_dispatch 运行仅执行检查。

## 使用已发布镜像

运维部署首选[服务器手册](server.md)的 `release.py --server` 流程，支持 HTTP/HTTPS IP:端口、配置校验及成功版本记录。以下手动配置方式适用于本地镜像检查；不包含服务器入口覆盖。

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

Windows/WSL 可将 `docker compose` 替换为 `.\scripts\compose.ps1`。`migrate` 使用拉取的 API 镜像，随后 `agent-migrate` 使用相同版本的 Agent 镜像，两者成功后才启动 Agent/API。运行 Agent 不执行 DDL。使用发布镜像时采用 `--no-build`；`scripts/start.ps1` 面向本地源码构建。

上述手动启动以新环境或已经完成 M7.25 所有权升级为前提。已有卷必须先按[服务器手册§33](server.md#33-agent-独立迁移与运行权限收紧)补齐迁移凭据和执行显式维护升级；CI 的随机测试凭据不能用于服务器。HNSW维护采用固定SHA的一次性 `agent-migrate` 容器，不能把迁移凭据传入运行服务。

当前 CD 交付到镜像仓库。尚未配置目标服务器、SSH 凭据或自动上线作业；目标机执行上述拉取和更新命令。应用镜像回滚需要确认数据库迁移兼容，不自动回滚数据库 schema。

权限收紧后，M7.25 之前的 Agent 镜像会因启动时执行 DDL 或缺少独立迁移模块而不兼容；不可通过恢复运行 DDL 让其上线。应用回退只选择已支持独立迁移且符合已有图/历史限制的配套版本，首次跨此边界失败保持维护状态并向前修复，必要时按手册恢复升级前一致备份到新空库。

M6.3 新增完整 SHA 的 `scripts/release.py`，按迁移/服务 readiness gate 更新并保留成功版本，支持显式应用回滚；TLS、OTel、Prometheus/Grafana/Jaeger override 和本地验收见[部署与监控](../docs/m6-deployment-monitoring.md)。CI 同时验证 Compose/TLS 配置、Prometheus 规则及 Collector 配置；当前不会自动连接服务器。

release.py 的固定镜像变量仅作用于本次发布，不写回根 `.env`。发布后重建容器应继续使用相同 SHA 和入口参数调用 release.py，避免裸 Compose up 切换到默认本地镜像。

镜像只构建一次并加载到 runner，再依次推送同一镜像的 GHCR/ACR 标签。每个标签推送最多三次、每次最多十分钟，失败明确标记所在仓库和标签；全部成功才生成发布摘要。这样单个仓库暂时超时不会立即丢失整轮构建结果。
