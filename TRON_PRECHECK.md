# TRON V1 Phase 0 Precheck

检查日期：2026-09-06  
检查范围：`dataserver:/home/euripides/projects/blockchain-platform` 与当前同步源码。

| 检查项 | 实际情况 | 最小兼容结论 |
|---|---|---|
| ClickHouse 地址字段 | `transactions`、`token_transfers`、`internal_calls`、`address_activity` 均为 `String` | 无需修改地址字段类型 |
| ClickHouse 幂等键 | 交易 `(chain, tx_hash)`；Token `(chain, tx_hash, log_index, token_address)`；Internal `(chain, tx_hash, call_index)` | 可直接容纳 TRON；TRC20 必须生成稳定 `log_index` |
| ClickHouse 分区 | 事实表均包含 `chain`，交易/Token 按链和月份分区 | 无需新建 TRON 平行表 |
| Graph / 资金流 | 资金流使用统一 `address_activity`，事实查询带 `chain`；未发现独立 Graph 后端 | 复用统一活动表，节点身份保持 `(chain,address)` |
| Investigation | RPC 调查入口为 EVM 专用；下载后的调查浏览使用统一 Explorer/ClickHouse | TRON V1 复用下载、Explorer 和活动数据，不调用 EVM RPC 深度接口 |
| Download Job | `download_jobs.chain`、`download_subjects.subject` 为文本 | 可直接保存 `chain=tron` 与 Base58 地址 |
| Capability | `evm_chain_id BIGINT NOT NULL` | 仅该列改为 nullable，并插入 `tron` capability |
| provider_usage | provider/chain/usage_type/error/status/latency/retry/rows 字段已存在 | 直接记录 `provider=trongrid, chain=tron` |
| token_metadata | 地址长度足够，但现有安全约束限定 EVM `0x` 地址 | V1 使用 TronGrid TRC20 响应内 metadata，不写 EVM-only metadata cache |
| Checkpoint | `download_shards.page_token TEXT`，页面在 durable spool 后 CAS 推进 | 可直接保存 TronGrid fingerprint，无新 checkpoint 表 |
| Durable spool | 原始页 + manifest 先 fsync，后推进 cursor | 直接复用；满足 cursor 不早于持久化 |
| Export | CSV/XLSX/Parquet 使用动态字符串，无 42 字符固定类型 | 地址字段无需迁移 |
| Explorer | 查询层和部分前端代码会 lowercase 地址，过滤器硬编码 EVM 正则 | 改为 chain-aware 规范化，增加 TRON URL formatter |
| Internal 数据 | 已有 `internal_calls`，但缺少 `block_number/timestamp` | 原表增列并复用，不新建业务表 |

结论：不存在全库链模型迁移的必要。实施范围限定为独立 `internal/tron`、一次 Provider 分流、chain-aware 地址入口、统一 writer/query 的小幅兼容和两条增量 migration。
