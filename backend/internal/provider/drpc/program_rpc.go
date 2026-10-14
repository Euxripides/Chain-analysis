package drpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// ReadProgramRPC uses the existing managed key, limiter, quota, retry and usage
// path. Callers must supply the durable contract-analysis budget context.
func (c *Client) ReadProgramRPC(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	allowed := map[string]bool{
		"getAccountInfo": true, "getMultipleAccounts": true, "getGenesisHash": true,
		"getSlot": true, "getBlockTime": true, "getBlock": true, "getTransaction": true,
	}
	if !allowed[method] {
		return nil, fmt.Errorf("unsupported program read method")
	}
	exec, err := c.plan(ctx, "contract_analysis", ChannelSolanaRPC, "contract_analysis")
	if err != nil {
		return nil, err
	}
	key, err := c.runtimeKey(ctx)
	if err != nil {
		return nil, err
	}
	requestID := c.requestID(provider.FetchRequest{})
	return c.callRPC(ctx, exec, requestID, key, rpcCallSpec{
		method: method, params: params, cu: CURPCCall, usageType: "contract_analysis",
		dataset: "contract_analysis", rows: 1,
	})
}
