# M7.1 PDF 与知识文件

本阶段在既有版本、索引任务、管理员激活和有效引用之上支持文本型 PDF。实际验收见[实施记录](progress.md)，本文定义实现契约和限制。

## 上传与存储

原有 Markdown/TXT JSON 上传保持兼容，仍使用 filename/content，限制 256 KiB、20,000 字符。PDF 使用 filename/content_base64，content 必须为空，标准 base64 解码后最多 2 MiB，必须有 `%PDF-` 头。路径、混合正文/二进制载荷、非法编码及越限在 Go 拒绝。HTTP JSON 请求上限为 4 MiB，Nginx 与 Go 保持一致。

Go 在当前租户及知识管理权限下创建不可变 document_version、保存原始 PDF 字节及其 SHA256，并在同一事务写 knowledge.index outbox。浏览器不选择 tenant、解析器或源文件路径。PDF 解析在 Python 持久任务中进行，不占 Go 数据库事务；解析未完成或失败时不能激活。正文和文件保存在现有 PostgreSQL 中，本阶段不接对象存储。

## 受限解析与恢复

解析器固定 pypdf 6.21.4，版本标识 `pypdf-6.21.4-text-v1`。子进程只接收 PDF 字节，不继承数据库、模型或服务密钥；不运行 PDF 的脚本、附件或外部链接。最多 50 页、提取文本 100,000 字符/512 KiB，进程墙钟 8 秒；Linux 额外限制 CPU 和地址空间。该限制是解析进程的资源边界，不声称提供操作系统沙箱。

Python 在自己的 manifest 中持久化分页文本和解析版本，片段不跨页。原物理页码从 1 开始，空白页仍保留页号。完全没有可提取文本的文件返回 PDF_OCR_REQUIRED；混合文件仅索引文本层，管理界面显示有文字的页数，图像文字、复杂表格、图形和公式需人工核对。加密、损坏、页数/文本/资源越限和超时返回固定错误码，不回传解析器原始错误或文件内容。

索引完成回调携带 job_id、原文件 content_hash、page_texts 和 parser_version。Go 验证任务/版本绑定、页号连续、总大小及固定解析版本，并在文档版本中一次保存提取结果。重复完成必须与首次结果相同；后来的回调不能覆盖 ready/failed 或改变激活指针。分页文本是可信内部解析服务产生的派生数据，继续作为不可信业务内容处理，不能提供身份、批准或工具指令。

终止解析错误先保存到持久任务，再重投同一失败回调；不会因进程重启变成新版本或绕过激活。成功 manifest 与片段同事务保存，回调丢失后复用已保存结果，避免重解析造成页码漂移。索引执行仍使用原有 job attempts/lease 围栏。

## 引用与预览

EvidenceCandidate/EvidenceData 新增 page_number：文本文件为 0，PDF 为 1–50。Go 在当前激活版本和当前团队/角色范围内检查对应页中确实包含 excerpt，再登记不可变 run_evidence；拒绝不存在的页、跨页摘录和伪造页码。message.completed 的页码由 Go 从已登记证据生成，模型不能改写。

管理员可通过 GET /documents/:document_id/versions/:version_id/pages 查看提取文本；接口校验租户、版本归属及知识管理权限，不返回原 PDF。普通用户只通过既有引用接口读取已授权片段。引用弹窗显示物理页码；撤回、团队撤权和 ACL 变化继续限制引用、历史消息及 SSE。原始 PDF 不直接交给浏览器执行或内嵌预览。

## 验收

使用合成的多页文本、加密和图像 PDF，覆盖上传幂等、非法编码/混合载荷、权限/跨租户、超限/坏文件/超时、页码来源、终止错误回调重放、manifest 提交后进程丢失、伪造页码、撤回及浏览器流程。确定性验收使用 mock 和基线检索，不调用付费接口；原 Markdown/TXT、语义索引和业务审批回归仍需通过。

复跑入口：根目录 scripts/check-go.ps1、Agent 目录 `python -m pytest -q --tb=short`（AGENT_TEST_DATABASE_URL 指向可建临时 schema 的开发数据库），Web 目录 `pnpm test`、`pnpm build`、`pnpm test:e2e`。浏览器验收必须使用独立的 mock 数据库和 `FLOWDESK_E2E_ENV_FILE`/`FLOWDESK_E2E_URL`，不能将上述命令直接指向业务服务。固定 PDF 位于 flow-desk-agent/tests/fixtures，不需要安装 ReportLab 才能运行测试。

升级需同步三个服务镜像及代理配置，由 Go/Python 分别执行追加迁移；迁移不删除旧数据。PDF 字节在 PostgreSQL 内，数据库备份同时覆盖原文件和分页结果。回退到不支持 PDF 的旧镜像前停止上传、等待索引回调完成并撤回 PDF 激活版本；不执行数据库 Down。旧版本的解析回调、元数据与页码能力不能视为兼容。

解析原理与限制参考 [pypdf 文本提取文档](https://pypdf.readthedocs.io/en/stable/user/extract-text.html)。扫描件 OCR、原文件下载/对象存储、复杂版面还原与索引物理清理继续作为后续扩展。
