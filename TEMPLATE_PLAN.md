# go-template 方案 v2

编写日期：2026-11-17。版本数据为当日查询结果，来源见文末。

## 0. 目标

| # | 目标 |
|---|---|
| 1 | 独立的 Go 后端模板：使用 Go 当前最新稳定技术栈与惯用写法 |
| 2 | 对外接口遵守《跨语言平台约定》`docs/platform-conventions.md`（下称「约定」），本仓库导出自己的 OpenAPI 3.1 文档供前端生成客户端；不绑定任何前端模板的具体业务 |
| 3 | 只提供基础能力（§2），外加一个 CRUD 示例模块，业务由使用者在此之上添加 |
| 4 | 可作为同一 monorepo 中的一个服务，与 Python / Java 模板共存（约定 §10） |

约定文件三仓库逐字节相同，本仓库只引用、不修改。本方案与约定冲突时以约定为准。

---

## 1. 现状（v1，已 push，M0–M5 完成）

| 项 | 现状 |
|---|---|
| 目标 | 与 nestjs-template API 契约逐字节一致 |
| 接口 | spec-first：输入为 nestjs 导出的 `openapi/nestjs-baseline.json`（OpenAPI 3.0），`/docs-json` 原样输出；`scope.yaml` 选定实现范围；`overlay.yaml` + 覆盖 oapi-codegen strict 模板（成功一律 HTTP 200） |
| 校验 | `pkg/validatorx` 按 `openapi/validation.json` 复刻 class-validator（含 validator.js 移植），约 4516 条 oracle 用例为门禁 |
| 路由 | 自写 Express 语义（不区分大小写、忽略尾斜杠、方法不符 404、原始路径匹配） |
| 认证 | HS256 双 JWT、客户端 MD5、bcrypt / argon2id、邮箱 / 手机密码 / 短信 / 微信 / 支付宝（自写客户端） |
| 数据 | 18 张表，camelCase 列、int 主键、`timestamp` 无时区；River 在 `river` schema；启动时按 `db/nestjs-schema.sql` 检查结构（`DB_SCHEMA_OWNER`） |
| 模块 | auth、users、files、health、audit、alert、account-cleanup，及通用业务模块 config、regions、notifications、addresses、favorites、feedbacks、consents、app-profile |
| 基础设施 | slog、Prometheus（nestjs 指标名，worker 独立端口 `WORKER_METRICS_PORT`）、OTel、Sentry、Redis 缓存 / 锁（Keyv 格式）、GCRA 限流（nestjs 键格式）、River 队列与周期任务、go-mail（Handlebars 模板移植） |
| 测试 | 单元、集成（testcontainers + 克隆库）、e2e、契约（nestjs 基线与 oracle）、parity（对 nestjs 黑盒比对） |
| CI | check（含 parity job）/ deploy / cleanup-registry / drift（每周对 nestjs 漂移检查） |
| 共享产物 | `openapi/{nestjs-baseline.json,validation.json,SOURCE,nest_handlers.json}`、`db/nestjs-schema.sql`、`scripts/openapi-sync.sh` 等，与 fastapi-temp、springboot-template 逐字节相同 |

v1 中与 nestjs 无关、可直接沿用的部分：分层与组装方式、chi + oapi-codegen strict、pgx + sqlc + goose、River、testcontainers 克隆库夹具、GCRA Lua 骨架、Sentry 初始化、微信 / 支付宝 / 阿里云短信 HTTP 客户端、工程配置（tools/go.mod、golangci-lint、prek、Renovate、Dockerfile、compose、manage.sh、deploy / cleanup-registry workflow）。

---

## 2. 范围

### 2.1 保留 / 实现

| 类别 | 内容 |
|---|---|
| 平台 | 配置、结构化日志（脱敏）、错误信封、i18n（zh / en）、Request ID、CORS、安全头、请求体限制、限流、幂等键、健康检查、指标（Prometheus 拉取或 OTLP 推送）、OTel、Sentry、OpenAPI 导出与 `/docs` |
| 认证（签发角色） | 约定 §11.1–§11.3：邮箱 + 密码、手机号 + 短信验证码、JWT（EdDSA）+ JWKS、不透明 refresh 轮换、会话管理、改密、当前用户、用户与角色管理 |
| 可选 provider | 微信小程序、微信 App、支付宝登录与微信手机号绑定；默认关闭，配置凭据后启用（§6.4） |
| 文件 | 约定 §11.5（local / S3 兼容存储） |
| 基础服务 | 邮件、队列、定时任务、审计日志、账号注销 |
| 工具 | 模块生成器；CRUD 示例模块 `notes` |
| 共存 | 签发 / 验证两种认证角色、独占 schema、`user.deleted` 事件（约定 §10） |

### 2.2 删除

| 模块 | 理由 |
|---|---|
| regions、addresses、favorites、feedbacks、consents、app-profile、notifications | 通用业务模块，不属于基础能力 |
| config（`appconfig`，`config` 表） | 代码中登记的是 AI 配额、刷题、协议正文、小程序站点信息等业务键值，属于业务配置；运行配置一律走环境变量 |
| 告警（`alert`，`admin_alert` 表） | 依赖站内通知与业务配置；错误上报由 Sentry、阈值告警由 Prometheus 告警规则承担 |

### 2.3 账号注销：保留

保留理由：应用商店上架与个人信息保护要求提供自助注销，属于基础能力。形态为「软删 + 吊销会话 + River 延时任务清理 + `user.deleted` 事件」，不再使用 `account_cleanup` 表（§8）。

### 2.4 本期不做

| 项 | 说明 |
|---|---|
| 对象存储直传（预签名上传） | 约定 §11.5 列为各服务自行扩展；需要「预签名 POST policy + 上传确认」两步才能限制大小并取得 sha256，留到后续（§16-6） |
| Connect / gRPC 服务端 | 约定 §10 为建议；`docs/architecture.md` 说明在 monorepo 中接入的方式，模板不带 proto 与生成代码（§16-7） |

---

## 3. 技术栈

版本均为 2026-11-17 查询的最新稳定版。

| 类别 | 选型 | 版本 | 说明 / 相对 v1 的变更理由 |
|---|---|---|---|
| 语言 | Go | 1.27.4 | 保留；`go 1.27.0` + `toolchain go1.27.4`；标准库 `uuid.NewV7` 生成主键 |
| HTTP | `net/http` + go-chi/chi v5 | 5.5.0 | 保留；改用 chi 默认语义（区分大小写、尾斜杠不归一），删除自写 Express 匹配 |
| OpenAPI | spec-first：oapi-codegen v2（chi-server + strict-server）+ runtime | 2.8.2 / 1.9.0 | 保留生成方式，输入改为本仓库维护的 3.1 spec；理由见 §3.1 |
| 请求校验 | kin-openapi `openapi3filter.ValidateRequest`，自写中间件调用 | 0.153.0 | 替换 `pkg/validatorx`：spec 是约束的唯一来源；不用 nethttp-middleware（内部 gorillamux 二次匹配，无法插在鉴权之后） |
| 业务校验 | usecase 返回 `entity.ValidationError` | — | `exists`、`contains` 等规则（§5.1） |
| 配置 | caarlos0/env v11 + go-playground/validator v10 | 11.4.2 / 10.30.6 | 保留；validator 只用于配置结构体 |
| 数据库 | PostgreSQL | 18.7 | 升级镜像标签；18 起有内置 `uuidv7()`，用作列默认值 |
| 驱动 | pgx v5（pgxpool） | 5.13.0 | 保留；`DATABASE_URL` 为 libpq URI，pgx 直接解析 |
| SQL | sqlc | 1.31.1 | 保留；列名改 snake_case 后不再需要引号；`uuid` 列以 `overrides` 映射到标准库 `uuid.UUID` |
| 迁移 | goose v3（库方式嵌入） | 3.30.1 | 保留；两个 provider（§7.3） |
| 缓存 / 锁 / 限流 | Redis + go-redis v9 | 8.10.3 / 9.23.0 | 保留；键格式按约定 §7、§10 |
| 队列 + 定时 | River + rivercontrib/otelriver | 0.51.2 / 0.12.0 | 保留 River；表从独立 `river` schema 移到服务 schema（约定 §10）；otelriver 中间件产出任务指标与链路 |
| JWT / JWK | lestrrat-go/jwx v3 | 3.4.0 | 替换 golang-jwt：一个库覆盖 EdDSA 签发 / 验证、RFC 7638 `kid`、JWKS 发布与远程 JWKS 缓存 |
| 密码 | golang.org/x/crypto/argon2 | 0.59.0 | 保留 argon2id；删除 MD5 预哈希与 bcrypt 兼容（约定 §5.4） |
| i18n | nicksnyder/go-i18n v2 + golang.org/x/text/language | 2.7.0 / 0.44.0 | 替换 nestjs 翻译文件加载器：标准消息文件与复数规则，`language.Matcher` 实现 q 值与区域宽松匹配 |
| 日志 | `log/slog` + otelslog | 随 Go / 0.21.0 | 保留；`ReplaceAttr` 输出约定 §8 字段名并脱敏 |
| 指标 | OTel metrics SDK；`OTEL_METRICS_EXPORTER=prometheus`（OTel Prometheus exporter + client_golang `promhttp`）或 `otlp` | 1.47.0 / 0.69.0 / 1.25.0 | 替换 client_golang 自定义指标名：otelhttp 按 HTTP 稳定语义约定产出 `http_server_request_duration_seconds`；client_golang 仍为直接依赖（exporter 不带 HTTP handler） |
| 链路 | OTel（otelhttp、autoexport、otelpgx、redisotel、otelriver） | 1.47.0 / 0.72.0 / 0.72.0 / 0.13.0 / 9.23.0 / 0.12.0 | 保留；`pkg/tracing` 改为全部由标准 `OTEL_*` 变量驱动（§8） |
| 错误上报 | sentry-go | 0.50.0 | 保留 |
| 邮件 | wneessen/go-mail + `html/template` / `text/template` | 0.8.2 | 保留库；模板改为 Go 模板，文案走 i18n |
| 对象存储 | aws-sdk-go-v2/service/s3 | 1.115.0 | 保留 |
| 微信 / 支付宝 / 短信 | 自写 HTTP 客户端（`internal/repo/webapi/{wechat,alipay,sms}`） | — | 保留 v1 实现；支付宝新增 `ALIPAY_PUBLIC_KEY` 用于验证响应签名 |
| 文件类型识别 | 标准库 `http.DetectContentType` | 随 Go | 约定 §11.5 按内容识别；覆盖 jpeg / png / webp / pdf |
| API 文档 UI | Scalar（`@scalar/api-reference`，CDN） | 1.80.0 | 升级 |
| 测试 | go test + testify + go.uber.org/mock + gotestsum + testcontainers-go | 1.12.1 / 0.6.0 / 1.13.0 / 0.45.0 | 保留 |
| 覆盖率 / 漏洞 | go-test-coverage / govulncheck | 2.23.0 / 1.10.0 | 升级 govulncheck |
| Lint | golangci-lint v2（gofumpt、gci） | 2.15.0 | 保留 |
| Git 钩子 | prek | 0.5.10 | 保留 |
| 依赖更新 | Renovate | — | 保留 |

删除的依赖：`dlclark/regexp2`（validator.js 移植用）、`golang-jwt/jwt/v5`。`tools/go.mod` 中的 kin-openapi 0.142.0 统一升到 0.153.0。

UUID 类型：标准库 `uuid.UUID`（`[16]byte`）没有 `sql.Scanner` / `driver.Valuer`，依赖 pgx 对 `[16]byte` 的原生编码；oapi-codegen 的 `format: uuid` 默认生成 google/uuid，通过 `output-options.type-mapping` 改为标准库类型。B2 的集成测试确认 pgx 读写（标量、NULL、数组）与 JSON 编解码。

### 3.1 OpenAPI 方式：spec-first

| 方案 | 结论 | 理由 |
|---|---|---|
| **spec-first**：`openapi/src/` 维护 3.1 spec，bundle 为 `openapi/openapi.json`，oapi-codegen 生成类型、strict 接口与 chi 注册 | **采用** | 约定 §2.3 的 `rule` 可由 JSON Schema 关键字直接得到；spec 是评审对象，前端与其它语言服务可直接阅读；沿用 v1 已验证的生成链路 |
| code-first：Huma v2（2.41.0） | 不采用 | 校验错误只有消息与位置，没有约束名，`rule` 需从消息反推 |

做法：

- `openapi/src/openapi.yaml`（根文档，含 `bearerAuth`）+ `openapi/src/paths/<module>.yaml` + `openapi/src/schemas/<module>.yaml`；公共部分 `openapi/src/common.yaml`：`ErrorResponse`、`ValidationErrorResponse`（422 共用：`data` 为字段列表或 null）、分页与 `Idempotency-Key` 参数、公共错误响应。除根文档外，各文件只含 `paths` 与 `components`，`$ref` 一律为文档内引用
- 成功响应在源文件中写 `x-envelope: true` + 数据 schema，`tools/openapigen` 合并各文件（同名 path 或 component 报错）后展开为约定 §2.1 的信封 schema（`<Name>Envelope`；数组 `<Name>ListEnvelope`；`type: 'null'` 为 `NullEnvelope`），并为每个 `/api` 操作补上未声明的 429（`TooManyRequests`）与 503（`ServiceUnavailable`），输出排序稳定的 `openapi/openapi.json`（入库，保留 `components` 引用）
- bundler 另输出全内联、无 `$ref` 的校验文档 `internal/controller/http/validation.json`（生成文件，embed），供 `openapi3filter` 使用，使所有 schema 走同一个校验器、产生同一种错误结构（§17）
- `make generate` = bundle → oapi-codegen → sqlc → mockgen；生成结果入库，`scripts/check-generated.sh` 的列表包含 `openapi/openapi.json` 与校验文档
- 运行时 `GET /openapi.json` 用 `embed` 返回 `openapi/openapi.json`，`GET /docs` 为 Scalar；`DOCS_ENABLED` 控制（约定 §1）
- 不覆盖 oapi-codegen 模板；HTTP 状态码按 spec 声明（约定 §2.1），handler 返回生成的 `<Op><Status>JSONResponse`

---

## 4. 目录结构与分层

```
go-template/
├── cmd/app/                    # 子命令：serve / worker / migrate / seed / createsuperuser / keys / healthcheck / version
├── cmd/modulegen/              # 模块生成器
├── internal/
│   ├── app/                    # 组装与生命周期；按认证角色装配模块
│   ├── config/                 # 环境变量结构体与校验
│   ├── entity/                 # 领域对象、错误码表、角色与权限码
│   ├── usecase/
│   │   ├── contracts.go        # usecase 接口（新建）
│   │   └── {auth,user,session,sms,file,audit,account,health,note}/
│   ├── repo/
│   │   ├── contracts.go
│   │   ├── postgres/{queries,sqlcgen}/
│   │   ├── redis/              # 缓存、锁、短信验证码、幂等记录、吊销键、宽限期令牌对
│   │   └── webapi/{mail,storage,sms,wechat,alipay,outbound}/
│   ├── controller/http/        # 中间件、操作表、错误写出、policy、docs；gen/ 生成；v1/ 实现
│   ├── events/                 # user.deleted 发布与订阅端口
│   ├── i18n/                   # go-i18n bundle 与 locales/{zh,en}.json
│   ├── worker/                 # River workers 与周期任务
│   └── testutil/
├── pkg/                        # 与业务无关：httpserver、postgres、logger、tracing、jwtx、password、ratelimit、jsontime、pagination、phone
├── migrations/{core,identity}/ # goose SQL（§7.3）
├── openapi/{src/,openapi.json,oapi-codegen.yaml,openapi.go}
├── templates/{mail,module}/
├── seeds/
├── tests/{contract,e2e}/
├── tools/{go.mod,openapigen}/
├── docker/  deploy/  scripts/  docs/
└── Makefile  Dockerfile  sqlc.yaml
```

| 规则 | 说明 |
|---|---|
| 依赖方向 | controller → usecase（接口在 `usecase/contracts.go`）→ repo（端口在 `repo/contracts.go`）→ entity；depguard 检查 |
| entity | 不引用 pgx、gen、net/http |
| controller | 只做生成类型 ↔ usecase 入参 / entity 的转换，不写业务 |
| policy | `controller/http/policy.go` 按 operationId 声明：鉴权（public / jwt / optional）、权限、近期登录、已验证邮箱、限流规则、事务、审计动作、请求体上限；spec 中每个 operation 必须有 policy，启动时校验 |
| 配置 | 只从 `config.Config` 读取，禁止 `os.Getenv`（forbidigo） |
| 依赖注入 | 手写构造函数，在 `internal/app` 组装 |

---

## 5. API 约定落地

| 约定章节 | 落地方式 |
|---|---|
| §1 路径与文档 | 业务路由挂在 `/api/v1` 子路由；`/health/*`、`/metrics`（`OTEL_METRICS_EXPORTER=prometheus` 时）、`/openapi.json`、`/docs`、`/.well-known/jwks.json` 在根路径；chi `NotFound` 输出 404 信封，`MethodNotAllowed` 输出 405 `common.methodNotAllowed` 并带 `Allow`；operationId 规则由契约测试检查 |
| §2.1 成功信封 | spec 中由 bundler 展开；`response.OK` 常量 `code: 0`、`message: "ok"` |
| §2.2 / §2.4 错误 | `entity.Error{Key, Data}`；errorKey → HTTP 状态与 `code` 的码表在 `internal/entity/errors.go`，单测与约定 §2.4 逐条比对；`message` 由 i18n 按 errorKey 翻译；401 写 `WWW-Authenticate: Bearer`，携带了无效 / 过期令牌时追加 `error="invalid_token"`；第三方调用失败 502 `common.badGateway`；未捕获错误 500 且不含异常信息 |
| §2.3 校验错误 | 见 §5.1 |
| §3 分页 | `pkg/pagination`：页码与游标两种参数 / 响应 schema 在 `common.yaml`；游标为 base64url 编码的 `(排序键, id)`，解码失败 422 `query.cursor`；`sort` 白名单由各接口声明，非法字段 422 `query.sort` |
| §4 数据类型 | 主键 UUIDv7；`pkg/jsontime` 输出毫秒 UTC `Z`、输入接受任意偏移；邮箱去空白转小写；手机号规范化为 E.164（`pkg/phone`）；可空字段输出 `null`（spec `type: [T, "null"]`） |
| §5 认证 | 见 §6 |
| §6 请求头 | 中间件链见 §5.2；CORS 自写中间件（§16-3）；`Idempotency-Key` 见 §8 |
| §7 限流 | 见 §8「限流」 |
| §8 日志与可观测性 | 见 §8 |
| §9 环境变量 | 见 §10 |
| §10 共存 | 见 §6.5、§7、§8「账号注销」「事件」 |
| §11 身份与文件接口 | 见 §6.3–§6.5、§8「文件」；spec 源文件按 §11 编写，契约测试逐项核对 |

### 5.1 校验

| 来源 | 做法 |
|---|---|
| spec 约束 | 中间件以 `openapi3filter.ValidateRequest`（`MultiError: true`、`AuthenticationFunc: openapi3filter.NoopAuthenticationFunc`）校验路径、查询参数与 JSON 请求体，错误适配器把每个 `SchemaError` / `RequestError` 转为约定 §2.3 的项；每个失败字段至少一项，请求体解码阶段的类型错误只报第一个 |
| 格式 | kin-openapi 默认只注册 byte / date / date-time / int32 / int64；启动时用 `openapi3.DefineStringFormatValidator` 注册 `email`、`uri`、`uuid`（`openapi3.FormatOfStringForUUIDOfRFC9562`），未注册的格式会被放行 |
| 长度 | `minLength` / `maxLength` 按 Unicode 码点计数（约定 §2.3），B2 单测用多字节与 emoji 字符确认 kin-openapi 的计数方式 |
| 业务规则 | usecase 返回 `entity.ValidationError{Field, Rule, Params}`，由错误写出统一为 422 `common.validationFailed`；`exists`：`setUserRoles` 的未知角色代码、`updateMe` 的 `avatarFileId`（不存在、非本人或用途不是 `avatar`）；`contains`：`setUserRoles` 缺少 `user`（`field: roles`，`params.value: "user"`） |
| 请求体 | 非上传接口用 `http.MaxBytesReader(BODY_LIMIT)`，超限 413 `common.payloadTooLarge`；JSON 非法 400 `common.malformedJson`；`Content-Type` 不在该 operation 的 `requestBody.content` 中 415 `common.unsupportedMediaType` |
| 上传接口 | §8「文件」 |

关键字映射：

| JSON Schema 关键字 | `rule` | `params` |
|---|---|---|
| `required` | `required` | — |
| `type`（含 null） | `type` | `{type}` |
| `format: email` / `uri` / `uuid` | `email` / `url` / `uuid` | — |
| 其它 `format`（`date`、`date-time` 等） | `format` | `{format}` |
| `minimum` / `exclusiveMinimum` | `min` | `{min}`；exclusive 时另加 `exclusive: true` |
| `maximum` / `exclusiveMaximum` | `max` | `{max}`；同上 |
| `minLength` / `maxLength` | `minLength` / `maxLength` | `{minLength}` / `{maxLength}` |
| `minItems` / `maxItems` | `minItems` / `maxItems` | 同名参数 |
| `pattern` | `pattern` | — |
| `enum` / `const` | `enum` | `{values}` |

适配器细节：

- 同时处理两种错误结构：3.0 校验器的 `*openapi3.SchemaError`（`SchemaField` 给出关键字、`JSONPointer()` 给出路径），以及 3.1 JSON Schema 2020-12 校验器的错误树（`Origin` 下的 `MultiError` 需递归展开）
- `required` 的 JSON Pointer 指向父对象，缺失字段名从 santhosh-tekuri/jsonschema/v6 的 `kind.Required.Missing` 取，逐个生成 `field`
- `field` 由 JSON Pointer 转为点号 + 下标；查询 / 路径参数前缀 `query.` / `path.`；消息键 `validation.<rule>`
- B2 的表驱动单测覆盖映射表每一行与三种格式，并分别对「含 `$ref` 的文档」与「全内联文档」各跑一遍

### 5.2 请求处理顺序

```
otelhttp → requestid → 访问日志 + recover → 安全头 → CORS → i18n
  ┌ 根路径（health、metrics、openapi.json、docs、JWKS）：直接处理
  └ /api/v1 子路由：
     → 操作查找：chi Mux.Find(临时 rctx, method, path) 得到路由模板与路径参数
       → 启动时由 spec 构建的 (method, 模板) → 操作表；设置 r.Pattern 供 otelhttp 取 http.route
       （未命中：路径存在则 405 + Allow，否则 404）
     → {service}-ip 限流（IP）
     → 令牌验证：public 不读 Authorization；jwt 失败 401；optional 失败按匿名继续（验证错误留在上下文，供要求登录的分支作答）
     → {service}-user 限流（已登录）与 policy 声明的共享规则（约定 §11.6）
     → 权限 → 近期登录 → 已验证邮箱
     → 请求体上限 → ValidateRequest（路径参数取自临时 rctx；422 / 415）→ 幂等键 → 事务
     → chi 路由与生成的参数绑定 → handler → 审计
```

- operationId、policy、校验用 `routers.Route` 由同一张表给出；chi 的参数绑定在校验之后，不会先于鉴权报错
- 需要先查库或只计失败的规则（`auth-refresh` 的会话主体、`auth-login-account`、`auth-mail-account`、`auth-password-verify`）由 usecase 调用 `ratelimit.Limiter`，不在中间件中（§8「限流」）
- 同一请求命中多条规则时，`X-RateLimit-*` 取剩余最少的一条（约定 §7）

---

## 6. 认证与权限

### 6.1 令牌与会话

按约定 §5.1–§5.3 实现，本节只列实现选择：

| 项 | 实现 |
|---|---|
| 签名 | jwx v3；`AUTH_SIGNING_KEY` 为 Ed25519 私钥 PEM，`AUTH_VERIFY_KEYS` 为轮换中的旧公钥；JWS 头 `alg: EdDSA`、`kid`（RFC 7638，SHA-256，base64url）必填 |
| claims | 约定 §5.1 全部 claim；`email_verified` 取签发时状态，`auth_time` 为登录时间、刷新时从会话行读取原样写入 |
| JWKS | `GET /.well-known/jwks.json` 发布当前公钥与 `AUTH_VERIFY_KEYS`，每个 JWK 为 `kty: OKP`、`crv: Ed25519`，带 `kid`、`alg: EdDSA`、`use: sig`，`Cache-Control: max-age=300` |
| 验证 | 签发角色用本地公钥集；验证角色用 `jwk.Cache` 注册 `AUTH_JWKS_URL`，注册时不等待首次拉取（启动与 ready 不依赖签发服务），未知 `kid` 触发刷新且两次刷新间隔 ≥ 30 秒；只接受头部 `alg: EdDSA` 且 `kid` 指向 OKP / Ed25519 JWK；校验 `iss` / `aud` / `exp`，时钟偏差 30 秒；过期返回 `auth.tokenExpired`，其他失败 `common.unauthorized`。约定 §5.1 的 RFC 9864 名称切换发生时，验证器同时接受 `EdDSA` 与 `Ed25519` |
| 吊销 | 写入时机与 TTL 按约定 §5.1；吊销键在请求事务提交前写出，改密、重置等请求因 5xx 回滚时已写出的键保留到 TTL（会话需重新登录，接受），由 `session` usecase 统一调用 `Revoker.Revoke(sid…)`；所有角色在验证后查询 `auth:revoked:sid:{sid}`；Redis 出错 503 `common.serviceUnavailable`；未配置 `REDIS_URL` 时用进程内集合 |
| 会话寿命 | `session.expires_at` = 登录时间 + `AUTH_REFRESH_TTL`，轮换不延长（约定 §5.2） |
| refresh 存储 | `session.refresh_hash` 为当前令牌 SHA-256；已轮换的哈希写入 `session_refresh_history`，保留到会话过期 |
| 宽限期暂存 | 键 `{service}:auth:refresh:grace:{旧哈希}`，TTL 30 秒；值为新令牌对，AES-256-GCM 加密，密钥 = HKDF-SHA256(旧 refresh 明文, info=`refresh-grace`)，服务端不持有额外密钥，只有持旧令牌者能解密；未配置 `REDIS_URL` 时存进程内带过期的 map（单实例） |
| 轮换 | 单事务：`SELECT … FOR UPDATE` 锁会话行 → 生成新令牌对 → 旧哈希写历史 → 写宽限期暂存 → 提交；暂存写失败则回滚并 503。并发请求在行锁释放后查到旧哈希已在历史中且在 30 秒内，读取暂存返回同一对 |
| 重用与无效 | 历史中且超过 30 秒 → 吊销会话、写吊销键，401 `auth.refreshTokenReused`；不存在、会话过期或已吊销 → 401 `auth.refreshTokenInvalid` |
| 会话 | 接口按约定 §11.2；`last_active_at` 在刷新时更新；`current` 按 access 的 `sid` 判断 |

### 6.2 密码与敏感操作

| 项 | 实现 |
|---|---|
| 规则 | spec 中 `minLength: 8`、`maxLength: 128`（约定 §5.4，按码点计数） |
| 哈希 | argon2id 参数与 PHC 字符串按约定 §5.4；登录成功后参数不同则重哈希；`PASSWORD_HASH_CONCURRENCY` 信号量 |
| 防时序 | 启动时计算一个固定的假哈希；账号不存在或无密码时对它执行一次校验 |
| 近期登录 | policy `Reauth: true`（`deleteMe`）检查 access 的 `auth_time` ≤ 10 分钟；`changePassword` 在用户无密码时由 usecase 检查；失败 403 `auth.reauthRequired` |
| 改密失败计数 | `changePassword` 校验 `currentPassword` 前对 `auth-password-verify` 只读检查，失败时计数，成功时清除（§8「限流」） |

### 6.3 认证与当前用户接口

路径、operationId、请求与响应字段按约定 §11.1、§11.2、§11.4，spec 源文件 `openapi/src/paths/{auth,me}.yaml` 照此编写，本节只列实现选择：

| 项 | 实现 |
|---|---|
| 注册 | 新用户 `status=active`、`email_verified_at` 为空、角色 `user`；同事务入队验证邮件；邮件链接以 `APP_FRONTEND_URL` 为基址。注册与登录的 policy 不开请求事务：argon2 在事务外计算 / 校验，之后由用例开短事务写用户、会话与任务。请求事务在第一条语句时才取连接并 `BEGIN`，`resetPassword`、`createUser` 先哈希再访问数据库，`changePassword` 在事务外读出旧哈希、两次 argon2 之后才写库，等待哈希槽位时都不占连接；`DB.InTx` 在 PostgreSQL 回滚失败事务时返回 `ErrRolledBack` |
| 已验证邮箱 | policy `VerifiedEmail: true` 按 access 的 `email_verified` claim 判断，签发与验证角色相同；模板内暂无接口使用，由业务模块按需声明 |
| 禁用账号 | 凭据正确且 `status=disabled` → 403 `auth.accountDisabled`；刷新时同样拒绝 |
| 一次性令牌 | `user_token` 存 SHA-256；同类新令牌签发时旧令牌标记失效（签发时锁用户行，部分唯一索引 `user_token_user_id_purpose_key` 保证同一用途只有一枚有效令牌）；有效期按约定 §11.1；失败 422 `auth.verificationTokenInvalid`。令牌由 worker 在执行 mail 任务时签发：入队只带 `(kind, user_id, locale)`，明文令牌只出现在 worker 内存与邮件中，不进 `river_job`；重试会签发新令牌并使旧令牌失效 |
| 短信验证码 | Redis `{service}:sms:code:{scene}:{phone}` 存哈希与尝试次数（5 次错误后删除）；`{service}:sms:cooldown:{phone}`（60 秒）、`{service}:sms:daily:{phone}:{北京时间日期}`（10 次，存活到北京时间次日零点）超出 429 `auth.sendTooFrequent` + `Retry-After`；`{service}:sms:fail:{phone}` 1 小时窗口累计 10 次失败后写 `{service}:sms:lock:{phone}`（1 小时；第 10 次仍为 422，之后 429），锁定期间校验与发送都返回 429 `auth.verificationLocked` + `Retry-After`；三组判定各为一个 Lua 脚本，未配置 `REDIS_URL` 时进程内实现；短信网关调用不随请求取消；失败 502 `common.badGateway`，只在网关明确拒绝或连接未建立（`repo.ErrSMSNotSent`）时撤回本次发送（删除验证码与冷却、日计数减一），超时、读响应失败等可能已发出的情况保留冷却与计数；写验证码失败 503 并撤回；`SMS_PROVIDER=log` 把验证码写进日志，仅供本地开发 |
| `sendSmsCode` 鉴权 | policy 为 optional；`scene=bind_phone` 时要求已登录：未带令牌 401 `common.unauthorized`，令牌无效或过期时返回其验证错误（过期为 401 `auth.tokenExpired`）；optional 的无效令牌仍按匿名处理，验证错误记在请求上下文 |
| 短信登录 | 未注册手机号自动注册（约定 §11.1）；policy 不开请求事务，用例开短事务；并发首登撞上手机号唯一约束时重试一次；签发会话前对用户行 `FOR UPDATE`，锁到时已被注销或已换绑其他手机号则按未注册处理 |
| `hasPassword` | `password_hash IS NOT NULL`；`changePassword` 已有密码时 `currentPassword` 必填 |
| 唯一冲突 | 唯一约束冲突翻译为 `user.emailTaken` / `user.phoneTaken` |
| 登录与找回限流 | `loginWithEmail` / `loginWithSms` 在校验前只读检查 `auth-login-account`，失败计数、成功清除；`forgotPassword` 对 `auth-mail-account` 正常计数（主体均为 `a:{…}`） |
| 账号注销 | `deleteMe`、`deleteUser` 走同一流程（§8「账号注销」） |

不提供手机号 + 密码登录。

### 6.4 可选 provider

统一入口 `listAuthProviders`、`loginWithProvider`，微信手机号绑定为 `bindPhoneWithWechat`（约定 §11.1、§11.2）。

| provider | 启用条件 | 换取身份 |
|---|---|---|
| `wechat_miniapp` | `WECHAT_MINIAPP_APP_ID` + `WECHAT_MINIAPP_APP_SECRET` | jscode2session → openid / unionid；`bindPhoneWithWechat` 调手机号接口 |
| `wechat_app` | `WECHAT_APP_APP_ID` + `WECHAT_APP_APP_SECRET` | OAuth access_token → openid / unionid |
| `alipay` | `ALIPAY_APP_ID` + `ALIPAY_PRIVATE_KEY` + `ALIPAY_PUBLIC_KEY`（新增变量） | `alipay.system.oauth.token` → user_id / open_id |

- `internal/usecase/auth/provider.go` 定义 `Provider` 接口（`ID()`、`Exchange(ctx, code)` 返回 `entity.ExternalIdentity`），启动时只注册凭据齐全的实现；只设置了一部分凭据时启动失败
- 第三方基址变量 `WECHAT_API_BASE`、`ALIPAY_GATEWAY`（与 `ALIYUN_SMS_ENDPOINT`）默认指向生产地址，集成测试用它们指向本地假服务器
- 支付宝只用 RSA2，请求以表单 POST；成功响应必须带可由 `ALIPAY_PUBLIC_KEY` 验证的 `sign`，否则 502；身份取 `open_id`，没有时取 `user_id`；sub_code 以 `isv.code-` 开头为 422
- 微信 errcode 40029 / 40163 为 422，其余与 HTTP 失败为 502；小程序 access_token（stable_token）缓存在 `repo.Cache`；失效时同一进程内只有一个请求重取（多副本各自可能重取一次），拿锁后先查缓存，普通重取仍返回失效 token 才 `force_refresh`；`wechat_app` 只换 openid / unionid，不读用户资料
- `{provider}` 在 spec 中为普通字符串，不用 enum；未知或未注册返回 404 `auth.providerDisabled`
- 第三方返回 code 无效 / 过期 → 422 `auth.providerCodeInvalid`；网络错误、5xx 或其它错误码 → 502 `common.badGateway`
- 身份匹配：微信行保存 `(provider, openid)` 与 `union_id`；登录时按 `(provider, openid)` 或同一 `union_id`（任一微信 provider）命中即为同一用户，命中后回填缺失的 `union_id` 或补建本 provider 的行；支付宝为 `(alipay, open_id 或 user_id)`；首次登录自动建号（无邮箱、无手机号），不按手机号合并（约定 §11.1）。匹配在一个事务内先取 `union:{unionid}` 再取 `{provider}:{subject}` 的 `pg_advisory_xact_lock`，并发首登只建一个账号；回填 `union_id` 前若该 unionid 已属于另一账号则不回填（记 warn），身份行保持无 unionid（该账号正在注销、尚未提交时同样不回填，注销提交后的下次登录补上）；短信登录两次都撞上手机号唯一约束时返回 409 `user.phoneTaken`（spec 已声明）；先对用户行 `FOR UPDATE` 再写身份行（回填 unionid、补建身份），与注销事务的加锁顺序一致；锁到时账号已被注销则重新匹配一次，按 unionid 找到的账号已注销时视为无主，均按新身份建号
- 健康检查不探测第三方

### 6.5 认证角色、角色与权限

| 项 | 实现 |
|---|---|
| 认证角色 | `AUTH_SIGNING_KEY` 已设置 → 签发：挂载约定 §11.1–§11.3、§11.5 与 JWKS，迁移 identity 表；只设置 `AUTH_JWKS_URL` → 验证：不挂载 §11.1–§11.3，§11.5 按配置挂载；两者都设置启动失败；都未设置时生产启动失败，非生产生成进程内临时 Ed25519 密钥并以签发角色运行（记 warn，重启后令牌失效） |
| 角色与权限 | 按约定 §5.5 由代码固定：`internal/entity/rbac.go` 声明角色、平台权限码、本服务私有权限码及其授予角色；`admin` 自动拥有本服务全部权限码；不建角色 / 权限表、不缓存 |
| 私有权限码 | 只在本服务生效；`listRoles` / `listPermissions` 只反映签发服务自己的权限码；模板本身不声明私有权限码 |
| 多角色 | `user_role(user_id, role_code)`；每个用户都有 `user`；access 的 `roles` claim 为全部角色代码 |
| 角色变更 | `setUserRoles`：缺 `user` 422 `contains`，未知角色 422 `exists`；同事务更新并吊销该用户全部会话 |
| 最后一个管理员 | 禁用、删除（含 `deleteMe`）、移除 `admin` 角色的事务先取 `pg_advisory_xact_lock(<固定键>)`，再统计其余 `active` 的 `admin`，为 0 时 409 `role.lastAdmin`；同一把锁覆盖所有可能减少管理员的操作 |
| 守卫 | policy 声明 `Perms: All(...)` / `Any(...)`；缺权限 403 `common.forbidden` |

---

## 7. 数据模型

### 7.1 规则

| 项 | 规则 |
|---|---|
| 命名 | 表名单数 snake_case；列 snake_case |
| 主键 | `id uuid PRIMARY KEY DEFAULT uuidv7()`，应用侧以 `uuid.NewV7()` 生成 |
| 时间 | `timestamptz`，`created_at` / `updated_at`（`DEFAULT now()`），软删 `deleted_at` |
| 枚举 | `text` + `CHECK`，取值 snake_case |
| 唯一 | 软删表用部分唯一索引 `WHERE deleted_at IS NULL`，软删后邮箱 / 手机号立即可被重新注册；邮箱以规范化后的值存储 |
| 用户引用 | 约定 §10：core 表引用用户只存 `sub`（`uuid` 列，无外键）；identity 表之间可建外键 |
| 约束命名 | `<table>_pkey`、`<table>_<columns>_key`、`<table>_<columns>_fkey`、`<table>_<columns>_idx`、`<table>_<name>_check`；检查正则 `^[a-z0-9_]+_(pkey\|key\|fkey\|idx\|check)$` |
| schema | 连接参数 `search_path = DATABASE_SCHEMA`；goose 版本表与 River 表在同一 schema；`migrate` 先执行 `CREATE SCHEMA IF NOT EXISTS`（约定 §9） |

### 7.2 表清单

| 表 | 主要列 | 迁移目录 |
|---|---|---|
| `app_user` | email、phone、password_hash（可空）、display_name、avatar_file_id、status（`active` / `disabled`）、email_verified_at、deleted_at；邮箱与手机号都可空（provider 建的账号两者皆无） | identity |
| `user_role` | user_id、role_code（联合主键） | identity |
| `user_identity` | user_id、provider（`wechat_miniapp` / `wechat_app` / `alipay`）、subject（openid 或支付宝 id）、union_id；`(provider, subject)` 唯一，`union_id` 普通索引 | identity（B4 的 `00002` 加入） |
| `session` | user_id、refresh_hash、auth_time、expires_at、revoked_at、user_agent、ip、last_active_at | identity |
| `session_refresh_history` | refresh_hash（主键）、session_id、rotated_at | identity |
| `user_token` | user_id、purpose（`email_verify` / `password_reset`）、token_hash、expires_at、used_at、invalidated_at | identity |
| `file` | owner_sub、purpose、name、content_type、size、sha256、storage、object_key、deleted_at | core（B5 的 `00003`） |
| `audit_log` | actor_sub、action、resource_type、resource_id、request_id、ip、data（jsonb） | core（`00001`） |
| `note` | owner_sub、title、content、deleted_at | core（`00002`） |
| River 表 | `rivermigrate` 管理 | — |

- `User` 由 `app_user` + `user_role` 组装：`emailVerified` = `email_verified_at IS NOT NULL`，`hasPassword` = `password_hash IS NOT NULL`，`avatarUrl` 由 `avatar_file_id` 生成签名地址
- 软删用户的同一事务删除其 `user_identity` 行，第三方身份立即可重新登录建号（约定 §11.3）
- `user` 是 PostgreSQL 保留字，用户表命名为 `app_user`

### 7.3 迁移

| 项 | 做法 |
|---|---|
| 目录 | `migrations/core/`、`migrations/identity/`，两个 goose provider，版本表 `goose_core_version`、`goose_identity_version`；`migrations/migrations.go` 的 embed 改为两个目录 |
| up | `CREATE SCHEMA IF NOT EXISTS` → core → identity（仅签发角色）→ `rivermigrate up` |
| down | `rivermigrate down` → identity down-to 0 → core down-to 0 |
| 初始迁移 | v1 的 `00001_initial_schema.sql` 删除，重写为两个目录的初始迁移（模板未上线，不保留旧结构的升级路径） |
| sqlc | `sqlc.yaml` 以两个迁移目录为 schema 输入 |
| `make test-migrations` | 在 `DATABASE_SCHEMA`（测试用非 public 名）上：up → `sqlc vet` → down → 断言 schema 中除 `goose_core_version`、`goose_identity_version` 外无剩余表 → up；约束与索引名按 §7.1 正则检查，排除 goose 版本表与 River 表 |

### 7.4 Seed

幂等。`seeds/seed.json` 改为演示账号定义：非生产创建 `admin@example.com`（`admin` + `user`）与 `user@example.com`（`user`），密码 `Passw0rd!`；生产只在 `SEED_ADMIN_EMAIL` 与 `SEED_ADMIN_PASSWORD` 都设置且无 `active` 的 `admin` 时创建管理员。

---

## 8. 基础设施

| 能力 | 实现 |
|---|---|
| 日志 | 约定 §8 字段；访问日志每请求一条，`route` 取路由模板；`ReplaceAttr` 删除 `Authorization`、令牌、密码、验证码字段，邮箱 / 手机号按约定掩码；`LOG_FORMAT=text` 用于本地 |
| OTel 初始化 | `pkg/tracing`（改名 `pkg/telemetry`）：resource 用 `resource.WithFromEnv`（读 `OTEL_SERVICE_NAME`、`OTEL_RESOURCE_ATTRIBUTES`）；trace / log exporter 由 `autoexport` 按 `OTEL_TRACES_EXPORTER`、`OTEL_LOGS_EXPORTER` 与 `OTEL_EXPORTER_OTLP_*` 构造，不再有自定义开关与 endpoint 覆盖；`OTEL_SDK_DISABLED=true` 全部关闭；新增 MeterProvider |
| 指标 | `OTEL_METRICS_EXPORTER=prometheus`（默认）：OTel Prometheus exporter + `promhttp` 在 `/metrics` 提供（`METRICS_TOKEN` Bearer，或设 `METRICS_PORT` 走独立端口）；`otlp`：推送，不挂载 `/metrics`。HTTP 指标来自 otelhttp，`http.route` 由操作查找设置的 `r.Pattern` 提供；River 指标来自 otelriver；pgx 指标来自 otelpgx。名称与标签按约定 §8 |
| worker 指标 | `worker` 进程在 `prometheus` 模式下于 `WORKER_METRICS_PORT` 提供 `/metrics`（`METRICS_TOKEN` 设置时校验）；未注册任何任务时 worker 只保持运行，不启动 River 客户端（River 要求至少一个 worker），B3 起随任务启用 |
| 健康 | `/health/live` 总是 200 `{status: "ok"}`；`/health/ready` 检查 Postgres、Redis（已配置时），不检查第三方与 JWKS，返回 `{status, checks: {database, redis}}`，失败 503 + `Retry-After`；停机期间 ready 返回 503 |
| Redis 键 | 只有 `auth:revoked:sid:` 与 `rl:` 按约定原样使用，其余（缓存、锁、短信、幂等、宽限期暂存）一律带 `{OTEL_SERVICE_NAME}:` 前缀 |
| 缓存 / 锁 | 普通字符串 / JSON 值；锁 `SET NX PX` + Lua 比较 token 释放；Redis 未配置时进程内实现 |
| 限流 | `pkg/ratelimit`：GCRA Lua 脚本内用 `TIME` 取 now，键与值按约定 §7；脚本两种模式：`consume`（判定并写入）与 `peek`（只判定不写入，用于只计失败的规则在校验前检查是否已超限）；`Reset(key)` 用于成功后清除。共享规则按约定 §11.6；私有规则 `{OTEL_SERVICE_NAME}-ip`（IP，300 次 / 分钟，突发 100，所有 `/api/v1`）与 `{OTEL_SERVICE_NAME}-user`（用户，120 次 / 分钟，突发 60，已登录请求），键为 `rl:{规则名}:{主体}`；Redis 出错时共享规则 503、私有规则放行；`RATE_LIMIT_ENABLED=false` 只用于测试 |
| 幂等键 | spec 中声明 `Idempotency-Key` 参数的 operation 启用；键 `{service}:idem:{u:sub 或 ip:IP}:{method}:{实际请求路径}:{key}` 存请求体 SHA-256 与处理状态；进行中的占位带请求自己的 owner 标识，TTL 为 `max(SHUTDOWN_TIMEOUT, HTTP 写超时) + 30s`（经验上限：写超时不取消处理器，处理器运行更久时占位会过期，同键重试会再次执行；处理器截止时间随 B6 的请求超时与 `statement_timeout` 一起确定），保存与删除只对 owner 仍持有的占位生效；首次结果为 2xx / 4xx 时保存状态码与响应体 24 小时（保存失败时占位留到过期），5xx 删除该键；进行中 409 `common.idempotencyInProgress`，请求体不同 422 `common.idempotencyKeyMismatch` |
| 队列 | River：`mail`、`account`、`default`；默认 5 次尝试、指数退避；事务内 `InsertTx` |
| 定时任务 | River 周期任务（leader 选举，每小时，`RunOnStart`）：`maintenance`（`usecase/maintenance`：签发角色删除过期会话及其 refresh 历史、过期 / 已用 / 已失效的 `user_token`；所有角色删除 `AUDIT_RETENTION` 之前的审计日志，每条 DELETE 至多 10000 行、循环到删完），`account.sweep`（签发角色：软删超过 `ACCOUNT_DELETION_GRACE` + 1 小时仍未清理的用户按 `(deleted_at, id)` 分页、每页 500 个全部重新入队 `account.purge`，已有任务的跳过，日志只计新入队数） |
| 账号注销 | `deleteMe` / `deleteUser`：同事务做最后管理员检查（§6.5）、软删用户、删除第三方身份、吊销全部会话、入队 `account.purge`（`account` 队列，延时 `ACCOUNT_DELETION_GRACE`，River 唯一任务按 user_id 去重）；任务对仍未软删的用户不做处理，否则在一个事务内依次调用登记的 `Purger`（文件对象与行、笔记删除、审计日志的 `actor_sub` 与 `ip` 清空）并物理删除用户（会话、令牌、角色级联），提交后经 `events.Publisher` 发布 `user.deleted`（CloudEvents 1.0 JSON，`source` 为 `OTEL_SERVICE_NAME`）；发布失败由 River 重试，重试时清理可重复执行；补偿扫描见「定时任务」 |
| 事件 | `events.Publisher`（B4）/ `events.Subscriber`（B6）端口；传输方式、键与主题名由 monorepo 配置（`EVENTS_*`，模板不预设变量），消费者组取 `OTEL_SERVICE_NAME`；模板默认实现只写日志；验证角色收到 `user.deleted` 后入队 `account.purge`，只运行本服务的 `Purger` |
| 审计 | policy `Audit: "<action>"` 的接口在成功后写 `audit_log`；`GET /api/v1/audit-logs`（`listAuditLogs`，`audit:read`，游标分页） |
| 邮件 | 模板 `templates/mail/{verify_email,reset_password}.{html,txt}.tmpl`，文案走 i18n 的 `mail.*`；`SMTP_URL` 为 `smtp://`（要求 STARTTLS；`?tls=none` 才允许明文，用于本地 Mailpit 与测试）或 `smtps://`（隐式 TLS），用户信息即认证；未配置时只写日志（掩码收件人与主题，不含正文链接），签发角色生产必填 |
| 文件 | 约定 §11.5 的三个接口；用途在代码中登记（`entity/file.go`）：`avatar`（约定默认）与 `attachment`（图片 + pdf，20 MiB）。上传 policy（`Upload: true`）的请求体上限为各用途上限最大值 + 64 KiB，不受 `BODY_LIMIT` 约束，读取超限映射为 413 `file.tooLarge`；`ValidateRequest` 设 `ExcludeRequestBody`，并对去掉请求体的请求副本校验（openapi3filter 的安全检查会整块读入请求体）。multipart 流式读取：`purpose` 在 `file` 之前时先校验用途并按该用途上限写入；`file` 在前时按最大上限写入临时对象，读到 `purpose` 后再校验大小与类型，不符则删除。类型取前 512 字节 `http.DetectContentType`，边写边计算 sha256 与大小；同一请求多于一个 `file` 为 422 `maxItems`，缺字段 422 `required`；文件名取 base name，非法 UTF-8 替换为 U+FFFD、去掉控制字符，至多 255 字符；写存储失败为 500，读请求体失败为 400。`File.url`：local 驱动为相对路径 `/api/v1/files/{id}/content?expires=&signature=`（HMAC-SHA256 签 `id.expires`，密钥 `FILE_URL_SECRET`，非生产未设置时进程启动随机生成），s3 驱动为预签名 GET（非图片带 `response-content-disposition: attachment`），有效期 `FILE_URL_TTL`；`expires`、`signature` 在 spec 中为可选、不限长的字符串，缺失或无效一律 404；`storage` 与当前 `STORAGE_DRIVER` 不同的文件不生成地址（`User.avatarUrl` 为 null，`File.url` 为空串）；下载在发出 200 之后复制失败时不再写错误信封；local 驱动要求 api 与 worker 共享 `STORAGE_LOCAL_ROOT`（ci 栈用卷 `uploaded-files`）；`getFileContent` 带 `X-Content-Type-Options: nosniff`，非图片带 `Content-Disposition: attachment`，签名无效或过期 404 |
| 停机 | SIGTERM → ready 503 → `http.Server.Shutdown(SHUTDOWN_TIMEOUT)` → River `Stop` → 关闭连接池 / Redis → flush OTel、Sentry |
| 进程 | 同一镜像：`serve`、`worker`、`migrate` |

---

## 9. API 清单

全部在 `/api/v1` 下（JWKS 除外）。

| 模块 | 接口 | 权限 |
|---|---|---|
| 认证、当前用户、管理 | 约定 §11.1–§11.3，仅签发角色挂载 | 约定 §11 |
| 文件 | 约定 §11.5 `uploadFile`、`getFile`、`getFileContent` | 登录 / 签名 |
| 审计 | `listAuditLogs` | `audit:read` |
| notes（示例） | `listNotes`（游标分页）、`createNote`（声明 `Idempotency-Key`，201）、`getNote`、`updateNote`、`deleteNote` | 登录，仅本人 |

---

## 10. 配置

公共变量按约定 §9（含 `DATABASE_SCHEMA`、`BODY_LIMIT`、`AUTH_VERIFY_KEYS`、`APP_FRONTEND_URL`、`OTEL_SERVICE_NAME`），标准 `OTEL_*` 变量（含 `OTEL_METRICS_EXPORTER`）按 OpenTelemetry 规范，均不重复列出。本服务特有变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `DATABASE_MAX_CONNS` / `WORKER_DATABASE_MAX_CONNS` | 20 / 10 | 连接池上限 |
| `PASSWORD_HASH_CONCURRENCY` | 2 | 约定 §5.4 |
| `RATE_LIMIT_ENABLED` | `true` | §8 |
| `METRICS_PORT` / `WORKER_METRICS_PORT` | 空 / `9464` | `prometheus` 模式下 `/metrics` 的独立端口 |
| `SHUTDOWN_TIMEOUT` | `20s` | |
| `SMTP_URL` / `MAIL_FROM` | 空 | 签发角色生产必填 |
| `STORAGE_DRIVER` / `STORAGE_LOCAL_ROOT` / `S3_ENDPOINT` / `S3_REGION` / `S3_BUCKET` / `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` / `S3_FORCE_PATH_STYLE` | `local` / `./data/files` | `s3` 时 `S3_BUCKET`、`S3_REGION` 必填 |
| `FILE_URL_SECRET` / `FILE_URL_TTL` | 空 / `1h` | local 驱动签名地址，至少 32 字符；生产 `local` 驱动必填 |
| `SMS_PROVIDER` / `ALIYUN_SMS_ACCESS_KEY_ID` / `ALIYUN_SMS_ACCESS_KEY_SECRET` / `ALIYUN_SMS_SIGN_NAME` / `ALIYUN_SMS_TEMPLATE_CODE` / `ALIYUN_SMS_ENDPOINT` | `log` / … / `https://dysmsapi.aliyuncs.com` | 取值 `log` / `aliyun`；`aliyun` 时四项账号变量必填；签发角色生产必须为 `aliyun`（短信登录是约定 §11.1 必备接口） |
| `WECHAT_MINIAPP_APP_ID` / `WECHAT_MINIAPP_APP_SECRET` / `WECHAT_APP_APP_ID` / `WECHAT_APP_APP_SECRET` / `WECHAT_API_BASE` | 空 / `https://api.weixin.qq.com` | §6.4 |
| `ALIPAY_APP_ID` / `ALIPAY_PRIVATE_KEY` / `ALIPAY_PUBLIC_KEY` / `ALIPAY_GATEWAY` | 空 / `https://openapi.alipay.com/gateway.do` | §6.4；`ALIPAY_PUBLIC_KEY` 为新增变量；v1 的 `ALIPAY_SIGN_TYPE`、`ALIPAY_CHARSET`、`ALIPAY_SANDBOX` 删除（只用 RSA2、UTF-8，沙箱改 `ALIPAY_GATEWAY`） |
| `EVENTS_*` | — | 事件传输配置，由 monorepo 决定（§8「事件」）；模板不定义 |
| `ACCOUNT_DELETION_GRACE` / `AUDIT_RETENTION` | `168h` / `4320h` | |
| `SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD` | 空 | §7.4 |

删除的 v1 变量：`DB_SCHEMA_OWNER`、`OTEL_ENABLED`、自定义 OTLP endpoint 变量、nestjs 同名变量（`API_PREFIX`、`THROTTLE_*`、`TRUST_PROXY`、`FILE_DRIVER`、`FILE_*_MAX_SIZE`、`AUTH_JWT_SECRET`、`METRICS_ENABLED`、`METRICS_PATH` 等）。

启动时一次性解析并列出全部错误；生产额外要求 `REDIS_URL`、`CORS_ORIGINS`、`prometheus` 模式下 `METRICS_TOKEN` 或 `METRICS_PORT`、认证角色变量（§6.5），签发角色另需 `APP_FRONTEND_URL`、`SMTP_URL`、`SMS_PROVIDER=aliyun`。

---

## 11. 工程配置

沿用 v1，变化如下：

| 项 | 做法 |
|---|---|
| tools/go.mod | oapi-codegen、goose、mockgen、govulncheck、gotestsum、go-test-coverage；kin-openapi 与主模块统一为 0.153.0；`tools/openapigen` 改为 bundler |
| Makefile | 删除 `openapi-sync`、`test-parity`；新增 `openapi`（bundle）、`keys`（生成 Ed25519 开发密钥写入 `.env`）；`module NAME=<name>` 不再需要 `TAG`；`test-migrations` 按 §7.3 重写（`NAMING_SQL` 参数化 schema、新正则、无 nestjs 豁免） |
| `.golangci.yml` | `build-tags` 去掉 `parity`；删除 `pkg/validatorx/` 排除项；depguard 规则随目录调整 |
| `.testcoverage.yml` | 覆盖率来源随测试层调整；排除新的生成文件（校验文档、`gen/`） |
| prek / `scripts/check-generated.sh` | 生成代码新鲜度检查路径改为 `openapi/src/`、`queries/`；生成文件列表加入 `openapi/openapi.json` 与校验文档 |
| `scripts/commit-msg.sh` | 头注释去掉 nestjs-template 引用，类型集合不变 |
| Docker | 不变（distroless、多架构、embed 迁移 / 模板 / locales / openapi.json）；`.dockerignore` 删除 `!db/`；`healthcheck` 子命令访问 `/health/live`，`--ready` 访问 `/health/ready` |
| compose | 删除 `docker-compose.parity.yaml`；镜像标签 postgres `18.7-alpine`、redis `8.10.3-alpine`；`docker/.env.ecs.example` 按 §10 与约定 §9 更新变量 |
| Renovate | `ignorePaths` 删除 `scripts/nestjs/nestjs.compose.yaml` |
| `_typos.toml` | 删除 `db/nestjs-schema.sql` 排除项 |
| CLAUDE.md 约定 | 注释规范、提交与推送原文保留 |

模块生成器 `make module NAME=<name> [PERMS=1]` 以 `note` 为模板生成：`openapi/src/paths|schemas/<name>.yaml`、core 迁移、sqlc 查询、repo / usecase / handler、`Purger`、policy 条目、单元与集成测试骨架，然后执行 `make generate`。默认与 `note` 相同，只按本人归属控制访问、不声明权限码；`PERMS=1` 时在 `rbac.go` 登记私有权限码 `<name>:read`、`<name>:write`（不授予其他角色，`admin` 自动拥有）并在 policy 中引用。

---

## 12. 测试策略

| 层 | 内容 | 位置 / 标签 |
|---|---|---|
| 单元 | usecase（mock 端口）；`pkg/*`；错误码表与约定 §2.4 逐条一致；校验映射、格式注册、码点长度（§5.1）；Request ID 正则与 UUIDv7 生成；`Accept-Language` 匹配；jsontime 输入输出；E.164 规范化；日志脱敏与掩码；`TRUSTED_PROXIES` 取 IP 与 IPv6 /64；GCRA 计算、`peek` 模式与 `X-RateLimit-*` 语义（突发、剩余、恢复秒数、多规则取最少）；RBAC 映射与 `admin` 全权限；JWS 头 `kid` 与 JWK 形状 | 同目录 `*_test.go` |
| 集成 | repo SQL 与约束；refresh 轮换（并发只产生一对、宽限期返回同一对、历史哈希重用吊销、`auth.refreshTokenInvalid`、会话寿命不随轮换延长）；吊销键写入时机与 TTL、Redis 出错 503；限流 Lua（Redis `TIME`、键值格式、Redis 出错时共享 / 私有规则行为、只计失败规则）；短信冷却 / 日上限 / 锁定；幂等键（实际路径作用域、2xx / 4xx 回放、5xx 释放、进行中、请求体不同）；`role.lastAdmin` 并发；软删后重新注册；微信 openid / unionid 回填；River 入队与周期任务；文件（MinIO、类型识别、`purpose` 先后两种顺序、超限清理、签名地址与响应头）；邮件（测试 SMTP 服务器 + golden）；完整路由的 HTTP 级测试：405 + `Allow`、尾斜杠 404、401 `WWW-Authenticate`、public 接口带无效令牌按匿名、413 / 415 / 422，**每个响应经 `openapi3filter.ValidateResponse` 对照 `openapi/openapi.json` 校验** | 同目录 `*_integration_test.go`，`integration` |
| 契约 | 已实现的约定 §11 operationId 方法与路径一致，请求 / 响应字段集合与 §11.4 对象一致；`make openapi` 后 `openapi.json` 无 diff；`GET /openapi.json` 与文件逐字节相同；`chi.Walk` 路由集合与 spec paths 双向一致（白名单：`/metrics`、`/docs`、`/openapi.json`、`/health/live`、`/health/ready`、`/.well-known/jwks.json`）；operationId 唯一且 lowerCamelCase；422 响应引用 `ValidationErrorResponse`、其余 4xx / 5xx 引用 `ErrorResponse`；每个 operation 有 policy；鉴权 operation 声明 `bearerAuth`。各批只核对该批已实现的子集，B6 起要求 §11 全量 | `tests/contract`，`contract` |
| 互通 | 固定测试密钥签发的 access 可由 JWKS 验证（含 `AUTH_VERIFY_KEYS` 中的旧密钥）；验证角色实例经 `AUTH_JWKS_URL` 拉取签发实例公钥后鉴权成功，签发实例登出后验证实例因吊销键返回 401；`email_verified` claim 驱动 `VerifiedEmail` | `tests/e2e` |
| e2e | compose：migrate → seed → api + worker；B2 为 health 与 notes；B3 起注册 → 验证邮件（Mailpit）→ 登录 → 刷新 → 会话 → 登出；B4 起注销；B5 起上传；未配置凭据时 `listAuthProviders` 为空数组 | `tests/e2e`，`e2e` |

- 集成夹具沿用 testcontainers + 模板库克隆（`Clone(t)`）与 Redis 逻辑库池；`PostgresImage` 改为 `18.7-alpine`
- B2 起提供 `testutil.IssueAccessToken`（固定 Ed25519 测试密钥，可设 `email_verified`、`auth_time`、`roles`）供需要登录的测试使用
- 覆盖率：单元 + 集成 + 契约合并，`internal/...` ≥ 80%（排除生成代码与 mock）

---

## 13. CI / CD

| workflow | 内容 |
|---|---|
| check.yml | lint、workflow-lint、type-check（vet、build、`make generate` 无 diff）、test（单元 + 集成）、migrations（`make test-migrations`）、contract、coverage、security（govulncheck、gitleaks）、build、image-scan（Trivy）、e2e；B1 删除 parity job 并改写文件头注释 |
| deploy.yml | 不变（`manage.sh health` 调用 `/app healthcheck --ready`，路径变化在子命令内） |
| cleanup-registry.yml | 不变 |
| drift.yml | 删除 |

CI 不再访问 nestjs-template 与 fastapi-temp。

---

## 14. 从 v1 迁移的重构清单

按批次列出（批次见 §15）。删除某文件时其 `_test.go` 一并删除，下文不再逐个列出。

### 14.1 删除

| 批 | 路径 | 说明 |
|---|---|---|
| B1 | `internal/usecase/{address,appconfig,appprofile,consent,favorite,feedback,region}/` | 与 v1 身份链路无耦合的业务模块 |
| B1 | `internal/entity/{address,config,favorite,feedback}.go` | |
| B1 | `internal/controller/http/v1/{addresses,app_profile,config,consents,favorites,feedbacks,regions}.go`、`v1/view/{address,config,consent,favorite,feedback,region}*.go` | |
| B1 | `internal/repo/postgres/{addresses,config,consents,favorites,feedbacks,regions}.go` 与对应 `queries/*.sql`、`queries/seed.sql` 的 region 部分 | 迁移文件本批不动 |
| B1 | `internal/repo/postgres/sqlcgen/{addresses,config,consents,favorites,feedbacks,regions}.sql.go`、`sqlcgen/copyfrom.go` | sqlc 不清理不再生成的文件，需手工删除（`copyfrom.go` 只服务 `SeedRegions`） |
| B1 | `internal/app/{addresses,config,consents,favorites,feedbacks,regions}_integration_test.go` | |
| B1 | `seeds/region-data.json` | 连带改写见 §14.2 B1 |
| B1 | `tests/parity/`、`docker/docker-compose.parity.yaml`、`scripts/parity.sh`、Makefile `test-parity`、`.golangci.yml` / `.vscode/settings.json` / Makefile `vet` 的 `parity` build tag、`check.yml` 的 parity job 与 `NESTJS_REPOSITORY` 变量 | |
| B1 | `.github/workflows/drift.yml` | |
| B1 | `scripts/{openapi-sync.sh,copy-shared-artifacts.sh,gen_nest_handlers.py,gen_validation_cases.py,schema_snapshot.py}`、`scripts/nestjs/`、Makefile `openapi-sync` 与 `FASTAPI_DIR` | 已入库的产物保留到 B2 |
| B1 | `tests/contract/contract_test.go` 的 `sharedArtifacts` 与 `TestSharedArtifactsMatchFastapiTemp` | 本仓库不再与 fastapi-temp 比对共享产物；fastapi-temp 并行改造后其 `HEAD` 已无这些文件 |
| B1 | `scripts/bench/{run.sh,auth.js}`、`docs/benchmark.md` | nestjs 对比压测，B6 重写 |
| B1 | `seeds/seed.json` 的 `config` 段、`queries/seed.sql` 的 `SeedConfig`、`internal/repo/postgres/seed.go` 的配置写入 | `appconfig` 删除后无读取方 |
| B1 | `pkg/pagination.{LazyTotal,OffsetLimit}` | 调用方只有 address、favorite、feedback |
| B2 | `internal/usecase/{alert,notification,accountcleanup}/` 与 mocks、`internal/entity/{alert,notification}.go`、`internal/worker/notification.go`、`v1/{notifications,account_cleanup}.go`、`v1/view/notification*.go`、`internal/repo/postgres/{alerts,notifications,account_cleanups}.go` 与 queries、`internal/app/{notifications,notification_delivery,infra}_*test.go`、`internal/worker/worker_internal_test.go` 中告警与清理用例、`internal/worker/schedule.go`、`internal/repo/postgres/{maintenance.go,infra_integration_test.go}`、`internal/app/{integrations,jobs}.go`、`internal/app/jobs_integration_test.go` 、`internal/metrics/`（由 OTel 指标替代）、`internal/app/throttle.go`、`internal/app/worker_metrics_internal_test.go` | 与 alert 注入、`metrics` 告警计数、`repo.{Jobs,Alerts,AccountCleanups,Maintenance}` 端口一起移除；worker 只留 River 客户端骨架（`default` 队列、任务计数中间件），B3 起按 §8 加队列与周期任务 |
| B2 | `openapi/{nestjs-baseline.json,validation.json,SOURCE,nest_handlers.json,overlay.yaml,scope.yaml}`、`openapi/templates/` | 由 `openapi/src/` 与 bundler 替代 |
| B2 | `pkg/validatorx/`、`.golangci.yml` 中 `pkg/validatorx/` 排除项、`tests/contract/{validation_test.go,unimplemented_test.go,fixtures/}`、`tests/e2e/probes_test.go` | 由 `openapi3filter` 替代 |
| B2 | `internal/controller/http/{operation.go,routing.go,upload.go}`、`httperr/translate.go`、`v1/{send,input,home}.go`、`v1/view/{home,health}*.go` 、旧 `controller/http` 包其余文件（`guard`、`throttle`、`observe`、`docs`、`audit`、`tx` 等）与 `v1/view/` | Express 语义、multer 语义、nestjs 信封与过滤器 |
| B2 | `db/`、`internal/app/migrate.go` 的 `CheckSchema`、`internal/config/groups.go` 的 `DB_SCHEMA_OWNER`、`internal/app/app_integration_test.go` 的 `TestMigrationsMatchNestjsSchemaSnapshot`、`internal/testutil/schema.go`、`.dockerignore` 的 `!db/`、`_typos.toml` 的排除项 、`internal/config/{groups,infra,integrations}.go`（短信、微信、支付宝、文件、邮件变量随 B3–B5 的功能重新加入）、`pkg/postgres` 的 TLS 选项（`DATABASE_URL` 的 `sslmode` 等参数由 pgx 解析） | nestjs 结构快照 |
| B2 | `migrations/00001_initial_schema.sql` | 由 `migrations/{core,identity}/` 替代 |
| B2 | v1 身份代码：`v1/{auth,phone_auth,users,files}.go`、`v1/view/user*.go`、`internal/usecase/{auth,user,session,permission,sms,file}/` 与 mocks、`internal/entity/{user,permission,jobs}.go`、`internal/repo/postgres/{users,sessions,user_bindings,files,seed}.go` 与 `queries/{users,sessions,user_bindings,files,permissions,seed}.sql`、`internal/app/{phone_auth,files,superuser,shutdown}_integration_test.go`、`internal/app/cleanup_internal_test.go`、`internal/controller/http/upload.go`、`v1/{input,send}.go`，`reqctx` 的主体 / 客户端 / 上传、`guard.go` 的 JWT 与权限守卫 | 依赖旧表结构；按新模型在 B3–B5 重写（git 历史可查）。`entity/social.go` 被保留到 B4 的 `webapi/{sms,wechat,alipay}` 引用，随其在 B4 删除；`shutdown_integration_test.go` 依赖上传接口保持请求在途，notes 步骤用慢速请求体的 `createNote` 重写 |
| B2 | `internal/repo/webapi/mail/`、`templates/mail/*.hbs`、`templates/templates.go`、`tests/fixtures/nestjs_mail/`、`internal/i18n/locales/{zh,en}/*.json` | 邮件无调用方后整体移除，B3 重建 |
| B2 | `cmd/modulegen/`、`templates/module/`、Makefile `module` 目标 | 读取 nestjs 基线与 `scope.yaml`；B6 重写 |
| B2 | `pkg/msduration/` | 唯一调用方 `internal/config/groups.go` 同批改写 |
| B2 | `cmd/app` 的 `permissions clear-cache`、`createsuperuser`（依赖 `validatorx.IsEmail`、`app.ClearPermissionCache`） | `createsuperuser` 在 B3 重建 |
| B2 | `tests/e2e/auth_test.go` | B3 重写 |
| B6 | `docs/contract.md` | 由 `docs/openapi.md` 替代 |

### 14.2 改写

| 批 | 路径 | 改写内容 |
|---|---|---|
| B1 | `internal/app/app.go`（含 `newAccountCleanup` 中 favorite / feedback / address 的 cleaner）、`v1/server.go`、`internal/repo/contracts.go` 与 `internal/repo/mocks/`、`policy.go`、`openapi/scope.yaml`、`openapi/overlay.yaml`（指向已删 `view` 类型与 DTO 的 `x-go-type` 条目） | 去掉 B1 删除模块的注入、端口、处理器与条目；文档中这些路由回落为未实现 404。B1 不单独部署；注销用户在这三张表中的残留数据由 B2 重建 schema 时一并清除 |
| B1 | `internal/app/cleanup_internal_test.go` | 清理顺序断言只保留 `users`、`files`（文件在 B2 删除） |
| B1 | `internal/testutil/schema.go` | godoc 去掉对 `scripts/schema_snapshot.py` 的引用 |
| B1 | `seeds/seeds.go`（去掉 region embed）、`internal/repo/postgres/seed.go`（去掉 `seedRegions`）、`internal/app/seed.go`、`internal/testutil/database.go`、`internal/app/app_integration_test.go` 中 region 相关代码 | 随 `region-data.json` 删除 |
| B1 | `UPDATE_CONTRACT_DOC=1` 重新生成 `docs/contract.md` 的未实现接口一节 | 使 `make test-contract` 通过 |
| B1 | `.github/workflows/check.yml`（删 parity job、改头注释）、`.github/renovate.json5`（删除 `ignorePaths`）、Makefile `ci` 目标说明 | |
| B1 | `CLAUDE.md` | 见 §14.5 |
| B1 | `docs/platform-conventions.md` | 纳入版本库（内容不改） |
| B1 | `README.md`、`docs/{architecture,cli,contract,database,deploy,testing}.md` | 删除指向 B1 已删模块、脚本、Make 目标、parity、drift、region / config 种子的内容；其余按 §14.5 在 B2 / B6 更新 |
| B2 | `openapi/` | 新建 `src/`、`openapi.json`；`oapi-codegen.yaml` 去掉 include-operation-ids / overlay / user-templates，加 uuid 类型映射；`openapi.go` embed 新文件 |
| B2 | `tools/openapigen`、`tools/go.mod` | bundler（展开 `x-envelope`、输出入库文档与全内联校验文档）；kin-openapi 0.153.0 |
| B2 | `internal/controller/http/{router,operations,middleware,pipeline,auth,ratelimit,validate,execute,ops,policy}.go` | §5.2 中间件链（otelhttp 的 `http.route` 来自操作查找写入的 `r.Pattern`）、由校验文档建的操作表（缺 policy、缺处理器、policy 用到按账号或会话计数的规则时启动失败）、policy 字段、校验适配器与格式注册、限流（`X-RateLimit-*` 取最紧的规则）、幂等键、事务与审计（三者缓冲响应，提交后才发出）、ops 路由；`Options.MeterProvider` 供测试注入；`v1.Register` 挂载生成的处理器 |
| B2 | `internal/controller/http/{httperr,response}/` | 约定 §2 信封、码表、401 头 |
| B2 | `internal/controller/http/reqctx/` | 去掉对 `validatorx`、`usecase/auth`、`usecase/file` 的引用，只保留请求 ID、语言、主体、操作 |
| B2 | `internal/entity/errors.go` | 约定 §2.4 码表；删除已无生产调用方的 `NewBusinessError` 与 `httperr` 业务键表 |
| B2 | `internal/usecase/health/`（含 `disk*.go` 删除磁盘检查）、`v1/{health,server}.go`、`internal/repo/postgres/{db,health,jobs,maintenance,audits}.go`、`internal/repo/contracts.go` 与 `internal/repo/mocks/` | ready 检查项；生成接口实现入口；事务与 River 入队（去掉通知 / 清理任务）；周期清理 SQL；审计按新表写入；端口按新模型重写 |
| B2 | `internal/i18n/` | go-i18n bundle，`locales/{zh,en}.json` 按 errorKey 与 `validation.<rule>` 组织 |
| B2 | `internal/app/{observability,worker_metrics,worker,app,infra,migrate,seed,auth}.go`、`internal/worker/worker.go` | OTel meter 与 `OTEL_METRICS_EXPORTER`；otelriver；去掉 alert / notification / accountcleanup 注入；迁移流程（§7.3） |
| B2 | `pkg/tracing` → `pkg/telemetry` | §8「OTel 初始化」 |
| B2 | `internal/config/` | 约定 §9 变量与 §10 本服务变量；时长用 `time.ParseDuration` |
| B2 | `internal/repo/redis/{cache,lock}.go` | 去掉 Keyv 格式，键加 `{service}:` 前缀 |
| B2 | `internal/testutil/{postgres,database}.go` | 镜像 18.7；按 `DATABASE_SCHEMA` 与两个 provider 迁移 |
| B2 | `migrations/migrations.go`、`seeds/seed.json`、`seeds/seeds.go` | 两个目录的 embed；演示账号定义 |
| B2 | `pkg/{ratelimit,logger,pagination,jsontime,jwtx}` | 约定 §7 键值、Redis `TIME`、`peek`、共享规则表 `ratelimit.Shared()`；约定 §8 字段与脱敏；页码 + 游标；毫秒格式；jwx 验签与 `Signer`（`testutil.IssueAccessToken` 与 e2e 需要签名；登录签发流程在 B3）、`RemoteKeys`（`jwk.Cache`，未知 `kid` 最多每 30 秒刷新一次）；`pagination` 删除无调用方的 `NewInfinity` |
| B2 | `cmd/app/main.go` | 去掉已删除子命令；`healthcheck` 路径 `/health/live` / `/health/ready` |
| B2 | `Makefile`（`test-migrations`、`openapi`、`generate`、去掉 `module` 与 `MOCK_SOURCES` 中已删条目）、`sqlc.yaml`、`.pre-commit-config.yaml`、`scripts/check-generated.sh`、`.golangci.yml` depguard、`.testcoverage.yml` | §7.3、§11 |
| B2 | `tests/contract/contract_test.go`、`tests/e2e/e2e_test.go`、`docker/docker-compose.{base,redis,postgres,ci}.yaml`、`docker/infra.compose.yaml`、`.env.example`、`.dockerignore` | §12；e2e 以 compose ci 的固定测试签名密钥签发 access；compose 与 `.env.example` 改为 `DATABASE_URL` / `REDIS_URL` 等新变量（e2e 需要），镜像 `postgres:18.7-alpine` |
| B3 | `pkg/{jwtx,password}` | EdDSA 签发与 JWKS；仅 argon2id、假哈希 |
| B3 | `cmd/app/main.go` | 新增 `keys generate`（打印私钥 PEM、公钥 PEM 与 kid）、重建 `createsuperuser --email`（密码读标准输入首行，已有账号则加 `admin` 并替换密码） |
| B3 | `openapi/src/common.yaml`、`internal/controller/http/{execute.go,reqctx}` | `page` 上限 100000（offset 按 bigint 传入）；`listUsers` 的 `sort` 为逗号分隔的 `createdAt` / `email`（至多两个、不重复），`keyword` 按字面子串匹配（转义 `%`、`_`）；`reqctx.SetAuditTarget` 让处理器为无 path id、无响应 id 的审计接口（`changePassword`、`resetPassword`）写入用户 id |
| B3 | `internal/entity/errors.go`、`internal/controller/http/httperr` | `Error.RetryAfter`：用例层限流的 429 带 `Retry-After` |
| B3 | `internal/controller/http/{pipeline,policy,ops}.go`、`reqctx` | `reqctx.Client`（客户端 IP 与 User-Agent，供会话记录与按 IP 的用例限流）；§11.1–§11.3 的 policy（共享规则中按 IP / 用户计数的由路由器检查，按账号、会话计数与只计失败的由用例经 `usecase.Limits` 检查）；`Options.JWKS` 挂载 `/.well-known/jwks.json`。验证角色仍挂载身份接口，按方案在 B6 装配 |
| B3 | `docker/docker-compose.ci.yaml`、`.dockerignore`、`.env.example`、`Makefile` | compose ci 加 Mailpit（宿主端口 `MAILPIT_PORT`，默认 18025）与 `SMTP_URL` / `APP_FRONTEND_URL`；镜像包含 `templates/`；`.env.example` 加 `SMTP_URL`、`MAIL_FROM`、`PASSWORD_HASH_CONCURRENCY`、`SEED_ADMIN_*`；`make keys` |
| B4 | `internal/repo/webapi/{sms,wechat,alipay}/`、删除 `internal/entity/{social,nest_error}.go` | `Provider` 实现；支付宝响应验签（`ALIPAY_PUBLIC_KEY`）；unionid；错误映射为 `auth.providerCodeInvalid` / `common.badGateway`；删除无调用方的微信订阅消息与短信 mock 模式 |
| B4 | `migrations/identity/00001_identity.sql` | 删除 `app_user_contact_check`（provider 建的账号没有邮箱与手机号；模板未上线，直接改初始迁移） |
| B4 | `internal/repo/postgres/{users,notes,audits,mailqueue}.go` 与 queries、`internal/usecase/{contracts.go,user,auth}`、`internal/controller/http/{policy.go,v1}`、`internal/config`、`internal/worker`、`internal/app/{identity,worker}.go` | 手机号查询与绑定、软删 / 物理删除、笔记与审计的 `Purger`；River 事务内入队抽成共用函数；§11.1–§11.3 的 B4 接口与 policy（`sendSmsCode` 为 optional + `auth-sms`，`deleteMe` 为 `Reauth`，审计 `user.delete`、`user.phone.bind`）；§10 的短信、provider、注销变量与生产要求；`account` 队列 |
| B4 | `docker/docker-compose.ci.yaml`、`.env.example` | ci 栈为生产模式，加 `SMS_PROVIDER=aliyun` 与占位账号（e2e 不发短信）；`.env.example` 加 §10 B4 变量 |
| B5 | `internal/repo/webapi/storage/` | `repo.FileStorage` 改为按对象键：`NewSink` / `Open` / `Delete` / `PresignGet`，Sink 在 `Commit` 时才定类型；local 写 `.part` 后改名 |
| B5 | `internal/controller/http/{policy,validate}.go`、`v1/{me,users,auth,views}.go`、`internal/usecase/user`、`tools/openapigen` | policy `Upload`；`avatarFileId` 的 `exists` 校验与 null 清除（`pkg/optional`，spec 用 `x-go-type`，bundler 输出的文档去掉 `x-go-*`）；`User.avatarUrl` 由文件用例批量生成 |
| B5 | `docker/docker-compose.ci.yaml`、`.env.example` | ci 栈 api 与 worker 共享卷 `uploaded-files`（`STORAGE_LOCAL_ROOT=/home/nonroot/files`）与 `FILE_URL_SECRET`；`.env.example` 加文件与审计变量 |
| B5 | `internal/usecase/audit/`、`internal/worker/`、`internal/usecase/account` | 查询接口（`listAuditLogs`：游标分页，`actorSub`、`action` 筛选）与保留期清理；周期任务（§8）；`account.Sweep` |
| B6 | `deploy/manage.sh`、`Dockerfile`、`docker/*.yaml`、`docker/.env.ecs.example`、`.env.example`、`.github/workflows/check.yml`、`scripts/commit-msg.sh` | §11、§13；`docker-compose.ecs.yaml` / `.env.ecs.example` 的生产签发服务需要 `SMS_PROVIDER=aliyun` 与 `ALIYUN_SMS_*`（B4 起启动校验） |
| B6 | `scripts/bench/` | 本服务自身压测 |
| B6 | `pkg/logger`、`pkg/ratelimit` | `MaskEmail` 按字符而非字节截取本地部分；进程内限流器满 10000 键后的淘汰改为不全表扫描 |

### 14.3 新建

| 批 | 路径 |
|---|---|
| B2 | `internal/usecase/contracts.go`、`internal/usecase/note/`、`migrations/core/`、`internal/controller/http/v1/notes.go`、`internal/controller/http/validation.json`（生成）、`pkg/phone`、`testutil.IssueAccessToken`、`internal/entity/note.go`、`internal/entity/nest_error.go`（B4 客户端沿用的旧错误类型，随 B4 删除）、`internal/repo/postgres/notes.go`、`internal/repo/redis/{revocations,idempotency}.go`、`pkg/clientip`、`pkg/telemetry`、`internal/controller/http/testdata/spec.yaml`（路由测试用 spec） |
| B3 | `migrations/identity/00001_identity.sql`（`app_user`、`user_role`、`session`、`session_refresh_history`、`user_token`）、`internal/entity/{user,rbac}.go`、`internal/usecase/{auth,user,session}/`、`internal/usecase/limits.go`、`internal/controller/http/v1/{auth,me,users,views}.go`（`listRoles` / `listPermissions` 在 `users.go`，JWKS 由 `ops.go` 提供）、`internal/repo/postgres/{users,sessions,tokens,mailqueue}.go` 与 queries、`internal/repo/redis/grace.go`、`internal/app/{identity,accounts}.go`、`pkg/password`、`internal/repo/webapi/mail/`、`templates/templates.go`、`templates/mail/*.tmpl`、`internal/i18n/locales` 中邮件文案、`internal/testutil/smtp.go`（go-smtp 进程内服务器）、`tests/e2e/auth_test.go` |
| B4 | `internal/usecase/{sms,account}/`、`internal/usecase/auth/provider.go`、`internal/usecase/phone.go`、`internal/events/`、`internal/entity/identity.go`、`migrations/identity/00002_user_identity.sql`、`internal/repo/postgres/{identities,accountqueue}.go` 与 `queries/identities.sql`、`internal/repo/redis/sms.go`、`internal/testutil/logbuffer.go`、`internal/app/{sms,provider,account}_integration_test.go` |
| B5 | `internal/usecase/file/`、`internal/usecase/maintenance/`、`internal/controller/http/v1/{files,audit}.go`、`internal/entity/file.go`、`migrations/core/00003_file.sql`、`internal/repo/postgres/{files,maintenance}.go` 与 `queries/{files,maintenance}.sql`、`pkg/optional`、`internal/app/files.go`、`openapi/src/{paths,schemas}/{files,audit}.yaml`、`tests/e2e/files_test.go` |
| B6 | `cmd/modulegen/`、`templates/module/`、`docs/openapi.md`、`docs/benchmark.md` |

### 14.4 保留

`internal/testutil/{docker,redis,minio}.go`、`internal/repo/redis/ping.go`、`internal/repo/webapi/outbound/`、`pkg/{httpserver,postgres}`、`scripts/check-comment-history.sh`、`.github/workflows/{deploy,cleanup-registry}.yml`、`.github/zizmor.yml`、`.editorconfig`、`.gitattributes`、`.gitleaks.toml`、`.vscode/`、`LICENSE`、`AGENTS.md`（软链接）。

### 14.5 文档更新

| 批 | 文件 | 更新条目 |
|---|---|---|
| B1 | `CLAUDE.md` | 首段目标改为「遵守 `docs/platform-conventions.md`，导出自身 OpenAPI」，并注明 v2 迁移进行中、以 `TEMPLATE_PLAN.md` 为准；命令表删 `openapi-sync`、`test-parity`；测试表删 parity 行及 parity 限流说明；注释规范与提交推送两节不动 |
| B2 | `CLAUDE.md` | 命令表新增 `openapi`，`module` 暂缺；「契约：spec-first」整节改为 spec 源文件、bundle、生成流程；分层表去掉通用业务模块、nestjs 字样、validatorx、Keyv，加 `usecase/contracts.go`；删除「路由语义（与 Express 一致）」整节，改为 §5.2 处理顺序；约束命名规则改为 §7.1（去掉 `idx_<table>_<column>`）；契约层说明改为 §12；生成文件列表加 `openapi/openapi.json` 与校验文档；Redis 测试说明随键前缀更新 |
| B3 / B6 | `CLAUDE.md` | B3 加 `keys`；B6 恢复 `module NAME=<name> [PERMS=1]` |
| B6 | `README.md` | 功能列表、快速开始（`make keys`、认证角色）、Make 目标、目录结构、文档索引 |
| B6 | `docs/architecture.md` | 目录、分层、请求链路、进程模型、认证角色、`user.deleted` 事件、在 monorepo 中接入 Connect（connect-go + otelconnect、复用 jwtx 拦截器、h2c 用 `http.Server.Protocols.SetUnencryptedHTTP2`） |
| B6 | `docs/auth.md` | 指向约定 §5、§11；本服务的令牌实现、轮换与宽限期、provider 启用变量 |
| B6 | `docs/database.md` | snake_case、UUIDv7、schema、两个迁移目录、无跨 schema 外键；删除「与 nestjs 共用数据库」 |
| B6 | `docs/cli.md` | 子命令、`keys generate`、模块生成器；删除共享产物脚本与 `permissions clear-cache` |
| B6 | `docs/testing.md` | 删除 parity 与 oracle，改为 §12 各层 |
| B6 | `docs/deploy.md` | 健康检查路径、环境变量（含 `OTEL_METRICS_EXPORTER`）、monorepo 部署要点 |
| B6 | `docs/benchmark.md` | 重新压测后新建 |

---

## 15. 实施阶段

每批：开发 → review → 修复 → 本地验证通过后停在工作区，等用户确认提交。契约验收各批只核对该批已实现的子集。

| 批 | 内容 | 验证 |
|---|---|---|
| B1 删除 | §14.1 / §14.2 的 B1 行：7 个无耦合业务模块与 region 种子、parity、drift、同步脚本、nestjs 压测；`docs/platform-conventions.md` 入库；CLAUDE.md 按 §14.5 B1 更新。notifications、alert、accountcleanup 与 v1 身份链路、nestjs 基线、validatorx、旧迁移留到 B2 | `make check`、`make test-integration`、`make test-contract`（重新生成 `docs/contract.md` 后）通过 |
| B2 平台层与数据基础 | spec 源 + bundler + `/openapi.json`；§5 全部（信封、码表、校验适配器与格式、415 / 413、405 + Allow、401 头、i18n、请求头、CORS、限流、幂等键）、日志、telemetry 与指标、health；core 迁移、`DATABASE_SCHEMA`、River 进服务 schema；jwx 验签 + 吊销键检查 + `testutil.IssueAccessToken`；`notes` 示例；删除 §14.1 B2 行（含 notifications / alert / accountcleanup、v1 身份代码、邮件、modulegen） | 单元（码表、映射两种文档、格式、GCRA）；集成（notes 全链路含登录态 + 响应对照 spec、uuid 读写）；契约（health、notes、文档一致性）；e2e（health、notes）；`make test-migrations` |
| B3 认证核心 | identity 迁移、EdDSA 签发 + JWKS、邮件模板与 `mail` 队列、约定 §11.1 邮箱类接口与 `refreshToken` / `logout`、§11.2 中 `getMe` / `updateMe`（B5 之前没有可引用的文件，非 null 的 `avatarFileId` 一律 422 `exists`）/ `changePassword` / 会话三接口、§11.3 除 `deleteUser` 外全部、RBAC、`role.lastAdmin`（禁用 / 移除角色）、共享限流规则（除 `auth-sms`）、审计写入、seed、`createsuperuser`、`keys generate` | 集成：轮换并发、宽限期、历史重用、吊销时机、只计失败规则；契约：已实现的 §11 接口与对象字段；e2e 注册到登出 |
| B4 短信、provider、注销 | `sendSmsCode`、`loginWithSms`、`bindPhone`、`auth-sms`；`listAuthProviders`、`loginWithProvider`（三个 provider）、`bindPhoneWithWechat`；`deleteMe` / `deleteUser`（含 `role.lastAdmin`）、`account.purge`、`user.deleted` 发布 | 单元（provider 客户端 httptest、支付宝验签、错误映射）；集成（短信限额与锁定、注销任务、软删后重新注册、事件发布）；e2e 注销 |
| B5 文件、审计与任务 | `uploadFile`、`getFile`、`getFileContent`、`avatarFileId` 的 `exists` 校验；`listAuditLogs`；周期任务 | 集成（MinIO、类型识别、`purpose` 顺序、超限清理、签名地址）；e2e 上传 |
| B6 共存与交付 | 验证角色装配与事件订阅、模块生成器、Makefile / compose / workflow / Dockerfile、§14.5 B6 文档、压测 | 契约 §11 全量；互通测试；`make ci`、`make test-e2e`、`make image` 本地通过；CI 全绿 |

---

## 16. 待决策

| # | 问题 | 建议 |
|---|---|---|
| 1 | 业务域错误码 | 已登记于约定 §2.4；新增条目先在约定中登记再实现 |
| 2 | 验证角色是否完全跳过 identity 迁移 | 跳过（两个 goose provider）；若希望结构简单，可改为单目录、验证角色也建表但不使用 |
| 3 | CORS 实现 | 自写中间件（约定头集合固定，约 60 行），减少一个依赖 |
| 4 | 演示账号密码 | `Passw0rd!`，仅非生产 |
| 5 | B2 一次下线 v1 身份链路 | 接受：B2 到 B3 之间服务只有 health 与 notes，前端联调从 B3 开始 |
| 6 | 对象存储直传 | 后续以「预签名 POST policy（`content-length-range`）+ 上传确认接口（服务端 HEAD 取大小、类型）」实现 |
| 7 | Connect / gRPC | 模板不带服务端代码，只在 `docs/architecture.md` 说明接入方式；monorepo 实际出现服务间调用时再加 |
| 8 | 私有限流规则参数 | `{OTEL_SERVICE_NAME}-ip` 300 次 / 分钟、突发 100；`{OTEL_SERVICE_NAME}-user` 120 次 / 分钟、突发 60 |

---

## 17. 风险

| 风险 | 影响 | 应对 |
|---|---|---|
| kin-openapi 校验 3.1 文档时：含内部 `$ref` 且无 2020 专有关键字的 schema 回落 3.0 校验器，其余走 JSON Schema 2020-12 校验器，两种错误结构不同；2020 路径的错误是树形，`required` 指针指向父对象；默认不校验 email / uri / uuid 格式 | 字段路径、`rule` 缺失或格式被放行 | 运行时只用 bundler 输出的全内联校验文档；适配器同时处理两种结构并递归展开；缺失字段名取自 `kind.Required.Missing`；启动时注册格式；B2 对两种文档跑同一组用例 |
| oapi-codegen 的 3.1 支持较新 | `type: [T, "null"]`、`oneOf` 生成异常 | spec 只用简单形态（可空字段用 type 数组，不用 `oneOf` 多态）；契约测试 + 响应对照 spec 兜底 |
| 刷新轮换与宽限期暂存的原子性 | 并发刷新产生两对令牌，或宽限期取不到令牌对 | 行锁 + 暂存写在提交前，写失败回滚（§6.1）；集成测试并发刷新 |
| Redis 故障时共享限流规则与吊销检查返回 503 | 登录类接口与所有鉴权接口不可用 | 生产 Redis 必填且纳入 ready 检查；告警规则覆盖 Redis 可用性 |
| B2 改动面大（平台层替换 + 下线 v1 身份代码 + 删除邮件与 modulegen） | 单批难以 review | B2 内部按「删除 → 平台层 → notes」三次本地提交的粒度组织（每次提交仍需用户授权），每步 `make check` 通过 |
| 标准库 `uuid.UUID` 无 Scanner / Valuer | sqlc、oapi-codegen 生成代码类型不匹配 | sqlc `overrides`、oapi-codegen `type-mapping`；B2 集成测试覆盖 |
| PostgreSQL 18 才有 `uuidv7()` | 低版本无法迁移 | 约定 PostgreSQL ≥ 18；应用侧始终生成 id，默认值只服务手工插入 |

---

## 附：版本核对来源（2026-11-17）

- Go module proxy（`proxy.golang.org/<module>/@latest`）：chi 5.5.0、oapi-codegen 2.8.2、oapi-codegen/runtime 1.9.0、kin-openapi 0.153.0、huma 2.41.0（未采用）、pgx 5.13.0、goose 3.30.1、river 0.51.2、rivercontrib/otelriver 0.12.0、go-redis 9.23.0、lestrrat-go/jwx v3.4.0、golang-jwt 5.3.1（不再使用）、go-playground/validator 10.30.6、caarlos0/env 11.4.2、go-i18n 2.7.0、x/crypto 0.59.0、x/text 0.44.0、sentry-go 0.50.0、otel 1.47.0、otel/sdk/metric 1.47.0、otel/exporters/prometheus 0.69.0（`@v/list`）、prometheus/client_golang 1.25.0、otelhttp 0.72.0、contrib/exporters/autoexport 0.72.0、otelslog 0.21.0、otelpgx 0.13.0、go-mail 0.8.2、aws-sdk-go-v2/service/s3 1.115.0、sqlc 1.31.1、testify 1.12.1、go.uber.org/mock 0.6.0、testcontainers-go 0.45.0、gotestsum 1.13.0、go-test-coverage 2.23.0、x/vuln 1.10.0、golangci-lint 2.15.0、emersion/go-smtp 0.26.0；connect 1.21.1、otelconnect 0.12.0（文档中引用）
- go.dev（`/dl/?mode=json`）：go1.27.4
- Docker Hub：`postgres:18.7-alpine`、`redis:8.10.3-alpine`（8.12 仅有 rc）、`golang:1.27.4`
- npm registry：`@scalar/api-reference` 1.80.0
- 未在本日复核：prek 0.5.10（GitHub API 未返回，沿用 2026-11-11 核对结果）
- oapi-codegen 2.8.2 README（OpenAPI 3.1 支持）与 kin-openapi 0.153.0 README（3.1 文档启用 JSON Schema 2020-12 校验器），读自本机模块缓存
