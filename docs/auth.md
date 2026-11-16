# 认证与权限

实现位置：`internal/usecase/{auth,session,permission,sms,user}`、`internal/controller/http/{guard.go,policy.go,throttle.go}`、`internal/repo/webapi/{sms,wechat,alipay}`、`pkg/{jwtx,password,msduration}`。

## 接口

全部成功响应为 HTTP 200。登录类接口返回 `{refreshToken, token, tokenExpires(ms), user}`，`auth/refresh` 返回 `{token, refreshToken, tokenExpires}`。

| 方法 | 路径（`/api/v1/` 下） | operationId | 鉴权 | 限流 | 请求事务 |
|---|---|---|---|---|---|
| POST | `auth/email/login` | loginWithEmail | 公开 | 5 / 60s | 是 |
| POST | `auth/email/register` | registerWithEmail | 公开 | 默认 | 是 |
| POST | `auth/email/confirm` | confirmEmail | 公开 | 默认 | 是 |
| POST | `auth/email/confirm/new` | confirmNewEmail | 公开 | 默认 | 是 |
| POST | `auth/forgot/password` | forgotPassword | 公开 | 3 / 300s | 是 |
| POST | `auth/reset/password` | resetPassword | 公开 | 5 / 300s | 是 |
| GET | `auth/me` | getCurrentUser | JWT | 默认 | |
| PUT | `auth/me` | updateCurrentUser | JWT | 10 / 60s | 是 |
| DELETE | `auth/me` | deleteCurrentUser | JWT | 默认 | 是 |
| POST | `auth/refresh` | refreshToken | refresh JWT | 默认 | 是 |
| POST | `auth/logout` | logout | JWT | 默认 | 是 |
| GET | `auth/sessions` | getMySessions | JWT | 默认 | |
| DELETE | `auth/sessions/:id` | revokeMySession | JWT | 默认 | 是 |
| POST | `auth/sessions/revoke-others` | revokeOtherSessions | JWT | 默认 | 是 |
| PUT | `auth/password` | changePassword | JWT | 3 / 60s | 是 |
| POST | `auth/phone/send-code` | sendSmsCode | 公开 | 3 / 60s | |
| POST | `auth/phone/login` | loginWithPhone | 公开 | 5 / 60s | 是 |
| POST | `auth/phone/sms-login` | loginWithSms | 公开 | 5 / 60s | 是 |
| POST | `auth/phone/register` | registerWithPhone | 公开 | 3 / 60s | 是 |
| POST | `auth/phone/reset-password` | resetPasswordByPhone | 公开 | 5 / 300s | 是 |
| POST | `auth/wechat/login` | loginWithWechat | 公开 | 10 / 60s | |
| POST | `auth/wechat/phone-login` | loginWithWechatPhone | 公开 | 10 / 60s | |
| POST | `auth/alipay/login` | loginWithAlipay | 公开 | 10 / 60s | |

「默认」指 `THROTTLE_LIMIT` 次 / `THROTTLE_TTL` 秒（100 / 60），`THROTTLE_ENABLED=false` 时不限流也不输出限流头。「请求事务」列为 policy 的 `Tx`，规则见 [database.md](database.md#事务)；发短信与微信、支付宝登录要等待外部 HTTP 调用，不声明 `Tx`，其中的写入各自提交。

## 鉴权方式

每个已实现的 operationId 必须在 `internal/controller/http/policy.go` 中声明，缺失时启动失败。

| `Auth` | 行为 |
|---|---|
| `AuthNone` | 不校验 |
| `AuthJWT` | 只读取 `Authorization: Bearer <token>`（scheme 不区分大小写），校验见下；失败 401 |
| `AuthOptionalJWT` | 无令牌或校验失败时按匿名处理 |
| `AuthRefresh` | 仅 `auth/refresh`，Bearer refresh 令牌 |
| `AuthMonitor` | `/api/v1/health`、`/metrics`：配置 `METRICS_TOKEN` 时要求 `Bearer <METRICS_TOKEN>`（常量时间比较）；未配置时非生产放行、生产拒绝；拒绝为 403 `Forbidden resource` |

## JWT

| 令牌 | 载荷 | 密钥 | 默认有效期 |
|---|---|---|---|
| access | `{id, role, sessionId, iat, exp}` | `AUTH_JWT_SECRET` | `AUTH_JWT_TOKEN_EXPIRES_IN=15m` |
| refresh | `{sessionId, hash, iat, exp}` | `AUTH_REFRESH_SECRET` | `AUTH_REFRESH_TOKEN_EXPIRES_IN=3650d` |
| 找回密码 | 含密码指纹 `sha256(hash)` 前 16 位，改密后失效 | `AUTH_FORGOT_SECRET` | `AUTH_FORGOT_TOKEN_EXPIRES_IN=30m` |
| 邮箱确认 | `confirmEmailUserId`（换邮箱时另有 `newEmail`） | `AUTH_CONFIRM_EMAIL_SECRET` | `AUTH_CONFIRM_EMAIL_TOKEN_EXPIRES_IN=1d` |

- 算法 HS256，解析时只接受 HS256
- access 的 `role` 在登录 / 注册类接口签发时为 `{id, name, __entity: "RoleEntity"}`，`auth/refresh` 签发时为 `{id}`
- `*_EXPIRES_IN` 按 npm `ms` 2.x 规则解析（`pkg/msduration`），无法解析时启动失败；`tokenExpires` = 当前毫秒时间 + access 有效期
- 生产环境要求四个密钥均 ≥32 字符且互不相同

### access 校验

1. 令牌验签与过期检查，载荷必须有非零的 `id`、`sessionId`，否则 401（`common.unauthorized` 译文）
2. 查缓存 `jwt:session:{sessionId}`（30s，`{statusId, roleId}`）；命中时状态不是 active / inactive → 401 `User is not active`
3. 未命中时用一条连接执行：会话联表未删除的用户查询，再节流更新 `lastActiveAt`（`lastActiveAt` 为空或早于 5 分钟才更新，失败只记日志）；会话不存在、已删除或不属于该用户 → 401 `Session expired`；用户状态不是 active / inactive → 401 `User is not active`
4. 以数据库中的角色为准写入缓存

会话被删除时（logout、吊销、改密、重置密码、重放吊销），`jwt:session:{id}` 在写库后立即清除一次，事务提交后再清除一次。

### 刷新轮换

- 会话不存在或无关联用户 → 401
- `session.hash != payload.hash`：若等于 60 秒内被换下的旧 hash（`session:rotated:{id}`，只保存最近一次）视为并发重试，只返回 401；否则判为重放，软删会话并 401。吊销写入随 4xx 提交
- 用户无角色 → 401；状态不是 active / inactive → 422 `{refresh: 'userNotActive'}`
- 生成新 hash，以 `UPDATE … WHERE hash = old` 做 CAS，失败 401；成功返回新的 `{token, refreshToken, tokenExpires}`
- logout 软删当前会话；改密（`PUT auth/password`、`PUT auth/me` 带 `password`）、吊销其它会话软删除当前会话以外的会话；重置密码（邮件或短信）软删全部会话；worker 每天 03:00 UTC 物理删除软删超过 7 天的会话

## 密码

客户端传 MD5 hex（32 位）。验证时同时尝试原值与大写形式。服务端哈希由 `PASSWORD_HASHER` 决定：

| 值 | 用途 |
|---|---|
| `argon2id`（默认，m=19456 KiB、t=2、p=1，PHC 字符串） | 独立部署 |
| `bcrypt`（cost 10） | 与 nestjs 共用数据库时 |

两种格式都能验证；验证通过且存储的哈希不是当前算法或参数时，按当前设置重哈希后写回。每个进程同时进行的哈希 / 验证数由 `PASSWORD_HASH_CONCURRENCY`（默认 2）限制。

## 登录方式

| 方式 | 行为 |
|---|---|
| 邮箱 | 注册后状态 inactive，入队确认邮件；active / inactive 均可登录；失败 422 `{email: 'notFound'}` / `{password: 'incorrectPassword'}`（输出为译文） |
| 手机 + 密码 | 失败 422 `{credentials: 'incorrectPhoneOrPassword'}` |
| 短信验证码 | 不自动注册，未知手机号 422 |
| 手机注册 | 直接 active 并返回令牌 |
| 微信 | `type`：`mini_app`（`jscode2session`）、`app`（OAuth）；按 openId 自动建号，唯一约束冲突时重新查询 |
| 微信手机号 | 绑定 openId 与手机号，不合并已有账号（`phoneBoundToOtherWechat`）；非大陆号拒绝（`phoneRegionNotSupported`） |
| 支付宝 | `alipay.system.oauth.token`，按 alipayOpenId 自动建号 |

注册与首次第三方登录时写入默认通知设置。

### 短信验证码

| 项 | 规则 |
|---|---|
| 类型 | `register` 要求手机号不存在（`phoneAlreadyExists`）；`login` / `reset_password` / `verify_identity` 要求存在（`phoneNotFound`）；`change_phone` 返回 `unsupportedCodeType` |
| 存储 | `sms:code:{type}:{phone}`，TTL `SMS_CODE_EXPIRES`（300s），`SMS_CODE_LENGTH`（6）位数字，`crypto/rand` 生成 |
| 重发间隔 | `SMS_RESEND_INTERVAL`（60s），锁 `sms:send:{type}:{phone}`；失败 422 `{phone: 'rateLimited', retryAfter}` 或 `{phone: 'sendFailed'}` |
| 尝试次数 | `SMS_MAX_ATTEMPTS`（5），成功或超限后删除；比较为常量时间 |
| 发送 | 阿里云 Dysms（`SMS_ENDPOINT`、`SMS_SIGN_NAME`、`SMS_TEMPLATE_CODE`）；`SMS_MOCK_MODE=true` 只写日志（`.env.example` 默认开启） |

微信、支付宝配置：`WECHAT_MINI_APP_ID` / `WECHAT_MINI_APP_SECRET`（小程序）、`WECHAT_APP_ID` / `WECHAT_APP_SECRET`（App）、`ALIPAY_APP_ID`、`ALIPAY_PRIVATE_KEY`、`ALIPAY_GATEWAY`、`ALIPAY_SIGN_TYPE`、`ALIPAY_CHARSET`、`ALIPAY_SANDBOX`。短信、微信、支付宝的出站请求不接入链路追踪，传输错误中的请求 URL 只保留 scheme、host 与 path（`internal/repo/webapi/outbound`）。

## RBAC

| 角色 | id | 状态 | id |
|---|---|---|---|
| admin | 1 | active | 1 |
| user | 2 | inactive | 2 |
| editor | 3 | | |
| reviewer | 4 | | |

- 权限枚举与默认映射：`internal/entity/permission.go`（值与 nestjs `PermissionEnum` 一致）；admin 拥有全部权限
- 角色权限读 `permission`、`role_permission` 表，表为空时回落代码默认值；缓存键 `permissions:v2:role-map`，5 分钟
- policy 中 `RBAC: true` 或声明了 `Permissions` 的路由经过权限守卫；`AnyPermission: true` 时任一满足即可
- 经过权限守卫却未声明权限 → 403 `No permission declared for this route`；无身份 → 403 `User not authenticated`；无角色 → 403 `User role not found`
- 缺权限 → 403 `Missing permissions: a, b` / `Missing any of permissions: a, b`（原文输出，无翻译键）

## 会话

`session` 表记录 `userAgent`（≤512 个 UTF-16 码元）、`ip`（按 `TRUST_PROXY` 取）、`lastActiveAt`。`GET auth/sessions` 返回 `[{id, userAgent, ip, createdAt, lastActiveAt, current}]`，按 `lastActiveAt` 倒序；`DELETE auth/sessions/:id` 对非本人会话返回 404 `sessionNotFound`。

## 限流计数主体

声明 `AuthJWT` 且带有效 Bearer access 令牌（只验签，不查会话）的请求按 `user:{id}` 计数，其余（含可选 JWT、refresh、监控令牌路由，以及令牌无效或过期）按 `ip:{normalizeIp(ip)}` 计数。放行时带 `X-RateLimit-Limit` / `X-RateLimit-Remaining` / `X-RateLimit-Reset`，429 只带 `Retry-After`。
