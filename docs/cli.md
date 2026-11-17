# CLI

`cmd/app` 编译为单一二进制（镜像中为 `/app`，`ENTRYPOINT ["/app"]`、默认 `CMD ["serve"]`），子命令用标准库 `flag` 分派，不引入 cobra。本地用 `go run ./cmd/app <命令>` 或对应的 Make 目标调用。

```bash
go run ./cmd/app            # 打印用法
```

| 命令 | 说明 | Make |
|---|---|---|
| `app serve` | 启动 HTTP API（`APP_PORT`，默认 3000）；`DB_SCHEMA_OWNER=external` 时先检查表结构 | `make dev` |
| `app worker` | 启动 River workers 与周期任务；`WORKER_METRICS_PORT`（默认 9464，0 关闭）上提供 `/<METRICS_PATH>`，与 API 用同一 `METRICS_TOKEN` 校验 | `make worker` |
| `app migrate` | goose 迁移 + River 迁移；`DB_SCHEMA_OWNER=external` 时只检查表结构并迁移 `river` schema | `make migrate` |
| `app seed` | 写入角色、状态、权限、行政区划、配置与初始账号，已存在的不覆盖，见 [database.md](database.md#seed)；完成后删除角色权限缓存（失败只告警，缓存最长 5 分钟后过期） | `make seed` |
| `app permissions clear-cache` | 删除角色权限缓存 `permissions:v2:role-map`（Redis），Redis 不可达时退出码 1；`REDIS_ENABLED=false` 时各进程在内存中缓存，最长 5 分钟后失效，命令只输出提示 | – |
| `app createsuperuser -email <email>` | 新建一个状态为启用的管理员（role 1），邮箱按登录接口相同的 `isEmail` 规则校验（需有顶级域名），密码取标准输入第一行（至少 6 个字符，按客户端做法先取 MD5 再哈希）；邮箱已存在时退出码 1。例：`printf '%s\n' "$PASSWORD" \| docker compose exec -T api /app createsuperuser -email root@example.com` | – |
| `app healthcheck [--ready]` | 请求本机 `http://127.0.0.1:<APP_PORT>/<API_PREFIX>/v1/health/live`（`--ready` 时为 `health/ready`），200 时退出码 0，否则 1；compose 的 api 健康检查与 `deploy/manage.sh health` 使用它 | – |
| `app version` | 打印构建版本（`-ldflags "-X main.version=..."`，`make image` 取 `git describe`） | – |

- 每个命令启动时执行 `config.Load()`，配置有误时把全部问题打印到 stderr 并以退出码 1 结束；未知命令打印用法，退出码 2
- 进程只读环境变量；Make 目标在调用前加载工作目录的 `.env`（`set -a; . ./.env`）
- 收到 SIGINT / SIGTERM 后按 [architecture.md](architecture.md#进程模型) 的顺序优雅停机，`SHUTDOWN_TIMEOUT`（默认 30s）为上限

## worker

| 队列 | 任务（River kind） | 并发 | 尝试次数 | 说明 |
|---|---|---|---|---|
| `mail` | `send-mail` | 1 | 3 | 注册确认、确认新邮箱、找回密码；按任务里记录的请求语言渲染 |
| `notification` | `send-email`、`send-sms`、`send-wechat-subscribe` | 5 | 3 | 每个 worker 进程每秒最多 10 个任务；结果写入通知的 `sentChannels` |
| `account-cleanup` | `clean-account` | 1 | 5 | 按 order 执行各模块清理器；同一用户持 `lock:account-cleanup:user-{id}`，锁被占用时 30 秒后重试 |
| `scheduler` | 周期任务，见下 | 1 | 1 | |

失败后的等待为 `1s × 2^(n-1)`（`clean-account` 为 `10s × 2^(n-1)`），每次尝试结束计 `queue_jobs_total{queue_name, status}`。

| 周期任务 | 时间 | 内容 |
|---|---|---|
| `cleanup-old-sessions` | 每天 03:00 UTC | 物理删除软删超过 7 天的会话 |
| `cleanup-read-notifications` | 每天 04:00 UTC | 软删除 `readAt` 早于 30 天的已读通知 |
| `account-cleanup-compensation` | 每小时第 25 分（Asia/Shanghai） | 已软删超过 30 分钟且没有 `account_cleanup` 记录的用户（按 id 升序，最多 200 个）重新入队 |

多个 worker 实例同时运行时，River leader 选举保证每个周期任务只投递一次。

## 代码生成

| 命令 | 输入 | 输出 |
|---|---|---|
| `make generate` | `openapi/nestjs-baseline.json` + `openapi/overlay.yaml` + `openapi/oapi-codegen.yaml`，范围取 `openapi/scope.yaml` | `internal/controller/http/gen/gen.go`（`tools/openapigen` 调用 oapi-codegen 库） |
| | `migrations/` + `internal/repo/postgres/queries/*.sql`（`sqlc.yaml`） | `internal/repo/postgres/sqlcgen/` |
| | Makefile `MOCK_SOURCES` 中声明端口的源文件（mockgen，`-typed`） | `internal/repo/mocks/`、`internal/usecase/<pkg>/mocks/` |
| `make migrate-create NAME=<name>` | – | `migrations/000NN_<name>.sql`（goose `-s` 顺序编号，含 Up / Down 骨架） |
| `make generate-check` | 同上 | 重新生成后检查生成代码与入库版本无差异 |

生成文件不手改。新增端口时把其所在源文件加入 Makefile `MOCK_SOURCES`。pre-commit 钩子在 `openapi/`、`tools/openapigen/`、`migrations/`、sqlc 查询、`Makefile` 或 mock 源文件有暂存改动时重新生成并检查无差异（`scripts/check-generated.sh`）。

## 模块生成器

`cmd/modulegen` 按 nestjs 文档中某个 tag 的全部接口生成模块骨架：

```bash
make module NAME=orders TAG=Orders
```

| 参数 | 说明 |
|---|---|
| `NAME`（`-name`） | Go 包名，小写字母开头，只含小写字母与数字 |
| `TAG`（`-tag`） | `openapi/nestjs-baseline.json` 中的 tag，须至少有一个接口 |

生成内容（模板在 `templates/module/`），目标文件已存在时报错退出、不覆盖：

| 文件 | 内容 |
|---|---|
| `internal/controller/http/v1/<NAME>.go` | 该 tag 每个 operation 的处理器桩，返回 501 |
| `internal/usecase/<NAME>/<NAME>.go` | `Service` 与构造函数 |
| `internal/repo/postgres/<NAME>.go` | 仓储实现骨架 |
| `internal/repo/contracts.go` | 追加仓储端口接口 |
| `openapi/scope.yaml` | 追加尚未列出的 operationId |

命令结束时打印后续步骤：

1. `make generate`
2. 在 `internal/controller/http/policy.go` 为每个 operation 声明 policy（缺失时启动失败）
3. 在 `internal/repo/postgres/queries/<NAME>.sql` 编写查询，并在 `internal/app` 组装 service

接口只能来自 nestjs 文档：契约测试要求每个注册的路由都在 `openapi/nestjs-baseline.json` 中，契约变更先在 nestjs 落地，再经 `make openapi-sync` 同步，见 [contract.md](contract.md)。

## 脚本

| 脚本 | 用途 |
|---|---|
| `scripts/copy-shared-artifacts.sh`（`make openapi-sync`） | 从 `FASTAPI_DIR`（默认 `../fastapi-temp`）复制共享产物；对方工作区中这些文件有未提交改动时退出 |
| `scripts/openapi-sync.sh`（`make openapi-sync OPENAPI_SYNC=run`） | 共享产物：在 nestjs-template 固定提交上启动服务并导出基线、`validation.json`、`nest_handlers.json`、`SOURCE`、表结构快照与校验用例；需要 Docker、git、uv |
| `scripts/schema_snapshot.py`、`scripts/gen_validation_cases.py`、`scripts/gen_nest_handlers.py`、`scripts/nestjs/` | 共享产物：`openapi-sync.sh` 调用的生成工具 |
| `scripts/parity.sh`（`make test-parity`） | 启动 nestjs 参照服务与本服务并运行 parity，见 [testing.md](testing.md#parity) |
| `scripts/check-generated.sh` | pre-commit：生成代码新鲜度 |
| `scripts/check-comment-history.sh` | pre-commit：新增注释与 `t.Run` 名中的变更历史句式告警（不阻断） |
| `scripts/commit-msg.sh` | commit-msg：conventional commits，类型集合同 nestjs commitlint，标题 ≤72 字符 |
| `scripts/bench/run.sh` | 与 nestjs 的压测对照（`up` / `bench` / `report` / `down`），结果见 [benchmark.md](benchmark.md) |

`openapi-sync.sh` 变量：

| 变量 | 默认 |
|---|---|
| `NESTJS_DIR` | `../nestjs-template` |
| `NESTJS_REF` | `openapi/SOURCE` 的 `commit` |
| `NESTJS_BRANCH` | `release/exam-prep` |
| `NESTJS_SCRIPT_REF` | `openapi/SOURCE` 的 `validation_script`，否则 `NESTJS_BRANCH` |
| `NESTJS_PORT` | `3001`（被占用时退出） |
| `NESTJS_SYNC_PROJECT` | `nestjs-openapi-sync-<仓库目录名>` |
