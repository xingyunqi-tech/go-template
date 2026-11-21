# 架构

## 目录结构

分层沿用 go-clean-template 的 `cmd/` + `internal/` + `pkg/`：

```
cmd/
├── app/                     # 单一二进制：serve、worker、migrate、seed、version 子命令
└── modulegen/               # 模块骨架生成器（make module）
internal/
├── app/                     # 组装：配置 → 基础设施 → repo → usecase → controller；各进程角色的生命周期
├── config/                  # caarlos0/env 解析 + go-playground/validator 校验，按分组拆分
├── entity/                  # 领域对象与领域错误（BusinessError、HTTP 风格错误），不引用 pgx、生成代码、net/http
├── usecase/                 # 业务流程；接口在 contracts.go
│   └── auth、user、session、permission、sms、file、health、alert、audit、accountcleanup、
│       notification
├── repo/                    # 端口在 contracts.go
│   ├── postgres/            # sqlc 查询（queries/ → sqlcgen/）与端口实现、请求事务、River 入队
│   ├── redis/               # 与 nestjs Keyv 兼容的缓存、lock:<key> 分布式锁
│   └── webapi/              # mail（模板渲染与 SMTP）、storage（local / S3）、sms（阿里云）、wechat、alipay、outbound
├── controller/http/         # 全局中间件、Express 路由语义、请求管线、policy.go、/docs-json
│   ├── gen/                 # oapi-codegen 生成（不手改）
│   ├── httperr/             # nestjs HttpException 与 AllExceptionsFilter 翻译规则、业务错误键表、唯一约束表
│   ├── response/            # {code, message, data} 信封
│   ├── reqctx/              # 请求上下文（身份、已校验的请求体与查询）
│   └── v1/                  # gen.StrictServerInterface 实现；view/ 为手写响应视图
├── worker/                  # River workers 与周期任务
├── metrics/                 # Prometheus 指标与 pgx 查询耗时 tracer
├── i18n/                    # nestjs src/i18n 翻译文件（locales/{en,zh}）与 Accept-Language 解析
└── testutil/                # 集成测试夹具（Postgres、Redis、MinIO 容器，表结构快照）
pkg/                         # 与业务无关，不引用 internal/
├── validatorx/              # 按 openapi/validation.json 复刻 nestjs ValidationPipe（含 validator.js 移植）
├── ratelimit/               # GCRA（Redis Lua + 进程内回落）
├── password/                # argon2id / bcrypt，带并发上限
├── jwtx/、msduration/       # HS256 JWT、npm ms 时长解析
├── jsontime/、pagination/   # toISOString 时间格式、nestjs 分页响应
└── httpserver/、postgres/、logger/、tracing/
```

仓库根的其它目录：

| 目录 | 内容 |
|---|---|
| `openapi/` | `nestjs-baseline.json`（`/docs-json` 原文）、`validation.json`、`nest_handlers.json`、`SOURCE`（nestjs 导出的原始产物）；`scope.yaml`、`overlay.yaml`、`oapi-codegen.yaml`、`templates/strict-interface.tmpl`（代码生成输入）；`openapi.go` 把运行时需要的文件 `embed` 进二进制 |
| `db/` | `nestjs-schema.sql`（nestjs 迁移后的表结构快照，nestjs 导出的原始产物），`db.go` 将其 `embed` |
| `migrations/` | goose SQL 迁移，`embed` 进二进制 |
| `seeds/` | `seed.json`（角色、状态、权限、角色权限） |
| `templates/` | `mail/`（从 nestjs 逐字节复制的 `.hbs` 邮件模板）、`module/`（模块生成器模板） |
| `tools/` | 独立 Go 模块：oapi-codegen、goose、mockgen、govulncheck、gotestsum、go-test-coverage 与 `openapigen` |
| `scripts/` | Git 钩子脚本 |
| `tests/` | `contract`、`e2e`、`fixtures/nestjs_mail`，见 [testing.md](testing.md) |
| `docker/`、`Dockerfile` | compose 文件与生产镜像，见 [deploy.md](deploy.md) |

## 分层

```
cmd/app → internal/app → controller/http/v1 → usecase（接口在 usecase/contracts.go）
                                            → repo（端口在 repo/contracts.go）
                                            → entity
```

| 规则 | 检查方式 |
|---|---|
| entity 不引用 pgx、生成代码、net/http | depguard |
| usecase 不引用 controller | depguard |
| controller 只做请求 ↔ usecase 入参、entity → `v1/view` 的转换；响应一律经手写 view 类型输出 | review |
| 配置只从 `config.Config` 读取，业务代码禁止 `os.Getenv` | forbidigo |
| 日志用 `log/slog`，禁止 `fmt.Print*` 与标准库 `log` | forbidigo |
| 构造函数手写，在 `internal/app` 组装，不引入 wire / fx | review |

## 接口声明

HTTP 接口由 oapi-codegen 按 `openapi/scope.yaml` 生成 `gen.StrictServerInterface` 与 chi 路由注册，`v1.Server` 实现它。nestjs 用装饰器表达、而 OpenAPI 文档里没有的行为，在 `internal/controller/http/policy.go` 中按 operationId 声明：

| 字段 | 说明 |
|---|---|
| `Auth` | `AuthNone`、`AuthJWT`、`AuthOptionalJWT`、`AuthRefresh`、`AuthMonitor`（监控令牌） |
| `RBAC` / `Permissions` / `AnyPermission` | 经过权限守卫；`Permissions` 全部满足，`AnyPermission` 时任一满足 |
| `Query` / `Params` | 校验查询串、路径参数所用的 nestjs DTO 名（请求体 DTO 从文档读取） |
| `Throttle` | 路由级限流 `{Limit, TTL}`（TTL 毫秒），对应 `@Throttle` |
| `Tx` | 在请求事务内执行，见 [database.md](database.md#事务) |
| `Audit` | 对应 `@Auditable`，成功后写 `audit_logs`；本期范围内的接口都没有声明 |
| `Upload` | 先执行上传用途守卫、接收 multipart 文件，再校验查询 DTO（只有 `uploadFile`） |
| `Raw` | 响应不包信封（`/metrics`、文件读取与下载） |
| `NoCache` | 2xx 响应加 `Cache-Control: no-cache, no-store, must-revalidate`（健康检查） |

启动时（`httpapi.NewRouter`）检查每个已注册的 operation：缺 policy、policy 用到的守卫或依赖未提供、DTO 不在 `validation.json` 中，任一情况都会让启动失败并列出全部问题。

nestjs 不进文档的接口（`/metrics`、`GET /api/v1/files/private/{id}`、local 驱动的 `GET /api/v1/files/{path}`）以 `RawRoute` 注册，列在 `Undocumented()`。

## 请求链路

全局中间件由外到内（`internal/controller/http/router.go`）：

| 顺序 | 中间件 | 作用 |
|---|---|---|
| 0 | otelhttp | 仅 `OTEL_ENABLED=true` 时包裹，span 名为 `<METHOD> <路由模板>` |
| 1 | `requestContext` | Request ID（入站 `X-Request-Id` 恰有一个且匹配 `^[\w.:-]{1,128}$` 时沿用，否则生成 UUID，回写响应头）与 `Accept-Language` 解析 |
| 2 | `cors` | 生产按 `FRONTEND_DOMAIN` 白名单，其它环境反射来源；任何 `OPTIONS` 请求在此返回 204 |
| 3 | `observe` | HTTP 指标（`path` 为 Express 写法的路由模板，未匹配为 `<unmatched>`，客户端提前断开记 499） |
| 4 | `accessLog` | pino-http 形状的请求日志（敏感字段替换为 `[REDACTED]`）与 panic 兜底 |
| 5 | `secureHeaders` | 与 nestjs helmet 相同的头集合与取值 |
| 6 | `clientInfo` | 按 `TRUST_PROXY` 跳数取客户端 IP |
| 7 | `jsonBody` | 路由匹配前读取 JSON 请求体：超过 256kb → 413，非法 JSON 或顶层为原始值 → 400 |
| 8 | 路径匹配 | 按百分号编码的原始路径匹配：静态段不区分大小写、允许一个末尾斜杠；未匹配与方法不符都是 404 `Cannot <METHOD> <originalUrl>` |
| 9 | 管线 | 见下 |

管线（`pipeline.go`）在生成的处理器前依次执行：路径参数解码（失败 400 `Failed to decode param '<raw>'`）→ 限流（GCRA）→ 鉴权守卫 → RBAC 权限守卫 → `ParseIdPipe`（数字 `:id`，失败 400 `{id: "Invalid id"}`）→ 路径参数 DTO → 请求体 / 查询 DTO 校验（`uploadFile` 为用途守卫 → multipart 接收 → 查询 DTO）。之后进入 strict 处理器，`Tx` 与 `Audit` 以 `StrictMiddlewareFunc` 实现。`HEAD` 命中同路径的 `GET` 处理器。

请求体与查询参数的类型、required 与 class-validator 约束都由 `pkg/validatorx` 按 `openapi/validation.json` 执行（先 `@Transform`，再按装饰器顺序检查全部约束，消息以 `", "` 拼接），不使用 `openapi3filter`。

## 响应与错误

| 情况 | 输出 |
|---|---|
| 成功 | `{"code": 200, "message": "success", "data": ...}`，HTTP 200（`uploadFile` 为 201）；无内容的接口不输出 `data` |
| HTTP 异常 | `{"code": <状态>, "message": <译文>, "data": <对象或 null>}`，翻译规则同 nestjs AllExceptionsFilter（`httperr`） |
| 业务错误 | `message` 为 `common.<errorKey>` 译文，`data` 为 `{...details, errorKey}`，状态码查 nestjs `business-error-keys.ts` 复制来的表 |
| 校验失败 | 422，`message` 为 `common.unprocessableEntity` 译文，`data: {字段: "约束1, 约束2"}` |
| 唯一约束冲突 | 按约束名查 nestjs `UNIQUE_CONSTRAINT_RESPONSES` 复制来的表 |
| 未匹配路由 / 方法不符 | 404，`message` 为 `Cannot <METHOD> <originalUrl>`，`data: null`，不带 `Allow` |
| 未知错误 | 500 `common.internalServerError`；设置 `SENTRY_DSN` 时上报 |

成功响应的写出由 `openapi/templates/strict-interface.tmpl` 覆盖 oapi-codegen 的默认模板实现。JSON 编码不转义 `<>&`，时间为 `pkg/jsontime`（UTC、3 位毫秒、`Z`）。

翻译文件：`internal/i18n/locales/{en,zh}/*.json`（与 nestjs `src/i18n` 相同）。

## 进程模型

同一二进制、同一镜像，按子命令运行不同角色（见 [cli.md](cli.md)）：

| 进程 | 命令 | 说明 |
|---|---|---|
| api | `app serve` | HTTP 服务；连接池上限 `DATABASE_MAX_CONNECTIONS` |
| worker | `app worker` | River workers 与周期任务（leader 选举保证每个周期任务只执行一次）；连接池上限 `WORKER_DATABASE_MAX_CONNECTIONS` |
| migrate | `app migrate` | 一次性：goose 迁移 + River 迁移 |
| seed | `app seed` | 一次性：写入种子数据 |

队列在 Postgres（River）上，与 `REDIS_ENABLED` 无关：邮件、通知、账号清理任务都需要 worker 进程消费。

优雅停机：

- api：收到 SIGINT / SIGTERM 后就绪探针 `/api/v1/health/ready` 立即返回 503，等待 `SHUTDOWN_DRAIN_PERIOD`，再 `http.Server.Shutdown` 等待在途请求（上限 `SHUTDOWN_TIMEOUT`）
- worker：`Stop` 等待在途任务（上限 `SHUTDOWN_TIMEOUT`），超时后取消在途任务并再等 5 秒
- 最后关闭 pgxpool、Redis，flush OTel 与 Sentry

## 基础设施

| 能力 | 位置 | 要点 |
|---|---|---|
| 缓存 | `repo/redis/cache.go` | 键 `cache::<key>`，值 `{"value", "expires"}`（与 Keyv 互通）；`REDIS_ENABLED=false` 时为进程内缓存 |
| 锁 | `repo/redis/lock.go` | `lock:<key>`，`SET NX PX` + Lua 比较 token 释放，单条命令超时 1s；Redis 未启用或命令失败时回落进程内锁 |
| 限流 | `pkg/ratelimit`、`controller/http/throttle.go` | GCRA，键 `throttle:<sha256>:default:gcra`（哈希输入与 nestjs 相同，`<Controller>-<handler>` 取自 `openapi/nest_handlers.json`）；JWT 路由按用户、其余按 IP（IPv6 按 /64）；Redis 命令超时 500ms，出错时回落进程内计数（最多 10000 个键）并告警 |
| 队列 | `repo/postgres/jobs.go`、`worker/` | River，表在 `river` schema；队列 `mail`、`notification`、`account-cleanup`、`scheduler`；请求事务内以 savepoint 执行 `InsertTx`，提交后才可见；默认 3 次尝试，退避 `1s × 2^(n-1)` |
| 定时任务 | `worker/worker.go` | 03:00 UTC 物理删除软删超过 7 天的会话、04:00 UTC 软删除 `readAt` 早于 30 天的已读通知、每小时第 25 分（Asia/Shanghai）账号清理补偿 |
| 日志 | `pkg/logger`、`internal/app/logging.go` | slog；生产 JSON、开发单行文本（`LOG_PRETTY_PRINT`）；记录带 request_id、trace_id / span_id、user_id |
| 指标 | `internal/metrics` | `/metrics`（`METRICS_PATH`，不带前缀、不包信封），名称、标签、桶与 nestjs 一致，样本带 `app=<APP_NAME>` |
| 健康检查 | `usecase/health` | `/api/v1/health`（监控令牌）、`/health/live`、`/health/ready`、`/api/health`，形状同 @nestjs/terminus |
| 链路 / 错误上报 | `internal/app/observability.go`、`pkg/tracing` | `OTEL_ENABLED`（OTLP/HTTP，`OTEL_EXPORTER_OTLP_ENDPOINT`）覆盖 HTTP 入口、pgx、go-redis；`SENTRY_DSN` 设置时上报 5xx 与任务错误 |
| 审计 | `controller/http/audit.go`、`usecase/audit` | policy 声明 `Audit` 的接口成功后写 `audit_logs`，敏感字段脱敏 |
| 告警 | `usecase/alert` | 写 `admin_alert`，按 dedupKey 去重，给 admin 角色用户发站内通知 |
| 账号清理 | `usecase/accountcleanup`、`worker/` | 注销后同事务入队 `clean-account`，按 order 执行各模块清理器；`POST /api/v1/admin/users/{id}/cleanup` 重新触发 |
| 邮件 | `repo/webapi/mail`、`worker/` | 请求内入 `mail` 队列，worker 按请求语言渲染并发送；未设置 `MAIL_HOST` 时只写日志 |
| 文件 | `usecase/file`、`repo/webapi/storage`、`controller/http/upload.go` | `FILE_DRIVER`：`local`（`FILE_LOCAL_ROOT`，默认 `files`）、`s3`、`s3-presigned` |

## 配置

`config.Load()` 在启动时一次性解析全部环境变量并校验，所有问题汇总在一条错误中后退出。变量名与 nestjs `env-example-relational` 一致，全部变量见 `.env.example`。进程只读环境变量，不读 `.env` 文件；`make dev`、`make worker`、`make migrate`、`make seed` 在调用前加载工作目录的 `.env`。空字符串与未设置等同（取默认值）。

生产环境（`APP_ENV=production`）额外要求：四个 JWT 密钥均 ≥32 字符且互不相同、`REDIS_ENABLED=true`、`MAIL_HOST` / `MAIL_DEFAULT_EMAIL` / `MAIL_DEFAULT_NAME` 必填。`METRICS_TOKEN` 设置时至少 16 个字符；`FILE_DRIVER` 不是 `local` 时 `ACCESS_KEY_ID`、`SECRET_ACCESS_KEY`、`AWS_DEFAULT_S3_BUCKET`、`AWS_S3_REGION` 必填。

与 nestjs 的行为差异见 [contract.md](contract.md#与-nestjs-的行为差异)。
