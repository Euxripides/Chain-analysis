package solana

import (
	"encoding/json"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
)

// NormalizeSolanaTransaction 把 Provider 行转成 transactions 行（规范 §8.2）。
//
// 在 ingest.NormalizeTransaction 之上只补 Solana 语义：
//   - base58 地址原样保留（泛型层按链判断，这里再兜一层，折叠不可逆）；
//   - native_symbol 恒为 SOL，confirmation/commitment 缺省 finalized（Data API 历史页
//     只返回已固化交易，Solana 没有"确认数"概念）；
//   - blockNumber 缺失时回落到 RPC 的 slot（§8.1：block 与 slot 是同一个槽位）；
//   - nonce/gas/gas_used/gas_price 为 0/空，不虚构 EVM 含义；input 按 §3 契约就是空串，
//     这里既不为它编造 EVM calldata，也不从中解析 selector；
//   - value_raw 只在行内给出单笔原生转账值时填写，缺失/空串保持空串而不是 "0"。
//
// fee_raw/signers/account_keys/program_ids/source_api/source_method/source_request_id/
// chain_payload_json 由 ingest.NormalizeTransaction 按 §3 行契约读取（全部可选），
// 本函数不再重复解析，避免键名策略出现两份。
func NormalizeSolanaTransaction(raw json.RawMessage, provider string) (ingest.TxRow, error) {
	row, err := ingest.NormalizeTransaction(raw, "solana", providerOrDefault(provider))
	if err != nil {
		return ingest.TxRow{}, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ingest.TxRow{}, err
	}
	if value := rawAddress(m, "from", "fromAddress"); value != "" {
		row.From = value
	}
	if value := rawAddress(m, "to", "toAddress"); value != "" {
		row.To = value
	}
	if row.BlockNumber == 0 {
		row.BlockNumber = uint64(numberField(m, "slot"))
	}
	// 空值不能变成 "0"：没有单笔原生转账时 value_raw 必须是空串（§8.2）。
	row.ValueRaw = strings.TrimSpace(stringField(m, "value"))
	row.Nonce, row.Gas, row.GasUsed, row.GasPrice = 0, 0, 0, ""
	row.NativeSymbol = "SOL"
	row.Confirmation = strings.TrimSpace(stringField(m, "confirmation"))
	if row.Confirmation == "" {
		row.Confirmation = strings.TrimSpace(stringField(m, "commitment"))
	}
	if row.Confirmation == "" {
		row.Confirmation = "finalized"
	}
	return row, nil
}
