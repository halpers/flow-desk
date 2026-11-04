# M6 实测记录

## M6.1 检索比较

2026-11-03，独立固定集 `m6-retrieval-v1` 共 24 条合成政策及同义改写，SHA256 与逐条排名见[原始结果](retrieval-real.json)。这批问题没有复用 M5 留出集。向量模型为 `qwen3.7-text-embedding-flash`，原生 1024 维；重排使用本地配置的 DeepSeek 聊天模型。

| 模式 | Recall@5 | 首位命中 | MRR@5 | P50 / P95 |
| --- | --- | --- | --- | --- |
| 字符基线 + 全文 RRF | 11/24（45.83%） | 6/24（25%） | 0.319 | 2 / 4 ms |
| 语义 + 全文 RRF | 23/24（95.83%） | 11/24（45.83%） | 0.678 | 306 / 580 ms |
| 语义 + 全文 RRF + 重排 | 23/24（95.83%） | 23/24（95.83%） | 0.958 | 1818 / 2266 ms |

重排仅调整同一组五个候选，因此不改变 Recall@5；其延迟包含原始冷查询，未把缓存命中当作延迟收益。`defect`（两周后出现功能故障）未进入前五；没有为追求满分继续调整本批数据或权重。结果只适用于本次小规模合成语料与网络，不能外推生产准确率或大规模索引性能。

有效比较共 48 次 embedding 请求，input 1,800 tokens；24 次重排请求，input 14,523 / output 4,730 tokens，无供应商错误、无重排回退。费用 unknown。首次独立接口探测另消耗 27 input tokens，不计入比较。更早一轮因评测器 graph_version 错误而无效，保留[失败记录](retrieval-invalid-graph-version.json)；该轮账本已随临时 schema 清理，无法准确恢复用量，不能视为零费用。有效轮的临时 schema 已清理。

## 本地服务验收

[重建记录](local-rollout.json)：两个演示租户分别重建 16、11 个现有版本，稳定 chunk ID，未改变 Go 激活状态。真实 HTTP 问答及撤回证据见[端到端报告](acceptance-semantic.json)。确定性 Agent 测试覆盖缓存/租户/profile 隔离、坏向量/维度、持久预算、旧索引 owner、引用撤权与伪造重排 ID。

## M6.2 MCP

官方 SDK 2.3.0 的内存协议与真实 stdio 子进程 2/2，通过未知工具/额外身份参数/撤权/未批准写拒绝和稳定 action 回执重放；此处 Go 供应商为受控测试服务。完整 Linux Agent 回归 16/16（8.56 秒），Go 真实数据库集成与 vet 通过。

[真实三服务 MCP 验收](acceptance-mcp.json) 4/4：原检查点/审批在重启后继续办理，建单/备注产生实际回执，拒绝与取消无新增效果，外租户访问拒绝。使用确定性模型，不产生本阶段付费模型用量。该报告不声称测试了第三方远程 MCP 服务或 OAuth。

## M6.3 本地部署与监控

2026-11-04，[发布演练](release-local.json) 完成已发布 M6.1 → M6.2 → 回滚 M6.1，每步核对实际 API 镜像并通过一次可信本地证书下的登录/订单任务。使用相同持久数据库，未执行 Down 或删除数据卷。演练后恢复当前源码镜像。

[TLS 办理](acceptance-tls-actions.json) 4/4，真实 Go/MCP 链路覆盖补充输入、审批/原动作建单、备注、拒绝/取消和外租户拒绝；待审批时重启 Agent，恢复到完成 6437ms。任务使用 mock，不能用该报告证明真实模型写入质量。

[监控验收](monitoring-local.json) 通过双服务指标抓取、Grafana 数据源/12 面板查询、Go/Agent 同 trace、Collector 隐私 canary 清理、Agent 停机告警触发及重启后解除。TLS 入口校验证书与 Secure Cookie。Agent 隔离 schema 回归 19/19（9.67 秒），包括 traceparent 重放身份不变、失败不导出私密状态和发布失败后的状态/锁/回滚保护；Go 集成及 vet 通过。

接受的发布/TLS/监控轮次不访问付费模型；但首次发布演练因 Windows→WSL 环境未转发而误用真实聊天配置，3 次请求均 MODEL_UNAVAILABLE，无返回 usage、费用 unknown，零 embedding 尝试。完整保留[无效轮记录](release-invalid-environment.json)，不能把整个 M6.3 的供应商尝试数写为零。现通过显式 mock Compose override、清空密钥及实际容器配置断言修正。用户暂停中断的下一次演练未出最终报告；恢复后重新完成了三个步骤。

这是本机合成数据验收。没有服务器部署、外部通知、持久 Jaeger 后端或生产可用性结论；运行方式和边界见[M6.3 说明](../../docs/m6-deployment-monitoring.md)。
