# OpenAPI

接口以 spec-first 方式维护：`openapi/src/` 是唯一的接口定义，路由、请求校验与处理器签名都由它生成。

## 源文件

| 文件 | 内容 |
|---|---|
| `openapi/src/openapi.yaml` | 根文档：info 与 `bearerAuth` |
| `openapi/src/common.yaml` | 错误 schema、校验错误、分页与 `Idempotency-Key` 参数、公共错误响应 |
| `openapi/src/paths/<module>.yaml` | 该模块的 `paths` |
| `openapi/src/schemas/<module>.yaml` | 该模块的 `components` |

各文件只含 `paths` 与 `components`，`$ref` 一律写文档内引用 `#/components/...`；同名 path 或 component 会让 bundle 失败。

成功响应写 `x-envelope: true` 与数据 schema，bundler 展开为约定 §2.1 的信封 `<Name>Envelope`（数组为 `<Name>ListEnvelope`，`type: 'null'` 为 `NullEnvelope`）。错误响应引用 `common.yaml` 的 `components/responses`：422 用 `ValidationFailed`，其余用对应的 `ErrorResponse` 响应；429 与 503 由 bundler 为每个 `/api` 操作补上。

`x-go-type` 等 `x-go-*` 扩展只影响生成的 Go 类型（例如 `avatarFileId` 用 `pkg/optional.Field` 区分缺省与 null），不出现在发布的文档中。

## 生成

`make openapi`（`tools/openapigen`）：

1. 合并源文件、展开信封、补 429 / 503，输出排序稳定的 `openapi/openapi.json`（入库，`GET /openapi.json` 原样返回）。
2. 输出全内联、无 `$ref` 的 `internal/controller/http/validation.json`，运行时 `openapi3filter` 用它校验请求。
3. 只为 2xx 响应运行 oapi-codegen（chi-server + strict-server），生成 `internal/controller/http/gen/gen.go`；错误由处理器返回 `entity.Error`，统一写成信封。

`make generate` 依次执行 openapi、sqlc、mockgen。三个生成文件不手改；契约测试检查 bundle 无漂移。

## 新增接口

1. 在 `openapi/src/paths|schemas/<module>.yaml` 写接口；operationId 用 lowerCamelCase。tag `auth`、`me`、`users` 保留给身份接口：验证服务按这三个 tag 去掉操作，业务接口用了它们会在验证服务中消失。
2. `make generate`。
3. 在 `internal/controller/http/policy.go` 为 operationId 声明 policy；需要登录的接口在 spec 中声明 `security: [bearerAuth: []]`（可选登录为 `[{}, {bearerAuth: []}]`）。
4. 在 `internal/controller/http/v1/` 实现生成的方法，返回 `gen.<Op><Status>JSONResponse`。

operation 缺 policy、policy 缺 operation、operation 缺处理器，启动都会失败。CRUD 模块可用 `make module NAME=<name>` 一次生成。

## 契约测试

`tests/contract`（`make test-contract`）检查：

- bundle 与入库的 `openapi.json` 一致，`GET /openapi.json` 逐字节相同；
- `chi.Walk` 得到的路由与文档 paths 双向一致；
- operationId 唯一且为 lowerCamelCase；每个 operation 有 policy，鉴权 policy 与 `bearerAuth` 声明一致；
- 422 响应引用 `ValidationErrorResponse`，其余错误响应引用 `ErrorResponse`；
- 约定 §11 的全部接口（方法、路径、operationId、请求字段、成功状态码）与 §11.4 对象字段。
