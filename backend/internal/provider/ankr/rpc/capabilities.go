package ankrrpc

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// ---- Capability（§18-§21）----
//
// 能力必须按 Provider + Chain 管理；不能写成 "Ankr supports trace = true"（§19）。
// 静态配置 + 启动探测 + 运行时学习共同决定最终能力（§20）。

// DefaultCapabilities Ankr 各链的静态默认能力。
// Trace/Debug 依赖账号计划（Premium），因此默认开启但允许探测/运行时学习关闭（§5/§20）。
func DefaultCapabilities() rpc.CapabilitySet {
	return rpc.CapabilitySet{
		StandardRPC: true, Archive: true, Batch: true, Trace: true, Debug: true,
		WebSocket: false, GRPC: false,
		MaxBatchSize: 100, MaxLogBlockRange: 5000, MaxTraceBlockRange: 100,
		Source: rpc.CapSourceStatic,
	}
}

// Capabilities 返回该链的静态能力。
func (c *Client) Capabilities(ctx context.Context, chainSlug string) (rpc.CapabilitySet, error) {
	_ = ctx
	slug := strings.ToLower(strings.TrimSpace(chainSlug))
	if _, ok := c.chainPath(slug); !ok {
		return rpc.CapabilitySet{}, &rpc.Error{Class: rpc.ClassMethodUnsupported, Provider: c.Name(),
			Chain: slug, Message: "chain is not mapped to an ankr rpc path"}
	}
	if caps, ok := c.opts.StaticCapabilities[slug]; ok {
		return caps, nil
	}
	return DefaultCapabilities(), nil
}

// Probe 启动探测：只做低成本调用（§21）。
//
//   - eth_blockNumber   → Standard RPC
//   - eth_getBalance @1 → Archive（固定历史区块）
//   - 2 条 Batch        → Batch
//
// Trace/Debug 不在这里频繁探测：由静态配置给出，运行时遇到 -32601 再学习关闭。
func (c *Client) Probe(ctx context.Context, chainSlug string) (rpc.CapabilitySet, error) {
	caps, err := c.Capabilities(ctx, chainSlug)
	if err != nil {
		return rpc.CapabilitySet{}, err
	}
	caps.Source = rpc.CapSourceProbe

	head, headErr := c.Call(ctx, rpc.Request{Chain: chainSlug, Method: "eth_blockNumber",
		Params: []any{}, Class: rpc.ClassChainHead})
	if headErr != nil {
		caps.StandardRPC = false
		return caps, headErr
	}
	caps.StandardRPC = true
	_ = head

	// Archive 探测：查询创世附近区块的余额，非 archive 节点会报 missing trie node
	if _, archiveErr := c.Call(ctx, rpc.Request{Chain: chainSlug, Method: "eth_getBalance",
		Params: []any{"0x0000000000000000000000000000000000000000", chain.AtBlock(1).String()},
		Class:  rpc.ClassBalance, BlockRef: chain.AtBlock(1)}); archiveErr != nil {
		if rpc.IsClass(archiveErr, rpc.ClassArchiveRequired) || rpc.IsClass(archiveErr, rpc.ClassMethodUnsupported) {
			caps.Archive = false
		}
	} else {
		caps.Archive = true
	}

	// Batch 探测
	results, batchErr := c.BatchCall(ctx, []rpc.Request{
		{Chain: chainSlug, Method: "eth_blockNumber", Params: []any{}, Class: rpc.ClassChainHead},
		{Chain: chainSlug, Method: "eth_chainId", Params: []any{}, Class: rpc.ClassNetwork},
	})
	caps.Batch = batchErr == nil && len(results) == 2 && results[0].Error == nil
	return caps, nil
}

// ChainID 读取链上 chainId，用于与 Chain Registry 交叉校验。
func (c *Client) ChainID(ctx context.Context, chainSlug string) (uint64, error) {
	response, err := c.Call(ctx, rpc.Request{Chain: chainSlug, Method: "eth_chainId",
		Params: []any{}, Class: rpc.ClassNetwork})
	if err != nil {
		return 0, err
	}
	var hexValue string
	if err := json.Unmarshal(response.Result, &hexValue); err != nil {
		return 0, &rpc.Error{Class: rpc.ClassInvalidResponse, Provider: c.Name(), Chain: chainSlug,
			Message: "chainId is not a hex string"}
	}
	return chain.HexToUint64(hexValue)
}
