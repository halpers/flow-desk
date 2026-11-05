# M7.10 历史版本元数据归档与分页

本阶段将已完成内容清理的版本详情移入独立的 PostgreSQL 元数据归档表；原版本行保留租户、文档、版本、任务、hash、创建时间与清理状态等身份及隔离信息。归档不是删除，也不承诺数据库总容量减少。审计、幂等回执、Python 清理墓碑和对象存储登记继续保留。

## 契约与边界

- `GET /api/v1/documents/:document_id/history`：完整版本历史，默认每页 20 条、最多 50 条，按创建时间和版本 ID 降序，使用绑定租户、用户及文档的游标。返回版本摘要、创建时间、归档时间和是否可归档；不返回原文、任务输入、对象存储地址或密钥。翻页期间新上传版本在刷新第一页后显示。
- `POST /api/v1/documents/:document_id/versions/:version_id/archive-metadata`：要求 Idempotency-Key、confirm=true 和 expected_document_version。仅已 purged、非激活、无引用、无未完成知识任务、无归档存储历史的版本可归档；逐版本确认。归档详情写入、热行精简、审计和幂等结果在同一事务提交，重放复核当前权限并返回原回执。
- 每次读取/重放都检查当前会话、knowledge:manage、租户、知识库角色 ACL 和团队范围。归档详情只通过 Go 授权查询，浏览器不直连数据库或 Agent。
- 热版本列表和清理预览排除已归档元数据；完整历史接口仍可查到这些版本。保留版本身份阻止迟到回调重新写入、重新激活或恢复原文。归档后的已清理版本不可恢复为可用版本。
- 本阶段不归档 source_objects 对象登记、业务回执/审计、消息、模型调用或 Python checkpoint；不做定时归档、不扫描未登记 S3 孤儿，也不删除防重写标记。

## 数据与并发

归档详情包含 filename/parser_version/error_code/chunk_count/ocr_mode/pdf_layout、归档人和时间；历史摘要中的 hash/原大小/格式/索引及清理状态仍由身份行提供。原文和页文本在 M7.7 已清理，本阶段不再次保存其副本。历史列表不会加载原字节、正文或存储地址。

归档使用文档排他锁，按既有文档→权限范围→版本顺序锁定；历史读取持文档共享锁，与归档事务串行，避免看到半份归档。资格查询检查引用、source_epoch/登记/归档任务历史、pending 索引和未送达 index/cleanup outbox；即使清理刚完成，仍需等投递确认为 delivered。再次归档返回 METADATA_ARCHIVE_PROTECTED，改变同键内容返回 IDEMPOTENCY_CONFLICT，文档修订冲突返回 VERSION_CONFLICT。

分页键为不可变 created_at/id，归档不改变顺序，所有终态记录都可逐页读取，超过旧列表 101 条上限的历史不再被截断。游标按当前用户/租户/文档校验范围，每页重新授权；游标是定位信息，不是授权凭证或持久快照。新版本正常使用服务器创建时间，翻页期间新增版本在刷新第一页后显示；手工回填时间不属于这一保证。热索引过滤 NOT metadata_archived，归档表保留在同一 PostgreSQL 数据库，不是外部冷存储或完整历史表分区。

## 验收与复跑

独立数据库测试覆盖跨租户、普通账号、团队/角色 ACL/会话撤权、保护条件、审计失败回滚、同键恢复、迟到回调、111 条历史（其中 107 条创建时间相同）及归档/新上传之间翻页、并发历史读取。独立 Compose 中公开 API [4/4 报告](../reports/m7/history-archive-acceptance.json)覆盖真实索引清理后归档、墓碑保留、完整分页、权限及幂等；Chrome 另实测提交成功但丢失响应后的同键重试。完整回归结果见[实施记录](progress.md)。

临时 mock 栈创建后，使用其 work/ 内配置执行（Windows 使用 Agent 虚拟环境解释器）：

```sh
python scripts/check-history-archive.py --env-file work/disposable/.env --output work/history-archive.json --browser-fixture work/history-browser.json
```

该脚本创建合成文档并仅对本次创建的版本调整日期，另注入 107 条已清理历史来验证分页，不能用于现有业务库。浏览器设置 FLOWDESK_E2E_ENV_FILE、FLOWDESK_E2E_URL、FLOWDESK_HISTORY_FIXTURE 后运行 tests/e2e/history-archive.spec.ts；夹具用过后需重建或重新选择未归档候选。缺少夹具时该项明确跳过。CI 执行公开 API 和数据库测试，不运行 Chrome。服务器升级/操作/回退见[运维手册 §20](../deploy/server.md#20-完整版本历史与元数据归档)。未部署真实服务器，也未调用付费模型。
