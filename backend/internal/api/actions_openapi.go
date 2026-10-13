package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

type actionOperation struct {
	Method, Path, ID, Summary, Description string
	Body                                   bool
}

var actionOperations = []actionOperation{
	{"get", "/api/v1/bridge/capabilities", "getCrossChainCapabilities", "Get bridge capabilities", "Read implemented adapters, query identifiers and credential configuration.", false},
	{"post", "/api/v1/bridge/transfer", "queryCrossChainTransfer", "Query a cross-chain transfer", "Read protocol association by hash or existing bridge identifier; inspect attempts and per-leg verification.", true},
	{"post", "/api/v1/bridge/candidates", "findCrossChainCandidates", "Find cross-chain candidates", "Bounded exact-decimal candidate search with explicit addresses, time range and asset; candidates are not verified pairs.", true},
	{"get", "/api/v1/address/overview", "getAddressOverview", "Get address overview", "Use first for a compact address summary. Do not use for raw history.", false},
	{"get", "/api/v1/address/assets", "getAddressAssets", "Get address assets", "Use for current balance snapshots, not historical cash flow.", false},
	{"get", "/api/v1/address/transactions", "getTransactions", "List normal transactions", "Use for filtered paginated transactions; use flow summary for totals.", false},
	{"get", "/api/v1/address/token-transfers", "getTokenTransfers", "List token transfers", "Use for filtered paginated transfers; use export for millions of rows. status matches the transaction's outcome and only matches transfers whose transaction row is indexed too; transfers are derived from emitted logs, so failed transactions contribute none.", false},
	{"get", "/api/v1/address/nft-transfers", "getNFTTransfers", "List NFT transfers", "Read paginated indexed NFT events; quantities and token IDs remain exact strings.", false},
	{"get", "/api/v1/address/logs", "getAddressLogs", "List contract logs", "Read local indexed logs emitted by address, without provider requests.", false},
	{"get", "/api/v1/address/balance-snapshots", "getBalanceSnapshots", "List stored balance snapshots", "Latest stored observation per asset; time filters select observation timestamps rather than reconstruct historical state.", false},
	{"get", "/api/v1/address/internal-transactions", "getInternalTransactions", "List internal transactions", "Use for normalized internal calls, not raw traces.", false},
	{"get", "/api/v1/address/counterparties", "getCounterparties", "Aggregate counterparties", "Rank counterparties without downloading complete history.", false},
	{"get", "/api/v1/address/flow-summary", "getFlowSummary", "Get flow summary", "Return server-side incoming, outgoing, net flow, counts and time bounds.", false},
	{"get", "/api/v1/address/top-flows", "getTopFlows", "Get top flows", "Return the largest counterparties or transfers.", false},
	{"get", "/api/v1/address/first-funding", "getFirstFunding", "Get first funding", "Return earliest native and token funding records available locally.", false},
	{"get", "/api/v1/transaction/detail", "getTransactionDetail", "Get transaction detail", "Return one transaction with decoded input, transfers, internal calls and logs. The local transactions dataset can be missing for an address while its token transfers are indexed: the response then omits transaction and sets meta.transaction_row_present=false, while transfers, internal calls and logs are still returned. A missing transaction row is not proof that the transaction does not exist.", false},
	{"get", "/api/v1/transaction/trace", "getTransactionTrace", "Get transaction trace", "Use when a contract call hierarchy is required. Local cache is preferred; a bounded provider request may be made on a cache miss.", false},
	{"get", "/api/v1/transaction/logs", "getTransactionLogs", "Get transaction logs", "Use for event analysis of one transaction.", false},
	{"get", "/api/v1/transaction/decode", "decodeTransaction", "Decode transaction", "Return cached decoded input or resolve it through the configured bounded provider path.", false},
	{"get", "/api/v1/solana/address/signatures", "getSolanaAddressSignatures", "List Solana address signatures", "Read one bounded finalized signature page directly from RPC; this does not assert full-history coverage.", false},
	{"get", "/api/v1/solana/transaction", "getSolanaTransaction", "Read Solana transaction", "Return one finalized parsed source-chain transaction when the configured RPC can read it.", false},
	{"get", "/api/v1/bridge/source-transaction", "getBridgeSourceTransaction", "Read bridge source transaction", "Read a known source transaction by chain and full hash. An order ID is a correlation label and does not prove bridge-order association.", false},
	{"post", "/api/v1/addresses/batch-overview", "batchGetAddressOverview", "Batch address overview", "Compare 1 to 200 addresses.", true},
	{"post", "/api/v1/addresses/batch-assets", "batchGetAddressAssets", "Batch address assets", "Screen current assets for 1 to 200 addresses.", true},
	{"post", "/api/v1/analysis/path", "findFundPaths", "Find fund paths", "Bounded multi-hop traversal, capped at depth 8 and 2000 nodes.", true},
	{"post", "/api/v1/analysis/address-network", "buildAddressNetwork", "Build address network", "Return nodes and aggregated edges from seed addresses.", true},
	{"post", "/api/v1/export/create", "createExport", "Create export", "Create an asynchronous export instead of paging huge JSON histories.", true},
	{"get", "/api/v1/export/status", "getExportStatus", "Get export status", "Poll an export job and download only after completion.", false},
	{"get", "/api/v1/export/address-summary", "getExportAddressSummary", "Summarize completed export by payer address", "Server-side CSV/Parquet token-transfer summary. Specify token or token_contract. Totals cover every matched exported record; addresses and original transfer details paginate independently. Data cutoff is the highest observed token-transfer block in the export, not a coverage claim. Cursor is bound to the file and filters and expires on API restart.", false},
	{"post", "/api/v1/data/ensure", "ensureChainData", "Ensure chain data", "Use the optimized system downloader to fill only missing Coverage. AI jobs are marked source=chatgpt_mcp.", true},
	{"get", "/api/v1/data/download-status", "getAIDownloadStatus", "Get AI download status", "Poll an AI-managed system download job by download_job_id or job_no.", false},
	{"post", "/api/v1/data/download-retry", "retryAIDownloadFailures", "Retry failed download parts", "Retry only failed or unrecoverable shards of an AI-managed download. Completed shards and successful checkpoints are preserved.", true},
}

func actionsOpenAPIDocument() map[string]any {
	paths := map[string]any{}
	for _, op := range actionOperations {
		responseRef := "#/components/schemas/" + actionResponseSchemaName(op.ID)
		entry := map[string]any{"operationId": op.ID, "summary": op.Summary, "description": op.Description, "security": []any{map[string]any{"bearerAuth": []any{}}}, "responses": map[string]any{"200": actionResponse("Successful structured response", responseRef), "400": map[string]any{"description": "Invalid request"}, "401": map[string]any{"description": "Invalid API key"}, "429": map[string]any{"description": "Rate limited"}}}
		if op.ID == "createExport" {
			entry["responses"].(map[string]any)["202"] = actionResponse("Export accepted", responseRef)
			delete(entry["responses"].(map[string]any), "200")
		}
		if op.Body {
			entry["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": actionRequestSchema(op.ID)}}}
		} else {
			entry["parameters"] = actionParameters(op.ID)
		}
		if paths[op.Path] == nil {
			paths[op.Path] = map[string]any{}
		}
		paths[op.Path].(map[string]any)[op.Method] = entry
	}
	addInvestigationOpenAPI(paths)
	return map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "Blockchain Investigation GPT Actions API", "version": "1.1.0", "description": "Evidence-aware blockchain investigation queries for AI clients. Monetary values are decimal strings, identifiers are complete, and current-balance claims require meta.coverage and meta.freshness."}, "servers": []any{map[string]any{"url": "https://your-domain.example"}}, "paths": paths, "components": map[string]any{"securitySchemes": map[string]any{"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"}}, "schemas": actionSchemas()}}
}

func actionResponse(description, ref string) map[string]any {
	return map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": ref}}}}
}

func actionParameters(id string) []any {
	names := map[string][]string{
		"getAddressOverview": {"chain", "address"}, "getAddressAssets": {"chain", "address"}, "getFirstFunding": {"chain", "address"},
		"getTransactions":         {"chain", "address", "direction", "start_time", "end_time", "min_value", "max_value", "counterparty", "method_id", "status", "page", "page_size", "sort"},
		"getTokenTransfers":       {"chain", "address", "direction", "token", "token_contract", "start_time", "end_time", "min_amount", "max_amount", "counterparty", "from_address", "to_address", "status", "page", "page_size", "sort"},
		"getNFTTransfers":         {"chain", "address", "direction", "token", "token_contract", "token_id", "start_time", "end_time", "counterparty", "from_address", "to_address", "page", "page_size", "sort"},
		"getAddressLogs":          {"chain", "address", "start_time", "end_time", "page", "page_size", "sort"},
		"getBalanceSnapshots":     {"chain", "address", "token", "token_contract", "start_time", "end_time", "page", "page_size", "sort"},
		"getInternalTransactions": {"chain", "address", "direction", "start_time", "end_time", "counterparty", "min_value", "max_value", "page", "page_size", "sort"},
		"getCounterparties":       {"chain", "address", "token", "token_contract", "direction", "start_time", "end_time", "min_total_amount", "min_tx_count", "sort_by", "limit"},
		"getFlowSummary":          {"chain", "address", "token", "token_contract", "start_time", "end_time"}, "getTopFlows": {"chain", "address", "direction", "token", "token_contract", "group_by", "limit", "start_time", "end_time"},
		"getTransactionDetail": {"chain", "tx_hash"}, "getTransactionTrace": {"chain", "tx_hash"}, "getTransactionLogs": {"chain", "tx_hash"}, "decodeTransaction": {"chain", "tx_hash"}, "getExportStatus": {"job_id"}, "getAIDownloadStatus": {"download_job_id", "job_no"},
		"getExportAddressSummary":    {"job_id", "token", "token_contract", "address", "direction", "from_address", "page_size", "cursor", "detail_page_size", "detail_cursor"},
		"getSolanaAddressSignatures": {"address", "before", "limit"}, "getSolanaTransaction": {"signature"}, "getBridgeSourceTransaction": {"source_chain", "source_tx_hash", "bridge_order_id"},
	}[id]
	out := make([]any, 0, len(names))
	for _, name := range names {
		schema := map[string]any{"type": "string"}
		if name == "chain" {
			schema["enum"] = actionChainValues()
		}
		if name == "source_chain" {
			schema["enum"] = actionChainValues()
		}
		switch name {
		case "direction":
			schema = map[string]any{"type": "string", "enum": []string{"all", "in", "out"}, "default": "all"}
		case "sort":
			schema = map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "default": "desc"}
		case "sort_by":
			schema = map[string]any{"type": "string", "enum": []string{"tx_count", "total_amount", "net_flow", "last_time"}, "default": "total_amount"}
		case "group_by":
			schema = map[string]any{"type": "string", "enum": []string{"address", "transaction"}, "default": "address"}
		case "status":
			schema = map[string]any{"type": "string", "enum": []string{"success", "failed"}}
		}
		if name == "page" || name == "page_size" || name == "detail_page_size" || name == "limit" || name == "min_tx_count" {
			schema = map[string]any{"type": "integer", "minimum": 1}
			if name == "page" {
				schema["default"] = 1
			}
			if name == "page_size" || name == "detail_page_size" {
				schema["maximum"] = 500
				schema["default"] = 100
			}
			if name == "limit" {
				if id == "getSolanaAddressSignatures" {
					schema["maximum"] = 100
					schema["default"] = 20
				} else if id == "getTopFlows" {
					schema["maximum"] = 200
					schema["default"] = 20
				} else {
					schema["maximum"] = 200
					schema["default"] = 50
				}
			}
		}
		if name == "start_time" || name == "end_time" {
			schema["format"] = "date-time"
		}
		if name == "job_id" || name == "download_job_id" {
			schema["format"] = "uuid"
		}
		if name == "cursor" || name == "detail_cursor" {
			schema["maxLength"] = 2048
		}
		out = append(out, map[string]any{"name": name, "in": "query", "required": (name == "address" && id != "getExportAddressSummary") || name == "chain" || name == "tx_hash" || name == "job_id" || name == "signature" || name == "source_chain" || name == "source_tx_hash", "schema": schema})
	}
	return out
}

func actionRequestSchema(id string) map[string]any {
	if schema := crossChainRequestSchema(id); schema != nil {
		return schema
	}
	str := map[string]any{"type": "string"}
	chainSchema := map[string]any{"type": "string", "enum": actionChainValues()}
	downloadChainSchema := map[string]any{"type": "string", "enum": actionChainValues()}
	addressArray := map[string]any{"type": "array", "minItems": 1, "maxItems": 200, "items": str}
	props := map[string]any{}
	required := []string{}
	switch id {
	case "batchGetAddressOverview":
		props = map[string]any{"chain": chainSchema, "addresses": addressArray}
		required = []string{"chain", "addresses"}
	case "batchGetAddressAssets":
		props = map[string]any{"chain": chainSchema, "addresses": addressArray, "token": str, "token_contract": str, "min_balance": str}
		required = []string{"chain", "addresses"}
	case "findFundPaths":
		props = map[string]any{"chain": chainSchema, "source": str, "token": str, "token_contract": str, "max_depth": map[string]any{"type": "integer", "minimum": 1, "maximum": 8, "default": 3}, "min_amount": str, "max_nodes": map[string]any{"type": "integer", "minimum": 1, "maximum": 2000, "default": 200}, "start_time": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "end_time": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "exclude_addresses": addressArray}
		required = []string{"chain", "source"}
	case "buildAddressNetwork":
		props = map[string]any{"chain": chainSchema, "seed_addresses": addressArray, "token": str, "token_contract": str, "start_time": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "end_time": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "max_depth": map[string]any{"type": "integer", "minimum": 1, "maximum": 8, "default": 3}, "min_total_amount": str, "max_nodes": map[string]any{"type": "integer", "minimum": 1, "maximum": 2000, "default": 200}}
		required = []string{"chain", "seed_addresses"}
	case "createExport":
		props = map[string]any{"chain": chainSchema, "address": str, "addresses": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": str}, "data_types": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "enum": []string{"transactions", "token_transfers", "nft_transfers", "internal_transactions", "logs", "balances"}}}, "token": str, "token_contract": str, "start_time": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "end_time": map[string]any{"type": []string{"string", "null"}, "format": "date-time"}, "format": map[string]any{"type": "string", "enum": []string{"parquet", "csv"}, "default": "parquet"}, "compression": map[string]any{"type": "string", "enum": []string{"zstd", "zip"}, "default": "zstd"}}
		required = []string{"chain", "data_types", "format", "compression"}
	case "ensureChainData":
		props = map[string]any{
			"chain":     downloadChainSchema,
			"addresses": addressArray,
			"datasets":  map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "enum": []string{"transactions", "token_transfers", "nft_transfers", "internal_transactions", "logs", "balances"}}},
			"range": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"type"},
				"properties": map[string]any{
					"type": map[string]any{"type": "string", "enum": []string{"full", "block", "time"}},
					// block 区间用 from_block/to_block（整数，含端点）；time 区间用
					// from/to（RFC3339）。旧 schema 只写了 from/to 且允许 integer，
					// 按它传 block 区间会命中 dataset.RangeSpec 的 time.Time 解析并
					// 返回 400 Time.UnmarshalJSON（并非下载失败）。
					"from_block": map[string]any{"type": []string{"integer", "null"}, "description": "Inclusive first block; required when type=block."},
					"to_block":   map[string]any{"type": []string{"integer", "null"}, "description": "Inclusive last block; required when type=block."},
					"from":       map[string]any{"type": []string{"string", "null"}, "format": "date-time", "description": "Inclusive start time (RFC3339); required when type=time."},
					"to":         map[string]any{"type": []string{"string", "null"}, "format": "date-time", "description": "Inclusive end time (RFC3339); required when type=time."},
				},
			},
			"performance_mode": map[string]any{"type": "string", "enum": []string{"safe", "fast", "turbo"}, "default": "fast"},
		}
		required = []string{"chain", "addresses", "datasets", "range"}
	case "retryAIDownloadFailures":
		props = map[string]any{"download_job_id": map[string]any{"type": "string", "format": "uuid"}}
		required = []string{"download_job_id"}
	}
	out := map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}
	if id == "findFundPaths" || id == "buildAddressNetwork" {
		out["anyOf"] = []any{map[string]any{"required": []string{"token"}}, map[string]any{"required": []string{"token_contract"}}}
	}
	if id == "createExport" {
		out["allOf"] = []any{map[string]any{"oneOf": []any{
			map[string]any{"required": []string{"address"}, "not": map[string]any{"required": []string{"addresses"}}},
			map[string]any{"required": []string{"addresses"}, "not": map[string]any{"required": []string{"address"}}},
		}}}
		out["oneOf"] = []any{
			map[string]any{"required": []string{"format", "compression"}, "properties": map[string]any{"format": map[string]any{"const": "parquet"}, "compression": map[string]any{"const": "zstd"}}},
			map[string]any{"required": []string{"format", "compression"}, "properties": map[string]any{"format": map[string]any{"const": "csv"}, "compression": map[string]any{"const": "zip"}}},
		}
	}
	return out
}

func actionResponseSchemaName(id string) string {
	if name := crossChainResponseName(id); name != "" {
		return name
	}
	names := map[string]string{
		"getAddressOverview": "AddressOverviewResponse", "getAddressAssets": "AddressAssetsResponse", "getTransactions": "TransactionListResponse",
		"getNFTTransfers": "NFTTransferListResponse", "getAddressLogs": "AddressLogsResponse", "getBalanceSnapshots": "BalanceSnapshotListResponse",
		"getTokenTransfers": "TokenTransferListResponse", "getInternalTransactions": "InternalTransactionListResponse", "getCounterparties": "CounterpartyListResponse",
		"getFlowSummary": "FlowSummaryResponse", "getTopFlows": "TopFlowResponse", "getFirstFunding": "FirstFundingResponse", "getTransactionDetail": "TransactionDetailResponse",
		"getTransactionTrace": "TransactionTraceResponse", "getTransactionLogs": "TransactionLogsResponse", "decodeTransaction": "DecodeTransactionResponse",
		"batchGetAddressOverview": "BatchOverviewResponse", "batchGetAddressAssets": "BatchAssetsResponse", "findFundPaths": "FundPathResponse",
		"buildAddressNetwork": "AddressNetworkResponse", "createExport": "ExportCreateResponse", "getExportStatus": "ExportStatusResponse", "getExportAddressSummary": "ExportAddressSummaryResponse",
		"ensureChainData": "EnsureChainDataResponse", "getAIDownloadStatus": "AIDownloadStatusResponse", "retryAIDownloadFailures": "AIDownloadRetryResponse",
		"getSolanaAddressSignatures": "SolanaAddressSignaturesResponse", "getSolanaTransaction": "SolanaTransactionResponse", "getBridgeSourceTransaction": "BridgeSourceTransactionResponse",
	}
	return names[id]
}

func actionSchemas() map[string]any {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	decimal := str("Exact decimal value encoded as a string; never parse as binary floating point.")
	timeValue := map[string]any{"type": []string{"string", "null"}, "format": "date-time"}
	missingRange := map[string]any{"type": "object", "properties": map[string]any{"dataset": str("Dataset containing the gap."), "start_block": map[string]any{"type": []string{"integer", "null"}}, "end_block": map[string]any{"type": []string{"integer", "null"}}, "start_time": timeValue, "end_time": timeValue, "reason": str("Why the range is missing.")}}
	coverage := map[string]any{"type": "object", "description": "Completeness is asserted per address and dataset: a dataset counts as complete when a full-range download for that address and dataset reached COMPLETED, independently of other datasets in the same download job. A missing dataset is listed in missing_ranges; complete=false never means the returned rows are wrong, only that full history is not asserted.", "required": []string{"complete", "missing_ranges"}, "properties": map[string]any{"complete": map[string]any{"type": "boolean"}, "start_block": map[string]any{"type": []string{"integer", "null"}}, "end_block": map[string]any{"type": []string{"integer", "null"}}, "start_time": timeValue, "end_time": timeValue, "missing_ranges": map[string]any{"type": "array", "items": missingRange}}}
	pagination := map[string]any{"type": "object", "required": []string{"page", "page_size", "returned", "total", "has_more"}, "properties": map[string]any{"page": map[string]any{"type": "integer"}, "page_size": map[string]any{"type": "integer"}, "returned": map[string]any{"type": "integer"}, "total": map[string]any{"type": "integer"}, "has_more": map[string]any{"type": "boolean"}, "next_page": map[string]any{"type": []string{"integer", "null"}}}}
	meta := map[string]any{"type": "object", "required": []string{"coverage", "freshness", "source", "cached"}, "properties": map[string]any{"coverage": coverage, "freshness": map[string]any{"type": "object", "required": []string{"as_of_time"}, "properties": map[string]any{"as_of_block": map[string]any{"type": []string{"integer", "null"}}, "as_of_time": timeValue}}, "pagination": pagination, "request_id": str("Request correlation identifier when available."), "query_generated_at": timeValue, "dataset_version": str("Dataset version."), "normalization_version": str("Normalization version."), "source": str("Evidence source used by the platform."), "cached": map[string]any{"type": "boolean"}, "token_resolution": map[string]any{"type": []string{"object", "null"}, "properties": map[string]any{"requested_symbol": str("Requested token symbol."), "resolved_contract": str("Resolved complete token contract."), "ambiguous": map[string]any{"type": "boolean"}}}}}
	errorSchema := map[string]any{"type": []string{"object", "null"}, "properties": map[string]any{"code": str("Stable machine-readable error code."), "message": str("Human-readable error message.")}}
	envelope := func(data map[string]any) map[string]any {
		return map[string]any{"type": "object", "required": []string{"success", "data", "meta", "error"}, "properties": map[string]any{"success": map[string]any{"type": "boolean"}, "data": data, "meta": map[string]any{"$ref": "#/components/schemas/EvidenceMeta"}, "error": errorSchema}}
	}
	integer := map[string]any{"type": "integer"}
	nullString := map[string]any{"type": []string{"string", "null"}}
	directionField := map[string]any{"type": "string", "enum": []string{"in", "out", "self"}}
	statusField := map[string]any{"type": "string", "enum": []string{"success", "failed", "unknown"}}
	baseEvidence := map[string]any{"chain": str("Canonical public chain key."), "tx_hash": str("Complete transaction hash."), "block_number": integer, "timestamp": timeValue, "from_address": str("Complete sender address."), "to_address": str("Complete recipient address."), "direction": directionField, "status": statusField}
	tokenTransferProps := map[string]any{}
	for k, v := range baseEvidence {
		tokenTransferProps[k] = v
	}
	for k, v := range map[string]any{"log_index": integer, "token_symbol": str("Token symbol."), "token_contract": str("Complete token contract address."), "token_name": str("Token name."), "decimals": integer, "raw_value": str("Unscaled integer token value."), "amount": decimal} {
		tokenTransferProps[k] = v
	}
	tokenTransfer := map[string]any{"type": "object", "additionalProperties": true, "required": []string{"tx_hash", "from_address", "to_address", "token_contract", "raw_value", "amount", "status"}, "properties": tokenTransferProps}
	transactionProps := map[string]any{}
	for k, v := range baseEvidence {
		transactionProps[k] = v
	}
	for k, v := range map[string]any{"transaction_index": integer, "native_value": decimal, "raw_value": str("Native value in base units."), "gas": decimal, "gas_used": decimal, "gas_price": decimal, "fee": decimal, "nonce": integer, "method_id": str("Method selector."), "method_name": nullString, "contract_address": nullString, "input": str("Raw transaction input.")} {
		transactionProps[k] = v
	}
	transaction := map[string]any{"type": "object", "additionalProperties": true, "required": []string{"tx_hash", "from_address", "status"}, "properties": transactionProps}
	internalTx := map[string]any{"type": "object", "additionalProperties": true, "required": []string{"tx_hash", "trace_id", "call_type", "status"}, "properties": map[string]any{"chain": str("Chain."), "tx_hash": str("Complete transaction hash."), "trace_id": str("Stable trace identifier."), "trace_address": map[string]any{"type": "array", "items": integer}, "call_type": str("CALL, DELEGATECALL, STATICCALL, CREATE or related type."), "from_address": str("Complete sender."), "to_address": str("Complete recipient."), "raw_value": str("Native value in base units."), "value": decimal, "gas": decimal, "gas_used": decimal, "error": nullString, "status": statusField, "timestamp": timeValue}}
	logItem := map[string]any{"type": "object", "additionalProperties": true, "properties": map[string]any{"tx_hash": str("Complete transaction hash."), "block_number": integer, "log_index": integer, "contract_address": str("Emitting contract."), "topic0": nullString, "topic1": nullString, "topic2": nullString, "topic3": nullString, "data": str("Unmodified raw log data."), "event_name": nullString, "decoded": map[string]any{"type": "boolean"}, "decoded_args": map[string]any{"type": []string{"object", "null"}, "additionalProperties": true}, "removed": map[string]any{"type": "boolean"}}}
	asset := map[string]any{"type": "object", "additionalProperties": true, "required": []string{"balance", "balance_source", "is_current_balance"}, "properties": map[string]any{"symbol": str("Token symbol."), "contract": str("Complete token contract address."), "balance": decimal, "usd_value": map[string]any{"type": []string{"string", "null"}}, "balance_source": map[string]any{"type": "string", "enum": []string{"balance_snapshot", "observed_net_flow"}}, "is_current_balance": map[string]any{"type": "boolean"}, "as_of_block": map[string]any{"type": []string{"integer", "null"}}, "as_of_time": timeValue}}
	list := func(item map[string]any) map[string]any { return map[string]any{"type": "array", "items": item} }
	object := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": true, "properties": props}
	}
	schemas := map[string]any{"EvidenceMeta": meta}
	schemas["AddressOverviewResponse"] = envelope(object(map[string]any{"chain": str("Chain."), "address": str("Complete address."), "address_type": str("wallet, contract or unknown."), "native_balance": decimal, "native_balance_source": str("Balance evidence source."), "transaction_count": integer, "token_transfer_count": integer, "internal_transaction_count": integer, "unique_in_counterparties": integer, "unique_out_counterparties": integer, "contract": map[string]any{"type": "boolean"}, "labels": map[string]any{"type": "array", "items": str("Address label.")}, "first_activity": timeValue, "last_activity": timeValue}))
	schemas["AddressAssetsResponse"] = envelope(object(map[string]any{"address": str("Complete address."), "native_balance": decimal, "native_balance_source": map[string]any{"type": "string", "enum": []string{"balance_snapshot", "observed_net_flow", "unavailable"}}, "native_is_current_balance": map[string]any{"type": "boolean"}, "native_as_of_block": map[string]any{"type": []string{"integer", "null"}}, "native_as_of_time": timeValue, "assets": list(asset), "estimated_total_usd": map[string]any{"type": []string{"string", "null"}}}))
	schemas["TransactionListResponse"] = envelope(list(transaction))
	schemas["TokenTransferListResponse"] = envelope(list(tokenTransfer))
	schemas["NFTTransferListResponse"] = envelope(list(object(map[string]any{"chain": str("Chain."), "tx_hash": str("Complete transaction hash."), "log_index": integer, "contract_address": str("NFT contract."), "token_id": str("Exact token ID."), "amount_raw": str("Exact integer quantity."), "token_standard": str("NFT token standard."), "from_address": str("Complete sender."), "to_address": str("Complete recipient."), "timestamp": timeValue, "block_number": integer, "direction": directionField})))
	schemas["AddressLogsResponse"] = envelope(list(logItem))
	schemas["BalanceSnapshotListResponse"] = envelope(list(object(map[string]any{"chain": str("Chain."), "address": str("Complete address."), "token_address": str("Asset contract, empty for native."), "balance_raw": str("Unscaled integer balance."), "balance": decimal, "token_decimals": integer, "snapshot_block": integer, "timestamp": timeValue, "as_of_block": integer, "as_of_time": timeValue, "balance_source": map[string]any{"const": "balance_snapshot"}, "is_current_balance": map[string]any{"const": false}, "source_provider": str("Snapshot provider.")})))
	schemas["InternalTransactionListResponse"] = envelope(list(internalTx))
	schemas["TransactionLogsResponse"] = envelope(list(logItem))
	topByAddress := object(map[string]any{"counterparty": str("Complete counterparty."), "direction": directionField, "token_symbol": str("Token symbol."), "token_contract": str("Complete token contract."), "total_amount": decimal, "tx_count": integer, "percentage": str("Share of total flow in the requested direction and filter window."), "first_tx_time": timeValue, "last_tx_time": timeValue})
	topByTransaction := object(map[string]any{"tx_hash": str("Complete transaction hash."), "from_address": str("Complete sender."), "to_address": str("Complete recipient."), "amount": decimal, "token_symbol": str("Token symbol."), "token_contract": str("Complete token contract."), "block_number": integer, "timestamp": timeValue})
	schemas["TopFlowResponse"] = envelope(map[string]any{"type": "array", "items": map[string]any{"oneOf": []any{topByAddress, topByTransaction}}})
	counterparty := object(map[string]any{"counterparty": str("Complete counterparty address."), "tx_count": integer, "incoming_tx_count": integer, "outgoing_tx_count": integer, "incoming_amount": decimal, "outgoing_amount": decimal, "net_flow": decimal, "token_symbol": str("Token symbol."), "token_contract": str("Complete token contract."), "percentage_of_total_in": decimal, "percentage_of_total_out": decimal, "labels": map[string]any{"type": "array", "items": str("Counterparty label.")}, "first_tx_time": timeValue, "last_tx_time": timeValue})
	schemas["CounterpartyListResponse"] = envelope(list(counterparty))
	schemas["FlowSummaryResponse"] = envelope(object(map[string]any{"address": str("Complete investigated address."), "token_symbol": str("Resolved token symbol."), "token_contract": str("Resolved complete token contract."), "total_in": decimal, "total_out": decimal, "net_flow": decimal, "in_tx_count": integer, "out_tx_count": integer, "unique_in_addresses": integer, "unique_out_addresses": integer, "first_in_time": timeValue, "last_in_time": timeValue, "first_out_time": timeValue, "last_out_time": timeValue}))
	funding := object(map[string]any{"from_address": str("Complete funding address."), "to_address": str("Funded address."), "tx_hash": str("Complete transaction hash."), "amount": decimal, "raw_value": str("Unscaled value."), "block_number": integer, "timestamp": timeValue, "asset": str("Native asset or token symbol."), "token_symbol": str("Token symbol."), "token_contract": str("Complete token contract."), "decimals": integer})
	schemas["FirstFundingResponse"] = envelope(object(map[string]any{"address": str("Complete address."), "first_native_funding": map[string]any{"oneOf": []any{funding, map[string]any{"type": "null"}}}, "first_token_funding": list(funding), "coverage_complete_before_first_observed_tx": map[string]any{"type": "boolean"}}))
	call := object(map[string]any{"trace_id": str("Trace node identifier."), "parent_trace_id": nullString, "depth": integer, "call_type": str("Call type."), "from_address": str("Complete sender."), "to_address": str("Complete recipient."), "value": decimal, "gas": decimal, "gas_used": decimal, "input": str("Raw call input."), "output": str("Raw call output."), "method_id": nullString, "method_name": nullString, "status": statusField, "error": nullString, "children_count": integer})
	call["required"] = []string{"trace_id", "depth", "call_type", "status"}
	schemas["TransactionTraceResponse"] = envelope(object(map[string]any{"chain": str("Chain."), "tx_hash": str("Complete transaction hash."), "trace_available": map[string]any{"type": "boolean"}, "provider": str("Trace evidence provider."), "root_call": call, "calls": list(call)}))
	decode := object(map[string]any{"chain": str("Chain."), "tx_hash": str("Complete transaction hash."), "to_address": str("Called address."), "method_id": str("Method selector."), "method_signature": nullString, "method_name": nullString, "decoded": map[string]any{"type": "boolean"}, "decoded_input": map[string]any{}, "decoded_output": map[string]any{}, "abi_source": map[string]any{"type": "string", "enum": []string{"verified_abi", "local_abi", "selector_database", "unknown"}}, "selector_source": nullString, "confidence": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}}})
	schemas["DecodeTransactionResponse"] = envelope(decode)
	schemas["TransactionDetailResponse"] = envelope(object(map[string]any{"tx_hash": str("Complete transaction hash."), "transaction": transaction, "receipt": map[string]any{"type": []string{"object", "null"}, "additionalProperties": true}, "decoded_input": map[string]any{}, "token_transfers": list(tokenTransfer), "internal_transactions": list(internalTx), "logs": list(logItem)}))
	solanaRecord := object(map[string]any{"chain": map[string]any{"type": "string", "const": "solana"}, "signature": str("Complete case-sensitive Solana signature."), "slot": integer, "block_time": map[string]any{"type": []string{"integer", "null"}}, "version": map[string]any{}, "transaction": object(map[string]any{}), "meta": map[string]any{"type": []string{"object", "null"}, "additionalProperties": true}, "source_record_status": map[string]any{"type": "string", "const": "readable"}, "rpc_source": str("Actual dRPC source, including the optional dRPC archive endpoint.")})
	schemas["SolanaTransactionResponse"] = envelope(solanaRecord)
	schemas["SolanaAddressSignaturesResponse"] = envelope(object(map[string]any{"chain": map[string]any{"type": "string", "const": "solana"}, "address": str("Complete case-sensitive Solana address."), "signatures": list(object(map[string]any{"signature": str("Complete transaction signature."), "slot": integer, "blockTime": map[string]any{"type": []string{"integer", "null"}}, "err": map[string]any{}}))}))
	schemas["BridgeSourceTransactionResponse"] = envelope(object(map[string]any{"bridge_order_id": str("Caller-provided correlation label, not proof of association."), "source_chain": str("Source chain."), "source_tx_hash": str("Complete transaction hash or Solana signature."), "source_record_status": map[string]any{"type": "string", "const": "readable"}, "source_record": object(map[string]any{}), "bridge_order_association_verified": map[string]any{"type": "boolean", "const": false}, "association_reason": str("Why order association is not yet verified.")}))
	schemas["BatchOverviewResponse"] = envelope(list(object(map[string]any{"address": str("Complete address."), "address_type": str("wallet, contract or unknown."), "transaction_count": integer, "token_transfer_count": integer, "first_activity": timeValue, "last_activity": timeValue, "native_balance": decimal, "native_balance_source": str("Balance evidence source."), "labels": map[string]any{"type": "array", "items": str("Address label.")}})))
	schemas["BatchAssetsResponse"] = envelope(list(object(map[string]any{"address": str("Complete address."), "native_balance": decimal, "estimated_total_usd": map[string]any{"type": []string{"string", "null"}}, "verified_current_total_usd": map[string]any{"type": []string{"string", "null"}}, "has_current_balance_snapshot": map[string]any{"type": "boolean"}, "assets": list(asset)})))
	node := object(map[string]any{"address": str("Complete address."), "labels": map[string]any{"type": "array", "items": str("Label.")}, "address_type": str("wallet, contract or unknown."), "current_balance": map[string]any{"type": []string{"string", "null"}}, "balance_source": map[string]any{"type": "string", "enum": []string{"balance_snapshot", "observed_net_flow", "unavailable"}}, "is_current_balance": map[string]any{"type": "boolean"}, "balance_as_of_block": map[string]any{"type": []string{"integer", "null"}}, "balance_as_of_time": timeValue, "total_in": decimal, "total_out": decimal, "net_flow": decimal, "depth": integer})
	edge := object(map[string]any{"from": str("Complete sender."), "to": str("Complete recipient."), "token_symbol": str("Token symbol."), "token_contract": str("Complete token contract."), "amount": decimal, "tx_count": integer, "first_time": timeValue, "last_time": timeValue, "percentage": str("Share of the parent node's total outgoing amount within this bounded traversal.")})
	traversal := map[string]any{"truncated": map[string]any{"type": "boolean"}, "node_count": integer, "edge_count": integer, "max_depth_requested": integer, "max_depth_reached": integer, "node_limit_reached": map[string]any{"type": "boolean"}}
	networkProps := map[string]any{"nodes": list(node), "edges": list(edge)}
	for k, v := range traversal {
		networkProps[k] = v
	}
	network := object(networkProps)
	path := object(map[string]any{"path_id": str("Path identifier."), "total_amount": decimal, "hop_count": integer, "hops": list(edge), "addresses": map[string]any{"type": "array", "items": str("Address in path order.")}})
	pathProps := map[string]any{"source": str("Source address."), "paths": list(path), "nodes": list(node), "edges": list(edge)}
	for k, v := range traversal {
		pathProps[k] = v
	}
	schemas["FundPathResponse"] = envelope(object(pathProps))
	schemas["AddressNetworkResponse"] = envelope(network)
	schemas["ExportCreateResponse"] = envelope(object(map[string]any{"job_id": str("Export job UUID."), "status": map[string]any{"type": "string", "enum": []string{"processing"}}, "addresses": list(str("Complete exported address.")), "address_count": integer}))
	exportFile := object(map[string]any{"name": str("Export part filename."), "url": map[string]any{"type": []string{"string", "null"}, "format": "uri"}, "rows": integer, "size_bytes": integer, "sha256": str("SHA-256 digest.")})
	schemas["ExportStatusResponse"] = envelope(object(map[string]any{"job_id": str("Export job UUID."), "status": str("Export status."), "total_rows": map[string]any{"type": "integer"}, "format": map[string]any{"type": "string", "enum": []string{"parquet", "csv"}}, "compression": map[string]any{"type": "string", "enum": []string{"zstd", "zip"}}, "files": list(exportFile), "manifest_url": map[string]any{"type": []string{"string", "null"}, "format": "uri"}, "download_url": map[string]any{"type": []string{"string", "null"}, "format": "uri"}, "sha256": map[string]any{"type": []string{"string", "null"}}, "expires_at": timeValue}))

	addressStatistic := object(map[string]any{"from_address": str("Complete payer address."), "record_count": integer, "total_amount": decimal, "total_amount_raw": map[string]any{"type": []string{"string", "null"}}, "first_block": str("First observed block."), "last_block": str("Last observed block.")})
	transferDetail := object(map[string]any{"record_id": str("Stable archive entry and record ordinal."), "from_address": str("Complete original payer address."), "to_address": str("Complete original recipient address."), "tx_hash": str("Complete transaction hash or signature."), "block_number": str("Original block number."), "amount": decimal, "amount_raw": str("Original unscaled amount when available."), "fields": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}})
	nullableString := map[string]any{"type": []string{"string", "null"}}
	schemas["ExportAddressSummaryResponse"] = envelope(object(map[string]any{
		"job_id": str("Export UUID."), "status": map[string]any{"type": "string", "const": "completed"}, "source_sha256": str("Verified complete export SHA-256."),
		"filters":           object(map[string]any{"job_id": str("Export UUID."), "chain": str("Exported chain."), "address": str("Direction reference address; empty when unrestricted."), "token": str("Selected token symbol."), "token_contract": str("Selected complete contract."), "direction": str("all, in or out."), "from_address": str("Optional payer filter.")}),
		"export_total_rows": integer, "dataset_rows": integer, "total_records": integer, "total_addresses": integer, "total_amount": decimal, "total_amount_raw": nullableString, "data_cutoff_block": nullableString,
		"addresses": list(addressStatistic), "next_cursor": nullableString, "has_more": map[string]any{"type": "boolean"},
		"details": list(transferDetail), "next_detail_cursor": nullableString, "details_has_more": map[string]any{"type": "boolean"}, "page_size": integer, "detail_page_size": integer,
	}))
	schemas["EnsureChainDataResponse"] = envelope(object(map[string]any{"status": str("ready when Coverage is complete, otherwise created."), "dataset_ref": str("Stable dataset reference."), "download_job_id": map[string]any{"type": []string{"string", "null"}, "format": "uuid"}, "coverage_before": decimal, "source": map[string]any{"type": "string", "const": "chatgpt_mcp"}, "ai_managed": map[string]any{"type": "boolean", "const": true}}))
	schemas["AIDownloadStatusResponse"] = envelope(object(map[string]any{"download_job_id": map[string]any{"type": "string", "format": "uuid"}, "job_no": str("Human-readable download job number."), "status": str("Download job status."), "source": map[string]any{"type": "string", "const": "chatgpt_mcp"}, "ai_managed": map[string]any{"type": "boolean", "const": true}, "chain": str("Canonical chain key."), "datasets": map[string]any{"type": "array", "items": str("Dataset key.")}, "range": map[string]any{"type": "object", "additionalProperties": true}, "stats": map[string]any{"type": "object", "additionalProperties": true}, "error": map[string]any{"type": []string{"string", "null"}}, "failures": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": true}}}))
	schemas["AIDownloadRetryResponse"] = envelope(object(map[string]any{"download_job_id": map[string]any{"type": "string", "format": "uuid"}, "job_no": str("Human-readable download job number."), "status": str("queued, an already-active state, or completed."), "reset_shards": integer, "preserved_completed_shards": integer, "already_active": map[string]any{"type": "boolean"}, "already_completed": map[string]any{"type": "boolean"}, "source": map[string]any{"type": "string", "const": "chatgpt_mcp"}, "ai_managed": map[string]any{"type": "boolean", "const": true}}))
	addInvestigationSchemas(schemas)
	addCrossChainSchemas(schemas)
	return schemas
}

func (s *Server) handleActionsOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	doc := actionsOpenAPIDocument()
	publicBase := strings.TrimRight(strings.TrimSpace(os.Getenv("GPT_ACTIONS_PUBLIC_BASE_URL")), "/")
	if publicBase == "" {
		publicBase = "https://aurore.online"
	}
	doc["servers"] = []any{map[string]any{"url": publicBase}}
	_ = json.NewEncoder(w).Encode(doc)
}
func (s *Server) handleActionsSwagger(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Blockchain Actions API</title><link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"></head><body><div id="swagger-ui"></div><script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script><script>SwaggerUIBundle({url:'/openapi.json',dom_id:'#swagger-ui',persistAuthorization:true})</script></body></html>`))
}
func (s *Server) handleActionsRedoc(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Blockchain Actions API</title></head><body><redoc spec-url="/openapi.json"></redoc><script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script></body></html>`))
}
