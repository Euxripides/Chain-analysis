# 链上分析平台完整真实数据验收测试方案

版本：V1.0  
适用范围：V1、V1.1、V1.2、V1.3、V1.4、V1.5、V1.6  
测试对象：地址下载、任务中心、状态总览、API 控制中心、数据浏览、案件与标签、Token Metadata、Method/ABI、统一链 RPC、导出及基础设施  
执行角色：DeepSeek（测试执行与取证）  
结果接收：将完整验收结果、Bug 清单和证据目录交回项目负责人，由后续修复任务处理

## 1. 测试目标

本次验收必须回答以下问题：

1. 页面展示的数据是否来自真实接口，而不是前端常量、Mock 值或旧缓存。
2. 页面、API、PostgreSQL、ClickHouse、MinIO 和 Ankr 调用审计之间是否一致。
3. 下载、复用、查询、筛选、统计和导出是否保持相同的数据范围与语义。
4. 暂停、恢复、取消、进程重启、Provider 异常等情况下，任务是否可以安全收敛。
5. 多链入口是否真正可用，是否存在“前端能选、后端未注册”的假功能。
6. API 控制中心的调用量、成本、异常和自动管控是否真实、同步且可解释。
7. 案件、标签、Token Metadata、Method/ABI 和合约识别是否准确且可持久化。
8. 导出的 CSV、XLSX、JSONL、Parquet 和 ZIP 是否可以真实打开，内容与页面及数据库一致。
9. 权限、输入校验、Secret 脱敏和下载 Ticket 是否达到安全要求。
10. 桌面端、移动端、慢网和大数据量条件下，页面是否仍然可用。

## 2. 强制验收原则

### 2.1 不允许以 HTTP 200 作为唯一通过依据

每个数据功能至少验证以下四层中的三层；关键资金数据必须验证全部四层：

1. 页面显示和交互结果。
2. API 响应字段、类型、数量和业务语义。
3. PostgreSQL 控制面或 ClickHouse 数据面中的真实记录。
4. MinIO Raw/导出文件、Provider 审计或链上交易回执。

### 2.2 禁止伪造真实数据结论

- `ANKR_MODE=mock` 只能用于故障注入、压力测试和确定性回归，不能作为真实数据验收结果。
- 真实数据用例必须记录 `ANKR_MODE=live`、执行时间、链、完整地址、完整交易哈希和区块范围。
- 官网账户余额只能来自 Ankr Management MCP；不能用本地成本估算反推余额。
- Selector 公共签名命中不是合约 ABI 验证，不得写成“已验证”。
- ClickHouse 物理行数不等于 `FINAL` 后的业务唯一行数，报告必须分别记录。
- Coverage 命中导致 0 次 Provider 请求属于正确复用，不能误报为下载失败。

### 2.3 测试期间的数据安全

- 默认只读测试；修改案件、标签、策略、预算和人工覆盖时，必须先保存原值并在 `finally/cleanup` 阶段恢复。
- 未经项目负责人明确授权，不得删除 ClickHouse、PostgreSQL、MinIO 或 `/data/blockchain-platform/` 中的数据。
- 不得执行 `docker compose down -v`、递归删除数据目录、清空数据库、重置 Git 工作区等不可恢复操作。
- Worker/API 中断测试只允许在维护窗口执行，每次只中断明确 PID，完成后必须恢复并验证健康状态。
- 不得在报告、日志、截图、命令行输出或 Git 中写出 API Key、OAuth Bearer、数据库密码、PKCE verifier。

### 2.4 Bug 修复边界

本方案交给 DeepSeek 时，默认职责是“测试、记录、取证”，不是直接修复：

- 发现 P0 时立即停止相关写入测试，保存证据并报告。
- 发现 P1/P2 时继续执行不受影响的测试，不得为了让用例通过而修改数据或降低断言。
- 不修改源代码、不提交 Git、不清理历史数据，除非项目负责人另行明确授权。

## 3. 环境与路径

### 3.1 Windows 本地

```text
项目：E:\Code\远程虚拟机\harness\blockchain-platform\
后端：E:\Code\远程虚拟机\harness\blockchain-platform\backend\
前端：E:\Code\远程虚拟机\harness\blockchain-platform\frontend\
部署：E:\Code\远程虚拟机\harness\blockchain-platform\deploy\
文档：E:\Code\远程虚拟机\harness\blockchain-platform\docs\
```

### 3.2 Ubuntu 虚拟机

```text
主机：192.168.31.152
用户：euripides
项目：/home/euripides/projects/blockchain-platform/
API：8080
Worker Metrics：8081
Frontend：3000
API 日志：/tmp/api.log
Worker 日志：/tmp/worker.log
持久化数据：/data/blockchain-platform/
```

### 3.3 已知容器

容器名称必须先通过 `docker ps` 实际确认，不得硬猜。当前参考名称：

```text
deploy-postgres-1
deploy-clickhouse-1
deploy-minio-1
deploy-nats-1
```

## 4. 测试输入与证据目录

### 4.1 已验证的 BSC 基准数据

```text
地址：0x9b77e3b4f85c7cea7da451f454895edbd10b80dd
官方 BSC USDT：0x55d398326f99059ff775485246999027b3197955
固定测试区块：98000718
固定交易哈希：0x2684ba9654b372f99b349ddf839afda409f4a306182d5fdde2e97af2e4d41617
```

以上数据可以用于重复回归。其他链的真实地址必须由执行者在测试开始前选取并记录来源，不得编造。

### 4.2 建议证据目录

```text
acceptance-evidence/
├── 00-environment/
├── 01-health/
├── 02-download/
├── 03-coverage/
├── 04-data-quality/
├── 05-explorer/
├── 06-export/
├── 07-metadata/
├── 08-method-abi/
├── 09-rpc/
├── 10-api-control/
├── 11-recovery/
├── 12-security/
├── 13-ui/
├── 14-performance/
├── bugs/
└── final-report.md
```

每个测试项至少保存：请求、响应、数据库查询结果、必要截图、文件哈希和执行时间。Secret 必须替换为 `[REDACTED_SECRET]`。

## 5. 严重等级与通过标准

| 等级 | 定义 | 示例 |
|---|---|---|
| P0 | 数据损坏、安全泄露、资金数据错误、不可恢复 | 越界下载被标记 Coverage；不同地址数据混入；Secret 泄露；取消后仍持续写入 |
| P1 | 核心功能不可用或数据明显不一致 | 页面统计与导出不一致；多链入口 503；恢复后重复下载；官方余额被本地估算替代 |
| P2 | 部分功能错误，有明确绕行方式 | 单个筛选条件失效；错误信息不准确；分页遗漏 |
| P3 | 视觉、易用性或低风险问题 | 对齐、截断、键盘焦点、移动端局部体验问题 |

最终通过条件：

- P0 = 0。
- P1 = 0。
- P2 必须全部列出，并给出影响范围；是否带条件通过由项目负责人决定。
- 所有强制用例执行完成，未执行项必须写明原因和所需条件。
- 所有测试数据修改均已恢复；若未恢复，最终结论不得写“通过”。
- API/Worker 最终健康，日志中没有未解释的 ERROR/WARN。

## 6. 阶段 A：环境和基线检查

### ENV-001 Git 与代码基线

操作：

```powershell
cd E:\Code\远程虚拟机\harness\blockchain-platform
git status --short
git log -1 --oneline
```

预期：

- 记录当前 commit。
- 工作区是否有修改必须写入报告；不得覆盖用户已有修改。

### ENV-002 服务进程与端口

```bash
ssh euripides@192.168.31.152
pgrep -af 'bin/(api|worker)'
ss -lntp | grep -E ':3000|:8080|:8081|:5433|:8123|:9001|:4222'
docker ps --format '{{.Names}}\t{{.Status}}\t{{.Ports}}'
```

预期：API、Worker 各一个有效进程；端口没有重复占用；数据服务容器健康。

### ENV-003 配置模式与 Secret 脱敏

只检查变量是否存在，不打印值：

```bash
pid=$(pgrep -x api | head -1)
tr '\0' '\n' < /proc/$pid/environ | sed -n -E 's/^(ANKR_MODE|RPC_ENABLED|RPC_CHAINS|ANKR_RPC_ENABLED)=.*/\1=[SET]/p'
```

真实验收预期：

- `ANKR_MODE=live`。
- `RPC_CHAINS` 包含 bsc、eth、polygon、arbitrum、base、avalanche、optimism，或应用使用等价默认值。
- API Key 不得出现在输出、进程参数或日志中。

### ENV-004 基础设施就绪

```bash
curl -sS http://127.0.0.1:8080/health/live
curl -sS http://127.0.0.1:8080/health/ready
```

预期：`live.status=ok`；`ready.status=ready`，postgres、clickhouse、minio、nats 全部为 `ok`。

## 7. 阶段 B：自动化基线

### AUTO-001 后端测试

```powershell
cd E:\Code\远程虚拟机\harness\blockchain-platform\backend
go test ./...
```

通过标准：退出码 0；记录包数、失败数和总耗时。禁止只截取最后一行。

### AUTO-002 前端构建

```powershell
cd E:\Code\远程虚拟机\harness\blockchain-platform\frontend
npm run build
```

通过标准：TypeScript 与 Vite 均成功；大 Chunk 警告单独记录为性能观察项，不冒充构建失败。

### AUTO-003 普通浏览器回归

先建立 API 隧道：

```powershell
ssh -N -L 8080:127.0.0.1:8080 euripides@192.168.31.152
```

另一个终端执行：

```powershell
cd E:\Code\远程虚拟机\harness\blockchain-platform\frontend
$env:E2E_SCREENSHOT_DIR="$env:TEMP\blockchain-platform-acceptance\screens"
npx playwright test
```

通过标准：所有非条件用例通过；记录每个 skip 的条件，不能把 skip 计入 pass。

### AUTO-004 真实写入与导出回归

```powershell
$env:E2E_LIVE_DOWNLOAD='1'
$env:E2E_LIVE_METADATA_MUTATION='1'
$env:E2E_ARTIFACT_DIR="$env:TEMP\blockchain-platform-acceptance\artifacts"
$env:E2E_SCREENSHOT_DIR="$env:TEMP\blockchain-platform-acceptance\screens-live"
npx playwright test
```

如需测试任务详情 CSV/XLSX，必须给出一个真实、已完成且包含 `logs` 的任务：

```powershell
$env:E2E_JOB_EXPORT_ID='<COMPLETED_LOGS_JOB_UUID>'
npx playwright test tests/live-download.spec.ts -g 'job detail downloads' --project=desktop
```

## 8. 阶段 C：地址下载和 Coverage

### DL-001 输入校验

分别提交：空地址、错误长度、非十六进制、重复地址、大小写混合地址、负区块、起始块大于结束块、未知链、未知 Dataset。

预期：

- 页面阻止明显无效输入。
- API 返回 400，而不是 500 或静默改成默认值。
- 地址规范化为小写并去重。
- 错误响应不得包含 SQL、堆栈、Secret 或内部 endpoint。

### DL-002 单区块真实下载

使用基准地址、区块 `98000718`，只选 `token_transfers`。

验证：

1. Coverage Preview 返回一个 item，区间精确为一个块。
2. 未覆盖时任务产生至少一次 Provider 请求，最终写入固定交易哈希。
3. 已覆盖时任务 0 次 Provider 请求、0 个新 Shard，仍能从 ClickHouse 查询到固定交易。
4. `from_block`、`to_block`、Dataset 和地址不得发生扩张。
5. 任务详情、数据浏览和导出均能找到同一行。

### DL-003 五类 Dataset

对 transactions、token_transfers、nft_transfers、logs、balances 分别执行小范围测试。

预期：

- 各 Dataset 状态独立，不因某个 Dataset 为 0 行而阻塞其他 Dataset。
- 0 行结果不得错误提交大范围 Coverage。
- balances 按快照处理，不伪装成完整历史区间。
- Raw 成功、ClickHouse 成功后才能提交 Coverage。

### COV-001 完整复用

对已成功完成的完全相同请求再次创建任务。

预期：Coverage 100%；Provider 新增请求数为 0；无 Raw 新对象；ClickHouse `FINAL` 行数不增加；任务快速收敛为 COMPLETED/SKIPPED。

### COV-002 部分缺口

先下载 `[N,N]`，再请求 `[N-1,N+1]`。

预期：只请求缺失块；已覆盖块不重复请求；最终 Coverage 合并为连续区间；页面命中率和保存请求数合理。

### COV-003 并发相同任务

同时创建两个相同地址、相同 Dataset、相同区间的任务。

预期：不得重复扩大 Coverage 或产生业务重复行；至少一个任务复用另一任务已经提交的 Coverage；父子状态全部收敛。

### COV-004 Coverage 与仓库一致性

对测试地址逐 Dataset 比较：

- PostgreSQL `dataset_coverage.row_count`。
- ClickHouse 对地址和区间查询的物理行数。
- ClickHouse `FINAL` 后业务唯一行数。
- API 查询的 `count/stats`。
- 页面统计及导出数据行数。

差异必须解释为明确语义（例如双向地址匹配、ReplacingMergeTree 物理版本、重复 Coverage 下载量）；不能仅写“基本一致”。

## 9. 阶段 D：数据质量

### DATA-001 数据集 Grain 和重复

分别检查业务唯一键：

| Dataset | 建议业务 Grain |
|---|---|
| transactions | chain + tx_hash |
| token_transfers | chain + tx_hash + log_index + token_address |
| nft_transfers | chain + tx_hash + log_index + contract_address + token_id |
| logs | chain + tx_hash + log_index |
| balances | chain + address + token_address + 快照版本/时间 |

必须同时报告物理行数和 `FINAL` 行数。发现物理重复时先确认是否为 ReplacingMergeTree 正常版本，不能直接判定为业务重复。

### DATA-002 必填字段和格式

检查：

- tx_hash 是否为完整 32-byte Hex。
- 地址是否为空、长度错误、非 Hex 或大小写未规范化。
- block_number、log_index 是否异常。
- timestamp 是否为空、明显未来时间或早于链创世时间。
- amount_raw/value_raw 是否仍为字符串。
- amount 是否发生科学计数法、截断或 float64 精度损失。
- token_decimals 是否落在合理范围，未知时是否明确为空/未知而不是伪造 18。

### DATA-003 链上抽样复核

每种 Dataset 至少随机抽取 10 行，使用真实 RPC receipt/log 或可信区块浏览器复核：完整哈希、区块、时间、发送方、接收方、合约、原始数量和日志索引。

判定：10 行必须全部一致；任何资金金额或交易归属不一致至少为 P1。

### DATA-004 历史测试数据污染

重点识别：

- `bench` 链。
- `0x1111...`、`0x2222...`、`0xaaaa...` 等明显占位地址。
- 大量连续低位数字地址。
- 由 Mock/Benchmark 生成但被计入当前状态总览的数据。

只输出污染范围、行数、时间和关联任务，不执行删除。报告必须分别给出“全部仓库数据”和“排除已确认测试数据”的统计，供项目负责人决定清理方案。

## 10. 阶段 E：数据浏览、筛选和统计

### BRW-001 地址目录

验证搜索、链筛选、案件、标签、同步状态、服务端分页和 cursor。

预期：

- cursor 非法时返回 400。
- 下一页无重复、无遗漏，顺序稳定。
- 地址、交易数、转账数、最高块、最后同步时间与数据库一致。
- 搜索完整地址、前缀、后缀、名称、案件、标签均符合定义。

### BRW-002 OKLink 风格列表

验证表头顺序：交易哈希、方法、时间、发送方、接收方、数量、代币。

预期：

- 金额列与内容对齐。
- 正负方向与 In/Out 标签一致。
- 地址为合约时显示合约图标。
- 合约/代币显示解析名称；无可信名称时才显示可识别的回退值。
- 地址和哈希外链完整、正确、使用新窗口且带 `noopener noreferrer`。

### BRW-003 筛选组合

覆盖：时间、数量 `>=/<=/区间`、Token 名称、Token 合约、发送方、接收方、Method 名称、签名、Selector、Unknown。

每个组合必须比较：API filter、返回行、页面行、统计 count、转入/转出、Token 种类及导出行。

### BRW-004 金额精度

选取：超大整数、18 位小数、极小金额、0、负方向显示和数值末尾大量 0 的记录。

预期：

- 页面采用可读的规范化金额，不展示无意义的超长尾数。
- tooltip 或详情可以追溯完整金额和 amount_raw。
- 排序和筛选使用数值语义，不使用格式化字符串比较。
- CSV/JSON 保留精度，XLSX 不将大整数静默转换为不精确浮点数。

## 11. 阶段 F：案件、标签和地址元数据

### META-001 任意名称创建与恢复

使用带时间戳的临时案件和标签，验证创建、关联、刷新后保持、取消、移除和删除。记录原始关联并在测试结束恢复。

预期：支持任意合法名称；重复名称不产生重复记录；保存后地址头部、目录和导出元数据同步。

### META-002 删除语义

创建仅用于测试的案件/标签后删除。

预期：删除标签不会删除地址或链上数据；删除主案件后地址安全重选或变成无案件；无悬空关联；审计事件存在。

### META-003 并发修改

两个浏览器同时修改同一地址元数据。

预期：结果符合接口定义，不能静默丢失双方修改；若当前没有版本冲突保护，记录为风险和复现步骤。

## 12. 阶段 G：Token Metadata

### TOK-001 官方 USDT

验证合约 `0x55d398326f99059ff775485246999027b3197955`：

- Symbol 为 USDT。
- 名称为 Tether USD。
- decimals 与链上合约一致。
- Logo 可以真实加载，`naturalWidth > 0`。
- 来源和可信状态为 verified registry 等真实可信来源。
- 不显示“未验证”“名称未收录”“解析”等误导文案。

### TOK-002 同名仿冒代币

选取与 USDT 同 Symbol 但不同合约的 Token。

预期：始终按 `chain + contract` 区分；不能继承官方 USDT Logo、名称或已验证状态；筛选按合约后不混入其他同名 Token。

### TOK-003 RPC Metadata 失败

对无 name/symbol、异常 decimals、非 ERC-20 合约和 RPC 超时情况测试。

预期：列表不崩溃；回退状态真实；失败有队列/审计；不会把合约地址冒充名称；不会无限重复请求。

### TOK-004 Logo 安全

验证错误 MIME、超大图片、损坏图片、WebP/PNG/SVG 边界和远程超时。

预期：后端限制大小和尺寸；前端回退图标稳定；没有布局跳动；Logo URL 不允许任意内网探测。

## 13. 阶段 H：Method/ABI

### MTH-001 Selector 本地提取

抽样标准 transfer、approve、swap、原生转账、空调用和合约创建。

预期：Selector 从 input 本地提取；原生转账/空调用/创建交易分类正确；完整 input 保留。

### MTH-002 Verified 语义

分别验证 manual_override、contract_abi、Ankr verified、local_signature、public_signature、heuristic、unknown。

预期：只有可信 ABI/人工覆盖/已验证 Provider 解码可标 verified；公共 4-byte 和本地签名候选不能标为合约已验证。

### MTH-003 碰撞

构造或选取一个 Selector 多候选场景。

预期：返回全部候选；可按 input 长度收敛时结果可解释；无法判断时名称为空、confidence=low，不能随机选择。

### MTH-004 Deep Decode 缓存

按顺序执行 `local_only → auto → auto → force_refresh`，同时记录 provider_usage。

预期：

- local_only 本地缺失时 404 且 0 Provider 请求。
- 第一次 auto 最多触发一次真实 Provider 解码并缓存。
- 第二次 auto 命中缓存，不新增外部请求。
- force_refresh 需要正确角色并产生真实请求。
- uint256 等大整数输出十进制字符串，不损失精度。

### MTH-005 Backfill

在隔离的小样本执行创建、暂停、恢复、取消和幂等重跑。

预期：使用 keyset checkpoint；不重新下载历史交易；第二次无变化扫描不重写数据；实时下载不被长期饿死。

## 14. 阶段 I：多链 RPC

### RPC-001 七链实时链头

逐一调用：

```text
GET /api/v1/chains/bsc/head
GET /api/v1/chains/eth/head
GET /api/v1/chains/polygon/head
GET /api/v1/chains/arbitrum/head
GET /api/v1/chains/base/head
GET /api/v1/chains/avalanche/head
GET /api/v1/chains/optimism/head
```

预期：全部 200；`head_block > 0`；`safe_block < head_block`；确认深度符合 Chain Registry；未知链返回 400。

### RPC-002 Capability

逐链读取 `/system/chains/{chain}/capabilities` 并与数据库 `rpc_provider_capabilities` 比较。

预期：standard/archive/batch/trace/debug 状态与实际探测一致；某能力失败只能降级对应功能，不能拖垮所有 RPC。

### RPC-003 Runtime

每链选择一个 EOA 和一个合约调用 `/addresses/{chain}/{address}/runtime`。

预期：EOA/合约候选、原生余额、链头正确；地址非法返回 400；数值无精度损失；相同请求缓存命中时不重复计外部调用。

### RPC-004 Receipt、Trace、Internal Calls

选取已确认交易测试 receipt；对有调用树的交易测试 trace 三种模式和 internal-calls。

预期：回执必填字段完整；Trace 不支持时返回明确 `trace_unsupported`；local_only 不访问 Provider；持久化后 internal-calls 不再触发 Provider。

### RPC-005 安全边界

尝试未知方法、写方法、超大 batch、错误 hash、错误 block tag 和 Raw RPC 路径。

预期：在到达 Provider 前拒绝不允许的调用；无公开 Raw RPC；错误不泄露 endpoint 与 API Key。

## 15. 阶段 J：导出

### EXP-001 格式矩阵

对同一筛选结果导出 CSV、XLSX、JSONL/JSON、Parquet。

预期：

- Export 状态最终 COMPLETED，rows 与真实数据行一致。
- CSV 可按 UTF-8 正确解析；JSONL 每行是完整 JSON。
- XLSX 首字节为 ZIP/OOXML，Excel 或 LibreOffice 可真实打开。
- Parquet 首尾均为 `PAR1`，可由 parquet reader 读取 schema 和行。
- 服务端 SHA-256 与下载文件本地 SHA-256 完全一致。

### EXP-002 导出范围

在地址详情分别选择：全部、筛选结果、勾选数据。

预期：

- 勾选 1 行只导出 1 行。
- 筛选结果包含全部筛选命中，不局限于当前分页。
- 全部不受当前筛选影响。
- 文件名清楚区分全部、筛选结果和已勾选。

### EXP-003 ZIP

选择多个地址或多个 Dataset 触发 ZIP。

预期：结构为 `manifest.json` 和 `addresses/{address}/{dataset}.{ext}`；无路径穿越；每个文件可独立打开；manifest 的地址、Dataset、格式和总任务一致。

### EXP-004 Ticket 与 Range

验证：合法 Ticket、错误 Ticket、过期 Ticket、`bytes=0-15`、开放尾区间、越界、反向、多 Range。

预期：合法区间返回 206 和正确 Content-Range/Content-Length；非法范围返回 416；过期/伪造 Ticket 返回 403；响应字节与完整文件对应片段完全相同。

### EXP-005 空、大数据和公式注入

验证 0 行、1 行、接近 2,000,000 行限制、中文、换行、引号，以及以 `= + - @` 开头的用户元数据。

预期：空导出可解释；达到限制时明确提示而非静默截断；CSV/XLSX 不触发公式执行；内存稳定；导出失败状态和错误可审计。

## 16. 阶段 K：状态总览与 API 控制中心

### CTL-001 同一窗口数据同步

在同一秒记录：

```text
GET /api/v1/api-control/overview?range=24h
GET /api/v1/providers/ankr/usage?hours=24
GET /api/v1/api-control/provider-accounts
```

同时查询 PostgreSQL `provider_usage`。SQL口径必须与服务端一致：

- logical_calls = `SUM(method_calls)`。
- provider_calls = 非 cache_hit 且非 singleflight_hit 的记录数。
- errors = 非成功状态或 `http_status >= 400`。
- retries = `retry_attempt > 0` 的记录数。
- cache_hits = `cache_hit=true` 的记录数。
- credits/cost = 对应列之和。

允许测试执行期间产生少量新增记录；应以固定 `from/to` 或事务快照消除时间漂移，不能用“差不多”判定。

### CTL-002 页面同步

比较状态总览和 API 控制中心 24h 指标、官方余额和同步时间。

预期：相同窗口使用同一数值；刷新后同步更新；Provider、链、时间筛选被带入所有聚合请求；页面不得混用“官网总账”和“本地审计”。

### CTL-003 官方余额

预期：

- 已连接时显示 Management MCP 返回的真实余额、剩余积分、计划和同步时间。
- 未连接/未配置/过期/同步失败时返回 `null` 或明确状态，不能显示 0 或本地估算冒充官方值。
- OAuth bearer 不出现在 API、日志、HTML、localStorage 或数据库明文字段。

### CTL-004 人工覆盖 TTL

保存原状态，创建短 TTL 人工覆盖，记录 `api_control_actions`，等待到期。

预期：立即进入指定中文状态；TTL 到期自动恢复自动决策；原因、操作者、起止时间可审计；测试结束恢复原状态。

### CTL-005 Kill Switch

仅在维护窗口执行。启用后创建一个小任务并观察外部调用计数。

预期：后台任务进入保护状态，Provider 请求不再增长；解除后任务可恢复；前台只读查询和健康接口仍可用；启用和解除均留审计。

### CTL-006 预算和告警

创建临时小预算，触发阈值，验证告警、静默、解决和恢复。

预期：金额、利用率、阈值计算正确；静默不删除告警；解决有操作记录；删除/恢复临时测试配置。

### CTL-007 中文和提示规范

预期：状态选项全部中文；前端不得出现常驻警告条、说明卡或错误段落。警告、说明、成功、错误、风险和确认信息只能通过具有标题、正文、关闭操作的弹窗展示。

## 17. 阶段 L：任务状态与故障恢复

以下用例必须在维护窗口执行，并持续记录 PID、任务状态、行数、Checkpoint、Provider 调用量和日志。

### REC-001 暂停/恢复

创建多页任务，在 RUNNING 时暂停。

预期：当前页在安全边界收敛；行数停止增长；Checkpoint 持久化；恢复后从 checkpoint/coverage 缺口继续；无重复业务行。

### REC-002 取消

取消运行任务。

预期：状态变为 CANCELLED；不再领取新 Shard；已完成数据保留；未完成区间不提交 Coverage；等待 2 分钟后无幽灵写入。

### REC-003 Worker 正常重启

对明确的 Worker PID 发送 SIGTERM，等待退出，再按原环境启动。

预期：租约和 checkpoint 保留；新 Worker 接管；任务最终完成；同一页不会无限重复。

### REC-004 Worker 强制终止

在已确认可恢复的隔离任务上执行 SIGKILL。

预期：租约超时后 Reaper 收回；任务重新调度；Coverage 只包含成功提交部分；最终数据 `FINAL` 不翻倍。

### REC-005 API 重启

保持 Worker 运行，重启 API。

预期：下载不受影响；任务列表、详情和 SSE 恢复；SSE 新连接立即收到当前快照，而不是等待下一次事件。

### REC-006 NATS 暂时不可用

在维护窗口短暂停止 NATS，保留 PostgreSQL/Worker。

预期：持久任务继续安全运行或明确降级；状态不只存在于 NATS；恢复后事件与状态重新可见；不得丢失任务。

### REC-007 Provider 异常

使用受控代理或测试 Provider 注入 429、5xx、超时、连接限制、超大响应、坏 JSON 和 Retry-After。

预期：错误分类准确；重试受上限约束；429 降速；超时/大响应降低 page size；最小页连续失败才拆 Range；取消不重试；错误进入审计和指标。

## 18. 阶段 M：权限与安全

### SEC-001 角色矩阵

使用 Viewer、Analyst、Investigator、Admin、Owner 身份测试：普通查询、force_refresh、Trace、ABI 上传、签名覆盖、Token override、预算和管控操作。

预期：低权限返回 403；高权限按设计可执行；未带身份时的行为与当前 V1 单用户策略一致并被明确记录。

### SEC-002 注入与错误泄露

对 query、cursor、地址、selector、case/tag 名称、排序和导出参数测试 SQL 注入、控制字符、超长值和非法 JSON。

预期：返回 400/413 等明确错误；无 SQL 文本、堆栈、文件路径、Secret；数据库无额外修改。

### SEC-003 下载对象隔离

用 Export A 的 Ticket 尝试访问 Export B，修改 Ticket 字符，访问过期 Ticket。

预期：只能下载绑定对象；不可枚举；错误不暴露 MinIO 内部地址；Bucket 保持私有。

### SEC-004 前端 Secret 扫描

检查构建产物、HTML、source map、localStorage、sessionStorage、Network 和控制台。

预期：无 ANKR API Key、OAuth Token、数据库密码或加密密钥。

## 19. 阶段 N：UI、兼容性与可访问性

### UI-001 视口矩阵

至少测试：390×844、768×1024、1366×768、1440×900、1920×1080。

覆盖地址下载、任务详情、状态总览、API 控制中心、数据浏览、地址详情、Token 弹层、元数据弹窗、交易详情抽屉。

预期：无页面级横向溢出；右侧空间有效利用；表头和数据对齐；弹窗无遮挡并可滚动；长地址、长金额、长 Token 名称不破坏布局。

### UI-002 浏览器矩阵

至少 Edge、Chrome、Firefox。记录浏览器完整版本。

预期：核心操作、下载、日期输入、弹窗、剪贴板和外链一致；不支持能力有明确降级。

### UI-003 键盘和焦点

只使用键盘操作导航、链选择、Token 筛选、弹窗、表格勾选和关闭。

预期：焦点可见；顺序合理；Escape 关闭弹层并把焦点还给触发按钮；Tab 不逃出模态框；图标按钮具有可访问名称。

### UI-004 慢网与错误恢复

模拟 Slow 3G、离线、接口 5 秒延迟和单接口失败。

预期：加载状态真实；重复点击不会创建重复任务；错误使用弹窗；关闭后不占据页面空间；恢复网络后可以重试。

### UI-005 图片和布局稳定性

禁用缓存并重新加载 Token Logo。

预期：图片有固定尺寸；加载前后表格不跳动；损坏 Logo 使用稳定回退图标；官方 Logo 不被仿冒合约复用。

## 20. 阶段 O：性能和长稳

### PERF-001 API 延迟

对健康、地址目录、数据查询、统计、API 控制中心聚合分别采集至少 100 次 P50/P95/P99。冷缓存和热缓存分开报告。

不得使用单次响应推断性能。

### PERF-002 下载吞吐

分别测试 1、10、100 个地址的小范围任务，记录有效行/秒、Provider 请求/秒、下载字节/秒、重试率和 ClickHouse 写入速度。

报告必须区分 Provider 探测/规划速度与真实下载有效行速度。

### PERF-003 前端大结果

验证 100 行页面、全结果统计、复杂筛选和大文件导出时的交互延迟、主线程阻塞和内存。

### PERF-004 6–24 小时长稳

持续采集：API/Worker RSS、goroutine、连接池、队列深度、磁盘、MinIO 对象数、ClickHouse part/merge、PostgreSQL 表增长、Provider 错误率。

通过标准：无持续内存泄漏、无无界队列、无任务永久卡住、无日志高速增长、无磁盘失控。

## 21. 最终恢复与清理检查

测试结束必须执行：

1. 恢复人工覆盖、Kill Switch、预算、告警静默和自动保护原值。
2. 删除仅用于验收的案件/标签，并恢复地址原关联。
3. 确认没有遗留 RUNNING/PAUSED 测试任务；不得擅自取消用户原有任务。
4. 确认 API、Worker 各一个进程且健康。
5. 确认 PostgreSQL、ClickHouse、MinIO、NATS 全部 ready。
6. 检查 `/tmp/api.log`、`/tmp/worker.log` 的 WARN/ERROR，并逐条归因。
7. 记录本次测试新增的 Provider 请求、Credits 和估算成本。
8. 测试数据只列清单，不直接清除链上仓库数据。

## 22. Bug 记录模板

每个 Bug 单独使用以下格式：

```markdown
## BUG-XXX：简明标题

- 严重等级：P0 / P1 / P2 / P3
- 状态：新建 / 可复现 / 偶发 / 阻塞 / 已确认非 Bug
- 发现时间：YYYY-MM-DD HH:mm:ss Z
- 环境：commit、VM、浏览器、ANKR_MODE
- 影响模块：
- 影响范围：
- 前置条件：
- 完整测试地址：
- 完整交易哈希：
- Job ID / Export ID / Request ID / Trace ID：

### 复现步骤

1.
2.
3.

### 实际结果

### 预期结果

### 证据

- 页面截图：
- 请求与响应：
- PostgreSQL 查询：
- ClickHouse 查询：
- MinIO/导出文件：
- API/Worker 日志：

### 数据影响判断

- 是否写入错误 Coverage：
- 是否产生业务重复：
- 是否存在数据遗漏：
- 是否泄露 Secret：
- 是否需要停止相关任务：

### 临时绕行方式

无则写“无”，不得把绕行方式写成修复完成。
```

## 23. 最终验收报告模板

```markdown
# 链上分析平台真实数据验收结果

## 1. 结论

- 结论：通过 / 带条件通过 / 不通过
- 测试 commit：
- 测试时间：
- ANKR_MODE：
- 执行人/Agent：
- P0/P1/P2/P3 数量：

## 2. 执行摘要

| 阶段 | 计划数 | 通过 | 失败 | 阻塞 | 未执行 |
|---|---:|---:|---:|---:|---:|

## 3. 真实数据证据

- 完整地址：
- 完整交易哈希：
- 区块范围：
- Job ID：
- Export ID：
- Provider 请求增量：
- Credits/成本增量：

## 4. 数据一致性结果

| 指标 | 页面 | API | PostgreSQL | ClickHouse FINAL | 导出 | 结论 |
|---|---:|---:|---:|---:|---:|---|

## 5. 多链结果

| 链 | Head | Safe Head | Runtime | Receipt | Trace | 下载 | 结论 |
|---|---:|---:|---|---|---|---|---|

## 6. Bug 清单

| ID | 等级 | 标题 | 状态 | 证据路径 |
|---|---|---|---|---|

## 7. 未执行与阻塞项

逐项说明原因、需要的权限/数据/维护窗口，以及是否影响最终结论。

## 8. 测试数据与恢复结果

- 新增案件/标签：
- 新增任务/导出：
- 人工覆盖/Kill Switch/预算是否恢复：
- 遗留测试进程：
- 遗留测试数据：

## 9. 最终健康状态

- API PID/健康：
- Worker PID/健康：
- PostgreSQL：
- ClickHouse：
- MinIO：
- NATS：
- 未解释 WARN/ERROR：

## 10. 证据目录

列出全部截图、JSON、SQL 输出、日志、CSV/XLSX/Parquet/ZIP 和哈希文件的相对路径。
```

## 24. 给 DeepSeek 的最终执行要求

1. 严格按测试 ID 报告，不要只给概括性评价。
2. 完整保留地址、交易哈希、Job ID、Export ID、Request ID 和 Trace ID。
3. 不把自动化脚本“通过”替代人工和数据库交叉验证。
4. 不把 Mock、压力测试或历史缓存冒充本轮真实数据。
5. 不把创建任务、开始运行或收到 202 当成下载完成。
6. 不把文件成功下载当成导出正确，必须解析内容并核对行数、字段和 SHA-256。
7. 不把 ClickHouse 物理重复直接视为业务重复，必须使用 `FINAL` 和业务 Grain 判断。
8. 不擅自修复、不清库、不降低断言；发现问题按模板记录。
9. 无证据的测试项必须标记“未验证”，不得标记通过。
10. 最终将 `final-report.md`、`bugs/` 和完整 `acceptance-evidence/` 一并交回。
