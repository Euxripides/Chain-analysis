# 阶段C+D 证据汇总 (2026-09-02)

## DL-001 输入校验 (POST /datasets/ensure)
| 用例 | 状态码 | 结果 |
|---|---|---|
| 空地址 | 400 | PASS |
| 错误长度地址 | 400 | PASS |
| 非十六进制地址 | 400 | PASS |
| 负区块 | 400 | PASS |
| from>to | 400 | PASS |
| 未知链 solana | 400 | PASS |
| 未知 Dataset | 400 | PASS |
| 坏 JSON | 400 | PASS |
| null body | 400 | PASS |
| 大小写混合+重复地址 | 202 | PASS: 规范化小写并去重为1个subject |
| 缺 chain | 202 | BUG候选: 静默默认 bsc (未报400) |
| 缺 datasets | 202 | BUG候选: 静默默认为全部5个dataset并创建真实任务 |

## DL-002 单区块真实下载
- 复用路径: job c79f39a0 (DL-0024) SKIPPED, 0 shards, 0 provider requests, 0.8s收敛 ✓
- 真实路径: job 65c96286 (DL-0030) subject=0x6808c73d7e9f1e1905c0618bb47bd6947ed4a6ae block=115394565
  - 1 provider request, 1 row, 478 bytes
  - CH落库行: tx 0xc498868807b1dfea2999f41a0fcdaa69d957e485382c2a0edbef24a0f3ac9e18 log_index=211
  - PG coverage: [115394565,115394565] row_count=1 raw_bytes=478 ✓ (无扩张)
  - MinIO raw: blockchain-raw/raw/ankr/bsc/token_transfers/2026/09/a41c1bc3.../ part-000001.json.gz + manifest.json.gz, 内容与CH一致 ✓
- 固定交易 0x2684ba96... 存在于CH (benchmark, block 98000718, LABUBU token 0xf5a3e06e...), 回执验证通过
- 观察: Ankr token_transfers 按参与者地址匹配; 以合约地址作subject返回0行(Provider语义, 非Bug)

## DL-003 五类Dataset (job 6be83a35, DL-0026, block 115394560-115394569)
- transactions/nft/logs: 真实Provider调用, 0行, 互不阻塞 ✓
- token_transfers: 仅请求缺口115394566-115394569 (5块复用) ✓
- balances: SKIPPED (快照复用) ✓
- 0行结果未提交任何Coverage ✓ (不会错误提交大范围)

## COV-001 完整复用: job ee6c6494 (DL-0031) SKIPPED, 0请求, 0新Raw, CH FINAL不变 ✓
## COV-002 部分缺口: preview ratio=0.333, gaps=[98000716,98000717]; 任务只请求缺口shard [98000716,98000717] ✓
## COV-003 并发相同任务:
- 已覆盖场景: 两任务均SKIPPED, 无重复 ✓
- 未覆盖+有行场景 (PEER2 0x680849ef... block 115394241): 两任务各1次Provider请求各下1行; CH物理4行/FINAL 1行(无业务重复) ✓; Coverage单行[115394241,115394241] row_count=2 (下载量口径, 未扩张区间)
## COV-004 四层一致性 (benchmark):
| 层 | transactions | token_transfers | nft | logs | balances |
|---|---|---|---|---|---|
| PG coverage row_count | 1 | 1 | - | - | 3 |
| CH 物理 | 9 | 479 | 0 | 0 | 3 |
| CH FINAL | 4 | 242 | 0 | 0 | 3 |
| workspace/page | 4 | 242 | 0 | 0 | 3 |
- 差异解释: 物理-FINAL差为ReplacingMergeTree版本; PG row_count只计Coverage提交任务行(不含backfill/历史下载), 语义=区间复用键而非总行数

## DATA-001 Grain: benchmark各dataset FINAL=uniq(grain) → 无业务重复 ✓
全局物理/FINAL: transactions 14.61M/9.39M; token_transfers 26.27M/26.18M; logs 12.35M/8.81M (ReplacingMergeTree版本)

## DATA-002 格式
- tx_hash: 真实行全部66位hex ✓; 合成行3-10位 (见DATA-004)
- 地址: 全部小写规范化 ✓ (uppercase=0); logs.address 存在混合大小写 (P2候选)
- timestamp: 无未来/创世前 ✓
- value_raw: 字符串存储无精度损失 ✓; 部分真实行为 "0x0" 十六进制零值 (P3观察)
- token_decimals: 18为主, 0-11均有真实分布
- block范围: 0..119,198,562 (bsc)

## DATA-003 链上抽样复核: transactions 10/10, token_transfers 10/10 (含amount/log_index/topics), logs 10/10 与RPC回执完全一致; balances 原生BNB快照=实时链上余额 8546694759625769 ✓

## DATA-004 污染范围 (只列不清除)
| 表 | FINAL总数 | 真实行(66位tx) | 占比 |
|---|---|---|---|
| transactions | 9,393,347 | 90,454 | 0.96% |
| token_transfers | 26,184,798 | 438,592 | 1.68% |
| logs | 8,808,805 | 1,871 | 0.02% |
- bench链 transactions: 500,000行
- 占位地址(0x1111/0x2222/0xaaaa/0x1234...等) transactions 8.56M行
- 0x0000...00XX低序地址 token_transfers 365,900行
- 来源任务: DL-20260831-0001..0022 (08-31, placeholder地址) + 2026-09-01批量写入8.8M transactions
