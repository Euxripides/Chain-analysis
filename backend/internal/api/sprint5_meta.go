package api

import (
	"net/http"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

// v2 元数据：链与 Dataset 选项必须来自服务端 registry，前端不写死（§18.10）。
func (s *Server) handleUserChains(w http.ResponseWriter, r *http.Request) {
	items := []map[string]any{}
	if s.deps.ChainRead != nil && s.deps.ChainRead.Chains() != nil {
		for _, cfg := range s.deps.ChainRead.Chains().List() {
			items = append(items, map[string]any{
				"slug": cfg.Slug, "name": cfg.Name, "type": cfg.Type, "enabled": cfg.Enabled,
				"supports_trace": cfg.SupportsTraceConcept, "default_provider": cfg.DefaultProvider,
				// download_only：该链只有下载取数，没有 Explorer 读路径（Solana），
				// 前端据此把它排除在 Explorer 之外，而不是让用户点进去拿到错误。
				"download_only": downloadOnlyChainType(cfg.Type),
			})
		}
	}
	respondOK(w, r, map[string]any{"items": items, "source": "chain_registry"})
}

// downloadOnlyChainType 判定链类型是否只有下载取数、没有 Explorer 读路径。
//
// Solana 走标准 JSON-RPC（getSignaturesForAddress/getTransaction），没有 EVM 的
// eth_getCode/eth_getBalance 读接口，也没有本地索引表，因此 Explorer 侧必须排除。
func downloadOnlyChainType(chainType string) bool {
	return strings.EqualFold(chainType, "solana")
}

func (s *Server) handleUserDatasets(w http.ResponseWriter, r *http.Request) {
	chain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chain")))
	datasets := chainDatasets(chain)
	items := make([]map[string]any, 0, len(datasets))
	for _, ds := range datasets {
		items = append(items, map[string]any{"id": ds, "label": datasetLabel(ds), "status": "available"})
	}
	source := "chain_datasets"
	if chain == "" {
		source = "model.AllDatasets"
	}
	respondOK(w, r, map[string]any{"items": items, "chain": chain, "source": source})
}

// chainDatasetsByChain 是「该链实际能下载哪些 Dataset」的唯一服务端口径。
//
// 在它出现之前 /api/v2/meta/datasets 对所有链返回全部 6 类，前端照单全收：
// internal_transactions 能被 EVM 选中、nft/logs/balances 能被 TRON 选中，
// 直到任务跑到 Provider 才失败。链级能力必须由服务端表达，前端不写死（§18.10）。
//
// 事实来源：internal/provider/ankr（EVM Advanced API）、internal/tron、
// internal/provider/solana（标准 JSON-RPC，当前只实现 transactions）。
var chainDatasetsByChain = map[string][]string{
	"bsc":       {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"eth":       {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"polygon":   {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"arbitrum":  {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"base":      {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"optimism":  {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	"avalanche": {"transactions", "token_transfers", "nft_transfers", "logs", "balances"},
	// TRON：internal_transactions 是一级数据集；nft/logs/balances 没有取数实现。
	"tron": {"transactions", "token_transfers", "internal_transactions"},
	// Solana：标准 JSON-RPC 当前只实现 transactions。
	"solana": {"transactions"},
}

// chainDatasets 返回该链可下载的 Dataset；chain 为空或未登记时返回全量，
// 保持既有调用方（不带 chain 的旧前端）的向后兼容。
func chainDatasets(chain string) []string {
	if chain == "" {
		return model.AllDatasets
	}
	if list, ok := chainDatasetsByChain[chain]; ok {
		return list
	}
	return model.AllDatasets
}

func datasetLabel(id string) string {
	switch id {
	case "transactions":
		return "普通交易"
	case "token_transfers":
		return "Token Transfer"
	case "internal_transactions":
		return "内部调用"
	case "nft_transfers":
		return "NFT Transfer"
	case "logs":
		return "事件日志"
	case "balances":
		return "地址余额快照"
	default:
		return strings.ReplaceAll(id, "_", " ")
	}
}

// Flow Profile：本地索引资金流，不调用 Provider；Canvas/Fund Flow 页面使用。
func (s *Server) handleExplorerFlowProfile(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveExplorerSubject(w, r)
	if !ok {
		return
	}
	if s.analysis == nil {
		respondError(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "分析服务未启用", nil)
		return
	}
	// P1-1：资金流分析是 explorer 里最重的读（实测 9.88s），必须跑在交互预算内。
	readCtx, cancelRead := explorerReadContext(r.Context())
	defer cancelRead()
	result, err := s.analysis.Analyze(readCtx, sub.Chain, sub.Normalized)
	if err != nil {
		if explorerScanRefusal(w, r, readCtx, err, "资金流分析") {
			return
		}
		respondError(w, r, http.StatusBadRequest, "DATA_ANALYSIS_FAILED", "资金流分析失败", nil)
		return
	}
	subject := map[string]any{"type": "address", "chain": sub.Chain, "address": sub.Address, "normalized_address": sub.Normalized}
	respondOK(w, r, explorerEnvelope(subject, result, map[string]any{
		"transaction_count": result.TransactionCount, "counterparty_count": result.UniqueCounterparties,
	}, nil, map[string]any{
		"evidence_boundary": result.EvidenceBoundary,
		"source":            "本地 token_transfers 索引，不调用 Provider",
	}, "clickhouse:blockchain.token_transfers"))
}
