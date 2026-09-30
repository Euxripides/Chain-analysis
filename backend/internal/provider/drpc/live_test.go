package drpc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/secret"
	soladdr "github.com/blockchain-platform/blockchain-platform/internal/solana"
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

func TestLiveRPCHistory(t *testing.T) {
	if os.Getenv("DRPC_RPC_HISTORY_LIVE") != "1" {
		t.Skip("explicit live RPC history test only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := New(Options{
		Enabled: true, RPCHistory: true, Tier: TierPaid,
		BaseURL: "https://lb.drpc.live/solana", RuntimeKeyRef: "store://drpc/runtime_api_key",
		Secrets: secret.NewManager(secret.ManagerOptions{}), HistoryPageSize: 2,
		ModeResolver: func(context.Context) string { return ModeSafe },
	}, nil)
	if !client.Configured(ctx) {
		t.Fatal("dRPC managed key unavailable")
	}
	head, err := client.ChainHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("finalized_head=%d", head)
	exec, planErr := client.plan(ctx, DatasetTransactions, ChannelSolanaRPC, OperationSolanaHistory)
	if planErr != nil {
		t.Fatal(planErr)
	}
	key, keyErr := client.runtimeKey(ctx)
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	firstAvailable, archiveErr := client.callRPC(ctx, exec, "live-rpc-archive", key, rpcCallSpec{method: "getFirstAvailableBlock", params: []any{}, cu: CURPCCall, dataset: DatasetTransactions, usageType: UsageProviderTest})
	if archiveErr != nil {
		t.Logf("first_available_block unavailable: %v", archiveErr)
	} else {
		t.Logf("first_available_block=%s", string(firstAvailable))
	}
	const sampleSignature = "5eDPXxyeaHuK9YJKPXpXbWUne9At11oQzdJv6hLmuVqQqneiiYdAMFDn7CYn9Edc9RQWbULAnv33GK34mnoSinfZ"
	sample, sampleErr := client.callRPC(ctx, exec, "live-sample", key, rpcCallSpec{method: MethodTransaction, params: []any{sampleSignature, map[string]any{"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": client.cfg.maxTxVersion}}, cu: CURPCCall, dataset: DatasetTransactions, usageType: UsageProviderTest})
	if sampleErr == nil {
		var tx struct {
			Transaction struct {
				Message struct {
					AccountKeys []struct {
						Pubkey string `json:"pubkey"`
					} `json:"accountKeys"`
				} `json:"message"`
			} `json:"transaction"`
		}
		if json.Unmarshal(sample, &tx) == nil {
			for i, account := range tx.Transaction.Message.AccountKeys {
				if i >= 50 {
					break
				}
				page, pageErr := client.callRPC(ctx, exec, "live-account-probe", key, rpcCallSpec{method: MethodSignatures, params: []any{account.Pubkey, map[string]any{"limit": 2, "commitment": "finalized"}}, cu: CURPCSignatures, dataset: DatasetTransactions, usageType: UsageProviderTest})
				if pageErr != nil {
					continue
				}
				var signatures []signatureInfo
				if json.Unmarshal(page, &signatures) == nil && len(signatures) == 1 {
					t.Logf("single_signature_sample_account=%s", account.Pubkey)
				}
			}
		}
	}
	const address = "5tzFkiKscXHK5ZXCGbXZxdw7gTjjD1mBwuoFbhUvuAi9"
	for _, dataset := range []string{DatasetTransactions, DatasetTokenTransfers, DatasetInternalTransactions, DatasetLogs} {
		cursor, rowsSeen := "", 0
		for page := 0; page < 12; page++ {
			result, fetchErr := client.Fetch(ctx, provider.FetchRequest{Chain: ChainSolana, Dataset: dataset, Address: address, ToBlock: &head, PageSize: 2, PageToken: cursor, RequestID: "live-rpc-" + dataset})
			if fetchErr != nil {
				t.Errorf("%s page %d: %v", dataset, page, fetchErr)
				break
			}
			rowsSeen += len(result.Rows)
			if dataset == DatasetTransactions && len(result.Rows) > 0 {
				var row struct {
					AccountKeys []string `json:"accountKeys"`
				}
				if json.Unmarshal(result.Rows[0], &row) == nil {
					for i, account := range row.AccountKeys {
						if i >= 20 {
							break
						}
						page, probeErr := client.callRPC(ctx, exec, "live-latest-account", key, rpcCallSpec{method: MethodSignatures, params: []any{account, map[string]any{"limit": 2, "commitment": "finalized"}}, cu: CURPCSignatures, dataset: DatasetTransactions, usageType: UsageProviderTest})
						if probeErr != nil {
							continue
						}
						var signatures []signatureInfo
						if json.Unmarshal(page, &signatures) == nil && len(signatures) == 1 {
							t.Logf("single_signature_recent_account=%s", account)
						}
					}
				}
			}
			if rowsSeen > 0 || !result.HasMore {
				break
			}
			cursor = result.NextPageToken
		}
		t.Logf("dataset=%s decoded_rows=%d", dataset, rowsSeen)
		if dataset == DatasetTransactions && rowsSeen == 0 {
			t.Error("active address returned no transaction rows within 12 pages")
		}
	}
	// A deterministic, valid address with no known activity exercises the
	// archive-to-exhaustion zero-row terminal contract. Active accounts are not
	// valid fixed-size fixtures: their signature counts change while testing.
	probeKey := sha256.Sum256([]byte("solana empty history release probe 20260929"))
	fullAddress := soladdr.EncodeBase58(probeKey[:])
	for _, dataset := range []string{DatasetTransactions, DatasetTokenTransfers, DatasetInternalTransactions, DatasetLogs} {
		result, fetchErr := client.Fetch(ctx, provider.FetchRequest{Chain: ChainSolana, Dataset: dataset, Address: fullAddress, ToBlock: &head, PageSize: 2, RequestID: "live-full-" + dataset})
		if fetchErr != nil {
			t.Errorf("full %s: %v", dataset, fetchErr)
			continue
		}
		if result.HasMore || result.NextPageToken != "" {
			t.Errorf("full %s did not exhaust the address: next=%q", dataset, result.NextPageToken)
		}
		if len(result.Rows) != 0 {
			t.Errorf("full %s expected an empty fixture, got %d rows", dataset, len(result.Rows))
		}
		t.Logf("full dataset=%s decoded_rows=%d exhausted=%t", dataset, len(result.Rows), !result.HasMore)
	}
}
