package tron

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// TxRecord reads one exact raw transaction through the existing runtime,
// usage accounting and provider limits. It does not fetch address history.
func (c *AnkrClient) TxRecord(ctx context.Context, chain, txHash string) (json.RawMessage, error) {
	value := strings.TrimPrefix(strings.TrimSpace(txHash), "0x")
	if raw, err := hex.DecodeString(value); chain != "tron" || err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("complete TRON transaction hash required")
	}
	var raw json.RawMessage
	_, err := c.rest(ctx, provider.FetchRequest{Chain: "tron", Dataset: "transaction_detail"}, "wallet/gettransactionbyid", map[string]any{"value": value, "visible": true}, "tx_detail", &raw)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (r *DatasetRouter) TxRecord(ctx context.Context, chain, hash string) (json.RawMessage, error) {
	p := r.ReceiptProvider
	if p == nil {
		p = r.Default
	}
	if reader, ok := p.(interface {
		TxRecord(context.Context, string, string) (json.RawMessage, error)
	}); ok {
		return reader.TxRecord(ctx, chain, hash)
	}
	return nil, fmt.Errorf("TRON raw transaction reader unavailable")
}

func (d *Dispatcher) TxRecord(ctx context.Context, chain, hash string) (json.RawMessage, error) {
	if chain == "tron" {
		if reader, ok := d.Tron.(interface {
			TxRecord(context.Context, string, string) (json.RawMessage, error)
		}); ok {
			return reader.TxRecord(ctx, chain, hash)
		}
	}
	return nil, fmt.Errorf("TRON raw transaction reader unavailable")
}

func (c *AnkrClient) TxTokenDecimals(ctx context.Context, chain, contract, owner string) (int, error) {
	if chain != "tron" || ValidateTronAddress(contract) != nil || ValidateTronAddress(owner) != nil {
		return -1, fmt.Errorf("valid TRON token and owner required")
	}
	var reply struct {
		Result struct {
			Success bool `json:"result"`
		} `json:"result"`
		Values []string `json:"constant_result"`
	}
	_, err := c.rest(ctx, provider.FetchRequest{Chain: "tron", Dataset: "token_metadata"}, "wallet/triggerconstantcontract", map[string]any{"owner_address": owner, "contract_address": contract, "function_selector": "decimals()", "visible": true}, "token_metadata", &reply)
	if err != nil || !reply.Result.Success || len(reply.Values) != 1 {
		return -1, fmt.Errorf("TRON token decimals unavailable")
	}
	n, ok := new(big.Int).SetString(strings.TrimPrefix(reply.Values[0], "0x"), 16)
	if !ok || !n.IsUint64() || n.Uint64() > 38 {
		return -1, fmt.Errorf("invalid TRON token decimals")
	}
	return int(n.Uint64()), nil
}
func (r *DatasetRouter) TxTokenDecimals(ctx context.Context, chain, contract, owner string) (int, error) {
	if reader, ok := r.providerForCapability("receipt").(interface {
		TxTokenDecimals(context.Context, string, string, string) (int, error)
	}); ok {
		return reader.TxTokenDecimals(ctx, chain, contract, owner)
	}
	return -1, fmt.Errorf("TRON token decimals reader unavailable")
}
func (d *Dispatcher) TxTokenDecimals(ctx context.Context, chain, contract, owner string) (int, error) {
	if chain == "tron" {
		if reader, ok := d.Tron.(interface {
			TxTokenDecimals(context.Context, string, string, string) (int, error)
		}); ok {
			return reader.TxTokenDecimals(ctx, chain, contract, owner)
		}
	}
	return -1, fmt.Errorf("TRON token decimals reader unavailable")
}
