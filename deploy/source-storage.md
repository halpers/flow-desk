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

Go 账号仅需目标桶 `knowledge/` 前缀的对象读取/写入，默认没有 DeleteObject、建桶或列桶操作；启用 M7.11 只读巡检才增加下节限定范围的 ListBucket。关闭公共读取，限制网络来源，使用验证通过的 TLS 证书；自签根证书需按服务器信任链部署，应用没有跳过 TLS 校验选项。按存储策略选择版本保护并做独立备份；启用或曾启用版本控制的桶不能开启下节的副本字节清理，生命周期规则不得自动过期仍被引用的原件或清理标记。

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

## 归档副本清理（M7.8，可选）

默认 `FILE_STORE_CLEANUP_ENABLED=false`。开启前升级同一 SHA 的 API/Web 和增量迁移 `2026110505_source_objects.sql`，独立备份数据库及对象；使用从未启用版本控制的私有桶。应用账号额外需要桶级 `s3:GetBucketVersioning`，清理本身仍无需 DeleteObject/ListBucket（另行开启巡检时需要限定 ListBucket）。必须验证 GetBucketVersioning 返回无 Status、错误 If-Match 被拒绝、正确 ETag 条件替换和 If-None-Match 防覆盖；当前仅实测固定 SeaweedFS 沙箱。启用/暂停版本控制、查询失败、对象锁或条件写不兼容时保持关闭，不通过授权通配符绕过。

确认条件满足后设 `FILE_STORE_CLEANUP_ENABLED=true`，按原发布脚本/全部 override 重建 API；浏览器按[服务器手册 §18](server.md#18-归档副本清理)先恢复原件、下载校验，再预览和人工确认。清理仅把已登记的旧副本替换为零字节永久标记，归档对象名/元数据数量不会减少；不会扫描未登记历史孤儿、清除供应商历史版本或缩小备份。标记不能删除或被生命周期过期，否则迟到归档任务可能重新上传旧文件。没有保留时间自动清理任务。

仅在本节上方的独立 mock 沙箱，验收可以这样执行；`work` 中配置副本包含随机测试凭据，结束后按测试项目销毁并删除副本，不要提交：

```bash
python scripts/configure-source-storage.py --enable-cleanup
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml up --detach --wait --wait-timeout 180
CGO_ENABLED=0 go -C flow-desk-serv/flowdesk test -c -o "$PWD/work/source-store.test" ./internal/client
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml run --rm --no-deps --volume "$PWD/work/source-store.test:/tmp/source-store.test:ro" --env FLOWDESK_S3_SANDBOX_TESTS=1 --env MODEL_PROVIDER=mock --entrypoint /tmp/source-store.test api -test.run '^TestS3SandboxRetirementContracts$' -test.v
install -m 600 .env work/source-cleanup.env
python scripts/check-source-cleanup.py --env-file work/source-cleanup.env --output work/source-cleanup.json
```

以上协议探针新建随机合成对象并保留标记，只用于一次性测试桶，不能在生产桶执行。验收脚本同时要求 `MODEL_PROVIDER=mock`、清理开关和 `work/` 下的配置文件；不访问付费模型。

## 故障处理与回退

| 现象 | 运维操作 |
| --- | --- |
| 新文件保持数据库原件 | 检查配置、网络、桶权限及条件写支持；索引/下载仍可使用数据库原件。修复后自动重试，8 次失败为死信，再点“归档原文件”重新投递 |
| SOURCE_UNAVAILABLE（503） | 核对 endpoint/region/bucket 与首次归档配置、凭据和对象备份；不要通过改数据库 key 绕过校验 |
| SOURCE_INTEGRITY_FAILED（503） | 从受控备份恢复同键的正确原件，按版本 content_hash 与 source_size 验证，不能覆盖为任意新内容 |
| SOURCE_ARCHIVE_DISABLED（409） | 当前只暂停/未启用归档；下载和恢复已归档文件仍需要完整的原存储配置 |
| SOURCE_CHANGED（409） | 读取期间发生恢复/再归档，刷新版本后使用新的请求键重试 |
| SOURCE_CLEANUP_DISABLED / SOURCE_CLEANUP_UNSUPPORTED（409） | 核对显式开关、桶版本控制查询权限及条件写能力；保持保护，不手改对象或登记表 |
| SOURCE_COPY_PROTECTED（409） | 刷新副本预览，核对是否仍为当前原件、代次或存储配置已变化、数据库原件未通过校验 |
| SOURCE_CLEANUP_UNAVAILABLE / 长时间清理中 | worker 持续退避重试，间隔上限 30 秒；核对网络/权限/内容完整性及标记，不手工完成任务 |

暂停新归档时把 provider 改为 postgres，但保留 endpoint/region/bucket/凭据，按原发布配置重建 API。下载及“恢复至数据库”仍可访问原存储；待处理归档任务会重试直至死信，重新开启后可手动投递。不要同时修改桶或 endpoint，定位指纹会拒绝访问不同存储；更换访问密钥不改变指纹。

回退 M7.3 或更早镜像前，在新版 API 上暂停归档，逐个将全部已归档版本（包括未激活和已撤回文档）恢复至数据库。管理员界面刷新确认，运维通过 owner 的只读查询检查：

```sql
SELECT source_storage, count(*) FROM business.document_versions GROUP BY source_storage;
SELECT status, count(*) FROM business.outbox_events WHERE topic='knowledge.archive' GROUP BY status;
```

只有不存在 s3 行，且没有 pending/delivering 归档任务时才回退；未清空任务应保持新版运行，等旧 epoch 任务被确认或进入死信。处理全部待索引/业务任务及既有 OCR 回退条件，备份后按[服务器回滚步骤](server.md)执行。原对象保留，不执行 Down、不删除定位列。迁移支持旧版 INSERT 自动补齐 size，不能因此跳过恢复已归档文件。丢失原对象且无备份时无法从提取文本还原原始 PDF。

## 一致性备份与恢复

启用 S3 后，PostgreSQL dump 仅含原件定位，已归档字节不在 dump 中。每套备份需保存数据库（包括 source_objects 和 outbox）、对象/对象版本及零字节标记、endpoint/region/bucket、存储凭据恢复渠道、源码/镜像 SHA 和备份时间。包含所有当前引用键与防重写标记；备份及凭据须加密存到独立受控位置。M7.8 人工清理会替换旧副本，必须用一致时间点的完整备份恢复，不能只恢复数据库或把旧 PDF 覆盖到 retired 标记上。

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

实现、故障测试及交付证据见[M7.4 说明](../docs/m7-object-storage.md)、[M7.8 说明](../docs/m7-source-cleanup.md)和[实施记录](../docs/progress.md)。

## 版本范围的只读对象巡检

M7.11 增加 `FILE_STORE_INVENTORY_ENABLED=false`，与清理开关独立。只有明确授予目标桶 `knowledge/*` 前缀的 `s3:ListBucket`，且验证普通桶 ListObjectsV2、StartAfter、按键排序及 URL 编码兼容性后才启用；没有 DeleteObject、ListObjectVersions 或全桶扫描要求。示例权限和逐步操作见[服务器手册 §21](server.md#21-s3-对象巡检与登记对账)。readiness 不探测列举权限，须另行验证页面。

巡检失败不会返回空列表或改变原件。SeaweedFS 4.52 对含空格键的列表使用有歧义的原始加号，此类页面会被拒绝；应用标准键不含空格。未登记不表示可删，零字节列表不证明清理标记有效，列表未出现也不证明文件丢失。外部供应商仍需独立测试桶验收。

以下仅用于 `work/` 中的独立 mock 栈，脚本会创建合成 PDF、反复归档/恢复并删除该新版本的一条登记，以模拟旧未登记对象，不能在生产运行：

```bash
python scripts/configure-source-storage.py --enable-inventory
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml up --detach --wait --wait-timeout 180
mkdir -p work
install -m 600 .env work/source-inventory.env
python scripts/check-source-inventory.py --env-file work/source-inventory.env --output work/source-inventory.json
```

## 未登记旧副本的人工登记

M7.12 的 `FILE_STORE_ADOPTION_ENABLED=false` 与巡检/清理独立。已有巡检 ListBucket 及原件 GetObject 权限足够，不额外增加写对象或删除权限。先在独立测试桶验证完整流程，再显式开启；服务器操作、资格保护、失败重试和回退见[服务器手册 §22](server.md#22-未登记副本人工核验与登记)。数据库原件必须先恢复并核对，标准旧副本须匹配大小/hash 后才登记，不自动批量修复。

以下仅在独立 mock 栈执行。验收会对新合成版本移除初始登记，随后恢复、核验登记并执行一次已有清理流程；不能在业务库或生产桶运行：

```bash
python scripts/configure-source-storage.py --enable-inventory --enable-adoption --enable-cleanup
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml up --detach --wait --wait-timeout 180
mkdir -p work
install -m 600 .env work/source-adoption.env
python scripts/check-source-adoption.py --env-file work/source-adoption.env --output work/source-adoption.json
```

## 租户范围对象巡检

M7.13 的 FILE_STORE_TENANT_INVENTORY_ENABLED 默认 false，并要求 FILE_STORE_INVENTORY_ENABLED。仅内置租户管理员可以读取当前可信租户前缀；已知版本继续检查库 ACL/团队，无权限项隐藏。未知/非标准对象只能人工核对，名称不构成归属证据。沿用第 21 节限定 knowledge/ 前缀的 ListBucket 权限，无额外 GetObject、写入或删除权限；部署、排障和停用见[服务器手册 §23](server.md#23-租户对象巡检与归属核对)。

以下命令只用于已有独立 mock/S3 开发验收栈，要求安装项目固定 Go/Python 依赖。脚本创建新合成租户，并通过专用 Go 测试构造 25 个租户对象及一个外租户对象；不能在业务库/生产桶运行。测试二进制是临时文件，不进入镜像发布产物。实际本地结果见[验收报告](../reports/m7/tenant-source-inventory-acceptance.json)。

```bash
python scripts/configure-source-storage.py --enable-tenant-inventory
docker compose --file docker-compose.yaml --file deploy/compose.storage.yaml up --detach --wait --wait-timeout 180
mkdir -p work
install -m 600 .env work/tenant-source-inventory.env
CGO_ENABLED=0 go -C flow-desk-serv/flowdesk test -c -o "$PWD/work/source-store.test" ./internal/client
python scripts/check-tenant-source-inventory.py --env-file work/tenant-source-inventory.env --output work/tenant-source-inventory.json
```
