package model

import (
	"strings"
	"sync/atomic"
)

// Dataset 能力状态：前端只允许勾选 available 项（开发文档 §5.2）。
const (
	DatasetAvailable   = "available"
	DatasetUnsupported = "unsupported"
)

// solanaDataAPI 表示 Solana 是否已切到 dRPC Data API 通道。
//
// 这不是"偏好"而是**能力差异**：Data API 直接返回地址交易历史与结构化 Transfers，
// 因此 nft_transfers 有明确来源；旧的 getSignaturesForAddress+getTransaction 路径只能靠
// decimals=0 猜测 NFT，ADR-023 D9 因此把它排除在白名单外。两者不能共用一份白名单。
var solanaDataAPI atomic.Bool
var solanaDRPCHistory atomic.Bool
var solanaDRPCBalances atomic.Bool

func SetSolanaDRPCRoutes(history, balances bool) {
	solanaDRPCHistory.Store(history)
	solanaDRPCBalances.Store(balances)
}

// SetSolanaDataAPI 由 app 启动时按 provider 路由结果设置一次（进程级能力开关）。
func SetSolanaDataAPI(enabled bool) { solanaDataAPI.Store(enabled) }

// SolanaDataAPIEnabled 报告当前进程是否走 dRPC Data API。
func SolanaDataAPIEnabled() bool { return solanaDataAPI.Load() }

// DatasetCapability 是某条链上某个 Dataset 的实际取数能力。
//
// 它比"能不能下载"更细：同一 Dataset 在不同 Provider/通道上的范围语义不同
// （Solana 的块范围是 Slot；Data API 支持时间范围，JSON-RPC 只能靠 Slot 换算）。
type DatasetCapability struct {
	ID string `json:"id"`
	// Label 是用户可见名称；由 API 层填充，模型层保持空。
	Label string `json:"label,omitempty"`
	// Status 为 available | unsupported；unsupported 的项前端不可勾选。
	Status string `json:"status"`
	// Provider / APIChannel 是该 Dataset 的实际来源；unsupported 时为空。
	Provider   string `json:"provider,omitempty"`
	APIChannel string `json:"api_channel,omitempty"`
	// 范围支持：full=全量历史，time=时间范围，block=区块/Slot 范围。
	SupportsFull       bool `json:"supports_full"`
	SupportsTimeRange  bool `json:"supports_time_range"`
	SupportsBlockRange bool `json:"supports_block_range"`
}

// ChainDatasetCapabilities 返回该链逐 Dataset 的能力表。
// chain 为空或未登记时返回全量可下载（向后兼容既有调用方）。
func ChainDatasetCapabilities(chain string) []DatasetCapability {
	key := strings.ToLower(strings.TrimSpace(chain))
	if key == "solana" {
		return solanaDatasetCapabilities()
	}
	if key == "" {
		out := make([]DatasetCapability, 0, len(AllDatasets))
		for _, ds := range AllDatasets {
			out = append(out, DatasetCapability{
				ID: ds, Status: DatasetAvailable,
				SupportsFull: true, SupportsTimeRange: true, SupportsBlockRange: true,
			})
		}
		return out
	}
	listed := ChainDatasets(key)
	provider, channel := chainDefaultSource(key)
	out := make([]DatasetCapability, 0, len(AllDatasets))
	available := make(map[string]bool, len(listed))
	for _, ds := range listed {
		available[ds] = true
	}
	for _, ds := range AllDatasets {
		cap := DatasetCapability{ID: ds, Status: DatasetUnsupported}
		if available[ds] {
			cap.Status = DatasetAvailable
			cap.Provider = provider
			cap.APIChannel = channel
			// balances 是快照语义：没有 range 概念，由下载层特殊处理。
			if ds != "balances" {
				cap.SupportsFull, cap.SupportsTimeRange, cap.SupportsBlockRange = true, true, true
			}
		}
		out = append(out, cap)
	}
	return out
}

// solanaDatasetCapabilities 是 Solana 的能力表（唯一事实来源）。
func solanaDatasetCapabilities() []DatasetCapability {
	dataAPI := solanaDataAPI.Load()
	provider, dataChannel, rpcChannel := "solana-rpc", "solana_rpc", "solana_rpc"
	if solanaDRPCHistory.Load() {
		provider = "drpc"
	}
	if dataAPI {
		provider, dataChannel = "drpc", "data_api"
	}
	items := []DatasetCapability{
		{ID: "transactions", Status: DatasetAvailable, Provider: provider, APIChannel: dataChannel},
		{ID: "token_transfers", Status: DatasetAvailable, Provider: provider, APIChannel: dataChannel},
		// internal_transactions（CPI 调用树）不是 dRPC 的能力，但现有 JSON-RPC 解码器
		// 已经能产出它。删除一个可用能力不是"接入 dRPC"的一部分，因此它继续由旧的
		// Solana JSON-RPC Provider 承载：路由未命中 dRPC 时按链级默认回落到 solana-rpc。
		{ID: "internal_transactions", Status: DatasetAvailable, Provider: provider, APIChannel: "solana_rpc"},
		{ID: "logs", Status: DatasetAvailable, Provider: provider, APIChannel: rpcChannel},
		{ID: "balances", Status: DatasetAvailable, Provider: func() string {
			if solanaDRPCBalances.Load() {
				return "drpc"
			}
			return "solana-rpc"
		}(), APIChannel: rpcChannel},

		{ID: "nft_transfers", Status: DatasetUnsupported},
	}
	if dataAPI {
		// Data API 的 transfer.token.nft 是明确的 NFT 对象，不再需要 decimals=0 猜测。
		items[5] = DatasetCapability{ID: "nft_transfers", Status: DatasetAvailable, Provider: provider, APIChannel: dataChannel}
	}
	for i := range items {
		if items[i].Status != DatasetAvailable {
			continue
		}
		switch items[i].ID {
		case "transactions", "token_transfers", "nft_transfers", "internal_transactions", "logs":
			items[i].SupportsFull, items[i].SupportsTimeRange, items[i].SupportsBlockRange = true, true, true
		case "balances":
			// 当前余额快照：没有范围语义。
		}
	}
	return items
}

// chainDefaultSource 返回非 Solana 链的默认 Provider 与通道名。
func chainDefaultSource(chain string) (string, string) {
	switch chain {
	case "tron":
		return "trongrid", "trongrid"
	default:
		return "ankr", "ankr_advanced"
	}
}
