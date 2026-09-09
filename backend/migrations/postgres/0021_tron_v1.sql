-- TRON capability is the only non-EVM row; no system-wide chain-id migration.
ALTER TABLE chain_capabilities ALTER COLUMN evm_chain_id DROP NOT NULL;
ALTER TABLE chain_capabilities ADD COLUMN IF NOT EXISTS chain_type TEXT NOT NULL DEFAULT 'evm';
ALTER TABLE chain_capabilities ADD COLUMN IF NOT EXISTS native_symbol TEXT NOT NULL DEFAULT '';

UPDATE chain_capabilities
SET chain_type='evm', native_symbol=CASE chain
  WHEN 'bsc' THEN 'BNB' WHEN 'eth' THEN 'ETH' WHEN 'polygon' THEN 'POL'
  WHEN 'arbitrum' THEN 'ETH' WHEN 'optimism' THEN 'ETH' WHEN 'base' THEN 'ETH'
  WHEN 'avalanche' THEN 'AVAX' ELSE native_symbol END
WHERE chain <> 'tron';

INSERT INTO chain_capabilities
  (chain, evm_chain_id, ankr_alias, chain_type, native_symbol,
   chainbase_tx_count, chainbase_token_count, ankr_transactions,
   ankr_token_transfers, range_supported, enabled)
VALUES ('tron', NULL, '', 'tron', 'TRX', false, false, false, false, false, true)
ON CONFLICT (chain) DO UPDATE SET
  evm_chain_id=NULL, ankr_alias='', chain_type='tron', native_symbol='TRX',
  chainbase_tx_count=false, chainbase_token_count=false,
  ankr_transactions=false, ankr_token_transfers=false,
  range_supported=false, enabled=true, updated_at=now();

-- Token metadata remains one shared cache; extend only its address guard.
ALTER TABLE token_metadata DROP CONSTRAINT IF EXISTS token_metadata_valid_address;
ALTER TABLE token_metadata ADD CONSTRAINT token_metadata_valid_address CHECK (
  (chain='tron' AND normalized_address ~ '^T[1-9A-HJ-NP-Za-km-z]{33}$') OR
  (chain<>'tron' AND normalized_address ~ '^0x[0-9a-f]{40}$')
);
