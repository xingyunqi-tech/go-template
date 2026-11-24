# 压测

## 方法

```bash
scripts/bench/run.sh up      # 构建生产镜像，compose 起 api、worker、Postgres、Redis 与种子账号
scripts/bench/run.sh bench   # 每个接口 × 每个并发跑 DURATION，k6 摘要写入 OUT_DIR
scripts/bench/run.sh down    # 删除栈与数据卷
```

| 变量 | 默认 | 说明 |
|---|---|---|
| `ENDPOINTS` | `me notes create-note refresh login` | `getMe`、`listNotes`（limit 20）、`createNote`、`refreshToken`（每个 VU 轮换自己的 refresh）、`loginWithEmail` |
| `VU_LEVELS` | `10 50` | 并发 VU |
| `DURATION` | `30s` | 每轮时长 |
| `API_CPUS` / `API_MEM` | `2` / `1g` | api 容器的 CPU 与内存上限（`GOMEMLIMIT=800MiB`） |
| `K6_IMAGE` | `grafana/k6:1.4.2` | 在 compose 网络内运行 |

栈为生产镜像，`RATE_LIMIT_ENABLED=false` 关闭限流（生产要求该值为 true，所以 `APP_ENV=test`；见 `scripts/bench/compose.yaml`），其余配置同 `.env.example`；k6 在 setup 阶段以种子账号 `user@example.com` 为每个 VU 登录一次。

## 结果

2026-11-24，Apple M1 Max（10 核），Docker Desktop 28.5.1（VM 10 CPU / 7.75 GiB），api 限 2 CPU / 1 GiB；Postgres 18.7、Redis 8.10.3 与 k6 在同一 VM 内。所有请求均成功（`http_req_failed` 0%）。

| 接口 | VU | 请求 / 秒 | 平均 | p50 | p95 | p99 |
|---|---|---|---|---|---|---|
| `getMe` | 10 | 5999 | 1.59 ms | 1.22 ms | 2.97 ms | 6.08 ms |
| `getMe` | 50 | 7294 | 6.40 ms | 5.82 ms | 10.74 ms | 14.89 ms |
| `listNotes` | 10 | 6815 | 1.39 ms | 1.15 ms | 2.81 ms | 4.41 ms |
| `listNotes` | 50 | 7559 | 6.23 ms | 5.75 ms | 11.20 ms | 13.72 ms |
| `createNote` | 10 | 2911 | 3.32 ms | 2.47 ms | 6.93 ms | 14.57 ms |
| `createNote` | 50 | 3396 | 13.87 ms | 12.90 ms | 22.24 ms | 35.92 ms |
| `refreshToken` | 10 | 2225 | 4.35 ms | 3.44 ms | 7.99 ms | 16.57 ms |
| `refreshToken` | 50 | 2436 | 19.41 ms | 17.66 ms | 30.36 ms | 57.88 ms |
| `loginWithEmail` | 10 | 57 | 172 ms | 168 ms | 227 ms | 318 ms |
| `loginWithEmail` | 50 | 62 | 762 ms | 779 ms | 900 ms | 992 ms |

## 解读

- 读接口（`getMe`、`listNotes`）每个请求都做 EdDSA 验签、吊销键查询（Redis）与一次数据库查询（压测关闭了限流），10 VU 时 CPU 已接近 2 核上限，50 VU 时吞吐只增加约 20%，延迟随排队线性增长。
- `createNote` 在请求事务内写笔记与审计日志；`refreshToken` 在一个事务内锁会话行、写历史、写宽限期暂存，两者受 Postgres 往返限制。
- 登录受 argon2id（m = 19 MiB、t = 2）与 `PASSWORD_HASH_CONCURRENCY=2` 限制，约 60 次 / 秒，并发增加只会排队；需要更高的登录吞吐时调大 `PASSWORD_HASH_CONCURRENCY` 与 CPU（每个并发计算约占一核、19 MiB 内存）。
- 数字只用于同一机器上的前后对比；生产容量以目标环境实测为准。
