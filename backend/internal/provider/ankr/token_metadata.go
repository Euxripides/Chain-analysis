package ankr

import (
	"bytes"
	"context"
	encodinghex "encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

const (
	selectorName     = "0x06fdde03"
	selectorSymbol   = "0x95d89b41"
	selectorDecimals = "0x313ce567"
)

func (c *Client) ResolveTokenMetadata(ctx context.Context, chain, contract string) (provider.TokenMetadataResult, error) {
	var out provider.TokenMetadataResult
	nameRaw, nameErr := c.ethCall(ctx, chain, contract, selectorName)
	if nameErr == nil {
		if v, e := decodeTokenString(nameRaw); e == nil {
			out.Name = &v
		} else {
			out.NameError = e.Error()
		}
	} else {
		out.NameError = nameErr.Error()
	}
	symbolRaw, symbolErr := c.ethCall(ctx, chain, contract, selectorSymbol)
	if symbolErr == nil {
		if v, e := decodeTokenString(symbolRaw); e == nil {
			out.Symbol = &v
		} else {
			out.SymbolError = e.Error()
		}
	} else {
		out.SymbolError = symbolErr.Error()
	}
	decRaw, decErr := c.ethCall(ctx, chain, contract, selectorDecimals)
	if decErr == nil {
		if v, e := decodeTokenDecimals(decRaw); e == nil {
			out.Decimals = &v
		} else {
			out.DecimalsError = e.Error()
		}
	} else {
		out.DecimalsError = decErr.Error()
	}
	if out.Name == nil && out.Symbol == nil && out.Decimals == nil {
		return out, fmt.Errorf("all token metadata calls failed: name=%s symbol=%s decimals=%s", out.NameError, out.SymbolError, out.DecimalsError)
	}
	return out, nil
}

func (c *Client) ethCall(ctx context.Context, chain, contract, data string) (string, error) {
	if err := c.limiter.Acquire(ctx); err != nil {
		return "", err
	}
	endpoint := strings.TrimSuffix(c.endpoint, "/")
	endpoint = strings.TrimSuffix(endpoint, "/multichain") + "/" + strings.ToLower(chain)
	if c.apiKey != "" {
		endpoint += "/" + c.apiKey
	}
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: int(c.idCounter.Add(1)), Method: "eth_call", Params: map[string]any{"to": contract, "data": data, "block": "latest"}})
	// Standard JSON-RPC expects positional params.
	var request map[string]any
	_ = json.Unmarshal(body, &request)
	request["params"] = []any{map[string]string{"to": contract, "data": data}, "latest"}
	body, _ = json.Marshal(request)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("rpc http %d", res.StatusCode)
	}
	var decoded struct {
		Result string    `json:"result"`
		Error  *rpcError `json:"error"`
	}
	if err = json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	if decoded.Error != nil {
		return "", fmt.Errorf("rpc %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	if decoded.Result == "" || decoded.Result == "0x" {
		return "", fmt.Errorf("empty rpc result")
	}
	return decoded.Result, nil
}

func decodeTokenString(value string) (string, error) {
	b, err := encodinghex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil {
		return "", err
	}
	if len(b) < 32 {
		return "", fmt.Errorf("short ABI result")
	}
	// Standard dynamic ABI string: offset word, length word, bytes.
	if len(b) >= 64 {
		offset := new(big.Int).SetBytes(b[:32])
		if offset.IsUint64() {
			o := int(offset.Uint64())
			if o >= 0 && o+32 <= len(b) {
				n := new(big.Int).SetBytes(b[o : o+32])
				if n.IsUint64() {
					length := int(n.Uint64())
					if length >= 0 && o+32+length <= len(b) {
						v := strings.TrimRight(string(b[o+32:o+32+length]), "\x00")
						if strings.TrimSpace(v) != "" {
							return v, nil
						}
					}
				}
			}
		}
	}
	// Non-standard bytes32 fallback.
	v := strings.TrimRight(string(b[:32]), "\x00")
	if strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("empty token string")
	}
	return v, nil
}

func decodeTokenDecimals(value string) (int, error) {
	b, err := encodinghex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil {
		return 0, err
	}
	if len(b) < 32 {
		return 0, fmt.Errorf("short decimals result")
	}
	n := new(big.Int).SetBytes(b[len(b)-32:])
	if !n.IsInt64() || n.Int64() < 0 || n.Int64() > 255 {
		return 0, fmt.Errorf("decimals outside 0..255")
	}
	return int(n.Int64()), nil
}
