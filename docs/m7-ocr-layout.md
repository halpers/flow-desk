# M7.24 OCR 与 PDF 阅读顺序联合提取

本阶段让已有版本级 `ocr_mode=auto/force` 与 `pdf_layout=columns/table` 联合使用。选择仍由上传者显式指定，同一版本不可修改；不是自动识别任意文档结构。支持水平排版、唯一稳定分栏的规则双栏扫描页，以及按物理行排列的简单表格。中文和英文可混排；表格结果是保留行列间距的文本，不输出语义单元格、合并关系或跨页表格结构。手写、旋转/倾斜排版、任意多栏、复杂合并单元格及跨页关联仍不在本阶段范围内。

`auto` 保留已有文本页的原生版面提取，只对无文字页执行 OCR；`force` 每个物理页都经过 OCR。每页渲染后由固定 Tesseract 5.5.0 的 `chi_sim+eng`、PSM 11 输出 TSV 词框，使用坐标恢复阅读顺序，不相信识别器的 block/line 顺序。表格按词框基线聚合行、保留水平间隔；双栏要求至少三行、且不少于有文字行一半支持同一唯一栏间空白，然后先左后右输出。跨栏全宽行作为分隔。Tesseract 的中文词可能带有包含后续字符的整块坐标；仅对同一已记录源行、横坐标递增、后续中文/标点框内含的情形，按下一个字的左边界截短前框，不修改识别文字。允许不超过四分之一字高的框边界舍入差；跨行、拉丁词、交叉或重复坐标的大幅重叠仍拒绝。无法确定唯一双栏顺序或词框明显重叠时返回 `PDF_LAYOUT_UNSUPPORTED`，不退回普通文本模式。

输入限制沿用 2 MiB PDF、50 个物理页、最多 10 个 OCR 页、100,000 个文本字符和 512 KiB 文本。联合模式沿用 OCR 的 45 秒总墙钟、Linux 768 MiB 地址空间、25 秒 CPU、12 MiB 单文件上限；单页渲染限 8 秒、识别限 12 秒，图片最长边 3200 像素。TSV 另限 2 MiB、100,000 条记录、20,000 个非空词框和 240 个显示列，汉字按两个显示列计算。验证固定表头、有限置信度、坐标/尺寸和页边界；未知、越界及带控制字符的记录返回 `PDF_OCR_FAILED`，数量超限返回 `PDF_LIMIT_EXCEEDED`。这些限制不提高复杂文档识别准确率；管理员仍应对照原件检查关键数字和表格对应关系。

原生 PDF parser 和普通 OCR parser 保留。发生联合 OCR 时，manifest 分别冻结为 `pypdf-6.21.4-tesseract-5.5.0-columns-v1` 或 `pypdf-6.21.4-tesseract-5.5.0-table-v1`；没有实际 OCR 的 auto 版本仍使用原生版面 parser。页码仍为原 PDF 物理页码，每页记录 `text`、`ocr` 或 `empty` 来源。Go 回调同时复核上传选择、parser、完整物理页顺序和 OCR 来源；force 不能混入原生 text 页，联合 parser 必须至少包含一个实际 OCR 页，OCR/空白处理页总数不得超过上限。持久化 manifest 和回调重试沿用现有幂等、代次隔离和崩溃恢复机制，已提交结果不会因重试重新解析。

迁移 [2026110902_ocr_layout.sql](../flow-desk-serv/flowdesk/db/migrations/2026110902_ocr_layout.sql) 放开 PDF 的联合选择；非 PDF 仍不能选择 OCR 或版面模式。没有增加表字段或 HTTP 字段。上传幂等摘要继续包含两个选择，使用同一键更改其中一个返回 `IDEMPOTENCY_CONFLICT`。版本选择、parser 和提取页内容都是不可变证据，降级迁移保留已有联合版本，不丢弃或改写记录。

联合 OCR 版本激活时，服务端同时要求管理员提交 `confirm_ocr=true` 和 `confirm_layout=true`。缺少确认分别返回 `OCR_REVIEW_REQUIRED`、`PDF_LAYOUT_REVIEW_REQUIRED`；操作员不能激活。前端提供组合选择，在同一审核窗口提示检查 OCR 文字、双栏顺序和表格行列，勾选后提交两项确认。两条审核审计和激活在一个事务内落库，后续步骤失败会一起回滚。这只能记录审核人的明确确认，不能证明实际阅读过原件。

引用继续绑定不可变版本、chunk 和物理页，OCR 来源警示在预览与引用展示。Go 按当前权限重新验证引用；文档撤回后，历史消息和引用接口继续撤销依据。原件存储、租户授权和模型工具权限路径没有改变。

固定合成样本位于 [fixtures](../flow-desk-agent/tests/fixtures/README.md)：`ocr-layout-columns.pdf` 为中英双栏扫描件，`ocr-layout-table.pdf` 为中文表格扫描件，`ocr-layout-ambiguous.pdf` 为明确拒绝的三栏扫描件，`ocr-layout-mixed.pdf` 为原生英文页与扫描中文页混合。扫描件来自已有合成 layout 样本的 180 DPI 灰度栅格，PDF 内只有图片、没有文本层。表格样本的“六十个月”与“设备编号与发票”能够识别并保持同行，而若干设备类别名被原生 OCR 错识为拉丁字符，提取器如实保留该识别结果，不自动补字或让模型修正。这是必须人工核对的已知限制。固定词框测试另外验证反序输入不丢词、表格行关联、恶意 TSV 和资源上限。它们是受控样本验收，不是通用 OCR 准确率评测。

在隔离的 mock 环境运行以下验收；配置必须位于仓库 `work/`，不得指向现有业务库。原生 OCR 需要项目固定的 Linux Agent 镜像，并设置 `FLOWDESK_NATIVE_OCR_TESTS=1`；数据库恢复测试使用 `AGENT_TEST_DATABASE_URL` 指定测试库的独立 schema。

```sh
python scripts/check-ocr-layout.py --env-file work/ocr-layout.env --output work/ocr-layout.json
cd flow-desk-agent
python -m pytest -q tests/test_ocr_layout.py tests/test_ocr.py tests/test_pdf_layout.py tests/test_pdf.py
```

公开 API [验收报告](../reports/m7/ocr-layout-acceptance.json)四组通过，覆盖联合上传与幂等、原生/扫描混合页、两项审核和操作员拒绝、物理第 2 页 OCR 引用、撤回撤销和三栏失败终态。Linux Agent本阶段全量101/101、Go专项2个顶层/含子用例6项及vet、Web单测4/4与类型/构建通过；浏览器用例为 [ocr-layout.spec.ts](../flow-desk-web/tests/e2e/ocr-layout.spec.ts)，最终复验状态见[实施记录](progress.md)。部署操作、逐页人工核对和配套回退见[服务器手册§35](../deploy/server.md#35-ocr与版面联合处理)。没有调用付费模型，也没有部署到远程服务器。
