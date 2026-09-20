package solana

import (
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
)

// NormalizeBalance 把 Provider 抓到的余额快照行转成 address_balances 行（ADR-024 D2）。
//
// 与 EVM 的差别必须显式保留：symbol/name/USD 价格没有可靠来源 → 留空，
// 不用 mint 地址冒充符号；快照槽位来自行内的 snapshotBlock（getSlot 实测值），
// 而不是调用方传进来的 shard 边界。
func NormalizeBalance(raw json.RawMessage) (ingest.BalanceRow, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ingest.BalanceRow{}, err
	}
	holder := strings.TrimSpace(stringField(m, "holderAddress"))
	if holder == "" {
		holder = strings.TrimSpace(stringField(m, "address"))
	}
	if holder == "" {
		return ingest.BalanceRow{}, errors.New("solana balance row missing holder address")
	}
	balanceRaw := strings.TrimSpace(stringField(m, "balanceRawInteger"))
	if balanceRaw == "" {
		balanceRaw = strings.TrimSpace(stringField(m, "balance"))
	}
	balance, ok := new(big.Int).SetString(balanceRaw, 10)
	if !ok {
		return ingest.BalanceRow{}, errors.New("solana balance row has invalid integer balance")
	}
	timestamp := numberField(m, "timestamp")
	if timestamp <= 0 {
		timestamp = time.Now().Unix()
	}
	decimals, err := strconv.ParseUint(strings.TrimSpace(stringField(m, "tokenDecimals")), 10, 16)
	if err != nil {
		return ingest.BalanceRow{}, errors.New("solana balance row has invalid decimals")
	}
	return ingest.BalanceRow{
		Chain: "solana", Address: holder,
		TokenSymbol: "", TokenName: "",
		TokenAddress:  strings.TrimSpace(stringField(m, "contractAddress")),
		TokenType:     strings.TrimSpace(stringField(m, "tokenType")),
		TokenDecimals: uint16(decimals),
		BalanceRaw:    balanceRaw, Balance: balance,
		// 没有价格来源：留空而不是编一个 0 或拿地址冒充符号。
		BalanceUSD: "", TokenPrice: "",
		SnapshotBlock: uint64(numberField(m, "snapshotBlock")),
		Timestamp:     time.Unix(timestamp, 0).UTC(),
		Provider:      "solana",
	}, nil
}
