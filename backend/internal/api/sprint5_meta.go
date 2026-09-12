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
			})
		}
	}
	respondOK(w, r, map[string]any{"items": items, "source": "chain_registry"})
}

func (s *Server) handleUserDatasets(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]any, 0, len(model.AllDatasets))
	for _, ds := range model.AllDatasets {
		items = append(items, map[string]any{"id": ds, "label": datasetLabel(ds), "status": "available"})
	}
	respondOK(w, r, map[string]any{"items": items, "source": "model.AllDatasets"})
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
	result, err := s.analysis.Analyze(r.Context(), sub.Chain, sub.Normalized)
	if err != nil {
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
