# BUG-014：数据集 Coverage 统计含历史 Mock/污染数据（状态总览数字失真）

- 严重等级：P2
- 状态：新建（待负责人决策清理方案）
- 发现时间：2026-09-02 20:58 +07:00
- 环境：VM 192.168.31.152
- 影响模块：dataset_coverage（PG）、ClickHouse 数据面、状态总览/coverage stats
- 影响范围：DATA-004 污染分析（详见 04-data-quality）：
  - transactions FINAL 9,393,347 行，真实行（66 位 tx_hash）仅 90,454（0.96%）；bench 链 500,000 行；占位地址行 8,560,207
  - token_transfers FINAL 26,184,798，真实 438,592（1.68%）
  - logs FINAL 8,808,805，真实 1,871（0.02%）
  - 合成行 tx_hash 长度 3~10；来源任务 DL-20260831-0001..0022（占位地址 0x1111/0x2222/0xaaaa/0x0000...00XX 等）与 2026-09-01 批量 8.8M 写入
  - dataset_coverage 中 0x16b9a828... logs 行 row_count=898312（实际 CH 仅 1722 行）；全局 coverage stats logs:rows=13,682,422、token_transfers:rows=23,175,998 等被严重放大
- 复现步骤：见 04-data-quality/data004b_pollution.sh 输出
- 实际结果：状态总览与 Coverage 统计被污染数据主导
- 预期结果：仓库数据与“排除已确认测试数据”两套统计分列，供清理决策
- 证据：04-data-quality/data004_pollution.sh、data004b_pollution.sh、03-coverage/cov004_consistency.sh
- 数据影响：未执行任何删除（仅列清单）
