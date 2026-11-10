# M7.28 用户自助修改密码

所有当前启用且持有有效会话的用户都可在工作台“修改密码”。操作只改变自己的凭据，不接受用户、租户或角色参数；管理员为其他用户重置密码仍使用原账号管理入口。实现和实际测试结果以[实施记录](progress.md)为准。

## 接口与凭据处理

`POST /api/v1/auth/password`使用现有Bearer会话认证，并要求可信Origin和`X-FlowDesk-Client: web`。JSON正文仅接受`current_password`、`new_password`和`confirm=true`，最多1024字节；未知字段、尾随JSON或非JSON请求拒绝。SessionAuth在验证凭据前先设置no-store，缺失、失效或已撤销凭据的401同样不可缓存。成功200返回EmptyResp并清除`/api/v1/auth`的HttpOnly刷新Cookie，所有结果`Cache-Control: no-store`。

```json
{
  "current_password": "Synthetic-old-password-123",
  "new_password": "Synthetic-new-password-456",
  "confirm": true
}
```

新密码与管理员设置采用同一规则：UTF-8，12–72字节，至少一个Unicode字母和一个十进制数字；不能与当前bcrypt凭据匹配。旧密码沿用登录验证能力，不能自动去掉首尾空白。仅数据库用户行保存带盐bcrypt；不持久化原密码、新密码、普通摘要或幂等请求指纹。审计只有用户身份和固定摘要。

此认证接口不使用Idempotency-Key，不返回新会话。成功后的旧会话必然失效，重复旧请求不能撤销以后重新登录得到的新会话。应用固定关闭go-zero原生请求日志，防止错误/慢请求DumpRequest记录密码；metadata tracing和指标继续使用原有配置。

## 事务与并发

1. 可信当前租户授权共享锁下，读取启用用户、auth_version和精确活跃session，结束只读事务。
2. 以可信tenant/user限流，在数据库锁外验证当前bcrypt并生成新bcrypt。
3. 新事务按租户授权排他锁→自己的用户FOR UPDATE顺序，再检查启用状态、原hash、原auth_version和精确活跃session。
4. 同事务更新密码、revision/auth_version，撤销该用户全部会话及既有会话家族授权，再写固定脱敏审计。后置失败整体回滚，角色/团队/业务回执保持原值。

管理员重置、账号停用和角色修改使用同一授权写锁；登录/刷新使用授权共享锁。bcrypt期间发生凭据更改、撤权、退出或原session轮换时，本次提交拒绝。期间产生的新登录若先提交，也会被密码变更的全用户撤销包含。同旧凭据并发请求只有一个可提交。新登录不会恢复此前run家族/能力的授权。

独立限流按可信tenant/user计算，换IP或重新登录不能在同实例重置额度：每账号突发5次，每分钟补1次，另有全实例每秒2次/突发10次上限。最多4096账号条目，闲置15分钟回收；满表拒绝而不驱逐活跃条目。这是M7.28交付时的实例内存实现；M7.29已用[PostgreSQL共享持久限流](m7-auth-rate-limits.md)替代，额度在实例间共享并跨重启保持，满额及安全回收按新方案执行。

## 页面与结果不确定恢复

用户填写当前密码、新密码、再次输入，并明确勾选退出所有设备；确认期间不可关闭或重复提交。表单失败、关闭和卸载都清除密码，不存入本地存储。成功时清内存token、取消并清空QueryClient，卸载全部工作台内容，展示新密码重登提示。

提交前先在sessionStorage保存仅值为1的重新认证标记，不保存身份、密码、token或请求正文；无法写入标记时不发送改密请求。App初始恢复和client.refresh发出/接收结果时均检查标记，防止刷新页面或迟到refresh重新恢复旧认证。明确未提交的400/403/409/429清理标记并清表单；成功、网络失败、超时、401或不确定结果保留标记，清空密码及当前页面数据并退出，提示先用新密码登录；如未成功再尝试旧密码或联系管理员。只有显式账号密码登录成功才清除此重新认证要求，系统不会自动重新提交密码。标记是当前标签页的交互恢复保护，不替代服务端会话撤销。

常见状态：400 `CURRENT_PASSWORD_INVALID` / `PASSWORD_UNCHANGED` / `INVALID_ARGUMENT`；401 `UNAUTHENTICATED`；403 `ORIGIN_REJECTED`；409 `AUTHORIZATION_BUSY`；429 `PASSWORD_CHANGE_RATE_LIMITED`。没有邮件找回、短信验证码或密码历史策略；管理员仍可按既有流程恢复账号。

## 验证与运维

无需新数据库迁移、Agent改动、模型服务或新公网端口，HTTP IP:port与HTTPS入口一致。发布配套API/Web并保留`Middlewares.Log=false`的凭据脱敏约束；不能收集请求正文或将密码填入日志。管理员不需要停用最后一个管理员即可让其本人改密。

专项数据库测试验证全部会话/旧家族撤销、角色保持、不记录幂等摘要、错误输入无效果、审计后置失败回滚、并发唯一提交、bcrypt期间管理员重置/停用/刷新/退出以及新登录竞争。公开脚本只创建并修改自己的合成账号；Chrome验证明确勾选、错误后清表单、真实成功响应丢失、其他设备失效及新密码重登，并验证未提交503时旧Cookie仍有效但reload不自动刷新、必须显式登录。client单测覆盖跨模块重载的布尔标记、失败登录不清标记、迟到refresh不得恢复token，以及存储不可用时本地拒绝。实际执行结果见实施记录，不将本节测试设计当作已运行结果。

只在`work/`下可丢弃mock环境运行：

```powershell
python scripts/check-self-password.py --env-file work/password-ci.env --output work/self-password-ci.json
pnpm.cmd --dir flow-desk-web test:e2e tests/e2e/self-password.spec.ts
```

服务器升级、原管理员恢复密码和排障沿用[部署手册](../deploy/server.md)。

本地实测：Go全量165个顶层/含子用例451项和vet、6项新增数据库专项、[公开API6/6](../reports/m7/self-password-acceptance.json)、原账号管理回归、Chrome3/3及Web8单测和类型/生产构建通过。原102项公开操作和路由handler/鉴权保持，新增1项；Ruff、actionlint及文档校验通过。具体命令结果及限制见[实施记录](progress.md)。
