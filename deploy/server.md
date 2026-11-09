# FlowDesk 单机服务器部署手册

本文供运维人员从空白 Linux 服务器部署已发布镜像，默认使用 **IP:端口**，无需先准备域名。支持 HTTP 和 HTTPS 两种入口；浏览器始终同源访问 Web，Nginx 将 `/api/` 代理到 Go，Python 仅在容器网络内提供服务。

适用范围：Ubuntu 24.04 LTS、x86_64、单机 Docker Compose。当前提供配置及本地隔离验收，未在真实远程服务器执行安装或上线。高可用、多副本、外部数据库、自动证书续期及自动 SSH 部署另需方案。镜像交付流程见 [CI/CD](ci-cd.md)，Windows 开发见[本地运行](../docs/runtime.md)。

## 1. 交接信息与部署方式

开始前准备以下信息。文中的 `203.0.113.10` 是文档示例地址，必须替换为用户实际访问的服务器 IPv4 地址。

| 项目 | 本文约定 |
| --- | --- |
| SSH | 运维账号可 sudo；事先确认 SSH 管理端口和来源地址 |
| 工作目录 | `/opt/flow-desk`；同一目录只运行一个 `flowdesk` Compose 项目 |
| HTTP 入口 | `http://203.0.113.10:13000`；受控网络演示，`APP_MODE=dev`、非 Secure Cookie |
| HTTPS 入口 | `https://203.0.113.10:13443`；`APP_MODE=pro`、Secure Cookie、证书含对应 IP SAN |
| 发布版本 | `v*` tag 对应 Actions 检查及三个发布任务成功后的完整 40 位提交 SHA |
| 镜像仓库 | GHCR 或阿里云 ACR 公网地址，三服务必须选择同一仓库和 SHA |
| 模型 | 首次用 mock 验证部署；接真实模型时另备兼容接口地址、模型名及密钥 |
| 建议起步资源 | 4 vCPU、8 GB 内存、40 GB 可用磁盘；这是小规模演示起点，需按知识量和并发测量扩容 |

HTTP 会明文传输登录和业务数据，适用于内网、VPN 或限制来源的演示环境。需要加密时选择 HTTPS IP 入口。IP 访问与域名访问的业务功能相同。

## 2. 安装基础软件

已安装 Docker 的机器先检查版本，不要混装 Ubuntu Docker 包与 Docker 官方 CE 包。以下命令用于全新 Ubuntu 24.04，以普通 sudo 运维账号执行：

```bash
sudo apt-get update
sudo apt-get install -y software-properties-common ca-certificates curl git python3 openssl
sudo add-apt-repository -y universe
sudo apt-get update
sudo apt-get install -y docker.io docker-compose-v2
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"
```

退出 SSH 并重新登录，使 docker 组生效，再检查：

```bash
uname -m
python3 --version
docker info >/dev/null
docker compose version
```

预期架构 `x86_64`、Python 3.12 或以上、Docker 服务可用、Compose **2.24.4 或以上**。新入口利用 `!override` 替换端口映射，旧版 Compose 会报标签解析错误；不能删除标签后继续部署。Ubuntu 包来源见[软件包说明](https://packages.ubuntu.com/noble/docker-compose-v2)，端口替换语义见 [Compose 合并文档](https://docs.docker.com/reference/compose-file/merge/)。docker 组拥有高权限，成员应限于运维账号，参见[官方说明](https://docs.docker.com/engine/install/linux-postinstall/)。

部署已发布镜像不需要宿主机 Go、Node、pnpm、Python 虚拟环境或模型 SDK。

## 3. 获取与镜像匹配的配置版本

先由研发按 [CI/CD 发布步骤](ci-cd.md)推送 `v*` tag，在 [Actions](https://github.com/halpers/flow-desk/actions) 确认该 tag 运行的检查及 API/Agent/Web 三个发布任务全部成功，记录完整提交 SHA。`main` 的日常检查和手动运行不发布镜像，不能仅凭绿色检查选择尚未发布的提交。目标版本必须包含本手册和 `scripts/bootstrap-server.py`，不能用更早版本的镜像替代 HTTP IP 兼容版本。

```bash
sudo install -d -m 0750 -o "$USER" -g "$(id -gn)" /opt/flow-desk
git clone https://github.com/halpers/flow-desk.git /opt/flow-desk
cd /opt/flow-desk
FLOWDESK_REVISION=REPLACE_WITH_PUBLISHED_40_CHAR_SHA
git fetch origin
git checkout --detach "$FLOWDESK_REVISION"
test "$(git rev-parse HEAD)" = "$FLOWDESK_REVISION"
```

将变量值替换为真实 SHA 后再执行，勿保留 `REPLACE_WITH_...`。已有目录升级时不重新 clone、不覆盖 `.env`，按第 10 节操作。配置与镜像须来自同一版本，不能只拉取 `main` 配置配旧镜像。

## 4. 创建服务器配置

### 4.1 默认：HTTP IP:端口

在仓库根目录执行，地址替换为浏览器将使用的实际地址：

```bash
python3 scripts/bootstrap-server.py --origin http://203.0.113.10:13000
chmod 600 .env
```

脚本从 `.env.example` 创建忽略提交的 `.env`，为数据库三个角色、JWT、服务间令牌、演示账号和 Grafana 生成独立随机凭据，不打印密钥。已有 `.env` 时拒绝覆盖。HTTP 入口自动配置：

```dotenv
WEB_BIND_ADDRESS=0.0.0.0
WEB_HOST_PORT=13000
ALLOWED_ORIGINS=http://203.0.113.10:13000
APP_MODE=dev
COOKIE_SECURE=false
```

`0.0.0.0` 是监听地址，不能当作浏览器地址或 Origin；有公网 NAT 时，Origin 用用户实际访问的公网 IP，监听仍可使用 `0.0.0.0`。也可用 `--bind-address <服务器本机IPv4>` 限制监听网卡。当前脚本支持 IPv4 或域名，不支持 IPv6 入口。

更换地址或端口时，必须同时修改 `.env` 的 `WEB_HOST_PORT`、`ALLOWED_ORIGINS`，以及对应防火墙规则，再按原 SHA 重新执行发布命令。Origin 包含协议、主机和非默认端口，不含路径、尾部斜杠或多个地址。浏览器只能使用这个已配置的入口登录和刷新会话。

### 4.2 可选：HTTPS IP:端口

尚未创建 `.env` 时，改用以下初始化命令；已创建时手工调整表中四项，脚本不会覆盖旧文件：

```bash
python3 scripts/bootstrap-server.py --origin https://203.0.113.10:13443
chmod 600 .env
```

| 配置 | HTTPS IP 示例 |
| --- | --- |
| `WEB_HOST_PORT` | `13443` |
| `ALLOWED_ORIGINS` | `https://203.0.113.10:13443` |
| `APP_MODE` | `pro` |
| `COOKIE_SECURE` | `true` |

证书路径固定为 `runtime/server-tls/server.crt`、`runtime/server-tls/server.key`。正式使用时放入客户端信任的证书链和私钥；证书的 SAN 必须包含访问 IP。若暂用自签证书进行受控验收，可按以下方式生成 30 天证书，已有证书时命令会停止：

```bash
FLOWDESK_SERVER_IP=203.0.113.10
mkdir -p runtime/server-tls
test ! -e runtime/server-tls/server.crt && test ! -e runtime/server-tls/server.key && (
  umask 077
  openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
    -keyout runtime/server-tls/server.key -out runtime/server-tls/server.crt \
    -subj "/CN=$FLOWDESK_SERVER_IP" -addext "subjectAltName=IP:$FLOWDESK_SERVER_IP"
)
chmod 600 runtime/server-tls/server.key
openssl x509 -in runtime/server-tls/server.crt -noout -dates -ext subjectAltName
```

客户端需信任自签证书；仅绕过浏览器警告不等于安装信任。不要分发私钥。证书续期后执行 `docker compose restart web` 重新加载；发行脚本只读证书，不签发或自动续期。

如将来改用域名，替换 Origin、证书 DNS SAN 和端口即可，Web 无需重新构建。`deploy/compose.tls.yaml` 和 `configure-monitoring.py` 是 **localhost 开发模板**；服务器应使用下文 `--server --tls`，不能混用。

### 4.3 配置项核对

| 配置 | 运维要求 |
| --- | --- |
| `POSTGRES_PASSWORD` / `BUSINESS_PASSWORD` / `AGENT_PASSWORD` | 空数据卷首次初始化三个角色；已有卷不会因修改 `.env` 自动改密码，轮换需同时修改数据库角色密码与容器配置 |
| `JWT_SECRET` / `INTERNAL_TOKEN` | 所有服务保持一致；轮换会影响会话或服务间调用，需安排窗口 |
| `DEMO_PASSWORD` | 六个合成演示账号首次创建时的密码，重复迁移不重置已有账号密码 |
| `PG_HOST_PORT` / `API_HOST_PORT` | 默认 55432 / 18080，只绑定 127.0.0.1；同机多个应用注意冲突 |
| `MODEL_PROVIDER` | 首次保留 `mock`，部署验收不调用外部付费模型 |
| `MODEL_BASE_URL` / `MODEL_NAME` / `MODEL_API_KEY` | 真模型使用 `MODEL_PROVIDER=openai-compatible`，endpoint 一般到 `/v1`，密钥仅写服务器 `.env` |
| `EMBEDDING_BASE_URL` / `EMBEDDING_MODEL` / `EMBEDDING_API_KEY` | 独立 embeddings 接口；聊天接口不能自动充当 embedding 接口 |
| `EMBEDDING_DIMENSIONS` / `EMBEDDING_REVISION` | 默认 0 / 1；0 使用模型原生维度，变更模型或维度后需重建索引 |
| `RETRIEVAL_PROFILE` / `RERANK_PROVIDER` | 默认 `baseline` / `none`；先完成语义索引再切换 `semantic`，可选 `chat` 重排会产生聊天模型调用 |
| `TOOL_TRANSPORT` | `http` 或 `mcp`；二者都由 Go 执行当前授权、审批和业务事务 |
| `GRAFANA_ADMIN_PASSWORD` | 初始化脚本随机生成，监控首次启动使用；已有 Grafana 数据卷的密码不会随环境变量自动重置 |
| `FLOWDESK_*_IMAGE` | 使用 release.py 时由本次发布专用 env 文件统一覆盖；勿混合不同版本 |

自动生成密码使用十六进制，适合当前 DSN 拼接；不要随意替换为含 `@`、`$` 等字符的未编码密码。不要将 `.env`、`docker compose config` 的完整输出或容器环境发到工单和聊天中。

## 5. 开放网络与镜像拉取

云安全组和宿主机访问策略只放行选择的 Web TCP 端口（13000 或 13443）及 SSH 管理端口，尽量限制来源。数据库、API、Agent、监控不需要公网端口；Docker 发布端口应同时通过云安全组/宿主机 Docker 转发规则核查，不能只根据 UFW 界面判断。HTTPS 证书已准备好时，无需额外开放 80 端口。

服务器需要出站访问镜像仓库；使用真模型时还需访问相应聊天/embedding endpoint。浏览器直接访问服务器 Web 端口，无需跨域、API 地址或 Python 地址配置。

选择一个仓库前缀，私有仓库先交互登录，不把密码放进命令行：

```bash
# GHCR
FLOWDESK_REGISTRY=ghcr.io/halpers
docker login ghcr.io
```

或使用已配置的阿里云公网仓库：

```bash
FLOWDESK_REGISTRY=crpi-1bbvvu1xvdtg7e0p.cn-hangzhou.personal.cr.aliyuncs.com/flow-desk
docker login crpi-1bbvvu1xvdtg7e0p.cn-hangzhou.personal.cr.aliyuncs.com
```

GHCR 需要具备包读取权限的凭据；阿里云使用 ACR 仓库访问凭据。不要使用带 `-vpc` 的地址，除非另行部署了匹配的网络环境。GitHub Secrets 用于 CI 推送，不会自动登录目标服务器。

## 6. 首次发布

确认仍位于仓库根目录，`FLOWDESK_REVISION`、`FLOWDESK_REGISTRY` 已在当前 shell 设置。HTTP IP 入口：

```bash
python3 scripts/release.py --revision "$FLOWDESK_REVISION" --registry "$FLOWDESK_REGISTRY" --server --plan
python3 scripts/release.py --revision "$FLOWDESK_REVISION" --registry "$FLOWDESK_REGISTRY" --server
```

HTTPS IP 入口增加 `--tls`：

```bash
python3 scripts/release.py --revision "$FLOWDESK_REVISION" --registry "$FLOWDESK_REGISTRY" --server --tls --plan
python3 scripts/release.py --revision "$FLOWDESK_REVISION" --registry "$FLOWDESK_REGISTRY" --server --tls
```

需要监控时，在实际发布命令中同时加 `--monitoring`。`--plan` 只输出固定镜像与 Compose 文件选择，不拉取、不迁移，也不代表配置或证书已经通过验收。

实际发布依次验证配置、拉取三个 `sha-<完整SHA>` 镜像、启动 PostgreSQL、执行 owner 业务迁移、启动 Agent 并迁移自身 schema、等待 Go 和 Web readiness。所有门禁通过后写入 `runtime/releases/state.json` 的 current/previous；失败保留 pending 与上次成功记录，不执行数据库 Down 或删除数据卷。

Go 运行账号为 `fd_business`，Python 为 `fd_agent`；owner 仅用于初始化/业务迁移。当前 Python 运行角色仍负责自身 DDL，独立迁移作业和进一步收紧 DDL 属于后续运维改造。四个基础常驻服务使用 `restart: unless-stopped`，Docker 服务设为开机启动后会恢复未手工停止的容器。

**后续重新创建容器仍使用 release.py 并重复相同入口/监控参数。** 本次固定镜像 env 记录在 `runtime/releases/`，不会改写根 `.env`；直接运行裸 `docker compose up` 或 `scripts/start.ps1` 可能换成本地 latest/源码镜像并丢失入口选项。`ps`、`logs`、`exec`、`stop`、`restart` 可用根 Compose 操作已有同名服务。

## 7. 上线验收

服务器本机先检查：

```bash
docker compose ps --all
curl --fail http://127.0.0.1:18080/health/ready
docker compose exec -T agent python -c "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:8000/health/ready').status)"
```

常驻服务应为 running/healthy；首次发布脚本通过 `run --rm migrate` 运行迁移，`ps` 不一定显示已退出的 migrate 容器。HTTP 入口检查：

```bash
curl --fail http://203.0.113.10:13000/ -o /dev/null
curl -s -o /dev/null -w '%{http_code}\n' http://203.0.113.10:13000/api/v1/auth/me
```

预期页面 200，未登录 `/auth/me` 为 401。HTTPS 使用对应入口；自签证书的命令示例：

```bash
curl --cacert runtime/server-tls/server.crt --fail https://203.0.113.10:13443/ -o /dev/null
```

在实际客户端浏览器完成以下检查，并记录镜像 SHA、入口、时间和结果：

1. 用 `demo.operator` 和服务器 `.env` 的 `DEMO_PASSWORD` 登录；新建会话，发送“查询 FD202611020002 的订单状态和物流”，等待结果和流式进度。
2. 刷新页面，登录态和会话仍可恢复，说明 Origin、Cookie 和代理可用。
3. 发起物流跟进工单，检查确认卡；只批准一次，确认出现唯一回执，刷新后可查看工单。
4. 使用 `demo.supervisor`、`demo.admin` 检查主管与知识/团队管理入口；`other.operator`、`other.supervisor`、`other.admin` 是另一个合成租户，不能读取第一租户对象。
5. 从未放行来源检查服务访问策略，确认数据库、API 及监控端口没有对公网开放。
6. 用管理员上传带文本层的合成 PDF（不超过 2 MiB/50 页），等待索引就绪，预览分页文本再激活；在会话中查询并打开带页码引用，撤回后刷新确认内容受限。默认“仅文本层”时扫描件显示需要 OCR，不能激活；M7.3 的中英文识别按第 14 节验收。参见 [PDF 范围及错误码](../docs/m7-knowledge-files.md)和[OCR 说明](../docs/m7-ocr.md)。

这六个账号用于合成演示，当前不提供完整的生产用户入职、统一身份接入和密码管理平台。接入真实客户前需另行确定身份接入与账号治理。

普通 HTTP 的非 localhost 浏览器环境可能没有 `crypto.randomUUID()`；前端已通过 `crypto.getRandomValues()` 生成 UUID v4，保持请求幂等身份。部署时必须使用包含该修复的 Web 镜像。浏览器 API 的限制见 [randomUUID](https://developer.mozilla.org/en-US/docs/Web/API/Crypto/randomUUID) 与 [getRandomValues](https://developer.mozilla.org/en-US/docs/Web/API/Crypto/getRandomValues)。

## 8. 接入真模型和语义检索

先用 mock 完成上节验收，再编辑 `.env` 设置聊天和 embedding 配置。真模型输出与供应商可用性需要单独联调，部署成功不代表真实模型质量验收通过。

变更配置后，使用当前发布 SHA 和原入口/监控参数再执行一次第 6 节发布命令；无需构建镜像。保留 `RETRIEVAL_PROFILE=baseline`，待 Agent 加载 embedding 配置后，由可信运维人员按实际租户逐个重建：

```bash
docker compose exec -T agent python -m flowdesk_agent.reindex --tenant-id REPLACE_WITH_TENANT_UUID --all
```

租户 UUID 从已登录账号的 `/api/v1/auth/me` 结果确认，替换占位符；不要依靠模型输出决定租户。重建成功后再将 `RETRIEVAL_PROFILE=semantic`，可选 `RERANK_PROVIDER=chat`，使用相同发布参数重建容器。发生检索问题时改回 baseline/none 并重新发布，旧索引保留。详细预算、重建和错误码见[语义 RAG](../docs/m6-semantic-rag.md)。不直接在真实服务器运行会激活/撤回合成文档的本地 smoke 脚本。

## 9. 监控与日常操作

发布时加 `--monitoring` 后，Grafana/Prometheus/Jaeger 分别只绑定服务器 127.0.0.1 的 13001/19090/16686。客户端通过 SSH 隧道访问：

```bash
ssh -N -L 13001:127.0.0.1:13001 -L 19090:127.0.0.1:19090 -L 16686:127.0.0.1:16686 YOUR_USER@YOUR_SERVER_IP
```

如 SSH 不是 22 端口，加 `-p <实际端口>`。浏览器打开 `http://localhost:13001`，Grafana 账号 `admin`、密码为服务器 `GRAFANA_ADMIN_PASSWORD`。检查 Prometheus Targets、FlowDesk Dashboard 和 Jaeger 的 flowdesk-agent trace。当前有指标规则和面板，未配置外部通知接收方；Jaeger 使用内存存储，重启会丢失 trace，业务回执与检查点仍在 PostgreSQL。

日常命令在服务器仓库根目录执行：

```bash
docker compose ps --all
docker compose logs --tail 100 api agent web
docker compose logs --tail 100 postgres
docker compose stop web api agent
```

停止后恢复，使用第 6 节相同固定 SHA 的发布命令。排障日志按 request_id/run_id/trace_id 关联，不向外复制原始敏感数据。SSE 代理保持无缓冲；前面另有负载均衡器时，也应关闭 `/api/v1/runs/*/events` 缓冲并允许长连接。

## 10. 升级与回滚

包含 M7.2 时应同时升级三个镜像，让 Go 执行 `2026110405_run_operations.sql`、Agent 追加 run/tenant 观测绑定表。无需新增公网端口、模型配置或代理路由。升级后用“任务记录”查询自己的任务，主管可切换“租户任务”；旧任务可能显示暂无执行记录，新任务应显示节点/用量或明确的未知标记。失败任务仅在无业务动作、无补充输入时可重试；有回执时回原会话核对，不能以重试代替恢复。不要在生产执行合成故障验收脚本；操作与本地复跑见[M7.2](../docs/m7-run-operations.md)。旧镜像会忽略重试关联，回退前处理新建重试任务，不执行数据库 Down。

升级前记录 current SHA、配置与证书版本，执行第 11 节备份；确认迁移和图版本兼容，并在维护窗口处理仍运行/待批准任务。当前支持 read-v1/ticket-v1/ticket-v2，但旧镜像不一定识别较新的图和动作，不能仅凭数据库兼容决定可回滚。

在原目录更新配置到目标 SHA，保留 `.env`、runtime、数据卷：

```bash
FLOWDESK_REVISION=REPLACE_WITH_NEW_PUBLISHED_40_CHAR_SHA
git fetch origin
git checkout --detach "$FLOWDESK_REVISION"
python3 scripts/release.py --revision "$FLOWDESK_REVISION" --registry "$FLOWDESK_REGISTRY" --server --plan
python3 scripts/release.py --revision "$FLOWDESK_REVISION" --registry "$FLOWDESK_REGISTRY" --server
```

HTTPS/监控部署须在上述两条命令中继续携带 `--tls` / `--monitoring`。发布存在短暂重建窗口，当前不承诺无停机。

```bash
python3 scripts/release.py --rollback --plan
python3 scripts/release.py --rollback
```

完整成功后 rollback 恢复 previous；失败且存在 pending 时优先恢复上次成功的 current。首次发布没有任何成功记录时不能回滚。回滚恢复记录中的仓库、SHA、服务器/TLS/监控选项，不回滚数据库、`.env`、证书或仓库文件；如这些配置发生变化，先恢复匹配配置并确认当前脚本/Compose 可表达目标版本，再执行回滚。强制关机留下 release.lock 时，先确认没有发布进程，再人工清理该锁；不要删除 state.json/pending.json 来绕过诊断。

## 11. 数据备份与恢复演练

默认数据库持久化在 `flowdesk_pgdata` 命名卷，PostgreSQL 18 挂载 `/var/lib/postgresql`。禁止删除卷来解决迁移、密码或权限问题。迁移不会重置已有数据。

以下命令适用于 Linux bash，在仓库根目录创建一致性逻辑备份；不需要在宿主机安装 PostgreSQL 客户端：

```bash
umask 077
FLOWDESK_BACKUP="/opt/flow-desk/storage/backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$FLOWDESK_BACKUP"
docker compose exec -T postgres pg_dump -U flowdesk_owner -Fc flowdesk > "$FLOWDESK_BACKUP/flowdesk.dump"
test -s "$FLOWDESK_BACKUP/flowdesk.dump"
docker compose exec -T postgres pg_restore --list < "$FLOWDESK_BACKUP/flowdesk.dump" > "$FLOWDESK_BACKUP/contents.txt"
sha256sum "$FLOWDESK_BACKUP/flowdesk.dump" > "$FLOWDESK_BACKUP/SHA256SUMS"
```

`pg_dump` 包含 business/agent/knowledge 和检查点，使用同一数据库快照；它不包含集群角色、环境密钥、证书、镜像或发布记录。应另外加密备份 `.env`、`runtime/releases/`、服务器证书及角色恢复方案，保留匹配的源码 SHA/镜像 digest，将备份复制到独立受控存储并配置保留周期。备份中含业务和认证数据，权限与密钥同级管理。启用 M7.4 S3 后，数据库备份不含已归档 PDF；必须同时备份对象及其存储配置，具体见[原文件存储手册](source-storage.md)。默认 PostgreSQL 模式仍包含全部原文件。

恢复演练必须进入全新数据库，原数据库保持不变。以下在同一个 PostgreSQL 实例使用已有角色/扩展安装；先设置上一步的实际备份目录：

```bash
FLOWDESK_RESTORE_DB="flowdesk_restore_$(date -u +%Y%m%d%H%M%S)"
docker compose exec -T postgres createdb -U flowdesk_owner "$FLOWDESK_RESTORE_DB"
docker compose exec -T postgres pg_restore -U flowdesk_owner --exit-on-error -d "$FLOWDESK_RESTORE_DB" < "$FLOWDESK_BACKUP/flowdesk.dump"
docker compose exec -T postgres psql -U flowdesk_owner -d "$FLOWDESK_RESTORE_DB" -v ON_ERROR_STOP=1 \
  -c 'SELECT count(*) AS receipts FROM business.action_receipts; SELECT count(*) AS checkpoints FROM agent.checkpoints;'
```

`createdb` 遇到已有数据库应失败，不继续覆盖。比对关键表行数、约束、回执、检查点和知识索引，记录演练时间；新机器恢复时须先创建同名角色和所需 pgvector 扩展环境。通过验证后，另行安排停写和数据库切换窗口，当前 Compose DSN 固定使用 `flowdesk`，不能只改一个未经支持的数据库名变量就声称切换完成。本节不自动执行切换或删除演练库。

`scripts/check-backup.py` 是带 PowerShell 依赖的本地合成环境演练，不能作为上述 Linux 服务器恢复步骤的替代。应用镜像回滚不能代替数据库恢复。

## 12. 常见问题

| 现象 | 检查和处理 |
| --- | --- |
| IP 页面无法连接 | Web 容器是否 healthy；访问 IP 是否实际可路由；云安全组/防火墙是否放行对应 TCP 端口；是否使用了 `--server` |
| Compose 不识别 `!override` | 升级 Compose 至 2.24.4+，不要修改端口替换语义 |
| 镜像拉取 401/403 或 manifest 不存在 | 目标机重新确认仓库登录、读取权限、公网仓库及完整 SHA；三个 publish job 是否全部成功；CPU 是否 x86_64 |
| 登录后刷新退回登录页 | Origin 的协议/IP/端口必须与浏览器一致；HTTP 用 dev/false，HTTPS 用 pro/true；不要混用 localhost 模板 |
| IP 页面新建/审批失败且提示 randomUUID | Web 镜像版本过旧，使用包含 HTTP UUID 兼容修复的完整发布版本 |
| HTTPS 名称不匹配/证书不受信任 | 检查 IP SAN、有效期、完整链及客户端信任；不要把 localhost 证书复制到服务器 |
| 数据库密码错误 | 已有卷不会重新执行初始化；不要只改 `.env`，应协调修改角色密码并重建相关容器，不能删卷 |
| readiness 失败或迁移失败 | `logs --tail 100` 检查失败服务；保留 pending、上次成功 SHA 和错误码；不对业务表手工补 SQL“修状态” |
| 模型失败但页面健康 | 检查出站网络、兼容 endpoint/模型名、预算和脱敏错误；服务 ready 不代表供应商可用 |
| RETRIEVAL_INDEX_NOT_READY | 使用当前 embedding profile 完成该租户知识重建，或回退 baseline 后重新发布 |
| SSE 一次性返回或频繁断开 | 检查额外代理的缓冲/超时，浏览器刷新后应从持久快照恢复；不要重发新 action 代替原任务恢复 |
| 已有回执但总结失败 | 先按回执检查真实工单，业务可能已提交，不能新建 action 重复执行 |

交付时附上：服务器/入口、源码与镜像 SHA、启用的 TLS/监控/模型选项、配置与备份保管位置、验收结果、值守人及回滚目标。实际本地验收记录见[服务器入口配置验收](../reports/m6/server-entry.md)；该记录不代表已执行远程上线。

## 14. M7.3 扫描 PDF OCR 升级与验收

按第 10 节备份及发布步骤一起升级 api/agent/web 到同一个已发布 SHA；Go 自动追加 2026110406_knowledge_ocr.sql。识别组件和中英文语言包已打入 Linux Agent 镜像，不需服务器另装 Tesseract、配置云 OCR 密钥或开放新端口。语言/包版本见源码 system-packages.lock，更新组件需同步解析版本和扫描样本验收。IP:port 与域名入口使用相同上传/API 路由。

运维在授权管理员账号中打开“知识库”，创建专用验收知识库，上传不含客户信息的中英文扫描 PDF（不超过 2 MiB），选择“自动识别无文字页”；含页眉文字和扫描正文时选“整页扫描识别”。等待就绪，确认 OCR 页数及物理页号，逐页预览文字。核对无误后点击激活，勾选“我已核对 OCR 提取文本”才可确认；服务端同样强制检查。管理员不能凭识别结果猜测金额或批准业务。

用有阅读权限的操作员发起相应政策问题，打开页码引用确认 OCR 提醒，再由管理员撤回，刷新后旧答复与引用应受限。每次最多 OCR 10 页，超限拆分；45 秒识别总超时、单页渲染/识别另有限时。未就绪组件显示 PDF_OCR_UNAVAILABLE，检查是否发布了配套 Agent 镜像；PDF_OCR_FAILED/EMPTY/TIMEOUT 分别检查扫描质量、是否有印刷文字以及拆分文件。排障以 job/version/request ID 为线索，不复制真实文件正文或密钥到日志。

回退旧版本前暂停 OCR 上传，等待或按原流程处理待索引任务，撤回所有 OCR 版本并核对活跃业务任务；旧 Agent 不能读取新 OCR manifest，不可直接恢复它来声称兼容。保留数据库和审计，不执行 Down。详细边界、固定样本及仅供独立 mock 环境的脚本见[OCR 交付说明](../docs/m7-ocr.md)，禁止直接在生产运行合成验收脚本。
## 15. M7.5 PDF 阅读方式升级与验收

按第 10 节备份并把 api/agent/web 一起发布至包含 M7.5 的同一 SHA，Go 追加 `2026110502_pdf_layout.sql`。本次无需新增端口、服务、环境变量或服务器原生依赖；原始顺序仍为默认，旧文档不自动重新解析。IP:port 与域名使用相同操作。

以管理员登录，在专用验收知识库上传不含客户信息的规则双栏文本 PDF，保持“仅文本层”，选择“双栏先左后右”。等待索引就绪，预览每一页并与下载原件对照，确认左栏结束后再到右栏，页眉页脚和物理页码正确。使用规则文字表格另建版本，选择“表格行列对齐”，检查每行名称、期限、金额等是否对应；横向滚动查看整行。核对后点击激活并勾选确认，操作员查询政策并打开页码引用，撤回后刷新确认引用受限。

PDF_LAYOUT_UNSUPPORTED 表示旋转、栏间歧义或超宽等不受支持版面。不要盲目换方式发布；可重新导出简洁文本层 PDF，或转为经人工核对的 TXT/Markdown 新版本。扫描件继续使用 OCR，当前不能与双栏/表格模式合用；复杂合并单元格、多栏和跨页表格语义不在保证范围。

回退 M7.5 以前的镜像前，暂停非 plain 上传并处理所有待索引/重试任务，撤回非 plain 活跃版本，检查引用这些版本的运行任务和待批准动作；旧 Agent 不认识新 parser manifest，不能恢复新模式的索引任务。需继续读取这些 manifest 时保留配套新 Agent/API。保留数据库字段、冻结结果和审核审计，不执行 Down。若同时回退至 M7.4 以前，另按[原文件存储手册](source-storage.md)恢复所有 S3 文件。仅供独立 mock 环境的自动验收和限制见[M7.5 说明](../docs/m7-pdf-layout.md)；生产按本节人工验收，不运行合成脚本。
## 16. 账号与角色管理升级及交接

M7.6 无新增环境变量，API/Web 使用同一发布 SHA，迁移由发布流程执行。升级前备份数据库，确认至少一个启用的内置 admin 能正常登录，并安排第二位可信管理员。先完成或取消待审批任务，进入维护窗口后再调整账号；被撤销的旧任务不能靠新登录继续执行。每租户最多 500 个账号、50 个自定义角色，账号最多 8 个角色；超出该规模先评估容量再迁移使用。

迁移 2026110503_accounts_roles.sql 保留原密码、用户、会话和历史业务，回填内置基础角色及角色绑定。账号变更后才撤销相关会话。更改 .env 的 DEMO_PASSWORD 不会覆盖已存在账号；需要通过本节页面重置。

管理员交接步骤：

1. 用已有管理员登录正常 IP:端口或 HTTPS 入口，点击“账号与角色”。确认本租户账号和 operator/supervisor/admin 三个只读角色存在。
2. 新增实名管理账号，选择内置 admin；密码需 12–72 UTF-8 字节且包含字母及数字。通过受控渠道交付本人，不能放进工单、截图或 Git。
3. 在独立浏览器配置文件中用新账号登录，确认能打开管理页。保留至少两位能登录的实名管理员；不要先停用唯一可用的管理员。当前账号的密码、角色、名称或启停由另一管理员维护。
4. 由新管理员重置或停用不再使用的初始化账号。重置/编辑会撤销目标全部设备的会话；目标需重新登录。已提交的工单、回执和审计保留，旧批准执行凭证失效。
5. 普通账号选择 operator 或 supervisor，可附加自定义增量角色；多个基础角色取最高级别。按需创建“知识维护”等自定义角色，勾选必要权限再分配。account:manage 和 role:manage 可创建/授予高权限身份，只授予可信管理人员。

人工验收使用经批准的验证账号：创建后在独立浏览器登录，检查基本权限；附加/停用自定义角色后旧浏览器重新访问应回到登录，新登录权限应变化；重置密码后旧密码不能登录、新密码能登录；停用账号后访问及刷新不能继续。自定义 knowledge:manage 仍受基础角色 ACL 和团队范围约束，不能预览/下载不可访问的知识文件。内置角色不可修改，最后一个启用的 admin 不可移除；并发保护已由隔离数据库用例验证，不在生产做并发停用演练。

版本冲突时关闭编辑框，点“刷新管理数据”，重新打开表单核对后提交。网络错误且内容未变时，可直接点击原表单保存以同键重试；重置重放不会再次撤销重置后新建的登录。停用的角色保留关联，编辑账号时应移除停用角色，不能新增分配停用角色。完整语义及自动验收证据见[账号管理说明](../docs/m7-accounts.md)；合成脚本仅供可丢弃 mock 环境，不在生产运行。

回退到 M7.6 之前时，旧 API 不认识自定义权限及管理接口，不能直接认为角色行为等价。应先在新版本管理页将每个账号收敛为单一内置角色、移除所有自定义关联并停用自定义角色，处理失效任务、确认至少两位管理员和新密码可用；保持维护窗口、排空在途请求后再回退 API/Web。数据字段、角色、历史审计及密码 hash 保留，不执行 Down，不用 SQL 手工改投影或会话。仍依赖多角色/自定义权限时保留 M7.6 配套服务。回退更早版本还需分别遵循本手册 OCR/PDF/S3 的回退要求。

## 17. 知识版本保留与清理

先执行第 10 节的备份与发布流程，将 API/Agent/Web 升级到包含 M7.7 的同一 SHA。Go 增量迁移为 `2026110504_knowledge_retention.sql`，Agent readiness 前自动新增永久墓碑表。不增加端口、环境变量或外部服务；域名与 IP:port 入口操作一致。

管理员进入“知识库”，在目标文档点“版本保留与清理”。默认保留最近 3 个版本、至少 30 天；M7.9 起可按第 19 节配置本库参数。逐项核对版本 ID、文件名、原大小、日期和保护原因；刚上传的文件应显示最近版本/未满保留期且不能清理。当前激活、历史任务引用、索引未完成和任何归档历史都受保护，不能强制跳过。S3 对象不在此次删除范围；恢复到数据库也不会移除归档保护。

备份核对后，仅对可清理候选打开确认弹窗并勾选范围。提交先显示清理中，稍后刷新预览/版本，确认“内容已清理，元数据保留”，下载和激活应禁用。网络中断时保留原弹窗，再次确认会复用幂等请求；不要手工删索引或改 cleanup_state。原文件大小是历史元数据，不是当前占用量；VACUUM、数据库磁盘收缩和备份保留另行管理。此功能保留历史及共享缓存，不代表删除所有内容副本。

长时间清理中时检查 Agent/API readiness、服务认证配置及两服务日志，使用 version/request ID 排障，不复制正文或密钥。清理 outbox 会持续退避重试，上限间隔 30 秒；Agent 恢复后继续推进，原文在索引确认前仍保存在数据库。若持续报 KNOWLEDGE_CLEANUP_UNAVAILABLE，应修复通信/数据库问题并等待重试，没有取消墓碑或手工强行完成接口。

一旦已有 cleanup_pending 或 purged 版本，禁止直接回退到不认识生命周期/墓碑的旧 API 或 Agent，也不能只恢复其中一个 schema。若必须恢复清理前内容，应维护停写、按第 10 节将完整数据库备份恢复到新库（包含 business 和 agent 等 schema），恢复配套镜像并核对引用和索引；这会恢复备份时间点的全量状态。生产环境不要修改版本创建时间来制造候选。自动合成验收仅限独立 mock 配置，见[清理设计与验收](../docs/m7-retention.md)。

## 18. 归档副本清理

M7.8 仅清除原件恢复到 PostgreSQL 后，已登记旧 S3 副本的文件字节；保留零字节防重写标记、数据库原件及版本历史。默认关闭，不增加端口，IP:port 与域名入口操作相同。先升级同一 SHA 配套服务及迁移 `2026110505_source_objects.sql`，备份数据库和完整对象集合；存储兼容、IAM 和开关配置按[文件存储部署](source-storage.md#归档副本清理m78可选)。

1. 运维确认桶从未开启版本控制、应用具备桶级 GetBucketVersioning 及对象条件读写权限，独立测试桶协议探针通过后，设 `FILE_STORE_CLEANUP_ENABLED=true`，按现有发布方式和全部 override 重建 API。不能关闭已有桶的版本控制来绕过限制，Suspended 仍拒绝。
2. 管理员进入“知识库”，找到 PDF 版本，先点“下载原文件”，按备份及上传样本核对 hash；点击“恢复至数据库”，刷新后再下载核对。当前仍为 S3 的原件受保护，不能直接清理。
3. 点“归档副本清理”，刷新预览，核对副本 ID、归档批次、历史大小和原因。只对“可清理的冗余副本”打开确认，勾选“我已核对数据库原件与备份”后提交。清理中等待自动刷新，最终显示“副本字节已清理”，再下载数据库原件复核。历史大小不会变为零，不代表仍占用相同字节。
4. 响应丢失时在原确认框直接重试，沿用同一请求键；SOURCE_CHANGED 则关闭、刷新并重新核对。持续清理中时排查 API/S3 网络、版本查询权限、原对象 hash 和条件写支持；outbox 的 SOURCE_CLEANUP_UNAVAILABLE 持续退避重试，间隔上限 30 秒。没有取消标记或强制完成按钮。

新归档使用新代次对象，不覆盖旧标记。禁止手动删除/过期标记、修改 source_objects 状态、让生命周期清除当前原件；这会破坏迟到任务的隔离。迁移只回填当前 s3 定位，迁移前已恢复并丢失定位的历史孤儿不在自动处理范围。此功能不减少对象数，不回收供应商历史版本，也不清除备份或整机缓存。M7.7 的“归档历史”版本保护继续生效，清理旧副本不会使该版本可删除。

停用新清理可将开关恢复 false，待处理任务会保留并重试，修复后重新开启继续完成。回退到 M7.8 前必须先保持新版本将 knowledge.source_cleanup 的 pending/delivering 排空，并停写/排空在途归档 worker；旧 API 不再按新代次登记副本，后续旧版产生的冗余副本不能视为已纳管。永久标记和登记表必须保留，不执行 Down。已有 M7.7 清理状态仍遵守第 17 节回退限制；恢复备份时按文件存储手册使用同一停写窗口的数据库与完整对象/标记备份。

## 19. 知识库保留策略

M7.9 无新增环境变量、服务或端口。先按第 10 节备份数据库并发布同一 SHA 的配套服务，migrate 执行 `2026110506_retention_policy.sql`；已有库及新库默认保留最近 3 个版本、至少 30 天，升级本身不新增清理任务。先确认具备该知识库 knowledge:manage、当前角色 ACL 和团队范围的管理员能登录，IP:port 和域名入口步骤一致。

1. 打开“知识库”，选择目标库，核对“保留策略”卡片中的当前参数和策略修订；规则适用于本库全部文档，其他库独立配置。
2. 点击“编辑保留策略”，填写最近保留版本数 1–100、最短保留天数 1–3650。较小值可能增加清理候选；需结合本单位保留要求和完整备份决定参数，不因配置成功就自动清理。
3. 勾选“我已核对本知识库的保留要求”并保存，核对修订递增、参数正确。响应丢失时保持原表单重试，沿用同一请求键；编辑参数会生成新键。每次成功保存（包括相同值）都递增策略修订并记录一次 knowledge.retention_policy 审计，重放不会重复保存。
4. 再进入文档“版本保留与清理”，核对新规则、策略修订及每项保护原因，再按第 17 节逐项确认。若另一管理员期间修改规则，旧预览提交返回 RETENTION_POLICY_CHANGED（409）；关闭确认框、刷新预览并重新核对，不重用旧规则判断资格。

策略变更不解除激活/引用/索引中/归档历史保护，不取消或恢复 cleanup_pending/purged；已确认任务继续按原命令完成。文档版本和策略修订为两个独立校验，不应通过手工改数据库修订绕过冲突。旧客户端省略 expected_policy_revision 只可在初始修订 1 发起新清理；策略曾保存后，即使回到 3/30，也需升级客户端并获取新预览。升级前已提交的同键清理记录保持原摘要，可在权限仍有效时重放。

回退至 M7.9 前先进入维护窗口，在新版页面逐库恢复 3 个/30 天，排空 knowledge.cleanup 的 pending/delivering 和在途请求，刷新验证后停止新版本 API/Web 再切换旧镜像。管理员/owner 可用以下只读查询核对，不直接 UPDATE 策略：

```sql
SELECT count(*) AS custom_policy_count FROM business.knowledge_bases
 WHERE retention_keep_latest<>3 OR retention_minimum_age_days<>30;
SELECT status,count(*) FROM business.outbox_events
 WHERE topic='knowledge.cleanup' AND status IN ('pending','delivering') GROUP BY status;
```

custom_policy_count 必须为 0，第二条不应有结果，且需满足第 17/18 节及其他功能的回退条件。保留策略列、修订及审计，不执行 Down；仍依赖自定义规则时保留新版。恢复清理前内容仍要求完整一致数据库及对象备份，单独恢复策略不能找回原文。独立 mock 验收及协议见[保留策略说明](../docs/m7-retention-policy.md)；禁止在生产运行调整版本日期的合成脚本。

## 20. 完整版本历史与元数据归档

M7.10 无新增服务、环境变量或端口，IP:port 与域名入口操作一致。先按第 10 节做一致数据库备份，再发布同一 SHA 的配套镜像；migrate 增量执行 `2026110507_metadata_archive.sql`。升级仅创建字段、归档表及索引，不自动归档已有记录。大规模历史表创建索引可能持锁，安排维护窗口，先在恢复副本测量迁移时间再升级。

1. 以有 knowledge:manage 且能管理目标库的账号登录，“知识库”→目标文档→“完整版本历史”。确认文件名、版本 ID、原创建时间、清理/归档状态正常；“下一页/上一页”逐页查看，刷新历史回到第一页查看新上传版本。
2. 只有“内容已清理”且显示“可归档版本详情”的版本可归档。激活、引用、未完成知识任务和原文件归档历史仍受保护；不要为获得候选修改数据库日期、状态或保护信息。
3. 点击“归档版本详情”，核对范围与备份并勾选确认。成功后日常版本列表不再显示该版本，但“完整版本历史”保留原文件名、片段数、parser/OCR/版面等详情和归档时间。操作不恢复原文，也不减少 S3 对象数。
4. 响应丢失时在原确认框重试，使用原请求键。VERSION_CONFLICT 或 METADATA_ARCHIVE_PROTECTED 时关闭确认框、刷新并重新核对；权限/团队/会话已撤销时重新授权或登录，不通过改库绕过检查。成功归档仅记录一次 knowledge.metadata_archive 审计。

运维 owner 在维护窗口可用以下只读查询核对归档与热记录一致性。missing_archive_count 应为 0；异常时保留备份并排查，禁止直接补写或删除身份行。

```sql
SELECT count(*) AS archived_version_count FROM business.document_versions WHERE metadata_archived;
SELECT count(*) AS missing_archive_count FROM business.document_versions v
 LEFT JOIN business.version_metadata_archives a ON a.tenant_id=v.tenant_id AND a.id=v.id
 WHERE v.metadata_archived AND a.id IS NULL;
```

归档表与原版本、审计、幂等回执须包含在同一数据库备份中。原版本行、job/hash 和 Python 墓碑必须保留，不能按“已归档”手工删除。此功能把终态详情移出热行，身份锚点和归档表仍会增长；不保证 PostgreSQL 文件缩小，不清除数据库/WAL/对象备份，也不处理 source_objects、消息或 checkpoint 的历史保留。

回退前在维护窗口停止归档新请求并等待在途事务结束，查询 archived_version_count。为 0 时可在满足第 17–19 节约束后切换旧镜像，保留新表和列，不执行 Down。已有归档时，旧版不能正确展示精简后的热行，需使用支持归档读取的版本或从升级前一致备份整体恢复；不要将旧版 API 与新版 Web 混用，也不要只恢复归档表。独立 mock 验收脚本只用于临时环境，不能在服务器运行；详见[历史归档说明](../docs/m7-history-archive.md)。

## 21. S3 对象巡检与登记对账

M7.11 无新增服务、端口或数据库迁移；IP:port 与域名入口都支持。它只在仍有版本身份且当前账号有管理权限时，列举当前配置桶中的该版本前缀；不能发现身份已丢失的全桶孤儿。默认 `FILE_STORE_INVENTORY_ENABLED=false`，与归档副本清理开关独立，开启巡检不启用清理。

1. 按第 11 节备份数据库及对象，发布同一 SHA 的配套镜像。原文件存储须已配置为 s3，且原件读取/归档验收正常。
2. 通过存储管理端给 Go 专用账号追加目标桶 knowledge/ 前缀的只读列举权限。以下是 AWS S3 IAM 的增量示例，替换桶名；其他供应商使用其等价权限配置，原 GetObject/PutObject 权限保留。不授予 DeleteObject 或全桶通配权限。

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": "s3:ListBucket",
    "Resource": "arn:aws:s3:::flowdesk-sources",
    "Condition": {"StringLike": {"s3:prefix": "knowledge/*"}}
  }]
}
```

3. 先在独立测试桶确认 ListObjectsV2 支持 Prefix、StartAfter、MaxKeys、普通桶按键排序及无歧义 URL 编码。Directory bucket 和非排序列举不支持；SeaweedFS 4.52 对含空格对象名的编码不兼容，相关页会明确失败，不能通过替换加号猜测对象名。应用标准键只含 UUID、hash、规范代次和分隔符，不受此限制。
4. 在服务器 `.env` 增加 `FILE_STORE_INVENTORY_ENABLED=true`，保持文件权限 600。按第 10 节原发布方式、相同 overrides 重新创建 API；外部 S3 无需 storage 沙箱 overlay。readiness 不检查列举权限，继续执行页面验收。
5. 管理员或有 knowledge:manage 且能管理目标库的账号进入“知识库”→PDF 版本→“对象巡检”。确认观察时间、当前批次、原件大小和第一页；“下一页/上一页”逐页核对，“刷新巡检”回到第一页。只有成功页面记录一次 knowledge.source_inventory 审计。未配置或无权限时不能把失败当作对象不存在。

“当前原件/已登记，列表大小匹配”只比较列表元数据与登记，不下载或验证 PDF hash。“登记已清理，列表为零字节”只表示观察为零，不核验防重写标记。“未登记”“非标准定位”“登记信息异常”“列表大小异常”应保留审计和备份后人工调查，禁止直接清理或补写登记。需验证当前原件时下载并离线核对 SHA256；有完整性异常时暂停清理，按第 18 节排查。该页面没有认领、修复或删除按钮。

运维可用受控存储清单离线关联对象指纹：存储指纹为 `hex(SHA256(endpoint 去尾斜线 + 换行 + region + 换行 + bucket))`，默认 region 为 us-east-1；页面对象指纹为 `hex(SHA256(存储指纹 + 换行 + 对象 key))`。只在受控环境处理清单，不把桶/key/凭据粘贴到公开报告，也不要改数据库以满足页面状态。

| 现象 | 操作 |
| --- | --- |
| SOURCE_INVENTORY_DISABLED（409） | 核对显式开关及 API 重建；只需巡检时不要打开清理开关 |
| SOURCE_INVENTORY_UNAVAILABLE（503） | 核对 ListBucket 范围、网络、排序/编码和有界响应兼容性；保留原件，不将错误视为空页 |
| 参数/游标错误（400）或 INVENTORY_CURSOR_EXPIRED（410） | 刷新第一页；游标不跨用户、版本、存储配置复用，有效期 15 分钟 |
| SOURCE_CHANGED（409） | 观察期间或翻页前发生恢复/重新归档，刷新后重新核对 |
| 401/403/404 | 核对当前登录、角色权限、库 ACL 和团队范围；禁止绕过授权查其他租户 |

分页是实时观察而非快照；期间外部新增/消失的对象须刷新复核。它不检查未列出的已登记文件是否存在，不处理供应商历史版本，也不证明全库完整性。停用时将开关改回 false 并按原方式重建 API，可撤销新增 ListBucket；无数据迁移或对象回写需要撤销。回退旧镜像仍须满足第 17–20 节已有功能的约束，不能删除原身份、登记或永久标记。设计及独立合成验收见[巡检说明](../docs/m7-source-inventory.md)；验收脚本严禁在服务器业务库执行。

## 22. 未登记副本人工核验与登记

M7.12 沿用现有服务、端口和数据库表，无新增迁移。它为仍保留可信版本身份、原件已恢复到数据库的标准旧副本提供逐项人工登记；登记写入账本，不写 S3，不切换原件定位。清理仍需第 18 节的独立确认。

1. 按第 11 节备份数据库和对象，发布同一 SHA 的配套镜像。先按第 21 节确认巡检和原件读取正常；GetObject 与限定前缀的 ListBucket 权限已足够，不新增 DeleteObject 或更宽访问范围。
2. 在独立测试桶完成恢复→巡检→核验登记→副本管理验收后，服务器 `.env` 设 `FILE_STORE_INVENTORY_ENABLED=true`、`FILE_STORE_ADOPTION_ENABLED=true`，保持文件权限 600。按第 10 节相同发布配置/overrides 重建 API。清理开关保持实际运维策略，开启登记不会自动开启清理。
3. 进入目标 PDF 版本，点击“恢复至数据库”，等待“归档原文件”按钮出现；下载原件并离线比对 SHA256 与可信原样本/备份。再打开“对象巡检”并刷新。只有当前未登记、标准旧式/旧代次定位、列表大小匹配，且早于当前原件代次的副本可点击“核验并登记副本”。其他项保留保护原因，不直接改数据库状态。
4. 核对对象指纹、旧批次和大小，勾选“我已恢复并核对数据库原件，同意登记此副本”，点击“确认核验并登记”。Go 再读取副本，核对大小和 SHA256，并在重新检查当前权限、原件状态后登记。成功显示登记编号，记录一次 knowledge.source_adopt 审计。该操作不会清除副本字节或恢复损坏文件。
5. 响应丢失时留在原确认框重试，保留原选择凭证、原件代次和请求键；不要反复关框新建请求。已提交请求在 30 天幂等有效期内返回原回执，即使选择凭证到期、新登记开关已关闭或副本已按正常流程清理；仍要求当前账号拥有目标资源权限。新登记请求使用 15 分钟选择凭证，过期后需关闭确认框、刷新并重新核对。
6. 需要清理冗余字节时，另行进入“归档副本清理”，按第 18 节预览和确认。登记后的旧副本仍受原件、代次、版本状态、当前授权和存储能力保护；永久零字节标记及登记/回执不能手工删除。

| 现象 | 操作 |
| --- | --- |
| SOURCE_ADOPTION_DISABLED（409） | 核对巡检与登记两个开关和 API 重建，不通过开启清理替代 |
| SOURCE_ADOPTION_PROTECTED / SOURCE_CHANGED（409） | 重新核对数据库原件、旧代次和当前状态，刷新后逐项确认 |
| SOURCE_ALREADY_REGISTERED（409） | 可能已由另一请求完成，刷新登记记录；原请求重试使用原键 |
| ADOPTION_SELECTION_EXPIRED（410）或选择参数错误（400） | 未提交成功的新请求关闭确认框、刷新巡检后重选；凭证不跨用户/版本/存储使用 |
| SOURCE_COPY_LIMIT（429） | 达到每版本 100 个未 retired 副本上限，先人工核对并按第 18 节处理允许清理的旧副本 |
| SOURCE_COPY_MISSING（404） | 副本已消失，刷新列表并核对受控存储清单；不补写登记 |
| SOURCE_INTEGRITY_FAILED / SOURCE_UNAVAILABLE（503） | 保留数据库原件和备份，排查副本损坏、GetObject 权限或网络，不强行登记 |
| IDEMPOTENCY_CONFLICT（409） | 同键参数变化，核对原回执；不要以修改凭证的方式重试原键 |

内容核验是读取时的观察，外部存储随后仍可能变化；已有下载/清理继续复核完整性。未知版本孤儿、非标准定位、当前/未来代次和零字节对象不能凭此流程认领。登记不构成全库修复或永久一致性保证。

迁移前已恢复至数据库、但当前批次仍为 0 的版本，旧式无代次副本同样按 0 处理，保持同代保护。可先备份，再走正常“归档原文件”→“恢复至数据库”流程，等待完成后刷新巡检；原件/存储校验失败时继续保护，不能通过手改代次、删除标记或补写登记绕过。

暂停新登记时将 FILE_STORE_ADOPTION_ENABLED 改回 false，按原方式重建 API；已提交登记和审计保留，同键查询仍检查当前授权。回退兼容旧镜像时保留 source_objects、api_idempotency 和审计，并满足第 17–21 节原有约束，不执行 Down 或删除永久标记。独立合成脚本及完整边界见[人工登记设计](../docs/m7-source-adoption.md)，不能在服务器业务库运行验收脚本。

## 23. 租户对象巡检与归属核对

M7.13 在既有版本巡检之外，增加不依赖版本记录的当前租户对象发现。沿用现有服务、端口和数据表，无迁移，IP:port 和域名入口都可用。默认关闭，仅内置租户管理员可用；委派 knowledge:manage 不获得租户级发现权限。

1. 按第 11 节备份并发布同一 SHA 的配套镜像。先完成第 21 节的 ListObjectsV2、排序/编码、限定前缀 ListBucket 权限验证；不需要扩大到任意桶/前缀，也不授予 DeleteObject 或 ListObjectVersions。
2. 在服务器 `.env` 设置 `FILE_STORE_INVENTORY_ENABLED=true` 和 `FILE_STORE_TENANT_INVENTORY_ENABLED=true`，保持权限 600。按第 10 节原发布方式和相同 overrides 重建 API。副本登记与清理开关保持原策略，巡检不会自动启用它们。
3. 使用当前租户的内置管理员登录，“知识库”→“租户对象巡检”。无需选择文档。记录观察时间、扫描/显示数量和对象指纹，按“下一页/上一页”逐页核对；“刷新巡检”返回第一页。巡检只列当前租户 `knowledge/<tenant>/`，不扫描租户身份未知目录或其他租户。
4. “已关联当前可访问版本”项可进入原“对象巡检”，再按第 21–22 节处理已知版本；当前原件、旧副本、大小或名称摘要异常应分别核对。原版本权限始终独立检查，不能借租户入口查看已知但不可访问的库。
5. 对“未找到版本记录”“非标准对象”项，先保留备份及 knowledge.tenant_source_inventory 审计，再由获授权运维在受控存储清单中按第 21 节指纹公式关联。核对可信数据库备份、历史导入记录、对象修改时间和离线 SHA256；对象名中的租户/版本/hash 仅是线索，不能据此认领或删除。没有可信身份或恢复依据时保留隔离，提交人工调查，不直接改业务表、补登记或回写对象。

| 现象 | 操作 |
| --- | --- |
| TENANT_SOURCE_INVENTORY_DISABLED（409） | 核对两个巡检开关以及 API 是否按原 overrides 重建 |
| SOURCE_INVENTORY_UNAVAILABLE（503） | 检查限定 ListBucket、网络、普通桶排序与无歧义编码；失败不等于没有对象 |
| 400 或 INVENTORY_CURSOR_EXPIRED（410） | 刷新第一页；不跨用户、租户、巡检接口或存储配置复用游标，期限 15 分钟 |
| 401/403 或看不到入口 | 核对当前会话和内置管理员身份；自定义知识管理角色仍不能访问 |
| 显示数小于扫描数，甚至本页为空 | 已知版本可能因当前 ACL/团队不可访问而隐藏；若可点下一页则继续，不把空页当扫描结束 |
| 名称摘要/格式或登记异常 | 保留备份和审计，离线核对可信来源；不将名称匹配或列表大小匹配当作内容校验 |

M7.13 的实时巡检仅提供逐页列表观察与当前数据库关联；人工说明可使用第 25 节调查记录。巡检本身不保存调查结论，不核验文件内容/清理标记，不证明未列出的原件丢失；分页期间对象可能变化。自动认领/修复、物理删除对象名、供应商历史版本及租户身份未知目录仍待后续。停用时将独立开关改回 false，按原方式重建 API，既有版本巡检可保留；若所有巡检均停用可收回 ListBucket。没有新增对象或数据库迁移需要撤销，既有防重放记录和标记继续保留。

独立合成测试、实际结果及复跑步骤见[租户巡检设计](../docs/m7-tenant-source-inventory.md)和[存储手册](source-storage.md#租户范围对象巡检)。未在真实服务器或任意供应商环境验证，服务器业务库不得运行合成验收脚本。


## 24. 完整副本历史与退休登记归档

M7.14 新增迁移 2026110601_source_object_archives.sql。保持同一 SHA 的 API/Agent/Web；IP:端口和域名入口都可使用，不增加服务、端口、环境开关或 S3 权限。完整历史读取及手工归档均要求当前 knowledge:manage 和目标库权限，普通 operator 不开放。归档仅移动 PostgreSQL 退休登记，不删除存储标记，不承诺数据库总磁盘缩小。

1. 按第 11 节备份整库及对象，记录当前 SHA、迁移状态及备份位置。数据库备份必须包含 source_objects、source_object_archives、document_versions、api_idempotency、outbox_events 和 audit_events，不能按“历史”排除新表。对象备份仍包含零字节防重写标记。
2. 按第 10 节原有发布方式使用同一 SHA 配套镜像；迁移任务自动应用新表和索引，检查迁移成功、API readiness 和登录。保留既有清理/巡检开关值。开启历史登记归档无需额外存储权限或开关。
3. 管理员进入“知识库”→目标 PDF→“归档副本清理”→“完整副本历史”。已精简的 PDF 版本可从“完整版本历史”对应项进入。检查副本 ID、批次、原大小和时间，逐页查看；日常清理预览最多 101 条，完整历史可继续翻页。
4. 要归档旧登记，先按第 18 节独立完成副本字节清理，等清理任务 delivered。仅 retired、退休时间完整、早于当前代次、非当前原件定位、无未完成清理投递的记录可归档。点击“归档此副本登记”，核对备份后勾选并确认。不要手改状态、代次或投递以解锁按钮。
5. 成功后记录页面副本 ID 和归档时间；完整历史显示“登记已归档”，日常清理列表移除该行。巡检仍将其识别为已登记的退休副本，旧清理/登记回执在当前授权下继续恢复。列表观察不等于内容验证，原件完整性需独立下载并校验。

| 现象 | 操作 |
| --- | --- |
| SOURCE_METADATA_ARCHIVE_PROTECTED（409） | 刷新资格，核对退休时间、当前代次及清理 outbox；已归档时不要用新键重复提交 |
| SOURCE_CHANGED（409） | 恢复/重新归档改变代次，刷新后重新核对；已提交的原键同参数回放仍返回原回执 |
| IDEMPOTENCY_CONFLICT（409） | 原键绑定的正文已不同；丢失响应须在原确认框重试，不修改原代次或改用新键猜测结果 |
| 400 | 刷新第一页，不跨用户/文档/版本复用游标，limit 为 1–50 |
| 401/403/404 或入口不可用 | 检查当前会话、管理权限、库 ACL、团队及资源身份；失败不应显示缓存历史 |
| 网络失败或 5xx | 在原确认框使用同键同正文重试；保留请求 ID，检查数据库及日志，不直接补写历史表 |

获授权运维可在维护窗口通过 owner 连接执行以下只读查询。overlap_count、pending_archived_cleanup_count 应为 0；异常时保留一致备份和请求 ID 交由研发排查，禁止删除任一表记录或零字节标记：

```sql
SELECT count(*) AS hot_registration_count FROM business.source_objects;
SELECT count(*) AS archived_registration_count FROM business.source_object_archives;
SELECT count(*) AS overlap_count FROM business.source_objects h
 JOIN business.source_object_archives a ON h.id=a.id OR (h.store_id=a.store_id AND h.object_key=a.object_key);
SELECT count(*) AS pending_archived_cleanup_count FROM business.source_object_archives a
 JOIN business.outbox_events e ON e.tenant_id=a.tenant_id AND e.payload->>'object_id'=a.id::text
 WHERE e.topic='knowledge.source_cleanup' AND e.status<>'delivered';
```

回退前在维护窗口停止 Web/API/Agent 并等待请求/worker 退出，保留一致备份，查询 archived_registration_count。为 0 时可按既有章节条件回退，保留新表与迁移记录，不执行 Down；已有历史行时，旧 API 不能识别完整登记，必须保留支持联合读取的版本，或将整套数据库和对象一致恢复到升级前备份。禁止只恢复热表、把归档行复制回去、只切换旧 API/Web、TRUNCATE 新表或移除永久存储标记。停写及恢复方式继续遵守第 11、17–23 节。

自动批量归档、物理对象名回收、供应商历史版本及消息/审计/回执/checkpoint 生命周期尚未实现。独立验收脚本不得在服务器执行；本地证据与复跑见[副本历史设计](../docs/m7-source-history.md)。

## 25. 巡检调查记录

M7.15 新增迁移 2026110602_source_investigations.sql，沿用现有三服务、端口和 IP:port/域名入口。无新环境变量、存储写权限、自动修复或定时任务。内置租户管理员可保存人工说明和跟进状态；“已结束跟进”只代表工作进度，不能作为归属证明或对象删除授权。

1. 按第 11 节备份整个数据库与对象，记录当前 SHA/迁移状态。新表 business.source_investigations 和 api_idempotency、audit_events、users/tenants 一并备份；其中调查说明是业务数据、定位为私有字段，不能只备份对象指纹。按第 10 节原方式发布同一 SHA 的配套 API/Agent/Web，检查迁移任务、readiness 和登录；应升级到迁移 2026110602，不执行 Down。
2. 首次调查来自第 23 节的租户巡检，要求 FILE_STORE_INVENTORY_ENABLED=true、FILE_STORE_TENANT_INVENTORY_ENABLED=true 和已验证的限定前缀 ListBucket。保持已有登记/清理开关；本功能不需要 GetObject/DeleteObject 或更宽桶权限。记录本身的读取/后续修改不访问存储，暂停巡检或存储故障后仍可按指纹跟进既有记录。
3. 管理员登录“知识库”→“租户对象巡检”，在目标项点击“调查记录”。核对对象指纹，填写1–2000字纯文本说明、选择待跟进/调查中/已结束跟进。记录可信备份、历史导入等核对依据与下一步，避免将密钥或客户原文放入说明。核对确认框固定快照，勾选后保存；成功记录修订号、更新人和时间。
4. 后续在同一巡检抽屉输入完整对象指纹并“查询调查记录”，即使当前巡检报错也可查询。没有记录会显示修订0草稿，单独输入指纹不能创建，需回到有效巡检项。每次读取/保存都核对当前会话、管理员身份及已知资源权限；版本身份后来补回时同样应用当前库 ACL。既有租户管理员可访问全租户团队的策略保持。
5. 网络失败或5xx时留在原确认框“重试保存调查记录”，保留原键/正文，不编辑后用新键猜测结果。已提交原请求在30天幂等期内返回原修订，即使后来另有更新；查看最新值需“重新读取”。INVESTIGATION_CHANGED（409）时选择“放弃重试并重新读取”，丢弃旧草稿后人工合并，不手改数据库 revision。关闭页面后也先读取核对服务器结果。

| 现象 | 处理 |
| --- | --- |
| 首次保存400或 INVENTORY_CURSOR_EXPIRED（410） | 关闭确认框/编辑器，刷新租户巡检并重选；不跨用户/租户/存储使用凭证，不把分页游标当选择凭证 |
| TENANT_SOURCE_INVENTORY_DISABLED（409）或 SOURCE_UNAVAILABLE（503） | 仅首次创建依赖有效巡检配置；检查第23节开关/存储配置，已有调查可按指纹继续跟进 |
| INVESTIGATION_CHANGED（409） | 另一请求已保存，重新读取最新修订后人工合并 |
| IDEMPOTENCY_CONFLICT（409）或 IDEMPOTENCY_EXPIRED（410） | 不能修改原键正文；核对服务器现值后再决定新操作 |
| 401/403/404、重新读取失败 | 核对当前账号/会话/租户、内置管理员及库 ACL；不通过查询私有定位绕过授权，失败应隐藏旧说明 |
| 记录已结束但对象仍在 | 正常；调查记录不执行登记、修复或清理，按第18/21–24节独立核对和确认 |

获授权运维可用维护连接执行以下只读汇总，不导出说明或私有定位：

```sql
SELECT status, count(*) AS investigation_count FROM business.source_investigations GROUP BY status;
SELECT count(*) AS investigation_audit_count FROM business.audit_events WHERE operation='knowledge.source_investigate';
```

审计仅包含修订和状态摘要，不保存说明正文；回执保存原提交结果，新修订不会改写旧回执。M7.15交付时只保存最新记录；第27节增加独立编辑历史，仍无自动删除策略。备份恢复使用第11节的一致整库，禁止只恢复该表、删除幂等回执、修改对象指纹/定位以转移调查。说明不能代替原件 SHA256 和可信来源核对。

回退前按原流程停写并备份。若其余已交付功能允许回退，可切换配套旧镜像，保留新表、审计、幂等数据和迁移记录；旧版不展示调查记录，记录不可视为丢失。不得执行 Down 或删表以回退，也不得绕过第17–24节已有约束。该功能不改变存储字节、对象登记或 Python 状态。

本地合成报告与复跑见[调查设计](../docs/m7-source-investigations.md)。验收脚本会创建合成租户并模拟 ACL，不得在服务器业务库运行。未部署真实服务器；跨租户/未知租户目录巡检、自动认领/修复、对象名删除及供应商历史版本仍待后续。

## 26. 巡检调查工作台

M7.16 增加当前租户已保存调查的状态筛选、分页和继续跟进入口。沿用原三服务和 IP:port/域名访问；无新环境变量、端口、存储权限或定时任务。查询不访问 S3，FILE_STORE_INVENTORY_ENABLED / FILE_STORE_TENANT_INVENTORY_ENABLED 关闭时已有记录仍可查询；首次创建仍按第23/25节执行。

1. 按第11节做一致整库/对象备份，保留 source_investigations、身份、审计和幂等回执。记录当前 SHA/迁移状态，再按第10节原配置发布同一 SHA 配套 API/Agent/Web，完成迁移及 readiness/登录验收。应升级到 2026110603_source_investigation_list.sql，它只为既有调查表增加全租户/按状态的 created_at/id 分页索引，不修改说明或状态。索引创建可能等待现有事务，应在维护窗口执行；不要删除迁移记录或手工改业务行。
2. 内置租户管理员打开已有 IP:端口或域名，“知识库”→“调查工作台”。委派 knowledge:manage 不获得入口。无需先选文档或打开巡检，可按全部、待跟进、调查中、已结束跟进筛选，使用上一页/下一页；刷新返回第一页。只展示当前资源允许访问的调查元数据，空页仍可能有后页，不能据此认定调查丢失。
3. 点击“继续跟进”单独读取最新说明，核对修订后按第25节显式确认保存；成功后列表重取。状态仅代表人工工作进度，不构成对象归属、内容完整性或删除授权。原请求响应丢失、同键恢复和修订冲突仍按第25节处理，不在列表批量改状态。
4. 列表按创建时间/ID排序，编辑不会把记录移到第一页。分页不是快照；新记录、状态筛选成员或权限变化后刷新复核。游标15分钟到期且翻页不续期，只能由原用户在原租户/筛选下使用。列表没有说明正文，运维不要通过私有定位绕过页面授权读取说明。

| 现象 | 处理 |
| --- | --- |
| 没有入口或401/403 | 检查当前账号、内置管理员身份及 knowledge:manage；重新登录，核对配套Web/API版本 |
| 某项消失/空页但可继续 | 可能受当前库ACL限制；继续下一页并刷新复核，不能扩大权限或改租户绕过 |
| INVESTIGATION_CURSOR_EXPIRED（410）或游标400 | 点击“刷新调查列表”回首页，不复用其他用户/筛选/接口游标 |
| 存储离线但列表失败 | 查询只依赖Go、数据库及当前授权；排查API、迁移和审计写入，不必开启S3权限 |
| 保存后仍非首页第一项 | 正常，按创建位置排序；检查原位置/状态筛选，刷新后重新读取 |
| 数据库或审计故障 | 本次查询失败，不返回部分页面；排查迁移/数据库可写性，保留原业务数据和审计 |

获授权维护连接可执行以下只读汇总，不导出说明或对象定位。统计是数据库维护视图，不等于某用户页面可见总量：

```sql
SELECT tenant_id,status,count(*) AS investigation_count
FROM business.source_investigations GROUP BY tenant_id,status;
SELECT count(*) AS worklist_read_audit_count FROM business.audit_events
WHERE operation='knowledge.source_investigation_list';
```

备份恢复继续使用第11/25节一致整库；不增加新数据表。回退前停写并备份，若第17–25节的其他功能约束允许，可回退配套旧镜像并保留新增索引、调查表、迁移账本、审计与幂等数据；M7.15版本缺少工作台入口，调查仍可按该版本原入口访问。禁止执行 Down、删索引/表/账本或单独切换旧Web来冒充已验证的回退。

本地113条分页/筛选/ACL及Chrome证据、复跑前提见[工作台设计](../docs/m7-investigation-worklist.md)。验收脚本会写入合成调查及模拟ACL，不得在服务器业务库运行。只完成配置及本地验收，未部署真实服务器；完整编辑历史由第27节补充；自动流转、跨租户发现和自动修复仍待后续。

## 27. 调查完整编辑历史

M7.17 增加迁移2026110604_source_investigation_revisions.sql，沿用原三服务、端口和IP:port/域名入口，无新配置或S3权限。历史记录说明、状态、操作人和时间；查询与后续保存不依赖存储在线。旧版本未保留的内容无法补回，升级只保留旧最新修订为“升级基线”。

1. 按第11节备份整个数据库及对象，记录SHA/迁移状态。在维护窗口暂停外部调查写入，等待在途请求结束，再按第10节原方式发布同一SHA的配套镜像。迁移会以SHARE ROW EXCLUSIVE锁等待旧调查写者，并在同一事务完成新表、旧最新内容回填和捕获/不可变触发器安装；数据量较大时预留复制及约束检查时间，不绕过锁或手改迁移账本。确认迁移到2026110604及readiness/登录后恢复访问。
2. 将 business.source_investigation_revisions 与 source_investigations、身份、api_idempotency 和audit_events纳入同一整库备份，包含约束、捕获及不可变触发器。旧快照复制原操作人/时间，升级时间不是修改发生时间。只备份最新说明或导出幂等回执不足以恢复独立历史；按第11节恢复到新空数据库，不在原业务库截断/覆盖历史或禁用触发器。
3. 管理员从“知识库”→“调查工作台”→“继续跟进”，也可从第25节巡检或指纹查询进入。已保存记录点击“查看编辑历史”，核对修订、状态、说明、操作人和时间；上一页/下一页查看更早内容，“刷新编辑历史”返回最新首页。旧记录显示升级基线，最早修订大于1时会说明缺失范围。
4. 旧历史本身不可改写；第28节增加旧快照确认恢复为新修订。普通修改返回当前编辑器，核对最新修订并显式确认。保存/审计/回执与历史追加同一事务，任一步失败均回滚；原键重试只返回原回执，不追加重复历史。响应丢失和409冲突按第25节处理。即使后来改成“已结束跟进”，旧说明和资源当前授权仍保持独立。
5. 默认每页20/最大50，翻页固定最初修订上界；期间新增修订不插入旧分页，刷新后可见。游标绑定原用户/租户/调查，15分钟到期且翻页不续期。每页仍检查当前内置管理员、knowledge:manage和库ACL；普通账号/委派权限不可查询，失败隐藏旧历史。

| 现象 | 处理 |
| --- | --- |
| 只有一个升级基线或最早修订大于1 | 正常，只保留了升级时已知最新内容；不能从空缺推断或伪造旧说明 |
| INVESTIGATION_HISTORY_CURSOR_EXPIRED（410）或游标400 | 点击“刷新编辑历史”，不复用其他用户/租户/对象或工作台游标 |
| 新修改未出现在当前翻页中 | 当前会话固定旧修订上界，刷新查看最新 |
| 401/403/404 | 核对当前会话、内置管理员、目标租户及库ACL；对象当前不可访问时历史同样拒绝 |
| 保存500或历史写入失败 | 整个保存回滚，核对原键回执/最新修订；排查新表、约束、触发器、空间及权限，不删除历史以恢复写入 |
| 迁移等待锁 | 核对并等待在途调查请求/维护连接，保持维护窗口，不禁用锁或强行把迁移标为完成 |

获授权维护连接可执行以下只读核对；不导出说明或私有定位。missing_latest_history_count应为0，异常时保留一致备份交研发处理，不手工补造快照：

```sql
SELECT count(*) AS revision_count,
 count(*) FILTER(WHERE is_baseline) AS baseline_count
FROM business.source_investigation_revisions;
SELECT count(*) AS missing_latest_history_count FROM (
 SELECT i.tenant_id,i.id FROM business.source_investigations i
 LEFT JOIN business.source_investigation_revisions r
 ON r.tenant_id=i.tenant_id AND r.investigation_id=i.id
 GROUP BY i.tenant_id,i.id,i.revision
 HAVING max(r.revision) IS DISTINCT FROM i.revision
) inconsistent;
```

回退前停写并做一致备份，继续遵守第17–26节已有约束。保留新表、唯一/外键约束、所有历史触发器及迁移账本，不执行Down或运行旧迁移工具改写版本。M7.15/16界面不展示历史，但合法保存仍由保留的数据库触发器追加快照；本地已用同一原保存路径验证。只在明确兼容的整套镜像之间回退，不能删历史表、禁用触发器或用旧备份单独覆盖调查表。

设计及本地合成报告见[调查编辑历史](../docs/m7-investigation-history.md)。验收脚本会写入大量合成修订并模拟ACL，禁止在服务器业务库执行；本节示例未在真实服务器执行。自动历史恢复/合并、历史保留清理和调查自动流转尚未实现。

## 28. 调查历史内容恢复

M7.18 为第27节已记录的旧修订增加人工恢复，沿用当前三服务、端口及IP:port/域名访问，不增加环境变量、S3权限或新迁移。从M7.17升级只需按第10节发布同一SHA的配套镜像；较早版本先按第25–27节执行必要迁移，必须保留2026110604的历史捕获和保护触发器。按第11节先做一致整库备份，检查readiness后再放行访问。

1. 通过当前IP:port或域名登录内置租户管理员，在“知识库→调查工作台→继续跟进→查看编辑历史”选择旧修订的“恢复此修订”。普通账号及仅委派knowledge:manage的账号无权操作；已知资源仍受当前知识库ACL限制。
2. 核对对象指纹、源修订说明/状态、期望当前修订及预期新修订，确认编辑器尚未保存的内容会丢弃。勾选“我确认将以上历史内容保存为新修订”后点击“确认恢复”。当前修订按钮禁用；升级基线可以恢复，基线之前未记录的修订无法恢复。
3. 成功后系统保存新修订，保留原全部历史、来源/目标修订的脱敏审计及不可变回执，并重新读取当前调查和工作台。恢复只替换人工说明/跟进状态，不会归属认领、登记、核验或删除对象，也不改变副本/原件账本。
4. 响应不确定时保留确认窗口，点击“重试恢复历史”使用原请求和原键。之后发生的新编辑不会被旧回执覆盖；恢复回执修订可能小于重新读取的当前修订。放弃重试只会重新读取，不撤销已提交恢复，禁止换键盲重发。

| 现象 | 运维处理 |
| --- | --- |
| 409 INVESTIGATION_CHANGED | 确认期间已有新编辑；重新读取核对，再决定是否确认恢复，不能手改数据库revision |
| 409 IDEMPOTENCY_CONFLICT | 保留原键原目标/正文恢复回执；需要新操作先重新读取并确认 |
| 409 INVESTIGATION_RESTORE_NOT_OLDER或目标404 | 选择当前调查已记录的旧修订；早期未保存内容无法补造，不从过期回执猜测 |
| 401/403/资源404 | 按当前账号、会话、角色/权限和知识库ACL排查；旧回执也必须当前授权，不能借用其他租户令牌 |
| 410 IDEMPOTENCY_EXPIRED | 原键已超过30天恢复期限；先读取当前状态，确需新恢复时重新确认 |
| 数据库/快照/审计/回执失败 | 整体回滚；保留原键，修复服务后重试，不禁用触发器或删除历史规避错误 |

获授权维护连接可只读核对恢复审计数量；不导出说明或私有定位，数量不等于某账号可见总量：

```sql
SELECT tenant_id,count(*) AS restore_audit_count
FROM business.audit_events
WHERE operation='knowledge.source_investigation_restore'
GROUP BY tenant_id;
```

备份仍为当前调查、全部不可变历史、身份、审计及幂等回执的一致整库，包含全部触发器/约束；单独恢复最新行或手工UPDATE旧快照都不等同业务恢复。需要撤销一次恢复时应重新核对并确认另一旧修订，形成下一新修订。按第11节演练灾难恢复到新空库，不能用生产库执行带写入/ACL故障注入的验收脚本。

回退先停写，确认在途请求及回执并备份；已有第29节归档快照时必须遵守该节兼容限制。若其他章节约束允许且没有冷快照，可回退M7.17配套镜像，保留最新说明、所有历史/触发器/审计/回执和迁移账本。旧版本只缺少恢复入口，不会自动撤销已完成恢复；不得删修订、执行Down或单独切旧Web宣称完成回退。复跑和本地证据见[恢复设计](../docs/m7-investigation-restore.md)；本次未上线真实服务器。

## 29. 调查历史分层归档

M7.19仅整理数据库历史，沿用原三服务、IP:port/域名入口和管理员权限，不需要新端口、环境变量或S3权限。完整内容保留在独立归档表，历史查询及恢复自动覆盖两层；不物理删除说明，也不承诺减少数据库总空间。

升级步骤：

1. 按第11节完成一致整库备份并保留当前镜像SHA。数据库备份需包含 source_investigations、source_investigation_revisions、source_investigation_revision_archives（升级后）、身份/权限、audit_events、api_idempotency、迁移账本及全部约束/触发器。早于M7.17的库先具备第27节历史迁移；已应用迁移不要修改或重置。
2. 安排维护窗口，暂缓调查编辑/恢复/归档，等待在途请求。迁移2026110605_investigation_revision_archives.sql按当前调查→热历史顺序持有写锁，新增冷表和精确移表保护。沿第10节使用同一SHA的配套镜像执行一次性migrate，再启动服务；不要并发运行多个迁移或把等待锁的迁移手工标为成功。
3. 核对迁移成功、API readiness及下面只读一致性查询，再放行访问。存在历史读长事务时迁移/归档可能等待；先排查在途维护或请求，不能禁用锁/触发器绕过。
4. 使用当前租户内置管理员经现有IP:port登录，“知识库→调查工作台→继续跟进→查看编辑历史”。已结束调查的历史需原时间满30天并且不在最近3修订内，才显示可用“归档此修订”。没有符合条件的记录属于正常保护，不手改历史时间制造候选。
5. 核对目标说明、状态、原操作者/时间与期望当前修订，勾选“我确认归档此修订并保留完整历史”，点击“确认归档”。成功标记“已归档”，当前说明/状态/修订号不变，完整内容仍可查看及通过“恢复此修订”生成新修订。归档不是删除，不操作S3或证明对象归属。
6. 响应未确认时在原窗口点击“重试归档修订”，使用原路径、正文和键。放弃重试仅重新读取，不撤销已提交归档；较新编辑不会被旧归档回执覆盖。不要换键盲重发。

| 现象 | 运维处理 |
| --- | --- |
| 409 INVESTIGATION_HISTORY_ARCHIVE_PROTECTED | 核对当前调查已结束、目标不是最近3修订、原时间满30天；保留保护无强制绕过入口 |
| 409 INVESTIGATION_CHANGED | 当前调查已更新，重新读取后核对确认；不改数据库revision |
| 409 INVESTIGATION_HISTORY_ALREADY_ARCHIVED | 新键目标已在冷层，刷新历史；若原请求响应丢失，使用原键恢复回执 |
| 409 IDEMPOTENCY_CONFLICT / 410 IDEMPOTENCY_EXPIRED | 原键正文必须相同；过期先读取当前状态，不换键盲重发 |
| 401/403/资源404 | 核对当前会话、内置管理员/knowledge:manage、租户和当前资源ACL；旧回执和冷历史同样检查 |
| 500、延迟约束或审计/回执失败 | 整个归档回滚；检查迁移、触发器、权限、空间，保留原键，不能手工删热行或禁用约束 |

获授权维护连接执行以下只读检查，不导出说明或定位。duplicate_identity_count与missing_latest_count应为0；异常时保留一致备份、暂停归档交研发核查，不手工搬移或补造历史：

```sql
SELECT (SELECT count(*) FROM business.source_investigation_revisions) AS hot_count,
 (SELECT count(*) FROM business.source_investigation_revision_archives) AS archive_count;
WITH history AS (
 SELECT tenant_id,investigation_id,revision FROM business.source_investigation_revisions
 UNION ALL
 SELECT tenant_id,investigation_id,revision FROM business.source_investigation_revision_archives
)
SELECT count(*) AS duplicate_identity_count FROM (
 SELECT tenant_id,investigation_id,revision FROM history
 GROUP BY tenant_id,investigation_id,revision HAVING count(*)<>1
) duplicates;
SELECT count(*) AS missing_latest_count FROM business.source_investigations i
WHERE NOT EXISTS (SELECT 1 FROM business.source_investigation_revisions h
 WHERE h.tenant_id=i.tenant_id AND h.investigation_id=i.id AND h.revision=i.revision);
```

回退前停写、确认在途请求、备份整库和原镜像。已有冷快照时，M7.17/M7.18仅读热历史的旧镜像会遗漏历史和拒绝冷恢复，因此不兼容完整功能；第27/28节旧回退说明受本限制约束。只选择能联合读取两层且理解保护触发器的兼容镜像。若只能恢复升级前版本，需明确接受升级后数据损失，并按第11节将完整升级前备份恢复到新空库后切换，不能混用新旧单表。

禁止执行Down、删除归档表、禁用保护或单独把最新行回灌，不能靠Web回退恢复历史内容。正常撤销内容改动应使用业务历史恢复生成新修订，归档快照自身继续保留。备份演练要核对两层完整性和恢复入口；本次未在服务器执行上述步骤。设计及实际本地报告见[分层归档](../docs/m7-investigation-history-archive.md)，带合成旧修订/ACL故障注入的验收脚本只能运行在一次性环境。

## 30. 调查历史批量归档

M7.20沿用第29节两层历史，当前调查和完整历史保留规则不变。由M7.19升级没有新迁移/配置/端口/S3权限；更早版本先执行2026110605，按第10节发布同一SHA的配套镜像并检查readiness。继续支持现有IP:port与域名入口，按第11/29节先做包含两层历史、身份、审计、幂等回执和所有保护触发器的整库备份。

操作步骤：

1. 当前租户内置管理员登录，“知识库→调查工作台→继续跟进→查看编辑历史”。只有已结束调查中满30天且不在最近3个修订内的热快照可选；已归档/受保护选择框禁用。
2. 选择当前页需要整理的修订，点击“批量归档选中修订”。选择仅限当前页，翻页/刷新会清空；不要为扩大数量修改数据库或绕过保留条件。接口单批最多50项，当前页面最多20项。
3. 等待服务端预览，逐项核对原说明、状态、操作者/时间、对象和当前期望修订；预览不是资格锁定。勾选“我确认将以上选中修订整批归档并保留完整历史”，点击“确认批量归档”。任一项在此期间变化或失权，整批拒绝，不跳过冲突项。
4. 成功提示本批数量，重新读取当前调查和工作台。当前说明/状态/修订号不变，所有目标标记“已归档”，可继续查看及单项恢复为新修订；不会操作S3原件/副本或证明归属。
5. 响应不确定保留窗口，点击“重试批量归档”使用原目标、正文和键。放弃重试只重新读取，不撤销已提交批次；后来的编辑不会被旧批次回执覆盖，禁止拆成新单项请求或换键盲重试。

| 现象 | 处理 |
| --- | --- |
| 预览失败/确认禁用 | 核对当前权限、修订、选择和保留条件；重新读取后重新选择，不把旧预览当授权 |
| 400 INVALID_ARGUMENT | 接口选择须为1–50个严格递增正整数、无重复，并显式确认；原键/正文应有效 |
| 409 INVESTIGATION_CHANGED | 当前调查已被编辑/恢复，整批未执行；重新核对后确认 |
| 409 INVESTIGATION_HISTORY_ALREADY_ARCHIVED或ARCHIVE_PROTECTED | 至少一项已归档或受保护，整批未执行；刷新历史并重新选择 |
| 404 RESOURCE_NOT_FOUND | 目标未记录或当前资源无权限，不返回部分预览/结果，不手工补造修订 |
| 409 IDEMPOTENCY_CONFLICT / 410 IDEMPOTENCY_EXPIRED | 原键只对应原对象/期望修订/选择；过期先重读，不能换键盲重发 |
| 401/403/权限404 | 预览、新批次、原回执均按当前会话/内置管理员/权限/租户及库ACL检查；页面清空说明 |
| 数据库/延迟约束/审计/回执500 | 整批回滚含早期已处理目标；检查迁移/保护/空间，保留原键，不逐项手工修补 |

可继续用第29节只读计数/重复身份/最新快照核对。批量审计operation为 knowledge.source_investigation_archive_batch，一批一条，不等于快照数量；预览审计独立为 knowledge.source_investigation_archive_preview。不要导出说明或定位到普通日志。

回退先暂停新批次、等待在途请求、核对原键回执并备份。M7.19配套镜像可读取全部冷快照/单项恢复，但没有批量接口和旧批次原键重试；回退不会撤销既有批次，未确认结果需恢复兼容镜像后用原键核对，不能另发单项请求代替。已有冷快照仍遵守第29节限制，不回退仅读热表的M7.17/18，不执行Down、删归档/回执或禁用触发器。

无需为本功能停机清空业务表或增加后台调度。详细行为与已执行本地证据见[批量归档设计](../docs/m7-investigation-history-batch.md)。带旧时间修订和ACL模拟的验收脚本只能运行于一次性合成环境；本次未在真实服务器执行部署命令。

## 31. 任务生命周期盘点

M7.21新增只读预览，不增加表、环境变量、公开端口或存储权限。按第10节发布同一SHA的Agent/API/Web并检查readiness；继续使用现有IP:port或域名。旧业务/图数据及备份范围保持不变，不执行数据库Down或清空历史。

1. 使用当前账号登录“任务记录”，选择自己或当前授权的租户任务；原会话正文权限不随run:observe扩大。
2. 打开“任务详情→生命周期预览”，核对两服务独立观察时间、各类记录数量及保护原因；可使用原任务列表的状态筛选和分页定位历史任务。
3. 运行/等待任务、30天内更新、有效租约、未投递/未确认回调、待输入/审批、业务回执/证据/重试关联等均保护。Agent不可用/未建立绑定时不认定可清理；不要通过改历史时间、任务状态或删除回调绕过保护。
4. 规划候选仅表示当前采样无已列保护，仍不是删除授权；本版本无归档/删除按钮或自动清理作业。不能据此手工删除消息、checkpoint、审计、回执或幂等身份。

| 现象 | 处理 |
| --- | --- |
| 409 LIFECYCLE_CHANGED | 任务在观测时变化，刷新重新盘点 |
| 503 LIFECYCLE_UNAVAILABLE | 核对Agent readiness、内网凭据/连通、版本配套、数据库负载/查询超时；不使用旧预览进行清理 |
| AGENT_SCOPE_UNAVAILABLE | 任务尚未建立可信观测绑定或属于旧历史；保持保护，不手工补写授权绑定 |
| 401/403/404 | 当前会话、角色或资源权限失效，页面清空预览和父详情，重新核对权限 |
| 计数与稍后读取不同 | 两服务独立采样，后台恢复或新回调可能继续发生；预览不锁定未来资格 |

可直接回退配套旧应用以移除入口，不需要数据回灌或逆向迁移；仍遵守此前历史归档版本的回退限制。合成验收脚本只能使用一次性mock环境，禁止在服务器业务库执行。[设计及本地证据](../docs/m7-run-lifecycle.md)不代表远程服务器已部署。
