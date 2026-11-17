# go-template

Go 后端模板，与 nestjs-template（`release/exam-prep`）**API 契约一致**：vite-vue3-temp、uniapp-template 用 Orval 从 `/docs-json` 生成的前端可以无改动切换到本服务。
模块路径 `github.com/xingyunqi-tech/go-template`，Go 版本见 `go.mod`。完整方案与阶段划分见 `TEMPLATE_PLAN.md`。

## 命令：一律通过 Makefile

| 任务 | 命令 |
|---|---|
| 下载 golangci-lint / prek / typos 二进制，安装 Git 钩子 | `make install` |
| 启动本地 Postgres、Redis | `make infra` |
| 迁移 / 种子数据 | `make migrate` / `make seed` |
| 本地启动 API / 队列 worker | `make dev` / `make worker` |
| 生成代码（oapi-codegen、sqlc、mockgen） | `make generate` |
| 新建迁移（goose 顺序编号） | `make migrate-create NAME=<name>` |
| 按 nestjs tag 生成模块骨架（处理器、usecase、repo、端口，并加入 `scope.yaml`） | `make module NAME=<包名> TAG=<tag>` |
| 格式化 | `make fmt` |
| lint（golangci-lint、typos、`go mod tidy -diff`） | `make lint` |
| vet + build | `make vet` |
| 单元测试 | `make test` |
| 集成测试（testcontainers，需要 Docker） | `make test-integration` |
| 契约测试 | `make test-contract` |
| e2e（构建镜像，compose 启动后黑盒测试） | `make test-e2e` |
| parity（启动 nestjs 参照服务后比对，需要 Docker） | `make test-parity` |
| 覆盖率门槛（单元 + 集成 + 契约，≥80%） | `make coverage` |
| 同步共享产物 | `make openapi-sync` |
| 本地 CI | `make ci` |

`make check` = lint + vet + 单元测试，是 pre-push 钩子。宣称改动完成之前，`make check`、`make test-integration`、`make test-contract` 必须通过；涉及部署形态的改动再跑 `make test-e2e` 与 `make image`。

工具版本：golangci-lint、prek、typos、sqlc 写在 Makefile 变量里，下载官方二进制到 `bin/`；纯 Go 工具（oapi-codegen、goose、mockgen、govulncheck、gotestsum、go-test-coverage）在 `tools/go.mod`，用 `go tool -modfile=tools/go.mod <name>` 调用。主 `go.mod` 只放运行时依赖。

## 契约：spec-first

- `openapi/nestjs-baseline.json` 是 nestjs `/docs-json` 的原始字节，`GET /docs-json` 原样返回。
- `openapi/{nestjs-baseline.json,validation.json,SOURCE,nest_handlers.json}`、`db/nestjs-schema.sql`、`scripts/{openapi-sync.sh,schema_snapshot.py,gen_validation_cases.py,gen_nest_handlers.py,nestjs/}`、`tests/contract/fixtures/validation-cases.json` 是与 fastapi-temp、springboot-template **逐字节相同的共享产物**，只能通过 `make openapi-sync` 更新，不得手改。接口变更先在 nestjs 落地。
- `openapi/scope.yaml` 列出本期已实现的 operationId；`make generate` 只为它们生成处理器接口与路由注册。新增实现时先把 operationId 加进去，再生成、实现。
- `openapi/overlay.yaml` 只作用于代码生成输入，不改变对外文档。
- `openapi/templates/strict-interface.tmpl` 覆盖 oapi-codegen 的 strict 响应写出：成功一律 HTTP 200（仅 `uploadFile` 为 201），body 为 `{code: 200, message: "success", data}`，无内容时不输出 `data`。

## 分层

```
cmd/app → internal/app → internal/controller/http/v1 → internal/usecase（接口在 usecase/contracts.go）
                                                      → internal/repo（端口在 repo/contracts.go）
                                                      → internal/entity
```

| 目录 | 内容 |
|---|---|
| `internal/entity` | 领域对象与领域错误；不引用 pgx、生成代码、net/http（depguard 检查） |
| `internal/usecase` | 业务规则；不引用 controller |
| `internal/controller/http` | 全局中间件、Express 路由语义、请求管线、`policy.go`（按 operationId 声明鉴权 / RBAC / 查询 DTO 等）、`/docs-json`；`gen/` 为生成代码，`v1/` 实现 `gen.StrictServerInterface`，`v1/view/` 是手写响应视图 |
| `internal/controller/http/httperr` | nestjs HttpException 与 AllExceptionsFilter 翻译规则、业务错误键表、唯一约束表 |
| `internal/i18n` | nestjs `src/i18n` 翻译文件与 `Accept-Language` 解析 |
| `internal/config` | caarlos0/env 解析，变量名与 nestjs 一致，启动时一次性校验并列出全部问题 |
| `internal/repo` | 端口在 `contracts.go`；`postgres/` 为 sqlc 查询与端口实现（请求事务经 context 传递，`AfterCommit` 登记提交后副作用；`jobs.go` 用 River `InsertTx` 在请求事务内入队，River 表在 `river` schema，由 `rivermigrate` 迁移），`redis/` 为与 nestjs Keyv 兼容的缓存与 `lock:<key>` 分布式锁（Redis 不可用时回落进程内），`webapi/mail` 为 nestjs 邮件模板渲染与 SMTP，`webapi/storage` 为 local / S3 文件存储，`webapi/{sms,wechat,alipay}` 为阿里云短信、微信、支付宝开放平台客户端 |
| `internal/usecase` | `auth`（邮箱、手机号、短信、微信、支付宝登录）、`user`、`session`、`permission`、`sms`、`file`、`health`、`alert`、`audit`、`accountcleanup`、`notification`、`appconfig`、`region`、`appprofile`、`consent`、`address`、`favorite`、`feedback` 等业务流程，返回 `entity.Error` / `entity.BusinessError` |
| `internal/worker` | River workers（`mail`、`notification`、`account-cleanup`）与周期任务（会话清理、已读通知清理、账号清理补偿），由 `worker` 子命令运行 |
| `internal/metrics` | Prometheus 指标（名称、标签、桶与 nestjs 一致，样本带 `app`），pgx 查询耗时 tracer |
| `templates/mail` | 从 nestjs 逐字节复制的邮件模板，golden 用例为 `tests/fixtures/nestjs_mail` |
| `migrations/`、`seeds/` | goose 迁移（表结构与 `db/nestjs-schema.sql` 一致）、种子数据（与 nestjs seed 账号一致） |
| `pkg/` | 与业务无关的封装，不引用 `internal/`；`pkg/validatorx` 按 `openapi/validation.json` 复刻 nestjs ValidationPipe（含 validator.js 移植） |

- 配置只从 `config.Config` 读取，业务代码禁止 `os.Getenv`（forbidigo）。
- 日志用 `log/slog`，禁止 `fmt.Print*` 与标准库 `log`。
- 构造函数手写，在 `internal/app` 组装，不引入 wire / fx。
- 未导出的包级常量以下划线开头（`_scalarVersion`）；`mnd` 拒绝裸数字，先定义常量。
- 新约束命名：`<table>_<column>_key`、`<table>_<column>_fkey`、`idx_<table>_<column>`。

## 路由语义（与 Express 一致）

- 路由匹配基于百分号编码的原始路径：`/api/%68ealth` 不命中 `/api/health`。
- 静态段不区分大小写，允许一个末尾斜杠，路径参数保留原始大小写与编码。
- 未匹配路由与方法不符都返回 404 `{code: 404, message: "Cannot <METHOD> <originalUrl>", data: null}`，不带 `Allow`。
- `HEAD` 命中同路径的 `GET` 处理器。
- 处理顺序：（`OTEL_ENABLED` 时 otelhttp）→ Request ID 与语言 → CORS（`OPTIONS` 一律 204）→ 指标 → pino-http 请求日志与 panic 兜底 → 安全头 → JSON 请求体（256kb，413 / 400）→ 路由匹配 → 路径参数解码 → 限流（GCRA）→ 鉴权 → RBAC → `ParseIdPipe`（数字 `:id`）→ 请求体 / 查询 DTO 校验（`uploadFile` 为用途守卫 → multipart 接收 → 查询 DTO）→ 生成的处理器（`Tx` 事务、`Audit` 审计）。新增接口必须在 `policy.go` 声明，缺失时启动失败（`Params` 声明校验路径参数的 DTO）；nestjs 不进文档的接口（`/metrics`、私有文件读取、local 下载）以 `RawRoute` 注册，列在 `Undocumented()`。

## 测试

| 层 | 位置 | 标签 |
|---|---|---|
| 单元 | 与源码同目录 `*_test.go` | 无 |
| 集成 | 与源码同目录 `*_integration_test.go`，夹具在 `internal/testutil` | `integration` |
| e2e | `tests/e2e` | `e2e` |
| 契约 | `tests/contract` | `contract` |
| parity | `tests/parity`，`make test-parity` 自动启动 nestjs 参照服务（compose 项目 `go-template-parity-nestjs`） | `parity` |

- 用例调用 `t.Parallel()`（paralleltest）；使用 `t.Setenv` 的用例除外。
- 集成测试每个包一个 Postgres 容器：`testutil.StartDatabase` 先迁移并写入种子到模板库，用例用 `Clone(t)` 得到独立库，可并行。
- policy 中 `Tx: true` 的接口在事务内执行：成功或 4xx 业务错误提交，5xx 与 SQL 语句本身的错误回滚；鉴权守卫在事务开始前完成，同一请求不会同时持有两条连接。
- MinIO（`chainguard/minio`，按 digest 固定）同样由 testcontainers 启动；local 文件驱动的用例用 `FILE_LOCAL_ROOT` 指向临时目录。
- Redis 每个测试包一个容器：`TestMain` 调 `testutil.StartRedisPool`，用例 `pool.Borrow(t)` 独占借一个逻辑库（DB 0–15，全部借出时阻塞），借出前、归还前各 `FLUSHDB`；用例里只用 `FLUSHDB`，不用 `FLUSHALL` 或跨库的全局命令。
- 端口 mock 由 mockgen 生成（`internal/repo/mocks`、`internal/usecase/<pkg>/mocks`），新增端口时把源文件加入 Makefile `MOCK_SOURCES`；简单端口用手写 fake。
- parity 两侧都开启限流（`THROTTLE_ENABLED=true`），每个请求带不同的 `X-Forwarded-For`；之后对本服务关闭限流再跑 nestjs `auth.e2e-spec.ts`。
- testcontainers 起不来时测试失败，不跳过。Docker Desktop 的 socket 不在 `/var/run/docker.sock` 时，夹具自动使用 `~/.docker/run/docker.sock`。

## 生成文件（不手改）

- `internal/controller/http/gen/gen.go` ← `make generate`（oapi-codegen）
- `internal/repo/postgres/sqlcgen/` ← `make generate`（sqlc，输入为 `migrations/` 与 `internal/repo/postgres/queries/`）
- `internal/repo/mocks/`、`internal/usecase/*/mocks/` ← `make generate`（mockgen，源文件见 Makefile `MOCK_SOURCES`）

## 注释规范

- 禁止段落式注释：不复述代码在做什么，不写"为什么不用另一种写法"的论证，不在 JSDoc 里写背景故事。
- 注释只描述**当前**代码，不写变更历史：禁止"原来/之前/现在改/不再/已修复/回归/新增了/移除了"等字眼；变更原因交给 commit message。
- 测试用例名只陈述行为，不写"回归 / 防止 / 修复"。
- 通过 Agent 派发的子任务同样受本规则约束，派发时必须把本规则原文写进提示词，并要求其收尾时统计新增注释行数。

Go 中的对应：

- 「JSDoc」对应 godoc 注释（包、导出标识符上方的注释）。
- 「测试用例名」对应 `TestXxx` 函数名与 `t.Run` 子测试名。

prek pre-commit 钩子会对新增注释与 `t.Run` 名中的变更历史句式告警（不阻断），最终以 review 为准。

## 提交与推送

- **只有用户当次明确说「提交 / commit」才能提交**。"开始做吧""继续""按这个改""可以"都只是让你写代码，不是让你提交。
- 一次提交授权只覆盖那一批改动，**不延续到后续任务**。下一轮要提交必须重新拿到许可。
- 改完停在工作区，汇报改了什么 + 验证结果，等用户 review。想提交就问一句，别自己决定。
- push 同理，且比 commit 更严：没听到"推 / push"就不要推。
- 通过 Agent 派发的子任务同样受本规则约束，派发时必须把本规则原文写进提示词。

提交信息为 conventional commits（类型同 nestjs commitlint，标题 ≤72 字符），由 commit-msg 钩子检查。
