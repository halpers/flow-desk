# M7.8 归档副本清理

原文件恢复到 PostgreSQL 后，可预览并人工确认清理已登记的旧 S3 副本。默认关闭，通过 FILE_STORE_CLEANUP_ENABLED=true 显式开启。当前使用的对象、未完成恢复的版本、配置不匹配的存储、未登记对象以及启用/暂停版本控制的桶不可清理。保留数据库原件、版本元数据、引用、审批、回执及审计。

## 对象与防重写

新归档使用包含 source_epoch 的独立键，在上传前把对象身份、租户、版本、存储指纹、hash、大小和代次登记到 Go 的 source_objects。迁移登记当前仍为 s3 的旧定位；迁移前已经恢复到数据库且丢失定位的历史孤儿不自动认领，不扫描任意桶路径。

只有对象代次小于当前 source_epoch，且用户确认时 PostgreSQL 原件长度/hash 正确，才可登记清理任务。新归档永远使用当前代次的新键。清理将校验旧对象内容，使用 ETag 条件写入零字节墓碑，并保留对象身份元数据；对象不存在时也条件创建墓碑。迟到的 PutIfAbsent 不能重新上传旧 PDF。此操作回收当前对象的文件字节，仍保留键/墓碑开销，不删除对象名称，不保证供应商立即回收物理磁盘或清除备份。

依据：[S3 条件写](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html)、[桶版本控制查询](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketVersioning.html)。桶版本控制查询失败或返回 Enabled/Suspended 时拒绝清理，不把覆盖写当成历史对象版本清除。条件写兼容性需按存储供应商验证。

## API 与事务

- GET /api/v1/documents/:document_id/versions/:version_id/archive-copies：返回当前 source_epoch、开关及登记对象的状态、大小和保护原因，不暴露桶/key/密钥。
- POST /api/v1/documents/:document_id/versions/:version_id/archive-copies/:object_id/cleanup：要求 Idempotency-Key、confirm=true、expected_source_epoch，重新校验当前知识管理权限、ACL、团队、会话、版本绑定和数据库原件；原子写入 cleanup_pending、审计及 knowledge.source_cleanup outbox。
- Worker 在事务外调用存储，提交结果前复核对象/版本绑定、当前定位和有效投递租约。失败持续退避重试，间隔上限 30 秒；响应丢失重放识别原墓碑，不重复清除其他对象。没有取消墓碑或任意路径删除接口。

M7.7 的“存在归档历史”版本保护保持有效。本阶段清理冗余归档字节，不删除知识版本的数据库原件；可配置版本保留策略及历史元数据归档仍待后续。

每版本最多保留 100 个未退休登记副本，达到上限时归档任务保留数据库原件并报 SOURCE_COPY_LIMIT；先清理可清理的旧副本再重新投递归档。预览优先返回未退休副本，最多 101 项；完整历史登记仍保存在数据库。开关关闭后已确认任务保持待重试，重新开启后继续，不提供取消接口。

请求体示例（路径必须绑定当前版本的登记对象，请求头另带 Idempotency-Key）：

```json
{"confirm": true, "expected_source_epoch": 1}
```

## 验收与运维

Go 67 项顶层测试（含子用例共 85）及 vet 通过。协议模拟覆盖对象已存在/缺失、响应丢失、ETag 冲突、内容损坏、错误标记、版本控制 Enabled/Suspended 和查询拒绝；实际 SeaweedFS 4.52 另行验证错误 If-Match 被拒绝、条件替换及迟到上传隔离。公开 API [4/4 报告](../reports/m7/source-cleanup-acceptance.json)与 Chrome 新用例验证真实链路。Agent 49/49、Web 单测 4/4、Chrome 12 通过/1 个既有老版本夹具用例跳过；不代表远程 S3 或生产容量验收。

沙箱复跑、显式开关、桶级权限及一致备份见[存储运维](../deploy/source-storage.md)；人工步骤和回退条件见[服务器手册 §18](../deploy/server.md#18-归档副本清理)。禁止在生产执行合成探针或制造过期/旧代次候选。
