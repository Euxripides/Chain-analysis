package solana

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

const (
	testNFTMint = "NftMint111111111111111111111111111111111111"
	testFTMint  = "FtMint1111111111111111111111111111111111111"
	testBadMint = "BadMint111111111111111111111111111111111111"
)

// mintRPC 只为 getAccountInfo 服务：NFT mint 返回 decimals=0/supply=1，
// 普通 mint 返回 decimals=6/supply=1000，坏 mint 返回错误。
func mintRPC(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mint := ""
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params[0], &mint)
		}
		w.Header().Set("Content-Type", "application/json")
		switch mint {
		case testNFTMint:
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"value":{"data":{"parsed":{"info":{"decimals":0,"supply":"1"}}}}}}`)
		case testFTMint:
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"value":{"data":{"parsed":{"info":{"decimals":6,"supply":"1000"}}}}}}`)
		default:
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid mint"}}`)
		}
	}))
}

func TestClassifyMintDetectsClassicNFT(t *testing.T) {
	mintClasses = newMintCache()
	server := mintRPC(t)
	defer server.Close()
	client := newTestClient(t, server.URL)

	if class := client.classifyMint(context.Background(), testNFTMint); !class.known || !class.isNFT {
		t.Fatalf("decimals=0 supply=1 must classify as a classic NFT: %+v", class)
	}
	if class := client.classifyMint(context.Background(), testFTMint); !class.known || class.isNFT {
		t.Fatalf("decimals=6 supply=1000 must stay fungible: %+v", class)
	}
	// 查不到就保守：known=false，调用方据此留在 token_transfers 并计数。
	if class := client.classifyMint(context.Background(), testBadMint); class.known {
		t.Fatalf("unknown mint must not be classified: %+v", class)
	}
}

func TestNFTAndFungibleNeverShareATransfer(t *testing.T) {
	rows := []tokenTransferRow{
		{ContractAddress: testNFTMint, TransactionHash: "SIG", LogIndex: 1, GrossAmountRaw: "1", ValueRawInteger: "1"},
		{ContractAddress: testFTMint, TransactionHash: "SIG", LogIndex: 2, GrossAmountRaw: "100", ValueRawInteger: "100"},
	}
	classes := map[string]mintClass{
		testNFTMint: {known: true, isNFT: true},
		testFTMint:  {known: true, isNFT: false},
	}

	fungible, skipped := filterFungibleTransfers(rows, classes)
	if len(fungible) != 1 || fungible[0].ContractAddress != testFTMint || skipped != 1 {
		t.Fatalf("token_transfers must drop the NFT and keep the fungible one: %+v skipped=%d", fungible, skipped)
	}

	nftRows := toNFTRows(rows, classes)
	if len(nftRows) != 1 {
		t.Fatalf("nft_transfers must contain exactly the NFT transfer, got %+v", nftRows)
	}
	if nftRows[0].ContractAddress != testNFTMint || nftRows[0].TokenID != "" {
		t.Fatalf("solana NFT rows must keep the mint as contract and leave tokenId empty: %+v", nftRows[0])
	}
	if nftRows[0].TokenName != "" || nftRows[0].TokenSymbol != "" {
		t.Fatalf("metadata has no source and must stay empty: %+v", nftRows[0])
	}
}

func TestUnclassifiedMintStaysFungible(t *testing.T) {
	rows := []tokenTransferRow{{ContractAddress: testBadMint, TransactionHash: "SIG", LogIndex: 1}}
	classes := map[string]mintClass{testBadMint: {known: false}}
	fungible, skipped := filterFungibleTransfers(rows, classes)
	if len(fungible) != 1 || skipped != 0 {
		t.Fatalf("unclassified mints must stay in token_transfers: %+v skipped=%d", fungible, skipped)
	}
	if nft := toNFTRows(rows, classes); len(nft) != 0 {
		t.Fatalf("unclassified mints must not enter nft_transfers: %+v", nft)
	}
}

func TestFetchRejectsNftOnOtherChains(t *testing.T) {
	server := mintRPC(t)
	defer server.Close()
	client := newTestClient(t, server.URL)
	if _, err := client.Fetch(context.Background(), provider.FetchRequest{Chain: "bsc", Dataset: "nft_transfers"}); err == nil {
		t.Fatal("solana provider must reject non-solana chains")
	}
}
