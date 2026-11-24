# 认证与权限

接口、errorKey、令牌 claim、会话与限流规则以约定 §5、§11 为准，本文只写本服务的实现。代码在 `internal/usecase/{auth,session,sms,user,account}`、`internal/controller/http/{auth,policy}.go`、`internal/repo/webapi/{sms,wechat,alipay}`、`pkg/{jwtx,password}`。

## 令牌

| 项 | 实现 |
|---|---|
| 签名 | jwx v3，EdDSA（Ed25519）；JWS 头带 RFC 7638 `kid`。`AUTH_SIGNING_KEY` 为私钥 PEM（`make keys` 或 `app keys generate` 生成），`AUTH_VERIFY_KEYS` 为轮换中的旧公钥 |
| JWKS | 签发服务 `GET /.well-known/jwks.json`，含当前公钥与 `AUTH_VERIFY_KEYS`，`Cache-Control: max-age=300` |
| 验证 | 签发服务用本地公钥集；验证服务注册 `AUTH_JWKS_URL`（启动不等待首次拉取，未知 `kid` 至多每 30 秒刷新）；校验 `iss` / `aud` / `exp`，时钟偏差 30 秒；过期为 `auth.tokenExpired` |
| 吊销 | 登出、改密、重置、禁用、角色变更、注销写 `auth:revoked:sid:{sid}`（TTL = access 有效期 + 60 秒）；所有服务验证后查询；Redis 出错 503 |

### 轮换密钥

1. `app keys generate` 生成新密钥。
2. 新私钥写入 `AUTH_SIGNING_KEY`，旧公钥追加到 `AUTH_VERIFY_KEYS`，重启签发服务；JWKS 同时发布两把公钥。
3. 超过 `AUTH_ACCESS_TTL` 后，从 `AUTH_VERIFY_KEYS` 删除旧公钥。

## refresh 轮换与宽限期

refresh 为不透明随机串，只存 SHA-256。刷新在单事务内 `SELECT … FOR UPDATE` 锁会话行，生成新令牌对，旧哈希写入 `session_refresh_history`，并把新令牌对以 AES-256-GCM 加密暂存 30 秒（键 `{service}:auth:refresh:grace:{旧哈希}`，密钥由旧 refresh 经 HKDF 派生，服务端不持有密钥）。

- 30 秒内再次使用旧 refresh：返回暂存的同一对（并发刷新只产生一对）。
- 超过 30 秒：吊销该会话，401 `auth.refreshTokenReused`。
- 会话寿命为登录时间 + `AUTH_REFRESH_TTL`，轮换不延长。

## 密码

argon2id（m = 19 MiB、t = 2、p = 1），PHC 字符串；`PASSWORD_HASH_CONCURRENCY` 限制同时计算的数量。账号不存在时对固定假哈希校验一次，防时序探测。注册、登录、改密在请求事务外计算哈希。

## 登录方式

| 方式 | 说明 |
|---|---|
| 邮箱 + 密码 | 注册入队验证邮件；验证、找回密码令牌由 worker 签发（只存哈希，同类新令牌使旧令牌失效），邮件链接以 `APP_FRONTEND_URL` 为基址 |
| 手机号 + 短信验证码 | 未注册的手机号自动注册。`SMS_PROVIDER=aliyun` 用阿里云 Dysms（`ALIYUN_SMS_*`），`log` 只把验证码写进日志（仅限本地） |
| 微信小程序 | `WECHAT_MINIAPP_APP_ID` + `WECHAT_MINIAPP_APP_SECRET`；也启用 `bindPhoneWithWechat` |
| 微信 App | `WECHAT_APP_APP_ID` + `WECHAT_APP_APP_SECRET` |
| 支付宝 | `ALIPAY_APP_ID` + `ALIPAY_PRIVATE_KEY` + `ALIPAY_PUBLIC_KEY`；RSA2，响应验签 |

provider 只在凭据齐全时启用，只设了一部分时启动失败；`listAuthProviders` 列出已启用的。微信身份按 `(provider, openid)` 匹配，两个微信 provider 经 unionid 共用账号；首次登录自动建号，不按手机号合并。第三方 code 无效为 422 `auth.providerCodeInvalid`，调用失败为 502 `common.badGateway`。`WECHAT_API_BASE`、`ALIPAY_GATEWAY`、`ALIYUN_SMS_ENDPOINT` 默认指向生产地址。

短信验证码：6 位，5 分钟有效，单码 5 次尝试；同一手机号冷却 60 秒、每天（北京时间）10 条；1 小时内失败 10 次锁定 1 小时。

## 角色与权限

角色与权限码由 `internal/entity/rbac.go` 固定：`admin` 拥有本服务全部权限码，`user` 为每个账号的默认角色。平台权限码为 `user:read`、`user:write`、`role:read`、`role:write`、`audit:read`；`make module NAME=<name> PERMS=1` 登记模块私有的 `<name>:read` / `<name>:write`。policy 以 `Perms: All(...)` / `Any(...)` 引用，缺权限为 403 `common.forbidden`。

禁用、删除最后一个 `active` 的管理员，或移除其 `admin` 角色：409 `role.lastAdmin`（事务内取同一把 advisory 锁）。

## 注销

`deleteMe`（要求 10 分钟内登录过）与 `deleteUser` 在同一事务内软删用户、删除第三方身份、吊销全部会话，并入队 `account.purge`（延时 `ACCOUNT_DELETION_GRACE`）。软删后邮箱、手机号、第三方身份立即可重新注册。清理任务删除文件、笔记，匿名化审计日志，物理删除用户，然后发布 `user.deleted`；`account.sweep` 每小时补偿未完成的清理。
