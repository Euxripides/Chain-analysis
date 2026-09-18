# F-R10-BUCKET 双读 helper 严格语义：单测与负向校准证据（轮次 20260913T122648Z）

- 范围：ADR-019 §2.1 读优先级与错误语义、§2.2 显式写桶；**尚未部署、尚未迁移、未触碰任何真实对象**。
- 主机：Windows 工作树（E:/Code/远程虚拟机/harness/blockchain-platform）；Go 1.23.2 windows/386 本地跑单测。
- 结论：`go build ./...` rc=0；`go test ./internal/api/... ./internal/config/... -count=1 -v` → **PASS=70 FAIL=0 SKIP=0**；
  其中本 helper 聚焦用例 **PASS=18 FAIL=0**；被改动文件 `gofmt -l` 为空。

## 1. 被审查对象（冻结 SHA-256）

| 文件 | SHA-256 |
| --- | --- |
| internal/api/export_object_store.go | `54efbd64e78af2d23f74310eac0edfddce26aa79e4748dbe4826ca9a53d90ad3` |
| internal/api/export_object_store_test.go | `49176eb18fbe78e3f823b36681449ed13833219b2972610e34413798df949b34` |
| internal/config/export_bucket_test.go | `20c1cf1289bcf33cc7110d864ba8822f5b43043470fd5f5e6bcc4086730c6f01` |
| internal/config/config.go | `2cb52212f1fc52f4b425fb6df061396593d4b51af3a344f6fe653a8adc6934ff` |
| internal/api/export.go | `db51d6cb15f2785756e910f5b2c3aec589c4f4797edfc34aa4bf03a43291d87c` |
| internal/api/export_v2.go | `4aa93530106279d531587e0877a5a8991ff1521392c58231dc24af43025b373f` |
| internal/api/sprint5.go | `cd20a1a32ddcdc7a4a5d08be3516a866e9f9e3f878e286064ecc01f84e2bf2a9` |
| internal/api/sprint5_exports.go | `68069dd97066a64162f2c101ca51b9664b52f424a83920b12de8fea3af915005` |

前置（被根指出两处语义缺陷的版本）冻结保留：
`.work/dsh-supervisor/r10v9-scratch/f-r10-bucket-prefix/export_object_store.prefix-404semantics.go`
SHA-256 `79d1f9b5dbf7d4f232df03cbb9559df6935dfeba04c5bf7aac84a5488b7c8c16`。

## 2. 根指出的两处缺陷与修法

1. **404 语义过宽**：旧实现把任意 HTTP 404 与 `NoSuchBucket` 都当成"对象缺失"可回退。
   实测依据（minio-go v7.0.90 `api-error-response.go:120-151`）：HEAD（StatObject）无响应体时，
   库按 `objectName != ""` **兜底编造 `NoSuchKey`**，因此"桶不存在"也会显示为对象级缺失。
   修法：只接受对象级错误码 `NoSuchKey`/`NoSuchObject`；判定"对象缺失"之前先用 `BucketExists`
   证明该桶确实存在；桶不存在 → `bucketMissingError` fail closed；`AccessDenied`/`Signature`/
   网络/5xx 一律立即失败，不回退、不降级。
2. **审计桶名字段不可核查**：旧 `auditFields()` 写逻辑别名 `export|raw`。
   修法：`object_bucket` = 真实桶名（如 `blockchain-exports`，与 MinIO inventory 逐字对应），
   另加 `object_bucket_role` = 逻辑角色。

## 3. 根追加要求的两项（本轮完成）

1. **运行期与 config.Validate 同严格度**：`parseExportReadOrder` 现在只接受恰好两个规范 token，
   raw/export 各一次，拒绝空白变体、未知项、重复项、缺任一桶（返回错误而非忽略）；
   `checkExportBuckets()` 在四个读写入口复核存储客户端可用、两个桶名非空且**不同名**。
2. **写后校验强度表述准确**：`putExportStream` 的回读只证明**对象存在且字节数一致（size-verified）**，
   不是 byte-exact；内容级一致由 `objectSHA256` 回读复算 SHA-256 写 PG / `export.completed` 审计链证明。
   迁移工具必须另做源 GET SHA、目标 GET SHA 与 PG SHA（有值的 204 条）逐字一致，禁止用 size/ETag 替代。

## 4. 用例清单（18/18 PASS）

| 用例 | 证明 |
| --- | --- |
| TestOpenExportObjectFallsBackWhenExportHasNoSuchKey | 首桶对象级缺失 → 回退 RAW 读成功 |
| TestOpenExportObjectPrefersExportBucket | 两桶都有时 EXPORT 优先 |
| TestOpenExportObjectDoesNotFallBackWhenExportBucketMissing | 首桶桶不存在（宽松 location 桩）→ fail closed 且为 bucketMissingError |
| TestOpenExportObjectDoesNotFallBackWhenExportBucketMissingStrictLocation | 真实 MinIO location 语义下同样 fail closed |
| TestOpenExportObjectDoesNotFallBackOnAccessDenied | AccessDenied → fail closed，不谎称缺失 |
| TestStatExportObjectDoesNotFallBackOnAccessDenied | stat 路径同上 |
| TestExportObjectMissingOnlyWhenBothBucketsLackTheKey | 两桶皆对象级缺失 → errExportObjectMissing |
| TestStatExportObjectFallsBackToRaw | stat 回退 |
| TestOpenExportObjectRangeFallsBackAndStreams | Range 回退 + 正确切片 |
| TestOpenExportObjectRangeFromExportBucketAndInvalidRange | Range 命中新桶；非法区间报 ErrInvalidRange 且不回退 |
| TestPutExportStreamHonoursWriteBucket | 写桶 raw/export 分别落对，且不重复写 |
| TestPutExportStreamRejectsInvalidWriteBucket | bogus/""/RAW/Export/" export"/"export " 全部 fail closed，且不产生任何对象 |
| TestExportReadOrderRejectsEveryInvalidVariant | 18 种非法读顺序（含根点名的 export,bogus、export,export、export）全部 fail closed |
| TestExportReadOrderAcceptsBothCanonicalOrders | export,raw 与 raw,export 均接受 |
| TestRuntimeRejectsRawAndExportBucketBeingIdentical | 运行期两桶同名 → 读/stat/写全部 fail closed |
| TestAuditFieldsUseRealBucketName | object_bucket 是真实桶名 + object_bucket_role |
| TestHelpersFailClosedWithoutStorageClient | nil 存储不 panic、返回错误 |
| TestIsExportObjectMissingDistinguishesBucketErrors | 哨兵语义与 ctx 取消区分 |

配置侧（internal/config）：非法/空写桶、非法读顺序（含 "export, raw"）、空桶名、RAW==EXPORT 全部拒绝；
默认值仍为 `EXPORT_WRITE_BUCKET=raw`、`EXPORT_READ_ORDER=export,raw`。

## 5. 保留的失败证据（未删改）

| # | 失败 | 现象 | 处置 |
| --- | --- | --- | --- |
| F1 | 测试桩缺 `Last-Modified` | `Last-Modified time format is invalid ... unable to parse` → Range/Stat 用例失败 | 桩补齐响应头（真实 MinIO 总会返回），**不是生产代码缺陷** |
| F2 | 桩未还原 aws-chunked 上传 | 5 字节对象 PUT 后回读 177 字节（`Content-Length: 177`，`X-Amz-Content-Sha256: STREAMING-AWS4-HMAC-SHA256-PAYLOAD`） | 桩实现 aws-chunked 解码；**同时暴露真实风险**：SDK 返回的 size 不等于对象字节数，故 `putExportStream` 改为从源头 `countReader` 计数 |
| F3 | 文档注释转义 | 注释里的 `\r\n` 被解释成真实换行 → `expected declaration, found '<'` | 改写注释，避免转义序列 |
| F4 | 编辑丢签名 | 替换注释块时误删 `func decodeAWSChunked` 签名 → `expected declaration, found out` | 恢复签名 |
| F5 | 写桶大小写宽松 | `EXPORT_WRITE_BUCKET=RAW` 曾被接受 | 改为规范字面量精确匹配（区分大小写、不容空白） |
| F6 | 读顺序运行期宽松 | `export,bogus` 曾被静默忽略后继续 | 运行期严格解析，非法即报错 |

## 6. 边界

- 本证据只覆盖 **helper/config 单测**；**尚未**部署 API、**尚未**迁移任何对象、**尚未**触碰
  `blockchain-raw` 的 270 个业务对象与 `blockchain-exports` 的 28 个既有 `audit-exports/*` 审计对象。
- 真实 MinIO 侧的错误码/桶语义校准、exportmigrate 负向校准与 2.75GB 迁移预估仍待执行。
