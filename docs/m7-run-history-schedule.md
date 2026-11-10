# M7.30 定时任务历史归档

本阶段提供租户级、默认关闭的定时归档。实际验收和范围以[实施记录](progress.md)为准。

## 授权与产品行为

当前租户内置admin且具有run:observe的管理员可读取策略；保存需当前会话、期望修订、Idempotency-Key和明确confirm。首次未配置返回revision=0、关闭、保留30天、间隔60分钟。保留天数30–3650，间隔5–10080分钟。启用是持续维护授权，绑定保存者、当时auth_version及策略修订；不保存密码、access/refresh令牌或浏览器会话。普通退出不撤销此明确授权；禁用账号、角色或权限变化、改密/重置/全会话撤销使原授权失效，需当前管理员再次确认保存。关闭策略阻止后续自动调用和完成，不撤销已提交的维护意图；已有pending仍可从原历史窗口人工核对。

每个到期周期至多检查一个候选并进行一次归档推进。候选限当前可信租户、终态、超过配置保留期且没有任何历史维护操作；人工恢复后的任务不会再次自动归档。按任务ID循环扫描，受保护项跳过，扫描尾部回到起点；没有全量扫描、跨租户选择、自动恢复或永久删除。间隔基于数据库时间，不提供cron或固定墙钟时间承诺。

## 持久执行和恢复

Go拥有run_history_schedules表。到期扫描只发现已明确启用的策略，不据此授权业务。短事务用FOR UPDATE SKIP LOCKED领取单一策略、生成租约token并记录2分钟到期时间。不同API实例共享持久状态；旧worker每次进入事务均验证token、租约、修订、启用状态和当前授权。外部Agent请求在事务外，有30秒整轮上限。

不改写通用HTTP身份校验。后台使用仅服务端可构造的归档授权路径，复用M7.23保护检查、永久封存和不可变操作回执；分别在Agent观察前、登记意图及Agent返回后复核当前策略、用户、任务和团队。登记原操作与策略last_operation_id同事务提交；重启或响应丢失只继续该原操作，不另建代次。业务载荷移动、完成审计及操作完成仍在同一事务。

修改策略或关闭会立即使在途租约失效。若已有原操作pending，可关闭但不能重新启用或修改参数直到人工核对完成。权限失效停止自动调用，显示authorization_required；其他管理员可明确保存接管。调度失败保留原意图，按间隔重试；已在Agent完成但Go未确认的操作仍可恢复。租约过期后的旧worker不能提交Go完成；它已发送的Agent请求可能完成，下一worker依原回执核对。

## 接口和前端

GET /api/v1/run-history-schedule读取当前租户策略和最近结果；PUT同路径接受enabled、minimum_age_days、interval_minutes、expected_revision、confirm及请求键。响应仅元数据，不返回消息、检查点、密钥或用户auth_version。旧键重放返回原保存回执，不覆盖当前策略。任务记录中的“定时归档”展示当前修订、保留期、间隔、下次检查、最近结果和任务/操作ID；保存明确勾选，失败保留固定正文和键，成功重读当前状态。失权清空内容，页面关闭取消读取，浏览器只调用Go。

## 退出标准

数据库集成覆盖默认关闭、修订/同键/权限、多个worker领取、ServiceContext重建模拟进程依赖重启、原操作恢复、Agent调用期间关闭及撤权、最后步骤回滚、受保护/跨租户/已恢复任务隔离、数据库时间保留期和有界循环。独立mock Compose通过公开HTTP配置，等待真实worker执行并验证公开历史保持、关闭停止、原键恢复及无新代次；Chrome验证确认、刷新/失去响应及失权清空。完整Go、受影响Web和既有归档回归需通过后才能标记完成。

## 运维总开关

RUN_HISTORY_SCHEDULER_ENABLED默认false，所有API均需一致配置。总开关关闭时不领取任务；界面返回runtime_enabled=false并说明等待运维开启。启用总开关不会自动生成租户策略，仍需管理员明确授权。环境变更须重建全部API；在途外部调用可能已完成Agent侧，停止/关闭不取消原pending意图，按原操作核对。

备份恢复前保持全部API停止，将总开关设为false后启动配套版本，先核对恢复的各租户授权和原pending操作，再按当前决定关闭/重授策略；最后才开启总开关。恢复旧备份会回退策略、租约和回执，不承诺保留备份之后的最新变更。

复跑只使用work/可丢弃mock配置并显式开启总开关：

```powershell
python scripts/check-run-history-schedule.py --env-file work/schedule-ci.env --output work/run-history-schedule.json --browser-fixture work/run-history-schedule-fixture.json
$env:FLOWDESK_RUN_HISTORY_SCHEDULE_FIXTURE = "../work/run-history-schedule-fixture.json"
pnpm.cmd --dir flow-desk-web test:e2e tests/e2e/run-history-schedule.spec.ts
```

首次启用或重新保存后，next_run_at为数据库当前时间加配置间隔，是可领取时间而非完成期限。每个API每5秒最多领取一个到期策略，负载、停机和故障可能导致排队延迟；每次至多检查一个候选，不作为大规模批次吞吐承诺。最近维护任务/原操作字段保存原回执，不把后续人工恢复解释为原归档失败。

## 本次实际验收

Go全量178个顶层/含子用例467项和vet、新增7项数据库专项及最终聚焦复验通过；[独立租户真实后台API5/5](../reports/m7/run-history-schedule-acceptance.json)、原单项归档HTTP4/4、Chrome2/2、Web8单测和类型/生产构建、Ruff及actionlint通过。Chrome丢弃真实已提交的保存响应后验证同正文/键重试及刷新；数据库专项通过重建依赖验证pending恢复，不把它解释为真实杀进程试验。运维见[服务器手册§40](../deploy/server.md#40-定时任务历史归档)。
