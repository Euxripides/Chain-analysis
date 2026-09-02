# BUG-003：创建任务时缺 chain / 缺 datasets 被静默填充默认值（应返回 400）

- 严重等级：P2
- 状态：可复现
- 发现时间：2026-09-02 20:54 +07:00
- 环境：VM 192.168.31.152，commit 62a6d98，ANKR_MODE=live
- 影响模块：Dataset Service（POST /api/v1/datasets/ensure）输入校验
- 影响范围：前端漏传参数时，后端静默补全（chain→bsc、datasets→全部5类），与本方案 DL-001 预期“API 返回 400，而不是静默改成默认值”不符；验收期间因此产生了两个非预期真实任务 DL-20260902-0021/0022
- 前置条件：无
- 完整测试地址：0x9b77e3b4f85c7cea7da451f454895edbd10b80dd
- 完整交易哈希：不适用
- Job ID：997f403b-07c9-493f-9cba-2beac6adfa46（缺 chain）、43a7e39b-889e-4804-b68b-d050483014a3（缺 datasets）

### 复现步骤

1. POST /datasets/ensure，省略 chain 字段 → 202，任务按 bsc 创建。
2. POST /datasets/ensure，省略 datasets 字段 → 202，任务按 5 类 Dataset 创建并真实执行。

### 实际结果

两个任务被创建且 COMPLETED（含 1 次以上 Provider 请求）。

### 预期结果

400 + 明确错误信息，或至少在响应中回显所采用的默认值。

### 证据

- 请求与响应：acceptance-evidence/02-download/C-D-phase-summary.md（DL-001 表）
- PostgreSQL 查询：download_jobs DL-20260902-0021/0022
- 数据影响：无错误 Coverage（区间正确）；产生少量测试任务记录（已记录为测试数据，见最终报告 §8）

### 临时绕行方式

前端始终显式传 chain 与 datasets。
