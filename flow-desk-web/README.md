# flow-desk-web

FlowDesk 前端服务目录。选型为 React + TypeScript + Vite + Ant Design，服务端状态使用 TanStack Query，路由使用 React Router。

M1 已实现登录、会话恢复、身份与权限展示、退出工作台，以及来自 Go OpenAPI 的生成类型。完整目标见 [前端详细设计](docs/design.md)。

- 规划开发端口：`5173`。
- 浏览器 API：`/api/v1`，开发时通过 Vite 代理到 Go 的 `8080`。
- 浏览器仅连接 Go，由 Go 处理身份、授权、业务写操作和 Agent 事件流。
- 已有命令：`pnpm dev`、`pnpm build`、`pnpm typecheck`、`pnpm api:sync`。先用 pnpm 11.25.0 执行 `pnpm install --frozen-lockfile`。
- 完整容器入口默认 `http://localhost:13000`，Nginx 将 `/api` 代理到 Go；Python 无浏览器入口。

跨服务行为见 [接口契约](../docs/contracts.md)。
