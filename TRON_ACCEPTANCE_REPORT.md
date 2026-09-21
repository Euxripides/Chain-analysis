# TRON 最小侵入式接入验收报告

验收日期：2026-09-06  
虚拟机：`dataserver` / `192.168.31.152`  
部署目录：`/home/euripides/projects/blockchain-platform`

## 结论

- 功能开发、部署与保守流量下的真实链路：**PASS**。
- 文档要求的最终生产放行 Gate：**NO-GO**。唯一未闭环项是缺少 TronGrid API Key，因此未执行 `100 transactions / 1,000 transfers / 10,000 transfers` 容量测试和三类数据各 100 条的随机对照。当前按匿名 TronGrid 保守限流运行，不建议直接用于高活跃 USDT 地址的大规模任务。

## 已验收范围

### Chain / Address

- `GET /api/v1/chains/capabilities` 返回 7 条 EVM 链和 `tron`；TRON 为 `chain_type=tron`、`evm_chain_id=null`、`native_symbol=TRX`。
- 前端可选 TRON，仅显示 Transactions、TRC20、Internal 三类数据集。
- Base58Check `T...` 和 `41...` hex 独立校验，校验 checksum，内部统一为大小写敏感的 `T...`；不执行 lowercase。
- 合法未激活地址与 invalid 地址分离；EOA / Contract 状态独立解析。

### Download / Data

- Transactions、TRC20、Internal 真实 TronGrid 任务均已 COMPLETED。
- 组合任务：`69256048-8c56-4b14-b4cc-25209abe422f`。
- TRC20 任务：`c1bd116e-44d4-444b-b1c1-f4987210c359`。
- Internal 任务：`2b492fbc-98d8-4890-9e89-fd2b332e84c9`。
- 写入复用现有 `transactions`、`token_transfers`、`internal_calls`、`address_activity`，未新建 TRON 平行业务表。
- TRX / TRC20 保留 raw integer；实测 TRX `8888888`，USDT `1000000`。
- TRC20 重放后 ClickHouse `FINAL` 仍为 1 条，`uniqExact(tx_hash,event_index)=1`，未发生金额翻倍。
- 库中 56 条 rejected Internal 记录对应的 `address_activity` 泄漏边数为 `0`。
- checkpoint 在 raw spool 持久化后推进，保存 fingerprint 和 `tron_last_timestamp_ms`；fingerprint 失效时带时间回叠重建。

### 增量 / 导出 / 产品

- 增量预检 ID：`79a34a14-2a6f-4796-94b1-aa2533844a06`。
- 增量任务：`6d709e01-f325-499f-a9d4-3a7be29b9d17`，`range_type=time`，从已有最新时间前 3 分钟回叠，重新获取 1 条后 COMPLETED，最终唯一行数仍为 1。
- CSV 导出 ID：`7e043ab1-aebe-47bb-8060-19645437ee44`，地址大小写保留。
- XLSX 导出 ID：`a3e5351e-ad74-4269-9d06-a9fa29e49cab`，1 行，SHA-256 `c76265565ac5e3484dec0bbb764ccf7859fb359603f55391875a995d9c074cf5`；下载后确认为有效 Excel 2007+ 容器，单元格内实际包含完整 TRON 地址、USDT 合约地址和 raw amount `1000000`。
- Explorer、TronScan 链接、Graph 共享活动层、Investigation 数据查询、Token Metadata 共享缓存、Provider Usage 监控均已接入。

### Regression / Build

- Linux：`go test ./...` PASS，`go vet ./...` PASS。
- 虚拟机 TRON 定向：`go test ./internal/tron` PASS。
- Frontend：`npm run build` PASS；仅有原有大 chunk 体积警告，无 CSS / TypeScript 错误。
- 真实浏览器：TRON 与既有 EVM 页面的 desktop / mobile 共 4 项 Playwright 测试全部 PASS，无 console/page error。
- API / Worker 启动时对 BSC、ETH、Polygon、Arbitrum、Base、Avalanche、Optimism 七链的 RPC capability probe 均完成；既有 Ankr / RPC / SQD 单元与集成回归通过。

## 当前运行状态

- API `http://192.168.31.152:8080`：ready，PostgreSQL / ClickHouse / NATS / MinIO 全部 ok。
- Frontend `http://192.168.31.152:3000`：ready。
- Worker metrics `http://192.168.31.152:8081/metrics`：ready。
- `TRON_ENABLED=true`，`TRONGRID_API_KEY` 当前为空；日志只记录 `api_key_configured=false`，不输出密钥。

## 未完成的文档强制放行项

1. `100 transactions`、`1,000 transfers`、`10,000 transfers`、高活跃 USDT 地址性能阶梯，包括 P50/P95、429/403/5xx/retry rate 和 DB write rate。
2. 随机抽样各 100 条 Transactions / TRC20 / Internal，逐字段对照可信 Explorer / Provider。
3. 基于上述容量数据确定生产 `initial concurrency` / `max concurrency`。

解锁条件：配置可用的 `TRONGRID_API_KEY`后，执行以上三项并保存源记录、请求统计、对照结果和导出证据，才可将最终生产放行结论改为 GO。
