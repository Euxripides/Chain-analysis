-- ADR-023 D6 / D10：Solana 转账的金额口径与代币账户证据。
--
-- 背景：Solana 的 SPL Token / Token-2022 转账来自指令解析而不是 EVM 日志：
--   * Token-2022 带费转账下"指令金额 ≠ 收款人实收"，必须能区分 gross / net / fee；
--   * 指令里的 source/destination 是代币账户（ATA）而不是钱包，证据详情需要能追溯到它们。
--
-- 口径（全链统一，见 ADR-023 D6）：
--   amount_raw        = 净额（实际移动/到账）；EVM/TRON 与带费链都适用
--   gross_amount_raw  = 链上声明的转账额
--   fee_amount_raw    = 被代扣的手续费；没有费用概念的链写 "0"
--   不变量：amount_raw = gross_amount_raw - fee_amount_raw（写入点兜底，见 ingest.Writer）
--
-- 幂等：ADD COLUMN IF NOT EXISTS；既有行取默认空串，不重写历史数据。

ALTER TABLE blockchain.token_transfers ADD COLUMN IF NOT EXISTS gross_amount_raw String DEFAULT '';
ALTER TABLE blockchain.token_transfers ADD COLUMN IF NOT EXISTS fee_amount_raw String DEFAULT '';
ALTER TABLE blockchain.token_transfers ADD COLUMN IF NOT EXISTS from_token_account String DEFAULT '';
ALTER TABLE blockchain.token_transfers ADD COLUMN IF NOT EXISTS to_token_account String DEFAULT '';

-- address_activity 是地址详情的读模型：证据详情（ADR-023 D10）需要主体/对手方的 ATA 与手续费，
-- 若只加在 token_transfers 上，地址详情就得为每行再查一次明细表（N+1 查询）。
ALTER TABLE blockchain.address_activity ADD COLUMN IF NOT EXISTS token_account String DEFAULT '';
ALTER TABLE blockchain.address_activity ADD COLUMN IF NOT EXISTS counterparty_token_account String DEFAULT '';
ALTER TABLE blockchain.address_activity ADD COLUMN IF NOT EXISTS fee_amount_raw String DEFAULT '';
