# 数据库

PostgreSQL ≥ 18（列默认值用内置 `uuidv7()`）。驱动 pgx v5（pgxpool），查询由 sqlc 生成，迁移用 goose（库方式嵌入）。

## 连接与 schema

| 变量 | 说明 |
|---|---|
| `DATABASE_URL` | libpq URI，由 pgx 直接解析（`sslmode` 等参数写在 URI 中）；本地未设置时为 `make infra` 的库 |
| `DATABASE_SCHEMA` | 本服务独占的 schema，连接的 `search_path` 指向它；`migrate` 先 `CREATE SCHEMA IF NOT EXISTS` |
| `DATABASE_MAX_CONNS` / `WORKER_DATABASE_MAX_CONNS` | 连接池上限，默认 20 / 10 |

同一 monorepo 中每个服务用自己的 schema，只迁移、只写自己的 schema，不建跨 schema 外键（约定 §10）。River 表与 goose 版本表也在该 schema。

## 迁移

| 目录 | 内容 | 执行者 |
|---|---|---|
| `migrations/core/` | `audit_log`、`note`、`file` 与生成的模块表 | 所有角色 |
| `migrations/identity/` | `app_user`、`user_role`、`user_identity`、`session`、`session_refresh_history`、`user_token` | 只有签发服务 |

两个 goose provider，版本表为 `goose_core_version`、`goose_identity_version`。`app migrate` 依次执行 core、identity（签发服务）与 River 迁移；`app migrate down` 反向回滚全部。新建迁移：`make migrate-create NAME=add_orders [SET=core|identity]`。

`make test-migrations`（需要 `make infra`）在独立 schema 中执行 up → `sqlc vet` → down（只剩 goose 版本表）→ up，并检查约束与索引命名。

## 模型规则

| 项 | 规则 |
|---|---|
| 命名 | 表名单数 snake_case（用户表为 `app_user`），列 snake_case |
| 主键 | `id uuid PRIMARY KEY DEFAULT uuidv7()`，应用侧 `uuid.NewV7()` 生成 |
| 时间 | `timestamptz`：`created_at`、`updated_at`，软删 `deleted_at` |
| 枚举 | `text` + `CHECK` |
| 唯一 | 软删表用部分唯一索引 `WHERE deleted_at IS NULL`，软删后邮箱、手机号可重新注册 |
| 用户引用 | core 表只存用户 `sub`（`uuid` 列，无外键）；identity 表之间可建外键 |
| 约束命名 | `<table>_pkey`、`<table>_<columns>_key|fkey|idx`、`<table>_<name>_check` |

sqlc 把 `uuid` 映射到标准库 `uuid.UUID`、`timestamptz` 映射到 `time.Time`。

## 事务

policy `Tx: true` 的接口在请求事务内执行：事务在第一条语句时才取连接并 `BEGIN`；2xx 与 4xx 提交，5xx 回滚；响应、审计在提交后发出；鉴权在事务开始前完成。用例需要独立的短事务时调用 `DB.InTx`（注册、登录、第三方登录、注销清理）。`AfterCommit` 登记提交后的副作用；River 任务用 `InsertTx` 在请求事务内入队。

## Seed

`app seed` 幂等：非生产创建 `admin@example.com`（`admin` + `user`）与 `user@example.com`，密码 `Passw0rd!`；生产只在设置 `SEED_ADMIN_EMAIL` 与 `SEED_ADMIN_PASSWORD` 且没有 `active` 管理员时创建管理员。验证服务没有用户表，不执行 seed。
