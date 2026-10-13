package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/query"
	"github.com/blockchain-platform/blockchain-platform/internal/tron"
)

func canonicalExplorerAddress(chain, value string) string {
	value = strings.TrimSpace(value)
	if chain == "solana" {
		return value
	}
	if chain == "tron" {
		if n, err := tron.NormalizeTronAddress(value); err == nil {
			return n
		}
		return value
	}
	return strings.ToLower(value)
}

func explorerStatisticsFilter(r *http.Request, chain string, addresses []string) (query.StatisticsFilter, error) {
	q := r.URL.Query()
	f := query.StatisticsFilter{Chain: chain, Addresses: addresses,
		Token: q.Get("token"), Direction: q.Get("direction"), Counterparty: q.Get("counterparty"), FromAddress: q.Get("from_address"), ToAddress: q.Get("to_address"), HideZero: q.Get("hide_zero") == "1", MinAmount: q.Get("min_amount"), MaxAmount: q.Get("max_amount"), Interval: q.Get("interval"), Timezone: q.Get("timezone")}
	if q.Get("datasets") != "" {
		f.Datasets = strings.Split(q.Get("datasets"), ",")
	}
	for key, dst := range map[string]**time.Time{"from_time": &f.FromTime, "to_time": &f.ToTime, "as_of": &f.AsOf} {
		if value := q.Get(key); value != "" {
			v, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return f, err
			}
			*dst = &v
		}
	}
	for _, value := range []*string{&f.Counterparty, &f.FromAddress, &f.ToAddress} {
		if *value == "" {
			continue
		}
		items, err := dataset.NormalizeAndDedupeAddressesForChain(chain, []string{*value})
		if err != nil || len(items) != 1 {
			return f, errors.New("invalid address filter")
		}
		*value = items[0]
	}
	return f, f.Normalize()
}

func (s *Server) handleExplorerStatistics(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.resolveExplorerSubject(w, r)
	if !ok {
		return
	}
	addresses := []string{sub.Normalized}
	if raw := r.URL.Query().Get("addresses"); raw != "" {
		addresses = []string{}
		var err error
		addresses, err = dataset.NormalizeAndDedupeAddressesForChain(sub.Chain, strings.Split(raw, ","))
		if err != nil {
			validationError(w, r, "地址组不合法", nil)
			return
		}
	}
	f, err := explorerStatisticsFilter(r, sub.Chain, addresses)
	if err != nil {
		validationError(w, r, "统计筛选不合法", []fieldError{{Field: "filter", Code: "invalid", Message: err.Error()}})
		return
	}
	ctx, cancel := explorerReadContext(r.Context())
	defer cancel()
	result, err := query.New(s.deps.CH).Statistics(ctx, f)
	if err != nil {
		if errors.Is(err, query.ErrStatisticsBudget) {
			respondError(w, r, http.StatusUnprocessableEntity, "QUERY_SCAN_BUDGET", "统计范围超出交互查询预算，请缩短时间范围或指定代币", map[string]any{"event_budget": query.StatisticsEventBudget})
			return
		}
		if explorerScanRefusal(w, r, ctx, err, "统计分析") {
			return
		}
		s.deps.Log.Error("explorer statistics failed", "err", err)
		respondError(w, r, http.StatusServiceUnavailable, "DATA_UNAVAILABLE", "统计数据暂不可用", nil)
		return
	}
	coverage := []map[string]any{}
	for _, address := range f.Addresses {
		coverage = append(coverage, map[string]any{"address": address, "coverage": s.localExplorerCoverage(ctx, explorerSubject{Chain: sub.Chain, Address: address, Normalized: address})})
	}
	respondOK(w, r, map[string]any{"statistics": result, "coverage": coverage, "source": map[string]any{"provider": "local_index", "as_of": f.AsOf, "data_version": result.Version}, "calculation": map[string]any{
		"identities":  "transactions: tx_hash; token_transfers: tx_hash + log_index + contract; internal_transactions: tx_hash + call_index",
		"group_flows": "in/out count external events; internal and self are separate; per-address totals include transfers between group members",
		"amounts":     "exact raw integer strings; failed events excluded from amounts; missing amounts counted separately",
		"share":       "counterparty event_count / event_count for the same dataset, contract and decimals",
	}})
}
