# M7.14 完整副本历史与退休登记归档

为 source_objects 提供不限旧预览 101 条的完整历史分页，并将已完成清理的退休登记移入独立 PostgreSQL 表。对象身份、定位、hash、大小、代次、时间保留；S3 零字节标记继续阻止迟到上传。本功能是热/历史登记分离，不承诺数据库总容量下降，不删除对象名。

## 数据与事务

- 迁移 2026110601 增加 source_object_archives 和未完成副本清理的对象索引。原登记字段完整保留，增加 archived_by/archived_at。每次只归档一个已 retired 且清理 outbox 已 delivered 的旧代次记录。
- 同一事务中按文档→知识库授权→版本→对象锁定，复核 expected_source_epoch 和资格；历史表插入、热行删除、审计和幂等回执一起提交。审计或回执失败时整体回滚。
- 标准定位由可信版本和代次构建，所有归档/人工登记写者先锁版本；同一定位的查重与移动串行。SourceObjects 的身份/定位查询和存储巡检兼容读取热表及历史表；归档记录不可重新登记或清理。既有清理/登记回执在当前授权下继续重放。
- 历史按不变 created_at/id 做有界 keyset 分页，默认 20/最大 50；归档不改变排序，期间新登记需刷新第一页。游标绑定用户/租户/文档/版本，每页重查授权。日常副本清理预览只查热表，历史表通过独立入口查询。
- 不调用 S3，不验证外部文件仍存在，不自动归档、不归档消息/审计/回执/checkpoint，也不处理供应商历史版本。

## 接口与页面

GET /api/v1/documents/:document_id/versions/:version_id/archive-copies/history：cursor/limit，默认 20、最大 50。响应包含当前 source_epoch、items、has_more、next_cursor。每项包含 id、source_epoch、source_size、status、created_at、retired_at、metadata_archived、archived_at、can_archive、archive_block_reason；不暴露桶/key/hash。

POST /api/v1/documents/:document_id/versions/:version_id/archive-copies/:object_id/archive-metadata：Idempotency-Key、confirm=true、expected_source_epoch。200 回执为 object_id/version_id/archived_at；同键同参数恢复不可变回执，即使当前代次后来变化；相同键变参为 IDEMPOTENCY_CONFLICT，新键对已归档行返回 SOURCE_METADATA_ARCHIVE_PROTECTED。两条接口均复核当前 knowledge:manage、租户、库 ACL/团队和会话。

入口：“知识库”→PDF 版本→“归档副本清理”→“完整副本历史”；已清理或元数据归档的 PDF 可从“完整版本历史”里的对应项进入。候选须勾选核对范围后确认；失败保留原键和正文，重试原弹窗。归档不改变知识原件或索引。

| 保护原因 | 处理 |
| --- | --- |
| not_retired | 先完成独立副本清理流程 |
| retirement_incomplete | 等待清理完成并检查任务；不手改时间 |
| current_generation | 当前/未来代次或当前原件定位受保护，刷新核对 |
| cleanup_delivery_pending | 清理投递尚未 delivered，检查 pending/delivering/dead_letter |
| already_archived | 历史保留；丢失响应应使用原请求重放 |

## 本地验收与复跑

[公共接口报告](../reports/m7/source-history-acceptance.json)通过 4/4：两轮真实 S3 归档/恢复/条件退休之后移动第一条登记；逐字段比较完整身份，审计仅一次，旧清理回执重放，S3 列表仍为两个已登记的零字节标记，数据库原件 SHA256 不变。109 条历史分 6 页无重复/遗漏，日常预览仍为 101 条；107 条为分页专用的合成未来代次记录，不代表真实存储文件或可归档副本。普通 operator 的读取和重放被拒绝。

Go 测试覆盖审计/回执后置失败回滚、当前代次、缺清理时间、未完成与死信投递、同键变参、旧 cleanup/adoption 回执和登记防重、租户/团队/ACL/撤销会话、跨用户游标、相同时间戳分页、期间移动/新增，以及历史查询等待归档事务提交。Chrome 用例验证 6 页前后/刷新、显式确认、丢失已提交响应后同键同正文恢复，以及 403 时隐藏缓存项；具体全量结果见[实施记录](progress.md)。

以下只在独立 mock/S3 开发栈执行，需安装项目固定依赖。脚本会创建新 PDF、清理两份旧副本并构造分页夹具，禁止在服务器业务库或生产桶运行：

```bash
python scripts/configure-source-storage.py --enable-cleanup --enable-inventory
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml up --detach --wait --wait-timeout 180
mkdir -p work
install -m 600 .env work/source-history.env
python scripts/check-source-history.py --env-file work/source-history.env --output work/source-history.json --browser-fixture work/source-history-fixture.json
```

Chrome 用例读取 FLOWDESK_SOURCE_HISTORY_FIXTURE 指定的夹具，以及现有 FLOWDESK_E2E_ENV_FILE / FLOWDESK_E2E_URL。确认测试会归档其候选，重跑前用新版本生成新夹具。CI 日常检查加入同一公共接口验收，main/PR 检查及 v* tag 才发布镜像的规则保持。

部署操作、只读核对及回退限制见[服务器手册 §24](../deploy/server.md#24-完整副本历史与退休登记归档)。当前只完成本地验收，未部署服务器。数据库总历史量仍增长；对象名回收需先解决迟到写入隔离，消息/回执/审计/checkpoint 归档、供应商历史版本仍待后续。
