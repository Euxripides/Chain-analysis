// OKLink-style reading order: transaction identity first, then block/time,
// participants, asset/amount, and finally technical metadata.
export const DATASET_FIELD_ORDER: Record<string, string[]> = {
  activity: [
    'tx_hash', 'block_number', 'timestamp', 'activity_type', 'direction',
    'counterparty', 'token_symbol', 'amount',
  ],
  transactions: [
    'tx_hash', 'method_name', 'method_id', 'method_signature', 'method_source',
    'method_verified', 'block_number', 'timestamp', 'from_address',
    'to_address', 'value', 'value_raw', 'status',
  ],
  token_transfers: [
    'tx_hash', 'block_number', 'timestamp', 'from_address', 'to_address',
    'token_symbol', 'amount', 'token_address',
  ],
  nft_transfers: [
    'tx_hash', 'block_number', 'timestamp', 'from_address', 'to_address',
    'collection_name', 'token_symbol', 'token_id', 'contract_address',
  ],
  logs: [
    'tx_hash', 'block_number', 'timestamp', 'log_index', 'address', 'event_name',
    'event_signature', 'topics', 'data', 'removed',
  ],
}

export function orderedFields(
  row: Record<string, unknown> | undefined,
  dataset: string,
  options: { exclude?: string[]; appendRemaining?: boolean } = {},
): string[] {
  if (!row) return []

  const excluded = new Set(options.exclude ?? [])
  const available = Object.keys(row).filter((field) => !excluded.has(field))
  const preferred = (DATASET_FIELD_ORDER[dataset] ?? []).filter((field) => available.includes(field))

  if (!options.appendRemaining) return preferred
  const preferredSet = new Set(preferred)
  return [...preferred, ...available.filter((field) => !preferredSet.has(field))]
}
