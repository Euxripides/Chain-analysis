package method

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// §57 性能测试：Fast Resolver 不得明显降低主下载吞吐。
// 对照组是"不解析"，实验组是完整 Fast Resolver（内存索引 + TTL 缓存）。

func benchRegistry() *Registry {
	registry := NewRegistry()
	registry.SetSignatures(SeedSignatures())
	registry.SetABIs([]ABIRecord{{
		Chain: "bsc", ContractAddress: "0xtoken", ABIJSON: []byte(erc20ABI),
		Source: "verified_explorer", Verified: true,
	}})
	return registry
}

// benchRequests 模拟真实分布：原生转账 / ERC20 / 未知 Selector 混合。
func benchRequests(n int) []MethodResolveRequest {
	out := make([]MethodResolveRequest, n)
	transfer := "0xa9059cbb" + strings.Repeat("0", 128)
	unknown := "0x12ab34cd" + strings.Repeat("0", 64)
	for i := range out {
		req := MethodResolveRequest{Chain: "bsc", TxHash: "0x" + strconv.Itoa(i)}
		switch i % 5 {
		case 0, 1:
			req.ContractAddress = "0xpeer"
			req.Input = "0x"
			req.Value = "1000000000000000000"
		case 2, 3:
			req.ContractAddress = "0xtoken"
			req.Input = transfer
			req.Value = "0"
		default:
			req.ContractAddress = "0xcafe"
			req.Input = unknown
			req.Value = "0"
		}
		out[i] = req
	}
	return out
}

func BenchmarkFastResolver(b *testing.B) {
	resolver := NewFastResolver(benchRegistry(), func(ResolveJob) {})
	requests := benchRequests(1000)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := resolver.Resolve(ctx, requests[i%len(requests)]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFastResolverColdCache 关闭结果缓存效果（每次不同合约），衡量最坏路径。
func BenchmarkFastResolverColdCache(b *testing.B) {
	resolver := NewFastResolver(benchRegistry(), func(ResolveJob) {})
	transfer := "0xa9059cbb" + strings.Repeat("0", 128)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := resolver.Resolve(ctx, MethodResolveRequest{
			Chain: "bsc", ContractAddress: "0x" + strconv.Itoa(i), Input: transfer, Value: "0",
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkClassifyOnly 仅结构分类（下载链路必须承担的最小成本）。
func BenchmarkClassifyOnly(b *testing.B) {
	requests := benchRequests(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := requests[i%len(requests)]
		Classify(req.Input, req.Value, req.ContractAddress == "")
	}
}

func BenchmarkDecodeInput(b *testing.B) {
	abi, err := ParseABI([]byte(erc20ABI))
	if err != nil {
		b.Fatal(err)
	}
	m, _ := abi.Lookup("0xa9059cbb")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeInput(m, transferInput); err != nil {
			b.Fatal(err)
		}
	}
}
