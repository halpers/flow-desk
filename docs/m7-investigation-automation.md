# M7.31 超时调查自动回待办

本阶段实现默认关闭的租户规则：调查中（investigating）的既有记录连续指定天数未更新后，自动转回待跟进（open）。保留原说明；每次流转产生新修订、完整不可变快照和scheduled审计。状态只描述跟进进度，不能据此证明对象归属或完整性。实现状态和实测以[实施记录](progress.md)为准。

## 策略和授权

新增GET/PUT /api/v1/investigation-automation，SessionAuth，仅当前内置admin且有knowledge:manage和conversation:use。首次返回修订0、关闭，默认7天/60分钟。PUT要求Idempotency-Key、expected_revision、enabled、idle_days（1–3650）、interval_minutes（5–10080）、confirm=true。保存修订加1，下一次检查为数据库时间加间隔；旧键回放返回原回执，不覆盖新策略。重放仍检查当前管理员和最近结果的资源ACL，页面成功后重读当前值。409 INVESTIGATION_AUTOMATION_CHANGED需刷新后重新确认。

策略保存owner及auth_version，表示持续授权，不保存登录密码、会话或令牌。普通退出不停止；改密、账号/权限变更使授权失效，当前管理员须明确重新确认。运维总开关INVESTIGATION_AUTOMATION_ENABLED默认false，与租户enabled共同控制执行。后台不伪造HTTP会话，仅在本用例内核对持久授权及资源当前ACL；已知版本和知识库read_roles、团队策略继续适用。未知资源身份后来恢复时同样按新ACL检查。

## 执行和恢复

每API独立5秒循环，每次至多一个到期租户、一条候选；5秒总deadline。FOR UPDATE SKIP LOCKED锁住策略直至本次事务结束，无外部I/O、跨服务调用或长期租约。按UUID循环游标有界选择同租户、investigating且DB updated_at超过阈值的记录。资源授权先于对象锁，之后重读版本/状态/时间，人工刚更新的记录跳过。当前owner最终共享锁阻止撤权与提交交错。

同一数据库事务更新状态（原说明不变）、触发器保存完整编辑历史、scheduled审计、游标/下一次时间/最近结果。任一步失败整体回滚，原策略仍到期，下次可安全重试；提交后进程重建从持久时间和状态继续，不重复修订。一次只改变一条记录；open/closed及新近更新的记录不自动流转。人工重新设为investigating并再次闲置到阈值后可再次回待办。

授权失效记录authorization_required；资源不可访问只记skipped，不返回该对象指纹或说明。周期可能排队，不是精确到期SLA。最近结果仅对当前仍有资源访问权的管理员返回last_object_ref/last_revision；不可访问则隐藏。策略元数据不包含私有桶、key、说明或模型内容。没有自动创建调查、自动结案、合并、对象登记、修复、删除或外部通知。

## 验收要求

隔离数据库验证确认/修订/同键并发、旧回执、双重开关、跨租户、当前授权和ACL、人工并发更新、完整快照和后置审计/策略失败回滚、多worker唯一效果、依赖重建继续及DB时钟边界。公开HTTP真实后台与Chrome确认/响应丢失重试/重读/关闭/撤权清空分别验收，不以mock回执代替数据库效果。运维步骤见[服务器手册§41](../deploy/server.md#41-超时调查自动回待办)，实际结果见下节。

## 本次实际验收

新增8项数据库专项及Go全量186个顶层/含子用例482项、vet、[真实后台公开HTTP5/5](../reports/m7/investigation-automation-acceptance.json)、Chrome2/2、Web8单测及类型/生产构建、Ruff及actionlint通过。Chrome丢弃真实已提交响应后核对同正文/键重试，数据库验证后置失败原子回滚、当前撤权和依赖重建后的持久恢复；不将重建依赖描述为实际杀进程试验。仅完成本地mock环境验收，未部署真实服务器。
