# M7.9 知识库保留策略

每个知识库独立配置最近保留版本数（1–100）和最短保留天数（1–3650），默认 3 个、30 天。规则同时作用于该库的全部文档；最近版本只计算 retained，排序仍按创建时间/id。策略不自动删除文件，清理仍需预览、核对备份并逐版本确认。激活、引用、索引/投递未完成及归档历史保护保持有效，不允许通过策略绕过。

## 协议与一致性

- GET /api/v1/knowledge-bases/:knowledge_base_id/retention-policy：要求当前 knowledge:manage 及该库 ACL/团队权限，返回 knowledge_base_id、keep_latest、minimum_age_days、revision。
- PUT 同一路径：要求 Idempotency-Key，提交 keep_latest、minimum_age_days、expected_revision。知识库排他锁下重验当前权限、参数和修订，原子更新策略、修订及审计；每次成功写入递增修订（包括保存相同值）。同键同请求重放返回原写入结果，不再次改规则；当前值须 GET 获取。撤权后不能重放结果。
- 文档清理预览增加 policy_revision。新客户端清理须提交 expected_policy_revision；策略修订不匹配返回 RETENTION_POLICY_CHANGED（409），刷新并重新确认。兼容旧客户端省略该字段仅限初始修订 1；策略曾被保存后，旧客户端即使规则回到 3/30 也必须升级。
- 预览和清理持有知识库共享锁，策略更新持有排他锁，使清理资格检查与规则更新串行化。清理同时检查文档修订。策略变更前已确认的 cleanup_pending 继续按原命令完成，不能恢复或取消已开始的清理。

策略修改不改变知识库 ACL revision、文档版本、原件定位、检索 manifest 或历史墓碑。迁移给已有库填充默认策略，旧版 INSERT 沿用数据库默认。旧镜像不认识自定义策略，回退前必须在新版将所有策略恢复默认并排空已确认清理；不能只删列或执行 Down。

请求体示例：

```json
{"keep_latest": 5, "minimum_age_days": 90, "expected_revision": 1}
```

## 验收与复跑

Go 全量 72 项顶层测试（含子用例共 90）及 vet 通过，覆盖默认迁移、参数边界、当前授权/回滚、不可变重放、旧摘要兼容和并发策略更新隔离。独立三服务公开 API [4/4 通过](../reports/m7/retention-policy-acceptance.json)，旧清理验收 3/3 通过；Agent 49/49、Web 单测 4/4、Chrome 13 通过/1 个依赖 M7.8 夹具的既有用例跳过。浏览器新用例覆盖保存确认、真实提交后丢失响应、同键恢复和并发修改后旧清理预览拒绝。

只在自己新建的独立 mock Compose 项目中执行以下命令；配置必须位于仓库 work/ 内，不能复制生产凭据。脚本只调整本轮新建版本的创建时间，用于验证年龄保护，不应在真实业务环境执行：

```bash
python scripts/check-retention-policy.py --env-file work/disposable.env --output work/retention-policy.json --browser-fixture work/retention-policy-browser.json
```

需在现有测试配置中指定 FLOWDESK_E2E_ENV_FILE 和 FLOWDESK_E2E_URL（正确的独立 Web 入口），额外设 FLOWDESK_RETENTION_POLICY_FIXTURE 指向该夹具，再运行前端 tests/e2e/retention-policy.spec.ts；未提供夹具时明确跳过。CI 运行公开 API、数据库和 Agent 测试，不运行 Chrome。按库操作、备份和回退见[服务器手册 §19](../deploy/server.md#19-知识库保留策略)，实际交付以[实施记录](progress.md)为准。
