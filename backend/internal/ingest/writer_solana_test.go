package ingest

import (
	"reflect"
	"testing"
	"time"
)

// insertColumnIndex 返回列在 INSERT 清单中的下标；缺失直接 Fatal，新列漏加立刻暴露。
func insertColumnIndex(t *testing.T, columns []string, name string) int {
	t.Helper()
	for i, column := range columns {
		if column == name {
			return i
		}
	}
	t.Fatalf("INSERT 列清单缺少 %q", name)
	return -1
}

// assertColumnPrefix 断言既有列仍按原顺序排在前面（新列只允许追加在末尾）。
func assertColumnPrefix(t *testing.T, table string, columns, want []string) {
	t.Helper()
	if len(columns) < len(want) {
		t.Fatalf("%s 列数 %d 少于既有列数 %d", table, len(columns), len(want))
	}
	for i, column := range want {
		if columns[i] != column {
			t.Fatalf("%s 第 %d 列 = %q，want %q（既有列顺序不能变）", table, i, columns[i], column)
		}
	}
}

// 规范 §2.2：新增列必须真的出现在 INSERT 清单里，且既有列顺序不变。
func TestInsertColumnsCarrySolanaColumns(t *testing.T) {
	cases := []struct {
		table   string
		columns []string
		prefix  []string
		want    []string
	}{
		{
			table: "transactions", columns: transactionsInsertColumns,
			prefix: []string{"chain", "block_number", "block_hash", "tx_hash", "timestamp", "from_address", "to_address", "value_raw", "nonce", "gas", "gas_used", "gas_price", "tx_type", "status", "input", "method_id", "source_provider", "method_name", "method_signature", "method_source", "method_verified", "method_confidence", "method_type", "method_resolved_at", "native_symbol", "confirmation", "raw_type", "raw_status"},
			want:   []string{"fee_raw", "signers", "account_keys", "program_ids", "source_api", "source_method", "source_request_id", "chain_payload_json"},
		},
		{
			table: "token_transfers", columns: tokenTransferInsertColumns,
			prefix: []string{"chain", "block_number", "timestamp", "tx_hash", "log_index", "token_address", "token_symbol", "token_decimals", "from_address", "to_address", "amount_raw", "amount", "gross_amount_raw", "fee_amount_raw", "from_token_account", "to_token_account", "source_provider"},
			want:   []string{"instruction_index", "inner_instruction_index", "source_api", "source_request_id"},
		},
		{
			table: "nft_transfers", columns: nftTransferInsertColumns,
			prefix: []string{"chain", "block_number", "timestamp", "tx_hash", "log_index", "contract_address", "token_id", "from_address", "to_address", "token_name", "token_symbol", "token_standard", "amount_raw", "source_provider"},
			want:   []string{"instruction_index", "inner_instruction_index", "source_api", "source_request_id"},
		},
		{
			table: "solana_program_logs", columns: solanaProgramLogInsertColumns,
			prefix: nil,
			want:   []string{"chain", "slot", "timestamp", "signature", "log_index", "program_id", "message", "invoke_depth", "success", "source_provider", "source_api", "source_request_id"},
		},
	}
	for _, tc := range cases {
		assertColumnPrefix(t, tc.table, tc.columns, tc.prefix)
		for _, column := range tc.want {
			index := insertColumnIndex(t, tc.columns, column)
			if index < len(tc.prefix) && tc.prefix != nil {
				t.Fatalf("%s 新列 %q 未追加在末尾", tc.table, column)
			}
		}
	}
	// source_ingested_at 由 ClickHouse 的 DEFAULT now64(3) 生成，不能进 INSERT。
	for _, column := range solanaProgramLogInsertColumns {
		if column == "source_ingested_at" {
			t.Fatalf("source_ingested_at 必须交给 ClickHouse 默认值")
		}
	}
}

// 列清单与实参必须一一对应：长度错位会静默写错列。
func TestRowArgsMatchInsertColumns(t *testing.T) {
	if got, want := len(txRowArgs(TxRow{})), len(transactionsInsertColumns); got != want {
		t.Fatalf("transactions 实参数 %d != 列数 %d", got, want)
	}
	if got, want := len(tokenTransferRowArgs(TokenTransferRow{})), len(tokenTransferInsertColumns); got != want {
		t.Fatalf("token_transfers 实参数 %d != 列数 %d", got, want)
	}
	if got, want := len(nftTransferRowArgs(NftTransferRow{})), len(nftTransferInsertColumns); got != want {
		t.Fatalf("nft_transfers 实参数 %d != 列数 %d", got, want)
	}
	if got, want := len(solanaProgramLogRowArgs(SolanaProgramLogRow{})), len(solanaProgramLogInsertColumns); got != want {
		t.Fatalf("solana_program_logs 实参数 %d != 列数 %d", got, want)
	}
}

// 新字段必须落在对应列上（按列名定位，不依赖字面顺序）。
func TestTransactionRowArgsCarrySolanaColumns(t *testing.T) {
	args := txRowArgs(TxRow{
		Chain: "solana", Provider: "drpc", FeeRaw: "5000",
		Signers: []string{"SignerA", "SignerB"}, AccountKeys: []string{"KeyA"}, ProgramIds: []string{"ProgA"},
		SourceAPI: "data_api", SourceMethod: "get_transactions_history", SourceRequestID: "req-1",
		ChainPayloadJSON: `{"memo":"x"}`,
	})
	if got := args[insertColumnIndex(t, transactionsInsertColumns, "fee_raw")]; got != "5000" {
		t.Fatalf("fee_raw = %v", got)
	}
	if got := args[insertColumnIndex(t, transactionsInsertColumns, "signers")]; !reflect.DeepEqual(got, []string{"SignerA", "SignerB"}) {
		t.Fatalf("signers = %v", got)
	}
	if got := args[insertColumnIndex(t, transactionsInsertColumns, "account_keys")]; !reflect.DeepEqual(got, []string{"KeyA"}) {
		t.Fatalf("account_keys = %v", got)
	}
	if got := args[insertColumnIndex(t, transactionsInsertColumns, "program_ids")]; !reflect.DeepEqual(got, []string{"ProgA"}) {
		t.Fatalf("program_ids = %v", got)
	}
	if got := args[insertColumnIndex(t, transactionsInsertColumns, "source_api")]; got != "data_api" {
		t.Fatalf("source_api = %v", got)
	}
	if got := args[insertColumnIndex(t, transactionsInsertColumns, "source_method")]; got != "get_transactions_history" {
		t.Fatalf("source_method = %v", got)
	}
	if got := args[insertColumnIndex(t, transactionsInsertColumns, "source_request_id")]; got != "req-1" {
		t.Fatalf("source_request_id = %v", got)
	}
	if got := args[insertColumnIndex(t, transactionsInsertColumns, "chain_payload_json")]; got != `{"memo":"x"}` {
		t.Fatalf("chain_payload_json = %v", got)
	}
	// EVM 行零值：数组保持空（nil 切片），不能凭空造证据。
	evm := txRowArgs(TxRow{Chain: "bsc"})
	if got := evm[insertColumnIndex(t, transactionsInsertColumns, "signers")]; !reflect.DeepEqual(got, []string(nil)) {
		t.Fatalf("evm signers must stay empty, got %v", got)
	}
}

// 指令列语义：Solana 行带 source_api，真实的 inner=0 必须保留；直构行（TRON）没有指令模型，
// 0 按列默认值 -1 落库，不能伪装成"第 0 条内层指令"。
func TestTokenTransferRowArgsCarryInstructionEvidence(t *testing.T) {
	inner := insertColumnIndex(t, tokenTransferInsertColumns, "inner_instruction_index")
	instruction := insertColumnIndex(t, tokenTransferInsertColumns, "instruction_index")

	solana := tokenTransferRowArgs(TokenTransferRow{InstructionIndex: 3, InnerInstructionIndex: 0, SourceAPI: "data_api", SourceRequestID: "req-1"})
	if solana[instruction] != uint32(3) || solana[inner] != int32(0) {
		t.Fatalf("solana instruction evidence = %v/%v", solana[instruction], solana[inner])
	}
	if got := solana[insertColumnIndex(t, tokenTransferInsertColumns, "source_api")]; got != "data_api" {
		t.Fatalf("source_api = %v", got)
	}
	if got := solana[insertColumnIndex(t, tokenTransferInsertColumns, "source_request_id")]; got != "req-1" {
		t.Fatalf("source_request_id = %v", got)
	}

	direct := tokenTransferRowArgs(TokenTransferRow{AmountRaw: "1"})
	if direct[instruction] != uint32(0) || direct[inner] != NoInnerInstruction {
		t.Fatalf("direct rows must fall back to 0/-1: %v/%v", direct[instruction], direct[inner])
	}

	explicit := tokenTransferRowArgs(TokenTransferRow{InstructionIndex: 2, InnerInstructionIndex: -1})
	if explicit[inner] != int32(-1) {
		t.Fatalf("explicit -1 must pass through: %v", explicit[inner])
	}
}

func TestNftTransferRowArgsCarryInstructionEvidence(t *testing.T) {
	inner := insertColumnIndex(t, nftTransferInsertColumns, "inner_instruction_index")
	solana := nftTransferRowArgs(NftTransferRow{InstructionIndex: 5, InnerInstructionIndex: 2, SourceAPI: "data_api", SourceRequestID: "req-2"})
	if solana[insertColumnIndex(t, nftTransferInsertColumns, "instruction_index")] != uint32(5) || solana[inner] != int32(2) {
		t.Fatalf("instruction evidence = %v/%v", solana[insertColumnIndex(t, nftTransferInsertColumns, "instruction_index")], solana[inner])
	}
	if got := solana[insertColumnIndex(t, nftTransferInsertColumns, "source_api")]; got != "data_api" {
		t.Fatalf("source_api = %v", got)
	}
	direct := nftTransferRowArgs(NftTransferRow{})
	if direct[inner] != NoInnerInstruction {
		t.Fatalf("direct rows must fall back to -1: %v", direct[inner])
	}
}

// §8.4 表结构逐列映射；source_ingested_at 不在实参里（服务端生成）。
func TestSolanaProgramLogRowArgsMapNativeColumns(t *testing.T) {
	stamp := time.Unix(1789875226, 0).UTC()
	row := SolanaProgramLogRow{
		Chain: "solana", Slot: 448611120, Timestamp: stamp, Signature: "SIG", LogIndex: 3,
		ProgramID: "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", Message: "Program log: hello",
		InvokeDepth: 2, Success: 0, SourceProvider: "drpc", SourceAPI: "solana_rpc", SourceRequestID: "req-1",
	}
	args := solanaProgramLogRowArgs(row)
	checks := []struct {
		column string
		want   any
	}{
		{"chain", "solana"}, {"slot", uint64(448611120)}, {"timestamp", stamp}, {"signature", "SIG"},
		{"log_index", uint32(3)}, {"program_id", "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"},
		{"message", "Program log: hello"}, {"invoke_depth", uint16(2)}, {"success", uint8(0)},
		{"source_provider", "drpc"}, {"source_api", "solana_rpc"}, {"source_request_id", "req-1"},
	}
	for _, check := range checks {
		if got := args[insertColumnIndex(t, solanaProgramLogInsertColumns, check.column)]; got != check.want {
			t.Fatalf("%s = %v, want %v", check.column, got, check.want)
		}
	}
}
