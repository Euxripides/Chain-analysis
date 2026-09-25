package drpc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/secret"
)

// 真实 dRPC 验收（默认跳过，只有显式设置 DRPC_LIVE_TEST=1 才跑）。
//
// 为什么保留这个测试：单测全部用 httptest 模拟，但"真实 Key + 真实端点"能验证到模拟
// 永远验证不了的东西 —— 上游真实响应形态、CU 计费口径、以及本包对真实错误码的分类。
// 2026-09-21 用它确认了：Solana 基础方法可用，而 getSignaturesForAddress 与 Data API 的
// Solana 历史不可用（详见 docs/implementation/drpc-solana-support-ticket.md）。
func TestLiveDataPlane(t *testing.T) {
	if os.Getenv("DRPC_LIVE_TEST") == "" {
		t.Skip("设置 DRPC_LIVE_TEST=1 才跑真实 dRPC 验收")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	mgr := secret.NewManager(secret.ManagerOptions{})
	var usage []string
	client := New(Options{
		Enabled: true, Tier: TierPaid,
		BaseURL:            "https://lb.drpc.live/solana",
		RuntimeKeyRef:      "store://drpc/runtime_api_key",
		StatisticsTokenRef: "store://drpc/statistics_api_token",
		KeysTokenRef:       "store://drpc/keys_api_token",
		Secrets:            mgr,
		ModeResolver:       func(context.Context) string { return ModeSafe },
	}, func(u model.UsageRow) {
		usage = append(usage, u.Provider+"/"+u.Method+"/"+u.APIType+"/"+u.UsageType)
	})
	if !client.Configured(ctx) {
		t.Fatalf("runtime key 不可解析：%s", client.UnavailableReason(ctx))
	}

	// 1) 链头：真实 getSlot
	head, err := client.ChainHead(ctx)
	if err != nil {
		t.Fatalf("ChainHead: %v", err)
	}
	t.Logf("ChainHead(getSlot) = %d", head)
	if head < 100_000_000 {
		t.Fatalf("链头不合理：%d", head)
	}

	// 2) 双通道探针：把两通道的真实状态打出来（不因失败而 fail，失败本身就是要观测的事实）
	probes := client.ProbeChannels(ctx)
	for _, ch := range []string{ChannelDataAPI, ChannelSolanaRPC} {
		t.Logf("probe[%s] = %+v", ch, probes[ch])
	}
	if !client.HistoryChannelAvailable(ctx) {
		t.Logf("警告：历史通道当前不可用（这是 2026-09-21 的已知 dRPC 侧状态），路由会回退到旧通道")
	}

	// 3) 数据面：balances 走 Solana JSON-RPC（getBalance / getTokenAccountsByOwner）
	res, ferr := client.Fetch(ctx, provider.FetchRequest{
		Chain: "solana", Dataset: "balances",
		Address:  "5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9",
		PageSize: 20, RequestID: "live-test-1",
	})
	if ferr != nil {
		t.Fatalf("balances fetch: %v", ferr)
	}
	t.Logf("balances rows=%d hasMore=%v decodedBytes=%d", len(res.Rows), res.HasMore, res.DecodedBytes)
	if len(res.Rows) == 0 {
		t.Fatal("balances 返回 0 行，说明真实行契约没有被满足")
	}
	for i, row := range res.Rows {
		if i >= 3 {
			break
		}
		t.Logf("  row[%d] = %s", i, string(row))
	}
	t.Logf("usage rows=%d %v", len(usage), usage)

	// 3b) 数据面：transactions 走 dRPC Data API（2026-09-22 起真实可用）
	for _, ds := range []string{"transactions", "token_transfers", "nft_transfers"} {
		txRes, txErr := client.Fetch(ctx, provider.FetchRequest{
			Chain: "solana", Dataset: ds,
			Address:  "5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9",
			PageSize: 3, RequestID: "live-test-" + ds,
		})
		if txErr != nil {
			t.Errorf("%s fetch: %v", ds, txErr)
			continue
		}
		token := txRes.NextPageToken
		if len(token) > 12 {
			token = token[:12]
		}
		t.Logf("%s rows=%d hasMore=%v nextToken=%q", ds, len(txRes.Rows), txRes.HasMore, token)
		for i, row := range txRes.Rows {
			if i >= 2 {
				break
			}
			t.Logf("  %s row[%d] = %s", ds, i, string(row))
		}
	}

	// 4) 额度视图：真实账户余额与当日 CU
	status := client.QuotaStatus()
	for _, k := range []string{"configured", "tier", "blocked", "has_snapshot", "effective_reason"} {
		t.Logf("quota[%s] = %v", k, status[k])
	}
}
