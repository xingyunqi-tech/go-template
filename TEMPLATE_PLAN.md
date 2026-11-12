# go-template 改造方案

> 目标：把 go-template 从「evrone/go-clean-template 克隆」改造成 Go 后端模板：
>
> 1. 技术栈取当前最新稳定版；
> 2. 与 nestjs-template **API 契约一致**，vite-vue3-temp、uniapp-template 通过 Orval 生成的前端可无改动切换；在不破坏契约的前提下，采纳比 nestjs-template 更好的方案（§5）；
> 3. 工程配置采用业界当前最佳实践（§10），测试分五层（§10.6）；
> 4. CI 结构参照 nestjs-template（§11）。
>
> 编写日期：2026-11-11。版本数据来自 Go module proxy（proxy.golang.org）、GitHub Releases API、go.dev、endoflife.date 当日查询结果，见文末「附」。
>
> 与 fastapi-temp/TEMPLATE_PLAN.md 的关系：§4 API 契约、§6 认证与权限、§7 数据模型、§9 环境变量、§11 CI 对齐要求与之语义一致，两份文档描述的是同一份契约；差异只在实现语言。
>
> 2026-11-11 按 fastapi-temp M0–M2 实现（`db14f72`、`fc385b7`、`303bf57`）中确认的契约细节修订：事务规则（§5 第 8 项）、§4.1 路由 / 请求体 / 安全头 / CORS 细节、§6 令牌与错误文本、§4.3「共享产物」、测试隔离与本机 Docker（§10.6）、阶段流程（§12）。springboot-template/TEMPLATE_PLAN.md 同步修订。
>
> 2026-11-11 按 fastapi-temp M3（基础设施，`303bf57` 之后的未提交工作区，仍在编辑中）与 nestjs `02fdb4d` 源码逐项核对修订：限流（§4.1.3）、files / health / metrics 契约（§4.6）、§5 第 4 项、§8 基础设施、§9 变量、§10.6 Redis / 文件 / 邮件测试隔离、§12 M3 验收、§14 风险。springboot-template/TEMPLATE_PLAN.md 同步修订。

---

## 0. 现状

| 项 | 状态 |
|---|---|
| 上游 | `evrone/go-clean-template`，远程名 `upstream`，分支 master，HEAD `c27e974`（2026-10-26，「go1.27.3 and security updates」） |
| 本地 | 无本地提交，无 `origin` |
| 语言 / 框架 | `go 1.27`；Fiber v3.6.1（fasthttp）；pgx v5.11.0 + squirrel v1.5.4；golang-migrate v4.20.1（`-tags migrate` 时应用启动即迁移）；zerolog；caarlos0/env v11.4.1；go-playground/validator；swag 注解生成 Swagger 2.0 |
| 示例业务 | translation（调用 Google 翻译）、task、user（JWT 单令牌 24h）；同一组用例暴露为 REST、gRPC、RabbitMQ RPC、NATS RPC 四种入口 |
| 分层 | `internal/{entity,usecase,repo,controller}` + `internal/app` 组装；接口集中在 `usecase/contracts.go`、`repo/contracts.go`；`pkg/` 放基础设施封装（httpserver、postgres、logger、tracing、jwt、grpcserver、rabbitmq、nats） |
| 工具链 | go.mod `tool` 指令管理 golangci-lint v2.15.0、gofumpt、gci、mockgen、govulncheck、swag、protoc 插件、migrate CLI，导致 go.mod 间接依赖数百行 |
| 工程配置 | `.golangci.yml`（v2 格式，约 50 个 linter）；Makefile（带 help）；Dockerfile 三阶段、最终 `scratch`、写死 `GOARCH=amd64`；compose 含 postgres 18.6、rabbitmq、nats、jaeger、nginx；`integration-test/` 以独立容器在 compose 网络内黑盒调用 HTTP / gRPC / AMQP / NATS |
| CI | 单个 `ci.yml`（只在 PR 触发）：golangci-lint、yamllint、hadolint、dotenv-linter、nancy（`continue-on-error`）、单测 + codecov bash 脚本、compose 集成测试；Actions 用浮动 tag；依赖更新用 Dependabot |
| 文档 | README（英 / 中 / 俄）、CLAUDE.md（AGENTS.md 软链接） |

上游可复用：分层与依赖方向、`contracts.go` 接口集中写法、usecase / repo 的 `tracing.go` 链路装饰器、`entity` 哨兵错误 + 分层包装错误风格、`internal/app` 组装与优雅停机骨架、`pkg/postgres`（pgxpool + otelpgx）、`pkg/tracing`、Makefile help 写法、`.golangci.yml` 的 linter 集合、mockgen 用法、`integration-test/` 的黑盒思路。其余按本方案重建。

### 0.1 删除清单

| 删除 | 原因 |
|---|---|
| `internal/controller/{grpc,amqp_rpc,nats_rpc}`、`pkg/{grpcserver,rabbitmq,nats}`、`docs/proto/`、protoc 相关 Makefile 目标与 tool | 前端只走 HTTP + OpenAPI；nestjs 无对应能力。需要内部 RPC 时再按业务单独引入 |
| translation / task 示例域、go-googletrans、旧迁移 | 示例业务，替换为 §1 范围 |
| `internal/controller/restapi/`（含 middleware、v1 request / response DTO） | 由 `internal/controller/http/` + 生成代码替换 |
| `internal/repo/persistent/`（squirrel 实现）、`internal/repo/webapi/translation_google.go` | 由 `internal/repo/postgres/`（sqlc）与新的 webapi 客户端替换 |
| `config/`（顶层目录） | 迁到 `internal/config/`，变量名改为 nestjs 名称 |
| `pkg/jwt`、`pkg/httpserver`（Fiber 版）、`pkg/logger`（zerolog 版） | 按 §2 重写为 `pkg/jwtx`、net/http 版 httpserver、slog 版 logger |
| `internal/app/migrate.go`（`-tags migrate` 启动即迁移） | 迁移改为一次性 `migrate` 子命令，见 §5 第 3 项 |
| Fiber、`gofiber/contrib/*`、swag、`docs/swagger.*`、`docs/docs.go`、`docs/img/` | 改为 net/http + spec-first，见 §2；`docs/` 重建为中文文档 |
| zerolog | 改用标准库 slog |
| `nginx/`、rabbitmq / nats / jaeger 的 compose 服务、`docker-compose-integration-test.yml` | 部署方式与 nestjs 一致（§11），观测后端不随模板分发 |
| README_RU.md、README_CN.md | README 统一为中文一份 |
| `.github/dependabot.yml`、`ci.yml` | 改为 Renovate + check / deploy / cleanup-registry 三个 workflow |

LICENSE（MIT，evrone 版权声明）保留。

---

## 1. 范围

### 1.1 本期做（对齐 nestjs-template `src/core` + `src/infrastructure` + 通用模块）

- 统一响应 / 错误 / 校验错误格式、i18n、Request ID、CORS、安全头、限流
- 认证：邮箱、手机号密码、短信验证码、微信（小程序 / App / 手机号）、支付宝；JWT 双令牌 + 刷新轮换 + 会话
- 用户管理、RBAC 权限、角色 / 状态
- 文件上传（local / s3 / s3-presigned）、私有文件访问
- 健康检查、Prometheus 指标、结构化日志、Sentry、OpenTelemetry
- Redis 缓存 + 分布式锁、任务队列、定时任务、审计日志、告警、账号清理
- 邮件（注册确认、找回密码；nestjs 的 Handlebars 模板改写为 Go `html/template`，中英双语）
- Seed 数据：角色、状态、权限、管理员与示例用户，账号与 nestjs seed 一致，前端模板开箱可登录
- 通用业务模块：config、regions、notifications、addresses、favorites、feedbacks、consents、app-profile
- 模块代码生成器（对应 nestjs 的 `generate:resource`）
- Docker / Compose 分环境、deploy/manage.sh、GitHub Actions

### 1.2 本期不做

nestjs-template `src/modules` 里的考试刷题业务：exams、questions、practice、papers、imports、errata、wrong-book、study-stats、question-notes、ai、game-auth、monitor。orders、payments、shares 见 §13 待决策。这些接口仍在 OpenAPI 文档里（文档与 nestjs 逐字节一致），处理方式见 §4.3。

---

## 2. 技术栈（当前最新稳定版）

| 类别 | 选型 | 版本（2026-11-11） | 说明 |
|---|---|---|---|
| 语言 | Go | **1.27**（1.27.4） | go.mod `go 1.27.0` + `toolchain go1.27.4`；1.27 起标准库有 `uuid`（含 `NewV7`）；`encoding/json/v2` 本期不用，见 §13 第 6 项 |
| HTTP 服务器 | 标准库 `net/http` | 随 Go | `http.Server` 超时、`Shutdown` 优雅停机；OTel、中间件生态都基于 `http.Handler` |
| 路由 | **go-chi/chi v5** | 5.5.0 | 理由见 §2.1；oapi-codegen `chi-server` 直接生成注册代码 |
| 接口生成 | **oapi-codegen v2（strict server）** | 2.8.2 | 输入 `openapi/nestjs-baseline.json`（nestjs 导出，三个后端同名同内容），生成类型 + `StrictServerInterface` + chi 路由注册；运行时 `oapi-codegen/runtime` 1.9.0 |
| 路由查找 | kin-openapi（`routers.FindRoute`） | 0.153.0 | 按 operationId 查 policy；请求体与查询参数的类型 / required 不用 `openapi3filter.ValidateRequest` 校验，统一由下一行的执行器按 class-validator 规则输出（类型错误与其它约束消息一起拼接），见 §4.3 |
| 请求校验（约束与消息） | `openapi/validation.json` + 自写执行器 `pkg/validatorx`（含 validator.js 语义移植） | — | nestjs 侧脚本导出 class-validator 元数据，按装饰器顺序执行并输出 class-validator 原文消息；以 `tests/contract/fixtures/validation-cases.json`（nestjs 实测结果）为门禁，见 §4.3 |
| 配置校验 | go-playground/validator v10 | 10.30.6 | 仅用于配置结构体与少量内部校验 |
| 数据库驱动 | pgx v5（pgxpool） | 5.12.0 | |
| SQL | **sqlc** | 1.31.1 | 写 SQL 生成类型安全代码（`sql_package: pgx/v5`）；动态筛选 / 排序的少数列表用 squirrel 1.5.4（仓库已有，2023 后无发布但功能稳定）。sqlc 的 PostgreSQL 解析器依赖 cgo，用官方发布的二进制 / 镜像运行，不放进 `tools/go.mod` |
| 迁移 | **goose v3** | 3.30.0 | 理由见 §2.1；库方式嵌入二进制，`migrate` 子命令一次性执行 |
| 数据库 | PostgreSQL | 18（镜像 `18.6-alpine`） | 独立部署用 18；与 nestjs 共库、parity 对照时用 17（`17.11-alpine`，同 nestjs CI）。endoflife.date 已列出 18.7 / 17.12（2026-11-09），但 Docker Hub 上 `18.7-alpine` 查询为 404，镜像以 Docker Hub 实际可用标签为准，由 Renovate 跟进 |
| 缓存 / 锁 / 限流 | Redis + go-redis v9 | 8.x / 9.23.0 | nestjs 现为 Redis 7.4，与其共用实例时按 7.x 兼容使用（Redis 服务端最新版本本次未复核） |
| 队列 + 定时 | **River**（Postgres） | 0.51.2 | 事务内入队、内置周期任务与 leader 选举；理由见 §2.1 |
| JWT | golang-jwt/jwt v5 | 5.3.1 | |
| 密码哈希 | golang.org/x/crypto（argon2、bcrypt） | 0.58.0 | 见 §5 第 1 项 |
| UUID | 标准库 `uuid` | 随 Go 1.27 | 新表主键 `uuid.NewV7()`；pgx 编解码在 M0 验证，不行则回落 google/uuid 1.6.0 |
| i18n | 自写加载器 + golang.org/x/text/language | 0.44.0 | 直接读取 nestjs 的 `i18n/{en,zh}/*.json`（`embed`），`language.Matcher` 解析 `Accept-Language` q 值 |
| 配置 | caarlos0/env v11 | 11.4.2 | 只读环境变量，变量名与 nestjs 一致；缺失项聚合报错 |
| 日志 | 标准库 `log/slog` + otelslog 桥 | 随 Go / 0.21.0 | JSON handler；trace_id / span_id 由自写 handler 包装注入 |
| 指标 | prometheus/client_golang | 1.25.0 | 指标名与 nestjs 一致 |
| 链路 | OpenTelemetry Go（otel、otelhttp、otelpgx、go-redis redisotel） | 1.47.0 / 0.72.0 / 0.13.0 / 随 go-redis（未单独核实） | `OTEL_ENABLED` 开关，OTLP 导出 |
| 错误上报 | sentry-go（含 sentryhttp） | 0.50.0 | |
| 邮件 | wneessen/go-mail + `html/template` | 0.8.2 | |
| 对象存储 | aws-sdk-go-v2/service/s3 | 1.114.10 | S3 兼容，阿里云 OSS 走 endpoint；presign 用于 s3-presigned |
| 第三方登录 | 微信：自写 HTTP 客户端（jscode2session、OAuth、手机号两三个接口）；支付宝：smartwalle/alipay v3 | 3.2.31 | 支付宝 RSA2 签名自写成本高，用库 |
| 短信 | 阿里云 Dysms：自写签名 HTTP 客户端 | — | 官方 Go SDK 版本本次未核实 |
| CLI | 标准库 `flag` 子命令 | — | `serve` / `worker` / `migrate` / `seed` / `healthcheck` / `permissions clear-cache` / `createsuperuser`，不引入 cobra |
| 测试 | `go test` + testify + go.uber.org/mock + gotestsum | 1.12.1 / 0.6.0 / 1.13.0 | 分层见 §10.6 |
| 测试容器 | testcontainers-go（postgres、redis 模块） | 0.45.0 | |
| Lint / 格式化 | golangci-lint v2（formatters：gofumpt、gci） | 2.15.0（gofumpt 0.12.0） | 用官方发布的二进制 / golangci-lint-action 安装，不放进 `tools/go.mod`（官方不建议 `go install` / `go tool` 方式） |
| 覆盖率门槛 | vladopajic/go-test-coverage | 2.23.0 | 读取 coverprofile，按配置排除生成代码并校验总门槛 |
| 漏洞扫描 | govulncheck | 1.9.0 | 只报告代码实际调用到的漏洞 |
| Git 钩子 | prek | 0.5.10 | 与 fastapi 方案一致；发布独立二进制，无需 Python / Rust 运行时 |
| 依赖更新 | Renovate | — | gomod、Docker 镜像、GitHub Actions 一份配置 |

### 2.1 选型对比与不选的方案

**HTTP 层：spec-first（chi + oapi-codegen strict server）**

| 方案 | 版本 | 契约一致性 | 生态 / 兼容 | 结论 |
|---|---|---|---|---|
| **chi + oapi-codegen strict（spec-first）** | 5.5.0 / 2.8.2 | 以 nestjs 导出的 OpenAPI 为唯一输入，`/docs-json` 直接返回该文件，与 nestjs **逐字节一致**；schema 名、operationId、paths 顺序天然相同 | `net/http` 原生，otelhttp、sentryhttp、cors 等中间件直接可用 | **采用** |
| 标准库 ServeMux（Go 1.22+ 模式路由） | 随 Go | 同上（oapi-codegen 有 `std-http-server`） | 无第三方依赖 | 备选；差异见下 |
| Fiber v3（仓库现状） | 3.7.0 | 需 swag 注解生成 Swagger 2.0，名字和顺序都要手工对齐 | 基于 fasthttp，不兼容 `net/http`，OTel / Sentry 需专用适配，`*fiber.Ctx` 复用导致跨 goroutine 使用易出错 | 不选 |
| Gin | 1.12.0 | 依赖 swag 注解，对 schema 名、顺序的控制弱 | `net/http` 兼容 | 不选 |
| Echo v5 | 5.4.4 | 同 Gin，需要注解或手写文档 | `net/http` 兼容 | 不选 |
| Huma v2（code-first） | 2.40.0 | 由 Go 结构体生成 OpenAPI 3.1，与 nestjs 的 3.0.0 文档在 nullable、schema 命名、operationId 上都要逐项对齐，且始终是「生成后再比对」 | 好 | 不选：本项目契约源头在 nestjs，code-first 只会多一层漂移 |

chi 与标准库 ServeMux 的差异不大：chi 提供 `Route` / `Group` / `With` 子路由中间件、`chi.Walk` 遍历已注册路由（契约测试「每个 operation 都有处理器」直接用它）、`RouteContext().RoutePattern()` 作指标标签。ServeMux 在 Go 1.23+ 也有 `r.Pattern`，但没有 Walk。取 chi；不存在必须换的理由，不列入待决策。

**Go 侧不新增接口**：所有接口变更先在 nestjs 落地并导出文档，再同步到本仓库（`make openapi-sync`），生成代码后实现。本仓库不手改 `openapi/` 下的共享产物（§4.3「共享产物」）。

**SQL 访问**

| 方案 | 版本 | 说明 | 结论 |
|---|---|---|---|
| **sqlc** | 1.31.1（2026-04-22） | 写原生 SQL，编译期生成类型安全代码；camelCase 列名在 SQL 中加引号（`"firstName"`），生成字段 `FirstName`；支持 pgx/v5；读 goose 迁移文件作为 schema | **采用** |
| squirrel | 1.5.4（2023-03） | 字符串拼 SQL，无类型检查 | 仅用于动态筛选 / 排序 |
| GORM | 1.31.2 | 反射 + 隐式行为（零值更新、自动关联），与「列名 / 约束名照抄 nestjs」的精确控制相冲突 | 不选 |
| go-jet / bob | 2.16.1 / 0.50.0 | 类型安全构建器，动态查询更强；生态和资料少于 sqlc | 不选，列表筛选规模变大时再评估 |

**迁移**

| 方案 | 版本 | 说明 | 结论 |
|---|---|---|---|
| **goose** | 3.30.0（2026-11-10） | up / down 写在同一个 SQL 文件；支持 Go 函数迁移（数据回填）；`Provider` API + `embed.FS` 便于做成一次性子命令；sqlc 原生识别 | **采用** |
| golang-migrate（仓库现状） | 4.20.1（2026-09-09） | up / down 两个文件，无 Go 迁移 | 不选 |
| Atlas | 1.3.0 | 声明式 diff 强，但核心能力在其云服务 / 商业版，模板引入成本高 | 不选；可选用于 CI 的结构对比 |

**任务队列**

| 方案 | 版本 | 说明 | 结论 |
|---|---|---|---|
| **River** | 0.51.2（2026-11-09） | 基于 Postgres；`InsertTx` 与业务写入同一事务（不会出现「事务回滚了任务却发出去了」）；内置重试退避、唯一任务、周期任务、leader 选举（定时任务不需要单独 scheduler 进程） | **采用** |
| Asynq | 0.26.0（2026-02-03 后无发布） | 基于 Redis，不能与数据库事务原子提交 | 不选 |

**配置**

| 方案 | 版本 | 结论 |
|---|---|---|
| **caarlos0/env v11** | 11.4.2 | 采用：只认环境变量（与 nestjs 一致），结构体标签声明，`required` 缺失时一次返回全部错误 |
| viper | 1.21.0（2025-09 后无发布） | 不选：依赖重、多来源合并语义与本项目无关 |
| koanf v2 | 2.4.1 | 不选：多来源能力用不到 |

**其它不选**

| 方案 | 原因 |
|---|---|
| go-redis/redis_rate（GCRA） | v10.0.1 停在 2023-04；GCRA 脚本只有几十行，自写并按 nestjs 头名输出 |
| alexedwards/argon2id | 1.0.0 停在 2023；直接用 x/crypto/argon2 + PHC 字符串 |
| nicksnyder/go-i18n | 消息文件格式与 nestjs 不同；直接复用 nestjs JSON 更省事 |
| goreleaser | 2.20.1；本项目只发镜像不发二进制，buildx 足够 |
| Dependabot | 不支持跨生态分组和自动合并策略，Renovate 更灵活 |

---

## 3. 目录结构

保留 go-clean-template 的 `cmd/` + `internal/` + `pkg/` 布局和分层。

```
go-template/
├── cmd/app/main.go                 # 子命令入口：serve / worker / migrate / seed / healthcheck / ...
├── internal/
│   ├── app/                        # 组装：配置 → 基础设施 → repo → usecase → controller；生命周期与优雅停机
│   ├── config/                     # caarlos0/env 结构体，按分组拆分
│   ├── entity/                     # 纯领域对象与领域错误，不引用 pgx / http
│   ├── usecase/
│   │   ├── contracts.go            # usecase 接口（controller 依赖它）
│   │   └── {auth,user,session,permission,file,audit,alert,accountcleanup,...}/
│   ├── repo/
│   │   ├── contracts.go            # repo 端口（usecase 依赖它）
│   │   ├── postgres/
│   │   │   ├── queries/*.sql       # sqlc 输入
│   │   │   ├── sqlcgen/            # sqlc 生成（不手改）
│   │   │   └── *.go                # 端口实现：sqlcgen ↔ entity 映射、唯一约束错误翻译
│   │   ├── redis/                  # 缓存、锁、会话缓存、短信验证码
│   │   └── webapi/{wechat,alipay,sms,mail,storage}/
│   ├── controller/http/
│   │   ├── gen/                    # oapi-codegen 生成：类型 + StrictServerInterface + chi 注册（不手改）
│   │   ├── v1/                     # StrictServerInterface 实现，按 nestjs controller 分文件
│   │   ├── v1/view/                # 手写响应视图类型（me / admin / public），控制键是否出现与 null，见 §4.4
│   │   ├── policy.go               # operationId → 鉴权方式 / RBAC / 权限 / 限流 / 审计 / 事务
│   │   ├── middleware/             # requestid、logging、recover、cors、secureheaders、bodylimit、i18n、throttle、auth、validate、tx、metrics
│   │   ├── response.go             # 成功信封、错误信封
│   │   └── docs.go                 # /docs-json（embed nestjs-baseline.json 原样输出）、/docs（Scalar）
│   └── worker/                     # River workers 与周期任务
├── pkg/                            # 与业务无关的封装
│   ├── httpserver/ postgres/ redis/ logger/ tracing/ metrics/
│   ├── i18n/ jsontime/ password/ jwtx/ ratelimit/ lock/ validatorx/
├── migrations/                     # goose SQL（含 River 迁移）
├── seeds/                          # seed 数据（与 nestjs seed 账号一致）
├── i18n/{en,zh}/*.json             # 从 nestjs 复制，保持同名同键
├── templates/{mail,module}/        # 邮件模板、代码生成器模板
├── openapi/
│   ├── nestjs-baseline.json        # 共享产物：nestjs /docs-json 导出，逐字节入库，/docs-json 原样返回
│   ├── validation.json             # 共享产物：nestjs class-validator / class-transformer 元数据（结构见 §4.3）
│   ├── SOURCE                      # 共享产物：repository / branch / commit / validation_script / environment
│   ├── overlay.yaml                # 仅作用于代码生成输入（nullable / required 修正、x-go-type），不改变对外文档
│   ├── scope.yaml                  # 本期实现的 operationId 清单
│   └── oapi-codegen.yaml
├── db/nestjs-schema.sql            # 共享产物：nestjs 迁移后的规范化表结构快照（每个对象一行，§7）
├── tests/
│   ├── contract/                   # 第 4 层；fixtures/validation-cases.json 为共享产物
│   ├── parity/                     # 第 5 层，用例与 fastapi-temp tests/parity 一一对应
│   └── e2e/                        # 第 3 层；单测、集成测试与源码同目录，见 §10.6
├── tools/go.mod                    # 纯 Go 开发工具单独模块，见 §10.1
├── docker/                         # 含共享的 nestjs.compose.yaml（§4.3）
├── deploy/
├── scripts/                        # 含共享的 openapi-sync.sh、schema_snapshot.py、gen_validation_cases.py、nestjs/validation-cases.ts
├── .github/{workflows,renovate.json5,zizmor.yml,ISSUE_TEMPLATE}
├── Dockerfile  .dockerignore  .golangci.yml  .pre-commit-config.yaml  sqlc.yaml  .editorconfig  .env.example
├── .vscode/{settings,extensions}.json
├── Makefile
├── README.md  CLAUDE.md  AGENTS.md -> CLAUDE.md  LICENSE
└── docs/
```

### 3.1 分层规则（沿用 go-clean-template，对应 nestjs 六边形架构）

```
controller/http/v1  →  usecase（接口在 usecase/contracts.go）  →  repo（端口在 repo/contracts.go）
          ↓                     ↓                                         ↓
      gen 类型              entity                               sqlcgen / go-redis / webapi
```

| 规则 | 说明 |
|---|---|
| entity 不引用 pgx、sqlcgen、gen | 与 nestjs「domain 不引用 ORM」一致 |
| controller 只做 gen 请求类型 ↔ usecase 入参、entity → view 类型的转换 | 不写业务；响应一律用 `v1/view` 中的手写类型输出，不直接序列化生成的响应结构体 |
| usecase 只依赖 repo 端口 | 单测用 mock / fake 端口 |
| 配置只从 `config.Config` 读取 | 禁止在业务代码里 `os.Getenv` |
| 依赖注入 | 手写构造函数，在 `internal/app` 组装；不引入 wire / fx |

---

## 4. API 契约（与 nestjs-template 对齐）

### 4.1 全局

| 项 | 约定 |
|---|---|
| 前缀 | `/api/v1/...`（`API_PREFIX`，默认 `api`）；`GET /`、`GET /api/health`、`GET /metrics`（`METRICS_PATH`）不带版本 |
| 端口 | 默认 `APP_PORT=3000`，与 nestjs 及两个前端的 Orval target / dev 环境（`localhost:3000`）一致 |
| 成功响应 | `{"code": 200, "message": "success", "data": <payload>}`；**所有成功请求 HTTP 200**（含 POST，含文档中以 `@ApiCreatedResponse` 标注 201 的接口），**唯一例外是 `POST files/upload` 返回 HTTP 201**（body 中 `code` 仍为 200）；`void` 接口不输出 `data` 键 |
| 已是信封的返回 | 原样透传 |
| 跳过包装 | 路由级（policy 中 `raw: true`）：`/metrics`、SSE、支付回调 |
| 错误响应 | `{"code": <HTTP 状态>, "message": <已翻译文本>, "data": <对象或 null>}`，无独立业务码 |
| 错误消息与 data 翻译 | 规则来自 nestjs `src/common/filters/all-exceptions.filter.ts`，见 §4.1.1 |
| 业务错误 | `entity.BusinessError{Key, Details}` → `message` = `common.<errorKey>` 的翻译，`data: {...details, "errorKey": key}`（data 不翻译），状态码查表（键表从 nestjs `business-error-keys.ts` 复制） |
| 校验错误 | 422，`message` = i18n `common.unprocessableEntity`（en `Validation failed` / zh `数据验证失败`），`data: {字段: "约束消息, 逗号拼接"}`：同字段所有失败约束都输出，按 class-validator 装饰器顺序（在 `@Transform` 之后执行）以 `", "` 拼接，约束与消息来源见 §4.3 的 `validation.json`；嵌套对象为嵌套 map；顶层字符串值同样按 §4.1.1 尝试 `common.<value>`（无对应键时原文输出）；未知字段剔除不报错 |
| 非法 `:id` | 400，`message` 为 `common.badRequest` 译文，`data: {"id": <common.invalidId 译文>}`（en `Invalid id` / zh `ID 格式不正确`），对应 nestjs `ParseIdPipe`（`^\d+$`、1 ≤ id ≤ 2147483647）；路径参数绑定失败一律走此分支，**不返回 422** |
| 唯一约束冲突 | 按约束名查表，见 §4.1.2 |
| 路由语义 | 与 Express 一致：`API_PREFIX` 下及排除路由（`/`、`/metrics`）的未匹配路由（含路径存在但方法不符）→ **404**（不是 405，不带 `Allow` 头），`message` 为 Nest 原文 `Cannot <METHOD> <originalUrl>`（含查询串，无翻译键，原文输出），`data: null`；忽略末尾斜杠（`/users` 与 `/users/` 命中同一路由，不重定向）；路径匹配不区分大小写（Express 5 默认关闭 `case sensitive routing`）；按原始（百分号编码）路径匹配，匹配前不解码（`/api/%68ealth` 不命中 `/api/health`，返回 404），路径参数在匹配后再解码；其它路径（如 `/nope`、`POST /docs-json`）nestjs 返回 Express 默认 HTML 404，不属于契约，本服务统一返回同样的 JSON 信封，parity 不比对 |
| 语言 | 见 §4.2 |
| Request ID | 入站 `X-Request-Id` **恰有一个**且匹配 `^[\w.:-]{1,128}$` 则沿用，否则（缺失、多个同名头、格式不符）生成 UUID；写日志、审计，并回写响应头。Node 把多个同名头合并为 `a, b`，必然不匹配正则，因此多个头等同于非法 |
| CORS | 生产按 `FRONTEND_DOMAIN` 白名单，`credentials: true`，允许方法 `GET/POST/PUT/PATCH/DELETE/OPTIONS`、允许头 `Content-Type`、`Authorization`、`Accept-Language`；非生产反射来源与请求头，允许方法为 `cors` 包默认值 `GET,HEAD,PUT,PATCH,POST,DELETE`，预检响应 `Vary: Origin, Access-Control-Request-Headers`；两种环境 `exposedHeaders` 均为 `X-Request-Id`、`Retry-After`、`X-RateLimit-Limit`、`X-RateLimit-Remaining`、`X-RateLimit-Reset`；CORS 在路由之前处理，**任何 `OPTIONS` 请求（含未知路径、无 `Origin`）都返回 204 空体**，不进入路由与 404 |
| 安全头 | 与 nestjs helmet 配置输出相同的头集合与取值（`Content-Security-Policy`、`Cross-Origin-Resource-Policy: cross-origin`、`Origin-Agent-Cluster`、`Referrer-Policy`、`Strict-Transport-Security`、`X-Content-Type-Options`、`X-DNS-Prefetch-Control`、`X-Download-Options`、`X-Frame-Options`、`X-Permitted-Cross-Domain-Policies`、`X-XSS-Protection`），**HSTS 在所有环境都发送**；取值从 nestjs 实际响应复制，parity 逐头比对 |
| 请求体 | JSON 上限 256kb，超限 413 `{code: 413, message: "request entity too large", data: null}`（body-parser 原文，不翻译）；JSON 非法或顶层为原始值（`null`、字符串、数字等）→ 400、`data: null`，`message` 为解析器错误文本，由各实现决定，parity 只比对 `code` 与 `data`；**请求体解析在路由与鉴权之前**（与 Express 全局 body-parser 一致）：未知路径带非法 JSON 返回 400 而不是 404，鉴权路由不带令牌、请求体非法返回 400 而不是 401；契约只约束 `application/json`，其它 content-type（nestjs 另接受 urlencoded 表单）不属于契约，本服务按空对象校验 |
| 限流 | 规则同 nestjs（默认 100 次 / 60s，鉴权路由按用户、否则按 IP，IPv6 按子网），`THROTTLE_ENABLED` 开关；放行时带 `X-RateLimit-Limit` / `X-RateLimit-Remaining` / `X-RateLimit-Reset`，429 只带 `Retry-After`；头名与出现条件属于契约，具体数值随算法不同（见 §5 第 4 项），不属于契约；细则见 §4.1.3 |
| 字段命名 | camelCase（由 OpenAPI 文档决定） |

#### 4.1.1 错误消息与 data 翻译（nestjs `all-exceptions.filter.ts`）

翻译文件为 nestjs `src/i18n/{en,zh}/common.json`；「尝试翻译」指 `common.<key>` 存在则用译文，否则保留原值。

| 异常类型 | message | data |
|---|---|---|
| 429 限流 | `common.tooManyRequests` | `null` |
| BusinessException | `common.<errorKey>` | `{...details, errorKey}`，不翻译 |
| 其它 HTTP 异常，带自定义 `message`（如 `ForbiddenException('Missing permissions: …')`、未匹配路由的 `Cannot <METHOD> <url>`） | 尝试翻译 `message`，无对应键时原文输出 | `errors` 字段，无则 `null` |
| 其它 HTTP 异常，无自定义 `message`，`error` 不是默认描述（如 `throwNotFoundError('sessionNotFound')`） | 尝试翻译 `error`（→ `Session not found` / `会话不存在`） | 同上 |
| 其它 HTTP 异常，无 `error` / `message` 或为默认描述 | 状态码默认消息：400/401/403/404/409/410/422/429/500 → `common.<key>`（`badRequest`、`unauthorized`、`forbidden`、`notFound`、`conflict`、`gone`、`unprocessableEntity`、`tooManyRequests`、`internalServerError`）；422 校验错误即 `common.unprocessableEntity` | 同上 |
| 唯一约束冲突 | 表中有 `error` 时尝试翻译它，否则状态码默认消息 | 表中的 `errors` |
| 未知异常 | `common.internalServerError` | `null` |

非 BusinessException 的 HTTP 异常与唯一约束冲突，`data` 中**顶层字符串值**尝试翻译（`{email:'notFound'}` 输出为 `{email:'Not found'}` / `{email:'资源不存在'}`），数字等非字符串值与嵌套 map 原样输出。下文 §6 中写作 `{field: 'key'}` 的错误均指翻译前的键。

#### 4.1.2 唯一约束 → 响应（照抄 nestjs `src/common/utils/exceptions.util.ts` 的 `UNIQUE_CONSTRAINT_RESPONSES`）

| 约束名 | 状态 | 响应 |
|---|---|---|
| `UQ_e12875dfb3b1d92d7d7c5377e22` | 422 | `errors: {email: 'emailAlreadyExists'}` |
| `user_phone_key` | 422 | `errors: {phone: 'phoneAlreadyExists'}` |
| `user_wechatOpenId_key` | 422 | `errors: {wechatOpenId: 'wechatOpenIdAlreadyExists'}` |
| `user_alipayOpenId_key` | 422 | `errors: {alipayOpenId: 'alipayOpenIdAlreadyExists'}` |
| `REL_75e2be4ce11d447ef43be0e374` | 422 | `errors: {photo: 'alreadyExists'}` |
| `UQ_favorite_user_target` | 409 | `error: 'alreadyFavorited'` |
| `UQ_question_subjectId_contentHash` | 409 | `error: 'duplicateQuestion'`（考试业务，本期不涉及，表中保留） |
| `UQ_order_orderNo` | 500 | — |
| `UQ_share_shareCode` | 500 | — |
| 未登记 | 409 | — |

Go 侧按 `pgconn.PgError.Code == 23505` 取 `ConstraintName` 查表，表内容随 nestjs 同步。

#### 4.1.3 限流（nestjs `src/infrastructure/throttler/`、@nestjs/throttler 6.7.1 `ThrottlerGuard`）

| 项 | 规则 |
|---|---|
| 作用范围 | 全局守卫：所有已匹配路由都计数，包括 `/api/v1/health*`、`/api/health`、`/`、`/metrics`、`GET files/:path`（nestjs 无 `@SkipThrottle`）；未匹配路由（404）与 `OPTIONS` 预检不计数；顺序在鉴权之前（§4.3「请求处理顺序」） |
| 规则 | 默认 `THROTTLE_LIMIT` 次 / `THROTTLE_TTL` 秒；路由级覆盖见 §4.6 auth 表「限流」列（`@Throttle({default: {limit, ttl}})`）；`THROTTLE_ENABLED=false` 时完全跳过，不输出任何限流头 |
| 计数主体 | 路由声明了 `AuthGuard('jwt')`（policy 为 jwt）且 `Authorization` 为 `Bearer <token>`、令牌用 `AUTH_JWT_SECRET` 验签且未过期时为 `user:{id}`（只验签，不查会话）；其余情况（含 optional-jwt、refresh、监控令牌路由，令牌无效或过期）为 `ip:{normalizeIp(ip)}` |
| `normalizeIp` | IPv4 原样；去掉 IPv6 zone（`%eth0`）；`::1` 原样；IPv4 映射地址（`::ffff:a.b.c.d`）转为 IPv4；其余 IPv6 按 /64 取网络地址，输出小写 RFC 5952 压缩形式加前缀（`2001:db8:abcd:12:1:2:3:4` → `2001:db8:abcd:12::/64`）；非法字符串原样。用例照搬 fastapi-temp `tests/unit/test_observability.py::TestNormalizeIp` |
| 客户端 IP | `TRUST_PROXY` 层数，见 §9 |
| 响应头 | 放行时在守卫处写入 `X-RateLimit-Limit`（本条规则的 limit）、`X-RateLimit-Remaining`、`X-RateLimit-Reset`（秒，整数），之后鉴权 / 校验失败的 401 / 403 / 422 响应同样带这三个头；超限 429 只带 `Retry-After`（秒，整数），不带 `X-RateLimit-*`；body `{code: 429, message: common.tooManyRequests 译文, data: null}` |
| Redis 键 | `throttle:` + `sha256hex("<Controller>-<handler>-default-<tracker>")` + `:default`（nestjs 另有 `…:default:block` 封禁键）。`<Controller>-<handler>` 是 nestjs 的类名与方法名，按 operationId 查表：复制 fastapi-temp `src/app/infrastructure/throttler/nest_handlers.json`（如 `loginWithEmail` → `AuthController-login`、`healthCheck` → `HealthController-check`、`/metrics` → `MetricsController-index`、`GET files/:path` → `FilesLocalController-download`），表外路由回落为 operationId |
| Redis 故障 | 单条命令超时 500ms；出错时本次改用进程内计数，每次回落 `throttler_storage_fallback_total` +1；首次降级记 error 日志并告警（type `throttler`、level CRITICAL、dedupKey `throttler-storage-fallback`），恢复时记 info 日志；`REDIS_ENABLED=false` 时直接用进程内计数 |

### 4.2 语言头：统一使用 `Accept-Language`

| | Accept-Language | x-custom-lang |
|---|---|---|
| 性质 | HTTP 标准头（RFC 9110），支持 q 值多语言优先级 | nestjs-boilerplate 自定义头 |
| 浏览器 | 自动携带用户系统语言，首次访问即可用 | 必须前端手动设置 |
| 缓存 / CDN | 可用标准 `Vary: Accept-Language`（本契约不发送，见下） | 需自定义 Vary |
| CORS | 属于安全列表头，不触发额外预检 | 自定义头，跨域时触发预检 |

**所有模板统一使用 `Accept-Language`，不使用 `x-custom-lang`。** 后端按 q 值匹配已支持语言（`zh-CN` 等区域标签宽松匹配到 `zh`），匹配不到回落 `APP_FALLBACK_LANGUAGE`。OpenAPI 全局参数 `Accept-Language` 已在 nestjs 文档中声明。

nestjs 响应不带 `Content-Language`，也不带 `Vary: Accept-Language`（只有 CORS 中间件加的 `Vary: Origin`），本服务同样不发送这两项；`Content-Language` 不属于契约，parity 比对时排除；`Vary` 只按 §4.1 CORS 规则在 CORS 用例中比较。

配套仓库已同步：nestjs-template 改用 `AcceptLanguageResolver` 并去掉 `APP_HEADER_LANGUAGE`；uniapp-template 请求头改为 `Accept-Language`；vite-vue3-temp 已在发送该头。

### 4.3 OpenAPI（Orval 依赖）：spec-first

**文档来源与输出**

- `openapi/nestjs-baseline.json` = nestjs `/docs-json` 的原始字节（OpenAPI 3.0.0），导出时 nestjs 以 `NODE_ENV=development`、`FILE_DRIVER=local` 启动（不同驱动注册不同的上传 controller，文档不同）。由 `make openapi-sync` 与其它共享产物一起产出，见下「共享产物」
- **`GET /docs-json`** 用 `embed` 原样返回该文件（`Content-Type: application/json`），前端对两个后端生成的代码完全相同；`/docs` 用 Scalar 页面引用 `/docs-json`；`SWAGGER_ENABLED` 控制，生产默认关闭（与 nestjs 一致）
- operationId、schema 名、paths 顺序、`bearer` 安全方案、全局 `Accept-Language` 参数、未包装的 DTO 响应 schema（前端 mutator 自己剥 `.data`）、`multipart/form-data` 的 `file` 字段，全部由 nestjs 文档决定，Go 侧无需也不能改

**nestjs 文档的局限**（spec-first 必须正视）

| 局限 | 原因 | 处理 |
|---|---|---|
| 可空字段未标 `nullable: true`，且默认 required（如 `src/core/users/domain/user.ts` 的 `email: string \| null` 只写了 `@ApiProperty({type: String})`） | nestjs 未启用 swagger CLI 插件，靠手写装饰器 | 响应**不用生成的结构体序列化**，改用手写 view 类型（§4.4）；请求类型需要时由 overlay 修正 nullable / required |
| `@Expose({groups})` 的字段在部分视图中不存在 | 分组不进文档 | view 类型按 me / admin / public 拆分 |
| 文档中没有 class-validator 约束（长度、格式、正则等） | 同上 | 约束由 `openapi/validation.json` 提供，见下 |
| 上传接口随 `FILE_DRIVER` 变化 | nestjs 按驱动注册不同 controller，只有 local 版带 operationId `uploadFile`；s3-presigned 版接收 JSON `{fileName, fileSize}` | 见下「文件上传」 |
| 鉴权方式不完整 | 文档 `security` 只有 bearer；监控令牌（`healthCheck`）、可选 JWT（如 papers）不体现 | 鉴权在实现侧（policy）声明，文档 `security` 只做交叉检查 |

**共享产物**（Python / Go / Java 三个仓库同名、同路径、逐字节相同，Make 目标同名）

| 产物 | 内容 | 生成方 |
|---|---|---|
| `openapi/nestjs-baseline.json` | nestjs `/docs-json` 原文 | `openapi-sync` 在 nestjs 固定提交上启动服务后下载 |
| `openapi/validation.json` | nestjs `scripts/export-validation.ts` 输出，结构见下「校验」 | nestjs 脚本 |
| `openapi/SOURCE` | 五行 `key=value`：`repository=xingyunqi-tech/nestjs-template`、`branch=release/exam-prep`、`commit=<契约提交，当前 02fdb4d…>`、`validation_script=<export-validation.ts 所在提交，当前 8b5f296…>`、`environment=NODE_ENV=development FILE_DRIVER=local` | `openapi-sync` |
| `db/nestjs-schema.sql` | nestjs 迁移后、§7 所列 18 张表的规范化结构快照：每个列 / 约束 / 索引 / 注释一行（`column t.c <type> NULL\|NOT NULL [DEFAULT …] [COMMENT …]`、`constraint t.name <def>`、`index t.name <def>`、`comment t '…'`），排序后输出 | fastapi-temp `scripts/schema_snapshot.py`（查询 `pg_catalog`，不用 `pg_dump`） |
| `tests/contract/fixtures/validation-cases.json` | 约 4516 条校验用例（`{id, dto, payload, expected}`，`expected` 为 `{ok: true, value}` 或 `{ok: false, status, errors}`），期望结果由真实 class-validator / class-transformer 跑出（nestjs oracle） | fastapi-temp `scripts/gen_validation_cases.py` 生成输入，`scripts/nestjs/validation-cases.ts` 在 nestjs builder 镜像中执行 |
| `docker/nestjs.compose.yaml` | nestjs 参照服务（compose project `nestjs-openapi-sync`，postgres 17.11 / redis 7.4.12 / mailpit，api 映射宿主 `NESTJS_PORT`，默认 3001；迁移 → `ENV_MODE=dev` seed → 启动） | fastapi-temp |
| `tests/parity` 用例 | 用例清单（请求、比对规则）与 fastapi-temp `tests/parity`（`test_global_contract.py`、`test_auth_parity.py`）一一对应，用例 id 相同；目标地址 nestjs 默认 3001、本服务默认 3000 | fastapi-temp 为当前基准；抽成语言无关数据文件见 §13 第 8 项 |

归属：契约内容的唯一来源是 nestjs（`02fdb4d` 契约提交 + `export-validation.ts`）；生成工具（`scripts/openapi-sync.sh`、`scripts/schema_snapshot.py`、`scripts/gen_validation_cases.py`、`scripts/nestjs/validation-cases.ts`、`docker/nestjs.compose.yaml`）由 fastapi-temp 维护，本仓库原样复制，不做改动。`make openapi-sync` 默认从同级 fastapi-temp 复制上表产物（`FASTAPI_DIR`，要求两边 `openapi/SOURCE` 相同）；需要独立重跑时执行复制来的 `scripts/openapi-sync.sh`（依赖 Docker 与 uv），产物必须与 fastapi-temp 中的逐字节相同。`export-validation.ts` 目前只在 nestjs 本地提交 `8b5f296`（`release/exam-prep` 领先 origin 1 个提交，未推送）：本地 `openapi-sync` 从 `NESTJS_DIR` 读取即可，CI 中 checkout 并固定 `validation_script` 提交要求该提交已推送到 origin，推送前 CI 只能使用入库产物。

**代码生成**（`make generate`，配置 `openapi/oapi-codegen.yaml`）

| 项 | 做法 |
|---|---|
| 生成内容 | `models`、`chi-server`、`strict-server`、`embedded-spec`（供校验加载） |
| 范围 | `output-options.include-operation-ids` 取自 `openapi/scope.yaml`，只为本期实现的接口生成处理器接口；同时 `output-options.skip-prune: true`，否则过滤后未被引用的 schema 会被裁掉 |
| 类型名 | 与 schema 名相同（`LoginResponseDto`、`QueryUserDto` …）；paths 顺序即生成的路由注册顺序 |
| 类型映射 | `type-mapping`：`string` + `format: date-time` → `jsontime.Time`（§4.4）；`number` 全局映射 `float64`；路径 id 参数经 overlay `x-go-type: int32` |
| overlay | `openapi/overlay.yaml` 只作用于生成输入：修正请求类型的 nullable / required、为响应 schema 加 `x-go-type` 指向 `v1/view` 中的手写类型。对外文档不受影响 |
| 响应写出 | 通过 `output-options.user-templates` 覆盖 strict 响应模板：每个 `Visit<Op>Response` 调用 `response.WriteData(w, body)`，**状态码固定写 200**（即使文档声明 201），仅 `uploadFile` 写 201；`void` 响应调用 `response.WriteOK(w)` 不输出 `data` |
| 错误 | `StrictHTTPServerOptions.RequestErrorHandlerFunc` / `ResponseErrorHandlerFunc` 与 handler 返回的 error 统一交给 `response.WriteError`，按 §4.1.1 输出；`ChiServerOptions.ErrorHandlerFunc`（路径 / 查询参数绑定失败）中路径参数错误 → 400 `{id:'invalidId'}`（翻译前的键，输出为译文，见 §4.1） |

**路由语义（对齐 Express，见 §4.1）**：chi `r.MethodNotAllowed` 与 `NotFound` 设为同一个 404 处理器（`Cannot <METHOD> <originalUrl>`，`data: null`，不带 `Allow` 头，`originalUrl` 取 `r.RequestURI` 原文）；在路由前的中间件中以 `r.URL.EscapedPath()` 为基础去掉末尾斜杠、静态段转小写后写入 chi `RouteContext.RoutePath`，**不使用解码后的 `r.URL.Path`**（`middleware.StripSlashes` 在未设置 `RoutePath` 时读取解码后的 `r.URL.Path` 再写回 `RoutePath`，会丢掉编码，不能直接使用；M1 按 chi 5.5.0 源码核对）；路径参数由 handler 取出后再按 `decodeURIComponent` 语义解码。用例：`/api/%68ealth` → 404，`/API/HEALTH`、`/api/health/` → 200。

**未实现的接口（§1.2 范围外）**：文档里有、本服务未注册的路由由 chi `NotFound` 返回与 nestjs 未知路由相同的 404 信封（`Cannot <METHOD> <originalUrl>`，见 §4.1「路由语义」）；完整清单由契约测试生成并写入 `docs/contract.md`，与实际不符时测试失败。前端仍可从本服务的 `/docs-json` 生成全部代码，只是调用范围外接口会得到 404。`/docs-json` 输出 nestjs 完整文档属于契约；按实现范围过滤的可选输出见 §13 第 9 项。

**文件上传（FILE_DRIVER）**

| 驱动 | 处理 |
|---|---|
| local / s3 | 生成的 `uploadFile`（multipart，字段 `file`），HTTP 201 |
| s3-presigned | 同一路径 `POST files/upload` 上的手写 handler，接收 `application/json` `{fileName, fileSize}`，返回 `{file, uploadSignedUrl}`；不走生成的 multipart 绑定，按 `validation.json` 中对应 DTO 校验 JSON 体，在契约测试的反向检查中列入白名单，差异写进 `docs/contract.md` |

**请求处理顺序（与 nestjs「守卫先于管道」一致）**

```
recover → requestid → logging/metrics/otel → cors（OPTIONS 在此结束，204）→ secureheaders → i18n
  → JSON 请求体读取与解析（256kb 上限 → 413；非法 JSON / 顶层原始值 → 400）
  → 路由匹配（原始路径，kin-openapi FindRoute 得到 operationId，查 policy；未匹配 → 404）
  → throttle（全局守卫）→ auth（JWT / 可选 JWT / refresh / 监控令牌）→ RBAC 权限
  → 路径参数（`:id` 按 ParseIdPipe 规则，失败 → 400 invalidId）
  → validation.json 校验（作用于已解析的 JSON 值，输出转换后的值）→ strict 解码（转换后的值）→ tx（StrictMiddlewareFunc）→ handler
```

- 不使用 `openapi3filter.ValidateRequest` / nethttp-middleware：它的类型错误会先于 class-validator 约束短路，消息也不同；`multipart/form-data` 请求体不经过 JSON 校验
- 解析后的请求体缓存在 context 中供后续步骤使用，不重复读取
- 顺序带来的可观察结果（均有单测，parity 覆盖）：未知路径带非法 JSON → 400（不是 404）；鉴权路由不带令牌、JSON 非法 → 400（不是 401）；不带令牌、JSON 合法但字段非法 → 401（不是 422）

**校验：`openapi/validation.json` + nestjs oracle 用例**

nestjs 文档不含约束，class-validator 的行为是：先执行 `@Transform`，再按装饰器顺序检查**全部**约束，失败消息用 `", "` 拼接。仅凭 OpenAPI 无法复现，做法：

- nestjs 侧脚本 `scripts/export-validation.ts`（nestjs 提交 `8b5f296`）读取 class-validator `getMetadataStorage()` 与 class-transformer 元数据，`openapi-sync` 把输出写成 `openapi/validation.json`。实际结构（136 个 DTO）：

  ```
  {generator, classValidator: "0.15.1", classTransformer: "0.5.1",
   pipe: {whitelist, forbidNonWhitelisted, forbidUnknownValues, transform, enableImplicitConversion, errorHttpStatusCode},
   classes: {<Dto>: {source, extends, initialValues,
     properties: {<field>: {designType, transforms: [{function, source, options}], transformsToPlain,
       type, expose, exclude,
       validations: [{type, key, name, constraints, message, messageFunction, defaultMessage,
                      each, groups, always, context, validateIf, validationTypeOptions}]}}}}}
  ```

  `transforms[].source` 为转换函数源码文本（如 `lowerCaseTransformer`），执行器按 `function` 名实现，源码只用于核对；`extends` 表示继承的 DTO，执行时合并父类字段；`initialValues` 为类字段初值（如分页默认值）
- Go 侧 `pkg/validatorx` 按该文件复刻整个 nestjs `ValidationPipe`（class-transformer `plainToInstance` + class-validator），作用于解码前的 JSON 值，包括 JS 值语义（`undefined` 与 `null` 区分、字符串长度按 UTF-16 码元计、`@Transform` 中的 JS 运算）：实现文件中出现的全部约束（`key` / `name`），默认消息按 `defaultMessage` 模板（`$property`、`$constraint1` …）生成；字符串类约束（`isEmail`、`isUrl`、`isUuid`、`isIso8601`、`isDateString`、`isLength`、`isNotEmpty` 等）以及 `isNumber` / `isInt` 等类型判断的语义来自 validator.js，与 Go 标准库或 go-playground/validator 的实现不同，需要按 validator.js 源码逐个移植（fastapi-temp 为此完整移植了 validator.js，见其 `src/app/common/validation/validator_js.py`），不得用现成 Go 校验库近似；遇到文件中出现但未实现的约束，启动时失败
- **门禁**：`tests/contract/fixtures/validation-cases.json` 的约 4516 条用例全部通过（逐条比对 `ok`、转换后的 `value`、422 的 `errors`），是 M1 验收项；执行器是 M1 中工作量最大的一项，按此预留时间
- 请求体与查询参数的类型、required、约束全部由执行器负责；端到端的 422 字段键、消息、顺序再由 parity 覆盖

### 4.4 序列化细节

nestjs 用 class-transformer + `JSON.stringify` 输出，Go `encoding/json` 默认行为在以下几处不同，需显式对齐：

| 项 | nestjs 输出 | Go 默认 | 对齐方式 |
|---|---|---|---|
| 时间 | `2026-11-10T12:00:00.000Z`（`toISOString`，毫秒、UTC、`Z`） | `time.Time` 为 RFC3339Nano，小数位不定长、可能带时区偏移 | `pkg/jsontime.Time`：转 UTC、固定 3 位毫秒、以 `Z` 结尾；经 overlay 映射到所有 `date-time` 字段 |
| 日期 | `birthday` 为 `YYYY-MM-DD` | oapi-codegen `openapi_types.Date` | 无需处理 |
| 数据库时间列 | `timestamp`（无时区，TypeORM `CreateDateColumn` 默认，默认值 `now()`） | — | 表结构与 nestjs 相同（`db/nestjs-schema.sql`，不改为 `timestamptz`）；所有数据库连接固定会话时区 UTC（pgx `RuntimeParams["timezone"] = "UTC"`），否则 `now()` 默认值与 `timestamp` 读写随服务器时区漂移；Go 侧读写 `timestamp` 一律按 UTC 解释 |
| `undefined` 字段 | 不输出该键 | 非指针零值照常输出 | 按 nestjs 实际输出逐字段确认：可空字段输出 `null`，未设置的可选字段 `omitempty` / `omitzero` 不输出；文档未标 nullable 的字段以实际输出为准 |
| 分组字段 | `@Expose({ groups: ['me', 'admin'] })` 控制可见性，spec 中只有一个 DTO | 无分组概念 | `controller/http/v1/view` 中手写 `UserMe` / `UserAdmin` / `UserPublic` 等视图类型（overlay `x-go-type` 指向它们），不用生成的结构体输出响应；对外文档中仍是 nestjs 的同一个 DTO schema，视图只决定运行时输出的字段集 |
| 空数组 | `[]` | nil 切片输出 `null` | 转换函数中切片一律初始化；单测覆盖 |
| 头像 | `photo.path` 为完整 URL | — | 序列化时按 `FILE_DRIVER` 拼接 `BACKEND_DOMAIN` 或 S3 地址 |
| 金额 | int（分） | — | 一律 `int64`，不用 float |
| HTML 转义 | `JSON.stringify` 不转义 `<>&` | `encoding/json` 转义为 `\u003c` | 编码器 `SetEscapeHTML(false)` |

对照方法：从 nestjs 录制各视图的响应样本存为 `testdata/fixtures/*.json`，单测逐视图断言键是否存在、是否为 `null`（§10.6 第 1 层）；同一份 seed 数据下，再用第 5 层 parity 测试对比两个后端的响应体。

### 4.5 分页

- 请求：`page`（≥1，默认 1）、`limit`（≥1，默认 10，路由上限 50 / 100），空串视为缺省；部分列表支持 JSON 字符串 `filters`、`sort`
- 排序：按请求的 `sort` 字段依次排序，未包含主键时最后追加主键升序（`id ASC`），保证分页结果稳定、与 nestjs 相同数据下顺序一致；`sort` 中出现不存在的字段时与 nestjs 相同按未知异常处理（TypeORM `EntityPropertyNotFoundError` 不是 HTTP 异常，经过滤器输出 500）
- 标准响应：`{list, total, page, limit, totalPages}`
- 无限滚动（仅 `GET /users`）：`{data, hasNextPage}`

### 4.6 本期端点

**auth**（全部 200；登录类接口响应 `data` 键顺序为 `{refreshToken, token, tokenExpires(ms), user}`（nestjs `createSessionAndTokens` 的字面量顺序）；`refreshToken` 接口为 `{token, refreshToken, tokenExpires}`；Go 侧 view 结构体字段按此顺序声明）

| 方法 | 路径 | operationId | 鉴权 | 限流 |
|---|---|---|---|---|
| POST | auth/email/login | loginWithEmail | – | 5/60s |
| POST | auth/email/register | registerWithEmail | – | 默认 |
| POST | auth/email/confirm | confirmEmail | – | |
| POST | auth/email/confirm/new | confirmNewEmail | – | |
| POST | auth/forgot/password | forgotPassword | – | 3/300s |
| POST | auth/reset/password | resetPassword | – | 5/300s |
| GET | auth/me | getCurrentUser | JWT | |
| PUT | auth/me | updateCurrentUser | JWT | 10/60s |
| DELETE | auth/me | deleteCurrentUser | JWT | |
| POST | auth/refresh | refreshToken | refresh JWT（Bearer，无 body） | |
| POST | auth/logout | logout | JWT | |
| POST | auth/phone/send-code | sendSmsCode | – | 3/60s |
| POST | auth/phone/login | loginWithPhone | – | 5/60s |
| POST | auth/phone/sms-login | loginWithSms | – | 5/60s |
| POST | auth/phone/register | registerWithPhone | – | 3/60s |
| POST | auth/phone/reset-password | resetPasswordByPhone | – | 5/300s |
| PUT | auth/password | changePassword | JWT | 3/60s |
| POST | auth/wechat/login | loginWithWechat | – | 10/60s |
| POST | auth/wechat/phone-login | loginWithWechatPhone | – | 10/60s |
| POST | auth/alipay/login | loginWithAlipay | – | 10/60s |
| GET | auth/sessions | getMySessions | JWT | 返回 `[{id, userAgent, ip, createdAt, lastActiveAt, current}]` |
| DELETE | auth/sessions/:id | revokeMySession | JWT | 非本人会话 404 `sessionNotFound` |
| POST | auth/sessions/revoke-others | revokeOtherSessions | JWT | |

**users**（JWT + 权限，admin 视图）

| 方法 | 路径 | operationId | 权限 |
|---|---|---|---|
| POST | users | createUser | user:create |
| GET | users | getUsers（无限滚动） | user:list |
| GET | users/:id | getUser | user:read |
| PUT | users/:id | updateUser | user:update |
| DELETE | users/:id | deleteUser | user:delete |

**files**：`POST files/upload?purpose=...`（**HTTP 201**，body `code` 200；local / s3 返回 `{file}`，operationId 仅 local 文档中有 `uploadFile`；s3-presigned body `{fileName, fileSize}`，返回 `{file, uploadSignedUrl}`，见 §4.3）、`GET files/private/:id`（uuid，非法值按 nestjs 内置 `ParseUUIDPipe` 返回 400，`message` 为原文 `Validation failed (uuid is expected)`（无翻译键），`data: null`）、local 驱动 `GET files/:path`。细则（nestjs `src/modules/files/`）：

| 用途 `purpose` | 允许扩展名 → `mimeType` | 可见性 | 所需权限 | 文件归属 | 大小上限（默认） |
|---|---|---|---|---|---|
| `image`（缺省或空串时按它处理） | jpg / jpeg → `image/jpeg`，png、gif、webp | PUBLIC | – | 上传者 | `FILE_IMAGE_MAX_SIZE`（5MB） |
| `private-image` | jpg / jpeg、png、webp | PRIVATE | – | 上传者 | `FILE_PRIVATE_IMAGE_MAX_SIZE`（10MB） |
| `content-image` | 同 image | PUBLIC | `content:manage` | 无（`ownerUserId` null） | `FILE_CONTENT_IMAGE_MAX_SIZE`（5MB） |
| `audio` | mp3 → `audio/mpeg`，m4a → `audio/mp4` | PUBLIC | `content:manage` | 无 | `FILE_AUDIO_MAX_SIZE`（20MB） |
| `import` | zip、xlsx、json | PRIVATE | `content:manage` | 上传者 | `FILE_IMPORT_MAX_SIZE`（50MB） |
| `export` | pdf | PRIVATE | `content:manage` | 上传者 | `FILE_EXPORT_MAX_SIZE`（20MB） |

- `mimeType` 只按扩展名（最后一个 `.` 之后，转小写）查上表，不看客户端 `Content-Type`；存储键为 21 位小写十六进制随机串 + `.` + 扩展名（nestjs `randomStringGenerator` = `uid(21)`），文件名中的目录部分丢弃
- 处理顺序（local / s3，与 nestjs 守卫 → 拦截器 → 管道一致）：限流 → JWT（无令牌 401，先于一切用途检查）→ 用途守卫（`purpose` 非法或重复 → 422 `{purpose: 'invalidFilePurpose'}`；缺权限 → 403 `Missing permissions: content:manage`；`Content-Length` > 用途上限 + 16KiB → 413）→ 流式接收 multipart（只接受字段名 `file` 的单个文件，见下）→ 查询参数校验（`purpose=""` 时用途守卫按 image 放行，此处才按 `UploadFileQueryDto` 返回 422 `{purpose: 'purpose must be one of the following values: image, private-image, content-image, audio, import, export'}`，已写入的文件删除）→ 无文件 → 422 `{file: 'selectFile'}`；文件大小 > 用途上限 → 413，删除已写入的文件
- multipart 语义照 multer 2.5.0 + busboy：单文件上限为全部用途上限的最大值，超出时中止读取并 413；扩展名不在用途表内 → 422 `{file: 'cantUploadFileType'}`（读到该文件头时即拒绝）；多个 `file` 或其它字段名的文件 → 400 `Unexpected file field - <name>`；空文件名的文件部分、非 multipart 请求体（JSON、空体）都视为没有文件；`multipart/form-data` 缺 boundary → 400 `Multipart: Boundary not found`；畸形头 / 截断 → 400 `Multipart: Malformed part header` / `Multipart: Unexpected end of form`；允许首个分隔符前的 preamble；非 `form-data` 的 multipart → 415。错误文本以 fastapi-temp `src/app/modules/files/uploads.py` 与 `tests/parity/test_files_parity.py::test_upload` 为准（M3 前先确认这些 parity 用例已对 nestjs 跑通）。Go 侧用 `mime/multipart.Reader` 流式读取并自行映射错误，**不用** `r.ParseMultipartForm` / `r.FormFile`（会先把整个请求体读入内存或临时文件，且错误语义不同）
- 错误形状：413 为 `{code: 413, message: "PAYLOAD_TOO_LARGE", data: {file: <fileTooLarge 译文>}}`（nestjs `STATUS_TO_I18N_KEY` 没有 413，`message` 取 `HttpStatus[413]` 枚举名，不翻译）；422 的 `message` 为 `common.unprocessableEntity` 译文；`data` 中的键按 §4.1.1 翻译（`invalidFilePurpose` → `Unsupported upload purpose`、`cantUploadFileType` → `Unsupported file type`、`selectFile` → `Please select a file`、`fileTooLarge` → `File is too large` / `文件过大`）
- 响应 `{file: {id, path, visibility, mimeType, size}}`（`ownerUserId`、`sha256` 不输出）；`path` 输出规则：local 驱动 PUBLIC 为 `BACKEND_DOMAIN + /{API_PREFIX}/v1/files/<key>`（库中 `path` 列存 `/{API_PREFIX}/v1/files/<key>`），PRIVATE 为 `BACKEND_DOMAIN + /{API_PREFIX}/v1/files/private/<id>`；s3 / s3-presigned 驱动库中 `path` 存对象键，输出为 3600 秒预签名 GET URL（PUBLIC 与 PRIVATE 相同）；`AWS_S3_ENDPOINT` 设置时按 `AWS_S3_FORCE_PATH_STYLE`（默认 true）决定路径式 / 虚拟主机式签名。用户 `photo.path` 走同一函数
- `GET files/private/:id`：JWT；不存在 404 `{error: 'fileNotFound'}`（`File not found`，`data: null`）；PRIVATE 且非本人 403 `{error: 'fileNotAccessible'}`（`You cannot access this file`）；PUBLIC 文件任何登录用户可读；local 驱动按 Express `res.sendFile` 输出（弱 ETag `W/"<size hex>-<mtime ms hex>"`、`Last-Modified`、`Cache-Control: public, max-age=0`、`Accept-Ranges: bytes`、条件请求 304），s3 驱动 302 重定向到预签名 URL（`Found. Redirecting to <url>`，按 `Accept` 选 text / html）
- `GET files/:path`（仅 local 驱动，公开）：只按库中 PUBLIC 记录查找，PRIVATE、不存在、`GET files/upload` 都返回 404 `fileNotFound`；输出头同上
- s3-presigned：body 按 `FileUploadDto`（`fileName` 字符串、`fileSize` 数字）校验；扩展名不在用途表内 → 422 `cantUploadFileType`，`fileSize` > 用途上限 → 413；签发 3600 秒 `PutObject` 预签名 URL（带 `ContentLength`），并立即写入 file 记录（`size` = `fileSize`）
- parity 只覆盖 local 驱动（nestjs 参照服务 `FILE_DRIVER=local`）；s3 / s3-presigned 由集成测试对 MinIO 容器覆盖

**health / metrics**

| 方法 | 路径 | operationId | 鉴权 |
|---|---|---|---|
| GET | /api/v1/health | healthCheck | 监控令牌 |
| GET | /api/v1/health/live | livenessProbe | – |
| GET | /api/v1/health/ready | readinessProbe | – |
| GET | /api/health | simpleHealthCheck | – |
| GET | / | getAppInfo | – |
| GET | /metrics | 不进文档 | 监控令牌 |

- 监控令牌（nestjs `MonitoringTokenGuard`）：配置了 `METRICS_TOKEN` 时要求 `Authorization: Bearer <METRICS_TOKEN>`（scheme 不区分大小写，常量时间比较）；未配置时非生产放行、生产拒绝；拒绝为 403 `{code: 403, message: "Forbidden resource", data: null}`（原文，不翻译）
- `/api/v1/health*` 为 @nestjs/terminus 12.1.0 形状，成功 200 且带 `Cache-Control: no-cache, no-store, must-revalidate`，`data` 为 `{status: "ok", info: {...}, error: {}, details: {...}}`，每项为 `{<key>: {...附加字段, status: "up"|"down"}}`，`details` = `info` + `error`；任一项 down 时 503，**body 只有 `{code: 503, message: "SERVICE_UNAVAILABLE", data: null}`**（terminus 结果被异常过滤器丢弃，`message` 为 `HttpStatus[503]` 枚举名，不翻译），不带 `Cache-Control`
- 检查项与顺序：`healthCheck` 为 `memory_heap`（堆 > 300MB 为 down，`{message: "Used heap exceeded the set threshold"}`）、`disk`（`/` 使用率 > 90%，`Used disk storage exceeded the set threshold`）、`database`（`SELECT 1`，1000ms 超时，`{responseTime: <ms>}`，超时 `timeout of 1000ms exceeded`）、`database_pool`（`pg_stat_database.numbackends` / `max_connections`，`{activeConnections, maxConnections, utilizationPercent}`，> 90% 为 down）、`redis`（仅 `REDIS_ENABLED=true` 时加入，PING）、`wechat_api`（`WECHAT_APP_ID` / `WECHAT_APP_SECRET` 缺一则 up `{message: "WeChat not configured, skipped"}`，否则 GET `https://api.weixin.qq.com/cgi-bin/getcallbackip`，5s，2xx 为 up `{responseTime: "OK"}`，否则 down `{statusCode}`）、`alipay_api`（`ALIPAY_APP_ID` 未设置则 up `{message: "Alipay not configured, skipped"}`，否则 HEAD `https://openapi.alipay.com/gateway.do`，2xx 或 405 为 up）；`livenessProbe` 只有 `memory_heap`（成功时为 `{status: "up"}`，无 `responseTime`）；`readinessProbe` 只有 `database`。堆指标 nestjs 为 V8 heapUsed、fastapi-temp 为进程 RSS，Go 取 `runtime/metrics` 的堆对象字节数；阈值相同，数值不属于契约
- `GET /api/health`（无 `Cache-Control`）：`{status: "ok", timestamp: <toISOString>, uptime: <进程启动以来整秒>, memory: {heapUsed, heapTotal, rss}（MB，四舍五入）, node: <运行时版本>}`；Go 取 `runtime.Version()`（如 `go1.27.4`）与 `runtime.MemStats`，键名与类型属于契约，值不属于
- `GET /`：`{name: APP_NAME}`（默认 `app`）
- `/metrics`：不带 `API_PREFIX`、不包信封，`Content-Type: text/plain; version=0.0.4; charset=utf-8`；`METRICS_ENABLED=false` 只关闭 HTTP 指标采集与运行时默认指标，路由仍存在（nestjs 行为）。nestjs `setGlobalPrefix` 的排除项写死为 `metrics`，非默认 `METRICS_PATH` 在 nestjs 上会落到 `/api/<path>`；本服务一律不带前缀，部署时保持默认值

**通用模块**：路由、operationId、权限照抄 nestjs 对应 controller（由文档与 `scope.yaml` 决定）。

鉴权方式（none / jwt / optional-jwt / refresh / monitor-token）、是否经过 RBAC 权限守卫、所需权限、限流规则、审计标记不在 OpenAPI 文档里（nestjs 用装饰器表达），本服务在 `controller/http/policy.go` 按 operationId 声明，以实现为准；契约测试只做交叉检查：文档声明 bearer 的接口 policy 必须是 jwt / optional-jwt / refresh，监控令牌接口（`healthCheck`、`/metrics`）文档不声明 bearer。

---

## 5. 相对 nestjs-template 的改进（均不破坏现有契约）

| # | 项 | nestjs 现状 | 本模板 |
|---|---|---|---|
| 1 | 密码哈希 | bcrypt(md5)，cost 10 | **argon2id(md5)**（OWASP 首选，x/crypto/argon2，PHC 字符串存储）；登录时识别旧 bcrypt 哈希，验证通过后自动重哈希为 argon2id（自写 `password.VerifyAndUpdate`）。客户端仍传 MD5，前端无感；`PASSWORD_HASHER` 切换默认算法 |
| 2 | 会话 | 已同步到 nestjs | 会话表可空列 `userAgent`（≤512）、`ip`、`lastActiveAt`（每会话最多 5 分钟写一次）；「我的设备」列表、下线指定设备、下线其他设备三个接口，见 §4.6 |
| 3 | 数据库迁移 | 每个应用容器启动时跑 `migration:run` | 独立一次性 `migrate` 子命令 / 服务 / job，应用 `depends_on: service_completed_successfully`；多副本不会并发迁移（上游 go-clean-template 也是启动即迁移，一并去掉） |
| 4 | 限流算法 | 固定窗口（`INCR` + `PEXPIRE`，超限后按 TTL 封禁） | Redis Lua **GCRA**（平滑、无窗口边界突刺；脚本内用 Redis `TIME` 取时间，键值为理论到达时间 TAT，`SET … PX`），进程内回落实现同算法且限制键数量；响应头名与出现条件与 nestjs 的 @nestjs/throttler 一致（§4.1.3）。键名与 nestjs 相同但值的格式不同，见 §14 |
| 5 | Request ID | 已同步到 nestjs（校验 + 回写） | 同 nestjs；日志另带 OTel `trace_id` / `span_id`，日志与链路可互相跳转 |
| 6 | OpenAPI | 运行时生成，无版本管理 | 直接使用 nestjs 导出文件并入库，spec-first 生成代码，契约漂移在编译期暴露 |
| 7 | 新表主键 | uuid v4 | **UUIDv7**（Go 1.27 标准库 `uuid.NewV7()`，按时间有序，索引友好）；已有 int 主键（user 等）保持不变 |
| 8 | 事务 | 服务内手动管理 | policy 标记的写接口由 tx `StrictMiddlewareFunc` 开启事务，`pgx.Tx` 经 context 传给 repo；提交时机在响应写出前（strict 中间件拿到的是响应对象，尚未写入 `ResponseWriter`），提交失败返回 500。结束规则（与 fastapi-temp `src/app/infrastructure/database/session.py` 的 `Database.transaction` 相同，语言无关）：**handler 成功 → 提交；handler 返回 4xx 业务 / HTTP 错误（`BusinessError`、401、403、404、422 等）→ 提交已写入的内容后再输出错误；其它错误（含 panic）→ 回滚**。原因：nestjs 无请求级事务，抛 401 之前的写入（如 §6.3 刷新重放时吊销会话）已经落库，若按「出错即回滚」实现，吊销会被撤销。例外：错误来源于数据库语句本身（如唯一约束冲突被翻译为 422 / 409）时，事务已处于失败状态，**不得提交**，一律回滚（pgx 对失败事务 `Commit` 返回 `pgx.ErrTxCommitRollback`，tx 中间件按回滚处理，不当作 500）；需要无论成败都保留的写入也可放在独立事务中执行。跨请求副作用（发邮件、通知）用 River `InsertTx` 在同一事务入队，提交成功才可见；4xx 提交路径与 fastapi-temp 一致丢弃已登记的提交后回调，而 `InsertTx` 入队的任务会随提交可见，所以入队放在全部业务检查之后。测试要求见 §6.3 |
| 9 | 优雅停机 | 依赖 Nest 默认 | `signal.NotifyContext` 收到 SIGTERM → readiness 立即返回 503 → `http.Server.Shutdown(SHUTDOWN_TIMEOUT)` 等待在途请求 → River `Stop` 等待在途任务 → 关闭 pgxpool、Redis → flush OTel / Sentry |
| 10 | 配置校验 | 运行时按模块校验 | 启动时一次性解析并校验全部配置（env + validator），缺必填项立即退出并列出全部缺失项 |
| 11 | 语言头 | 已同步到 nestjs（`AcceptLanguageResolver`） | 标准头 `Accept-Language`，见 §4.2 |
| 12 | 测试隔离 | e2e 连真实服务、串行执行 | 集成测试用 testcontainers-go 起 Postgres / Redis；大部分用例在外层事务内执行、应用内的事务转为 SAVEPOINT，结束后回滚；依赖真实提交的用例（刷新 CAS 并发、4xx 提交规则、提交后副作用、缓存与提交的先后）从已迁移 + seed 的模板库 `CREATE DATABASE … TEMPLATE` 克隆独立库；`t.Parallel()` 并行，见 §10.6 |

第 1 项与「两个后端共用同一个库」有冲突（nestjs 无法验证 argon2id）：共用期间设 `PASSWORD_HASHER=bcrypt`，独立部署时用默认 `argon2id`。第 2、5、11 项与限流响应头已在 nestjs 实现，属于共同契约。

---

## 6. 认证与权限

### 6.1 JWT

- access：`{id, role, sessionId, iat, exp}`，HS256，`AUTH_JWT_SECRET`，默认 15m。`role` 的形状随签发来源不同（照抄 nestjs `token.service.ts` 的调用方）：登录 / 注册类接口签发时为完整角色实体 `{id, name, __entity: "RoleEntity"}`（键顺序如此），`auth/refresh` 签发时为 `{id}`
- refresh：`{sessionId, hash, iat, exp}`，`AUTH_REFRESH_SECRET`，默认 3650d
- `iat` 为秒；`exp = iat + floor(ms(expiresIn) / 1000)`（与 jsonwebtoken 相同）；`tokenExpires = 当前毫秒时间 + ms(AUTH_JWT_TOKEN_EXPIRES_IN)`。`ms` 按 npm `ms` 2.x 规则解析：纯数字（含小数、负数）视为毫秒；单位大小写不敏感，支持 `ms/msec(s)/millisecond(s)`、`s/sec(s)/second(s)`、`m/min(s)/minute(s)`、`h/hr(s)/hour(s)`、`d/day(s)`、`w/week(s)`、`y/yr(s)/year(s)`（年按 365.25 天）；数字与单位间可有空格；超过 100 字符或无法解析视为配置错误（启动失败），不使用 Go `time.ParseDuration`
- forgot / confirm-email 令牌独立密钥（30m / 1d）；找回密码令牌内嵌密码指纹 `sha256(hash)[:16]`，改密后失效
- 生产环境四个密钥必须 ≥32 字符且互不相同
- golang-jwt 解析时固定 `jwt.WithValidMethods([]string{"HS256"})`，拒绝 `alg` 篡改

### 6.2 access 校验

只认 Bearer 头 → 必须有 `id`、`sessionId` → 查缓存 `jwt:session:{sessionId}`（30s，存 `{statusId, roleId}`），未命中查库：会话存在且属于该用户、用户状态 active / inactive → 以数据库角色覆盖令牌角色；同时节流更新 `lastActiveAt`（每会话每 5 分钟最多一次）。失败 401，`message` 为 nestjs `jwt.strategy.ts` 原文（无翻译键，原文输出）：

| 情形 | message |
|---|---|
| 无令牌、签名 / 过期无效、载荷缺 `id` / `sessionId` | `common.unauthorized` 译文 |
| 会话不存在、已删除或不属于该用户 | `Session expired` |
| 用户不存在 | `User not found` |
| 用户状态不是 active / inactive（缓存命中或查库） | `User is not active` |

会话被删除（logout、吊销、改密删除其它会话、重放吊销）时，`jwt:session:{id}` 缓存**在写库后立即清除一次，并在事务提交后再清除一次**：只在提交前清除时，并发请求可能在提交前从库中读到旧会话并回填缓存，令已删除会话在 30s 内仍可用。

### 6.3 刷新轮换

- `POST /auth/refresh`，refresh JWT 放 Bearer 头
- 会话不存在或已无关联用户 → 401
- 每次轮换成功后把被换掉的旧 hash 写入 `session:rotated:{id}`，TTL 60s（nestjs `session.service.ts` 的 `ROTATED_HASH_GRACE_MS`）；该键只保存**最近一次**被换掉的一个 hash，再次轮换即覆盖
- `session.hash != payload.hash`：若 `payload.hash` **等于** `session:rotated:{id}` 中存的旧 hash（`wasRecentlyRotatedFrom`，60s 内的并发或重试）→ 普通 401、不吊销；否则判为重放，吊销会话并 401。因此 h0 → h1 → h2 两次轮换后再用 h0 刷新属于重放
- 会话匹配后重新读取用户：用户或角色不存在 → 401；用户状态不是 active / inactive → **422** `{refresh: 'userNotActive'}`（nestjs `validateUserStatusForAuth(user, 'refresh')`，输出译文 `User is not active` / `用户未激活`）
- 否则生成新 hash，以 `UPDATE ... WHERE hash = old` 做 CAS，失败 401；返回新 access（`role: {id}`）+ refresh
- 重放吊销与 401 同在一个请求内：按 §5 第 8 项的事务规则，401 属于 4xx，吊销写入必须提交。集成测试（克隆库模式）：登录 → 刷新得到 h1 → 再刷新得到 h2 → 用 h0 刷新返回 401 → 此后用 h2 刷新返回 401、用登录时的 access 令牌请求 `auth/me` 返回 401 `Session expired`
- logout 软删会话并清缓存；改密 / 重置 / 换邮箱删除其它会话；03:00 物理删除软删超过 7 天的会话

### 6.4 密码

客户端传 MD5 hex（32 位，转小写）；服务端哈希见 §5 第 1 项；比较时同时尝试大写 MD5。

### 6.5 登录方式

| 方式 | 行为 |
|---|---|
| 邮箱 | 注册后 inactive 并发确认邮件；active / inactive 均可登录；错误 422 `{email:'notFound'}` / `{password:'incorrectPassword'}`（输出为译文，如 `{email:'Not found'}`，见 §4.1.1） |
| 手机 + 密码 | 错误 422 `{credentials:'incorrectPhoneOrPassword'}` |
| 短信登录 | 不自动注册，未知手机号 422 `{phone:'notFound'}` |
| 手机注册 | 直接 active 并返回令牌 |
| 微信 | `mini_app` → jscode2session，`app` → OAuth；按 openId 自动建号，唯一冲突时重查 |
| 微信手机号 | 绑定 openId + 手机，不合并账号（`phoneBoundToOtherWechat`），非大陆号拒绝（`phoneRegionNotSupported`） |
| 支付宝 | 按 alipayOpenId 自动建号 |

### 6.6 短信验证码

- `send-code` 预检：register 要求手机号不存在；login / reset_password / verify_identity 要求存在；change_phone 返回 `unsupportedCodeType`
- `SMS_CODE_LENGTH`（6）位数字（`crypto/rand`），缓存 `sms:code:{type}:{phone}` = `{code, attempts, createdAt}`，TTL 300s
- 重发间隔 60s（检查 + 锁 `sms:send:{type}:{phone}`），失败 422 `{phone:'rateLimited'|'sendFailed', retryAfter}`（`phone` 输出译文，`retryAfter` 为数字原样输出）
- 最多 5 次尝试；成功或超限删除；失败 422 `{code:'invalidCode'}`；比较用 `subtle.ConstantTimeCompare`
- 阿里云 Dysms；`SMS_MOCK_MODE=true` 只打日志

### 6.7 RBAC

- 角色 admin=1、user=2、editor=3、reviewer=4；状态 active=1、inactive=2
- 权限枚举、默认映射照抄 nestjs（枚举值不变）
- 表 `permission`、`role_permission`；数据库为空回落代码默认值；缓存 5 分钟
- policy 中 `RBAC: true` 表示经过权限守卫，`Permissions: All(...)`（AND）、`Any(...)`（OR）；路由级覆盖控制器级
- 经过权限守卫（对应 nestjs `PermissionsGuard`，`src/core/permissions/permissions.guard.ts`）却未声明任何权限的路由 → 403，message 为原文 `No permission declared for this route`；只要求 JWT、不经过权限守卫的路由（如 `auth/me`）不受此限；启动时列出 `RBAC: true` 且无权限的路由作为告警
- 缺权限时 403，message 为 nestjs 原文 `Missing permissions: a, b` / `Missing any of permissions: a, b`（无对应翻译键，按 §4.1.1 原文输出）
- 权限守卫中的其它 403 原文：请求无用户 `User not authenticated`，用户无角色（数据库角色为空，§6.2 覆盖后 `role.id` 缺失）`User role not found`

---

## 7. 数据模型

表名、列名与 nestjs 一致（camelCase 列名）。sqlc 查询中列名加双引号（`"firstName"`），生成的 Go 字段为 `FirstName`；repo 层把 sqlcgen 结构映射为 entity，列名映射集中在 SQL 中显式书写，不依赖反射或命名约定。两个后端可指向同一个库。

本期表：user、role、status、session、permission、role_permission、file、audit_logs、config、region、notification、notification_setting、address、favorite、feedback、user_consent、account_cleanup、admin_alert。约束名（如 `user_phone_key`）照抄；软删 `deletedAt`；已删除用户释放唯一字段的部分索引同样实现。新约束命名规则写入 CLAUDE.md（`<table>_<column>_key`、`<table>_<column>_fkey`、`idx_<table>_<column>`），由 migrations job 检查。

迁移：goose 初始迁移一次建出上述结构；sqlc 以 `migrations/` 为 schema 输入。CI 中 `up` → `sqlc vet` + `sqlc generate` 后无 diff（查询与结构一致）→ `down-to 0` → `up`（验证可回滚）。River 的表由 `rivermigrate` 生成的 SQL 纳入 goose 迁移，放在独立 schema `river`。

**共库时的迁移归属**：两套迁移工具（TypeORM migrations、goose）不能同时管理同一个库，否则会重复建表、互相覆盖。约定：

| 部署方式 | 表结构归属 | Go 侧做法 |
|---|---|---|
| 独立部署（默认） | goose | 正常 `migrate up` |
| 与 nestjs 共库 | nestjs（TypeORM） | 设 `DB_SCHEMA_OWNER=external`：migrate 只执行 `river` schema 的迁移，跳过业务表；启动时只校验所需表 / 列存在；会话设备列已在 nestjs 迁移 `AddSessionClientInfo` 中 |

结构一致性以共享产物 `db/nestjs-schema.sql`（§4.3，fastapi-temp `scripts/schema_snapshot.py` 生成的规范化快照，每个对象一行）为准，不用 `pg_dump`：集成测试对 goose 迁移后的库执行与 `schema_snapshot.py` 相同的四条 `pg_catalog` 查询并按相同格式输出（Go 侧移植查询与格式化，或直接调用复制来的脚本），断言「快照 − 实际」恰为 `constraint feedback.FK_feedback_errata …` 与 `constraint feedback.FK_feedback_question …` 两行（引用本期不做的 `errata`、`question` 表），「实际 − 快照」为空；只比较 `public` schema 中 §7 所列 18 张表，`river` schema 与 goose 版本表不在范围内。另有定时 drift job 对 nestjs release 分支 HEAD 重新生成快照比对，见 §11.1。

**Seed**：与 nestjs `user-seed.service.ts` 一致。非生产创建 `admin@example.com`、`john.doe@example.com`（密码 `Secret00`）等演示账号；生产不创建演示账号，仅在 `SEED_ADMIN_EMAIL`（去空白、转小写）与 `SEED_ADMIN_PASSWORD` 都设置且尚无管理员时创建管理员。nestjs 以 `ENV_MODE=prod` 判定生产，本服务与 fastapi-temp 一样以 `APP_ENV=production` 判定（nestjs 参照服务在 `docker/nestjs.compose.yaml` 中以 `ENV_MODE=dev` 执行 seed）。seed 幂等，可重复执行。

---

## 8. 基础设施

| 能力 | 实现 |
|---|---|
| 日志 | slog JSON handler（开发环境 text 单行）；级别 `LOG_LEVEL`（未设置时生产 info、其它 debug），格式 `LOG_PRETTY_PRINT`（未设置时非生产为单行文本）；自写包装 handler 从 context 注入 request_id、trace_id、span_id、user_id；请求日志对应 pino-http：每个请求一条，`msg` 为 `<METHOD> <originalUrl> <status>`（出错时追加 ` - <错误>`），字段 `context: "HTTP"`、`req{id, method, url, query, headers, remoteAddress}`、`res{statusCode}`、`responseTime`（ms）；≥500 或异常为 error、≥400 warn、其余 info；脱敏（值替换为 `[REDACTED]`）`req.headers.authorization`、`req.headers.cookie`、`req.body` 的 password/oldPassword/newPassword/code/phone/email、`req.query` 的 hash/token；`OTEL_ENABLED` 时经 otelslog 同步导出 |
| 指标 | 名称、类型、标签、桶与 nestjs `metrics.module.ts` 相同：`http_requests_total`（counter，`method`、`path`、`status`）、`http_request_duration_seconds`（histogram，同标签，桶 0.005/0.01/0.025/0.05/0.1/0.25/0.5/1/2.5/5/10）、`http_active_connections`（gauge）、`database_query_duration_seconds`（histogram，`operation`、`table`，桶 0.001/0.005/0.01/0.025/0.05/0.1/0.25/0.5/1）、`cache_hits_total` / `cache_misses_total`（`cache_name`）、`queue_jobs_total`（`queue_name`、`status`）、`admin_alerts_total`（`type`、`level`）、`admin_alerts_deduplicated_total`（`type`）、`throttler_storage_fallback_total`（无标签）；HELP 文本照抄；所有样本加 `app=<APP_NAME>`（`prometheus.WrapRegistererWith`；nestjs 在 `APP_NAME` 未设置时为 `nestjs-app`，本服务与 fastapi-temp 用应用名默认值 `app`）。HTTP 指标在响应结束时记录：`path` 为完整路由模板并转成 Express 写法（chi `/api/v1/users/{id}` → `/api/v1/users/:id`），未匹配路由为 `<unmatched>`，客户端在响应写完前断开记 `status="499"`；CORS 预检在指标中间件之前结束，不计数。nestjs 只实际记录 HTTP、告警、限流回落三组，其余只注册；本服务全部记录（pgx tracer 记数据库耗时，表名从 SQL 解析）。单进程无需 multiprocess 模式；运行时指标为 `go_*` / `process_*`（不属于契约） |
| 链路 | otelhttp、otelpgx、go-redis redisotel、出站 `http.Client` 用 otelhttp Transport；OTLP/HTTP 导出（`OTEL_EXPORTER_OTLP_ENDPOINT` + `/v1/traces`），`service.name` = `APP_NAME`；`OTEL_ENABLED` 默认 false；Sentry 只在 `SENTRY_DSN` 设置时初始化（`environment` = `APP_ENV`，不发送 PII）。nestjs 无这两项，不属于契约 |
| 健康检查 | 形状与检查项见 §4.6「health / metrics」；微信 / 支付宝探测用带 5s 超时的独立 `http.Client` |
| 缓存 | Redis 命名空间 `cache`，默认 TTL 60s；Redis 不可用回落进程内缓存 |
| 分布式锁 | 键 `lock:<key>`，`SET NX PX` + Lua 比较 token 释放，单条命令超时 1s；Redis 未启用或命令失败时回落进程内锁（仅本实例生效），与 nestjs `lock.service.ts` 相同 |
| 队列 | River（Postgres）；队列与任务（nestjs BullMQ 队列名 / job 名）：`mail`（`send-mail`）、`notification`（`send-email`、`send-sms`、`send-wechat-subscribe`；BullMQ 并发 5、limiter 10 个 / 秒，River 无 limiter，用 `MaxWorkers: 5` + worker 内 `golang.org/x/time/rate` 近似）、`account-cleanup`（`clean-account`，并发 1）；默认 3 次尝试（含首次），失败后等待 `1s × 2^(n-1)`（BullMQ `exponential`，用 River `RetryPolicy` 自写，River 默认的 `attempt^4` 退避不用）；`MaxAttempts` 即 BullMQ `attempts`；每次结束计 `queue_jobs_total{queue_name, status="completed"|"failed"}`。测试用 `rivertest` + `river.Config{TestOnly: true}`（Postgres 本就是必需依赖，不存在「无 Redis 降级」问题；nestjs / fastapi-temp 在 `REDIS_ENABLED=false` 时改为进程内执行，本服务无此分支） |
| 定时任务 | River 周期任务，leader 选举保证只执行一次（nestjs 用 `lock:scheduler:<name>` 锁，本服务不需要）：每天 03:00 UTC 物理删除 `deletedAt` 早于 7 天的会话；每天 04:00 UTC 软删除 `readAt` 早于 30 天的已读通知（nestjs 这两项未指定时区，容器为 UTC）；每小时第 25 分（`Asia/Shanghai`）执行账号清理补偿（见「账号清理」）。任务以条件更新保证幂等，与 nestjs 同库同时运行也不会出错 |
| 审计 | policy 中 `Audit: "<action>"` 的接口由中间件在响应成功后写 `audit_logs`（失败不影响响应） |
| 告警 | `alert.Raise(ctx, type, level, message, payload, dedupKey)`（dedupKey 缺省为 type）：同 dedupKey 已有未确认告警时不写入，计 `admin_alerts_deduplicated_total{type}`；否则写 `admin_alert`、计 `admin_alerts_total{type, level}`，并给 admin 角色用户发站内通知（`[<level>] <type>`）；写入失败只记日志 |
| 账号清理 | 删会话 → 软删 → 同事务 `InsertTx` 投递 `account-cleanup` 任务（参数 `{userId}`，5 次尝试，`10s × 2^(n-1)` 退避）。去重对应 nestjs jobId `user-<id>`：River `UniqueOpts{ByArgs: true, ByState: [available, pending, retryable, running, scheduled]}`（不含 completed / discarded，已结束的同用户任务不阻止再次入队；River 对 `ByState` 必含项的要求在 M3 按 0.51.2 核对）。任务按 order 升序执行全部清理器（nestjs：users 900 解除头像引用、files 1000 删除该用户文件与存储对象且仍被外键引用的保留；M4 模块 favorites 70、feedbacks 90、notifications 100、addresses 110），单个失败不影响其余，全部成功后写 `account_cleanup(userId, completedAt)`；最后一次尝试仍失败时告警（type `accountCleanupFailed`、WARN、payload `{userId, reason, failedCleaners}`、dedupKey `accountCleanupFailed:<id>`），入队失败同样告警。补偿任务：每小时取已软删超过 30 分钟且无 `account_cleanup` 记录的用户（按 id 升序，200 个）重新入队 |
| 邮件 | 注册确认、确认新邮箱、找回密码三个模板，从 nestjs `src/integrations/mail/mail-templates/*.hbs` 移植。nestjs 在请求内同步发送（SMTP 失败时接口 500）；本服务与 fastapi-temp 一样入 `mail` 队列（同事务 `InsertTx`，§5 第 8 项）：请求侧把模板名、收件人、数据与当前请求语言写入任务参数，worker 按该语言渲染。主题 = `common.<titleKey>` 译文（activation / confirm-new-email 为 `confirmEmail`，reset-password 为 `resetPassword`）；正文 `text{n}` 取 `confirm-email` / `confirm-new-email` / `reset-password` 命名空间；纯文本 = `<url> <title>`；链接 = `FRONTEND_DOMAIN` 逗号分隔的第一个 + `/confirm-email`、`/confirm-new-email`、`/password-change`，查询串按 WHATWG `URLSearchParams` 编码（`hash`，找回密码另有 `expires`）；`app_name` = `APP_NAME`；发件人 `"<MAIL_DEFAULT_NAME>" <<MAIL_DEFAULT_EMAIL>>`；TLS 语义同 nodemailer（`MAIL_SECURE` 隐式 TLS、`MAIL_IGNORE_TLS` 禁用 STARTTLS、`MAIL_REQUIRE_TLS` 强制）。模板用 `text/template` + Handlebars 转义函数（``& < > " ' ` =`` → `&amp; &lt; &gt; &quot; &#x27; &#x60; &#x3D;`），不用 `html/template`（上下文转义输出 `&#39;` 并改写 `href` 中的 URL，与 nestjs 字节不同）；以 fastapi-temp `tests/fixtures/nestjs_mail/*.json`（nestjs 渲染的 subject / text / html）作 golden 用例逐字节比对 |
| 文件 | `FILE_DRIVER=local|s3|s3-presigned`，规则见 §4.6 files；local 写到工作目录下 `./files`；s3 驱动先写临时文件再 `PutObject`（`Content-Type` 取用途表的 mime），超限或后续失败时删除对象 |
| 进程 | 同一镜像三种角色：api（`serve`）、worker（`worker`，含周期任务）、migrate（一次性）。比 fastapi 少一个 scheduler 进程 |

---

## 9. 配置（环境变量）

变量名与 nestjs `env-example-relational` 一致，差异：

| nestjs | Go |
|---|---|
| `NODE_ENV` | `APP_ENV`（development / test / production） |
| `DATABASE_TYPE`、`DATABASE_SYNCHRONIZE` | 不需要 |
| — | `PASSWORD_HASHER`、`DB_SCHEMA_OWNER`、`OTEL_ENABLED`、`OTEL_EXPORTER_OTLP_ENDPOINT`、`SENTRY_DSN`、`SHUTDOWN_TIMEOUT` |

与 nestjs 同名同义、契约相关的变量（必须支持，默认值与 nestjs 一致）：

| 变量 | 默认 | 作用 |
|---|---|---|
| `APP_PORT` | `3000` | 监听端口；前端 Orval target、dev 环境、manage.sh、HEALTHCHECK 均按 3000 |
| `API_PREFIX` | `api` | 全局前缀，`/`、`/metrics` 除外 |
| `TRUST_PROXY` | `0` | 反向代理**层数**（整数，非布尔），限流按 `X-Forwarded-For` 倒数第 N 跳取客户端 IP |
| `REDIS_ENABLED` | 未设置视为 `false`（只认字符串 `true`）；`env-example-relational` 为 `true` | 关闭时缓存 / 限流回落进程内实现（队列在 Postgres 上，不受影响）；生产强制 `true` |
| `THROTTLE_ENABLED` / `THROTTLE_TTL` / `THROTTLE_LIMIT` | 未设置视为 `true`（nestjs 只有字符串 `false` 关闭），`env-example-relational` 为 `false` / `60` / `100`；TTL 单位秒，两者为 ≥1 的整数 | 限流开关与默认规则（§4.1.3）；parity / e2e 环境两边都设为 `true` |
| `METRICS_ENABLED` / `METRICS_PATH` / `METRICS_TOKEN` | `true`（只有 `false` 关闭）/ `metrics` / 空；`METRICS_TOKEN` 设置时至少 16 个字符，否则启动失败 | 令牌同时保护 `/metrics` 与 `/api/v1/health`；生产未配令牌时两者都 403；`METRICS_ENABLED=false` 只关闭 HTTP 指标与默认运行时指标（§4.6） |
| `LOG_LEVEL` / `LOG_PRETTY_PRINT` | 未设置时生产 `info` / `false`，其它 `debug` / `true`；`LOG_LEVEL` 取 `fatal/error/warn/info/debug/trace/silent` | 日志级别与格式（§8） |
| `FILE_DRIVER` | `local` | `local` / `s3` / `s3-presigned`；后两者要求 `ACCESS_KEY_ID`、`SECRET_ACCESS_KEY`、`AWS_DEFAULT_S3_BUCKET`、`AWS_S3_REGION`，可选 `AWS_S3_ENDPOINT` |
| `AWS_S3_FORCE_PATH_STYLE` | `true`（只有 `false` 关闭） | 仅在设置了 `AWS_S3_ENDPOINT` 时生效：MinIO 为 `true`，阿里云 OSS 为 `false` |
| `FILE_IMAGE_MAX_SIZE` / `FILE_PRIVATE_IMAGE_MAX_SIZE` / `FILE_CONTENT_IMAGE_MAX_SIZE` / `FILE_AUDIO_MAX_SIZE` / `FILE_IMPORT_MAX_SIZE` / `FILE_EXPORT_MAX_SIZE` | `5242880` / `10485760` / `5242880` / `20971520` / `52428800` / `20971520`（字节，≥1 的整数） | 各上传用途的大小上限（§4.6 files）；multipart 单文件上限取其最大值 |
| `WECHAT_APP_ID` / `WECHAT_APP_SECRET` / `ALIPAY_APP_ID` | 空 | 除登录外，`healthCheck` 据此决定是否探测微信 / 支付宝（§4.6） |
| `MAIL_HOST` / `MAIL_PORT` / `MAIL_USER` / `MAIL_PASSWORD` / `MAIL_DEFAULT_EMAIL` / `MAIL_DEFAULT_NAME` / `MAIL_IGNORE_TLS` / `MAIL_SECURE` / `MAIL_REQUIRE_TLS` | `MAIL_PORT` 587，三个布尔只认 `true` | nestjs 任何环境都要求 `MAIL_HOST`、`MAIL_DEFAULT_EMAIL`、`MAIL_DEFAULT_NAME`；本服务与 fastapi-temp 一样只在生产强制，非生产未设置 `MAIL_HOST` 时邮件只写日志 |
| `SWAGGER_ENABLED` | 非生产 `true`、生产 `false` | 控制 `/docs`、`/docs-json` |
| `APP_FALLBACK_LANGUAGE` | `en` | 语言回落 |
| `AUTH_JWT_TOKEN_EXPIRES_IN` 等 `*_EXPIRES_IN` | `15m` / `3650d` / `30m` / `1d` | 按 npm `ms` 规则解析（§6.1） |
| `SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD` | 空 | 仅生产 seed 使用（§7「Seed」）；nestjs 侧生产判定变量为 `ENV_MODE=prod`，本服务为 `APP_ENV=production` |

nestjs 未使用 `REDIS_KEY_PREFIX`（键前缀写死在代码里，如 `throttle:`、`lock:`、`session:rotated:`），本服务同样不引入，Redis 键名与 nestjs 逐字一致，便于共用实例。

fastapi 侧的 `WEB_CONCURRENCY`、`PROMETHEUS_MULTIPROC_DIR` 在 Go 中不需要（单进程多 goroutine；Go 1.25 起 `GOMAXPROCS` 自动感知容器 CPU 限额）；需要时用标准的 `GOMEMLIMIT`。上游的 `PG_URL`、`HTTP_PORT`、`RMQ_*`、`NATS_*`、`GRPC_PORT`、`TRACING_*` 全部改为 nestjs 变量名。

配置结构按分组拆成多个子结构体（app、database、redis、auth、mail、file、sms、wechat、alipay、throttle、observability），敏感字段使用自定义 `Secret` 类型（`String()` / `LogValue()` 输出 `***`），启动时一次性校验。

---

## 10. 工程配置（业界当前最佳实践）

### 10.1 项目配置

| 项 | 做法 |
|---|---|
| 模块 | `github.com/xingyunqi-tech/go-template`；`go 1.27.0` + `toolchain go1.27.4` |
| 工具依赖 | 纯 Go 工具放 `tools/go.mod`，`go tool -modfile=tools/go.mod <name>` 调用（oapi-codegen、goose、mockgen、govulncheck、gotestsum、go-test-coverage）。主 go.mod 只含运行时依赖，govulncheck 和 Renovate 的结果更干净；上游把工具写进主模块导致 go.mod 数百行间接依赖。例外：golangci-lint 用官方二进制 / action，sqlc 需要 cgo、用官方二进制或 `sqlc/sqlc` 镜像，prek 用其发布的二进制；这三者版本写在 Makefile / workflow 变量中，由 Renovate 更新 |
| 生成代码 | `make generate` = oapi-codegen + sqlc + mockgen；生成文件带 `// Code generated ... DO NOT EDIT.`，golangci-lint 按 generated 排除；生成结果入库，CI 只在 type-check job 校验一次无 diff |
| golangci-lint | 沿用上游 `.golangci.yml` 的 linter 集合，补充 `sloglint`、`contextcheck`、`exhaustruct`（仅 config 包）、`forbidigo`（禁止 `fmt.Print*`、`log.*`、业务代码中的 `os.Getenv`）、`depguard`（entity 不得引用 pgx / gen / net/http；usecase 不得引用 controller）；formatters：gofumpt + gci（去掉与 gci 重复的 goimports） |
| 编译检查 | `go vet ./...` + `go build ./...`（对应 nestjs 的 type-check） |
| 测试 | `gotestsum` 输出 JUnit；`-race -shuffle=on`；build tag 区分 `integration` / `e2e` / `parity` |
| 覆盖率 | unit + integration 合并，`internal/...` 语句覆盖率 ≥ 80%（排除 `gen/`、`sqlcgen/`、mock），由 go-test-coverage（`.testcoverage.yml`）校验；Go 无分支覆盖率，以语句覆盖率为准 |
| 编辑器 | `.editorconfig`；`.vscode/settings.json`（gopls、gofumpt、golangci-lint）、`extensions.json` 推荐 `golang.go` |
| 任务入口 | 根目录 `Makefile`（保留上游 help 写法）：`dev`、`generate`、`check`、`fmt`、`lint`、`test`、`test-integration`、`test-e2e`、`test-contract`、`test-parity`、`coverage`、`migrate`、`migrate-create`、`seed`、`openapi-sync`、`infra`、`ci`、`clean`；其中 `openapi-sync`、`test-contract`、`test-parity` 与 fastapi-temp、springboot-template 同名同义；CI 与本地调用同一组目标 |

### 10.2 Git 钩子（prek，与 fastapi 方案一致）

| 阶段 | 内容 |
|---|---|
| pre-commit | golangci-lint `fmt` + `run --new-from-rev=HEAD`（仅暂存文件所在包）、typos、`go mod tidy -diff`、gitleaks（密钥泄露）、生成代码新鲜度（改了 `openapi/`、`queries/` 时跑 `make generate` 并检查 diff）、注释变更历史提示（见 §10.5，只告警不阻断）、end-of-file、trailing-whitespace |
| commit-msg | conventional commits，类型集合与 nestjs commitlint 一致，header ≤72（`scripts/commit-msg.sh`，不依赖 Node） |
| pre-push | `make check`（lint + vet + unit） |

### 10.3 Docker

- 多阶段：builder `golang:1.27-trixie`（与 runtime 的 Debian 13 一致），`--platform=$BUILDPLATFORM` + `GOOS/GOARCH=$TARGETOS/$TARGETARCH` 交叉编译（不经 QEMU），BuildKit cache mount 缓存 `GOMODCACHE` 与 `GOCACHE`，`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=..."` → runtime `gcr.io/distroless/static-debian13:nonroot`（按 digest 固定，Renovate 更新）
- runtime 不含 shell、curl、wget：`HEALTHCHECK` 用 `["/app", "healthcheck"]` 子命令访问 `/api/v1/health/live`；`/app healthcheck --ready` 访问 `/api/v1/health/ready`，供 manage.sh 使用
- 迁移文件、i18n、邮件模板、`nestjs-baseline.json`、`validation.json` 全部 `embed` 进二进制，镜像只有一个文件
- 构建：buildx 多架构（amd64 / arm64），生成 SBOM 与 provenance attestation
- `.dockerignore` 白名单写法；hadolint 检查 Dockerfile
- compose：`docker/` 下与 nestjs 同名同职责（base、postgres、redis、dev、test、ci、ecs），邮件服务用 Mailpit；dev 用 `develop.watch` + 重新构建（或本机 `go run` 连 compose 中的依赖）

### 10.4 依赖与安全

- Renovate：`gomod`（`postUpdateOptions: ["gomodTidy", "gomodUpdateImportPaths"]`，同时覆盖 `tools/go.mod`）、Docker 镜像、GitHub Actions 统一管理；patch / minor 分组，工具链与测试依赖分组自动合并（CI 通过前提下），major 单独 PR；版本上限与 nestjs 写法一致：Go 保持 1.27.x（`golang` 镜像与 go 指令 `allowedVersions: <1.28`）、Postgres / Redis 镜像不接收 major；otel 系列模块成组更新；与 nestjs 相同，用 `customManagers`（regex）更新 workflow 中 `*_IMAGE:` 环境变量（actionlint、gitleaks、trivy）以及 Makefile / workflow 中 golangci-lint、sqlc、prek 的版本变量
- 漏洞扫描（与 nestjs 的 check.yml 一致）：govulncheck（对应 `pnpm audit --prod`，只看运行时代码可达的漏洞）、Trivy（镜像 HIGH / CRITICAL 且有修复版本，`--ignore-unfixed`）、gitleaks（只扫本次 push / PR 引入的提交）、zizmor（workflow 安全，`.github/zizmor.yml` 配置忽略项，门槛 medium）、actionlint（workflow 语法）
- GitHub Actions 全部按 commit SHA 固定版本并注释版本号，由 Renovate 更新

### 10.5 文档与仓库约定（CLAUDE.md）

README（中文，结构参照 nestjs-template）、CLAUDE.md / AGENTS.md（软链接）、`docs/`：architecture、auth、database、deploy、testing、cli、contract（契约同步流程与已知差异）、benchmark（压测基线）。

CLAUDE.md 替换上游内容，写明架构决策、禁止事项，并原样纳入以下约定：

**注释规范**

- 禁止段落式注释：不复述代码在做什么，不写"为什么不用另一种写法"的论证，不在 JSDoc 里写背景故事。
- 注释只描述**当前**代码，不写变更历史：禁止"原来/之前/现在改/不再/已修复/回归/新增了/移除了"等字眼；变更原因交给 commit message。
- 测试用例名只陈述行为，不写"回归 / 防止 / 修复"。
- 通过 Agent 派发的子任务同样受本规则约束，派发时必须把本规则原文写进提示词，并要求其收尾时统计新增注释行数。

Go 中的对应（单独列出，不改动上面的原文）：

- 「JSDoc」对应 godoc 注释（包、导出标识符上方的注释）。
- 「测试用例名」对应 `TestXxx` 函数名与 `t.Run` 子测试名。

**提交与推送**

- **只有用户当次明确说「提交 / commit」才能提交**。"开始做吧""继续""按这个改""可以"都只是让你写代码，不是让你提交。
- 一次提交授权只覆盖那一批改动，**不延续到后续任务**。下一轮要提交必须重新拿到许可。
- 改完停在工作区，汇报改了什么 + 验证结果，等用户 review。想提交就问一句，别自己决定。
- push 同理，且比 commit 更严：没听到"推 / push"就不要推。
- 通过 Agent 派发的子任务同样受本规则约束，派发时必须把本规则原文写进提示词。

配套自动检查：prek pre-commit 钩子扫描暂存 diff 中新增的 `//` 注释行和 `t.Run` 名称，只匹配变更历史句式（如「原来…」「之前…」「现在改为」「已修复」「新增了」「移除了」、英文 `previously` / `no longer` / `regression`），**只告警不阻断**，避免「不再重试」这类描述当前行为的正常注释被误拦；最终以 review 为准。golangci-lint 的 `godot` 等注释格式规则保留，`godox` 禁止提交 TODO / FIXME。

### 10.6 测试策略（五层）

| 层 | 测什么 | 框架 / 库（版本） | 目录 | make 目标 | CI job |
|---|---|---|---|---|---|
| 1 单元 | usecase、中间件（requestid、throttle 头、auth、RBAC、i18n、错误翻译 §4.1.1）、`pkg/*`（jsontime、password、jwtx、validatorx、ratelimit 脚本参数）、view 类型（对照 nestjs 录制样本逐视图断言键存在与 `null`） | `go test` + testify 1.12.1；mockgen（go.uber.org/mock 0.6.0）生成 usecase / repo 端口 mock，简单端口用手写 fake；`httptest` | 与源码同目录 `*_test.go`（无 build tag）；样本 `testdata/fixtures/` | `make test` | test |
| 2 API / 集成 | repo 的 SQL 与约束、唯一约束查表、刷新轮换 CAS、`lastActiveAt` 节流、限流 Lua、锁、River 入队；通过完整 chi 路由的 HTTP 级测试（真实中间件链 + 真实 Postgres / Redis） | testcontainers-go 0.45.0（modules/postgres、modules/redis 0.45.0）+ `httptest.Server`；River `rivertest` | 与源码同目录 `*_integration_test.go`（`//go:build integration`）；公共夹具在 `internal/testutil` | `make test-integration` | test |
| 3 E2E | 完整部署形态：migrate job + seed + api + worker，黑盒 HTTP 走完注册 → 确认邮件（Mailpit API 取链接）→ 登录 → 刷新 → 会话管理 → 退出、文件上传、限流、健康检查 | `go test` + `net/http`，`APP_URL` 指定目标；compose（base + postgres + redis + ci） | `tests/e2e`（`//go:build e2e`） | `make test-e2e` | e2e |
| 4 契约 | 见下 | kin-openapi 0.153.0 + chi.Walk | `tests/contract` | `make test-contract` | contract |
| 5 Parity | 同一套黑盒用例分别打本服务和 nestjs-template（同一份 seed），比对状态码与响应体 | `go test` + `net/http`；JSON 归一化比较 | `tests/parity`（`//go:build parity`） | `make test-parity` | parity |

**第 2 层：隔离方式**

| 场景 | 隔离 | 并行 |
|---|---|---|
| 公共准备 | 每个测试包的 `TestMain` 启动一组 Postgres / Redis 容器，`goose up` + seed 写入模板库 `app_template`；每个包从模板克隆一份工作库 | — |
| 默认（repo 测试与大部分 HTTP 级测试） | repo 通过 `DBTX` 接口（`*pgxpool.Pool` 与 `pgx.Tx` 都满足，sqlc 生成代码即基于此接口）取连接；每个用例开一个外层 `pgx.Tx`，repo 与 HTTP 测试中的 tx 中间件都绑定到它，内部 `Begin` 转为 SAVEPOINT（「提交」只释放 SAVEPOINT），用例结束 `Rollback` | `t.Parallel()`（每个用例独占一条连接） |
| 依赖真实提交的用例：并发刷新 CAS、§6.3 重放吊销在 401 后仍生效、§5 第 8 项 4xx 提交 / 数据库错误回滚、提交后清缓存、River 提交后可见、`lastActiveAt` 节流 | 每个用例 `CREATE DATABASE t_<随机> TEMPLATE app_template` 克隆独立库，使用真实连接池与 Redis，用完 `DROP` | `t.Parallel()` |
| Redis | 键名写死且与 nestjs 逐字相同（`throttle:`、`lock:`、`jwt:session:`、`session:rotated:` …），生产代码不接受键前缀，因此不能按前缀隔离。每个测试包一个 Redis 容器，用到 Redis 的用例从夹具的逻辑库池（DB 0–15，带缓冲 channel）独占借一个，`FLUSHDB` 后使用、归还前再 `FLUSHDB`；fastapi-temp 的做法是每个 xdist worker 一个容器、用例内 `flushdb` 后串行执行 | 同时最多 16 个 |
| River | `rivertest` + `river.Config{TestOnly: true}`，用 `rivertest.RequireInserted` 断言入队，不启动 worker；重试 / 退避 / 最终失败告警用直接调用 worker 的 `Work` + 注入时钟测试；真正启动 `river.Client` 的端到端用例放在克隆库中 | — |
| 文件 | local 驱动写 `./files`（相对工作目录，与 nestjs 相同）；`t.Chdir` 不能与 `t.Parallel()` 同用，存储根目录做成构造参数，测试注入 `t.TempDir()`；s3 / s3-presigned 用 testcontainers-go `modules/minio`（每包一个容器、每用例独立桶） | `t.Parallel()` |
| 邮件 | 测试内起 SMTP 接收器（如 `emersion/go-smtp` 的测试服务器，或 Mailpit 容器），对收到的 subject / text / html 与 golden 文件逐字节比对 | `t.Parallel()` |

开发机已有本地 Docker：集成测试（testcontainers）、compose e2e、parity、镜像构建在每个阶段都必须在本地跑通，CI 再跑一遍；testcontainers 起不来时测试直接失败，不跳过（不使用 `SkipIfProviderIsNotHealthy`）。Docker Desktop 的 socket 不在默认路径时，夹具按 fastapi-temp 的做法在未设置 `DOCKER_HOST` 时探测 `~/.docker/run/docker.sock`。

**第 3 层：E2E 与上游 `integration-test/` 的关系**

上游 `integration-test/` 的思路（应用跑在 compose 中，测试只通过网络访问）保留，布局调整：目录改为 `tests/e2e`，删除 gRPC / AMQP / NATS 客户端与 `integration-test/Dockerfile`；测试不再打成镜像在 compose 网络内运行，而是在 runner 上 `go test -tags e2e` 访问映射端口，失败时上传 compose 日志。e2e 环境 `THROTTLE_ENABLED=true`。

**第 4 层：契约测试**

spec-first 让大部分一致性在生成阶段就成立，但仍逐项断言，防止有人绕过生成流程：

| 断言 | 方法 |
|---|---|
| `/docs-json` 返回内容与 `openapi/nestjs-baseline.json` 逐字节相同 | `httptest` 启动完整路由后取响应比对 |
| 共享产物与 nestjs 当前导出一致 | parity job 中重跑 `openapi-sync` 比对（本层只校验 `SOURCE` 五个键齐全、`environment` 为 `NODE_ENV=development FILE_DRIVER=local`；有同级 fastapi-temp 时再校验共享产物与其逐字节相同） |
| 校验执行器与 nestjs 一致 | `tests/contract/fixtures/validation-cases.json` 全部用例（约 4516 条）逐条通过；M1 起为门禁 |
| paths 顺序、operationId、schema 名 | 解析文档后与 nestjs 基线逐项比对（同一文件时自然通过；用于 `openapi-sync` 后的变更审阅输出） |
| `scope.yaml` 中每个 operation 都有已注册处理器 | `chi.Walk` 收集 method + 路由模板，与文档 path（`{id}` 归一化）对照；缺失即失败 |
| 每个已注册路由都在文档中或在显式白名单 | 同上反向检查；白名单：`/metrics`、`/docs`、s3-presigned 驱动的 `POST files/upload`（JSON 版） |
| policy 完整且与文档 `security` 交叉一致 | 遍历 `scope.yaml`，每个 operationId 必须有 policy；规则见 §4.6 末段 |
| `validation.json` 中每个约束 Go 侧均已实现 | 遍历文件，未实现即失败 |

生成代码无 diff 只在 type-check job 校验，本层不重复。

**第 5 层：Parity**

- 用例覆盖本期全部端点的正常与错误路径：422 的字段键、消息与拼接顺序，401 / 403 / 404，§4.1.1 的翻译规则（分别以 `Accept-Language: en` 与 `zh` 执行），`X-Request-Id` 回写，上传返回 201
- 限流：两边都设 `THROTTLE_ENABLED=true`；放行响应校验 `X-RateLimit-Limit` / `X-RateLimit-Remaining` / `X-RateLimit-Reset` 存在且为整数，429 校验 `Retry-After` 存在且为整数、不带 `X-RateLimit-*`，不比较其余数值（GCRA 与固定窗口不同）
- 文件与健康检查：与 fastapi-temp `tests/parity/test_files_parity.py` 一一对应（local 驱动的上传成功 / 错误矩阵含 multipart 边界、`en` / `zh-CN` 两种语言、公开下载头、私有文件访问、uuid 校验）；health 的 503 与 terminus 形状由集成测试覆盖（parity 环境无法让 nestjs 的依赖故障）
- 不比较 `Content-Language`；`Vary` 只在 CORS 用例中比较（§4.1），其它用例不比较
- 全局契约用例与 fastapi-temp `tests/parity/test_global_contract.py` 一一对应：未匹配路由（含编码路径、查询串）、大小写与尾斜杠、`HEAD`、Request ID 沿用 / 生成、安全头逐头相等、CORS 简单请求与预检（含 `Vary`）、非法 JSON 先于路由返回 400、超限 413、`/api/%68ealth` 404
- 目标地址参数化：`PARITY_TARGETS=go=http://localhost:3000,nestjs=http://localhost:3001`（nestjs 由共享的 `docker/nestjs.compose.yaml` 启动，默认映射 3001）；同一用例对两个目标分别执行，再比较状态码、契约内响应头与响应体
- 归一化：忽略 `id`、`createdAt`、`updatedAt`、`lastActiveAt`、`token`、`refreshToken`、`tokenExpires`、`hash` 等易变字段（只校验类型与格式，如时间必须匹配 `\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z`）
- 同一套用例对 nestjs 也必须通过，用来证明用例写的是 nestjs 的真实行为；另外直接复用 nestjs 的 `test/user/auth.e2e-spec.ts`（11 个用例，以 `APP_URL` 黑盒访问）打本服务
- 前提：seed 账号一致（`admin@example.com`、`john.doe@example.com`，密码 `Secret00`）、测试用密钥一致、端口可配置；共库对照时 `PASSWORD_HASHER=bcrypt`
- fastapi-temp 的 `tests/parity` 目前是 pytest 代码，不能逐字节复用；本仓库按其用例清单逐条移植（用例名、请求、断言一致），抽成语言无关数据文件见 §13 第 8 项

---

## 11. CI / CD（参照 nestjs-template）

### 11.1 check.yml

触发：push / PR 到 main、master、`release/**`，手动。`permissions: contents: read`；concurrency 取消旧运行；每个 job `timeout-minutes: 15`（e2e、parity 30）；`actions/setup-go` 读取 `go.mod`（`go-version-file`）并启用模块 / 构建缓存；checkout 一律 `persist-credentials: false`。job 划分与 nestjs 的 check.yml 对应（lint / type-check / test / build / migrations / workflow-lint / security / image-scan / e2e），另加 contract、parity。

| job | 内容 | 依赖 |
|---|---|---|
| lint | golangci-lint-action（含 formatters 检查）、typos、hadolint、`go mod tidy -diff`（主模块与 tools 模块） | – |
| workflow-lint | actionlint（docker 镜像）、zizmor-action（min-severity medium） | – |
| type-check | `go vet ./...`、`go build ./...`、`make generate` 后 `git diff --exit-code`（全流水线唯一一次生成代码校验） | – |
| test | 第 1、2 层：`gotestsum -- -race -shuffle=on -tags integration`（testcontainers，起不来即失败），合并覆盖率后由 go-test-coverage 校验 ≥80%，上传覆盖率与 JUnit 报告 | – |
| migrations | postgres service；goose up → `sqlc vet` → down-to 0 → up；约束命名检查 | – |
| contract | 第 4 层 | – |
| security | govulncheck、gitleaks（docker 镜像，`--log-opts` 为本次提交范围） | – |
| build | `go build` 全部子命令（linux amd64 / arm64） | – |
| image-scan | 构建生产镜像 + Trivy（HIGH / CRITICAL，`--ignore-unfixed`）；失败单独标红，不阻塞 e2e | – |
| e2e | 第 3 层：compose（base + postgres + redis + ci）起 migrate → seed → api + worker，跑 `tests/e2e`，失败上传日志 | lint、type-check、test、build、migrations、workflow-lint、security |
| parity | 第 5 层：checkout nestjs-template（固定到 `openapi/SOURCE` 的 `commit`，当前 `02fdb4d`；`export-validation.ts` 取 `validation_script` 提交，要求已推送到 origin），以共享的 `docker/nestjs.compose.yaml`（`FILE_DRIVER=local`，映射 3001）启动 nestjs，本服务另用一个空库迁移 + 同一份 seed，两边 `THROTTLE_ENABLED=true`；重跑 `scripts/openapi-sync.sh`，`openapi/`、`db/nestjs-schema.sql`、`tests/contract/fixtures/validation-cases.json` 无 diff；跑 `tests/parity` 与 nestjs `test/user/auth.e2e-spec.ts` | build、migrations |

另有 `drift.yml`：每周定时 + 手动，对 nestjs release 分支 HEAD（而非 `SOURCE` 固定提交）重跑 `openapi-sync`，与入库的共享产物比对，有差异时失败并在 job summary 中列出，提醒执行 `make openapi-sync`。

本地与 CI 执行同一组目标：开发机已有 Docker，`make test`（含 testcontainers 集成测试）、`make test-e2e`、`make test-parity`、镜像构建在每个阶段结束前都在本地跑通，CI 结果用于复核。

parity job 是「前端无改动切换」最直接的证据。nestjs-template 为私有仓库时，parity / drift job 需要只读 deploy key 或 fine-grained token（作为 secret，仅这两个 job 可用）。

### 11.2 deploy.yml

与 nestjs 一致：手动触发，输入 deploy、target（aliyun / tencent / all）；多架构镜像推 GHCR（+ 腾讯 TCR），带 SBOM / provenance；矩阵部署 `aliyun-production` / `tencent-production`；scp compose + manage.sh；SSH 拉镜像（3 次重试）→ 执行 migrate 服务 → `up -d --remove-orphans` → 轮询 `/api/v1/health/ready` 30 次 → 保留最新 3 个镜像；notify `timeout-minutes: 5`。

### 11.3 cleanup-registry.yml

与 nestjs 一致：每周一 03:00（北京时间）+ 手动 dry_run + workflow_call；GHCR 保留 2 个带标签镜像并删无标签；TCR 未配密钥时跳过。

### 11.4 deploy/manage.sh

命令集同 nestjs（start / stop / restart / remove / status / logs / health / cleanup / help），容器名 `go-api`；distroless 镜像内没有 wget / curl，健康检查用 `docker compose exec -T api /app healthcheck --ready`（或宿主机 `curl http://localhost:3000/api/v1/health/ready`）；`start` 先跑 migrate 服务。

---

## 12. 实施阶段

| 阶段 | 内容 | 验收 |
|---|---|---|
| **M0 清理与骨架** | 按 §0.1 删除；module path、Go 1.27.4 toolchain；`tools/go.mod`；chi + net/http 骨架（Express 路由语义）；`make openapi-sync`（从 fastapi-temp 复制或重跑共享脚本）写入 §4.3「共享产物」全部文件；oapi-codegen 配置（skip-prune、type-mapping、overlay、user-templates）+ `make generate`；`/docs-json`；§10 工程配置（golangci-lint、prek、Makefile、Renovate、编辑器、CLAUDE.md）；验证 pgx 对标准库 `uuid.UUID` 的编解码 | `make check`、`make test` 全绿；`/docs-json` 与 `openapi/nestjs-baseline.json` 逐字节一致；共享产物与 fastapi-temp 逐字节一致；第 4 层契约测试框架可运行 |
| **M1 全局契约** | 成功 / 错误信封（strict 模板覆盖，200 / 上传 201）、§4.1.1 翻译规则、唯一约束表、**校验执行器 `pkg/validatorx`（含 validator.js 语义移植，本阶段最大工作量）**、i18n、Request ID、CORS、安全头、请求体（413 / 400）、分页、policy 表（含 RBAC 标记）、请求处理顺序（请求体解析先于路由与鉴权）、原始路径匹配、路径 id 400 | **`tests/contract/fixtures/validation-cases.json` 约 4516 条 nestjs oracle 用例全部通过**；第 1 层：每种错误形状、翻译规则、中间件顺序（非法 JSON 400 先于 404 / 401，401 先于 422）、非法 id 返回 400 有单测；第 4 层：policy 与文档交叉一致、validation.json 约束全部实现；第 5 层全局契约用例（与 fastapi-temp `test_global_contract.py` 对应）对 nestjs 与本服务均通过 |
| **M2 数据层与认证核心** | goose 初始迁移、sqlc 查询、核心表、seed（含生产 `SEED_ADMIN_*`）；邮箱注册 / 登录 / 确认 / 找回、refresh 轮换、logout、me、改密、注销、会话管理接口；RBAC；users 接口；view 类型；tx 中间件（§5 第 8 项结束规则） | 第 1 层：user 各视图键 / null 与 nestjs 样本一致，登录响应键顺序、两种令牌 `role` 形状、`exp` 计算有单测；第 2 层：repo、刷新 CAS、唯一约束查表、§7 结构快照比对的集成测试本地与 CI 均通过并可并行；**重放刷新返回 401 且会话被吊销（之后 access 令牌 401 `Session expired`）**；nestjs `test/user/auth.e2e-spec.ts`（11 个用例）以 `APP_URL` 指向本服务全部通过；第 5 层认证与用户部分对两个后端均通过；vite-vue3-temp 不改代码指向本服务，可完成登录、刷新令牌、获取用户信息、退出（范围外接口返回 404，后台考试业务页面报错属预期，不在验收范围）。不以「Orval 重新生成零 diff / `vue-tsc`」作验收：`/docs-json` 与基线逐字节相同，这两项必然成立，不构成证据 |
| **M3 基础设施** | 缓存 / 锁、GCRA 限流（§4.1.3）、日志、指标、链路、健康检查（监控令牌）、Sentry、River 队列与周期任务、审计、告警、账号清理（含补偿任务）、邮件（模板移植 + 队列）、文件三驱动（含 s3-presigned 手写 handler、multipart 流式解析）、优雅停机；复制 fastapi-temp 的 `nest_handlers.json` 与 `tests/fixtures/nestjs_mail/` | 第 1 层：`normalizeIp`、限流键（与 nestjs `sha256` 键逐字相同）、GCRA 参数、指标路由标签转换、邮件 golden 用例；第 2 层：限流头出现条件（放行 / 429 / 关闭 / 未匹配路由）、按用户与按 IP 计数、Redis 故障回落与恢复（计数器 + 告警）、锁（Redis 与回落）、health 三个探针成功与 503 body、监控令牌、`/metrics` 名称 / 标签 / 桶 / `<unmatched>` / 499、上传矩阵（local 与 MinIO 上的 s3 / s3-presigned）、私有文件访问、River 入队 / 重试 / 去重 / 最终失败告警、账号清理全流程与补偿、两个清理任务的条件；第 5 层：限流头、files parity 全部用例；SIGTERM 时在途请求完成后退出（集成测试） |
| **M4 扩展登录与通用模块** | 短信、手机号、微信、支付宝登录；8 个通用模块；代码生成器 | uniapp-template 指向本服务可手机号登录；`scope.yaml` 覆盖本期全部接口，第 4 层零缺失；第 5 层覆盖全部本期端点（含 422 响应体） |
| **M5 交付** | Dockerfile、compose、manage.sh、check / deploy / cleanup-registry / drift 四个 workflow、安全扫描、README / docs（含 `docs/contract.md`） | CI 全绿（含 e2e、contract、parity、security、image-scan）；覆盖率 ≥80%；`make ci` 本地通过（开发机有 Docker）；k6（v2.3.0 或当时最新）压测登录、刷新、`auth/me`、列表四个接口，结果记录到 `docs/benchmark.md`，与 nestjs 同环境对比 |

阶段流程：每个阶段「开发 → 子 agent review → 修复 → 本地提交」后再进入下一阶段；阶段验收中的容器类检查（testcontainers、compose e2e、parity、镜像构建）在本地跑通后才提交；不 push。

---

## 13. 待决策

| # | 问题 | 建议 |
|---|---|---|
| 1 | 路由：chi 还是标准库 ServeMux | chi（`Walk` 用于契约测试、子路由中间件、`StripSlashes`）；差异小，若追求零依赖可换 ServeMux，生成代码只改一个开关 |
| 2 | SQL：sqlc 还是其它 | sqlc + 少量 squirrel 处理动态筛选；sqlc 最近发布 2026-04-22，若半年内无新版仍可用（生成代码不依赖其运行时） |
| 3 | 队列：River 还是 Asynq | River（事务内入队、无需 scheduler 进程、活跃发布）；共库时其表放 `river` schema |
| 4 | 是否与 nestjs 共用表结构（camelCase 列名）；默认密码哈希（`PASSWORD_HASHER`） | 共用，便于切换与对照测试；实际共库时 `DB_SCHEMA_OWNER=external`（表结构归 nestjs，§7）、`PASSWORD_HASHER=bcrypt`；独立部署默认 argon2id |
| 5 | Go 版本策略 | 跟随最新 minor（当前 1.27），Renovate 自动 patch；minor 升级手动（新版发布满一个 patch 版本后）；保证与 Go 官方支持的两个版本兼容即可，不额外维护旧版本 |
| 6 | `encoding/json/v2` 是否采用 | 本期仍用 v1 API（oapi-codegen 生成代码与 view 类型都基于 v1 标签语义）；v2 可更直接控制 `omitzero` 与时间格式，待 oapi-codegen 支持后评估 |
| 7 | class-validator 元数据导出脚本的推送 | 脚本已在 nestjs-template 落地（`scripts/export-validation.ts`，本地提交 `8b5f296`，`release/exam-prep` 领先 origin 1 个提交），由 nestjs 维护者随 DTO 变更更新；三个后端的 `openapi-sync` 使用它并提交同一份产物。待定的只是何时推送到 origin：推送前 CI 无法按 `SOURCE` 的 `validation_script` checkout，只能使用入库产物 |
| 8 | parity 用例是否跨语言共享 | 当前基准是 fastapi-temp `tests/parity`（pytest），Go 按用例清单逐条移植；建议之后抽出一份语言无关的用例数据文件（HTTP 请求 + 比对规则），三个后端各写一个很薄的执行器，放在 template 目录的独立仓库或 fastapi-temp，与 §4.3 其它共享产物同样逐字节复制 |
| 9 | `/docs-json` 是否只暴露已实现接口 | 默认原样输出 nestjs 文档（前端生成代码与 nestjs 完全一致）；可加 `DOCS_SCOPE=implemented` 输出按 `scope.yaml` 过滤后的文档（保持原顺序），供只对接本服务的新前端使用 |
| 10 | orders、payments、shares 是否进第二期 | 作为可选模块进第二期 |
| 11 | 是否把 API 契约抽成独立文档 | 抽出 §4、§6、§7、§9 中的契约部分为与语言无关的《后端模板 API 契约》，放在 template 目录下；nestjs、Python、Go、Java 模板都以它为准；机器可读部分即 §4.3「共享产物」，不在文档中重复；生成工具届时也从 fastapi-temp 移到同一位置 |
| 12 | 仓库远程 | 保留 `upstream` 仅作参考，不再合并上游；新建 `origin` 由用户决定 |
| 13 | 是否复现 Express 的弱 ETag 与 304（nestjs 未关闭 Express 默认的 `etag: weak`，GET 响应带 `ETag`，请求带匹配的 `If-None-Match` 时返回 304 空体） | 不纳入契约，parity 忽略 ETag 头并且不发送 If-None-Match |


决策（2026-11-11）：第 1–7、10–13 项按建议执行；第 8 项不抽取跨语言用例文件，Go 按 fastapi-temp `tests/parity` 用例清单逐条移植；第 9 项不加 `DOCS_SCOPE`，`/docs-json` 只原样输出 nestjs 文档。

---

## 14. 风险

| 风险 | 影响 | 应对 |
|---|---|---|
| 校验行为与 class-validator 不一致（约束集合、顺序、`@Transform`、消息模板、validator.js 字符串规则） | 422 响应体与 nestjs 不同，前端展示的消息不同 | `validation.json` + 未实现约束启动失败 + validator.js 逐函数移植 + 约 4516 条 nestjs oracle 用例作 M1 门禁 + parity 覆盖 422 响应体 |
| 事务结束规则写成「出错即回滚」 | 刷新重放的会话吊销随 401 一起回滚，重放被检测后会话仍然有效 | §5 第 8 项 4xx 提交规则 + §6.3 克隆库集成测试 |
| nestjs 文档 nullable / required 不准确、分组字段缺失 | 生成结构体输出的键与 null 与 nestjs 不同 | 响应用手写 view 类型；单测对照 nestjs 录制样本 |
| oapi-codegen 用户模板随版本变化 | 升级后信封写入或状态码失效、编译失败 | 模板文件最小化覆盖；Renovate 对 oapi-codegen 单独 PR，契约与 e2e 测试兜底 |
| 上传接口随 `FILE_DRIVER` 变化 | s3-presigned 请求被校验拒绝或契约检查误报 | 导出固定 local；s3-presigned 手写 handler + 白名单 + `docs/contract.md` |
| 序列化细节差异（时间格式、null / 缺省、空数组、HTML 转义） | 前端显示或判断异常 | §4.4 对齐表 + 单测 + parity |
| 共库时两套迁移冲突 | 表结构被覆盖或重复创建 | §7 迁移归属约定，`DB_SCHEMA_OWNER=external`；结构对比步骤与 drift job |
| nestjs-template 后续接口变更 | 两个后端契约漂移 | `openapi-sync` 后生成代码编译失败即暴露；drift job 每周比对 nestjs release 分支 HEAD |
| GitHub / 镜像仓库网络不稳 | 依赖、镜像下载失败 | `GOPROXY` 可配置国内镜像，Docker 镜像加速；CI 步骤对网络操作加重试 |
| 标准库 `uuid` 与 pgx 的类型适配 | 主键读写报错 | M0 验证，必要时回落 google/uuid |
| sqlc 发布节奏放缓、依赖 cgo | 新 PostgreSQL 语法支持滞后；无法 `go tool` 安装 | 用官方二进制 / 镜像；生成代码无运行时依赖，可锁版本继续使用；必要时局部手写 pgx 查询 |
| 与 nestjs 共用 Redis 且两边都开启限流 | 限流键名相同而值不同（GCRA 存 TAT 浮点串，nestjs 存 `INCR` 计数）：nestjs 对该键 `INCR` 报错后降级为单实例内存计数并告警，本服务读到小整数时按「已过期」处理，两边计数互不生效 | 共用实例时只在一侧开启限流，或给两个后端配置不同 `REDIS_DB`（会话缓存随之不共享）；parity 环境两边各用独立 Redis |
| multipart 错误语义（multer / busboy）与 Go `mime/multipart` 不同 | 上传错误的状态码与 `message` 和 nestjs 不同 | 流式解析 + 显式错误映射，按 files parity 矩阵逐条对照；文本未在 parity 中核实的项先在 nestjs 上实测 |
| 邮件模板转义与 Handlebars 不同 | 邮件 HTML 字节与 nestjs 不同（`'`、`=`、`` ` `` 的实体、URL 改写） | `text/template` + Handlebars 转义函数，golden 用例逐字节比对 |

---

## 附：版本核对来源

- Go module proxy（`proxy.golang.org/<module>/@latest`，2026-11-10 ~ 11 UTC 查询）：go-chi/chi/v5 5.5.0（2026-10-14）、oapi-codegen/v2 2.8.2（2026-10-31）、oapi-codegen/runtime 1.9.0（2026-11-10）、oapi-codegen/nethttp-middleware 1.3.1（2026-10-23，本方案不用）、getkin/kin-openapi 0.153.0（2026-11-07）、jackc/pgx/v5 5.12.0（2026-10-31）、sqlc 1.31.1（2026-04-22）、golang-migrate/v4 4.20.1（2026-09-09）、pressly/goose/v3 3.30.0（2026-11-10）、ariga.io/atlas 1.3.0（2026-07-29）、redis/go-redis/v9 9.23.0（2026-10-05）、riverqueue/river 0.51.2（2026-11-09，riverpgxv5 同版本）、hibiken/asynq 0.26.0（2026-02-03）、golang-jwt/jwt/v5 5.3.1（2026-01-28）、golang.org/x/crypto 0.58.0（2026-10-09）、golang.org/x/text 0.44.0（2026-11-09）、otelslog 0.21.0（2026-10-02）、prometheus/client_golang 1.25.0（2026-10-08）、go.opentelemetry.io/otel 1.47.0（2026-10-02）、otelhttp 0.72.0（2026-10-02）、exaring/otelpgx 0.13.0（2026-10-08）、sentry-go/otel 0.50.0（2026-10-08）、spf13/viper 1.21.0（2025-09-08）、caarlos0/env/v11 11.4.2（2026-11-02）、knadh/koanf/v2 2.4.1（2026-10-18）、go-playground/validator/v10 10.30.6（2026-10-29）、stretchr/testify 1.12.1（2026-08-17）、testcontainers-go 0.45.0（2026-11-05，modules/postgres、modules/redis 同版本）、golangci-lint/v2 2.15.0（2026-10-16）、gofumpt 0.12.0（2026-09-07）、golang.org/x/vuln 1.9.0（2026-10-11）、vladopajic/go-test-coverage/v2 2.23.0（2026-11-07）、gotest.tools/gotestsum 1.13.0（2025-09-11）、goreleaser/v2 2.20.1（2026-11-10）、gofiber/fiber/v3 3.7.0（2026-11-09）、gin 1.12.0（2026-02-28）、labstack/echo/v5 5.4.4（2026-10-30）、danielgtaylor/huma/v2 2.40.0（2026-10-27）、google/uuid 1.6.0（2024-01-23）、Masterminds/squirrel 1.5.4（2023-03-17）、gorm 1.31.2（2026-06-22）、go-jet/jet/v2 2.16.1（2026-10-06）、stephenafamo/bob 0.50.0（2026-08-11）、nicksnyder/go-i18n/v2 2.7.0（2026-10-27）、go-chi/cors 1.2.2（2025-07-01）、rs/cors 1.11.1（2024-08-29）、go-chi/httprate 0.16.1（2026-10-08）、go-redis/redis_rate/v10 10.0.1（2023-04-02）、alexedwards/argon2id 1.0.0（2023-10-21）、aws-sdk-go-v2/service/s3 1.114.10（2026-11-10）、wneessen/go-mail 0.8.2（2026-11-08）、go.uber.org/mock 0.6.0（2025-08-18）、smartwalle/alipay/v3 3.2.31（2026-07-26）、silenceper/wechat/v2 2.1.14（2026-07-20）
- GitHub Releases API（同日）：getsentry/sentry-go v0.50.0（2026-10-08）、j178/prek v0.5.10（2026-11-04）、evilmartians/lefthook v2.2.2（2026-10-13，未采用）、riverqueue/river v0.51.2、hibiken/asynq v0.26.0、ariga/atlas v1.3.0（2026-08-02）、pressly/goose v3.30.0、golangci-lint v2.15.0、zizmor v1.32.0（2026-11-02）、gitleaks v8.30.1（2026-03-21）、trivy v0.76.2（2026-11-05）、actionlint v1.7.12（2026-03-30）、hadolint v2.15.1（2026-07-31）、typos v1.52.2（2026-11-11）、renovate 44.227.4（2026-11-10）、actions/checkout v7.0.1（2026-07-20）、actions/setup-go v7.0.0（2026-07-16）、golangci-lint-action v9.3.0（2026-06-29）、grafana/k6 v2.3.0（2026-09-21）
- go.dev / endoflife.date / Docker Hub：Go 1.27.0 发布于 2026-08-19，最新 1.27.4；1.26 最新 1.26.11；1.25 已于 2026-08-19 停止支持；Go 1.27 发行说明确认新增标准库 `uuid`（pkg.go.dev 显示含 `NewV7`）。同一份发行说明写明 `encoding/json/v2` 已作为标准库包提供，`GOEXPERIMENT=nojsonv2` 只是退出开关，不需要开启实验标志；本期仍不采用（§13 第 6 项）。endoflife.date 列出 PostgreSQL 18.7 / 17.12（2026-11-09）；Docker Hub 查询 `postgres:18.6-alpine` 返回 200，`18.7-alpine` 返回 404，`17.11-alpine` / `17.12-alpine` 请求失败（未核实），故镜像取 18.6 / 17.11（后者与 nestjs CI 一致）
- gcr.io 镜像标签：`distroless/static-debian13` 存在 `nonroot`、`latest`、`debug` 标签
- oapi-codegen v2.8.2 README：支持 `chi-server` / `std-http-server` / `fiber-server` 等、strict server、OpenAPI 3.0 与 3.1、`user-templates`、Overlay、`include-operation-ids`（configuration-schema.json）；`skip-prune`、`type-mapping` 由评审提出，配置项名称在 M0 按该版本 configuration-schema.json 核对
- 上游仓库（`c27e974`）：go.mod、Makefile、.golangci.yml、Dockerfile、docker-compose*.yml、.github/、internal/、pkg/、migrations/、integration-test/、CLAUDE.md
- nestjs-template 契约（`02fdb4d`）：`src/main.ts`（OpenAPI 3.0.0、`/docs-json`、CORS 允许头 / 暴露头）、`src/common/validation-options.ts`、`src/common/filters/all-exceptions.filter.ts`、`src/common/utils/exceptions.util.ts`（`UNIQUE_CONSTRAINT_RESPONSES`）、`src/core/users/domain/user.ts`（`email: string | null`）、`src/modules/files/infrastructure/uploader/{local,s3,s3-presigned}/files.controller.ts`（仅 local 有 `uploadFile`）、`src/infrastructure/health`（`MonitoringTokenGuard`）、`env-example-relational`（`APP_PORT=3000`、`THROTTLE_ENABLED=false`）、`test/user/auth.e2e-spec.ts`（11 个用例）、`.github/workflows/check.yml`（Trivy 0.76.2、gitleaks v8.30.1、actionlint 1.7.12、postgres 17.11）、`.github/renovate.json5`、`vitest.config.mts`、`docker/`（redis 7.4.12）；其余契约细节沿用 fastapi-temp/TEMPLATE_PLAN.md 已核对内容
- nestjs-template 补充核对（`02fdb4d`，2026-11-11）：`src/core/auth/auth.service.ts`（`createSessionAndTokens` 返回键顺序 `refreshToken, token, tokenExpires, user`、登录签发 `role: user.role`、`refreshToken()` 签发 `role: {id}`、`validateUserStatusForAuth(user, 'refresh')`、重放时 `sessionService.revoke` 后抛 401）、`src/core/auth/services/token.service.ts`（`tokenExpires = Date.now() + ms(expiresIn)`）、`src/core/auth/strategies/jwt.strategy.ts`（`Session expired`、`User not found`、`User is not active`）、`src/core/auth/helpers/auth-validation.helper.ts`（422 `userNotActive`）、`src/core/permissions/permissions.guard.ts`（`User not authenticated`、`User role not found`）、`src/core/session/session.service.ts`（`session:rotated:{id}` 只存一个 hash）、`src/common/request-context.ts`（`typeof incoming === 'string'` 且匹配正则才沿用）、`src/main.ts`（helmet 配置、非生产 `enableCors({origin: true, credentials: true})`、`useBodyParser('json', {limit})`、`setGlobalPrefix` 排除 `/`、`metrics`）、`src/infrastructure/database/seeds/relational/user/user-seed.service.ts`（`ENV_MODE === 'prod'`、`SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD`）、`env-example-relational`（`AUTH_*_EXPIRES_IN`）；`8b5f296`（本地未推送）：`scripts/export-validation.ts`
- fastapi-temp 实现（`303bf57`）：`src/app/infrastructure/database/session.py`（`Database.transaction`：`HttpError` 提交、其它异常回滚；连接 `server_settings.timezone = UTC`）、`src/app/core/session/service.py`（`forget` 提交前后各清一次缓存）、`src/app/common/security.py`（`ms` 解析、`exp` 计算）、`src/app/common/middleware.py`（helmet 头集合与取值、CORS 默认方法与 `Vary`、原始路径匹配）、`src/app/core/users/repository.py`（排序追加主键）、`src/app/cli/seed.py`、`openapi/validation.json`（136 个 DTO）、`openapi/SOURCE`、`tests/contract/fixtures/validation-cases.json`（4516 条）、`scripts/{openapi-sync.sh,schema_snapshot.py,gen_validation_cases.py,nestjs/validation-cases.ts}`、`docker/nestjs.compose.yaml`、`tests/integration/{conftest.py,test_schema.py,test_auth.py}`、`tests/parity/*.py`；计划文本修订见 `git diff 76aa96a 303bf57 -- TEMPLATE_PLAN.md`
- nestjs-template M3 相关源码（`02fdb4d`，2026-11-11）：`src/infrastructure/throttler/{user-throttler.guard.ts,redis-throttler.storage.ts,throttler.module.ts,config/throttler.config.ts}`、`node_modules/@nestjs/throttler`（6.7.1：`throttler.guard.js` 的头写入与 `generateKey`、`ip.js` 的 `normalizeIp`）、`src/core/auth/auth.controller.ts`（`@Throttle`）、`src/infrastructure/metrics/{metrics.module.ts,metrics.middleware.ts,metrics.controller.ts,config/metrics.config.ts}`、`src/common/guards/monitoring-token.guard.ts`、`src/infrastructure/health/**`、`node_modules/@nestjs/terminus`（12.1.0：`health-indicator.service.js` 的 `responseTime` 与超时文本、`health-check.decorator.js` 的 `Cache-Control`）、`src/modules/home/home.service.ts`、`src/common/filters/all-exceptions.filter.ts`（503 / 413 走 `HttpStatus[code]`）、`src/modules/files/**`（用途表、守卫、拦截器、三个驱动、`file-url.ts`、`files-account.cleaner.ts`）、`node_modules/multer`（2.5.0 错误文本）、`src/infrastructure/queue/**`、`src/infrastructure/account-cleanup/**`、`src/modules/account-cleanup/**`（补偿任务 `25 * * * *` Asia/Shanghai）、`src/infrastructure/scheduler/**`、`src/infrastructure/redis/lock.service.ts`、`src/modules/monitor/alert.service.ts`、`src/integrations/mail/**`、`src/infrastructure/logger/config/logger.config.ts`、`src/app.module.ts`（pino-http 配置）、`src/main.ts`（`setGlobalPrefix` 排除 `metrics`、中间件顺序）、`env-example-relational`
- fastapi-temp M3 工作区（`303bf57` 之后未提交，2026-11-11 读取，仍在编辑）：`src/app/infrastructure/{throttler,metrics,health,logging,observability,queue,redis,scheduler,account_cleanup}/`、`src/app/modules/files/`、`src/app/integrations/mail/`、`src/app/config/{file,health,log,metrics,observability,throttler}.py`、`tests/integration/test_{throttler,metrics,health,files,background_jobs}.py`、`tests/parity/test_files_parity.py`、`tests/fixtures/nestjs_mail/`、`.env.example`、`docs/contract.md`
- 未核实：Redis 服务端最新版本（endoflife.date 请求失败）、fastapi-temp M3 的 files parity 用例是否已对 nestjs 跑通（multipart 错误文本、415 分支）、`nest_handlers.json` 的生成方式（仓库内无生成脚本）、River 0.51.2 `UniqueOpts.ByState` 的必含状态、go-redis `extra/redisotel` 版本、阿里云 Dysms 官方 Go SDK 版本、Chainguard static 镜像版本、Docker Hub `postgres:17.x-alpine` 标签
