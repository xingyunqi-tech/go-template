# 压测（k6）

登录、刷新、`auth/me`、用户列表四个接口在同一台机器、同样资源限制下对比 go-template 与 nestjs-template（`02fdb4d`）。测试日期 2026-11-16。

## 环境

| 项 | 值 |
|---|---|
| 机器 | MacBook Pro（MacBookPro18,2），Apple M1 Max，10 核，32 GB |
| Docker | Docker Desktop，Engine 28.5.1，VM 分配 10 CPU、7.75 GiB 内存 |
| 压测工具 | `grafana/k6:2.3.0`，容器接入被测栈的 compose 网络，直连 `http://api:3000` |
| api 容器限制 | 两边都是 `cpus: 2`、`mem_limit: 1g`；Postgres、Redis 等不限 |

| | go-template | nestjs-template |
|---|---|---|
| 镜像 | `go-template:bench`（本仓库 `Dockerfile`，distroless static） | `fastapi-temp/nestjs-ref:02fdb4def2df`（nestjs `Dockerfile` 的 `production` target） |
| 运行时 | Go 1.27.4，net/http + chi v5.5.0，pgx v5.13.0，River v0.51.2 | Node 24.22.2，NestJS（Express），TypeORM + pg |
| 并发模型 | 单进程，goroutine 调度到容器的 2 个 CPU；`GOMEMLIMIT=800MiB` | 单个 Node 进程（未启用 cluster），一个事件循环；bcryptjs 在主线程上分段执行 |
| 密码哈希 | argon2id（`PASSWORD_HASHER=argon2id`）：`t=2`、`m=19456`（19 MiB）、`p=1` | bcryptjs，10 轮 |
| 数据库连接池 | `DATABASE_MAX_CONNECTIONS=80`（pgxpool） | `DATABASE_MAX_CONNECTIONS=100` |
| Postgres / Redis | `postgres:18.6-alpine`、`redis:8.10.3-alpine`（项目 `bench-go`） | `postgres:17.11-alpine`、`redis:7.4.12-alpine`（项目 `bench-nestjs`） |
| 模式 | `APP_ENV=production`（ci overlay） | `NODE_ENV=production` |
| 日志 | info 级 JSON，每个请求一条访问日志 | info 级 JSON（pino），每个请求一条访问日志 |
| 限流 | `THROTTLE_ENABLED=false` | `THROTTLE_ENABLED=false` |
| 其他进程 | 同栈另有 worker（不限资源） | 同栈另有 mailpit |

go 栈由 `docker/docker-compose.{base,postgres,redis,ci}.yaml` 加一个 override 组成，override 只替换镜像、关闭限流、日志级别设为 info，并给 api 加资源限制与 `GOMEMLIMIT`；migrate 跑迁移，seed 写入演示账号。nestjs 栈由 `scripts/nestjs/nestjs.compose.yaml` 加 override 起，env 来自 nestjs `02fdb4d` 的 `env-example-relational`，替换项为 `THROTTLE_ENABLED=false`、各 `*_SECRET` 补足 32 位（生产模式启动校验要求），override 设 `NODE_ENV=production`、`LOG_LEVEL=info`、`LOG_PRETTY_PRINT=false`。两边配置与 fastapi-temp `docs/benchmark.md` 相同，k6 脚本 `scripts/bench/auth.js` 与 fastapi-temp 逐字节相同。

## 方法

- 接口与账号：
  - `POST /api/v1/auth/email/login`：`john.doe@example.com`，密码字段为 `md5("Secret00")`。
  - `POST /api/v1/auth/refresh`：setup 阶段为每个 VU 登录一次拿到独立会话，每次请求带上一次返回的新 refreshToken。
  - `GET /api/v1/auth/me`：`john.doe@example.com` 的 access token。
  - `GET /api/v1/users?limit=10`：`admin@example.com` 的 access token。
- 每个接口单独跑一次 k6：`constant-vus`，总时长 40 s，前 10 s 为预热，只统计之后 30 s 内发出的请求（按 `phase` 标签过滤）；VU 无思考时间。
- VU 分 10、50、100 三档。按档位、接口顺序执行，每组内两个后端交替先后（第一组 go 先，第二组 nestjs 先，依此类推）。
- 指标：RPS = 统计窗口内完成请求数 / 30 s；p50、p95、p99 为 `http_req_duration`；错误率为 `http_req_failed`（非 2xx/3xx 或网络错误）。

## 结果

| 接口 | VU | 后端 | 请求数 | RPS | p50 (ms) | p95 (ms) | p99 (ms) | 错误率 |
| login | 10 | go | 2001 | 66.7 | 143 | 195.8 | 338.9 | 0% |
| login | 10 | nestjs | 396 | 13.2 | 743.1 | 972 | 1103.2 | 0% |
| login | 50 | go | 2138 | 71.3 | 699.3 | 750.2 | 776.6 | 0% |
| login | 50 | nestjs | 396 | 13.2 | 3625.7 | 3708 | 4909.1 | 0% |
| login | 100 | go | 2023 | 67.4 | 1471 | 1582.4 | 1606.9 | 0% |
| login | 100 | nestjs | 368 | 12.3 | 5083.5 | 7824.3 | 14513.3 | 0% |
| refresh | 10 | go | 139980 | 4666 | 1.9 | 3.6 | 5.6 | 0% |
| refresh | 10 | nestjs | 20614 | 687.1 | 13.8 | 22.6 | 32.8 | 0% |
| refresh | 50 | go | 181724 | 6057.5 | 7.7 | 14 | 21.8 | 0% |
| refresh | 50 | nestjs | 24617 | 820.6 | 63.4 | 97.2 | 149.2 | 0% |
| refresh | 100 | go | 191654 | 6388.5 | 15.7 | 28.2 | 47.5 | 0% |
| refresh | 100 | nestjs | 19069 | 635.6 | 142.7 | 473.5 | 1004 | 0% |
| me | 10 | go | 259093 | 8636.4 | 0.9 | 2.3 | 3.8 | 0% |
| me | 10 | nestjs | 36498 | 1216.6 | 7.3 | 11.6 | 22.8 | 0% |
| me | 50 | go | 317242 | 10574.7 | 4.1 | 8.7 | 11.7 | 0% |
| me | 50 | nestjs | 43159 | 1438.6 | 32.3 | 48.8 | 70.3 | 0% |
| me | 100 | go | 326371 | 10879 | 8.4 | 15.1 | 20 | 0% |
| me | 100 | nestjs | 38630 | 1287.7 | 68.8 | 135.8 | 222.9 | 0% |
| users | 10 | go | 280305 | 9343.5 | 0.9 | 2 | 2.8 | 0% |
| users | 10 | nestjs | 35216 | 1173.9 | 7.8 | 11.6 | 18.2 | 0% |
| users | 50 | go | 290516 | 9683.9 | 4.7 | 9.5 | 12 | 0% |
| users | 50 | nestjs | 30458 | 1015.3 | 36.6 | 97.1 | 281 | 0% |
| users | 100 | go | 222954 | 7431.8 | 10.9 | 24.1 | 52.7 | 0% |
| users | 100 | nestjs | 31946 | 1064.9 | 82.3 | 159.8 | 241.8 | 0% |

### 错误与资源

- 全部场景错误率为 0，k6 的状态码检查全部通过。
- 跑完全部档位后，api 容器内存：go 约 56 MiB，nestjs 约 404 MiB（`docker stats`，上限 1 GiB）。

## 结论

- 登录：两边都受密码哈希 CPU 成本主导，算法不同（argon2id `t=2,m=19MiB,p=1` 对 bcrypt 10 轮）。go 67–71 RPS，nestjs 12.3–13.2 RPS；100 VU 时 p99 分别为 1.6 s、14.5 s。
- 刷新：go 4666–6389 RPS，nestjs 636–821 RPS。
- `auth/me`：go 8636–10879 RPS，nestjs 1217–1439 RPS。
- 用户列表：go 7432–9684 RPS，nestjs 1015–1174 RPS。
- 除登录外，go 吞吐约为 nestjs 的 6.8–10 倍，p50 约为 nestjs 的 1/7–1/9。

## 复现

前置：本地有 nestjs-template 仓库（默认 `../nestjs-template`，可用 `NESTJS_DIR` 指定，只读取 `openapi/SOURCE` 中提交的 `env-example-relational`）和 `fastapi-temp/nestjs-ref:02fdb4def2df` 镜像（`make test-parity` 或 `OPENAPI_SYNC=run make openapi-sync` 会构建），并装有 `jq`。

```bash
make image && docker tag go-template:local go-template:bench
docker pull grafana/k6:2.3.0

export WORK_DIR="$(mktemp -d)"
scripts/bench/run.sh up
VU_LEVELS="10 50 100" scripts/bench/run.sh bench
scripts/bench/run.sh down
```

- `up` 生成两份 override 和 nestjs env 到 `WORK_DIR`，以项目名 `bench-go`、`bench-nestjs` 起栈，宿主端口均为 `127.0.0.1` 随机端口。
- `bench` 依次跑完所有组合，每次结果写入 `$WORK_DIR/results/<后端>-<接口>-<VU>.json`，最后输出上面的 Markdown 表；`report` 可单独重新生成表格。
- `down` 以 `down --volumes --remove-orphans` 删除两个栈。
- 变量：`API_CPUS`（默认 2）、`API_MEM`（默认 1g）、`GO_MEMLIMIT`（默认 800MiB）、`ENDPOINTS`、`K6_IMAGE`。
