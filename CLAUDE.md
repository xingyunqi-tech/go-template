# go-template

Go 后端模板，对外行为遵守 `docs/platform-conventions.md`（跨语言平台约定），导出自身的 OpenAPI 3.1 文档供前端生成客户端。
**v2 迁移进行中**：本文件按 `TEMPLATE_PLAN.md` 的批次更新，与约定或方案冲突时以二者为准。
模块路径 `github.com/xingyunqi-tech/go-template`，Go 版本见 `go.mod`。完整方案与阶段划分见 `TEMPLATE_PLAN.md`。

## 命令：一律通过 Makefile

| 任务 | 命令 |
|---|---|
| 下载 golangci-lint / prek / typos 二进制，安装 Git 钩子 | `make install` |
| 启动本地 Postgres、Redis | `make infra` |
| 迁移（创建 `DATABASE_SCHEMA`）/ 种子数据 | `make migrate` / `make seed` |
| 生成签名密钥（`AUTH_SIGNING_KEY` 与公钥） | `make keys` |
| 创建管理员（密码从标准输入读取） | `go run ./cmd/app createsuperuser --email <email>` |
| 本地启动 API / 队列 worker | `make dev` / `make worker` |
| 由 `openapi/src` 生成 `openapi/openapi.json`、校验文档与 oapi-codegen 代码 | `make openapi` |
| 生成代码（openapi、sqlc、mockgen） | `make generate` |
| 新建迁移（goose 顺序编号） | `make migrate-create NAME=<name> [SET=core\|identity]` |
| 格式化 | `make fmt` |
| lint（golangci-lint、typos、`go mod tidy -diff`） | `make lint` |
| vet + build | `make vet` |
| 单元测试 | `make test` |
| 集成测试（testcontainers，需要 Docker） | `make test-integration` |
| 契约测试 | `make test-contract` |
| 迁移 up / down / 命名检查（需要 `make infra`） | `make test-migrations` |
| e2e（构建镜像，compose 启动后黑盒测试） | `make test-e2e` |
| 覆盖率门槛（单元 + 集成 + 契约，≥80%） | `make coverage` |
| 本地 CI | `make ci` |

`make check` = lint + vet + 单元测试，是 pre-push 钩子。宣称改动完成之前，`make check`、`make test-integration`、`make test-contract` 必须通过；涉及部署形态的改动再跑 `make test-e2e` 与 `make image`。

工具版本：golangci-lint、prek、typos、sqlc 写在 Makefile 变量里，下载官方二进制到 `bin/`；纯 Go 工具（oapi-codegen、goose、mockgen、govulncheck、gotestsum、go-test-coverage）在 `tools/go.mod`，用 `go tool -modfile=tools/go.mod <name>` 调用。主 `go.mod` 只放运行时依赖。

## 契约：spec-first

- 接口在 `openapi/src/` 编写：`openapi.yaml`（根文档）、`common.yaml`（错误 schema、分页参数、公共响应）、`paths/<module>.yaml`、`schemas/<module>.yaml`。各文件只含 `paths` 与 `components`，由 bundler 合并，`$ref` 一律写文档内引用 `#/components/...`。
- 成功响应写 `x-envelope: true` 与数据 schema，bundler 展开为 `{code: 0, message: "ok", data}` 的 `<Name>Envelope`（数组为 `<Name>ListEnvelope`，`type: 'null'` 为 `NullEnvelope`）。错误响应引用 `common.yaml` 的 `components/responses`：422 用 `ValidationErrorResponse`，其余用 `ErrorResponse`；429、503 由 bundler 为每个 `/api` 操作补上，不必手写。
- `make openapi`（`tools/openapigen`）输出入库的 `openapi/openapi.json`（`GET /openapi.json` 原样返回）、全内联的 `internal/controller/http/validation.json`（请求校验用），并只为 2xx 响应生成 `internal/controller/http/gen/gen.go`；`x-go-*` 扩展只用于代码生成，不进前两个文档。
- 新增接口：写 spec → `make generate` → 在 `internal/controller/http/policy.go` 声明 policy → 在 `v1/` 实现生成的方法。文档中的 operation 缺 policy、policy 缺 operation、operation 缺处理器，启动都会失败。
- HTTP 状态码按 spec 声明，处理器返回生成的 `<Op><Status>JSONResponse`，信封常量在 `controller/http/response`。

## 分层

```
cmd/app → internal/app → internal/controller/http/v1 → internal/usecase（接口在 usecase/contracts.go）
                                                      → internal/repo（端口在 repo/contracts.go）
                                                      → internal/entity
```

| 目录 | 内容 |
|---|---|
| `internal/entity` | 领域对象（`user.go`、`file.go` 含文件用途与类型白名单）、provider 与短信场景（`identity.go`）、角色与权限码（`rbac.go`，约定 §5.5，代码固定）、错误码表（`errors.go`，与约定 §2.4 逐条一致）、`ValidationError`；不引用 pgx、生成代码、net/http（depguard 检查） |
| `internal/usecase` | 业务规则：`auth`（注册、邮箱与短信登录、第三方 provider 登录与身份匹配、刷新轮换与宽限期、一次性邮件令牌）、`sms`（验证码发送与校验）、`account`（注销、`account.purge` 与补偿扫描）、`file`（上传、签名地址、`getFileContent`、文件 `Purger`）、`maintenance`（过期会话与令牌、审计保留期清理）、`session`（会话与吊销键）、`user`（当前用户与管理、绑定手机号、最后一个管理员）、`note`（CRUD 示例）、`health`、`audit`（写入、查询、保留期清理）；按账号、会话计数与只计失败的共享限流规则经 `usecase.Limits` 在用例内检查；返回 `entity.Error` / `entity.ValidationError`；不引用 controller |
| `internal/controller/http` | 中间件链、操作表、校验适配器、鉴权与限流、幂等键、事务、审计、`policy.go`、ops 路由（health、metrics、`/openapi.json`、`/docs`）；`gen/` 为生成代码，`v1/` 实现 `gen.StrictServerInterface`，`httperr/` 写错误信封，`reqctx/` 为请求 ID、主体、操作 |
| `internal/i18n` | go-i18n 消息文件 `locales/{zh,en}.json`（按 errorKey 与 `validation.<rule>` 组织）与 `Accept-Language` 解析 |
| `internal/config` | caarlos0/env 解析约定 §9 与本服务变量，启动时一次性校验并列出全部问题 |
| `internal/repo` | 端口在 `contracts.go`；`postgres/` 为 sqlc 查询与端口实现（请求事务经 context 传递，`AfterCommit` 登记提交后副作用，River 表在 `DATABASE_SCHEMA`），`redis/` 为缓存、锁、吊销键、幂等键、刷新宽限期暂存、短信验证码与发送 / 失败计数（键带 `{OTEL_SERVICE_NAME}:` 前缀，吊销键 `auth:revoked:sid:` 除外；未配置 `REDIS_URL` 时进程内实现），`webapi/mail` 为 SMTP 发信（未配置 `SMTP_URL` 时只写日志），`webapi/storage` 为 local / S3 文件存储（local 写 `<key>.part` 后改名，S3 经临时文件上传），`webapi/sms` 为阿里云 Dysms（`SMS_PROVIDER=log` 只写日志），`webapi/{wechat,alipay}` 为 provider（失败映射为 `auth.providerCodeInvalid` / `common.badGateway`，支付宝响应用 `ALIPAY_PUBLIC_KEY` 验签）；第三方基址由 `ALIYUN_SMS_ENDPOINT`、`WECHAT_API_BASE`、`ALIPAY_GATEWAY` 配置 |
| `internal/worker` | River 客户端配置（otelriver）与任务：`mail` 队列由 worker 签发一次性令牌（只存哈希）并发信；`account` 队列执行 `account.purge`（依次调用 `Purger`、物理删除用户、发布 `user.deleted`）；每小时的周期任务 `maintenance` 与 `account.sweep`（leader 执行，启动时先跑一次） |
| `internal/events` | `user.deleted` 等事件（CloudEvents 1.0 JSON）与 `Publisher` 端口；默认实现只写日志 |
| `templates/` | 邮件模板 `mail/<kind>.{html,txt}.tmpl`，文案在 i18n 的 `mail.*` |
| `migrations/`、`seeds/` | goose 迁移：`core/` 每个服务都执行，`identity/` 只在签发服务执行，版本表 `goose_<set>_version`；`seeds/seed.json` 为演示账号定义 |
| `pkg/` | 与业务无关的封装，不引用 `internal/`：`optional`（区分缺省与 null 的 JSON 字段，spec 中以 `x-go-type` 引用）、`clientip`（`TRUSTED_PROXIES`）、`jwtx`（EdDSA 签名、JWKS、验签）、`ratelimit`（GCRA 与共享规则表）、`telemetry`、`logger`、`pagination`、`phone`、`jsontime`、`postgres` 等 |

- 配置只从 `config.Config` 读取，业务代码禁止 `os.Getenv`（forbidigo）。
- 日志用 `log/slog`，禁止 `fmt.Print*` 与标准库 `log`。
- 构造函数手写，在 `internal/app` 组装，不引入 wire / fx。
- 未导出的包级常量以下划线开头（`_scalarVersion`）；`mnd` 拒绝裸数字，先定义常量。
- 表名单数 snake_case，主键 `uuid DEFAULT uuidv7()`（应用侧 `uuid.NewV7()` 生成），时间 `timestamptz`；约束与索引命名 `<table>_pkey`、`<table>_<columns>_key|fkey|idx|check`。core 表引用用户只存 `sub`，不建跨 schema 外键。

## 请求处理顺序

- 全局：otelhttp（`http.route` 取自操作查找设置的 `r.Pattern`）→ Request ID → 访问日志与 panic 兜底 → 安全头 → CORS（预检 204）→ 语言。
- 根路径：`/health/live`、`/health/ready`、`/metrics`（`METRICS_TOKEN` 或 `METRICS_PORT`）、`/openapi.json`、`/docs`（`DOCS_ENABLED`）直接处理。
- `/api/v1`：操作查找（未命中：路径存在 405 + `Allow`，否则 404）→ `{service}-ip` 限流 → 令牌验证（public 不读 `Authorization`；optional 无效按匿名，验证错误经 `reqctx.AuthError` 取得）与吊销键检查 → `{service}-user` 与 policy 声明的共享限流 → 权限 → 近期登录 → 已验证邮箱 → 请求体上限（413）、`Content-Type`（415）、JSON 语法（400）→ `openapi3filter` 校验（422；policy `Upload` 的上传接口不读请求体，流式交给处理器，超出 `BodyLimit` 为 413 `file.tooLarge`）→ 幂等键 → 事务 → 生成的处理器 → 审计。
- 路由区分大小写，末尾斜杠不归一。校验错误的 `field` 为 JSON 路径，查询与路径参数为 `query.<name>` / `path.<name>`。

## 测试

| 层 | 位置 | 标签 |
|---|---|---|
| 单元 | 与源码同目录 `*_test.go` | 无 |
| 集成 | 与源码同目录 `*_integration_test.go`，夹具在 `internal/testutil` | `integration` |
| e2e | `tests/e2e`（compose ci：生产镜像，固定测试签名密钥） | `e2e` |
| 契约 | `tests/contract`：bundle 无漂移、`/openapi.json` 与文件一致、路由与文档双向一致、operationId 规则、policy 与 `bearerAuth` 一致、错误响应引用 | `contract` |

- 用例调用 `t.Parallel()`（paralleltest）；使用 `t.Setenv` 的用例除外。
- 集成测试每个包一个 Postgres 容器：`testutil.StartDatabase` 先迁移模板库，用例用 `Clone(t)` 得到独立库，可并行。需要登录的用例用 `testutil.IssueAccessToken`（固定 Ed25519 测试密钥，`testutil.SigningKeyPEM()` 为对应的 `AUTH_SIGNING_KEY`）。邮件用例用 `testutil.StartSMTP` 起进程内 SMTP 服务器，并启动 `app.NewWorkerClient` 处理 mail 队列，`Next(t, to).Token(t)` 取邮件链接中的令牌。
- `internal/app` 的 HTTP 用例经 `testApp.do` 发请求，`/api/v1` 的每个响应都用 `openapi3filter.ValidateResponse` 对照 `openapi/openapi.json` 校验。
- policy 中 `Tx: true` 的接口在事务内执行：2xx 与 4xx 提交，5xx 回滚，响应在提交后发出；鉴权在事务开始前完成，同一请求不会同时持有两条连接。
- MinIO（`chainguard/minio`，按 digest 固定）同样由 testcontainers 启动；文件用例用 `localFiles(t.TempDir())` 或 `testutil.StartMinio`。
- Redis 每个测试包一个容器：`TestMain` 调 `testutil.StartRedisPool`，用例 `pool.Borrow(t)` 独占借一个逻辑库（DB 0–15，全部借出时阻塞），借出前、归还前各 `FLUSHDB`，`RedisDB.URL()` 给出 `REDIS_URL`；用例里只用 `FLUSHDB`，不用 `FLUSHALL` 或跨库的全局命令。
- 端口 mock 由 mockgen 生成（`internal/repo/mocks`），新增端口时把源文件加入 Makefile `MOCK_SOURCES`；简单端口用手写 fake。
- 短信、微信、支付宝用例用 httptest 假服务器，经 `ALIYUN_SMS_ENDPOINT`、`WECHAT_API_BASE`、`ALIPAY_GATEWAY` 指过去；注销用例把 `ACCOUNT_DELETION_GRACE` 设为 0 让 worker 立即清理，`identityApp.workerLog` 收集 worker 日志（含发布的事件）。
- testcontainers 起不来时测试失败，不跳过。Docker Desktop 的 socket 不在 `/var/run/docker.sock` 时，夹具自动使用 `~/.docker/run/docker.sock`。

## 生成文件（不手改）

- `openapi/openapi.json`、`internal/controller/http/validation.json`、`internal/controller/http/gen/gen.go` ← `make openapi`（`tools/openapigen`，输入为 `openapi/src/` 与 `openapi/oapi-codegen.yaml`）
- `internal/repo/postgres/sqlcgen/` ← `make generate`（sqlc，输入为 `migrations/core/` 与 `internal/repo/postgres/queries/`）
- `internal/repo/mocks/` ← `make generate`（mockgen，源文件见 Makefile `MOCK_SOURCES`）

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
