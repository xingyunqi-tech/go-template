# 数据库

PostgreSQL 18（镜像 `postgres:18.6-alpine`；nestjs 参照服务 `scripts/nestjs/nestjs.compose.yaml` 为 17.11）、pgx v5（pgxpool）、sqlc、goose；队列 River 的表在同一个库的 `river` schema。

## 连接配置

| 变量 | 默认 | 说明 |
|---|---|---|
| `DATABASE_URL` | 空 | 设置时优先使用 |
| `DATABASE_HOST` / `DATABASE_PORT` / `DATABASE_USERNAME` / `DATABASE_PASSWORD` / `DATABASE_NAME` | `localhost` / `5432` / `root` / `secret` / `api` | 未设置 `DATABASE_URL` 时拼接连接串 |
| `DATABASE_MAX_CONNECTIONS` | `80` | `serve` 进程连接池上限（pgxpool `MaxConns`） |
| `WORKER_DATABASE_MAX_CONNECTIONS` | `10` | `worker` 进程连接池上限，与 API 不共用连接池 |
| `DATABASE_CONNECTION_TIMEOUT_MS` | `10000` | 从连接池获取连接的超时（毫秒，正整数）；只约束获取阶段，超时按未知错误返回 500 |
| `DATABASE_SSL_ENABLED` / `DATABASE_REJECT_UNAUTHORIZED` / `DATABASE_CA` / `DATABASE_KEY` / `DATABASE_CERT` | `false` / `true` / 空 | 语义同 node-postgres ssl 选项，CA / KEY / CERT 为 PEM 文本 |
| `DB_SCHEMA_OWNER` | `goose` | `external` 表示表结构由 nestjs 迁移管理，见下文 |

所有连接的会话时区固定为 UTC（`pkg/postgres`），`timestamp` 列一律按 UTC 读写。

各进程的连接上限：

| 进程 | 上限 |
|---|---|
| `app serve` | `DATABASE_MAX_CONNECTIONS` |
| `app worker` | `WORKER_DATABASE_MAX_CONNECTIONS` |
| `app migrate`、`app seed` | 2，部署时一次性运行 |

部署时须满足所有 api 副本 + worker 副本 ≤ Postgres `max_connections`，默认值为 80 + 10 ≤ 100；多副本时按副本数分摊 `DATABASE_MAX_CONNECTIONS`。

## 模型

- 表名、列名、约束名、索引与 nestjs 一致，`db/nestjs-schema.sql` 为对照快照；列名 camelCase，SQL 中加双引号（`"firstName"`），sqlc 生成的 Go 字段为 `FirstName`
- `migrations/` 是 sqlc 的 schema 输入，`internal/repo/postgres/queries/*.sql` 是查询输入，生成到 `internal/repo/postgres/sqlcgen/`（不手改）；`internal/repo/postgres/*.go` 把 sqlcgen 结构映射为 entity
- 动态排序的列表手写 SQL：排序列只取白名单映射的列名，值走占位参数；未知排序字段按未知错误返回 500（与 TypeORM `EntityPropertyNotFoundError` 一致）
- 时间列为 `timestamp without time zone`，存 UTC；API 输出为 `toISOString` 格式（毫秒、`Z`）
- 软删除列 `deletedAt`；已删除用户的唯一字段由部分索引释放
- uuid 主键（`file`、`user_consent`）：插入时由应用以 `uuid.NewV7()` 生成（按时间有序）；列默认值 `uuid_generate_v4()` 与 nestjs 表结构一致，初始迁移创建 `uuid-ossp` 扩展
- 新约束命名：`<table>_<column>_key`、`<table>_<column>_fkey`、`idx_<table>_<column>`（`make test-migrations` 检查）

表：`user`、`role`、`status`、`session`、`permission`、`role_permission`、`file`、`audit_logs`、`config`、`region`、`notification`、`notification_setting`、`address`、`favorite`、`feedback`、`user_consent`、`account_cleanup`、`admin_alert`。

唯一约束冲突（SQLSTATE 23505）按约束名查 `internal/controller/http/httperr/translate.go` 中从 nestjs `UNIQUE_CONSTRAINT_RESPONSES` 复制的表，未登记的约束返回 409。

## 事务

| 用法 | 行为 |
|---|---|
| policy `Tx: true` | `transactional` strict 中间件在处理器外开启事务，`pgx.Tx` 经 context 传给 repo；响应写出前提交，提交失败返回 500 |
| `DB.AfterCommit(ctx, fn)` | 登记提交成功后执行的副作用（如再次清除会话缓存）；无请求事务时立即执行；回调的 context 不携带已结束的事务 |
| `DB.InTx(ctx, fn)` | 请求外（worker、CLI）手动开启独立事务，`fn` 成功则提交 |
| `Jobs.Enqueue*` | 请求事务内以 savepoint 执行 River `InsertTx`，入队失败不破坏外层事务；任务随提交可见 |

请求事务的结束规则：

| 处理器结果 | 事务 |
|---|---|
| 成功 | 提交 |
| 4xx 业务 / HTTP 错误（`BusinessError`、401、403、404、422 等） | 提交已写入的内容并执行已登记的提交后回调，再输出错误 |
| 错误来源于 SQL 语句本身（如唯一约束冲突翻译成的 422 / 409） | 回滚 |
| 5xx、panic | 回滚 |

鉴权守卫在事务开始前完成，repo 在事务内只用事务连接，同一请求不会同时持有两条连接。无请求事务时，repo 每次调用在 `DATABASE_CONNECTION_TIMEOUT_MS` 内从池中获取一条连接，用完即归还。

## 迁移

```bash
make migrate                                                 # = go run ./cmd/app migrate（读取 .env）
go tool -modfile=tools/go.mod goose -dir migrations create add_product sql
make generate                                                # 迁移或查询变更后重新生成 sqlcgen
make test-migrations                                         # 空库 up → sqlc vet → down-to 0 → up → 命名检查（需要 make infra）
```

- 迁移文件 `migrations/<版本>_<名称>.sql`，当前只有 `00001_initial_schema.sql`（含 `-- +goose Up` 与 `-- +goose Down`）；`migrations.FS` 将其 `embed` 进二进制
- `app migrate` 依次执行 goose 迁移与 River 迁移（`rivermigrate`，schema `river`，River 自带版本表）
- 已合并的迁移不修改，变更通过新迁移完成
- compose 中由一次性 `migrate` 服务执行，api 与 worker 依赖其成功完成，多副本不会并发迁移
- 集成测试 `TestMigrationsMatchNestjsSchemaSnapshot` 对迁移后的库执行与 `scripts/schema_snapshot.py` 相同的 `pg_catalog` 查询（`internal/testutil/schema.go`），断言与 `db/nestjs-schema.sql` 只差引用本期未实现表的两个外键（`FK_feedback_errata`、`FK_feedback_question`）

## 与 nestjs 共用数据库

| 部署方式 | 表结构归属 | 设置 |
|---|---|---|
| 独立部署（默认） | goose | `DB_SCHEMA_OWNER=goose`、`PASSWORD_HASHER=argon2id` |
| 与 nestjs 共库 | nestjs（TypeORM 迁移） | `DB_SCHEMA_OWNER=external`、`PASSWORD_HASHER=bcrypt` |

`external` 时 `app migrate` 不执行 goose，只检查当前 schema 包含 `db/nestjs-schema.sql` 列出的全部列，再迁移 `river` schema；`app serve` 启动时做同样的列检查，缺失时启动失败并列出缺失项。

共库时 Redis 键名与 nestjs 逐字相同（`jwt:session:`、`session:rotated:`、`sms:code:`、`lock:`、`cache::` 等），限流状态键带 `:gcra` 后缀、与 nestjs 的固定窗口键分开计数；River 与 BullMQ 不互通，两边各自处理自己的任务。

## Seed

`make seed`（`app seed`）在一个事务内写入，已存在的数据不覆盖：

| 数据 | 来源 |
|---|---|
| 角色、状态、权限、角色权限、配置项 | `seeds/seed.json` |
| 行政区划 | `seeds/region-data.json`（`region` 表为空时写入） |
| 管理员 | 没有未删除的 admin 角色用户时创建：非生产为 `admin@example.com` / `Secret00`；生产（`APP_ENV=production`）取 `SEED_ADMIN_EMAIL`（去空白、转小写）/ `SEED_ADMIN_PASSWORD`，任一未设置则不创建 |
| 示例用户 | 非生产且没有未删除的 user 角色用户时创建 `john.doe@example.com` / `Secret00` |

密码以 MD5 后按 `PASSWORD_HASHER` 哈希存储。账号与 nestjs seed 一致，前端模板可直接登录。角色权限缓存（`permissions:v2:role-map`，5 分钟）不由 seed 清除，改动 `role_permission` 后最多 5 分钟生效。
