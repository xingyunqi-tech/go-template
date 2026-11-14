# go-template

Go 后端模板，API 契约与 [nestjs-template](https://github.com/xingyunqi-tech/nestjs-template)（`release/exam-prep`）一致：前端（vite-vue3-temp、uniapp-template）用 Orval 从 `/docs-json` 生成的代码可直接对接本服务。

- 语言：Go 1.27
- HTTP：标准库 `net/http` + chi，spec-first（oapi-codegen strict server，输入为 nestjs 导出的 OpenAPI 文档）
- 分层：`cmd/` + `internal/{entity,usecase,repo,controller}` + `pkg/`（源自 evrone/go-clean-template）

实施方案与阶段见 [TEMPLATE_PLAN.md](TEMPLATE_PLAN.md)，开发约定见 [CLAUDE.md](CLAUDE.md)。

## 快速开始

```bash
cp .env.example .env
make install      # 下载 golangci-lint / prek / typos / sqlc，安装 Git 钩子
make infra        # 启动 Postgres 与 Redis
make migrate      # 建表（goose）
make seed         # 角色、权限、行政区划、配置与演示账号（admin@example.com / john.doe@example.com，密码 Secret00）
make dev          # http://localhost:3000
make worker       # 队列 worker 与周期任务（邮件、账号清理）
```

- `GET /`：应用信息
- `GET /api/health`：简单健康检查
- `/api/v1/auth/*`：邮箱注册 / 登录 / 确认 / 找回密码、刷新令牌、个人资料、改密、注销、会话管理
- `/api/v1/users`：用户管理（管理员）
- `GET /docs-json`：OpenAPI 文档（与 nestjs-template 逐字节相同）
- `GET /docs`：Scalar 文档页面

`SWAGGER_ENABLED` 未设置时，生产环境关闭文档。

## 常用命令

```bash
make help              # 列出全部目标
make generate          # 按 openapi/scope.yaml 生成接口代码，并由 sqlc 生成查询代码
make check             # lint + vet + 单元测试
make test-integration  # 集成测试（需要 Docker）
make test-contract     # 契约测试（含 4516 条 nestjs 校验 oracle 用例）
make test-parity       # 与 nestjs 参照服务逐请求比对（需要 Docker）
make test-e2e          # 构建镜像并以 compose 启动后黑盒测试
make openapi-sync      # 从 fastapi-temp 同步共享契约产物
make image             # 构建生产镜像
```

## 镜像

`Dockerfile` 多阶段构建，运行时为 `gcr.io/distroless/static-debian13:nonroot`，镜像只含一个静态二进制（子命令 `serve` / `worker` / `migrate` / `seed`）；compose 先运行一次性的 migrate，再启动 api 与 worker：

```bash
docker compose -f docker/docker-compose.base.yaml -f docker/docker-compose.postgres.yaml -f docker/docker-compose.redis.yaml up --build
```

## 许可证

[MIT](LICENSE)
