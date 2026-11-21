# 测试

`go test` + testify，gotestsum 输出 JUnit 到 `coverage/`；单元、集成、契约三层均以 `-race -shuffle=on` 运行。

## 分层

| 层 | 位置 | build tag | 依赖 | 运行 |
|---|---|---|---|---|
| 1 单元 | 与源码同目录 `*_test.go` | 无 | 无 | `make test` |
| 2 集成 | 与源码同目录 `*_integration_test.go`，夹具在 `internal/testutil` | `integration` | Docker（testcontainers） | `make test-integration` |
| 3 e2e | `tests/e2e` | `e2e` | Docker Compose | `make test-e2e` |
| 4 契约 | `tests/contract` | `contract` | 无 | `make test-contract` |

`make check` = lint + vet + 单元测试，是 pre-push 钩子。`make vet` 带上全部三个标签编译，保证各层测试代码都能通过编译。

## 覆盖率

`make test`、`make test-integration`、`make test-contract` 分别输出 `coverage/{unit,integration,contract}.out`（`-coverpkg=./internal/...,./pkg/...`）；`make coverage` 用 go-test-coverage 合并三份并校验语句覆盖率 ≥ 80%（`.testcoverage.yml`，排除 `gen/`、`sqlcgen/`、mock 与 `internal/testutil/`）。

## 单元测试

- 用例调用 `t.Parallel()`（paralleltest）；使用 `t.Setenv` 的用例除外
- usecase 依赖 repo 端口与包内声明的端口：mockgen（go.uber.org/mock 0.6.0，`-typed`）生成于 `internal/repo/mocks` 与 `internal/usecase/<pkg>/mocks`，测试在外部测试包 `<pkg>_test` 中引用；简单端口用手写 fake
- 中间件、管线、守卫、限流、错误翻译、view 类型用 `httptest` 直接测试
- `pkg/validatorx` 的 validator.js 移植、`pkg/ratelimit` 的 GCRA 计算（含与 fastapi-temp 共用 Redis 键的 TAT 格式）、`pkg/msduration`、`pkg/password`、`pkg/jwtx` 各有单测
- 邮件模板以 `tests/fixtures/nestjs_mail/*.json`（nestjs 渲染的 subject / text / html）为 golden 逐字节比对

## 集成测试

| 夹具 | 说明 |
|---|---|
| `testutil.StartDatabase` | 每个测试包的 `TestMain` 启动一个 `postgres:18.6-alpine` 容器，在模板库上执行 goose + River 迁移与 seed |
| `Database.Clone(t)` | `CREATE DATABASE … TEMPLATE` 克隆独立库并返回 DSN，用例结束 `DROP … WITH (FORCE)`；真实连接池、真实提交，用例可并行 |
| `testutil.StartRedisPool` | 每个用到 Redis 的测试包在 `TestMain` 启动一个 `redis:8.10.3-alpine` 容器；用例 `pool.Borrow(t)` 独占借一个逻辑库（DB 0–15，全部借出时阻塞），借出前、归还前各 `FLUSHDB`；客户端以 `Options{Addr: db.Addr, DB: db.DB}` 连接，对应生产的 `REDIS_HOST:REDIS_PORT` 与 `REDIS_DB`。用例里只用 `FLUSHDB`，不用 `FLUSHALL` 或跨库的全局命令 |
| `testutil.StartMinio` | `chainguard/minio`（按 digest 固定）容器与独立桶，覆盖 `s3` / `s3-presigned` 驱动 |
| `testutil.SchemaSnapshot` | 按 `pg_catalog` 输出表结构快照，比对迁移结果与 `db/nestjs-schema.sql` |

- HTTP 级用例通过 `app.NewHandler` 组装完整路由（真实中间件链、真实 Postgres），用 `httptest` 发请求
- local 文件驱动的用例用 `FILE_LOCAL_ROOT` 指向 `t.TempDir()`
- 依赖真实提交语义的场景（刷新 CAS 与重放吊销、4xx 提交 / SQL 错误回滚、提交后清缓存、River 提交后可见、`lastActiveAt` 节流）都在克隆库上运行
- testcontainers 起不来时测试失败，不跳过；未设置 `DOCKER_HOST` 且 `/var/run/docker.sock` 不存在时，夹具使用 `~/.docker/run/docker.sock`（Docker Desktop）

```bash
go test -tags integration -run TestRefresh ./internal/app/...
```

## 契约测试

`tests/contract/contract_test.go`：

| 用例 | 断言 |
|---|---|
| `TestDocsJSONServesBaselineBytes` | `/docs-json` 与 `openapi/nestjs-baseline.json` 逐字节一致 |
| `TestSourceDeclaresContractOrigin` | `openapi/SOURCE` 五个键齐全，`environment` 为 `NODE_ENV=development FILE_DRIVER=local` |
| `TestEveryScopedOperationIsRegistered` | `scope.yaml` 中每个 operation 都有注册的处理器（`chi.Walk`） |
| `TestEveryRegisteredRouteIsDocumented` | 每个注册的路由都在基线中，或在白名单 `GET /docs`、`GET /docs-json`、`GET /metrics` 中 |
| `TestPoliciesMatchScopeAndDocumentedSecurity` | `scope.yaml` 的每个 operation 都有 policy；文档声明 bearer 的必须鉴权、反之亦然；policy 中的 operation 都在 `scope.yaml` 或 `Undocumented()` 中 |
| `TestValidationMetadataIsFullyImplemented` | `validation.json` 中出现的约束与转换函数 `pkg/validatorx` 全部实现 |

`tests/contract/validation_test.go`：`TestValidationCasesMatchNestjsValidationPipe` 逐条运行 `tests/contract/fixtures/validation-cases.json`（nestjs 真实 ValidationPipe 跑出的期望结果），比对 `ok`、转换后的值与 422 的 `errors`。

## e2e

`make test-e2e` 以 base + postgres + redis + ci 组合（compose 项目 `go-template-e2e`）`up -d --build --wait` 起完整服务栈：生产镜像、`APP_ENV=production`、一次性 migrate 与 seed、api、worker。用例经 `APP_URL`（默认 `http://localhost:3000`）黑盒访问，失败时输出 compose 日志，结束后 `down -v`。

覆盖：`GET /`、`/docs-json` 逐字节一致、未匹配路由的 404 信封、`/api/health`、就绪 / 存活探针、生产环境监控令牌、邮箱注册 → 登录 → `auth/me` → 刷新 → 退出、seed 管理员列出用户。

## 编写约定

- 用例名（`TestXxx` 与 `t.Run` 子测试名）只陈述行为
- 新接口至少有集成测试覆盖成功路径与各类错误形状
- 需要真实提交语义的用例用 `Clone(t)` 得到的独立库，不用外层事务包裹
