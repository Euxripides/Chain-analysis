package ankr

import "encoding/json"

// Ankr Advanced API 响应字段（camelCase，与官方一致）。

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		TraceID string `json:"trace_id"`
	} `json:"data"`
}

type pagedResult struct {
	NextPageToken string            `json:"nextPageToken"`
	Transactions  []json.RawMessage `json:"transactions"`
	// Ankr 实际响应：token_transfers 与 nft_transfers 都放在 transfers 键下
	Transfers []json.RawMessage `json:"transfers"`
	Logs      []json.RawMessage `json:"logs"`
	// token_holders：按余额降序的持有人与总持有人数
	Holders      []json.RawMessage `json:"holders"`
	HoldersCount int64             `json:"holdersCount"`
}

type balanceResult struct {
	Assets          []json.RawMessage `json:"assets"`
	TotalBalanceUsd string            `json:"totalBalanceUsd"`
}
