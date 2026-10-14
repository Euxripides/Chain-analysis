# 合约分析运行与发布

入口为 https://aurore.online/app-v2/contract-analyses，MCP 沿用 /mcp，OpenAPI 为 /contract-analysis-openapi.json。在当前工作区选择 EVM 链、完整地址和固定区块/时间，按需要给出交易哈希、历史范围与预算。方法、证据、分页和版本比较保持 URL 定位。

## 运行位置

候选目录：/home/euripides/projects/blockchain-platform-worktrees/contract-analysis-20261014。原目录与原服务保留，部署文件及哈希见 acceptance/contract-analysis/release/manifest.json。

| 服务 | 职责 |
|---|---|
| blockchain-contract-api.service | 127.0.0.1:18084，feature-only API |
| blockchain-contract-worker.service | 专用 processor，重型并发 1 |
| blockchain-contract-mcp.service | 119 工具；旧工具使用原 API，保留原 OAuth 权威 |
| blockchain-contract-console.service | 127.0.0.1:18086，已验收产物和旧资源 |

服务只引用私有环境文件。密钥、密码、Cookie 和 OAuth token 不进入源码、公开文档和证据包。

## 预算与故障

提交固定区块、哈希、代码、引擎与原始输入。恢复复用 snapshot 和处理版本；改变引擎时拒绝混用原任务。读取已保存结果不重新访问链。

RPC、credits、状态读取与处理时间使用持久预算，提交固定区块、重试、取消和崩溃均保留消耗。费用核对使用 status.metrics、同 job_id 的 provider_usage 和 contract_analysis_budgets；estimated_usd 是配置单价估算，独立于链上原始金额。

故障先检查 error_code、phase、coverage、engine_runs。恢复时核对 snapshot 不变。源码身份不符、预算、超时、重放差异、witness 缺失分别记录，不以改区块、补零或修改既有 SHA256 消除错误。

取消以 fence 阻止旧发布并清理归属容器；崩溃恢复只清理同任务旧 fence。日志按 unit 读取，禁止输出私有环境文件。

## 发布与回退

controlled_release.py prepare 检查原基线、报告和文件；deploy 应用增量迁移、新服务及 nginx，异常恢复原入口。

disable_submissions 关闭新提交、停止新 worker，保留查询与工件。rollback 恢复旧用户端和 MCP，保留数据库及对象；重新启用后可读取既有任务。回退不删表、不删证据。

公开 OpenAPI 副本位于 /var/www/html/contract-analysis/openapi.json；源码目录和私有证据不通过 alias 公开。

## 证据

acceptance/contract-analysis/implementation-status.md 汇总实际检查。其目录包含报告、PNG、交易 witness、重放、费用、ZIP、审计链及客户端 schema 比对。功能支持和未实现范围见 docs/contract-analysis-capabilities.md。
