# BUG-007：logs 表 address 列大小写不一致（两次摄入形态不同）

- 严重等级：P3
- 状态：可复现
- 发现时间：2026-09-02 21:34 +07:00
- 环境：VM 192.168.31.152，commit 62a6d98
- 影响模块：日志摄入归一化
- 影响范围：同一合约地址在 blockchain.logs 中以小写（本轮下载）与 checksum（历史下载）两种形态共存；按地址精确查询会漏行，需 lower() 才能聚合；与其他数据集（全部小写）不一致
- 前置条件：不同时期（不同 normalizer 版本）摄入的 logs
- 完整测试地址：0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae（PancakeSwap Router）
- Job ID：3e031e80-1622-4024-b50e-39769e2adc0c（小写）与历史 08-31 任务（checksum）

### 复现步骤

1. SELECT address, count() FROM blockchain.logs FINAL WHERE lower(address)='0x16b9a82891338f9ba80e2d6970fdda79d1eb0dae' GROUP BY address → 两种形态 324/1722 行。

### 实际结果

同一地址两种形态共存（无 tx+log_index 业务重复，但查询命中不一致）。

### 预期结果

全部统一小写（与其他数据集一致）。

### 证据

- ClickHouse 查询：11-recovery/logs_case_check.sh 输出
- 数据影响：无业务重复；查询口径需 lower()
