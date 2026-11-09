# M7.25 Agent 独立迁移与运行权限收紧

Agent HTTP 进程和 worker 不再执行应用迁移或 LangGraph setup。独立一次性作业完成应用表、checkpoint 表和运行授权后，Agent 才启动。运行账号不拥有 schema、表或索引，不获得迁移账号成员身份，也不能借助 public 或临时 schema 创建数据库对象。此阶段使用已有 PostgreSQL 和 Compose，不增加公网端口或模型调用。

## 账号与凭据

| 账号/服务 | 权限与职责 | 凭据出现的位置 |
| --- | --- | --- |
| flowdesk_owner / PostgreSQL | 首次角色初始化、现有卷显式所有权升级、原 Go 迁移 | 数据库、Go migrate 及维护 profile 的 agent-role-upgrade |
| fd_agent_migrator / agent-migrate | 拥有 Agent/knowledge schemas 和 Agent 表，执行应用及固定版本 LangGraph 迁移、授予运行 DML、显式 HNSW 维护 | AGENT_MIGRATION_DATABASE_URL，仅在一次性迁移/维护容器 |
| fd_agent / agent | Agent schema 表 SELECT/INSERT/UPDATE/DELETE，序列 USAGE/SELECT；schema_versions 与 checkpoint_migrations 仅 SELECT；knowledge schema USAGE | AGENT_DATABASE_URL，仅运行容器 |

新增 AGENT_MIGRATION_PASSWORD，由 bootstrap-local.ps1 / bootstrap-server.py 独立随机生成，不复用 AGENT_PASSWORD 或 owner 密码。根 .env 仍被 Git 忽略，运行 Agent 不接收 owner/migrator DSN、密码或角色成员授权。默认示例中的占位值不能作为实际凭据。

fd_agent 无超级用户、CREATEDB、CREATEROLE、REPLICATION、BYPASSRLS 或其他角色成员身份。初始化/升级在本 FlowDesk 数据库收回 PUBLIC 与运行账号的 CREATE/TEMP，以及 public schema 的 CREATE；这意味着不能利用 CREATE TEMP 或另一个公开 schema 绕过限制。Go 的业务权限和业务迁移职责保持分离。多个应用共用同一数据库的部署不属于本 Compose 拓扑，应先评估 PUBLIC 权限变化，不能直接套用维护命令。

## 首次安装

空卷 PostgreSQL init 创建独立 migrator 角色并把 agent/knowledge schema 交给它；业务 schema 仍归原 owner 管理。启动顺序为 PostgreSQL 健康 → Go migrate 成功 → agent-migrate 成功 → Agent readiness → API → Web。完整 Compose up 和 scripts/start.ps1 使用相同依赖，release.py 在启动 Agent 前显式执行 agent-migrate，任何失败均不推进成功发布记录。

迁移命令为 python -m flowdesk_agent.storage.migrate。它只接受 AGENT_MIGRATION_DATABASE_URL，拒绝错误账号/schema；运行 Agent 不作为兼容回退。应用迁移使用同一个数据库 advisory lock，先核对版本记录为合法连续前缀，再用锁持有连接执行固定依赖版本的 LangGraph setup，随后按顺序执行待应用迁移。每项应用迁移及其版本记录同事务提交。后续阶段追加 MIGRATIONS 时自动按列表长度计算最新版本，不能另写硬编码“最新为10/11”。

所有迁移完成后统一授予现有/未来 Agent 表所需 DML，并再次收回迁移记录表的写权限；运行角色不获得 TRUNCATE、TRIGGER、REFERENCES 等额外表权限。执行失败后保留已完成的有序迁移，修复后重新运行同一作业；不执行 Down 或清空 schema。

## 已有数据卷升级

已有卷不会重新执行 init 脚本，修改 .env 也不会自动更改 PostgreSQL 密码。先按[服务器手册](../deploy/server.md)备份数据库、发布状态与环境配置，核对活动任务及旧 worker，安排维护窗口。新账号配置与所有权迁移是显式操作，不由普通 Agent 启动偷偷执行。

1. 在与新镜像匹配的源码目录配置独立密码：

```bash
python3 scripts/configure-agent-migration.py
```

脚本只在既有 .env 缺少此项时追加随机十六进制凭据；已有字节/密钥不改写、不打印，重复运行保持相同值。已有空值、重复项或非受支持值明确拒绝且保留原文件。新角色尚不存在时首次使用该值创建；已有角色密码不会重置，需保持已配置的真实凭据，错误密码应通过正式轮换流程处理。

2. 记录目标发布 SHA 与仓库，先拉取包含 M7.25 的 Agent 镜像。处理在途/待确认任务后，停止入口与运行进程，保留 PostgreSQL 和数据卷：

```bash
FLOWDESK_MIGRATION_IMAGE="$FLOWDESK_REGISTRY/flow-desk-agent:sha-$FLOWDESK_REVISION"
docker pull "$FLOWDESK_MIGRATION_IMAGE"
docker compose stop web api agent
FLOWDESK_AGENT_IMAGE="$FLOWDESK_MIGRATION_IMAGE" docker compose run --rm --no-deps agent-role-upgrade
```

agent-role-upgrade 是 maintenance profile 下的 owner-only 命令，要求维护确认。它拒绝仍存在的 fd_agent 数据库连接，使用独立事务/目标 advisory lock、5秒锁等待和60秒语句上限。只转移 agent/knowledge schema 内已核对的关系；表的自动 TOAST 关系按 PostgreSQL 实际依赖关联识别，不把所有 pg_* 对象当作可信。发现运行角色拥有其他用户 schema/关系，或任何 schema 中的自有 routine/独立类型等需要单独评审的对象时明确拒绝；自动行/数组类型只按真实内部依赖及父对象所有权核对，不会使用全库 REASSIGN OWNED 或触碰业务表。

已有 migrator 角色必须能登录且不含超级用户、创建数据库/角色、复制、绕过RLS及角色成员身份等异常能力，否则停止。所有权和权限调整同事务，后期对象检查失败会回滚此前调整；不更改业务数据、原 Agent 行、历史密码、图状态或现有 checkpoint。

3. 运行同一目标镜像的迁移作业，成功后按原服务器入口/TLS/监控选项执行固定 SHA 发布：

```bash
FLOWDESK_AGENT_IMAGE="$FLOWDESK_MIGRATION_IMAGE" docker compose run --rm --no-deps agent-migrate
python3 scripts/release.py --revision "$FLOWDESK_REVISION" --registry "$FLOWDESK_REGISTRY" --server
```

HTTPS/监控继续追加原有 --tls / --monitoring。release.py 会重复安全执行两个迁移作业并等待健康，不需要手工改迁移记录。不能改用裸 compose up 来替换固定版本镜像。

## 启动与 readiness 门禁

Agent lifespan 及 /health/ready 都只进行 READ ONLY 检查，单语句3秒上限。必须为 fd_agent、当前 schema=agent，应用版本精确等于1..len(MIGRATIONS)，checkpoint 版本精确等于固定库迁移序列。缺项、版本间隙或比当前镜像更新的数据库都拒绝；不会自动补建表、跳过版本或继续启动 worker。

同一门禁核对运行角色的数据库/schema CREATE/TEMP、角色属性/成员身份、关系/routine/独立类型所有权（覆盖其他 schema）、业务 schema 可访问性及当前 Agent 每张表的精确读写权限。额外权限或缺少必要 DML 都失败。运行中权限/版本变化会让 readiness 返回503，不打印 DSN、SQL 原始异常或密钥。存量进程权限变更仍应停止并重建，不把健康探针当作对已经开始事务的即时撤权机制。

| 错误/现象 | 处理 |
| --- | --- |
| 缺少 AGENT_MIGRATION_PASSWORD | 新安装用 bootstrap；已有环境运行显式 configure 脚本，不复用运行密码 |
| AGENT_ROLE_UPGRADE_STOP_RUNTIME_FIRST | 停止旧 Agent 并核对连接，无在途进程后重试；不绕过门禁 |
| AGENT_ROLE_UPGRADE_UNEXPECTED_OWNER / REVIEW_OWNED_ROUTINES / REVIEW_OWNED_TYPES | 保持备份和原权限，交研发核对未知所有权，不执行全库重分配或强制删对象 |
| AGENT_ROLE_UPGRADE_MIGRATOR_UNSAFE | 核对 migrator 角色来源和权限，不把超级用户连接当作迁移账号 |
| AGENT_MIGRATION_FAILED | 检查独立迁移作业、账号/schema、空间及版本；修复后重跑，不启动运行进程补迁移 |
| AGENT_SCHEMA_VERSION_MISMATCH | 镜像/数据库版本不匹配或记录不连续，换兼容镜像或修复迁移，不手工补版本数字 |
| AGENT_RUNTIME_PRIVILEGES_UNSAFE / TABLE_GRANTS_INVALID | 检查所有权升级及迁移授权，保持运行 DDL 关闭，不为启动成功授予 owner 权限 |
| 普通登录失败 | 既有卷不因 env 变化而轮换密码，核对正确凭据；不删数据卷解决 |

## HNSW 运维兼容

M7.22 的维护 CLI 改为显式 AGENT_MIGRATION_DATABASE_URL，不回退读取运行连接。使用相同发布镜像的一次性 agent-migrate 服务覆盖 entrypoint；目标 ID/profile/维度沿用原手册中已核对的值：

```bash
FLOWDESK_AGENT_IMAGE="$FLOWDESK_MIGRATION_IMAGE" docker compose run --rm --no-deps --entrypoint python agent-migrate -m flowdesk_agent.ann_index status --tenant-id "$TENANT_UUID" --profile "$SEMANTIC_PROFILE" --dimension "$VECTOR_DIMENSION"
FLOWDESK_AGENT_IMAGE="$FLOWDESK_MIGRATION_IMAGE" docker compose run --rm --no-deps --entrypoint python agent-migrate -m flowdesk_agent.ann_index create --tenant-id "$TENANT_UUID" --profile "$SEMANTIC_PROFILE" --dimension "$VECTOR_DIMENSION" --confirm-maintenance
```

drop 同样从此维护容器运行。日常 Agent 不接触迁移凭据。原目录/指纹验证、并发维护互斥、精确默认和回退保持。CLI维护是受信任运维动作，不是模型工具。

## 回退与验证

本阶段不提供 Down。旧镜像会在运行角色中执行 setup/migrate，与本阶段的 DML-only 权限不兼容；只回退到已经支持独立迁移的配套版本，不能临时归还运行 DDL/owner 权限来让旧镜像启动。首次跨此边界发布失败时保持维护状态，修复并向前发布兼容版本，或按原手册将升级前一致备份恢复到新空库后切换；原有图版本与历史归档的回退限制仍适用。

tests/test_migration_roles.py 使用唯一临时数据库和随机实际登录角色，验证图checkpoint真实写入与连接替换后读取、队列DML、拒绝DDL/TEMP/越权角色/业务访问及迁移元数据写入、版本缺失/未来/间隙、额外权限/缺少DML、旧所有权依赖、后置失败回滚和密码保持。实际运行账号完成语义索引DML与exact/HNSW检索，独立迁移账号完成HNSW创建/状态/删除，运行账号不能创建/删除索引。测试要求一次性开发数据库管理员，不重置现有业务 schema。

scripts/check-agent-migration.py 只接受 work/ 内的 mock 环境文件，验证实际部署账号、checkpoint恢复及迁移重放，不调用模型。configure脚本另有凭据字节保持和幂等测试。实际执行证据按[实施记录](progress.md)登记，不把本文命令当作已在真实服务器执行。

本地最终Linux Agent全量86/86（含实际角色专项6/6）、Windows部署账号验收4/4通过。新空卷完整初始化与全栈readiness通过并已清理；原旧卷升级后预置命令1/1、租户观测绑定1/1保持且全栈健康。所有环境均一次性mock栈，模型调用为零，未在服务器执行部署。

验收脚本使用 Python3.12；Windows通过独立SelectorEventLoop运行异步数据库连接，Linux使用默认事件循环。先安装Agent锁定依赖后，从项目根目录执行：

```bash
python scripts/check-agent-migration.py --env-file work/parallel-local/.env --output work/agent-migration.json
```

Windows也可使用flow-desk-agent/.venv/Scripts/python.exe运行同一脚本；环境文件必须对应一次性mock栈，不能使用真实服务器.env。
