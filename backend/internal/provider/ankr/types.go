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
}

type pagedResult struct {
	NextPageToken  string            `json:"nextPageToken"`
	Transactions   []json.RawMessage `json:"transactions"`
	TokenTransfers []json.RawMessage `json:"tokenTransfers"`
	NftTransfers   []json.RawMessage `json:"nftTransfers"`
	Logs           []json.RawMessage `json:"logs"`
}

type balanceResult struct {
	Assets          []json.RawMessage `json:"assets"`
	TotalBalanceUsd string            `json:"totalBalanceUsd"`
}
