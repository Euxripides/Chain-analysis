package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blockchain-platform/blockchain-platform/internal/auth"
	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	"github.com/blockchain-platform/blockchain-platform/internal/chainread"
	"github.com/blockchain-platform/blockchain-platform/internal/chaintrace"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

func (s *Server) handleChainCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.deps.RPCGateway == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "rpc infrastructure is disabled"})
		return
	}
	chainSlug := strings.ToLower(chi.URLParam(r, "chain"))
	if _, err := s.deps.RPCGateway.Chains().Require(chainSlug); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	caps := s.deps.RPCGateway.Capabilities().ChainCapability(chainSlug)
	writeJSON(w, http.StatusOK, map[string]any{
		"chain": chainSlug,
		"rpc": map[string]any{
			"standard": caps.StandardRPC, "archive": caps.Archive, "batch": caps.Batch,
			"trace": caps.Trace, "debug": caps.Debug,
			"max_batch_size": caps.MaxBatchSize, "max_log_block_range": caps.MaxLogBlockRange,
		},
	})
}

func (s *Server) handleChainHead(w http.ResponseWriter, r *http.Request) {
	if s.deps.Head == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "chain head service is disabled"})
		return
	}
	result, err := s.deps.Head.Head(r.Context(), chi.URLParam(r, "chain"))
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAddressRuntime(w http.ResponseWriter, r *http.Request) {
	if s.deps.Head == nil || s.deps.AddressType == nil || s.deps.BalanceRead == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "address runtime service is disabled"})
		return
	}
	chainSlug := strings.ToLower(chi.URLParam(r, "chain"))
	address, err := chain.ParseAddress(chi.URLParam(r, "address"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	user := auth.FromContext(r.Context())
	opt := chainread.Options{Purpose: rpc.PurposeInvestigation, Priority: rpc.PriorityInteractive,
		Budget: rpc.BudgetContext{Purpose: rpc.PurposeInvestigation, OrganizationID: user.OrganizationID}}
	head, err := s.deps.Head.Head(r.Context(), chainSlug)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	addressInfo, err := s.deps.AddressType.Classify(r.Context(), chainSlug, address, chain.Latest(), opt)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	balance, err := s.deps.BalanceRead.Native(r.Context(), chainSlug, address, chain.Latest(), opt)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"chain": chainSlug, "address": address, "address_type": addressInfo.Type,
		"code_size": addressInfo.CodeSize, "native_balance": balance.String(),
		"head_block": head.HeadBlock, "safe_block": head.SafeBlock, "fetched_at": addressInfo.FetchedAt,
	})
}

func (s *Server) handleRPCReceipt(w http.ResponseWriter, r *http.Request) {
	chainSlug := strings.ToLower(chi.URLParam(r, "chain"))
	if chainSlug == "tron" {
		fetcher, ok := s.deps.Prov.(provider.TxReceiptFetcher)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "TRON receipt provider is disabled"})
			return
		}
		raw, err := fetcher.TxReceipt(r.Context(), chainSlug, chi.URLParam(r, "tx_hash"))
		if err != nil {
			writeRPCError(w, err)
			return
		}
		var receipt any
		if err := json.Unmarshal(raw, &receipt); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "invalid TRON receipt response"})
			return
		}
		writeJSON(w, http.StatusOK, receipt)
		return
	}
	if s.deps.ChainRead == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "chain reader is disabled"})
		return
	}
	hash, err := chain.ParseHash(chi.URLParam(r, "tx_hash"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	receipt, err := s.deps.ChainRead.Receipt(r.Context(), chi.URLParam(r, "chain"), hash,
		chainread.Options{Purpose: rpc.PurposeReceipt, Priority: rpc.PriorityInteractive})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	if receipt == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "transaction receipt not found"})
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *Server) handleTransactionTrace(w http.ResponseWriter, r *http.Request) {
	if !canForceDecode(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "trace requires analyst or investigator role"})
		return
	}
	if s.deps.Trace == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "trace service is disabled"})
		return
	}
	hash, err := chain.ParseHash(chi.URLParam(r, "tx_hash"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	investigationID := strings.TrimSpace(r.URL.Query().Get("investigation_id"))
	if investigationID != "" {
		if _, err := uuid.Parse(investigationID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "investigation_id must be a UUID"})
			return
		}
	}
	result, err := s.deps.Trace.TransactionTrace(r.Context(), chi.URLParam(r, "chain"), hash,
		chaintrace.Options{Consistency: r.URL.Query().Get("mode"), InvestigationID: investigationID})
	if errors.Is(err, chaintrace.ErrTraceNotAvailableLocally) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleInternalCalls(w http.ResponseWriter, r *http.Request) {
	if s.deps.Trace == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "trace service is disabled"})
		return
	}
	hash, err := chain.ParseHash(chi.URLParam(r, "tx_hash"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	items, err := s.deps.Trace.InternalCalls(r.Context(), chi.URLParam(r, "chain"), hash)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "cannot read internal calls"})
		return
	}
	if items == nil {
		items = []chaintrace.InternalActivity{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func writeRPCError(w http.ResponseWriter, err error) {
	mapped := rpc.AsError(err)
	status := http.StatusBadGateway
	switch mapped.Class {
	case rpc.ClassInvalidParams, rpc.ClassMethodDenied, rpc.ClassRequestTooLarge:
		status = http.StatusBadRequest
	case rpc.ClassBudgetExceeded, rpc.ClassRateLimit:
		status = http.StatusTooManyRequests
	case rpc.ClassTraceUnsupported, rpc.ClassMethodUnsupported, rpc.ClassNoProvider, rpc.ClassCircuitOpen:
		status = http.StatusServiceUnavailable
	case rpc.ClassLocalMiss:
		status = http.StatusNotFound
	}
	msg := mapped.Public()
	// invalid_params 的 Message 是系统生成的参数说明（无 Provider/Secret 泄漏），
	// 直接返回便于调用方修正参数，而不是笼统的 “Provider temporarily unavailable”。
	if mapped.Class == rpc.ClassInvalidParams && mapped.Message != "" {
		msg = mapped.Message
	}
	writeJSON(w, status, map[string]any{"error": msg, "error_class": mapped.Class})
}
