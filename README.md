# go-template

Go 后端模板：对外行为遵守 [跨语言平台约定](docs/platform-conventions.md)，导出自身的 OpenAPI 3.1 文档（`/openapi.json`）供前端生成客户端。只提供基础能力，外加一个 CRUD 示例模块 `notes`，业务在此之上添加。可作为同一 monorepo 中的一个服务，与 Python / Java 模板共存。

## 功能

| 类别 | 内容 |
|---|---|
| 平台 | 错误信封与 errorKey 码表、字段级校验错误、i18n（zh / en）、Request ID、CORS、安全头、请求体限制、GCRA 限流、幂等键、健康检查、Prometheus 或 OTLP 指标、OTel 链路、Sentry、`/docs`（Scalar） |
| 认证（签发服务） | 邮箱 + 密码、手机号 + 短信验证码、微信小程序 / 微信 App / 支付宝登录（配置凭据后启用）、EdDSA JWT + JWKS、refresh 轮换与 30 秒宽限期、会话管理、改密与找回、当前用户、用户与角色管理、账号注销 |
| 验证服务 | 设 `AUTH_JWKS_URL` 只验证签发服务的令牌，共享吊销键；订阅 `user.deleted` 清理本服务数据（事件传输需在 monorepo 中接入） |
| 文件 | 本地或 S3 兼容存储，按内容识别类型，签名地址 |
| 基础服务 | 邮件（SMTP）、River 队列与周期任务、审计日志与查询 |
| 工具 | `make module NAME=<name>` 以 notes 为模板生成 CRUD 模块 |

## 快速开始

需要 Go（版本见 `go.mod`）与 Docker。

```bash
make install            # 工具二进制与 Git 钩子
cp .env.example .env
make keys               # 生成 AUTH_SIGNING_KEY，填入 .env（不设时非生产以临时密钥运行）
make infra              # 本地 Postgres、Redis（端口 DATABASE_PORT / REDIS_PORT）
make migrate && make seed
make dev                # API：http://localhost:3000，文档 /docs
make worker             # 另开终端：队列 worker 与周期任务
```

种子账号（非生产）：`admin@example.com` 与 `user@example.com`，密码 `Passw0rd!`。生产用 `SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD` 或 `go run ./cmd/app createsuperuser --email <email>`。

认证角色由变量决定：设 `AUTH_SIGNING_KEY` 为签发服务（挂载约定 §11.1–§11.3 与 JWKS，迁移 identity 表）；只设 `AUTH_JWKS_URL` 为验证服务；两者都设时启动失败。

## Make 目标

`make help` 列出全部目标。常用：

| 目标 | 作用 |
|---|---|
| `make check` | lint + vet + 单元测试（pre-push 钩子） |
| `make test-integration` / `test-contract` / `test-e2e` / `test-migrations` | 集成（testcontainers）/ 契约 / e2e（compose 生产镜像）/ 迁移检查 |
| `make coverage` | 单元 + 集成 + 契约覆盖率，门槛 80% |
| `make generate` | OpenAPI bundle 与 oapi-codegen、sqlc、mockgen |
| `make module NAME=<name> [PLURAL=<plural>] [PERMS=1]` | 生成 CRUD 模块 |
| `make ci` | 本地运行 CI 的全部 job |

## 目录结构

```
cmd/app/            子命令：serve / worker / migrate / seed / createsuperuser / keys / healthcheck / version
cmd/modulegen/      模块生成器
internal/app/       组装与生命周期，按认证角色装配
internal/controller/http/   中间件链、操作表、policy；gen/ 生成代码，v1/ 处理器
internal/usecase/   业务规则（auth、sms、account、session、user、file、audit、maintenance、note、health）
internal/repo/      端口与实现：postgres/、redis/、webapi/{mail,sms,wechat,alipay,storage}
internal/entity/    领域对象、错误码表、角色与权限码
internal/events/    user.deleted 事件
internal/worker/    River workers 与周期任务
migrations/{core,identity}/   goose 迁移
openapi/src/        接口定义源文件（spec-first）
templates/          邮件模板、模块模板
tests/{contract,e2e}/
scripts/bench/      压测
```

## 文档

| 文件 | 内容 |
|---|---|
| [docs/platform-conventions.md](docs/platform-conventions.md) | 跨语言平台约定（三仓库相同，不在本仓库修改） |
| [docs/architecture.md](docs/architecture.md) | 分层、请求链路、进程模型、认证角色、事件、Connect 接入 |
| [docs/openapi.md](docs/openapi.md) | spec 源文件、bundler 与生成流程、新增接口步骤 |
| [docs/auth.md](docs/auth.md) | 令牌实现、轮换与宽限期、登录方式与 provider 变量 |
| [docs/database.md](docs/database.md) | schema、两个迁移目录、命名规则、事务 |
| [docs/cli.md](docs/cli.md) | 子命令、`keys generate`、模块生成器 |
| [docs/testing.md](docs/testing.md) | 各层测试与运行方式 |
| [docs/deploy.md](docs/deploy.md) | 镜像、compose、健康检查、环境变量、服务器部署 |
| [docs/benchmark.md](docs/benchmark.md) | 压测方法与结果 |
| [TEMPLATE_PLAN.md](TEMPLATE_PLAN.md) | v2 方案与实施批次 |

## 许可证

见 [LICENSE](LICENSE)。
