// Shared network identity; operation-specific capabilities remain server-side.
export const EVM_CHAIN_VALUES = ["bsc", "ethereum", "eth", "polygon", "arbitrum", "optimism", "base", "avalanche", "fantom", "flare", "gnosis", "linea", "scroll", "somnia_mainnet", "story_mainnet", "taiko", "telos", "xlayer"] as const;
export const CHAIN_VALUES = [...EVM_CHAIN_VALUES, "tron", "solana"] as const;
export const DATASET_VALUES = ["transactions", "token_transfers", "nft_transfers", "internal_transactions", "logs", "balances"] as const;
