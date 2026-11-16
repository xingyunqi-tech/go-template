# go-template

Go 后端模板。HTTP API 契约与 [nestjs-template](https://github.com/xingyunqi-tech/nestjs-template)（`release/exam-prep` 分支 `02fdb4d`，见 `openapi/SOURCE`）一致：vite-vue3-temp、uniapp-template 由 `/docs-json` 经 Orval 生成的前端可在两个后端之间无改动切换。改造方案见 [TEMPLATE_PLAN.md](TEMPLATE_PLAN.md)，开发约定见 [CLAUDE.md](CLAUDE.md)。

| 项 | 选型 |
|---|---|
| 语言 | Go 1.27（`go.mod`） |
| Web | 标准库 `net/http` + chi；spec-first：oapi-codegen strict server，输入为 nestjs 导出的 OpenAPI 文档 |
| 数据 | PostgreSQL 18、pgx v5、sqlc、goose |
| 缓存 / 锁 / 限流 | Redis 8、go-redis（GCRA 限流，Redis 不可用时回落进程内） |
| 队列 / 定时任务 | River（Postgres） |
| 认证 | golang-jwt 双令牌 + 会话、argon2id / bcrypt |
| 可观测性 | log/slog、Prometheus client_golang、OpenTelemetry、Sentry |
| 分层 | `cmd/` + `internal/{entity,usecase,repo,controller}` + `pkg/`（源自 evrone/go-clean-template） |
| 质量 | golangci-lint v2、typos、testify + testcontainers-go、prek 钩子 |

## 功能

- 统一响应信封、错误翻译（i18n en / zh）、与 nestjs ValidationPipe 等价的校验、Request ID、CORS、安全头、限流，Express 路由语义
- 认证：邮箱、手机号密码、短信验证码、微信（小程序 / App / 手机号）、支付宝；JWT access + refresh 轮换；会话（设备）管理；RBAC
- 用户管理、文件上传（`local` / `s3` / `s3-presigned`）与私有文件
- 通用模块：config、regions、notifications、addresses、favorites、feedbacks、consents、app-profile
- 健康检查、Prometheus 指标、审计、运营告警、账号注销清理、邮件（中英模板）、定时任务
- `/docs-json` 逐字节输出 nestjs 完整 OpenAPI 基线（含本模板未实现的考试业务接口，请求返回 404 信封），`/docs` 为 Scalar 页面；`SWAGGER_ENABLED` 控制，未设置时生产关闭
- 模块骨架生成器：按 nestjs 文档中的 tag 生成处理器、usecase、repo

## 快速开始

前置：Go 1.27、Docker。

```bash
make install      # 下载 golangci-lint / prek / typos / sqlc 到 bin/，安装 Git 钩子
cp .env.example .env
make infra        # Postgres 18 :5432、Redis 8 :6379
make migrate      # goose 迁移 + River 迁移
make seed         # 角色、状态、权限、行政区划、配置与初始账号
make dev          # http://localhost:3000
make worker       # 另开终端：队列 worker 与周期任务（邮件、通知、账号清理）
```

- `.env.example` 中 `REDIS_ENABLED=false`，缓存、锁、限流使用进程内实现；改为 `true` 后连接 `make infra` 启动的 Redis。队列在 Postgres 上，邮件、通知、账号清理始终需要 worker 进程；生产强制 `REDIS_ENABLED=true`
- 未设置 `MAIL_HOST` 时（非生产）邮件只写 worker 日志；`SMS_MOCK_MODE=true` 时验证码只写 API 日志
- 非生产环境 seed 账号：`admin@example.com` / `john.doe@example.com`，密码 `Secret00`（接口传 MD5）

| 路由 | 说明 |
|---|---|
| `GET /` | 应用信息 |
| `GET /api/health` | 简单健康检查 |
| `GET /api/v1/health`、`/api/v1/health/live`、`/api/v1/health/ready` | terminus 形状的健康检查与探针 |
| `/api/v1/auth/*` | 邮箱 / 手机 / 微信 / 支付宝登录注册、刷新令牌、个人资料、改密、注销、会话管理 |
| `/api/v1/users` | 用户管理（管理员） |
| `GET /docs-json`、`GET /docs` | OpenAPI 文档（与 nestjs 逐字节相同）、Scalar 页面 |
| `GET /metrics` | Prometheus 指标（监控令牌） |

## Make 目标

| 目标 | 说明 |
|---|---|
| `make install` | 下载工具二进制并安装 Git 钩子（`make tools` 只下载） |
| `make infra` | 启动本地 Postgres 与 Redis |
| `make migrate` / `make seed` | 迁移 / 种子数据（读取 `.env`） |
| `make dev` / `make worker` | 本地启动 API / 队列 worker（读取 `.env`） |
| `make generate` | oapi-codegen（`openapi/scope.yaml` 范围内）+ sqlc |
| `make generate-check` | 重新生成后检查生成代码与入库版本一致 |
| `make module NAME=orders TAG=Orders` | 按 nestjs 文档 tag 生成模块骨架并加入 `scope.yaml` |
| `make openapi-sync` | 从 fastapi-temp 复制共享产物；`OPENAPI_SYNC=run` 时在 nestjs 上重新导出 |
| `make fmt` | gofumpt + gci |
| `make lint` | golangci-lint（主模块与 tools 模块）、typos、`go mod tidy -diff`（`make lint-new` 只报告相对 HEAD 新增的问题，`make typos` 只做拼写检查） |
| `make vet` | `go vet`（含全部测试标签）+ `go build` |
| `make check` | lint + vet + 单元测试（pre-push 钩子） |
| `make test` | 第 1 层：单元测试 |
| `make test-integration` | 第 2 层：集成测试（testcontainers，需要 Docker） |
| `make test-e2e` | 第 3 层：构建镜像，compose 启动完整服务栈后黑盒测试 |
| `make test-contract` | 第 4 层：契约测试（含全部 nestjs 校验 oracle 用例） |
| `make test-parity` | 第 5 层：启动 nestjs 参照服务与本服务后逐请求比对（需要 Docker 与 nestjs-template 本地仓库） |
| `make test-migrations` | 空库 up → sqlc vet → down-to 0 → up，检查约束与索引命名（需要 `make infra`） |
| `make coverage` | 合并单元、集成、契约覆盖率并校验 ≥80% |
| `make vuln` / `make gitleaks` / `make security` | govulncheck / gitleaks / 两者 |
| `make hadolint` / `make workflow-lint` | Dockerfile 检查 / actionlint + zizmor |
| `make build` | 交叉编译 linux amd64 / arm64 到 `bin/` |
| `make image` / `make image-scan` | 构建生产镜像 `go-template:local` / Trivy 扫描 |
| `make ci` | 本地运行 CI 中除 parity 外的全部检查 |
| `make clean` | 清理 `bin/` 与 `coverage/` |

`make help` 列出全部目标与说明。

## 目录结构

```
cmd/app/            单一二进制：serve、worker、migrate、seed、healthcheck、version
cmd/modulegen/      模块骨架生成器
internal/
  app/              组装与进程生命周期
  config/           环境变量解析与校验
  entity/           领域对象与领域错误
  usecase/          业务流程（接口在 contracts.go）
  repo/             postgres（sqlc）、redis、webapi（邮件、存储、短信、微信、支付宝）
  controller/http/  中间件、Express 路由语义、请求管线、policy.go；gen/ 为生成代码，v1/ 为处理器
  worker/           River workers 与周期任务
  metrics/、i18n/、testutil/
pkg/                与业务无关的封装（validatorx、ratelimit、password、jwtx、jsontime……）
openapi/            nestjs 基线、validation.json、scope.yaml、overlay.yaml
migrations/、seeds/、templates/、db/
tests/              contract、parity、e2e
docker/、deploy/、scripts/、tools/
```

详见 [docs/architecture.md](docs/architecture.md)。

## 镜像与 Compose

`Dockerfile` 多阶段构建，运行时为 `gcr.io/distroless/static-debian13:nonroot`，镜像只含一个静态二进制 `/app`（迁移、种子数据、邮件模板、OpenAPI 文档都已 `embed`）。`docker/` 下的 compose 文件按组合使用：`docker-compose.base.yaml`（migrate、api、worker）叠加 `docker-compose.postgres.yaml`、`docker-compose.redis.yaml`，再叠加 `dev` / `test` / `ci` / `parity` 之一；服务器部署用独立的 `docker-compose.ecs.yaml`；`infra.compose.yaml` 只启动本地依赖（`make infra`）。详见 [docs/deploy.md](docs/deploy.md)。

## 文档

| 文档 | 内容 |
|---|---|
| [docs/architecture.md](docs/architecture.md) | 目录结构、分层、policy、请求链路、进程模型、基础设施 |
| [docs/auth.md](docs/auth.md) | 登录方式、JWT 与会话、密码、短信验证码、RBAC |
| [docs/database.md](docs/database.md) | 连接配置、模型与迁移、事务、共库、seed |
| [docs/deploy.md](docs/deploy.md) | 镜像、compose、deploy/manage.sh、GitHub Actions |
| [docs/testing.md](docs/testing.md) | 五层测试与运行方式 |
| [docs/cli.md](docs/cli.md) | 子命令、worker、代码生成与模块生成器、脚本 |
| [docs/contract.md](docs/contract.md) | 契约同步流程、spec-first 生成、parity、未实现接口、与 nestjs 的行为差异 |
| [docs/benchmark.md](docs/benchmark.md) | 与 nestjs 的压测对照 |
| [CLAUDE.md](CLAUDE.md) | 开发约定（AI 助手同样遵守） |

## 许可证

[MIT](LICENSE)
