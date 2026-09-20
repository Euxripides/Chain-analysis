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
				// explorer_mode 让前端知道该链能读到什么（见 explorerModeForChainType）。
				"explorer_mode": explorerModeForChainType(cfg.Type),
			})
		}
	}
	respondOK(w, r, map[string]any{"items": items, "source": "chain_registry"})
}

// explorerModeForChainType 表达该链在 Explorer 里的读能力：
//
//	full        —— 链上有 RPC 读路径（EVM/TRON），地址类型、余额快照等都能取。
//	local_index —— 只有下载入库的本地索引（Solana）：活动/对手方/资金流可读，
//	               地址类型与实时余额快照不适用。前端据此给出说明，而不是隐藏整条链。
func explorerModeForChainType(chainType string) string {
	if strings.EqualFold(chainType, "solana") {
		return "local_index"
	}
	return "full"
}

func (s *Server) handleUserDatasets(w http.ResponseWriter, r *http.Request) {
	chain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chain")))
	datasets := model.ChainDatasets(chain)
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
