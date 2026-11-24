# 架构

## 分层

```
cmd/app → internal/app → internal/controller/http/v1 → internal/usecase（接口在 usecase/contracts.go）
                                                      → internal/repo（端口在 repo/contracts.go）
                                                      → internal/entity
```

| 层 | 职责 | 约束 |
|---|---|---|
| `internal/entity` | 领域对象、错误码表（与约定 §2.4 逐条一致）、角色与权限码、文件用途 | 不引用 pgx、生成代码、net/http（depguard） |
| `internal/usecase` | 业务规则，返回 `entity.Error` / `entity.ValidationError` | 不引用 controller |
| `internal/repo` | 端口与实现：`postgres`（sqlc）、`redis`（未配置 `REDIS_URL` 时进程内实现）、`webapi`（SMTP、短信、微信、支付宝、对象存储） | |
| `internal/controller/http` | 中间件链、操作表、policy、校验适配器；`v1/` 只做生成类型与用例入参之间的转换 | 不写业务 |
| `internal/app` | 手写构造函数组装，按认证角色装配 | 不引入 DI 框架 |

`pkg/` 为与业务无关的封装，不引用 `internal/`。

## 请求链路

```
otelhttp → requestid → 访问日志 + recover → 安全头 → CORS → i18n
  ┌ 根路径：/health/live、/health/ready、/metrics、/openapi.json、/docs、/.well-known/jwks.json
  └ /api/v1：
     操作查找（未命中：路径存在 405 + Allow，否则 404；未进文档的生成路由一律 404）
     → {service}-ip 限流 → 令牌验证与吊销键 → {service}-user 与共享限流 → 权限 → 近期登录 → 已验证邮箱
     → 请求体（413 / 415 / 400；上传接口流式）→ openapi3filter 校验（422）→ 幂等键 → 事务 → 处理器 → 审计
```

操作表由 `internal/controller/http/validation.json` 建立，每个 operation 必须有 policy（`policy.go`）与处理器，否则启动失败。policy 声明鉴权方式、权限、近期登录、限流规则、请求事务、审计动作与请求体上限。

请求事务在第一条语句时才取连接并 `BEGIN`；2xx 与 4xx 提交，5xx 回滚，响应在提交后发出。按账号、会话计数与只计失败的限流规则由用例经 `usecase.Limits` 检查。

## 进程模型

同一镜像的三个子命令：

| 子命令 | 内容 |
|---|---|
| `serve` | HTTP API；SIGTERM 后 ready 返回 503，在 `SHUTDOWN_TIMEOUT` 内停机 |
| `worker` | River：`mail`、`account`、`default` 队列；每小时的 `maintenance`（过期会话与令牌、审计保留期）与 `account.sweep`（注销补偿扫描），由 leader 执行 |
| `migrate` | 创建 `DATABASE_SCHEMA`，执行 core（与签发服务的 identity）迁移和 River 迁移 |

River 表、goose 版本表与业务表在同一 schema。API 进程用只插入的 River 客户端在请求事务内入队。

## 认证角色

| 角色 | 条件 | 挂载 |
|---|---|---|
| 签发 | `AUTH_SIGNING_KEY` | 约定 §11.1–§11.3、§11.5、JWKS；迁移 identity 表 |
| 验证 | 只设 `AUTH_JWKS_URL` | §11.5 与本服务业务接口；`/openapi.json` 不含身份接口；只迁移 core |
| 临时签发 | 都不设，非生产 | 同签发，进程内生成密钥（重启后令牌失效） |

所有角色在验证令牌后查询 `auth:revoked:sid:{sid}`，签发服务登出、改密、禁用、注销后其他服务立即拒绝该会话。

## `user.deleted` 事件

签发服务的 `account.purge` 任务在清理完用户数据并物理删除用户后，经 `events.Publisher` 发布 CloudEvents 1.0 JSON（`type: user.deleted`，`data: {"userId": ...}`）。验证服务的 worker 经 `events.Subscriber` 订阅，收到后入队 `account.purge`，只运行本服务的清理器（文件、笔记、审计匿名化）。

模板默认的发布器只写日志、订阅器不接收任何事件；传输方式（Redis Streams、Kafka 等）、主题名与 `EVENTS_*` 变量由 monorepo 决定，实现这两个端口后在 `internal/app/worker.go` 与 `identity.go` 替换。消费者组取 `OTEL_SERVICE_NAME`。

## 在 monorepo 中接入 Connect

服务间同步调用建议用 Connect（约定 §10），模板不带 proto 与生成代码。接入方式：

1. proto 放在 monorepo 的 `contracts/proto/`，用 `buf generate` 生成 connect-go 代码到本服务。
2. 在 `internal/controller/` 下新建 `connect/`，实现生成的 handler 接口，调用现有用例。
3. 鉴权复用 `pkg/jwtx.Verifier` 与吊销键检查，写成 connect 拦截器；链路用 otelconnect。
4. 在 `internal/app` 把 connect handler 挂到同一 `http.Server`，h2c 用 `http.Server.Protocols.SetUnencryptedHTTP2(true)`。

## 配置

只从 `internal/config.Config` 读取；启动时一次性解析并列出全部问题。生产额外要求 `DATABASE_URL`、`REDIS_URL`、`CORS_ORIGINS`、`RATE_LIMIT_ENABLED=true`、认证角色变量、`prometheus` 模式下 `METRICS_TOKEN` 或 `METRICS_PORT`、`local` 存储的 `FILE_URL_SECRET`；签发服务另需 `APP_FRONTEND_URL`、`SMTP_URL`、`SMS_PROVIDER=aliyun`。变量清单见 `.env.example` 与 [deploy.md](deploy.md)。
