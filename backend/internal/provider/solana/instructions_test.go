package solana

import (
	"encoding/json"
	"testing"

	soladdr "github.com/blockchain-platform/blockchain-platform/internal/solana"
)

// rawInstruction 用 base58 的 data 构造一条指令（模拟 json / jsonParsed 未识别的形态）。
func rawInstruction(programID string, accounts []string, data []byte) parsedInstructionRaw {
	encoded, _ := json.Marshal(soladdr.EncodeBase58(data))
	quoted := make([]json.RawMessage, 0, len(accounts))
	for _, account := range accounts {
		payload, _ := json.Marshal(account)
		quoted = append(quoted, payload)
	}
	return parsedInstructionRaw{ProgramID: programID, Accounts: quoted, Data: encoded}
}

// parsedInstruction 构造 jsonParsed 形态的指令。
func parsedInstruction(programID, kind, info string, accounts ...string) parsedInstructionRaw {
	quoted := make([]json.RawMessage, 0, len(accounts))
	for _, account := range accounts {
		payload, _ := json.Marshal(account)
		quoted = append(quoted, payload)
	}
	return parsedInstructionRaw{
		ProgramID: programID, Accounts: quoted,
		Parsed: &parsedSemantic{Type: kind, Info: json.RawMessage(info)},
	}
}

func balance(index int, mint, owner, amount string, decimals uint8) tokenBalanceEntry {
	var entry tokenBalanceEntry
	entry.AccountIndex = index
	entry.Mint = mint
	entry.Owner = owner
	entry.UITokenAmount.Amount = amount
	entry.UITokenAmount.Decimals = decimals
	return entry
}

func littleEndian(value uint64) []byte {
	out := make([]byte, 8)
	for i := 0; i < 8; i++ {
		out[i] = byte(value >> (8 * i))
	}
	return out
}

func TestDecodeSPLTransferCheckedFromRawBytes(t *testing.T) {
	// 真实样本的字节布局：0c + u64 小端金额 + decimals + 32 字节 mint。
	data := []byte{discTransferChecked}
	data = append(data, littleEndian(3090000000)...)
	data = append(data, 6)
	mint := make([]byte, 32)
	for i := range mint {
		mint[i] = 1
	}
	data = append(data, mint...)

	ix := rawInstruction(ProgramSPLToken, []string{"SourceATA", "MintATA", "DestATA", "Authority"}, data)
	transfer, ok := decodeSPLTransfer(ProgramSPLToken, "", ix, nil)
	if !ok {
		t.Fatal("expected transferChecked to decode from raw bytes")
	}
	if transfer.Kind != "transferChecked" || transfer.Gross.String() != "3090000000" || transfer.Decimals != 6 {
		t.Fatalf("unexpected decode: %+v", transfer)
	}
	if transfer.Source != "SourceATA" || transfer.Destination != "DestATA" || transfer.Mint != "MintATA" {
		t.Fatalf("unexpected accounts: %+v", transfer)
	}
}

func TestDecodeSPLTransferRejectsNonTransferBytes(t *testing.T) {
	// MintTo(7) 不是转账；System Program 也不该被当成 SPL。
	mintTo := rawInstruction(ProgramSPLToken, []string{"A", "B", "C"}, []byte{7, 1, 2, 3})
	if _, ok := decodeSPLTransfer(ProgramSPLToken, "", mintTo, nil); ok {
		t.Fatal("non-transfer instruction must be rejected")
	}
	transferData := []byte{discTransfer}
	transferData = append(transferData, littleEndian(5)...)
	systemInstruction := rawInstruction(SystemProgram, []string{"A", "B"}, transferData)
	if _, ok := decodeSPLTransfer(SystemProgram, "", systemInstruction, nil); ok {
		t.Fatal("system program must not be decoded as an SPL transfer")
	}
}

func TestBuildTokenTransferRowsReconcilesNetFromBalances(t *testing.T) {
	keys := []string{"Payer", "SourceATA", "DestATA", "Mint"}
	ix := parsedInstruction(ProgramSPLToken, "transferChecked",
		`{"source":"SourceATA","destination":"DestATA","authority":"Payer","mint":"Mint","tokenAmount":{"amount":"1000","decimals":6}}`,
		"SourceATA", "Mint", "DestATA")

	// 收款人只收到 990：差额 10 就是 Token-2022 代扣的手续费。
	pre := []tokenBalanceEntry{balance(1, "Mint", "OwnerA", "5000", 6)}
	post := []tokenBalanceEntry{balance(1, "Mint", "OwnerA", "4000", 6), balance(2, "Mint", "OwnerB", "990", 6)}

	index := buildTokenAccountIndex(keys, pre, post)
	rows, stats := buildTokenTransferRows(448611120, 1789875226, "SIG", keys, []parsedInstructionRaw{ix}, nil, index)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d (stats %+v)", len(rows), stats)
	}
	row := rows[0]
	if row.GrossAmountRaw != "1000" || row.ValueRawInteger != "990" || row.FeeAmountRaw != "10" {
		t.Fatalf("net must come from balances and fee = gross-net: %+v", row)
	}
	if stats.FeeFromBalances != 1 {
		t.Fatalf("fee detected from balances must be counted, stats=%+v", stats)
	}
	if row.FromAddress != "OwnerA" || row.ToAddress != "OwnerB" {
		t.Fatalf("owner mapping must come from token balances: %+v", row)
	}
	if row.FromTokenAccount != "SourceATA" || row.ToTokenAccount != "DestATA" {
		t.Fatalf("ATAs must be preserved: %+v", row)
	}
	if row.LogIndex != 0 || row.BlockHeight != "448611120" || row.ContractAddress != "Mint" || row.TokenDecimals != 6 {
		t.Fatalf("unexpected identity fields: %+v", row)
	}
}

func TestBuildTokenTransferRowsUsesInnerInstructionKey(t *testing.T) {
	keys := []string{"Payer", "SourceATA", "DestATA", "Mint"}
	ix := parsedInstruction(ProgramSPLToken, "transferChecked",
		`{"source":"SourceATA","destination":"DestATA","mint":"Mint","tokenAmount":{"amount":"1000","decimals":6}}`,
		"SourceATA", "Mint", "DestATA")
	pre := []tokenBalanceEntry{balance(1, "Mint", "OwnerA", "5000", 6)}
	post := []tokenBalanceEntry{balance(1, "Mint", "OwnerA", "4000", 6), balance(2, "Mint", "OwnerB", "1000", 6)}
	index := buildTokenAccountIndex(keys, pre, post)
	groups := []innerInstructionGroup{{Index: 3, Instructions: []parsedInstructionRaw{ix, ix}}}
	rows, _ := buildTokenTransferRows(7, 100, "SIG", keys, nil, groups, index)
	if len(rows) != 2 {
		t.Fatalf("expected 2 inner rows, got %d", len(rows))
	}
	if rows[0].LogIndex != 3*256+1 || rows[1].LogIndex != 3*256+2 {
		t.Fatalf("inner log_index must be outer*256+inner(1-based): %+v", rows)
	}
}

func TestBuildTokenTransferRowsSkipsUnresolvedOwners(t *testing.T) {
	keys := []string{"Payer", "SourceATA", "DestATA", "Mint"}
	ix := parsedInstruction(ProgramSPLToken, "transferChecked",
		`{"source":"SourceATA","destination":"DestATA","mint":"Mint","tokenAmount":{"amount":"10","decimals":6}}`,
		"SourceATA", "Mint", "DestATA")
	// 目标账户没有任何余额条目：owner 解析不出 → 跳过并计数，绝不写猜测值。
	index := buildTokenAccountIndex(keys, nil, []tokenBalanceEntry{balance(1, "Mint", "OwnerA", "0", 6)})
	rows, stats := buildTokenTransferRows(1, 1, "SIG", keys, []parsedInstructionRaw{ix}, nil, index)
	if len(rows) != 0 {
		t.Fatalf("rows must be skipped when the owner is unresolved, got %+v", rows)
	}
	if stats.OwnerUnresolved != 1 {
		t.Fatalf("unresolved owners must be counted, stats=%+v", stats)
	}
}

func TestBuildCPITree(t *testing.T) {
	logs := []string{
		"Program A invoke [1]",
		"Program log: hello",
		"Program B invoke [2]",
		"Program B success",
		"Program A success",
		"Program C invoke [1]",
		"Program C failed: custom program error: 0x1",
	}
	nodes := buildCPITree(logs, []string{"A", "C"})
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d (%+v)", len(nodes), nodes)
	}
	if nodes[0].Program != "A" || nodes[0].Depth != 1 || nodes[0].TracePath != "0" || nodes[0].Parent != "" {
		t.Fatalf("unexpected root node: %+v", nodes[0])
	}
	if nodes[1].Program != "B" || nodes[1].Depth != 2 || nodes[1].Parent != "A" || nodes[1].TracePath != "0.0" {
		t.Fatalf("unexpected child node: %+v", nodes[1])
	}
	if nodes[2].Program != "C" || nodes[2].CallIndex != 256 || !nodes[2].Failed || nodes[2].Error == "" {
		t.Fatalf("unexpected failure node: %+v", nodes[2])
	}
}
