# 测试

`go test` + testify，gotestsum 输出 JUnit 到 `coverage/`；单元、集成、契约以 `-race -shuffle=on` 运行。

## 分层

| 层 | 位置 | build tag | 依赖 | 运行 |
|---|---|---|---|---|
| 单元 | 与源码同目录 `*_test.go` | 无 | 无 | `make test` |
| 集成 | 与源码同目录 `*_integration_test.go`，夹具在 `internal/testutil` | `integration` | Docker（testcontainers） | `make test-integration` |
| 契约 | `tests/contract` | `contract` | 无 | `make test-contract` |
| e2e | `tests/e2e` | `e2e` | Docker Compose | `make test-e2e` |
| 迁移 | Makefile | — | `make infra` | `make test-migrations` |

`make check` = lint + vet + 单元测试，是 pre-push 钩子。`make vet` 带上全部标签编译。宣称改动完成前，`make check`、`make test-integration`、`make test-contract` 必须通过；涉及部署形态的改动再跑 `make test-e2e` 与 `make image`。

## 覆盖率

`make coverage` 合并单元、集成、契约三份覆盖率（`-coverpkg=./internal/...,./pkg/...`），语句覆盖率 ≥ 80%（`.testcoverage.yml`，排除生成代码、mock 与 `internal/testutil/`）。

## 单元测试

- 用例调用 `t.Parallel()`（paralleltest），使用 `t.Setenv` 的除外。
- 用例依赖的 repo 端口用 mockgen 生成的 `internal/repo/mocks`（新增端口时把源文件加入 Makefile `MOCK_SOURCES`）；简单端口用手写 fake。
- 中间件、管线、限流、错误写出用 `httptest` 直接测；第三方客户端（短信、微信、支付宝）对 httptest 服务器测请求签名、响应验签与错误映射。
- 码表与约定 §2.4 逐条比对；校验适配器的关键字映射对「含 `$ref` 的文档」与「全内联文档」各跑一遍。

## 集成测试

| 夹具 | 说明 |
|---|---|
| Postgres | 每个包一个容器；`testutil.StartDatabase` 迁移模板库，用例 `Clone(t)` 得到独立库，可并行 |
| Redis | 每个包一个容器；`pool.Borrow(t)` 独占一个逻辑库（借出前、归还前 `FLUSHDB`） |
| MinIO | `testutil.StartMinio`（`chainguard/minio`，按 digest 固定） |
| SMTP | `testutil.StartSMTP` 进程内服务器；`Next(t, to).Token(t)` 取邮件链接中的令牌 |
| 令牌 | `testutil.IssueAccessToken`（固定 Ed25519 测试密钥，`testutil.SigningKeyPEM()` 为对应的 `AUTH_SIGNING_KEY`） |
| 第三方 | 短信、微信、支付宝用 httptest 假服务器，经 `ALIYUN_SMS_ENDPOINT`、`WECHAT_API_BASE`、`ALIPAY_GATEWAY` 指过去 |

`internal/app` 的 HTTP 用例经 `testApp.do` 发请求，`/api/v1` 的每个响应都用 `openapi3filter.ValidateResponse` 对照 `openapi/openapi.json` 校验。需要 worker 的用例（邮件、注销清理、周期任务）用 `app.NewWorkerClient` 启动 River；`identityApp.workerLog` 收集 worker 日志。testcontainers 起不来时测试失败，不跳过。

## 契约测试

见 [openapi.md](openapi.md#契约测试)。

## e2e

`make test-e2e` 构建生产镜像，以 `docker/docker-compose.ci.yaml` 起完整栈（`APP_ENV=production`，Mailpit 收信，固定测试签名密钥），黑盒覆盖：健康检查与指标、notes、注册到登出（含 Mailpit 中的验证邮件）、注销、上传头像并下载、JWKS（含 `AUTH_VERIFY_KEYS` 旧密钥）、互通（`api-verifier` 经 `AUTH_JWKS_URL` 验证签发服务的令牌，签发服务登出后返回 401，不提供身份接口）。

## 编写约定

- 测试名只陈述行为，不写「回归 / 防止 / 修复」。
- 断言用 `require` 处理前置条件、`assert` 处理检查项。
- 集成测试的状态隔离靠克隆库与独占的 Redis 逻辑库，不依赖执行顺序。
