# BUG-015：transactions.value_raw 零值以 0x0 十六进制存储（格式不一致）

- 严重等级：P3
- 状态：可复现
- 发现时间：2026-09-02 21:15 +07:00
- 环境：VM 192.168.31.152
- 影响模块：交易摄入归一化
- 影响范围：DATA-002 格式检查：真实行中零值交易的 value_raw 为 "0x0"（十六进制），与十进制字符串约定不一致（非零值均为十进制）；影响下游按十进制解析的消费者
- 复现步骤：SELECT value_raw FROM blockchain.transactions FINAL WHERE chain='bsc' AND value_raw='0x0'（真实行样本 10 行中 10 行为 0x0）
- 实际结果：0x0 十六进制格式
- 预期结果：统一十进制字符串 "0"
- 证据：04-data-quality/data002_format.sh、data003_samples.sh
- 数据影响：无
