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
