# 与 nestjs-template 的契约

本服务的 HTTP 契约以 nestjs-template `release/exam-prep` 分支提交 `02fdb4d` 为准（`openapi/SOURCE` 的 `commit`），校验元数据导出脚本 `scripts/export-validation.ts` 取自提交 `8b5f296`（`validation_script`），导出环境为 `NODE_ENV=development FILE_DRIVER=local`。契约变更先在 nestjs 落地，再同步到本仓库。

## 共享产物

以下文件在 fastapi-temp、springboot-template 与本仓库中同名、同路径、逐字节相同，只能通过 `make openapi-sync` 更新，不得手改：

| 产物 | 内容 | 本服务的用途 |
|---|---|---|
| `openapi/nestjs-baseline.json` | nestjs `/docs-json` 原始字节（OpenAPI 3.0.0） | `GET /docs-json` 原样返回；代码生成输入；运行时从中读取 `METHOD path → operationId`、路径参数类型与请求体 DTO |
| `openapi/validation.json` | nestjs class-validator / class-transformer 元数据 | `pkg/validatorx` 按它复刻 ValidationPipe |
| `openapi/nest_handlers.json` | operationId → nestjs `<Controller>-<handler>` | 限流键的哈希输入（`uploadFile` 按 `FILE_DRIVER` 取 Local / S3 / S3Presigned controller） |
| `openapi/SOURCE` | `repository`、`branch`、`commit`、`validation_script`、`environment` | 记录契约来源 |
| `db/nestjs-schema.sql` | nestjs 迁移后 18 张表的规范化结构快照，每个列 / 约束 / 索引 / 注释一行 | 迁移一致性测试；`DB_SCHEMA_OWNER=external` 时的列检查 |
| `tests/contract/fixtures/validation-cases.json` | 校验用例，期望结果由 nestjs 真实 ValidationPipe 跑出 | 契约测试门禁 |
| `scripts/openapi-sync.sh`、`scripts/schema_snapshot.py`、`scripts/gen_validation_cases.py`、`scripts/gen_nest_handlers.py`、`scripts/nestjs/` | 生成上述产物的工具（由 fastapi-temp 维护） | `OPENAPI_SYNC=run` 时调用 |

运行时需要的 `nestjs-baseline.json`、`validation.json`、`nest_handlers.json`、`nestjs-schema.sql` 都 `embed` 进二进制。

## 同步流程

```bash
make openapi-sync                    # 从 FASTAPI_DIR（默认 ../fastapi-temp）复制共享产物
make openapi-sync OPENAPI_SYNC=run   # 在 nestjs 固定提交上重新导出（需要 Docker、git、uv）
```

- 默认方式由 `scripts/copy-shared-artifacts.sh` 从 fastapi-temp 复制；fastapi-temp 中这些文件有未提交改动时拒绝复制
- `OPENAPI_SYNC=run` 执行 `scripts/openapi-sync.sh`：在 `NESTJS_DIR` 的 `NESTJS_REF` 上构建并启动 nestjs（compose 项目 `nestjs-openapi-sync-<仓库目录名>`），下载 `/docs-json`，导出 `validation.json`、限流 handler 映射、表结构快照，并在 nestjs builder 镜像中跑出校验用例的期望结果；产物须与 fastapi-temp 中的逐字节相同
- 契约测试 `TestSharedArtifactsMatchFastapiTemp` 比对本仓库与 fastapi-temp `HEAD` 中的共享产物，`TestSourceDeclaresContractOrigin` 检查 `SOURCE` 的五个键

同步之后：

1. `make generate`：基线变化会改变生成的类型与接口
2. `make test-contract`：`/docs-json` 字节、`scope.yaml` 注册、policy 与文档 `security` 交叉检查、`validation.json` 约束全部实现、校验用例全部通过
3. `make test-integration`：迁移结果与新的 `db/nestjs-schema.sql` 比对
4. `make test-parity`：对新提交的 nestjs 参照服务比对行为

## spec-first 代码生成

| 文件 | 作用 |
|---|---|
| `openapi/scope.yaml` | 本期已实现的 operationId（71 个）；`make generate` 只为它们生成处理器接口与路由注册 |
| `openapi/oapi-codegen.yaml` | 生成 `models`、`chi-server`、`strict-server`，`skip-prune: true`；`date-time` → `jsontime.Time`，`uuid` → `string`，`number` → `float64` |
| `openapi/overlay.yaml` | 只作用于生成输入：数字路径参数映射为 `int32`、去掉 `Accept-Language` 头参数、响应 schema 经 `x-go-type` 指向 `v1/view` 中的手写类型、部分请求体 DTO 映射为 `any`（处理器从 `validatorx` 校验后的 JSON 值读取字段）；不改变 `/docs-json` 输出 |
| `openapi/templates/strict-interface.tmpl` | 覆盖 strict 响应写出：成功一律 HTTP 200（仅 `uploadFile` 为 201），body 为 `{code: 200, message: "success", data}`，无内容时不输出 `data` |
| `tools/openapigen` | 读取 `oapi-codegen.yaml` 与 `scope.yaml` 后调用 oapi-codegen 库，输出 `internal/controller/http/gen/gen.go` |

新增一个已实现的接口：

1. 把 operationId 加进 `openapi/scope.yaml`（或用 `make module NAME=... TAG=...` 按 tag 批量加入并生成骨架，见 [cli.md](cli.md#模块生成器)）
2. `make generate`，在 `internal/controller/http/v1` 实现生成的方法，响应用 `v1/view` 中的类型
3. 在 `internal/controller/http/policy.go` 声明 policy；缺 policy、缺守卫或 DTO 不在 `validation.json` 中时启动失败
4. 补集成测试与 parity 用例

nestjs 文档的局限由实现侧补齐：可空字段未标 `nullable`、`@Expose({groups})` 分组不进文档，所以响应一律经手写 view 类型输出；鉴权方式（监控令牌、可选 JWT）、RBAC 权限、限流、事务不在文档中，写在 policy 中，契约测试只做与文档 `security` 的交叉检查。

## 校验

nestjs 文档不含 class-validator 约束。`pkg/validatorx` 读取 `openapi/validation.json`，按 nestjs 全局 `ValidationPipe` 的配置执行 class-transformer `plainToInstance`（含 `@Transform`、`@Type`、类字段初值）与 class-validator：同一字段的全部失败约束按装饰器顺序以 `", "` 拼接，字符串长度按 UTF-16 码元计，`isEmail`、`isUrl`、`isUUID`、`isISO8601` 等格式约束移植自 validator.js。文件中出现而未实现的约束或转换函数会让 `validatorx.New` 失败。门禁是 `TestValidationCasesMatchNestjsValidationPipe` 对 `validation-cases.json` 全部用例逐条比对。

## parity

parity 证明同一请求在两个服务上得到相同的响应：`make test-parity` 在 `openapi/SOURCE` 的提交上构建并启动 nestjs 参照服务（`scripts/nestjs/nestjs.compose.yaml`），以 compose 启动本服务，两侧使用同一份 seed 与 `THROTTLE_ENABLED=true`、`TRUST_PROXY=1`，然后运行 `tests/parity`；最后把本服务改为限流关闭，直接运行 nestjs 自己的 `test/user/auth.e2e-spec.ts`。用例与 fastapi-temp `tests/parity` 逐条对应，同一套用例对 nestjs 也必须通过。运行方式、变量与比对规则见 [testing.md](testing.md#parity)。

## 不在基线中的路由

| 路由 | 说明 |
|---|---|
| `GET /docs`、`GET /docs-json` | `SWAGGER_ENABLED` 控制，未设置时生产关闭 |
| `GET /metrics`（`METRICS_PATH`） | 监控令牌，不包信封 |
| `GET /api/v1/files/private/{id}` | JWT；local 驱动按 Express `res.sendFile` 输出，s3 驱动 302 到预签名 URL |
| `GET /api/v1/files/{path}` | 仅 local 驱动注册，公开文件下载 |

以上路由在 nestjs 中同样存在且不进文档（`Undocumented()`、契约测试白名单）。

`FILE_DRIVER=s3-presigned` 时，同一路径 `POST /api/v1/files/upload` 接收 `application/json` `FileUploadDto {fileName, fileSize}`、返回 `{file, uploadSignedUrl}`，与基线（local 驱动导出）中的 multipart 定义不同；nestjs 在该驱动下行为相同。

## 基线中未实现的接口（148 个）

请求这些接口返回 404 信封 `Cannot <METHOD> <originalUrl>`。前端仍可从 `/docs-json` 生成全部代码。

| 方法 | 路径 | operationId | tag |
|---|---|---|---|
| POST | `/api/v1/practice/sessions` | createPracticeSession | Practice |
| GET | `/api/v1/practice/sessions` | getPracticeSessions | Practice |
| GET | `/api/v1/practice/sessions/ongoing` | getOngoingPracticeSessions | Practice |
| GET | `/api/v1/practice/sessions/{id}` | getPracticeSession | Practice |
| GET | `/api/v1/practice/sessions/{id}/questions` | getPracticeSessionQuestions | Practice |
| PUT | `/api/v1/practice/sessions/{id}/answers` | savePracticeAnswers | Practice |
| POST | `/api/v1/practice/sessions/{id}/self-assessment` | selfAssessPracticeAnswer | Practice |
| POST | `/api/v1/practice/sessions/{id}/progress` | updatePracticeProgress | Practice |
| GET | `/api/v1/practice/sessions/{id}/report` | getPracticeSessionReport | Practice |
| POST | `/api/v1/practice/sessions/{id}/submit` | submitPracticeSession | Practice |
| POST | `/api/v1/practice/sessions/{id}/abandon` | abandonPracticeSession | Practice |
| GET | `/api/v1/questions/{id}` | getQuestion | Questions |
| GET | `/api/v1/exams` | getExams | Exams |
| GET | `/api/v1/exams/{id}/subjects` | getExamSubjects | Exams |
| GET | `/api/v1/me/exam-profiles` | getMyExamProfiles | Exam Profiles |
| PUT | `/api/v1/me/exam-profiles/{examId}` | upsertMyExamProfile | Exam Profiles |
| DELETE | `/api/v1/me/exam-profiles/{examId}` | deleteMyExamProfile | Exam Profiles |
| POST | `/api/v1/me/exam-profiles/{examId}/current` | setMyCurrentExam | Exam Profiles |
| GET | `/api/v1/admin/exams` | adminGetExams | Admin Exams |
| POST | `/api/v1/admin/exams` | adminCreateExam | Admin Exams |
| GET | `/api/v1/admin/exams/{id}` | adminGetExam | Admin Exams |
| PATCH | `/api/v1/admin/exams/{id}` | adminUpdateExam | Admin Exams |
| DELETE | `/api/v1/admin/exams/{id}` | adminDeleteExam | Admin Exams |
| POST | `/api/v1/admin/exams/{id}/publish` | adminPublishExam | Admin Exams |
| POST | `/api/v1/admin/exams/{id}/offline` | adminOfflineExam | Admin Exams |
| GET | `/api/v1/admin/subjects` | adminGetSubjects | Admin Subjects |
| POST | `/api/v1/admin/subjects` | adminCreateSubject | Admin Subjects |
| GET | `/api/v1/admin/subjects/{id}` | adminGetSubject | Admin Subjects |
| PATCH | `/api/v1/admin/subjects/{id}` | adminUpdateSubject | Admin Subjects |
| DELETE | `/api/v1/admin/subjects/{id}` | adminDeleteSubject | Admin Subjects |
| POST | `/api/v1/admin/subjects/{id}/publish` | adminPublishSubject | Admin Subjects |
| POST | `/api/v1/admin/subjects/{id}/offline` | adminOfflineSubject | Admin Subjects |
| GET | `/api/v1/admin/subjects/{id}/knowledge-points` | adminGetSubjectKnowledgePoints | Admin Subjects |
| POST | `/api/v1/admin/knowledge-points` | adminCreateKnowledgePoint | Admin Knowledge Points |
| PATCH | `/api/v1/admin/knowledge-points/{id}` | adminUpdateKnowledgePoint | Admin Knowledge Points |
| DELETE | `/api/v1/admin/knowledge-points/{id}` | adminDeleteKnowledgePoint | Admin Knowledge Points |
| POST | `/api/v1/admin/knowledge-points/{id}/move` | adminMoveKnowledgePoint | Admin Knowledge Points |
| POST | `/api/v1/admin/knowledge-points/{id}/migrate-questions` | adminMigrateKnowledgePointQuestions | Admin Knowledge Points |
| GET | `/api/v1/admin/questions` | adminGetQuestions | Admin Questions |
| POST | `/api/v1/admin/questions` | adminCreateQuestion | Admin Questions |
| POST | `/api/v1/admin/questions/batch-publish` | adminBatchPublishQuestions | Admin Questions |
| POST | `/api/v1/admin/questions/batch-submit` | adminBatchSubmitQuestions | Admin Questions |
| POST | `/api/v1/admin/questions/batch-reject` | adminBatchRejectQuestions | Admin Questions |
| POST | `/api/v1/admin/questions/batch-offline` | adminBatchOfflineQuestions | Admin Questions |
| POST | `/api/v1/admin/questions/batch-delete` | adminBatchDeleteQuestions | Admin Questions |
| PATCH | `/api/v1/admin/questions/batch-update` | adminBatchUpdateQuestions | Admin Questions |
| GET | `/api/v1/admin/questions/{id}` | adminGetQuestion | Admin Questions |
| PATCH | `/api/v1/admin/questions/{id}` | adminUpdateQuestion | Admin Questions |
| DELETE | `/api/v1/admin/questions/{id}` | adminDeleteQuestion | Admin Questions |
| GET | `/api/v1/admin/questions/{id}/versions` | adminGetQuestionVersions | Admin Questions |
| POST | `/api/v1/admin/questions/{id}/submit` | adminSubmitQuestion | Admin Questions |
| POST | `/api/v1/admin/questions/{id}/publish` | adminPublishQuestion | Admin Questions |
| POST | `/api/v1/admin/questions/{id}/reject` | adminRejectQuestion | Admin Questions |
| POST | `/api/v1/admin/questions/{id}/offline` | adminOfflineQuestion | Admin Questions |
| GET | `/api/v1/subjects/{id}/papers` | getSubjectPapers | Papers |
| GET | `/api/v1/papers/{id}` | getPaper | Papers |
| GET | `/api/v1/subjects/{id}/paper-rules` | getSubjectPaperRules | Papers |
| POST | `/api/v1/paper-rules/{id}/generate` | generatePaper | Papers |
| GET | `/api/v1/subjects/{id}/daily` | getSubjectDaily | Papers |
| GET | `/api/v1/subjects/{id}/daily/trial` | getSubjectDailyTrial | Papers |
| POST | `/api/v1/subjects/{id}/daily/trial/check` | checkSubjectDailyTrial | Papers |
| GET | `/api/v1/admin/papers` | adminGetPapers | Admin Papers |
| POST | `/api/v1/admin/papers` | adminCreatePaper | Admin Papers |
| GET | `/api/v1/admin/papers/{id}` | adminGetPaper | Admin Papers |
| PATCH | `/api/v1/admin/papers/{id}` | adminUpdatePaper | Admin Papers |
| DELETE | `/api/v1/admin/papers/{id}` | adminDeletePaper | Admin Papers |
| PUT | `/api/v1/admin/papers/{id}/questions` | adminSetPaperQuestions | Admin Papers |
| POST | `/api/v1/admin/papers/{id}/publish` | adminPublishPaper | Admin Papers |
| POST | `/api/v1/admin/papers/{id}/offline` | adminOfflinePaper | Admin Papers |
| POST | `/api/v1/admin/papers/{id}/copy` | adminCopyPaper | Admin Papers |
| GET | `/api/v1/admin/paper-rules` | adminGetPaperRules | Admin Paper Rules |
| POST | `/api/v1/admin/paper-rules` | adminCreatePaperRule | Admin Paper Rules |
| GET | `/api/v1/admin/paper-rules/{id}` | adminGetPaperRule | Admin Paper Rules |
| PATCH | `/api/v1/admin/paper-rules/{id}` | adminUpdatePaperRule | Admin Paper Rules |
| DELETE | `/api/v1/admin/paper-rules/{id}` | adminDeletePaperRule | Admin Paper Rules |
| POST | `/api/v1/admin/paper-rules/{id}/preview` | adminPreviewPaperRule | Admin Paper Rules |
| GET | `/api/v1/admin/subjects/{id}/daily-sets` | adminGetDailyQuestionSets | Admin Daily Question Sets |
| POST | `/api/v1/admin/subjects/{id}/daily-sets/{date}/regenerate` | adminRegenerateDailyQuestionSet | Admin Daily Question Sets |
| POST | `/api/v1/orders` | createOrder | Orders |
| GET | `/api/v1/orders` | getOrders | Orders |
| GET | `/api/v1/orders/{id}` | getOrder | Orders |
| DELETE | `/api/v1/orders/{id}` | cancelOrder | Orders |
| POST | `/api/v1/wechat/payment/jsapi` | WechatPayController_createJsapiPayment_v1 | Wechat Pay |
| POST | `/api/v1/wechat/payment/app` | WechatPayController_createAppPayment_v1 | Wechat Pay |
| GET | `/api/v1/wechat/order/{outTradeNo}` | WechatPayController_queryOrder_v1 | Wechat Pay |
| POST | `/api/v1/wechat/refund` | WechatPayController_refund_v1 | Wechat Pay |
| POST | `/api/v1/wechat/notify` | WechatPayController_notify_v1 | Wechat Pay |
| POST | `/api/v1/alipay/payment/mobile` | AlipayController_createMobilePayment_v1 | Alipay |
| POST | `/api/v1/alipay/payment/mini` | AlipayController_createMiniPayment_v1 | Alipay |
| POST | `/api/v1/alipay/payment/web` | AlipayController_createWebPayment_v1 | Alipay |
| GET | `/api/v1/alipay/order/{outTradeNo}` | AlipayController_queryOrder_v1 | Alipay |
| POST | `/api/v1/alipay/refund` | AlipayController_refund_v1 | Alipay |
| POST | `/api/v1/alipay/order/close/{outTradeNo}` | AlipayController_closeOrder_v1 | Alipay |
| POST | `/api/v1/alipay/notify` | AlipayController_notify_v1 | Alipay |
| POST | `/api/v1/shares` | createShare | Shares |
| GET | `/api/v1/shares` | getShares | Shares |
| GET | `/api/v1/shares/code/{code}` | getShareByCode | Shares |
| GET | `/api/v1/shares/{id}` | getShareById | Shares |
| DELETE | `/api/v1/shares/{id}` | deleteShare | Shares |
| GET | `/api/v1/shares/{id}/stats` | getShareStats | Shares |
| POST | `/api/v1/shares/code/{code}/track` | trackShare | Shares |
| POST | `/api/v1/shares/wechat/config` | getWeChatConfig | Shares |
| POST | `/api/v1/shares/alipay/config` | getAlipayConfig | Shares |
| GET | `/api/v1/admin/monitor/queues` | getMonitorQueues | Monitor |
| GET | `/api/v1/admin/monitor/alerts` | getMonitorAlerts | Monitor |
| POST | `/api/v1/admin/monitor/alerts/{id}/ack` | acknowledgeMonitorAlert | Monitor |
| POST | `/api/v1/admin/imports` | adminCreateImport | Admin Imports |
| GET | `/api/v1/admin/imports` | adminGetImports | Admin Imports |
| POST | `/api/v1/admin/imports/precheck` | adminPrecheckImport | Admin Imports |
| GET | `/api/v1/admin/imports/{id}` | adminGetImport | Admin Imports |
| GET | `/api/v1/admin/imports/{id}/errors.json` | adminGetImportErrors | Admin Imports |
| GET | `/api/v1/admin/imports/{id}/errors` | adminDownloadImportErrors | Admin Imports |
| GET | `/api/v1/wrong-book` | getWrongBook | Wrong Book |
| GET | `/api/v1/wrong-book/summary` | getWrongBookSummary | Wrong Book |
| POST | `/api/v1/wrong-book/{questionId}/master` | masterWrongQuestion | Wrong Book |
| POST | `/api/v1/wrong-book/{questionId}/restore` | restoreWrongQuestion | Wrong Book |
| GET | `/api/v1/me/study/overview` | getStudyOverview | Study Stats |
| GET | `/api/v1/me/study/daily` | getStudyDaily | Study Stats |
| GET | `/api/v1/me/study/knowledge` | getStudyKnowledge | Study Stats |
| GET | `/api/v1/me/study/weak-points` | getStudyWeakPoints | Study Stats |
| GET | `/api/v1/me/checkins` | getCheckins | Study Stats |
| GET | `/api/v1/subjects/{id}/knowledge-points` | getSubjectKnowledgePoints | Exams |
| PUT | `/api/v1/questions/{id}/note` | saveQuestionNote | Question Notes |
| DELETE | `/api/v1/questions/{id}/note` | deleteQuestionNote | Question Notes |
| GET | `/api/v1/me/question-notes` | getMyQuestionNotes | Question Notes |
| POST | `/api/v1/admin/questions/{id}/errata` | adminCreateErrata | Admin Errata |
| GET | `/api/v1/admin/errata` | adminGetErrataList | Admin Errata |
| GET | `/api/v1/admin/errata/{id}` | adminGetErrata | Admin Errata |
| POST | `/api/v1/admin/errata/{id}/retry` | adminRetryErrata | Admin Errata |
| POST | `/api/v1/questions/{id}/reports` | createQuestionReport | Question Reports |
| GET | `/api/v1/me/question-reports` | getMyQuestionReports | Question Reports |
| GET | `/api/v1/admin/question-reports` | adminGetQuestionReports | Admin Question Reports |
| GET | `/api/v1/admin/question-reports/{id}` | adminGetQuestionReport | Admin Question Reports |
| PATCH | `/api/v1/admin/question-reports/{id}` | adminUpdateQuestionReport | Admin Question Reports |
| POST | `/api/v1/ai/conversations` | createAiConversation | AI |
| GET | `/api/v1/ai/conversations` | getAiConversations | AI |
| GET | `/api/v1/ai/conversations/{id}/messages` | getAiMessages | AI |
| POST | `/api/v1/ai/conversations/{id}/messages` | sendAiMessage | AI |
| DELETE | `/api/v1/ai/conversations/{id}` | deleteAiConversation | AI |
| GET | `/api/v1/ai/messages/{id}` | getAiMessage | AI |
| GET | `/api/v1/ai/messages/{id}/events` | getAiMessageEvents | AI |
| POST | `/api/v1/ai/messages/{id}/stop` | stopAiMessage | AI |
| POST | `/api/v1/ai/messages/{id}/feedback` | setAiMessageFeedback | AI |
| POST | `/api/v1/ai/messages/{id}/report` | reportAiMessage | AI |
| GET | `/api/v1/ai/quota` | getAiQuota | AI |
| GET | `/api/v1/admin/ai/usage` | adminGetAiUsage | Admin AI |
| POST | `/api/v1/game/auth/login` | gameLogin | Game Auth |
| POST | `/api/v1/game/auth/refresh` | gameRefreshToken | Game Auth |

## 与 nestjs 的行为差异

### 有意与 nestjs 保持一致的实现细节

- 反馈列表 `GET /api/v1/feedbacks` 的 `total` 按 TypeORM `findAndCount` 的方式计算：本页未取满、且不是「首页之后的空页」时 `total = skip + 本页行数`，不执行 count 查询；否则执行 count（`internal/usecase/feedback/feedback.go`、`pkg/pagination.LazyTotal`）
- 分页排序中出现不存在的字段时返回 500（TypeORM `EntityPropertyNotFoundError` 经异常过滤器输出为未知错误，`internal/repo/postgres/users.go` 的 `ErrUnknownSortProperty`）
- 健康检查 503 时 body 只有 `{code: 503, message: "SERVICE_UNAVAILABLE", data: null}`（terminus 结果被异常过滤器丢弃）
- `METRICS_ENABLED=false` 只关闭 HTTP 指标采集与运行时默认指标，`/metrics` 路由仍存在

### 实现方式不同（不改变契约）

| 项 | nestjs | 本服务 |
|---|---|---|
| 密码哈希 | bcrypt(md5)，cost 10 | 默认 argon2id(md5)（`PASSWORD_HASHER`），登录时识别 bcrypt 并按当前设置重哈希；共库时设 `PASSWORD_HASHER=bcrypt` |
| 限流算法 | 固定窗口（`throttle:<sha>:default` INCR + 封禁键） | GCRA，状态键 `throttle:<sha>:default:gcra`（哈希输入相同）；头名与出现条件一致，`X-RateLimit-Remaining` / `X-RateLimit-Reset` / `Retry-After` 的数值不同；共用 Redis 时两边各自计数 |
| 限流存储故障 | — | Redis 命令超时 500ms，出错时回落进程内计数（最多 10000 个键，按进程独立），计 `throttler_storage_fallback_total` 并告警 |
| 数据库迁移 | 应用启动时执行 | 一次性 `app migrate`，compose 中 api / worker 依赖其成功完成 |
| 事务 | 服务内手动管理，无请求级事务 | policy `Tx` 的接口在请求事务内执行，4xx 提交、5xx 与 SQL 错误回滚（见 [database.md](database.md#事务)）；返回 4xx 前已执行的写入与 nestjs 一样保留 |
| 队列 | BullMQ（Redis）；`REDIS_ENABLED=false` 时进程内执行 | River（Postgres，`river` schema），与 `REDIS_ENABLED` 无关，始终需要 worker 进程；与 BullMQ 不互通，共库时两边各自处理自己的任务 |
| 周期任务互斥 | `lock:scheduler:<name>` | River leader 选举，不使用锁键 |
| 账号清理并发 | jobId `user-<id>` 去重 | River `UniqueOpts`（按参数，未完成状态）去重；执行时另持 `lock:account-cleanup:user-{id}`，被占用时 30 秒后重试 |
| 配置校验 | 各模块运行时校验 | 启动时一次性解析并校验全部变量，列出全部问题后退出 |
| 优雅停机 | Nest 默认 | 就绪探针先返回 503，等待 `SHUTDOWN_DRAIN_PERIOD`，再在 `SHUTDOWN_TIMEOUT` 内等待在途请求与任务 |
| 可观测性 | pino 日志、Prometheus | 另有 OpenTelemetry（`OTEL_ENABLED`）与 Sentry（`SENTRY_DSN`），日志带 trace_id / span_id；均不属于契约 |
| 指标采集范围 | 只记录 HTTP、告警、限流回落三组，其余只注册 | 全部记录：`database_query_duration_seconds` 由 pgx tracer 记录，`cache_hits_total` / `cache_misses_total` 由缓存记录 |

### 行为差异

**请求与响应**

- Express 对 JSON 响应默认生成弱 `ETag`，请求带匹配的 `If-None-Match` 时返回 304 空体；本服务的 JSON 响应不带 `ETag`，也不返回 304（不纳入契约，parity 不发送 `If-None-Match`）。local 驱动的文件读取与下载（`GET /api/v1/files/private/{id}`、`GET /api/v1/files/{path}`）按 send 1.2.1、fresh 2.0.0、range-parser 1.3.0 移植：弱 `ETag` `W/"<size hex>-<mtime ms hex>"`、`Last-Modified`、304 / 412 条件请求、`Range` 206 / 416（`internal/controller/http/v1/send.go`）；与 Node 的差别：`If-Modified-Since`、`If-Unmodified-Since` 与日期形式的 `If-Range` 只接受 HTTP 日期的三种格式（`http.ParseTime`），Node 的 `Date.parse` 还接受 ISO 日期与 `UTC`、`+0000` 等时区写法；请求头按 UTF-8 解码，Node 按 latin1，`Range` 中含非 ASCII 空白时去除空白的结果可能不同
- `API_PREFIX` 与排除路由之外的未知路径（如 `/nope`、`POST /docs-json`）nestjs 返回 Express 默认 HTML 404；本服务统一返回 JSON 404 信封
- nestjs 另接受 `application/x-www-form-urlencoded` 请求体；本服务只解析 `application/json`，其它 content-type 的请求体按空对象校验
- `METRICS_PATH` 非默认值时 nestjs 的指标路由落到 `/api/<path>`（`setGlobalPrefix` 只排除 `metrics`）；本服务一律为 `/<METRICS_PATH>`，不带前缀
- `GET /api/health` 的 `node` 为 Go 运行时版本（如 `go1.27.4`），`memory` 取 Go 运行时内存；`/api/v1/health` 的 `memory_heap` 比较 Go 运行时持有且未归还系统的内存（nestjs 为 V8 `heapUsed`），阈值同为 300MB
- `APP_NAME` 未设置时指标的 `app` 标签为 `app`（nestjs 为 `nestjs-app`）

**认证与外部集成**

- 微信登录、微信手机号登录、支付宝登录与 `sendSmsCode` 不在请求事务内执行（policy 未声明 `Tx`），等待外部 HTTP 调用期间不持有数据库连接，其中的写入各自提交（`internal/controller/http/policy.go`）
- 短信、微信、支付宝的出站请求不接入 OpenTelemetry 链路追踪，因为请求 URL 中带有密钥、验证码与手机号（`internal/app/integrations.go` 的 `outbound`）
- 这些出站请求的网络错误（连接失败、超时等）写入日志与通知 `sentChannels` 的文本为 Go 的错误文本，其中请求 URL 只保留 scheme、host 与 path（`internal/repo/webapi/outbound.Redact`）；nestjs 为 axios 的 `error.message`
- `SMS_MOCK_MODE=true` 时的日志为结构化记录：消息 `[MOCK SMS]`，字段 `to`（脱敏手机号）与 `params`（模板参数 JSON）；nestjs 为单行文本 `[MOCK SMS] To: …, Params: …`（`internal/repo/webapi/sms/aliyun.go`）

**邮件与队列**

- 注册确认、确认新邮箱、找回密码邮件在请求事务内入 `mail` 队列，提交后由 worker 按请求语言渲染并发送，SMTP 失败不影响接口响应；nestjs 在请求内同步发送，SMTP 失败时接口返回 500
- `MAIL_HOST`、`MAIL_DEFAULT_EMAIL`、`MAIL_DEFAULT_NAME` 只在生产环境必填，非生产未设置 `MAIL_HOST` 时邮件只写日志；nestjs 在任何环境都要求这三项
- notification 队列的「每秒 10 个任务」在每个 worker 进程内各自计算（`golang.org/x/time/rate`），启用多个 worker 进程时总速率为 10 × 进程数；nestjs BullMQ limiter 为跨 worker 的全局计数
- worker 进程中记录的 `queue_jobs_total`、`admin_alerts_total`（账号清理失败告警等）在 worker 进程自己的注册表中，worker 不提供 HTTP 端点，API 的 `/metrics` 中没有这些样本；nestjs 的队列处理器与 HTTP 在同一进程，样本出现在 `/metrics`

**数据**

- 收货地址的省市区校验直接查询 `region` 表（`internal/usecase/address/address.go`、`internal/repo/postgres/addresses.go` 的 `Regions`），不经过行政区划缓存；nestjs 经 `RegionsService` 读取（带缓存），`region` 表变更后本服务立即生效
- S3 驱动上传对象的 `ContentType` 取用途表中按扩展名确定的 mime；nestjs multer-s3 用 `AUTO_CONTENT_TYPE` 按文件内容嗅探。只影响存储对象的元数据
- 本期没有 questions / papers 模块：收藏（新增、切换、检查）目标为题目时返回 `questionNotAccessible`，为试卷时返回 `paperNotFound`；收藏列表中这两类收藏的 `targetOffline` 恒为 `true`。与 nestjs 共库时，nestjs 已创建的题目 / 试卷收藏在本服务中显示为已下架

**配置**

- 环境变量为空字符串时与未设置等同，取默认值或视为未配置；nestjs 的 class-validator 把空字符串当作已设置的值，`WECHAT_SUBSCRIBE_ORDER_PAID_FIELDS=`、`WECHAT_MINI_PROGRAM_STATE=` 会让 nestjs 启动失败，本服务分别按未配置模板字段、`formal` 处理
- 进程只读环境变量，不读 `.env` 文件（Make 目标在调用前加载 `.env`）
- seed 以 `APP_ENV=production` 判定生产（nestjs 为 `ENV_MODE=prod`）
- 本服务独有的变量：`APP_ENV`（对应 nestjs `NODE_ENV`）、`PASSWORD_HASHER`、`PASSWORD_HASH_CONCURRENCY`、`WORKER_DATABASE_MAX_CONNECTIONS`、`DB_SCHEMA_OWNER`、`OTEL_ENABLED`、`OTEL_EXPORTER_OTLP_ENDPOINT`、`SENTRY_DSN`、`SHUTDOWN_TIMEOUT`，以及：

| 变量 | 默认 | 说明 |
|---|---|---|
| `FILE_LOCAL_ROOT` | `files` | local 驱动的存储目录（相对工作目录）；nestjs 写死 `./files`，本变量供测试与部署挂载卷使用 |
| `SHUTDOWN_DRAIN_PERIOD` | `0s` | 收到 SIGTERM 后就绪探针先返回 503 的时长，之后才停止接收请求；部署在负载均衡后时设为不小于探针周期 |

### 范围与决定

- `/docs-json` 只原样输出 nestjs 完整文档，不提供按 `scope.yaml` 过滤的输出（不设 `DOCS_SCOPE`）
- parity 用例不抽取为跨语言数据文件，按 fastapi-temp `tests/parity` 的用例清单逐条移植为 Go 代码
- Express 的弱 `ETag` 与 304 不纳入 JSON 响应的契约（见上）
- 考试刷题业务（exams、questions、practice、papers、imports、errata、wrong-book、study-stats、question-notes、ai、game-auth、monitor）与 orders、payments、shares 本期不实现，接口见上表
