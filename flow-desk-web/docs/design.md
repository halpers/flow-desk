# 前端详细设计

技术路线：React + TypeScript + Vite + Ant Design + React Router + TanStack Query。该选择沿用参考项目的通用前端技术，不引入钱包或链上依赖。本文包含完整页面目标；当前已提供身份、工作台、知识管理、审批/输入与工单抽屉，具体实现及浏览器证据见[实施记录](../../docs/progress.md)，下文规划路由不表示均已单独实现。

## 1. 信息架构与页面

| 路由 | 页面 | 内容与权限 |
| --- | --- | --- |
| `/login` | 登录 | 账号密码、失败反馈和登录后返回原页面 |
| `/workspace` | 智能办理 | 自己的会话列表、消息、任务进度、补充输入、确认卡片 |
| `/workspace/:conversationId` | 指定会话 | 历史消息、当前 run 快照、恢复订阅 |
| `/orders` | 订单查询 | 订单号、时间和状态筛选，本租户脱敏数据 |
| `/orders/:orderId` | 订单详情 | 行项目、物流、关联工单，携带订单上下文发起会话 |
| `/tickets` | 工单列表 | 条件筛选、负责人和状态，游标分页 |
| `/tickets/:ticketId` | 工单详情 | 订单关联、内部备注、处理时间线、受权限约束的状态操作 |
| `/knowledge` | 知识库 | 授权知识库、文档与版本状态；上传/撤回仅 admin |
| `/runs/:runId` | 执行记录 | 脱敏步骤、工具摘要、耗时/用量、错误及业务回执 |
| `/audit` | 操作审计 | supervisor/admin 查看本租户审计，默认脱敏 |

首页进入智能办理。确认操作直接嵌入所属会话，不另建缺少接口来源的全局审批队列。菜单与按钮根据 `/auth/me` 权限生成展示，后端继续强制授权。

## 2. 智能办理布局

桌面采用三块区域：左侧会话导航；中间消息与输入；右侧当前订单、有效引用及办理结果。窄屏将右侧内容折叠为抽屉，会话导航可收起。

消息展示：

- 用户消息保留输入时间和可识别内容；助手消息展示文本、引用及实际工单回执。
- 进度只显示“查询订单”“检索规则”“等待确认”等用户可理解的说明。
- 工具结果以结构化摘要卡片展示，敏感信息由后端脱敏；不展示内部服务 URL、令牌或模型私有推理。
- 引用展示文档名、版本、页码/段落，点击通过授权 API 打开安全预览。
- 业务执行成功后显示工单编号和详情入口；即使总结失败，已完成的业务结果仍可见。

页面主要状态：idle、submitting、queued、running、waiting_input、waiting_approval、cancelling 和终态。状态以 Go 快照/事件为准，本地状态只补充网络发送和订阅连接信息。

## 3. 会话与发送交互

1. 用户提交时生成 client_message_id 和 Idempotency-Key，在收到明确结果前保持不变。
2. 列表可以显示“发送中”的本地消息占位，收到服务端 message_id 后合并。
3. 202 返回后读取 run 快照并订阅其事件；输入区提示正在办理。
4. 同会话已有活动 run 时保留草稿，提示先处理当前任务；不偷偷创建第二个 run。
5. 网络超时允许重试同一请求键，并查询已有任务，避免重复消息。

waiting_input 使用服务端 input_id/question/choices 渲染补充卡片，通过 input 接口回复；它不是发送普通消息的新任务。waiting_approval 展示精确操作卡片；用户要修改方案时先拒绝原动作，再以新消息发起。

## 4. 确认卡片

卡片内容由 Go display_snapshot 驱动：动作名称、订单/工单编号、即将写入的标题/内容/分类/优先级、政策依据、失效时间和必要风险说明。payload_digest 可保留在协议层，普通界面无需展示技术摘要。

确认按钮的 loading 和 disabled 防止误点，真正幂等由 Go 实现。提交只含 approval_id、decision、expected_version、reason，不提交修改后的工具参数。

- approved：展示“已确认，正在办理”，继续订阅结果。
- executed：展示服务端业务回执。
- rejected/cancelled：显示终结原因，允许新建消息。
- expired/invalidated/version conflict：刷新快照，提示重新生成方案。

用户刷新页面时从服务端恢复卡片，不能仅凭浏览器本地存储保留一张可点击旧确认。

## 5. SSE、重连与刷新

用 fetch + ReadableStream 解析 SSE，支持 Authorization、AbortController、Last-Event-ID；解析器须正确处理拆包、多行 data、注释心跳和 Unicode 边界。

每个订阅维护 run_id、last_applied_seq、message generation/chunk index 和连接状态。last seq 可以在当前会话内短暂保存，业务正文和凭据不放 localStorage。离开页面关闭订阅，后台 run 继续。

恢复算法：

1. GET run 快照，原子替换当前状态、待确认/待输入、草稿/最终消息，记录 last_event_seq。
2. 从该 seq 之后连接事件流。
3. 重复 seq 丢弃；连续事件按序应用；缺口先补拉再继续。
4. 网络断开指数退避并抖动，上限 15 秒；显示连接状态和手动重连入口。
5. 410 游标过期重取快照；401 走单次 token 刷新后重连；403 停止展示并清理受限缓存。
6. message.reset 清空旧 generation 草稿；completed 使用完整内容覆盖。
7. 终态完成后刷新关联订单/工单查询并关闭连接。

SSE 消息只更新相关 query key，禁止重放旧 tool.completed 时重复弹成功提示或重复发业务请求。

## 6. 前端代码与状态归属

```text
src/
  app/                 # 路由、Provider、布局、鉴权恢复
  features/
    auth/
    conversations/
    runs/
    orders/
    tickets/
    knowledge/
    audit/
  shared/
    api/               # HTTP client、生成类型、错误映射
    events/            # SSE parser、事件 schema、reducer
    auth/              # 内存 token、刷新 singleflight
    ui/                # 通用卡片、空状态、错误提示
    lib/               # 时间/金额/脱敏格式、稳定 ID
```

服务端状态交由 TanStack Query；组件管理筛选、草稿、抽屉等临时状态。run 状态 reducer 由版本化事件契约驱动，避免多个组件各自维护一套任务枚举。

query key 至少包含 tenant/user 作用域；logout 或租户/用户变化清空缓存、停止事件订阅并释放附件 ObjectURL。access token 只在内存；刷新流程 singleflight，防止同页并发轮换 refresh token。

## 7. 工单与知识库交互

工单状态和分配操作提交 expected_version，409 后重载详情并保留未提交表单供用户核对。Agent 创建工单与手工创建区分来源，但详情展示相同业务对象。

知识上传先做前端文件类型/大小提示，服务端执行最终校验。上传成功显示索引任务状态而非立即宣称可检索；用有界轮询获取文档版本状态。失败展示原因与重新上传入口；旧有效版本仍可使用。

文档预览通过带身份请求取 Blob 并控制生命周期，不拼带 token 的下载 URL。Markdown 禁止 raw HTML 和自动远程图片，链接限制安全协议并使用 noreferrer；避免来自文档的脚本或追踪资源。

## 8. 可用性和可访问性

- 所有输入有 label，确认/取消可用键盘完成，抽屉和弹窗正确管理焦点。
- SSE 频繁更新不逐 token 触发读屏通知，只对等待输入、确认和终态作有限 announce。
- 颜色之外增加文字/图标区分状态；耗时较长时显示真实进度和取消入口。
- 空列表、无检索结果、权限不足、离线和执行失败都有可操作提示。
- 时间按用户时区展示，API 时间保持 UTC；金额按字符串/精确格式呈现。

## 9. 契约与测试

M1 从 Go OpenAPI 生成 API 类型；事件使用 shared/contracts JSON Schema 及生成类型。生成输出放 shared/api 的明确 generated 目录，保留来源头，CI 检查重新生成无差异。

单元测试覆盖 SSE 拆包/乱序/重复/重置、状态 reducer、401 singleflight、确认过期和取消竞争。组件测试覆盖卡片不同状态、表单校验和安全 Markdown。Playwright 覆盖登录→发起任务→确认→看到工单，以及刷新/断线后恢复。

实现后建立 pnpm dev/build/typecheck/lint/test/test:e2e 等脚本，固定 Node/pnpm 版本并提交锁文件。设计阶段仅检查文档，不宣称前端构建成功。
