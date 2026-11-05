# M7.7 知识版本保留与清理

管理员先查看文档清理预览，再逐个确认清理。固定保留最近 3 个未清理版本，创建满 30 天才可进入候选，没有定时自动删除。当前激活版本、任何 run_evidence 引用、索引未结束或投递未完成的版本均受保护。发起过 S3 归档（包括失败、恢复到数据库的版本）也受保护，本阶段不删除对象存储文件，不承诺回收 S3 空间。

Go 拥有保留规则、权限、版本元数据、审计和清理 outbox。Python 只清理自己拥有的检索索引。保留文档、版本身份/hash、历史消息、引用、审批、回执和审计。PostgreSQL 清除原文字节和提取页，实际磁盘收缩由常规 VACUUM/存储维护负责。

## 协议与状态

- `GET /api/v1/documents/:document_id/retention` 返回固定规则、文档修订号、版本状态、大小及全部保护原因，不含正文或对象定位。
- `POST /api/v1/documents/:document_id/versions/:version_id/cleanup` 要求 Idempotency-Key、confirm=true 和预览中的 expected_document_version；在事务内重验租户、角色、团队、会话及保护条件。retained 变为 cleanup_pending，同时写入审计及 knowledge.cleanup outbox。
- 内部 `POST /internal/v1/knowledge/cleanup` 只接受服务认证，命令绑定 tenant_id、version_id、job_id 和 content_hash。Python 同事务登记永久墓碑、终止索引 job、删除语义向量/manifest、词法片段及原始 manifest，提交后才返回 202。
- Go 收到确认并仍持有投递租约后清除数据库内容，置为 purged。响应丢失和重启重放复用同一墓碑，失去租约的投递不能提交完成。失败保留数据库原文，但从 cleanup_pending 起禁止激活、读取及索引回调。
- Python 索引接受、提交及独立语义重建与清理共享按版本的事务锁并检查墓碑，阻止在途解析/embedding 或迟到重放复活索引。没有公开撤销墓碑接口。

预览有时效，执行时重新校验。清理不可撤销，内容恢复需要备份恢复流程。备份副本及外部对象存储遵循各自保留规则。

cleanup outbox 失败按指数退避自动重试，间隔上限 30 秒，区别于普通投递的死信机制；清理已确认后不因八次失败停止重试。不提供取消或绕过保护的接口。版本元数据保留 source_size（原文件大小），它不表示当前仍占用同等数据库空间。共享 embedding 缓存、用量台账、历史消息、模型记录和检查点不按版本清除，本功能不是全面数据擦除。

## 边界

最近版本按 created_at、id 排序，只计算 retained。版本列表优先显示未清理版本，再显示最近墓碑，最多 101 条；100 个未清理/待清理版本的配额在 purged 后释放。元数据和墓碑持续增长，历史元数据归档、可配置策略及 S3 垃圾回收不包含在本阶段。

## 验收

完成后的实际结果记录于 [progress.md](progress.md)，此协议不替代交付证据。

本地验收脚本 `scripts/check-retention.py` 强制显式传入仓库 work/ 下的 mock 配置，只对脚本本轮新建的合成版本调整创建日期。禁止把生产连接信息复制进该目录运行。示例（使用自己新建的独立测试 Compose 配置）：

```bash
python scripts/check-retention.py --env-file work/disposable.env --output work/retention.json --browser-fixture work/retention-browser.json
```

浏览器保护规则测试可直接运行；实际清理用例额外要求 FLOWDESK_RETENTION_FIXTURE 指向该 browser-fixture 文件，未配置则明确跳过。CI 通过独立配置运行公开 API 清理及 Python 并发/事务测试；它不运行 Chrome。
