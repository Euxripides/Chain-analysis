-- nft_transfers 补 token_standard（ERC721 / ERC1155 / Solana SPL）。
--
-- 背景：NFT 转账的"标准"此前没有落库——Ankr 的 `ankr_getNftTransfers` 行里本来就有
-- `type: "ERC721"`（2026-09-20 用 BAYC tx 0xda308d… 的 raw 对象实测确认），但归一化
-- 与表结构都没有接住它，于是 ERC721 与 ERC1155 在库里无法区分。
-- collection 仍由 token_name/token_symbol 承载（Ankr 给的是 collectionName/Symbol）。
--
-- 幂等：ADD COLUMN IF NOT EXISTS；既有行取默认空串。

ALTER TABLE blockchain.nft_transfers ADD COLUMN IF NOT EXISTS token_standard LowCardinality(String) DEFAULT '';
