# M5 评测与展示

M5 使用固定合成任务、可执行业务断言和真实 HTTP 链路。结果入口是[实测报告](../reports/m5/summary.md)，实现阶段证据见[实施记录](progress.md)。

## 固定任务与评分

[tasks-v1.jsonl](../flow-desk-agent/eval/datasets/tasks-v1.jsonl) 包含 100 条任务：事实 20、政策 20、补充输入 15、办理 20、权限/恶意内容 15、恢复 10；每类按固定顺序分层为 60 开发、20 回归、20 留出。生成器不读取随机数、运行 ID 或密钥。JSON Schema、唯一 ID、类别/划分计数、数据集 SHA256 和真实留出任务 ID 均由报告脚本检查。

基线运行开发/回归 80 条；改进使用这 80 条失败项。最后 mock 运行全部 100 条、DeepSeek 单独运行原 20 条留出任务。没有用留出结果调整 Prompt。基线的评分器 KeyError 原始报告保留为 harness-draft，修正契约读取后在原产品版本重跑 14 条；产品失败项继续计入分母。一轮改进评测与浏览器测试同时写入相同订单，计数受到其他合法 run 干扰，该整轮作废并保留 interference 报告及只读核对证据；最终轮独立运行，不能与其他写入测试并行。

断言检查：补充输入与批准前无新工单、动作工具及不可变参数、批准/拒绝/取消终态、真实回执对应对象、精确新增工单数量、备注版本只增加一次、租户外对象 404、操作员管理操作 403、引用标题/有效片段支持问题、SSE 游标回放与刷新快照。读取 Agent 自有 tool_results 验证参数；Go 业务事实一律通过公开 API。外部模型不会自己给自己评分。

工具指标按有 run 的任务计分，包含应当不使用业务工具的政策问题；直接权限 API 样本不进入工具分母。引用以片段为分母，检查是否直接支持指定合成问题。`leaks` 记录禁止标记回显，包含恶意文档文字被直接复述的情况；样本有限，不覆盖任意秘密或任意攻击。没有将 mock 百分比当作真实模型质量。

## 可复跑命令

从项目根目录执行；依赖已存在于 Agent 的锁定环境。命令会向本地合成租户追加会话/工单，不清空已有数据；请在演示实例执行。开始一轮前完成或取消当前活跃任务，避免配额相互影响。

```powershell
python flow-desk-agent/eval/generate_dataset.py
python scripts/model-profile.py mock
./scripts/compose.ps1 up --detach --no-deps --build agent
flow-desk-agent/.venv/Scripts/python.exe scripts/evaluate.py --profile mock --label improved-mock --splits dev,regression,holdout
flow-desk-agent/.venv/Scripts/python.exe scripts/evaluate-retrieval.py
flow-desk-agent/.venv/Scripts/python.exe scripts/export-trace.py --report reports/m5/improved-mock.json

python scripts/model-profile.py openai-compatible
./scripts/compose.ps1 up --detach --no-deps --build agent
flow-desk-agent/.venv/Scripts/python.exe scripts/evaluate.py --profile openai-compatible --label holdout-real --splits holdout
flow-desk-agent/.venv/Scripts/python.exe scripts/export-trace.py --report reports/m5/holdout-real.json
flow-desk-agent/.venv/Scripts/python.exe scripts/report-evaluation.py
```

模型 URL、name 和 key 只保存在本地 `.env`；`model-profile.py` 仅修改 provider。每任务预算沿用 M3，真实留出集最多 20 条，不自动扩展。价格未知标为 unknown。报告包含实际调用与 tokens，不能据此推算未标价费用。

历史基线需要检出 M4 答复/路由实现，保留独立报告；当前版本再次使用 `--label baseline-mock` 只会测当前代码，不能重建旧质量表现。历史 JSON、代码提交和改进解释均保留，不能覆盖后把它当成旧版本。

## 追踪与性能

`execution_spans` 保存 node、dispatch、执行代次、完成/中断/失败状态和耗时。run_id 是稳定 trace_id，dispatch_id 区分恢复代次；公开请求 ID、SSE 工具耗时、节点耗时和模型调用/usage 在导出的 trace JSON 中关联。节点 trace 写入是 best effort，失败只记录脱敏日志，不改变业务效果；进程被强杀前未完成的节点可能没有耗时记录。该实现没有接入 OTel collector，也不保存原始提示词、工具正文或私有推理到 trace。

检索对比直接复用线上排序方法，在 Go 当前授权的 tenant/active version allowlist 上比较词面检索与 RRF；过滤在排序前执行。20 个问题采用相同相关版本标注。当前向量仍是字符词面特征，不能声称语义 embedding 或召回提升。

普通 API 压测创建新数据库，运行同一迁移/账号种子、填入一个租户的 10,000 订单和 10,000 工单，用独立原生 Windows Go API 进程和运行角色，在 PostgreSQL Docker/WSL 上以 20 并发请求四种列表/详情接口，预热 40 次后计量 1,000 次。末尾关闭进程并删除仅由该脚本创建的数据库。该测量不包含模型，不是容器生产容量测算；使用 Windows monotonic 计时，样本存在系统时钟粒度。

```powershell
New-Item -ItemType Directory -Force work | Out-Null
Push-Location flow-desk-serv/flowdesk
go build -buildvcs=false -o ../../work/m5-perf-api.exe .
go build -buildvcs=false -o ../../work/m5-perf-migrate.exe ./cmd/migrate
Pop-Location
flow-desk-agent/.venv/Scripts/python.exe scripts/benchmark-api.py

python scripts/model-profile.py mock
./scripts/compose.ps1 up --detach --no-deps --build agent
flow-desk-agent/.venv/Scripts/python.exe scripts/benchmark-runs.py
flow-desk-agent/.venv/Scripts/python.exe scripts/smoke-actions.py --restart-agent
flow-desk-agent/.venv/Scripts/python.exe scripts/check-backup.py
```

`benchmark-runs.py` 使用两个租户、五个 actor、每人两个任务，全部接受后才取消，测量 202 到非 queued 的 `run.started` SSE。恢复验收记录 Agent 重启到批准任务完成的总耗时，目标小于 60s。恢复备份使用 PostgreSQL 导出的同一快照，将 dump 恢复到新库，校验 13 张关键表内容摘要后清理新库；本地 dump 位于 Git 忽略目录。

## 实现边界

指标是本机、固定合成数据、单次运行结果。mock 的事实句和首引用选择仅供确定性功能验收；真实模型独立受 Pydantic、引用校验、预算和 Go 业务授权约束。数据集还不能覆盖长会话、多语义歧义或复杂政策冲突。未实施 MCP、语义 embedding、PDF/OCR、团队共享权限、生产监控与容量认证。

留出集只按主类别分层，子场景覆盖不均衡，例如没有真实模型备注任务；不能用 20 条结果外推全部办理方式。报告 tokens 针对评分 run，fixture 准备不计入任务评分。每轮按公开 API 绑定当前合成资源的 ID/expected_version，不复用旧批准；UUID、工单编号和创建时间会随重复运行改变，判定使用业务不变量。
