# 原文件存储部署与运维

默认 `FILE_STORE_PROVIDER=postgres`，不需要额外服务。M7.4 可选将 PDF 异步归档到私有 S3 兼容存储，TXT/Markdown 仍保存在 PostgreSQL。已有 PDF 不自动搬迁；管理员可在知识库版本中“归档原文件”。下载只经过 Go，浏览器及 Python 无需存储账号；服务器 IP:端口入口沿用[服务器手册](server.md)。

## 使用外部私有 S3

先部署同一 M7.4 或后续兼容版本的三个服务镜像，迁移由 migrate 自动执行。准备已存在的私有桶、HTTPS endpoint（无 bucket/路径/query）、区域及专用访问凭据。适配器要求 SigV4、path-style、GetObject 和带 `If-None-Match: *` 的 PutObject；首次接供应商时先用独立测试桶验收。当前已测 SeaweedFS 4.52，不据此声明所有供应商兼容。

在服务器 `.env` 填写以下项。密钥由运维秘密管理渠道提供，示例省略值；文件保持 `chmod 600 .env`，不要提交 Git：

```dotenv
FILE_STORE_PROVIDER=s3
FILE_STORE_ENDPOINT=https://s3.example.invalid
FILE_STORE_REGION=us-east-1
FILE_STORE_BUCKET=flowdesk-sources
FILE_STORE_ACCESS_KEY=
FILE_STORE_SECRET_KEY=
FILE_STORE_ALLOW_HTTP=false
```

Go 账号仅需目标桶 `knowledge/` 前缀的对象读取/写入，应用没有 DeleteObject、建桶或列桶操作。关闭公共读取，限制网络来源，使用验证通过的 TLS 证书；自签根证书需按服务器信任链部署，应用没有跳过 TLS 校验选项。桶启用版本保护及独立备份；生命周期规则不得自动过期仍被引用的原件。

按原服务器发布方式重新创建 API（包括所需 server/TLS/monitoring override），配置只注入 Go API。常规发布脚本从根 Compose 读取这些变量，外部存储无需增加 `--storage` 参数，也无需运行本地沙箱 overlay。readiness 检查不依赖外部 S3；就绪后还须单独验收原件读取。

管理员上传一个合成 PDF，等待索引完成，刷新版本，确认按钮变为“恢复至数据库”；点击“下载原文件”，离线计算 SHA256 与上传样本比较。再执行一次恢复、下载、归档、下载。普通操作员不得下载原件。新 PDF 先以数据库原件接收，归档失败会保留数据库字节；不可仅以 HTTP 202 判断对象已经归档。

## 本地独立 S3 沙箱

仅在已初始化的独立 mock 开发环境执行，不能以此覆盖真实 `.env`。辅助脚本只补充空的沙箱账号、密钥和桶，不改已有值，也不打印它们：

```bash
python scripts/configure-source-storage.py
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml config --quiet
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml up --detach --build --wait --wait-timeout 180
python scripts/check-source-storage.py --output work/source-storage.json
```

脚本依赖开发环境的 httpx；Linux 使用[Agent 锁文件](../flow-desk-agent/requirements.lock)准备依赖，Windows 可使用 `flow-desk-agent/.venv/Scripts/python.exe`。验收脚本拒绝非 mock 配置，创建合成知识库/草稿，不调用付费模型，也不删除业务数据。临时数据仅由独立项目清理。

overlay 使用 SeaweedFS 4.52 固定 digest，`source_data` 卷同时保存对象内容及 filer 元数据，所有存储端口只在容器网络内开放，未提供凭据时启动失败。`FILE_STORE_ALLOW_HTTP=true` 只用于此内部沙箱连接，浏览器入口仍可使用服务器 HTTPS。单进程沙箱用于开发和本地验收，高可用及生产容量需单独规划。不要对有文件的项目执行 `down --volumes`。

## 故障处理与回退

| 现象 | 运维操作 |
| --- | --- |
| 新文件保持数据库原件 | 检查配置、网络、桶权限及条件写支持；索引/下载仍可使用数据库原件。修复后自动重试，8 次失败为死信，再点“归档原文件”重新投递 |
| SOURCE_UNAVAILABLE（503） | 核对 endpoint/region/bucket 与首次归档配置、凭据和对象备份；不要通过改数据库 key 绕过校验 |
| SOURCE_INTEGRITY_FAILED（503） | 从受控备份恢复同键的正确原件，按版本 content_hash 与 source_size 验证，不能覆盖为任意新内容 |
| SOURCE_ARCHIVE_DISABLED（409） | 当前只暂停/未启用归档；下载和恢复已归档文件仍需要完整的原存储配置 |
| SOURCE_CHANGED（409） | 读取期间发生恢复/再归档，刷新版本后使用新的请求键重试 |

暂停新归档时把 provider 改为 postgres，但保留 endpoint/region/bucket/凭据，按原发布配置重建 API。下载及“恢复至数据库”仍可访问原存储；待处理归档任务会重试直至死信，重新开启后可手动投递。不要同时修改桶或 endpoint，定位指纹会拒绝访问不同存储；更换访问密钥不改变指纹。

回退 M7.3 或更早镜像前，在新版 API 上暂停归档，逐个将全部已归档版本（包括未激活和已撤回文档）恢复至数据库。管理员界面刷新确认，运维通过 owner 的只读查询检查：

```sql
SELECT source_storage, count(*) FROM business.document_versions GROUP BY source_storage;
SELECT status, count(*) FROM business.outbox_events WHERE topic='knowledge.archive' GROUP BY status;
```

只有不存在 s3 行，且没有 pending/delivering 归档任务时才回退；未清空任务应保持新版运行，等旧 epoch 任务被确认或进入死信。处理全部待索引/业务任务及既有 OCR 回退条件，备份后按[服务器回滚步骤](server.md)执行。原对象保留，不执行 Down、不删除定位列。迁移支持旧版 INSERT 自动补齐 size，不能因此跳过恢复已归档文件。丢失原对象且无备份时无法从提取文本还原原始 PDF。

## 一致性备份与恢复

启用 S3 后，PostgreSQL dump 仅含原件定位，已归档字节不在 dump 中。每套备份需保存数据库、对象/对象版本、endpoint/region/bucket、存储凭据恢复渠道、源码/镜像 SHA 和备份时间。包含所有当前引用键；备份及凭据须加密存到独立受控位置。应用无自动删除，不代表存储侧不会丢数据。

外部托管 S3：安排停写窗口，停止 Web/API/Agent，等容器完全停止后执行[数据库备份](server.md)及供应商原生桶快照/版本备份，再恢复同一发布配置。数据库与对象备份必须同属这个窗口，不能只备份新上传部分。恢复到新环境时先恢复完整对象及数据库，保持相同 endpoint/region/bucket（或先在原配置下恢复至数据库后迁移），核对原件 hash、审计/回执/检查点，再启动 worker；禁止让旧、新两个环境同时接收业务写入。

本地沙箱使用停机冷备，以下在仓库根目录的 Linux shell 执行。`FLOWDESK_BACKUP` 必须为本轮新建备份目录，沿用服务器手册的路径设置；数据库备份在服务停写期间完成：

```bash
umask 077
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml stop web api agent objectstore
docker compose exec -T postgres sh -ec 'PGPASSWORD="$POSTGRES_PASSWORD" pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$FLOWDESK_BACKUP/flowdesk.dump"
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml run --rm --no-deps --entrypoint tar objectstore -czf - -C /data . > "$FLOWDESK_BACKUP/source-data.tgz"
sha256sum "$FLOWDESK_BACKUP/flowdesk.dump" "$FLOWDESK_BACKUP/source-data.tgz" > "$FLOWDESK_BACKUP/SHA256SUMS"
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml up --detach --wait --wait-timeout 180
```

恢复演练使用全新独立 Compose 项目及空卷，先校验 SHA256SUMS。数据库按服务器手册恢复到全新库；对象数据在启动 SeaweedFS 前，将 source-data.tgz 解包到该项目的空 source_data 卷 `/data`，必须包含 filerldb2 元数据而非只复制 volume 文件。保持相同固定镜像、bucket 和内部 endpoint，启动前检查权限/磁盘，启动后逐个下载校验。这里只提供运维步骤；本阶段实际验收包含对象重启后下载和应用级恢复，未执行远程供应商快照或服务器整机恢复。

实现、故障测试及交付证据见[M7.4 说明](../docs/m7-object-storage.md)和[实施记录](../docs/progress.md)。
