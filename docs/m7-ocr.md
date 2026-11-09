# M7.3 扫描 PDF OCR

本阶段在 PDF 持久索引任务内增加本地简体中文/英文 OCR。原字节、版本激活、物理页码、租户/团队和有效引用仍由 Go 管理；实际验收记录在[实施记录](progress.md)。OCR 文本是可能出错的派生内容，不能作为身份、批准或工具指令。

M7.24进一步支持auto/force与显式双栏或表格阅读方式联合处理，需要同时确认OCR和版面审核；新增parser、受控样本、已知错字及升级回退边界见[联合处理](m7-ocr-layout.md)和[服务器手册§35](../deploy/server.md#35-ocr与版面联合处理)。普通plain OCR的既有parser与行为保持。

## 上传与版本

上传和上传新版本增加 ocr_mode：off（兼容默认，仅文本层）、auto（仅无文字页 OCR）、force（整页 OCR）。非 PDF 只允许 off。模式随不可变版本保存，源文件 SHA256 仍按原字节计算；改变识别方式需要新版本，同一幂等键改变模式必须冲突。浏览器不提交解析器、语言、命令、路径或识别结果。

原文件仍限 2 MiB、最多 50 页；最多对 10 页执行 OCR，整页模式超过 10 页直接拒绝，自动模式按需要识别的页数计限。空白页保留物理页号。auto 不会发现有文本页中的图片文字，页眉加正文扫描图等情况应选 force。没有可用文字时明确失败，不保存部分识别成功的索引。

## 处理与恢复

Docker Agent 固定 Poppler、Tesseract 5.5.0 和 chi_sim/eng 语言包到 system-packages.lock。PDF 子进程不继承服务凭据，原生渲染/识别只操作父进程创建的私有临时目录；无 shell 和用户命令参数。每页灰度渲染长边最多 3200 像素，文本总量沿用 100,000 字符/512 KiB。OCR 总墙钟 45 秒，渲染/识别另有单次超时；Linux 设 CPU、内存和文件尺寸上限、单线程。超时/取消杀死整个进程组，再清理目录；不声称提供操作系统沙箱。

页结果保存 extraction_method（text/ocr/empty），OCR manifest 使用新的固定 parser_version。旧文本 manifest 和回调继续可读、可重放。仅在索引租约仍属于当前 attempts 时提交 manifest/片段；回调丢失重用已保存结果，失败也先持久化固定错误码，不重新识别或覆盖已有终态。45 秒 OCR 小于当前 60 秒索引租约，过期执行器即使算出结果也不能提交。

OCR 结果经 NFC/控制字符清理，仅合并相邻中文字符之间的水平空白，保留换行、数字与英文词间空格；不修正金额或猜测条款。未使用 OCR 内容的自动模式继续输出旧文本解析版本；旧 manifest 不强制新增逐页字段。新解析版本为 pypdf-6.21.4-tesseract-5.5.0-ocr-v1。

固定错误包括 PDF_OCR_UNAVAILABLE、PDF_OCR_FAILED、PDF_OCR_EMPTY、PDF_OCR_TIMEOUT、PDF_OCR_PAGE_LIMIT，沿用加密/损坏/文本资源越限错误。不将原生 stderr、文件正文、路径或凭据回传到页面或日志。

## 预览、激活与引用

版本列表显示识别模式和 OCR 页数；预览按物理页标明文字来自文本层还是 OCR。包含 OCR 页的版本必须由管理员核对后提交 confirm_ocr=true 才能激活，Go 在同一当前授权/版本事务校验并记录审计；不根据识别置信度自动发布。引用的 extraction_method 由 Go 从不可变版本页生成并保存，不接受模型覆写，普通用户打开 OCR 引用时看到识别来源提示。撤回/撤权继续阻止引用读取，OCR 不改变办理审批规则。

## 部署和验证

三个镜像一起升级，Go 追加迁移保存识别模式和引用来源，Python 在既有 manifest JSON 保存逐页来源。原生 OCR 运行于 Linux Agent 镜像，不需要云端密钥或额外公网服务。旧镜像不识别 OCR manifest；回退前停止 OCR 上传、处理待索引任务并撤回 OCR 版本，不执行 Down。语言包/引擎升级必须同步更新原生锁文件、解析版本与固定样本验收。

Linux Agent 基础镜像按 digest 固定，四项直接原生依赖使用精确 Debian 包版本；其传递系统依赖仍由 Debian 软件源解析，并非完整快照构建。若锁定包被源清理，应维护锁文件和引擎标识并重新验收，不静默改装其他版本。Windows 直接运行 Python 只支持文本层；OCR 返回 PDF_OCR_UNAVAILABLE，正式 OCR 使用提供的 Linux 镜像。

使用合成的中文扫描件、英文扫描件和混合文本/图像页测试真实识别、页码、回调恢复、无文字/超限/坏文件、进程组超时清理和激活前审核；通过 Web→Go→Agent 检索 OCR 引用并验证撤回。测试使用独立 mock Compose、独立数据库/临时 schema；不重置原有环境。OCR 质量受分辨率、倾斜、字体、表格和手写影响，本阶段不承诺手写、复杂版面还原或供应商级准确率。

实现依据：[Tesseract 命令](https://tesseract-ocr.github.io/tessdoc/Command-Line-Usage.html)、[识别质量说明](https://tesseract-ocr.github.io/tessdoc/ImproveQuality.html)、[Poppler 渲染选项](https://manpages.debian.org/trixie/poppler-utils/pdftoppm.1.en.html)。

## 复跑

在已启动的独立 mock Compose 中执行以下命令；配置路径替换为该环境实际路径，禁止指向有业务数据的环境。公共 HTTP 脚本创建合成知识库、版本和会话，最后撤回激活的合成版本；不删除业务数据。

```bash
python scripts/check-ocr.py --env-file work/disposable/.env --output work/ocr.json
docker compose run --rm --no-deps --volume "$PWD/flow-desk-agent/tests:/app/tests:ro" --env FLOWDESK_NATIVE_OCR_TESTS=1 agent python -m pytest -q tests/test_ocr.py -p no:cacheprovider
```

Compose 命令须在对应独立配置目录执行，并将测试挂载路径替换为源码绝对路径。Agent 全量测试还需 AGENT_TEST_DATABASE_URL 指向可新建临时 schema 的开发数据库管理员；每项独立 schema 清理后退出。Web 在其目录运行 pnpm test、pnpm build；FLOWDESK_E2E_ENV_FILE / FLOWDESK_E2E_URL 指向同一隔离环境后运行 pnpm test:e2e tests/e2e/ocr.spec.ts。CI 在固定镜像内运行真实 OCR 测试及公共 API 脚本，main 只检查，版本 tag 检查通过后才推送镜像。
