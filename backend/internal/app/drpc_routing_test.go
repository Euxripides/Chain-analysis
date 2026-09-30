package app

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider/route"
)

func solanaPolicyRows() []route.PolicyRow {
	return []route.PolicyRow{
		{PolicyKey: "solana:transactions", Chain: "solana", Dataset: "transactions",
			Enabled: true, PrimaryProvider: "drpc", APIChannel: route.ChannelDataAPI,
			OperationClass: route.OperationSolanaHistory, Revision: 2},
		{PolicyKey: "solana:balances", Chain: "solana", Dataset: "balances",
			Enabled: true, PrimaryProvider: "drpc", APIChannel: route.ChannelSolanaRPC,
			OperationClass: route.OperationSolanaBalance, Revision: 1},
		{PolicyKey: "tron:transactions", Chain: "tron", Dataset: "transactions",
			Enabled: true, PrimaryProvider: "tron-grid", APIChannel: route.ChannelTronGrid,
			OperationClass: route.OperationTronHistory, Revision: 1},
		{PolicyKey: "bsc:transactions", Chain: "bsc", Dataset: "transactions",
			Enabled: true, PrimaryProvider: "ankr-multichain", APIChannel: route.ChannelAnkrAdvanced,
			OperationClass: route.OperationEVMHistory, Revision: 1},
	}
}

// 未启用 Data API 历史时：历史留在旧通道（slot 精确、稳定），余额可以走 dRPC。
//
// 这个组合是 2026-09-22 的线上事实：dRPC 的 Solana Data API 时好时坏且不返回 slot，
// 而它的 getBalance 稳定可用。把两者绑在一起切换，要么白扔可用的余额能力，
// 要么把历史下载送进不稳定通道。
func TestProjectSolanaPoliciesSplitsBalancesFromHistory(t *testing.T) {
	raw := solanaPolicyRows()
	plan := solanaRoutePlan{balancesKey: route.ProviderDRPC, historyKey: "solana-rpc"}
	rows := projectSolanaPolicies(raw, plan)

	if rows[0].PrimaryProvider != "solana-rpc" || rows[0].APIChannel != route.ChannelSolanaRPC {
		t.Fatalf("历史应留在旧通道且通道跟着 Provider 走：%+v", rows[0])
	}
	if rows[1].PrimaryProvider != route.ProviderDRPC || rows[1].APIChannel != route.ChannelSolanaRPC {
		t.Fatalf("余额应走 dRPC 的 Solana JSON-RPC 通道：%+v", rows[1])
	}
	// 语义字段（操作类 / 策略版本）必须原样透传。
	if rows[0].OperationClass != route.OperationSolanaHistory || rows[0].Revision != 2 {
		t.Fatalf("投影不应改变操作类/revision: %+v", rows[0])
	}
	// 非 Solana 链的别名归一化照常生效。
	if rows[2].PrimaryProvider != "trongrid" || rows[3].PrimaryProvider != "ankr" {
		t.Fatalf("别名归一化失效: %q / %q", rows[2].PrimaryProvider, rows[3].PrimaryProvider)
	}
	// 原有输入不能被就地修改。
	if raw[0].PrimaryProvider != "drpc" || raw[1].APIChannel != route.ChannelSolanaRPC {
		t.Fatalf("投影函数修改了输入切片: %+v", raw[0])
	}
}

// 历史通道真正就绪并显式启用时，历史数据集才切到 dRPC 的 Data API。
func TestProjectSolanaPoliciesHistoryOnDataAPI(t *testing.T) {
	plan := solanaRoutePlan{balancesKey: route.ProviderDRPC, historyKey: route.ProviderDRPC, historyDataAPI: true}
	rows := projectSolanaPolicies(solanaPolicyRows(), plan)
	if rows[0].PrimaryProvider != route.ProviderDRPC || rows[0].APIChannel != route.ChannelDataAPI {
		t.Fatalf("启用后历史应走 dRPC Data API：%+v", rows[0])
	}
	if rows[1].APIChannel != route.ChannelSolanaRPC {
		t.Fatalf("余额始终走 JSON-RPC 通道：%+v", rows[1])
	}
}

// 没有旧通道可回退时 fail closed：所有 Solana 数据集清空 Provider，
// 由链级默认决定（未注册则报 unsupported），绝不静默取别的链的数据。
func TestSolanaRoutePlanFailClosed(t *testing.T) {
	plan := solanaRoutePlan{}
	if plan.anyEnabled() {
		t.Fatal("空计划不应启用 Solana 路由")
	}
	rows := projectSolanaPolicies(solanaPolicyRows(), plan)
	if rows[0].PrimaryProvider != "solana-rpc" || rows[1].PrimaryProvider != "solana-rpc" {
		t.Fatalf("空计划应把 Solana 行落到旧通道默认值（不启用路由时不会被读到）：%+v", rows[:2])
	}
}

func TestProjectSolanaInternalRouteUsesFinalizedRPC(t *testing.T) {
	rows := projectSolanaPolicies([]route.PolicyRow{{
		PolicyKey: "solana:internal_transactions", Chain: "solana", Dataset: "internal_transactions",
		Enabled: true, PrimaryProvider: "drpc", APIChannel: route.ChannelSolanaRPC,
		OperationClass: route.OperationSolanaHistory, Revision: 1,
	}}, solanaRoutePlan{historyKey: route.ProviderDRPC})
	if len(rows) != 1 || rows[0].PrimaryProvider != route.ProviderDRPC || rows[0].APIChannel != route.ChannelSolanaRPC {
		t.Fatalf("internal_transactions must route through finalized dRPC RPC: %+v", rows)
	}
}
