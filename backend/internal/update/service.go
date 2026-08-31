// Package update：混合模式增量更新（`30-`46：精确小增量 / 估算大增量 / Target 冻结）。
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/addressmeta"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/dataset"
	"github.com/blockchain-platform/blockchain-platform/internal/ingest"
	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

const (
	previewTTL     = 15 * time.Minute
	previewCacheTTL = 60 * time.Second
	safeHeadBuffer = 15
)

type Service struct {
	Store *store.Store
	CH    *clickhouse.Conn
	Prov  provider.Provider
	DS    *dataset.Service
	Meta  *addressmeta.Service
	Log   *slog.Logger

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	result   []byte
	expires  time.Time
}

func New(st *store.Store, ch *clickhouse.Conn, prov provider.Provider, ds *dataset.Service, meta *addressmeta.Service, log *slog.Logger) *Service {
	return &Service{Store: st, CH: ch, Prov: prov, DS: ds, Meta: meta, Log: log, cache: map[string]cacheEntry{}}
}

type PreviewRequest struct {
	Chain     string   `json:"chain"`
	Addresses []string `json:"addresses"`
	Datasets  []string `json:"datasets"`
	PageSize  int      `json:"preview_page_size"`
}

type PreviewItem struct {
	Address       string `json:"address"`
	Dataset       string `json:"dataset"`
	FromBlock     int64  `json:"from_block"`
	ToBlock       int64  `json:"to_block"`
	HasNewData    bool   `json:"has_new_data"`
	CountMode     string `json:"count_mode"` // exact | estimate | snapshot
	NewRows       int64  `json:"new_rows"`
	Snapshot      bool   `json:"snapshot"`
	MinimumRows   int64  `json:"minimum_rows"`
	EstimatedRows int64  `json:"estimated_rows"`
	EstimatedLow  int64  `json:"estimated_low"`
	EstimatedHigh int64  `json:"estimated_high"`
	EstimatedBytes int64 `json:"estimated_bytes"`
	Confidence    string `json:"confidence"`
}

type PreviewResponse struct {
	PreviewID   string        `json:"preview_id"`
	TargetBlock int64         `json:"target_block"`
	ExpiresAt   time.Time     `json:"expires_at"`
	Summary     PreviewSummary `json:"summary"`
	Items       []PreviewItem `json:"items"`
}

type PreviewSummary struct {
	Addresses      int   `json:"addresses"`
	UpToDate       int   `json:"up_to_date"`
	ExactRows      int64 `json:"exact_rows"`
	EstimatedRows  int64 `json:"estimated_rows"`
	EstimatedBytes int64 `json:"estimated_bytes"`
}

// SafeHead 链级安全头（`32）：本地最大块 - reorg buffer。
func (s *Service) SafeHead(ctx context.Context, chain string) (int64, error) {
	var head uint64
	if err := s.CH.DB().QueryRow(ctx,
		"SELECT COALESCE(max(block_number),0) FROM blockchain.token_transfers FINAL WHERE chain=?", chain).Scan(&head); err != nil {
		return 0, err
	}
	if head == 0 {
		if err := s.CH.DB().QueryRow(ctx,
			"SELECT COALESCE(max(block_number),0) FROM blockchain.transactions FINAL WHERE chain=?", chain).Scan(&head); err != nil {
			return 0, err
		}
	}
	if head > safeHeadBuffer {
		head -= safeHeadBuffer
	}
	return int64(head), nil
}

// Preview 混合预检（`34-`42）。
func (s *Service) Preview(ctx context.Context, req PreviewRequest) (*PreviewResponse, error) {
	addresses, err := dataset.NormalizeAndDedupeAddresses(req.Addresses)
	if err != nil {
		return nil, err
	}
	chain := strings.ToLower(strings.TrimSpace(req.Chain))
	if chain == "" {
		chain = "bsc"
	}
	if req.PageSize <= 0 {
		req.PageSize = 500
	}
	target, err := s.SafeHead(ctx, chain)
	if err != nil {
		return nil, err
	}
	orgID, wsID, err := s.Store.DefaultOrgWorkspace(ctx)
	if err != nil {
		return nil, err
	}

	items := []PreviewItem{}
	for _, addr := range addresses {
		items = append(items, s.previewAddress(ctx, chain, addr, req.Datasets, req.PageSize, target)...)
	}

	var summary PreviewSummary
	summary.Addresses = len(addresses)
	for _, it := range items {
		if !it.HasNewData && !it.Snapshot {
			summary.UpToDate++
		}
		if it.CountMode == "exact" {
			summary.ExactRows += it.NewRows
		}
		if it.CountMode == "estimate" {
			summary.EstimatedRows += it.EstimatedRows
			summary.EstimatedBytes += it.EstimatedBytes
		}
	}

	resultJSON, _ := json.Marshal(map[string]any{"items": items, "summary": summary})
	requestJSON, _ := json.Marshal(req)

	// 60s 缓存防连续点击（`68）
	cacheKey := chain + "|" + strings.Join(addresses, ",") + "|" + strings.Join(req.Datasets, ",") + "|" + fmt.Sprint(target)
	s.mu.Lock()
	if ce, ok := s.cache[cacheKey]; ok && time.Now().Before(ce.expires) {
		var cached PreviewResponse
		if json.Unmarshal(ce.result, &cached) == nil {
			s.mu.Unlock()
			return &cached, nil
		}
	}
	s.mu.Unlock()

	previewID, err := s.Store.CreateUpdatePreview(ctx, wsID, orgID, chain, target, requestJSON, resultJSON, previewTTL)
	if err != nil {
		return nil, err
	}
	resp := &PreviewResponse{
		PreviewID: previewID, TargetBlock: target,
		ExpiresAt: time.Now().Add(previewTTL),
		Summary: summary, Items: items,
	}
	b, _ := json.Marshal(resp)
	s.mu.Lock()
	s.cache[cacheKey] = cacheEntry{result: b, expires: time.Now().Add(previewCacheTTL)}
	s.mu.Unlock()
	return resp, nil
}

func (s *Service) previewAddress(ctx context.Context, chain, addr string, wantDatasets []string, pageSize int, target int64) []PreviewItem {
	covered, err := s.Store.ListCoverage(ctx, chain, addr, "")
	_ = covered
	if err != nil {
		s.Log.Error("preview coverage", "err", err)
	}
	// 该地址已存在的 Dataset（`31 默认）
	existing := map[string]bool{}
	var items []PreviewItem
	for _, c := range covered {
		if c.Dataset == "balances" {
			items = append(items, PreviewItem{Address: addr, Dataset: "balances", CountMode: "snapshot", Snapshot: true, HasNewData: true})
			continue
		}
		existing[c.Dataset] = true
	}
	datasets := wantDatasets
	if len(datasets) == 0 {
		for d := range existing {
			datasets = append(datasets, d)
		}
	}
	seen := map[string]bool{}
	for _, d := range datasets {
		if seen[d] || d == "balances" {
			continue
		}
		seen[d] = true
		end := int64(0)
		for _, c := range covered {
			if c.Dataset == d && c.ToBlock > end {
				end = c.ToBlock
			}
		}
		from := end + 1
		if target == 0 || from > target {
			items = append(items, PreviewItem{Address: addr, Dataset: d, FromBlock: from, ToBlock: target,
				HasNewData: false, CountMode: "exact", NewRows: 0})
			continue
		}
		items = append(items, s.probe(ctx, chain, addr, d, from, target, pageSize))
	}
	return items
}

// probe 单页探测（`36-`41）：小增量精确、大增量估算。
func (s *Service) probe(ctx context.Context, chain, addr, dataset string, from, to int64, pageSize int) PreviewItem {
	item := PreviewItem{Address: addr, Dataset: dataset, FromBlock: from, ToBlock: to, HasNewData: true}
	f, t := uint64(from), uint64(to)
	actx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := s.Prov.Fetch(actx, provider.FetchRequest{
		Chain: chain, Dataset: dataset, Address: addr,
		FromBlock: &f, ToBlock: &t, PageSize: pageSize,
	})
	span := to - from + 1
	if err != nil {
		// Probe 失败：按历史密度估算，置信度低（`39）
		est, rate, low, high := s.estimateFromHistory(ctx, chain, addr, dataset, span, 0)
		item.CountMode = "estimate"
		item.MinimumRows = 0
		item.EstimatedRows = est
		item.EstimatedLow = low
		item.EstimatedHigh = high
		item.Confidence = "low"
		item.EstimatedBytes = est * s.historyBytesPerRow(ctx, chain, addr, dataset)
		_ = rate
		return item
	}
	inRange := 0
	for _, raw := range res.Rows {
		if bn, ok := ingest.RawBlockNumber(raw); ok && bn >= uint64(from) && bn <= uint64(to) {
			inRange++
		}
	}
	if !res.HasMore {
		item.CountMode = "exact"
		item.NewRows = int64(inRange)
		if inRange == 0 {
			item.HasNewData = false
		}
		return item
	}
	// 大增量估算（`39/`40：不再为精确 Count 完整分页）
	est, _, low, high := s.estimateFromHistory(ctx, chain, addr, dataset, span, int64(inRange))
	item.CountMode = "estimate"
	item.MinimumRows = int64(inRange)
	if item.MinimumRows < int64(pageSize) {
		item.MinimumRows = int64(pageSize)
	}
	item.EstimatedRows = est
	item.EstimatedLow = low
	item.EstimatedHigh = high
	item.EstimatedBytes = est * s.historyBytesPerRow(ctx, chain, addr, dataset)
	item.Confidence = "medium"
	return item
}

func (s *Service) estimateFromHistory(ctx context.Context, chain, addr, dataset string, span, probeRows int64) (est, rate, low, high int64) {
	var histRows, histSpan int64
	_ = s.CH.DB().QueryRow(ctx, "SELECT 0, 0") // noop 保持签名
	rows, err := s.Store.ListCoverage(ctx, chain, addr, dataset)
	if err == nil {
		for _, c := range rows {
			histRows += c.RowCount
			if c.ToBlock >= c.FromBlock {
				histSpan += c.ToBlock - c.FromBlock + 1
			}
		}
	}
	var rpb float64
	if histSpan > 0 {
		rpb = float64(histRows) / float64(histSpan)
	}
	if probeRows > 0 && span > 0 {
		probeRate := float64(probeRows) / float64(span)
		if rpb <= 0 {
			rpb = probeRate
		} else {
			rpb = 0.5*rpb + 0.5*probeRate
		}
	}
	if rpb <= 0 {
		rpb = 0.001 // 未知：低置信
	}
	est = int64(rpb * float64(span))
	if est < 1 {
		est = 1
	}
	low = int64(float64(est) * 0.6)
	high = int64(float64(est) * 1.8)
	if low < probeRows {
		low = probeRows
	}
	if high < est {
		high = est * 2
	}
	rate = int64(rpb * 1000000)
	return
}

func (s *Service) historyBytesPerRow(ctx context.Context, chain, addr, dataset string) int64 {
	rows, err := s.Store.ListCoverage(ctx, chain, addr, dataset)
	if err != nil {
		return 256
	}
	var bytes, count int64
	for _, c := range rows {
		bytes += c.RawBytes
		count += c.RowCount
	}
	if count > 0 {
		if bpr := bytes / count; bpr > 0 {
			return bpr
		}
	}
	return 256
}

// Confirm 确认更新（`44：校验过期 → 冻结 target → 复检 Coverage → 复用 EnsureDataset）。
func (s *Service) Confirm(ctx context.Context, previewID string) ([]string, error) {
	target, requestJSON, _, expiresAt, err := s.Store.GetUpdatePreview(ctx, previewID)
	if err != nil {
		return nil, fmt.Errorf("preview not found: %w", err)
	}
	if time.Now().After(expiresAt) {
		return nil, errors.New("preview expired, please re-run preview")
	}
	var req PreviewRequest
	if err := json.Unmarshal(requestJSON, &req); err != nil {
		return nil, err
	}
	chain := strings.ToLower(strings.TrimSpace(req.Chain))
	if chain == "" {
		chain = "bsc"
	}
	_, wsID, err := s.Store.DefaultOrgWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	var jobIDs []string
	for _, addr := range req.Addresses {
		// 复检 Coverage（`44.4/`77.11）
		covered, err := s.Store.ListCoverage(ctx, chain, addr, "")
		if err != nil {
			return nil, err
		}
		ends := map[string]int64{}
		hasBalances := false
		for _, c := range covered {
			if c.Dataset == "balances" {
				hasBalances = true
				continue
			}
			if c.ToBlock > ends[c.Dataset] {
				ends[c.Dataset] = c.ToBlock
			}
		}
		datasets := req.Datasets
		if len(datasets) == 0 {
			for d := range ends {
				datasets = append(datasets, d)
			}
		}
		var gaps []string
		minFrom := int64(0)
		for _, d := range datasets {
			if d == "balances" {
				continue
			}
			from := ends[d] + 1
			if target == 0 || from > target {
				continue
			}
			gaps = append(gaps, d)
			if minFrom == 0 || from < minFrom {
				minFrom = from
			}
		}
		if len(gaps) == 0 && !hasBalances {
			continue // 其它任务已补全 → 复用，不重复下载（`44.5）
		}
		if len(gaps) > 0 {
			from := minFrom
			resp, err := s.DS.Ensure(ctx, dataset.EnsureRequest{
				Source: "update", Priority: 90,
				Subjects: []string{addr}, Chain: chain, Datasets: gaps,
				Range: dataset.RangeSpec{Type: "block", FromBlock: &from, ToBlock: &target},
			})
			if err != nil {
				return nil, err
			}
			if resp.JobID != nil {
				jobIDs = append(jobIDs, *resp.JobID)
			}
		}
		if hasBalances {
			resp, err := s.DS.Ensure(ctx, dataset.EnsureRequest{
				Source: "update", Priority: 90,
				Subjects: []string{addr}, Chain: chain, Datasets: []string{"balances"},
				Range: dataset.RangeSpec{Type: "full"},
			})
			if err != nil {
				return nil, err
			}
			if resp.JobID != nil {
				jobIDs = append(jobIDs, *resp.JobID)
			}
		}
		_ = s.Store.SetWorkspaceAddressSyncStatus(ctx, wsID, chain, addr, "updating")
	}
	return jobIDs, nil
}
