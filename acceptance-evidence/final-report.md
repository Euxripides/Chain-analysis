# 链上分析平台真实数据验收结果

## 1. 结论

- 结论：**正式通过，准予放行**
- 初始测试 commit：`62a6d98`
- 修复提交：`b0b1e79` + `7b276e4` + `bb719e2`；VM 运行代码基线：`bb719e203a3779703c7fb3100b23a5014eeb95a1`。本报告属于后续纯证据提交，不改变运行代码基线
- 测试时间：2026-09-02 20:48～23:50（UTC+7）；放行复核：2026-09-03 01:11（UTC+8）
- ANKR_MODE：`live`，验收结论未使用 Mock 数据
- 执行人/Agent：Codex；授权来源：当前工作区用户于 2026-09-03 下达“你来操作”的放行执行指令
- 原始发现：P0=0、P1=2、P2=5、P3=9
- 放行时未关闭项：P0=0、P1=0、P2=5、P3=9；两项 P1 均已修复、部署并通过真实数据复验
- 72 个用例原始执行结果：60 通过、9 失败、0 阻塞、3 未执行；另完成放行门槛定向复验 7/7 通过

P2/P3 作为已知技术债接受放行风险，不改变其 Bug 状态，不视为已经修复。BUG-014 污染数据不在本次授权删除范围内；用户原任务 `DL-20260831-0036` 不在本次授权操作范围内。

## 2. 执行摘要

| 阶段 | 计划数 | 结果摘要 | 证据 |
|---|---:|---|---|
| A 环境基线 | 4 | 通过 | `00-environment/A-B-phase-summary.md` |
| B 自动化基线 | 4 | 通过；Go、构建及 Playwright 回归完成 | `00-environment/A-B-phase-summary.md` |
| C 下载 | 3 | 核心下载通过；输入静默默认保留为 BUG-003 | `02-download/C-D-phase-summary.md` |
| D Coverage/数据质量 | 8 | 一致性与链上抽样通过；污染和格式项登记 | `02-download/C-D-phase-summary.md`、`04-data-quality/` |
| E 数据浏览 | 4 | 核心查询、精度与统计通过；P3 行为项登记 | `05-explorer/E-phase-summary.md` |
| F 案件/标签 | 3 | 创建、关联、恢复通过；并发 last-write-wins 风险登记 | `07-metadata/F-G-H-phase-summary.md` |
| G Token Metadata | 4 | 通过 | `07-metadata/F-G-H-phase-summary.md` |
| H Method/ABI | 5 | 核心解析、缓存、Backfill 通过；队列遗留登记 | `07-metadata/F-G-H-phase-summary.md` |
| I 多链 RPC | 5 | 七链、Capability、Runtime、Receipt/Trace 与边界通过 | `09-rpc/I-phase-summary.md` |
| J 导出 | 5 | 初测发现 P1；修复部署后门槛复验通过 | `06-export/J-phase-summary.md`、本报告 §11 |
| K API 控制中心 | 7 | 官方余额、TTL、Kill Switch、自愈通过；告警缺口登记 | `10-api-control/K-phase-summary.md` |
| L 故障恢复 | 7 | 暂停、取消、重启、SIGKILL、NATS 中断通过；REC-007 部分执行 | `11-recovery/L-phase-summary.md` |
| M 权限与安全 | 4 | 权限、隔离、Secret 扫描通过；错误文本泄露登记 | `12-security/M-phase-summary.md` |
| N UI/兼容性 | 5 | 核心桌面/移动交互通过；兼容性和可访问性问题登记 | `13-ui/N-phase-summary.md` |
| O 性能与长稳 | 4 | 延迟与吞吐有实测；PERF-004 未执行 | `14-performance/O-phase-summary.md` |
| **A～O 合计** | **72** | **60 通过 / 9 失败 / 0 阻塞 / 3 未执行** | `00-environment/`～`14-performance/` |

原始总数为初次全量验收快照，保留用于审计；P1 修复后的新增结果以 §11 的 7/7 定向复验为准，不回写、伪造原始执行计数。

## 3. 真实数据证据

- 主要完整地址：
  - `0x9b77e3b4f85c7cea7da451f454895edbd10b80dd`
  - `0x6808c73d7e9f1e1905c0618bb47bd6947ed4a6ae`
  - `0x680849ef321ea46f3038665134c866bf55b5a6ae`
  - `0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28`
- 关键完整交易哈希：
  - BUG-002 复验：`0x4b61b5353106750a10f66d12d61eba904ce0def3c09b5d43aaa4d55700d28e2a`
  - 单区块下载：`0xc498868807b1dfea2999f41a0fcdaa69d957e485382c2a0edbef24a0f3ac9e18`
- 关键区块：`114147137`、`115394565`；BUG-002 Raw 分片覆盖 `109998276`～`119154061`
- 关键 Job：`f0b43c1d-d922-4af5-bbab-7b52a6728d88`、`88e97c80-7aa7-47b0-8a9c-34b4cadcd55f`
- 关键 Export：`db282626-51c2-4c92-ae80-b36d7fb6a63c`；门槛复验导出前缀 `9d149b12`、`ce7458af`、`40b2b17b`、`94fed8bc`
- API 控制中心同一 24 小时窗口交叉核对：logical calls 1,178、Provider calls 585、errors 8、retries 4、cache hits 9、credits 371,800；PG 与 API 逐项一致
- Ankr 官方余额来源：Ankr Management MCP，GREEN 计划，复验时剩余 192,245,400 credits；未用本地估算替代官方余额

## 4. 数据一致性结果

| Dataset | 页面 | API | PostgreSQL Coverage | ClickHouse FINAL | 导出 | 结论 |
|---|---:|---:|---|---:|---:|---|
| transactions | 4 | 4 | 1（覆盖提交口径） | 4 | 已抽检 | 一致 |
| token_transfers | 242 | 242 | 1（覆盖提交口径） | 242 | 242 | 一致 |
| nft_transfers | 0 | 0 | 无错误大范围提交 | 0 | 0 | 一致 |
| logs | 0 | 0 | 无错误大范围提交 | 0 | 已抽检 | 一致 |
| balances | 3 | 3 | 3 | 3 | 已抽检 | 一致 |

PostgreSQL `row_count` 表示 Coverage 提交任务的下载量，不是仓库总行数；ClickHouse 物理行与 FINAL 的差异来自 ReplacingMergeTree 版本语义。transactions、token_transfers、logs 各 10 笔链上回执抽样 30/30 一致；原生余额 `8546694759625769` 与实时链上余额逐位一致。

## 5. 多链结果

| 链 | Head | Safe Head | Runtime | Receipt | Trace | 下载/能力 | 结论 |
|---|---|---|---|---|---|---|---|
| BSC | >0 | < Head | 通过 | 通过 | 通过 | 实际下载通过 | 通过 |
| Ethereum | >0 | < Head | 通过 | 通过 | 能力如实标注 | Capability 与 PG 一致 | 通过 |
| Polygon | >0 | < Head | 通过 | 通过 | 能力如实标注 | archive=false 如实降级 | 通过 |
| Arbitrum | >0 | < Head | 通过 | 通过 | 能力如实标注 | Capability 与 PG 一致 | 通过 |
| Base | >0 | < Head | 通过 | 通过 | 能力如实标注 | Capability 与 PG 一致 | 通过 |
| Avalanche | >0 | < Head | 通过 | 通过 | 能力如实标注 | Capability 与 PG 一致 | 通过 |
| Optimism | >0 | < Head | 通过 | 通过 | 能力如实标注 | Capability 与 PG 一致 | 通过 |

七链 Capability 共 56 行，与 PostgreSQL `rpc_provider_capabilities` 一致；未知链、坏地址、坏哈希及公开 Raw RPC 边界均按预期拒绝。

## 6. Bug 清单

| ID | 等级 | 标题 | 放行状态 | 证据路径 |
|---|---|---|---|---|
| BUG-001 | P1 | 部署落后、导出字段与 SHA-256 问题 | 已修复并复验通过 | `bugs/BUG-001-stale-deployment.md` |
| BUG-002 | P1 | token_transfers 历史区间静默漏数 | 已修复并复验通过 | `bugs/BUG-002-token-transfers-range-miss.md` |
| BUG-003 | P2 | 缺 chain/datasets 静默默认 | 风险接受，待修复 | `bugs/BUG-003-silent-defaults.md` |
| BUG-004 | P2 | PostgreSQL 错误文本泄露 | 风险接受，待修复 | `bugs/BUG-004-pg-error-leak.md` |
| BUG-005 | P2 | 预算告警未生成、无 DELETE API | 风险接受，待修复 | `bugs/BUG-005-budget-alerts-missing.md` |
| BUG-006 | P2 | Method 解析队列长期 UNRESOLVED | 风险接受，待修复 | `bugs/BUG-006-method-queue-stuck.md` |
| BUG-007 | P3 | logs.address 大小写不一致 | 技术债 | `bugs/BUG-007-logs-address-case.md` |
| BUG-008 | P3 | 非法 cursor 返回 200 空列表 | 技术债 | `bugs/BUG-008-cursor-200.md` |
| BUG-009 | P3 | token_transfers 忽略 method 筛选 | 技术债 | `bugs/BUG-009-method-filter-ignored.md` |
| BUG-010 | P3 | 元数据弹窗 Escape 无效 | 技术债 | `bugs/BUG-010-escape-dialog.md` |
| BUG-011 | P3 | 768×1024 数据浏览横向溢出 | 技术债 | `bugs/BUG-011-viewport-768-overflow.md` |
| BUG-012 | P3 | Chrome 管控页局部渲染超时 | 技术债 | `bugs/BUG-012-chrome-control-tab.md` |
| BUG-013 | P3 | Worker 启动探测污染错误率 | 技术债 | `bugs/BUG-013-startup-probe-errors.md` |
| BUG-014 | P2 | Coverage/统计含历史污染数据 | 风险接受；禁止擅自删除 | `bugs/BUG-014-pollution-in-stats.md` |
| BUG-015 | P3 | value_raw 零值格式不一致 | 技术债 | `bugs/BUG-015-value-raw-hex-zero.md` |
| BUG-016 | P3 | at-least-once 物理重放 | 已确认可接受语义 | `bugs/BUG-016-at-least-once-replay.md` |

## 7. 未执行与阻塞项

- `PERF-004`：需 6～24 小时连续窗口；本次只保留 RSS、API/Worker、ClickHouse 基线快照。作为长稳补充项，不阻断本次放行。
- `REC-007`：完整异常矩阵需受控代理和维护配置；已用真实 upstream/method_unsupported/timeout 错误与全量单测验证错误分类、退避和重试上限。不阻断本次放行。
- Firefox 完整矩阵：Playwright bundled Firefox 的核心 6 用例通过，完整矩阵未单独执行；Edge、Chrome 及 Firefox 核心路径已有真实回归。兼容性风险按 BUG-012 继续跟踪。
- 阻塞项：无。

## 8. 测试数据与恢复结果

- 测试产生的案件、标签、预算、签名、display_name 已删除或恢复。
- 原案件“盘古”和标签“入金地址”关联仍存在；目标地址 display_name 已恢复为空。
- 人工覆盖已清空，Kill Switch=false，控制状态可自动回到 NORMAL。
- BUG-014 历史 Mock/污染数据只完成范围清单，**未删除**。
- 用户原任务 `DL-20260831-0036` 在 2026-09-03 放行复核时仍为 `PAUSED`，未执行恢复、取消或状态修改。
- 放行复核未发现测试遗留的非终态 shard；近 6 小时无测试遗留 RUNNING/QUEUED/PAUSE_REQUESTED 等任务。

## 9. 最终健康状态

2026-09-03 01:11（UTC+8）通过 SSH 对 VM 做只读复核：

- API：PID `1804248`，`/health/ready` 返回 ready
- Worker：PID `1804250`
- PostgreSQL：healthy / ready check=ok
- ClickHouse：healthy / ready check=ok
- MinIO：运行中 / ready check=ok
- NATS：运行中 / ready check=ok
- Kill Switch：false
- 人工覆盖：空
- 未解释 WARN/ERROR：验收结束时无；测试期间日志均已逐条归因

## 10. 证据目录

- `00-environment/`：环境、构建、部署与基线脚本
- `01-health/`：健康检查和用户原 PAUSED 任务核验
- `02-download/`：真实下载、Raw 分页、BUG-002 根因和门槛复验
- `03-coverage/`：复用、竞态和四层一致性 SQL
- `04-data-quality/`：Grain、格式、链上抽样、污染范围
- `05-explorer/`：地址目录、筛选、精度证据
- `06-export/`：格式、哈希、token_decimals 和导出复验
- `07-metadata/`：案件、标签、Token Metadata 汇总
- `08-method-abi/`：Method/ABI、Backfill 和队列证据
- `09-rpc/`：七链 RPC、Runtime、Receipt/Trace 证据
- `10-api-control/`：官方余额、状态机、TTL、Kill Switch 和清理
- `11-recovery/`：暂停、取消、SIGTERM、SIGKILL、API/NATS 恢复
- `12-security/`：角色矩阵、注入、Ticket、Secret 扫描
- `13-ui/`：Playwright、视口、失败 trace
- `14-performance/`：延迟、吞吐和长稳基线
- `bugs/`：BUG-001～BUG-016 独立记录
- `RELEASE-DECISION.md`：本次放行决定、风险边界与实时复核摘要

本地证据包共 138 个文件；放行操作前对 `acceptance-evidence/` 执行 Secret 特征扫描，未命中 API Key、数据库/MinIO 密码、私钥或 Bearer Token。

## 11. 放行门槛复验

### 11.1 重新构建与部署

- 使用 `scripts/sync_to_vm.py` 同步 375 个文件，排除 `.env`、`node_modules`、`dist`、`bin`、`.git`，VM 密钥未覆盖。
- 使用 `scripts/dev.sh` 重建 `bin/api`、`bin/worker` 并重启 Vite。
- 二进制构建时间与源码同步时间均为 2026-09-02 23:36；API/Worker 于 23:36:54 启动。
- Worker 二进制 strings 检查包含 `range_not_reached` 修复代码；前端 :3000 由当前源码提供，`/api-control` 实际渲染成功。

### 11.2 BUG-002 根因与修复

Ankr 对 transactions/token_transfers/nft_transfers 忽略请求块边界，并按最旧到最新升序枚举整段地址索引。原执行器按降序假设，在第一页见到下界以下行即停止，导致历史目标区间未被扫描便以 0 行完成。

`bb719e2` 将停止条件改为方向无关的双侧横跨：只有下界侧和上界侧均已出现，或 Provider 明确耗尽，才允许结束。耗尽但未横跨、区间内 0 行且末页仍饱和时，写入 `range_not_reached` 到 shard.error 和审计；重试耗尽后进入 FAILED，禁止静默 0 行 COMPLETED。相关三组单测及全量 `go test ./...` 通过。

### 11.3 定向复验结果

| 复验项 | 结果 |
|---|---|
| parquet/jsonl | 242 行 COMPLETED；PAR1 首尾正确；JSONL 逐行合法且重复运行哈希一致 |
| SHA-256 | CSV/XLSX/JSONL/Parquet 均非空，本地 Get-FileHash 与服务端逐位一致 |
| token_decimals | CSV 仅 `{18,9}`，XLSX K2 为 `<v>18</v>`，不再输出 Go 指针地址 |
| `/api-control` | VM :3000 返回 200，浏览器 h1 为“API 控制中心”，数据加载成功 |
| BUG-002 目标交易 | 完整枚举 4 页后入库 log 214/216 两行，与链上回执逐位一致；Coverage 仅提交 `[114147137,114147137]` |
| 静默完成防护 | 同一历史请求不再 0 行静默完成；截断守卫有单测覆盖 |
| 部署状态 | API/Worker/前端均运行当前修复基线，放行实时健康检查通过 |

放行执行阶段再次运行 `go test ./...`，全部包通过；再次运行 `npm run build`，TypeScript 与 Vite 构建成功。VM 上五个关键修复源码文件的 SHA-256 与本地逐一相同，`/api-control` 实时返回 200，`/health/ready` 四项依赖全部为 ok。

V1 的 subject/job 表单仅支持 CSV/XLSX/JSON；JSONL/Parquet 由 V2 addresses/datasets 表单提供。该差异已登记为 `docs/api.md` 文档项，不构成本次放行阻断，也不得表述为 V1 已支持 JSONL/Parquet。

## 12. 放行决定

在 P0=0、开放 P1=0、三项放行门槛全部通过、实时健康状态正常、测试数据已恢复的前提下，决定将原“带条件通过”升级为“正式通过，准予放行”。P2/P3 风险接受仅授权本次版本放行，不授权删除历史污染数据，也不授权修改用户原 PAUSED 任务。
