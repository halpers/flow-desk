# IP:端口部署配置验收

日期：2026-11-04。范围为本机 Windows Chrome + WSL Docker 的独立临时 Compose 项目，使用空 PostgreSQL 数据卷和合成演示账号。未连接远程服务器，未执行 Ubuntu apt 安装、真实公网访问、生产身份接入或真实模型调用。

## 配置与环境

- 运维主入口：[单机服务器部署手册](../../deploy/server.md)。默认 HTTP IP:端口，可选 HTTPS IP SAN 证书；域名可选。
- 独立项目 `flowdesk_server_entry_20261104`，Web 仅绑定 WSL 私有 IPv4；原 `flowdesk` 本地服务、数据库、模型配置及发布记录保持原状。
- API/Agent 使用 M6.5 本地缓存镜像；Web 从本次修改后的源码构建。发布脚本使用本地专用镜像别名和合成的 40 位 SHA `0000000000000000000000000000000000000001`，`--pull-policy never`，没有把该别名作为真实仓库版本发布。
- 模型 mock、检索 baseline、工具 mcp；不注入真实聊天或 embedding 密钥，不产生外部模型调用。
- Docker Compose v5.5.0。服务器配置要求 Compose 2.24.4+；Ubuntu 24.04 的完整安装流程只做文档检查，未在一台新 Ubuntu 服务器执行。

## 实测结果

| 检查 | 结果 |
| --- | --- |
| 随机初始化、已有 `.env` 不覆盖、非法 Origin/端口拒绝、默认 80 端口规范化 | 通过；`scripts/check-server-entry.py` |
| HTTP/HTTPS + monitoring 最终 Compose 配置 | 通过；Web 只有一条选定端口，API/DB/监控为 loopback，Agent 无宿主端口 |
| HTTP `http://172.28.253.80:23000` | 完整 release readiness 通过；页面 200、匿名 401、错误 Origin 403、登录/刷新/退出通过，Cookie 无 Secure |
| HTTP IP Chrome 完整回归 | 5/5，约 1.3 分钟；会话/SSE 断线与刷新、丢失批准响应、物流建单→接手→办结、知识上传/激活/撤回、团队撤权 |
| 非安全浏览器上下文 | 工作台用例实际断言 `isSecureContext=false` 且 `crypto.randomUUID` 不存在，查询仍成功；验证走到随机字节 UUID 回退 |
| HTTPS `https://172.28.253.80:23443` | 完整 release readiness 通过；客户端显式信任临时证书并验证 IP SAN；页面/认证/Origin/刷新/退出通过，Cookie 有 Secure |
| HTTPS Chrome 查询与刷新 | 1/1，7.6 秒；浏览器测试对临时自签证书开启 ignoreHTTPSErrors，未修改操作系统信任库；独立 HTTP 客户端没有跳过证书校验 |
| HTTPS → 前一 HTTP 入口应用回滚 | 通过；先恢复匹配的 HTTP `.env`，rollback 自动选择记录中的 server=true、tls=false，再次通过登录/刷新/退出 |
| 数据库备份恢复 | 225,760 字节 custom-format dump，恢复到全新演练库；原库与恢复库均为 5 条 action_receipts、84 条 checkpoints；原库未覆盖 |
| 前端单测、类型检查及构建 | 4/4 单测，TypeScript/Vite 通过；保留原有构建 chunk 大小提示 |
| 部署脚本 Ruff、工作流 actionlint | 通过 |
| 运维手册 bash 示例 | 20 个代码块通过 `bash -n`；该检查不执行示例中的安装/发布命令 |

本次 UUID 回退使用 `crypto.getRandomValues()` 生成 v4 UUID，覆盖全部会话、工单、知识和团队的客户端请求身份；不会改动后端的授权或幂等协议。新增一项单测覆盖没有 randomUUID 时的格式、唯一性及随机字节来源。

## 复核与边界

仓库根目录执行 `python3 scripts/check-server-entry.py` 可重复检查配置合并和初始化；不启动服务，不访问模型，会清理自己创建的临时 env 文件。CI 已加入同一检查，Linux runner 还验证新 env 文件权限为 0600。

浏览器复跑可使用 `FLOWDESK_E2E_ENV_FILE` 指向合成环境配置、`FLOWDESK_E2E_URL` 指向实际入口，再在 Web 目录执行 `pnpm test:e2e`。HTTP 非安全环境可加 `FLOWDESK_E2E_EXPECT_INSECURE=1`；自签 HTTPS 验收可加 `FLOWDESK_E2E_INSECURE_TLS=1`，不能用它代替证书身份验证。

独立测试容器和数据卷在验收后清理。本次仅验证本地配置与运行路径，不能据此声称生产网络、异地备份、公开可信 IP 证书、自动续期、多副本或生产 RPO/RTO 已验收。服务器管理员应按手册在实际环境重新执行上线验收并保留交接记录。
