# M7.22 租户隔离的语义检索规模优化

语义检索默认保持精确查询。管理员可在维护窗口为明确的租户、语义 profile 和固定维度显式创建 HNSW 部分表达式索引，再选择启用近似检索。未建立合格索引、构建中、索引缺失/失效、指纹变化、不支持的维度，均使用精确路径。HTTP、引用和业务审批契约不变，不调用额外模型。

## 配置和索引范围

| 配置 | 默认值 | 范围 |
| --- | --- | --- |
| SEMANTIC_SEARCH_MODE | exact | exact 或 hnsw；仅影响 semantic profile 的向量候选 |
| SEMANTIC_HNSW_EF_SEARCH | 100 | 40–1000 |
| SEMANTIC_HNSW_MAX_SCAN_TUPLES | 20000 | 1000–100000 |

现有 RETRIEVAL_PROFILE=baseline/semantic 和 embedding profile 隔离保持。存储仍接受 1–8192 维；本阶段 HNSW 仅支持 vector 的 1–2000 维，不截断、不降精度、不自动修改模型。高维查询明确走精确回退，CLI 对超范围维度拒绝。embedding endpoint/model/维度/revision 改变后应按原语义重建流程生成新 profile，旧索引不会被静默挪用。

Agent 自有 semantic_ann_indexes 记录租户/profile/维度、固定名称、building/ready 及定义指纹；索引直接建在语义向量表，新增/清理向量由 PostgreSQL 自动维护对应条目。查询不仅检查登记，还核对 pg_catalog 中所属 schema/表、HNSW 方法、单表达式、knowledge.vector_cosine_ops、固定部分条件、构建参数及有效/就绪状态。任意同名索引不会被直接信任或删除。

每个索引仅覆盖一个租户/profile/维度，名称由这三项 SHA256 派生，不接受任意表、SQL、索引名或自由表达式。固定 m=16、ef_construction=64；CLI 不自动遍历所有租户。当前运行角色已经拥有 Agent 表及 DDL；该 CLI 只供受信任运维调用，不是租户用户或模型工具。独立迁移角色拆分不在本阶段。

## 显式维护和恢复

先完成备份及新版本 Agent 迁移，在维护窗口核对目标租户/profile/维度和索引容量。可从 Agent 自有 semantic_manifests 的聚合查询核对已完成 profile；不直接读取 Go 业务表。以下变量仅表示已核对的 ID，不含模型密钥：

```bash
TENANT_UUID="已核对的租户UUID"
SEMANTIC_PROFILE="semantic-v1-已核对的24位十六进制profile摘要"
VECTOR_DIMENSION=1536
docker compose exec -T agent python -m flowdesk_agent.ann_index status --tenant-id "$TENANT_UUID" --profile "$SEMANTIC_PROFILE" --dimension "$VECTOR_DIMENSION"
docker compose exec -T agent python -m flowdesk_agent.ann_index create --tenant-id "$TENANT_UUID" --profile "$SEMANTIC_PROFILE" --dimension "$VECTOR_DIMENSION" --confirm-maintenance
```

CLI 使用独立 autocommit 连接以及租户/profile/维度的会话 advisory lock；并发维护同一索引明确拒绝，不相互覆盖。CREATE/DROP INDEX CONCURRENTLY 不放入业务事务；固定锁等待 5 秒、单语句 300 秒上限。建索引增加 CPU、I/O、内存及磁盘负载，并可能等待旧事务，因此需要维护窗口，不能据此声明无停机能力。确认 profile 实际存在且所有对应 manifest 维度一致才允许创建。

先登记 building，构建成功且定义校验通过后发布 ready。失败/连接中断不会把未完成索引发布为可用；重新执行同一 create 命令会识别已完成索引，或丢弃定义匹配的无效构建后重建。若定义不符则明确停止，交由运维排查，不删其他索引。DROP 先撤销 ready；过程中查询走精确回退。登记表和索引应随同 Agent schema 一致备份。

```bash
docker compose exec -T agent python -m flowdesk_agent.ann_index drop --tenant-id "$TENANT_UUID" --profile "$SEMANTIC_PROFILE" --dimension "$VECTOR_DIMENSION" --confirm-maintenance
```

只有在 status=ready 后才将 SEMANTIC_SEARCH_MODE=hnsw 并按现有发布流程重建 Agent。回退改回 exact 并重建 Agent；不需要删除知识、切换模型、撤回版本或删除索引。运维可随后显式 drop 对应索引，向量、manifest、原件和激活状态不变。供应商不支持所需 HNSW/pgvector 0.8+ 时，保持 exact。

## 查询、授权和有界回退

Go 仍提供可信租户和当前授权版本，Python SQL 同时限定 tenant/profile/dimension/version，条件在候选 LIMIT 20 以内；不会先取全局近邻再由 Python 或模型筛选。部分索引隔离不同租户/profile，查询参数不产生新资源权限。任意跨租户版本 ID 即使进入输入列表，也不能匹配可信 tenant 条件；重排前后及最终引用仍由原 Go 证据接口复核，撤权保护不变。

HNSW 采用 strict_order 迭代扫描，事务局部设置 ef_search、max_scan_tuples、force_custom_plan 和 5 秒语句上限。只把向量距离作为内层索引排序，外层按距离/chunk ID稳定排序后继续原全文 RRF。每次 ANN 若不足20个候选则重走精确查询，不把截断的过滤结果当作知识全集；扫描超时同样尝试一次精确回退。精确路径也有独立5秒上限，超时明确失败而非空结果，设置不泄漏到连接池其他请求。

默认 exact 及所有回退的 ORDER BY 使用距离加零，阻止规划器将精确排序替换成近似索引扫描；不能仅靠环境名称承诺精确性。登记表不存在或其他未知数据库错误属于部署/运行错误，明确失败，不伪装成无结果或吞成精确成功。

ANN 的版本过滤仍由 PostgreSQL 在索引扫描后应用，因此筛选比例会影响候选数及召回。即使返回满20个，近似结果也不保证与精确 top20 相同；不足候选回退不等于完整召回保证。需要严格精确结果的租户保持 exact。实际是否使用索引、召回损失和端到端收益必须以目标环境 EXPLAIN/基准为准；注册 ready 不是查询计划证明。

## 验证及合成基准

功能测试通过与现有 Agent 测试相同的 AGENT_TEST_DATABASE_URL 创建并删除临时 schema，不重置业务库；覆盖注册/中断恢复、互斥维护、错误定义拒绝、租户/profile/维度、隐藏版本干扰、候选不足回退、真实计划、默认精确、锁等待超时及池设置恢复。既有语义检索、撤权、索引清理及审批恢复应在集成时回归。

```bash
pytest -q tests/test_ann_retrieval.py
python scripts/benchmark-retrieval-scale.py --rows 10000 --dimension 64 --queries 40 --ef-search 100 --output work/retrieval-scale.json
```

第一条从 flow-desk-agent 目录运行，第二条从仓库根运行。AGENT_TEST_DATABASE_URL 仅在本地环境配置，不出现在命令输出或报告中。基准使用固定随机种子、合成向量和 UUID、独立 schema，模型调用为零；同时存在未授权同租户版本及外租户干扰。记录精确/近似 top20 集合重合率、p50/p95、实际路径、建索引时间、索引字节数及自然规划的真实 EXPLAIN ANALYZE。

实际执行结果由 [M7.22 基准报告](../reports/m7/retrieval-scale-acceptance.json)记录。本次隔离 Linux PostgreSQL 18.6 (Debian 18.6-1.pgdg12+2)、pgvector 0.8.7，固定种子722、10000行、64维、40个查询，均未调用模型。

| 实测项 | 结果 |
| --- | --- |
| 精确查询 p50 / p95 | 9.664 / 12.06 ms |
| HNSW 路径 p50 / p95 | 4.813 / 5.822 ms |
| 对精确 top20 的平均 / 最低召回 | 97.5% / 85.0% |
| 构建耗时 / 索引大小 | 1417.99 ms / 4292608 bytes |
| 完整候选的近似查询 | 40 / 40，无候选不足回退 |

自然查询计划真实使用该租户/profile的 HNSW 索引，没有通过关闭顺序扫描或排序获得这一基准；完整 EXPLAIN ANALYZE 随报告保存。小规模功能夹具单独在事务内关闭顺序扫描/排序以验证访问方法，不能把该强制计划当作性能结果。索引只覆盖7500个同租户向量，实际允许其中两个版本的5000条，另有2500条外租户干扰；返回的候选均检查当前租户/允许版本。

平均召回不是100%，最低单查询为85%，因此默认仍为 exact。以上向量构造和数据库查询基准不能代替 embedding 质量、真实 RAG 答案准确率、生产吞吐或全部供应商兼容性；本次64维结果也不代表其他维度或数据分布必然提速。HNSW 构建图的并发时序可能使重复运行的近似集合略有变化。

参考：[pgvector HNSW、过滤与多租户说明](https://github.com/pgvector/pgvector#hnsw)。
