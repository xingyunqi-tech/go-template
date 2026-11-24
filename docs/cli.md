# CLI

镜像只含一个二进制 `/app`，本地用 `go run ./cmd/app <command>`。

| 子命令 | 作用 |
|---|---|
| `serve` | HTTP API |
| `worker` | River 队列 worker 与周期任务；`prometheus` 模式下在 `WORKER_METRICS_PORT` 提供 `/metrics` |
| `migrate [up\|down]` | 创建 `DATABASE_SCHEMA` 并执行迁移，或全部回滚 |
| `seed` | 写入种子账号（见 [database.md](database.md#seed)） |
| `createsuperuser --email <email>` | 创建管理员，或把已有账号设为管理员（替换密码、启用、标记邮箱已验证、吊销全部会话）；密码从标准输入首行读取 |
| `keys generate` | 打印新的 Ed25519 私钥 PEM（`AUTH_SIGNING_KEY`）、公钥 PEM（`AUTH_VERIFY_KEYS`）与 `kid` |
| `healthcheck [--ready]` | 请求本机 `/health/live`（或 `/health/ready`），200 时退出码 0；用于容器健康检查 |
| `version` | 打印构建版本 |

```bash
printf '%s\n' 'S3cret-passw0rd' | go run ./cmd/app createsuperuser --email root@example.com
go run ./cmd/app keys generate
```

## 代码生成

| 命令 | 输入 → 输出 |
|---|---|
| `make openapi` | `openapi/src/` → `openapi/openapi.json`、`internal/controller/http/validation.json`、`internal/controller/http/gen/gen.go`（见 [openapi.md](openapi.md)） |
| `make generate` | openapi + sqlc（`migrations/`、`internal/repo/postgres/queries/` → `sqlcgen/`）+ mockgen（`internal/repo/contracts.go` → `internal/repo/mocks/`） |
| `make generate-check` | 重新生成后检查与入库版本一致（CI） |

## 模块生成器

```bash
make module NAME=task              # 按本人归属控制访问
make module NAME=task PERMS=1      # 另登记 task:read / task:write 并在 policy 中要求
make module NAME=category PLURAL=categories
```

以 notes 模块为模板（`templates/module/`），生成：

- `openapi/src/{paths,schemas}/<plural>.yaml`、core 迁移、sqlc 查询；
- `internal/entity/<name>.go`、`internal/repo/postgres/<plural>.go`（含 `Purger`）、`internal/usecase/<name>/`、`internal/controller/http/v1/<plural>.go`；
- 用例单元测试与 `internal/app/<plural>_integration_test.go`。

并在 `modulegen:<slot>` 锚点处登记 policy、v1 Server 字段、`internal/app/app.go` 的 import 与模块组装、注销清理器、权限码与 `.golangci.yml` 的 `dupl` 排除，在 `repo/contracts.go`、`usecase/contracts.go` 末尾追加端口与用例接口，最后执行 `make generate` 与 `make fmt`。全部内容先在内存中生成，成功后才写盘；名称是 Go 关键字或预声明标识符、PostgreSQL 保留字，或与 entity、`internal/app`、spec 中已有的名称重名，或目标文件已存在时，拒绝生成。生成后按业务修改字段，再跑 `make check` 与 `make test-integration`。

## 脚本

| 脚本 | 作用 |
|---|---|
| `scripts/bench/run.sh up\|bench\|down` | 压测（见 [benchmark.md](benchmark.md)） |
| `scripts/check-generated.sh` | pre-commit：生成代码的输入有暂存改动时重新生成，生成结果有 diff 则失败 |
| `scripts/check-comment-history.sh` | pre-commit：新增注释出现变更历史句式时告警 |
| `scripts/commit-msg.sh` | commit-msg：conventional commits |
| `deploy/manage.sh` | 服务器上的启停、日志与健康检查（见 [deploy.md](deploy.md)） |
