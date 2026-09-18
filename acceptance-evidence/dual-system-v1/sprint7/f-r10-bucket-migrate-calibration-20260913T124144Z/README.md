# F-R10-BUCKET 迁移工具：单测 + 隔离负向校准 + 真实 dry-run 与迁移预估（20260913T124144Z）

- 范围：ADR-019 §2.3 的 `cmd/exportmigrate` 与 0058 账本；**尚未部署 writer 切换、尚未执行任何复制**。
- 运行身份：VM `/home/euripides/projects/blockchain-platform/backend`，Go 1.26.7 linux/amd64 本机构建。
- 结论：`go build ./... rc=0`、`go vet rc=0`；VM 侧 `go test ./internal/api/... ./internal/config/... ./internal/exportmigrate/...` 全 ok；
  本地 `PASS=84 FAIL=0`（含 16 个 helper 用例 + 15 个 engine 用例 + config 用例）。
  真实 270 对象 dry-run：**failures=0、total_bytes=2,749,993,808、pg_sha_present=204 / empty=66**，与根权威口径逐字一致。

## 1. 冻结件与身份

| 产物 | SHA-256 |
| --- | --- |
| VM `backend/bin/exportmigrate.next`（隔离校准用） | `0d258818002b4dbad7dc22cc2b32d52f30f4f0b4691e23156b847d3a173eae1a` |
| VM `backend/bin/api.next`（双读版，**未部署**） | `2c632d9944890bfad4034ac091215b2fcb9c332754757348637effb39f42ebfd` |
| VM `backend/bin/worker.next`（**未部署**） | `e501b7e9d4d72677bb43c4da8c209c6b80b0a472168c6137cffac7a7e98be4eb` |
| VM `backend/bin/migrate.next`（含 0058，**未执行**） | `59a6a2482e9d60f3a0a71247805676a64e8d9e4551a76b0b32307a92f3aaa1e7` |
| VM `internal/exportmigrate/engine.go` | `997eabf952b4883169f99c8f8ddb0dc333fd2b4cddde0e00e1f0f2db5c6340e9` |
| VM `cmd/exportmigrate/main.go` | `f11108d6b2b27f5d5a8918be499b9a3ce4bca20a6ca2b814bd60cf9f343f78f4` |
| 现网运行中 API（未变） | `484e2707e6574cce10e41e68047176ff5255aa231d1615c4b10cddd3fe5db2e4` |
| 现网运行中 Worker（未变） | `513e67bd7e9d4a9f96aeb659a237ca2399e7fc2074fdfe0978d0f6d8e6dbdb48` |

## 2. 真实 dry-run（270 对象，只读，零副作用）

命令：`exportmigrate -mode migrate -from-bucket blockchain-raw -to-bucket blockchain-exports -ledger jsonl -report dryrun.json`（**无 -execute**）

| 指标 | 实测 |
| --- | --- |
| 处理对象 | **270**（unique refs 270） |
| 计划动作 | `planned_copy` 270，状态 `PENDING` 270 |
| PG sha 有值 / 为空 | **204 / 66**（与根权威 121059Z inventory 逐字一致） |
| 源 GET SHA-256 缺失 | **0**（270 个对象全部算出） |
| 总字节 | **2,749,993,808** |
| failures | **0** → 204 条有 PG sha 的对象源 GET SHA 与 `exports.sha256` **全部一致**，66 条缺 sha 的已计算出并保留 source SHA |
| 账本文件 | **未创建**（dry-run 零副作用实测证明） |
| 耗时 | 19:41:44.869 → 19:41:52.502 = **7.633 s**（含 PG 查询 + 270 次目标 stat + 2.75GB 源读取与 SHA-256） |

注：`exports/691f6a0d-368e-40a6-9f50-dc3743576772.csv` 为 **0 字节**对象（SHA e3b0c442…，即空串哈希）。既非缺陷也非阻塞，但迁移与后续验收不得把它当成"缺失"或"失败"。

## 3. 隔离负向校准（真实 MinIO，专用 scratch 桶，未触碰任何业务对象）

夹具桶：`negcal-src` / `negcal-dst`（由校准脚本创建）。业务桶 `blockchain-raw`/`blockchain-exports` 全程只读。

| 用例 | 夹具 | 实测结果 | 判定 |
| --- | --- | --- | --- |
| dry-run 零副作用（全部 5 个分支） | 负向夹具 | rc=2；`ledger_after_dryrun=no`；目标桶无新对象 | PASS |
| 正常复制 | clean 夹具 12 对象 | `copied` 7 / `skipped_verified` 5，全部 `VERIFIED` | PASS |
| 缺 PG sha | `nosha.csv`（pg_sha256=""） | 复制成功；账本记录 source=destination SHA，未回填 PG | PASS |
| 目标同 key 不同内容 | `conflict.csv` | `CONFLICT` + fail closed；目标对象 SHA 前后一致（`6d250551…`）**未被覆盖**；源对象保留 | PASS |
| 目标同 key 相同内容 | `same.csv` | `ALREADY_VERIFIED`，未重复复制 | PASS |
| PG sha 与源字节不符 | `badsha.csv`（pg_sha256=全 0） | `FAILED`，**未复制**（目标不存在）；错误信息含双方 SHA | PASS |
| 跨桶复制不删源 | 全部 | `negcal-src` 对象仍存在（size/etag 不变） | PASS |
| 续跑（checkpoint） | clean 12 对象 | 先 `-limit 5` → 5 VERIFIED；再全量 → `copied 7 + skipped_verified 5`；`verify` → 12/12 verified，drift 0 | PASS |

负向夹具本轮出现 `ALREADY_VERIFIED`（ok/nosha/same）是因为前一轮校准（negcal2）已把同名对象复制进同一 scratch 目标桶；这本身正是"绝不覆盖、按字节判定"的独立证据。

## 4. 本轮校准发现的真实缺陷（全部已修，失败轮保留）

| # | 缺陷 | 证据 | 修法 |
| --- | --- | --- | --- |
| N1 | `Item`/Row 无 JSON tag，`-items-file` 反序列化静默得到空对象 | `ERROR load items file: items[0] must have export_id and object_ref`（轮次 123802Z） | 加 snake_case tag；并保留该失败轮 |
| N2 | **dry-run 在 CONFLICT/ALREADY_VERIFIED 分支仍写账本** | 轮次 123844Z：`ledger_after_dryrun=yes`、`by_action {already_verified:1, conflict:1}` | `commit()` 增加 `execute` 门；dry-run 一律不写；新增单测 `TestDryRunWritesNothingOnEveryBranch` |
| N3 | `-ledger=jsonl` + PG 物品来源时 `pool` 为 nil → **panic** | 轮次 124012Z：`panic: invalid memory address`（main.go:149） | 按模式决定是否建连接 + nil 守卫 |
| N4 | 工具用 `store.New`（owner），与运行期降权口径不符；psql 实测 `permission denied for table exports` | 124012Z psql 输出 | 改用 `store.NewWithRuntimeRole(DATABASE_URL, DB_RUNTIME_ROLE)`（ADR-017 §5） |
| N5 | 远程 source 树被 `cat <empty> \| base64 -d > target` 截空 9 个文件 | 129 个字节 0 的文件 | 改为"先解码到临时件 → SHA 核对 → 才 install"；19 个文件全部 MATCH，已修复 |

传输层结论（供后续会话复用）：
- `sftp_upload` 本会话不可用（工具要求本地路径在 `/workspace` 下，工作区是 Windows 非 ASCII 路径）；
- **可靠通道**：本地 base64 分块 → `SSHManager exec` 的 `printf '%s' '<chunk>' >> file`（12–16KB/块）→ 远端 `base64 -d` → **SHA 核对后才 install**；
- 早期"parts 全为空"是我的 PowerShell 格式串错误（`"{0:d3}"` 字面量），**不是** `sftp_write_text` 的缺陷；小载荷 `sftp_write_text` 实测正常（27 字节往返一致）。

## 5. 2.75GB 实际迁移预估（基于实测，可续跑）

| 项 | 预估/实测 |
| --- | --- |
| 待复制 | 270 对象 / **2,749,993,808 B（2.56 GiB）** |
| 单次源读+SHA 实测 | **7.63 s**（2.75GB → 约 360 MB/s 有效吞吐） |
| 复制阶段新增 I/O | 写入 2.75GB + 目标回读复算 2.75GB ≈ 源读的 2 倍 |
| 预计总耗时（乐观/保守） | **约 25 s / 60–240 s**（含 multipart 与哈希开销；不与现网导出并发时取乐观侧） |
| 目标桶新增占用 | **+2,749,993,808 B**；MinIO 所在卷当前可用 56 GB，充足 |
| 稳态占用 | raw 与 export 各 2.56 GiB（**不删除 raw**，ADR-019 §2.3） |
| 续跑方案 | 账本即 checkpoint：每对象 `PENDING→COPIED→VERIFIED`；中断后重跑只处理非 VERIFIED 行；`-limit N` 可分片试点 |
| 幂等 | 主键 `(export_id, to_bucket)`：重复运行不产生重复行；已 VERIFIED 对象直接 `skipped_verified` |
| 校验强度 | 源 GET SHA（全部 270）→ 复制 → 目标 GET SHA 逐字节比对；有 PG sha 的 204 条另与 `exports.sha256` 交叉校验；**不用 size/ETag 作为字节证明** |

## 6. 边界与未完成

- **尚未部署**：`api.next`/`worker.next` 仍只是文件，现网 API/Worker 仍是 `484e2707…`/`513e67bd…`；`EXPORT_WRITE_BUCKET` 未切换。
- **尚未应用 0058**：`migrate.next` 已构建但未执行；`export_object_migrations` 表尚不存在。
- **尚未复制任何业务对象**：`blockchain-exports` 仍只有 28 个既有 `audit-exports/*`（未触碰）。
- 待办顺序：应用 0058 → `-mode migrate -execute`（真实 270 对象）→ `-mode verify` → 部署双读版（写桶仍 raw）→ 四语义真实验证 → 受控切写 → 新导出只入新桶 → 回滚验证。
