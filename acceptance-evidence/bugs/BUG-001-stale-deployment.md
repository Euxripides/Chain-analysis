# BUG-001：生产部署落后于代码基线（导出 jsonl/parquet 不可用、SHA-256 为空、token_decimals 列输出指针十六进制、前端缺少 V1.6 页面）

- 严重等级：P1
- 状态：已修复并复验通过（commit b0b1e79/7b276e4，见 final-report.md §11）
- 发现时间：2026-09-02 21:18 +07:00
- 环境：VM 192.168.31.152，commit 62a6d98（本地），ANKR_MODE=live，运行中 API 二进制构建于 2026-09-02 20:20，启动于 20:21:47
- 影响模块：导出（EXP-001/004/005）、前端部署、版本管理
- 影响范围：文档声明的 Parquet/JSONL 导出不可用；导出元数据 sha256 恒为空；CSV/XLSX 的 token_decimals 列为非确定性十六进制（Go 指针格式化）；include_meta 未输出案件/标签列；VM :3000 前端无 /api-control 路由（V1.6 缺失）
- 前置条件：无
- 完整测试地址：0x9b77e3b4f85c7cea7da451f454895edbd10b80dd
- 完整交易哈希：0xc498868807b1dfea2999f41a0fcdaa69d957e485382c2a0edbef24a0f3ac9e18
- Job ID / Export ID：64d47c9a-73b4-4fdf-8617-d1d75f04454a（CSV）、ae76210f-234a-4b14-9da2-434227d63060（XLSX）、a07b74b3-0a8a-446f-8b63-f986e6d40d8d（JSONL 尝试 400）

### 复现步骤

1. `POST /api/v1/exports` format=jsonl → 400 "format must be csv, xlsx or json"（源码 export_v2.go:51 支持 csv|xlsx|jsonl|parquet）。
2. `POST /api/v1/exports` format=parquet → 同上 400。
3. 任意 COMPLETED 导出 `GET /api/v1/exports/{id}` → `"sha256":""`。
4. 下载 CSV/XLSX 查看 token_decimals 列 → `0x37715d13f518`（CSV）与 `0x377157b505b8`（XLSX，同一行两次导出值不同，系内存地址）。
5. 打开 http://192.168.31.152:3000/api-control → 老 bundle（index-DgdpbANK.js，构建于 09-01 15:10）无该路由，页面回落到下载页。

### 实际结果

见上述。

### 预期结果

- format=jsonl/parquet 返回 202 并生成对应文件（api.md 与验收方案 §15 均要求 Parquet）。
- 导出响应包含与服务端对象一致的真实 SHA-256。
- token_decimals 输出 18（PG token_metadata.decimals=18、CH UInt16=18、JSONL 输出 18 均正确；仅 CSV/XLSX 列错误）。
- 部署前端包含 V1.6 全部路由。

### 证据

- 页面截图：无（API 级）
- 请求与响应：acceptance-evidence/06-export/ 内记录（400 响应体）
- PostgreSQL 查询：token_metadata.decimals=18（06-export/token_decimals_check.sh）
- ClickHouse 查询：token_transfers.token_decimals=18（04-data-quality/data002_format.sh）
- MinIO/导出文件：CSV 头行 token_decimals=0x37715d13f518（06-export 下载件），XLSX sheet1.xml K2=0x377157b505b8
- API/Worker 日志：无
- 版本证据：VM 二进制 /home/euripides/projects/blockchain-platform/backend/bin/api 构建于 09-02 20:20（ls -la）；API 进程 STARTED 20:21:47；VM 源码 export_v2.go mtime 09-02 20:45；VM 前端 dist 构建 09-01 15:10 且 grep api-control=0

### 数据影响判断

- 是否写入错误 Coverage：否
- 是否产生业务重复：否
- 是否存在数据遗漏：否（仅功能缺失/字段错误）
- 是否泄露 Secret：否
- 是否需要停止相关任务：否

### 临时绕行方式

导出改用 csv/xlsx/json；jsonl/parquet 与 sha256 需重新构建并部署当前源码后可用。不得把绕行方式写成修复完成。