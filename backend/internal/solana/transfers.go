package solana

import (
	"encoding/json"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
)

// NormalizeTokenTransferForChain 是 Solana 专用的转账归一化入口（规范 §8.3）。
//
// 复用 ingest.NormalizeTokenTransfer 得到与其它链同构的行（金额不变量、代币账户语义
// 都由泛型层统一），再补两件 Solana 专属的事：
//  1. 用 raw 原文覆盖地址字段——泛型层按链判断是否折叠大小写，这里再兜一层，
//     保证 base58 大小写不因调用方传错 chain 而丢失（折叠不可逆）；
//  2. 解析 instructionIndex/innerInstructionIndex/sourceApi/sourceRequestId：
//     log_index 只是本地稳定序号，真实指令位置必须单独承载。
func NormalizeTokenTransferForChain(raw json.RawMessage, chain, provider string) (ingest.TokenTransferRow, error) {
	chain = chainOrDefault(chain)
	provider = providerOrDefault(provider)
	row, err := ingest.NormalizeTokenTransfer(raw, chain, provider)
	if err != nil {
		return ingest.TokenTransferRow{}, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ingest.TokenTransferRow{}, err
	}
	// 只在 raw 确实给出地址时才覆盖：空值保持泛型层结果，不把地址写成空。
	if value := rawAddress(m, "fromAddress", "from"); value != "" {
		row.From = value
	}
	if value := rawAddress(m, "toAddress", "to"); value != "" {
		row.To = value
	}
	if value := rawAddress(m, "contractAddress"); value != "" {
		row.TokenAddress = value
	}
	if value := rawAddress(m, "fromTokenAccount"); value != "" {
		row.FromTokenAccount = value
	}
	if value := rawAddress(m, "toTokenAccount"); value != "" {
		row.ToTokenAccount = value
	}
	row.InstructionIndex = uint32(numberField(m, "instructionIndex"))
	row.InnerInstructionIndex = innerInstructionField(m, "innerInstructionIndex")
	row.SourceAPI = strings.TrimSpace(stringField(m, "sourceApi"))
	row.SourceRequestID = strings.TrimSpace(stringField(m, "sourceRequestId"))
	return row, nil
}
