# 阶段 I 汇总：多链 RPC

- RPC-001 PASS: bsc/eth/polygon/arbitrum/base/avalanche/optimism 七链 head 全部 200；head_block>0 且 safe_block<head_block（确认深度：bsc15/eth12/polygon64/arb20/base20/avax20/op20，与 Chain Registry 一致）；未知链 solana→400 invalid_params
- RPC-002 PASS: /system/chains/{chain}/capabilities 与 PG rpc_provider_capabilities 完全一致（56 行 ankr+mock-rpc 探测记录）；polygon archive=false 为真实探测结果且 API 同步降级展示；grpc/websocket 均 false（诚实标注）
- RPC-003 PASS: EOA（benchmark, code_size=0）与合约（USDT, code_size=4413）分类正确；原生余额字符串无精度损失（8546694759625769，与 balances 快照一致）；非法地址→400 "address must be 20 bytes"；重复请求命中缓存不重复计费（CODE 请求数仅 +2）
- RPC-004 PASS: 回执必填字段完整（status/block/from/to/gas/logs）；trace local_only→404（不访问 Provider）、auto→200 归一化调用树、viewer→403；internal-calls 持久化后不触发 Provider（count=1 且无新 usage）
- RPC-005 PASS: 无公开 Raw RPC（/rpc、/eth、/debug/rpc 全部 404）；坏哈希→400；坏链→400；块过滤器反向→400；超大 limit 被钳制；错误不含 endpoint/API Key
- 真实 Provider 错误分类验证（REC-007 关联）: upstream / method_unsupported / timeout 分类正确写入 provider_usage + 审计
