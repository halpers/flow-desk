# M3：版本化知识库与兼容模型

Go 管理知识库、角色 ACL、文档版本和激活指针。M3 先支持 UTF-8 `.txt` / `.md`，单文件最多 256 KiB、20,000 字符；浏览器读取文本后使用 JSON 上传，避免引入未约束的二进制解析器。正文只供受服务认证的索引接口读取，普通用户通过授权引用读取片段。

上传在一个业务事务中创建不可变版本和 `knowledge.index` outbox。Python 持久化索引命令，重投复用同一 job/version；事务提交分块后反复回调 Go，Go 只将对应 job 标为 ready。管理员显式激活 ready 版本，必须提交文档 expected_version。索引回调从不改变激活指针，乱序完成不能覆盖新版本。撤回立即清除有效版本并递增文档版本。

知识库固定角色 ACL 在 Go 中维护。检索先取得 run 当前可读的有效版本列表，再在 SQL 排名之前按 tenant/version 过滤。中文字符二元组 + ASCII 词项用于 PostgreSQL simple 全文检索；另用固定 256 维字符 n-gram 特征向量作精确向量召回，以 RRF 融合。`char-ngram-v1` 是可复现的词面向量基线，不具备语义 embedding 能力；真实 embedding 服务尚未接入，不能假定聊天接口提供 embedding。M5 报告基线召回表现和局限。

候选证据在 Go 中再次核对当前 ACL、激活版本并登记 run_evidence；模型只能返回证据 ID。最终 message.completed 只接受本 run 登记且仍有效的证据。历史消息和 SSE 回放遇到已撤回或失权的引用时隐藏正文；引用读取接口重新检查当前访问权限。

真实模型使用 `/chat/completions`，配置 `MODEL_PROVIDER=openai-compatible`、`MODEL_BASE_URL`、`MODEL_NAME`、`MODEL_API_KEY`。DeepSeek 使用 `MODEL_JSON_MODE=json_object`；也支持显式选择 json_schema。JSON 模式仍经 Pydantic 验证，拒绝空输出、截断和伪造引用。输入正文及证据是数据，不接受其中的权限、工具或系统指令。输出只保存业务答复，不保存模型私有推理。

每次 HTTP 尝试先在 Python PostgreSQL 中持久化预算预留，恢复后不归零。每 run 最多 6 次模型尝试、24,000 个保守预算单位，单次输出最多 2,048 tokens，单请求超时 35 秒；UTF-8 输入字节数作为调用前保守估计，有真实 usage 后记录 input/output tokens。网络、429 和 5xx 最多重试两次，结构错误最多修正一次，共享同一预算和 run deadline。成功结构化结果按稳定 call ID 缓存，恢复复用。价格未配置则费用标记 unknown，不推算供应商价格。

协议依据：[OpenAI 结构化输出](https://developers.openai.com/api/docs/guides/structured-outputs?api-mode=chat)、[DeepSeek JSON 模式](https://api-docs.deepseek.com/guides/json_mode/)。真实模型验收与 mock CI 分开运行，实际命令和报告见 progress.md。

复跑：设置本地 `.env` 的 MODEL_PROVIDER 后执行 `scripts/compose.ps1 up --detach --no-deps agent`，再运行 `flow-desk-agent/.venv/Scripts/python.exe scripts/smoke-rag.py --profile mock` 或 `--profile openai-compatible`。脚本复用两个租户各 10 份合成政策，保存独立报告到 reports/m3。每条问答校验答案关键词、标注文档版本引用及外租户拒绝；最后撤回首份政策，验证引用、快照与 SSE 回放受限。真实模式会产生模型调用费用；未配置价格故估算费用 unknown。本组是功能验收，不代表留出集模型质量门槛。
