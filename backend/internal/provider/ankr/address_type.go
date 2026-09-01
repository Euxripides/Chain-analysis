package ankr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type addressTypeResponse struct {
	ID     int       `json:"id"`
	Result string    `json:"result"`
	Error  *rpcError `json:"error"`
}

// ResolveAddressTypes uses eth_getCode in one JSON-RPC batch. A non-empty
// runtime bytecode result is authoritative evidence that the address is a
// contract at the latest block.
func (c *Client) ResolveAddressTypes(ctx context.Context, chain string, addresses []string) (map[string]bool, error) {
	results := make(map[string]bool, len(addresses))
	requests := make([]rpcRequest, 0, len(addresses))
	idToAddress := make(map[int]string, len(addresses))
	seen := make(map[string]bool, len(addresses))
	for _, raw := range addresses {
		address := strings.ToLower(strings.TrimSpace(raw))
		if len(address) != 42 || !strings.HasPrefix(address, "0x") || seen[address] {
			continue
		}
		seen[address] = true
		results[address] = false
		id := int(c.idCounter.Add(1))
		idToAddress[id] = address
		requests = append(requests, rpcRequest{JSONRPC: "2.0", ID: id, Method: "eth_getCode", Params: []any{address, "latest"}})
	}
	if len(requests) == 0 {
		return results, nil
	}
	if err := c.limiter.Acquire(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(requests)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSuffix(c.endpoint, "/")
	endpoint = strings.TrimSuffix(endpoint, "/multichain") + "/" + strings.ToLower(chain)
	if c.apiKey != "" {
		endpoint += "/" + c.apiKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rpc http %d", res.StatusCode)
	}
	var decoded []addressTypeResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode eth_getCode batch: %w", err)
	}
	for _, item := range decoded {
		address, ok := idToAddress[item.ID]
		if !ok || item.Error != nil {
			continue
		}
		code := strings.TrimLeft(strings.TrimPrefix(strings.ToLower(item.Result), "0x"), "0")
		results[address] = code != ""
	}
	return results, nil
}
