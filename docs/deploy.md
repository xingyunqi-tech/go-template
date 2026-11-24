# 部署

## 镜像

`Dockerfile` 两阶段：builder 为 `golang:1.27.4-trixie`，以 `--platform=$BUILDPLATFORM` 交叉编译（`CGO_ENABLED=0`，BuildKit 缓存模块与构建缓存）；runtime 为 `gcr.io/distroless/static-debian13:nonroot`（按 digest 固定），只含 `/app` 一个文件，以 `nonroot`（uid 65532）运行，工作目录 `/home/nonroot`。迁移、种子、i18n、邮件模板、OpenAPI 文档都 embed 在二进制中。

```bash
make image        # docker buildx build --load -t go-template:local .
```

同一镜像通过子命令运行不同进程（`ENTRYPOINT ["/app"]`）：

| 进程 | 命令 |
|---|---|
| api | `serve`（默认 `CMD`） |
| worker | `worker`（River 队列与周期任务） |
| migrate | `migrate`（创建 `DATABASE_SCHEMA`，执行 core、identity（签发服务）与 River 迁移） |
| seed | `seed` |

镜像内没有 shell、curl、wget，健康检查用二进制自带的子命令：compose 中 api 服务的 healthcheck 为 `/app healthcheck`（存活），`deploy/manage.sh` 用 `docker compose exec -T api /app healthcheck --ready`（就绪）。

## Compose

文件在 `docker/`，从仓库根以 `-f` 组合：

| 场景 | 组合 | 项目名 | 入口 |
|---|---|---|---|
| 本地依赖 | `infra.compose.yaml` | `go-template-infra` | `make infra` |
| 开发服务栈 | base + redis + postgres + dev | `go-template-dev` | 见下 |
| 测试服务栈 | base + redis + postgres + test | `go-template-test` | 见下 |
| e2e / CI | base + postgres + redis + ci | `go-template-e2e`（`-p`） | `make test-e2e` |
| 服务器 | `docker-compose.ecs.yaml` | `go-template` | `deploy/manage.sh` |

| 文件 | 服务 / 作用 |
|---|---|
| `docker-compose.base.yaml` | `migrate`（一次性）、`api`（从源码构建 `go-template:local`，宿主端口 `APP_PORT`，默认 3000）、`worker`；配置取 `.env.example` |
| `docker-compose.postgres.yaml` | `postgres`（宿主端口 `DATABASE_PORT`，默认 5432） |
| `docker-compose.redis.yaml` | `redis`（宿主端口 `REDIS_PORT`，默认 6379），为 api / worker 设置 `REDIS_URL` |
| `docker-compose.dev.yaml` | 镜像 `go-template:dev`、`APP_ENV=development`、`SMTP_URL` 指向 Mailpit；`mailpit`（SMTP 宿主端口 `MAIL_PORT` 默认 1025，UI `MAIL_CLIENT_PORT` 默认 1080）；`seed` 在 migrate 之后写入演示账号；`develop.watch` 在 `cmd/`、`internal/`、`pkg/`、`migrations/`、`seeds/`、`templates/`、`openapi/`、`go.mod`、`go.sum` 变更时重新构建 api 与 worker |
| `docker-compose.test.yaml` | `APP_ENV=test`；`mailpit`（只映射 UI）；`seed`；postgres / redis 不映射宿主端口；供手工联调 |
| `docker-compose.ci.yaml` | 生产镜像以 `APP_ENV=production` 运行，固定测试签名密钥与 `AUTH_VERIFY_KEYS` 旧公钥、`TRUSTED_PROXIES=1`；`seed` 以 `APP_ENV=test` 写入演示账号；api 与 worker 共享文件卷；`migrate-verifier` + `api-verifier`（`AUTH_JWKS_URL` 指向 api、独立 schema `verifier`，宿主端口 `VERIFIER_PORT`，默认 3001）；端口只绑定 `127.0.0.1` |
| `docker-compose.ecs.yaml` | 服务器：migrate、seed（profile `seed`）、api、worker、postgres、redis；镜像 `IMAGE_FULL_NAME`，配置取部署目录 `.env` |

开发服务栈：

```bash
docker compose -f docker/docker-compose.base.yaml -f docker/docker-compose.redis.yaml \
  -f docker/docker-compose.postgres.yaml -f docker/docker-compose.dev.yaml up --build --watch
```

本机已有服务占用默认端口时，通过 `APP_PORT`、`DATABASE_PORT`、`REDIS_PORT`、`MAIL_PORT`、`MAIL_CLIENT_PORT` 改宿主端口。也可以只用 `make infra` 起依赖，再 `make dev` / `make worker` 在本机运行。

完整的黑盒测试用 `make test-e2e`（ci 组合，结束后删除栈与数据卷）。

## 服务器部署

### 目录布局

```
<部署目录>/
├── .env                          # 参考 docker/.env.ecs.example
├── .env.image                    # 可选：IMAGE_FULL_NAME=<镜像>，由 CI/CD 写入
├── deploy/manage.sh
└── docker/docker-compose.ecs.yaml
```

compose 文件写死项目名 `go-template`，所有调用都是 `docker compose --env-file .env [--env-file .env.image] -f docker/docker-compose.ecs.yaml …`，部署目录的 `.env` 同时用于变量替换（`APP_PORT`、`DATABASE_*`、`IMAGE_FULL_NAME`）和容器环境变量。

### 首次准备

1. 服务器安装 Docker 与 Compose 插件
2. 创建部署目录，放置 `.env`：`cp docker/.env.ecs.example .env` 后填写；首次部署设 `RUN_SEEDS=true`，写入后改回 `false`
3. 同步 `deploy/manage.sh` 与 `docker/docker-compose.ecs.yaml`，执行 `./deploy/manage.sh start`；之后由 CI/CD 同步并重启

`docker-compose.ecs.yaml` 固定以下变量，`.env` 中的值不生效：`APP_ENV=production`、容器内 `APP_PORT=3000`（宿主端口取 `.env` 的 `APP_PORT`）、`DATABASE_URL`（由 `DATABASE_USERNAME`、`DATABASE_PASSWORD`、`DATABASE_NAME` 组装，主机 `postgres`）、`REDIS_URL=redis://redis:6379/0`、`STORAGE_LOCAL_ROOT=/home/nonroot/files`。

生产启动时一次列出全部配置问题，满足以下要求才能启动 api / worker / migrate：

| 变量 | 要求 |
|---|---|
| `REDIS_URL`、`CORS_ORIGINS`、`DATABASE_URL` | 必填；`RATE_LIMIT_ENABLED` 必须为 `true` |
| `AUTH_SIGNING_KEY` 或 `AUTH_JWKS_URL` | 二选一；签发服务另需 `APP_FRONTEND_URL`、`SMTP_URL`、`SMS_PROVIDER=aliyun` 与 `ALIYUN_SMS_*` |
| `METRICS_TOKEN` 或 `METRICS_PORT` | `OTEL_METRICS_EXPORTER=prometheus` 时其一；`METRICS_TOKEN` 至少 16 字符 |
| `FILE_URL_SECRET` | `STORAGE_DRIVER=local` 时必填，至少 32 字符；`s3` 时 `S3_BUCKET`、`S3_REGION` 必填 |
| `WECHAT_*`、`ALIPAY_*` | 每个 provider 的凭据全设或全不设 |
| `SEED_ADMIN_EMAIL` / `SEED_ADMIN_PASSWORD` | `RUN_SEEDS=true` 且没有 active 管理员时创建管理员 |
| `TRUSTED_PROXIES` | 反向代理层数或 CIDR 列表 |

local 存储要求 api 与 worker 共享 `STORAGE_LOCAL_ROOT`（注销清理在 worker 执行）；多台服务器部署时改用 `STORAGE_DRIVER=s3`。

健康检查：`/health/live` 总是 200；`/health/ready` 检查 Postgres 与 Redis，不检查第三方与 JWKS，停机期间返回 503。worker 指标在 `WORKER_METRICS_PORT`（默认 9464）。

数据卷：`postgres-data`、`redis-data`、`uploaded-files`（挂载到 api 与 worker 的 `/home/nonroot`，local 驱动的文件在其中的 `files/`）。

### deploy/manage.sh

| 命令 | 作用 |
|---|---|
| `start` | `compose run --rm migrate`；`.env` 中 `RUN_SEEDS=true` 时再 `compose run --rm seed`；然后 `up -d --remove-orphans`，用 `docker compose exec -T api /app healthcheck --ready` 轮询就绪（30 次 × 5s），失败时输出 api / worker 最近日志并退出 1 |
| `stop` | `compose stop` |
| `restart` | `compose restart`，3 秒后检查 `go-api` 容器在运行 |
| `remove` / `rm` | `compose --profile seed down -v --remove-orphans`（删除数据卷） |
| `status` / `ps` | `compose ps` |
| `logs` | `compose logs -f` |
| `health` | `go-api` 运行且 `/app healthcheck --ready` 成功时退出 0 |
| `cleanup <镜像>` | 按创建时间保留该镜像最新 3 个 |
| `help` | 帮助 |

容器名：`go-api`、`go-worker`、`go-migrate`、`go-seed`、`go-postgres`、`go-redis`。

### CI/CD 约定

deploy workflow 向部署目录同步 `docker/docker-compose.ecs.yaml` 与 `deploy/manage.sh`，把本次镜像写入 `.env.image`（`IMAGE_FULL_NAME=…`），SSH 执行拉取镜像（3 次重试）→ migrate → `up -d --remove-orphans` → `deploy/manage.sh health` 轮询就绪 30 次 → 删除该仓库最新 3 个之外的旧镜像。

| workflow | 触发 | 内容 |
|---|---|---|
| `check.yml` | push / PR 到 main、master、`release/**`，手动 | lint、workflow-lint、type-check、test、migrations、contract、coverage、security、build、image-scan、e2e，每个 job 调用同名 Makefile 目标 |
| `deploy.yml` | 手动（输入 `deploy`、`target`：aliyun / tencent / all） | 多架构镜像推送到 GHCR 与腾讯云 TCR 个人版，服务器从 TCR 拉取并部署；成功后调用 `cleanup-registry.yml` |
| `cleanup-registry.yml` | 每周一 03:00（北京时间）、手动（`dry_run`）、被 deploy 调用 | GHCR 保留 latest 与最近 2 个带 tag 的镜像并删除无 tag 镜像；TCR 保留 2 个 |

仓库需要配置：

| 类型 | 名称 | 用途 |
|---|---|---|
| Secret | `<PREFIX>_HOST`、`<PREFIX>_PORT`（默认 22）、`<PREFIX>_USER`、`<PREFIX>_SSH_KEY`、`<PREFIX>_PATH`（`<PREFIX>` 为 `ALIYUN` 或 `TENCENT`） | SSH 部署目标与部署目录 |
| Secret | `TENCENT_TCR_PASSWORD` | 推送与拉取 TCR 镜像 |
| Variable | `TENCENT_TCR_REGISTRY`、`TENCENT_TCR_NAMESPACE`、`TENCENT_TCR_USERNAME` | TCR 仓库 `<命名空间>/go-template` |
| Secret | `TENCENTCLOUD_SECRET_ID`、`TENCENTCLOUD_SECRET_KEY`；Variable `TENCENT_TCR_REGION`（默认 ap-guangzhou） | 清理 TCR 旧镜像，未配置时跳过 |
| Environment | `aliyun-production`、`tencent-production` | 部署 job 的环境 |
