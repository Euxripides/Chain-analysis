package ingest

import (
	"encoding/json"
	"testing"
)

func TestNormalizeNftTransferCarriesTokenStandard(t *testing.T) {
	// Ankr 的 NFT 行把标准放在 type（2026-09-20 用 BAYC tx 的 raw 对象实测 "ERC721"）。
	raw := json.RawMessage(`{"blockHeight":"14248724","transactionHash":"0xda308d","logIndex":360,"contractAddress":"0xBC4CA0EdA7647A8aB7C2061c2E118A18a936f13D","tokenId":"4937","fromAddress":"0xFE348A1C6401b6A5C01Bd4deEf7BBac9148Be0cE","toAddress":"0x26db9404cf88268739103924C15BFAB7345E8812","value":"1","type":"ERC721","collectionName":"BoredApeYachtClub","collectionSymbol":"BAYC","timestamp":1645437472}`)
	row, err := NormalizeNftTransfer(raw, "eth", "ankr")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if row.TokenStandard != "ERC721" {
		t.Fatalf("EVM rows must carry the standard from type: %+v", row)
	}
	if row.Contract != "0xbc4ca0eda7647a8ab7c2061c2e118a18a936f13d" || row.TokenID != "4937" {
		t.Fatalf("unexpected identity: %+v", row)
	}
	if row.TokenName != "BoredApeYachtClub" || row.TokenSymbol != "BAYC" {
		t.Fatalf("collection must still ride on name/symbol: %+v", row)
	}

	// Solana 行用 tokenStandard（SPL），不能被误认成 ERC721。
	solanaRaw := json.RawMessage(`{"blockHeight":"1","transactionHash":"SIG","contractAddress":"Mint","value":"1","tokenStandard":"SPL","timestamp":1}`)
	solanaRow, err := NormalizeNftTransfer(solanaRaw, "solana", "solana")
	if err != nil {
		t.Fatalf("normalize solana: %v", err)
	}
	if solanaRow.TokenStandard != "SPL" {
		t.Fatalf("solana rows must carry SPL: %+v", solanaRow)
	}

	// 没有来源就留空，绝不默认成 ERC721。
	missing := json.RawMessage(`{"blockHeight":"1","transactionHash":"SIG","contractAddress":"M","value":"1","timestamp":1}`)
	missingRow, err := NormalizeNftTransfer(missing, "eth", "ankr")
	if err != nil {
		t.Fatalf("normalize missing: %v", err)
	}
	if missingRow.TokenStandard != "" {
		t.Fatalf("missing standard must stay empty, got %q", missingRow.TokenStandard)
	}
}
