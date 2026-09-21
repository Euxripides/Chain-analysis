package app

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/provider/route"
)

// Solana 策略投影是本次接入最容易踩的坑：迁移把策略指向 drpc，但部署顺序是「先上代码、后录 Key」。
// 这个测试固定住「未就绪时必须投影回旧通道」，否则一旦回归，Solana 下载会在 Key 录入前全部失败。
func TestProjectSolanaPoliciesFallsBackUntilDRPCReady(t *testing.T) {
	raw := []route.PolicyRow{
		{PolicyKey: "solana:transactions", Chain: "solana", Dataset: "transactions",
			Enabled: true, PrimaryProvider: "drpc", APIChannel: route.ChannelDataAPI,
			OperationClass: route.OperationSolanaHistory, Revision: 2},
		{PolicyKey: "tron:transactions", Chain: "tron", Dataset: "transactions",
			Enabled: true, PrimaryProvider: "tron-grid", APIChannel: route.ChannelTronGrid,
			OperationClass: route.OperationTronHistory, Revision: 1},
		{PolicyKey: "bsc:transactions", Chain: "bsc", Dataset: "transactions",
			Enabled: true, PrimaryProvider: "ankr-multichain", APIChannel: route.ChannelAnkrAdvanced,
			OperationClass: route.OperationEVMHistory, Revision: 1},
	}

	notReady := projectSolanaPolicies(raw, false)
	if notReady[0].PrimaryProvider != "solana-rpc" {
		t.Fatalf("未配置 Key 时 Solana 必须回退到旧通道，得到 %q", notReady[0].PrimaryProvider)
	}
	// 语义字段不能被改动。
	if notReady[0].APIChannel != route.ChannelDataAPI || notReady[0].OperationClass != route.OperationSolanaHistory || notReady[0].Revision != 2 {
		t.Fatalf("投影不应改变通道/操作类/revision: %+v", notReady[0])
	}
	// 非 Solana 链的别名归一化照常生效，且不受 dRPC 开关影响。
	if notReady[1].PrimaryProvider != "trongrid" || notReady[2].PrimaryProvider != "ankr" {
		t.Fatalf("别名归一化失效: %q / %q", notReady[1].PrimaryProvider, notReady[2].PrimaryProvider)
	}

	ready := projectSolanaPolicies(raw, true)
	if ready[0].PrimaryProvider != route.ProviderDRPC {
		t.Fatalf("Key 就绪后 Solana 必须走 drpc，得到 %q", ready[0].PrimaryProvider)
	}
	// 原始输入不能被就地修改（否则下一次投影拿到的已经是改过的行）。
	if raw[0].PrimaryProvider != "drpc" {
		t.Fatalf("投影函数修改了输入切片: %q", raw[0].PrimaryProvider)
	}
}
