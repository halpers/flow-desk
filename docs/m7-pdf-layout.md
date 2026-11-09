# M7.5 PDF 阅读顺序与表格文本

已增加版本级 `pdf_layout`：`plain`（缺省，既有解析）、`columns`（规则双栏，先左后右）、`table`（按物理行保留表格水平对齐）。本节记录M7.5的原生文本层实现；M7.24进一步支持与auto/force OCR联合使用，见[联合处理](m7-ocr-layout.md)及[服务器手册§35](../deploy/server.md#35-ocr与版面联合处理)。每个版本固定同一种方式，不做自动模式猜测。复杂合并单元格、任意多栏、跨页表格语义和手写仍不属于当前范围。

使用已固定 pypdf 6.21.4 的 layout 提取模式生成按物理行排列的等宽文本。table 保留水平空白和行序；columns 只在存在唯一稳定栏间空白、足够成对正文行时重排，保留页眉/页脚与段内全宽分隔行。双栏每页至少三组成对行，且不少于有文字行的一半；栏间至少三个空格。每行最多 240 个字符、每页最多 20,000 次非空文字片段回调。页旋转、倾斜/镜像文字、栏间歧义或超宽返回 PDF_LAYOUT_UNSUPPORTED，超资源返回 PDF_LIMIT_EXCEEDED；不自动降级。沿用 2 MiB/50 页/100,000 字符、8 秒总墙钟、Linux 384 MiB 地址空间和 5 秒 CPU 限制。原理及限制参考 [pypdf 官方文本提取文档](https://pypdf.readthedocs.io/en/stable/user/extract-text.html)。

Go 迁移 `2026110502_pdf_layout.sql` 保存不可变选择；非PDF继续禁止非plain，初期OCR互斥由M7.24的`2026110902_ocr_layout.sql`放开。选择纳入上传幂等摘要，默认 plain 保留新增字段之前的摘要形状；内部 source 传给 Python。原生解析器版本为 pypdf-6.21.4-columns-v1 或 pypdf-6.21.4-table-v1，Go 回调复核选择、parser 与空 OCR 来源一致；联合OCR使用独立parser和完整逐页来源。manifest、页码、索引代次隔离、S3 原件读取和回调恢复沿用现有机制，重试复用已持久化结果。默认旧版本及旧 parser 继续可读，不重建已有索引。

非 plain 版本需管理员预览核对并提交 `confirm_layout=true` 后激活，缺少确认返回 PDF_LAYOUT_REVIEW_REQUIRED（409），确认 OCR 不能代替确认版面。服务端强制确认字段，无法证明人实际阅读过原件；审核与激活同事务记录 knowledge.layout_review。预览保留等宽空白且横向滚动；引用继续绑定不可变物理页，不由模型生成页码或改变提取文本。撤回照常限制引用与历史消息。

四份固定合成 PDF 位于 `flow-desk-agent/tests/fixtures/layout-*.pdf`，已渲染检查全部五个物理页；中文字体作为子集嵌入，Linux 不依赖宿主字体。英文/中文双栏样本的原始提取按行交错，新方式先左后右；表格样本的原始提取按列，新方式把“天琴专业设备 / 六十个月 / 设备编号与发票”保留在同一行。两份正常样本所有非空白字符计数与原始提取相同。三栏与旋转样本明确失败。这是固定规则样本的行为对比，不是通用 PDF 准确率或语义表格恢复评分。

公共 API 的[验收报告](../reports/m7/pdf-layout-acceptance.json)记录样本 SHA256、物理页数和五项验收组：两种选择/幂等/审核/撤回、异常终态、操作员第 2 页引用、S3 字节一致下载。完整结果及服务回归见[实施记录](progress.md)。没有调用付费模型或部署远程服务器。

在独立 mock 环境重跑，先按[本地运行](runtime.md)生成临时配置并启动服务；若验收 S3，先按[存储手册](../deploy/source-storage.md)启用沙箱。以下命令只用于已隔离的合成环境，不用于现有业务数据库；Windows 把 python 替换为 Agent 虚拟环境解释器：

```sh
python scripts/check-pdf-layout.py --env-file /path/to/disposable/.env --require-s3 --output work/pdf-layout.json
cd flow-desk-agent
python -m pytest -q tests/test_pdf_layout.py tests/test_pdf.py
```

未启用 S3 的临时环境省略 `--require-s3`，其报告不会声称完成存储验收。pytest 的数据库恢复用例使用 AGENT_TEST_DATABASE_URL 对应测试库的独立 schema，原生 OCR 项需配套 Linux 镜像与 FLOWDESK_NATIVE_OCR_TESTS=1。CI 在常规回归及 S3 沙箱之后执行同一公开 API 验收；main 仅检查，v* tag 才发布镜像。服务器升级/人工验收/回退条件见[部署手册第 15 节](../deploy/server.md)。
