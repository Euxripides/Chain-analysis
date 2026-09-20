package solana

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
)

// NormalizeNftTransfer 把 Provider 归类出的经典 NFT 转账行转成 nft_transfers 行（ADR-024 D3）。
//
// TokenID 恒为空：Solana 没有 EVM 的 tokenId，NFT 的身份就是 mint 地址本身
// （把 mint 塞进 tokenId 会与 EVM 的 (contract, tokenId) 语义冲突）。
func NormalizeNftTransfer(raw json.RawMessage) (ingest.NftTransferRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ingest.NftTransferRow{}, err
	}
	txHash := strings.TrimSpace(stringField(m, "transactionHash"))
	if txHash == "" {
		return ingest.NftTransferRow{}, errors.New("solana nft row missing transactionHash")
	}
	contract := strings.TrimSpace(stringField(m, "contractAddress"))
	if contract == "" {
		return ingest.NftTransferRow{}, errors.New("solana nft row missing contract address")
	}
	blockNumber, err := strconv.ParseUint(strings.TrimSpace(stringField(m, "blockNumber")), 10, 64)
	if err != nil {
		// 兼容 blockHeight（Ankr 行风格）
		blockNumber, err = strconv.ParseUint(strings.TrimSpace(stringField(m, "blockHeight")), 10, 64)
		if err != nil {
			return ingest.NftTransferRow{}, errors.New("solana nft row has invalid block number")
		}
	}
	timestamp := numberField(m, "timestamp")
	if timestamp <= 0 {
		timestamp = time.Now().Unix()
	}
	return ingest.NftTransferRow{
		Chain: "solana", BlockNumber: blockNumber, Timestamp: time.Unix(timestamp, 0).UTC(),
		TxHash: txHash, LogIndex: uint32(numberField(m, "logIndex")),
		Contract: contract,
		TokenID:  "",
		From:     strings.TrimSpace(stringField(m, "fromAddress")),
		To:       strings.TrimSpace(stringField(m, "toAddress")),
		// 没有元数据来源：留空，不用 mint 冒充 NFT 名称。
		TokenName: "", TokenSymbol: "",
		// Solana 没有 ERC 标准；Provider 标注 SPL，缺失时留空而不是冒充 ERC721。
		TokenStandard: strings.TrimSpace(stringField(m, "tokenStandard")),
		AmountRaw:     strings.TrimSpace(stringField(m, "value")),
		Provider:      "solana",
	}, nil
}
