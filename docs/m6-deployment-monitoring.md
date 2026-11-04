# M6.3 部署配置与本地监控

交付范围是可运行的部署配置、固定镜像发布/回滚脚本和本地合成数据验收。没有连接服务器或执行远程上线。基础拓扑见[部署说明](../deploy/README.md)，镜像发布见[CI/CD](../deploy/ci-cd.md)。

## 本地启动

已有根目录 `.env`，并已安装 Agent 锁定依赖后，在仓库根目录执行：

```powershell
flow-desk-agent/.venv/Scripts/python.exe scripts/configure-monitoring.py
./scripts/compose.ps1 --file deploy/compose.monitoring.yaml --file deploy/compose.tls.yaml up --detach --build --wait
```

Linux 使用 `python scripts/configure-monitoring.py`，然后 `docker compose --file docker-compose.yaml --file deploy/compose.monitoring.yaml --file deploy/compose.tls.yaml up --detach --build --wait`。首次先按[运行说明](runtime.md)初始化本地凭据。

配置脚本仅补充随机 `GRAFANA_ADMIN_PASSWORD`，不覆盖现有模型配置；证书和私钥位于 Git 忽略的 `runtime/tls/`，已有完整证书对保留。开发证书有效期 30 天，只适用于 localhost/127.0.0.1。验收脚本显式信任该证书，浏览器需信任本地证书；正式域名须自行提供对应证书，并配置 Origin。

| 入口 | 默认本机地址 | 访问说明 |
| --- | --- | --- |
| 应用 TLS | https://localhost:13443 | `demo.operator`，密码读取 `.env` 的 `DEMO_PASSWORD` |
| Grafana | http://localhost:13001 | `admin`，密码读取 `.env` 的 `GRAFANA_ADMIN_PASSWORD` |
| Prometheus | http://localhost:19090 | Targets、Alerts、PromQL |
| Jaeger | http://localhost:16686 | 选择 flowdesk-agent，按 flowdesk.run_id 查 trace |
| Collector OTLP HTTP | http://localhost:14318/v1/traces | 仅本地验收注入入口 |

所有新增宿主端口只绑定 127.0.0.1。Go 指标在容器内 `api:9101/metrics`，Agent 指标在 `agent:8000/metrics`，均不经浏览器代理发布。TLS override 将应用 Nginx 改为仅监听 443，默认 13000 的 HTTP 映射此时没有监听服务；恢复普通 HTTP 开发环境时去掉 TLS override 并重新创建 web/api。

TLS override 开启 `COOKIE_SECURE=true`、`APP_MODE=pro` 和 HTTPS Origin，Nginx 保留 SSE 无缓冲代理。Go 原生开发未设置 APP_MODE 时仍使用 YAML 的 dev 模式；非 dev 模式拒绝非 Secure Cookie 配置。该 override 是本地模板，正式环境还需调整域名/Origin、移除 API/数据库宿主映射、配置私有网络和入口访问控制。

## 固定版本发布与回滚

`scripts/release.py` 接受完整 40 位 Git SHA，三个服务使用同一 `sha-<commit>` 标签。默认 GHCR；也可选择已发布的阿里云公网命名空间。私有仓库先在本机完成 `docker login`，凭据不作为脚本参数或写入源文件。

```powershell
flow-desk-agent/.venv/Scripts/python.exe scripts/release.py --revision <40位提交SHA> --monitoring --tls --plan
flow-desk-agent/.venv/Scripts/python.exe scripts/release.py --revision <40位提交SHA> --monitoring --tls
flow-desk-agent/.venv/Scripts/python.exe scripts/release.py --rollback
```

阿里云前缀参数为 `--registry crpi-1bbvvu1xvdtg7e0p.cn-hangzhou.personal.cr.aliyuncs.com/flow-desk`。`--pull-policy` 默认 always；离线已缓存镜像可用 never。脚本只操作当前机器的 Compose，不使用 SSH。

顺序为：拉取三个固定镜像 → 基础设施 readiness → Go owner 迁移 → Agent readiness/自身追加迁移 → Go readiness → Web readiness。全链路健康后才原子更新忽略目录 `runtime/releases/state.json` 的 current/previous。独占 release.lock 防止并行发布；执行前写 pending.json。失败保留上次成功状态及 pending，显式 rollback 优先恢复 current；完整成功后的 rollback 恢复 previous。锁在异常退出的 finally 中释放；机器强制关机留下的锁应先确认没有发布进程再人工处理。

迁移失败不会继续启动后续应用或改写成功记录，部分服务更新失败也不会声称发布成功。回滚只更换应用镜像和所记录的 TLS/监控开关，不执行数据库 Down、不删除卷；已有向前兼容迁移与图版本必须支持目标旧镜像。新增 graph/checkpoint 或破坏性 schema 变更仍需要独立迁移方案。Compose 会有短暂重建窗口，当前脚本不提供多副本无停机发布。监控容器若已运行，应用回滚不会主动删除这些旁路容器。

## Trace、指标与告警

Go go-zero HTTP tracing 和新增 agent.dispatch span 注入 W3C traceparent。Python 内部执行接口验证其格式，随持久命令保存，但不参与业务命令 hash；重复命令保留首次 traceparent。worker 重启后仍从持久父上下文创建 agent.execute 和各图节点 span。HTTP 工具和 MCP 子进程将当前 traceparent 传给 Go，业务身份和审批仍由独立服务凭据/capability 决定，traceparent 不提供授权。

仅新增元数据 span，不主动给外部模型请求安装 HTTP/提示词采集。SDK、go-zero 及已有依赖也可能产生子 span；Collector 对全部接收数据统一执行资源/属性白名单、清空 status.message、删除全部 span events。允许字段为服务名/版本、HTTP 路由模板/方法/状态码、run/dispatch/generation、节点名/状态。不导出提示词、正文、检索内容、SQL 内容、密钥、异常消息/堆栈或私有推理。run_id 仅为受本机访问限制的 trace 关联 metadata，不作为 Prometheus 标签。

Agent `/metrics` 聚合自身表，只使用固定 status/direction 标签。命令、回调、节点、索引、模型/embedding 调用和 token 数是保留账本的 gauge，清理后可减少；不能当作进程单调 counter 使用。另提供最近五分钟检索节点和重排 P95，空窗口为 0；前者包含节点内授权、召回等操作，后者仅重排尝试，不等于全部 HTTP 任务耗时。每次数据库语句设 2 秒上限，失败返回 503 和 up=0。

Go 使用 go-zero 的 HTTP 请求 counter/histogram；工具错误图表按受控工具路由模板和 HTTP 状态分组，不记录工具参数。Grafana 自动提供 12 个面板，覆盖双服务存活、队列/活跃命令、模型及 embedding tokens/失败、回调、索引状态、检索/重排延迟、节点失败和工具 HTTP 错误。

Trace 使用 Jaeger 自带 UI 与稳定 v3 OTLP JSON 查询接口。当前 Grafana Jaeger HTTP 插件仍调用旧查询接口，实测与 Jaeger 2.22 返回 404，因此未提供一个不可用的数据源；Grafana 仅预配已验证的 Prometheus 数据源，trace 入口在面板中说明。

Prometheus 每 5 秒抓取/评估，保留 7 天，使用独立数据卷。四项规则为服务失联持续 15 秒、命令排队超过 10 持续 1 分钟、永久失败回调持续 1 分钟、最近五分钟保留模型失败数增量超过 2 持续 30 秒。失败增量是账本 gauge 的差值，不保证清理期间的精确错误率。规则状态可在 Prometheus Alerts 查看；本阶段没有配置外部通知或发送消息。

固定监控镜像为 Collector contrib 0.164.0、Prometheus 3.13.5、Grafana 13.2.5、Jaeger 2.22.0；Python OTel SDK/OTLP HTTP exporter 1.45.2。Jaeger 使用默认临时内存存储，重启即丢 trace；Collector 队列也不是耐久日志。正式长期监控尚需持久 trace 后端、容量/保留策略、认证/TLS 与告警接收端。

## 验收与复跑

隔离运行付费供应商的本地监控验收：

```powershell
./scripts/compose.ps1 --file deploy/compose.monitoring.yaml --file deploy/compose.tls.yaml --file deploy/compose.mcp-acceptance.yaml up --detach --build --wait
flow-desk-agent/.venv/Scripts/python.exe scripts/check-monitoring.py
flow-desk-agent/.venv/Scripts/python.exe scripts/smoke-actions.py --tls --restart-agent --transport mcp --profile mock --output reports/m6/acceptance-tls-actions.json
```

此 override 使用 mock 聊天、baseline 检索、真实 MCP/Go 业务链路，未访问外部模型。验收包含可信证书校验、Secure Cookie、任务成功、两项 scrape、Grafana 数据源/全部面板查询、Go/Agent 同 trace、Collector 注入隐私 canary 后不入库、短暂停止 Agent 触发告警并重启解除；脚本在 finally 中恢复 Agent。报告见[本地监控](../reports/m6/monitoring-local.json)。

[TLS 办理报告](../reports/m6/acceptance-tls-actions.json) 另覆盖补充输入、审批中重启恢复、原 action 真实建单、备注、拒绝/取消及租户边界。以上两个验收会重启同一 Agent，按顺序运行。

固定版本验收脚本 `scripts/check-release.py --baseline <旧40位SHA> --candidate <新40位SHA>` 在当前机器依次运行已发布旧版、升级、回滚，各通过一次 TLS 登录和任务。它使用发布脚本的 `--synthetic` 显式添加 Compose mock override，并在发起任务前检查实际容器为 mock、聊天及 embedding 密钥为空；该开关保存在发布记录中，回滚沿用。仅用于已有本地合成数据库，执行前先完成当前任务。报告见[本地发布](../reports/m6/release-local.json)。发布验收后，使用上方本地监控 Compose 命令恢复当前源码镜像；要恢复 `.env` 的真实模型配置则去掉 mcp-acceptance override。

首次演练依赖 Windows 子进程环境变量，但 WSL 未将其传给 Docker，误用了真实聊天配置；3 次请求均为 MODEL_UNAVAILABLE，没有返回 usage，费用 unknown。保留[无效演练记录](../reports/m6/release-invalid-environment.json)，不计入通过结果，也不将其宣称为零付费用量。修正后使用上述显式 override 和容器断言。

CI 保留隔离数据库、应用集成和 mock HTTP smoke，追加 Compose/TLS 解析、promtool 规则检查及 Collector validate，不访问付费模型。Collector 静态 validate 不能代替真实数据检查，本地 canary 验收发现过 OTTL 事件赋值类型错误，现改为显式 filter processor 删除事件。

实现依据：[Collector 配置](https://opentelemetry.io/docs/collector/configuration/)、[OTTL filter](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/v0.164.0/processor/filterprocessor/README.md)、[Jaeger 2.22 API](https://www.jaegertracing.io/docs/2.22/architecture/apis/)、[Prometheus 告警规则](https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/)、[Grafana provisioning](https://grafana.com/docs/grafana/latest/administration/provisioning/)。
