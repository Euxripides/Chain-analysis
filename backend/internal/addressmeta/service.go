// Package addressmeta：Workspace 地址目录与案件/标签链接（`4-`9）。
package addressmeta

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blockchain-platform/blockchain-platform/internal/addressnorm"
	"github.com/blockchain-platform/blockchain-platform/internal/clickhouse"
	"github.com/blockchain-platform/blockchain-platform/internal/model"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

type Service struct {
	Store *store.Store
	CH    *clickhouse.Conn
}

func New(st *store.Store, ch ...*clickhouse.Conn) *Service {
	s := &Service{Store: st}
	if len(ch) > 0 {
		s.CH = ch[0]
	}
	return s
}

// tenantScopeError 表示调用方没有提供可信的租户作用域。
//
// P0 修复：本 Service 原先通过 Workspace(ctx) 直接取 Store.DefaultOrgWorkspace，
// 让所有调用方（含 HTTP 读接口）都落到"默认组织/默认工作区"。现在作用域必须由
// 调用方显式传入：认证请求路径传主体自己的 org/ws；后台下载路径传任务自己的
// org/ws（download_jobs 有 organization_id / workspace_id 列）。
var tenantScopeError = errors.New("addressmeta: tenant scope is required")

func requireScope(orgID, wsID string) error {
	if strings.TrimSpace(orgID) == "" || strings.TrimSpace(wsID) == "" {
		return tenantScopeError
	}
	return nil
}

// EnsureLinked 确保地址在目录中并挂上案件/标签（Metadata 与下载解耦，`26/`77.5）。
func (s *Service) EnsureLinked(ctx context.Context, orgID, wsID, chain, addr, caseName string, tags []string, createMissing bool) error {
	if err := requireScope(orgID, wsID); err != nil {
		return err
	}
	if err := s.Store.UpsertWorkspaceAddress(ctx, wsID, orgID, chain, addr, "", ""); err != nil {
		return err
	}
	if caseName != "" {
		c, err := s.Store.GetCaseByName(ctx, wsID, caseName)
		if err != nil {
			return err
		}
		if c == nil {
			if !createMissing {
				return fmt.Errorf("case not found: %s", caseName)
			}
			c2, err := s.Store.CreateCase(ctx, wsID, orgID, "", caseName, "", "")
			if err != nil {
				return err
			}
			c = &c2
		}
		if err := s.Store.AddCaseLink(ctx, wsID, chain, addr, c.ID); err != nil {
			return err
		}
		if err := s.Store.SetPrimaryCase(ctx, wsID, chain, addr, c.ID); err != nil {
			return err
		}
	}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		tag, err := s.Store.GetTagByName(ctx, wsID, t)
		if err != nil {
			return err
		}
		if tag == nil {
			if !createMissing {
				return fmt.Errorf("tag not found: %s", t)
			}
			tag2, err := s.Store.CreateTag(ctx, wsID, orgID, t, "", "", "user")
			if err != nil {
				return err
			}
			tag = &tag2
		}
		if err := s.Store.AddTagLink(ctx, wsID, chain, addr, tag.ID); err != nil {
			return err
		}
	}
	return nil
}

// SyncFromCoverage 下载完成后刷新目录统计（`46）。
// 作用域来自下载任务自身的 organization_id / workspace_id，不再取默认组织。
func (s *Service) SyncFromCoverage(ctx context.Context, orgID, wsID, chain, addr, status string) error {
	if err := requireScope(orgID, wsID); err != nil {
		return err
	}
	if s.CH != nil {
		// 查询键归一化：Solana 保留 base58 大小写。这里是后台统计路径，
		// 历史上不做地址校验，沿用宽容归一化，避免畸形历史主体让 finalize 失败。
		normalized := addressnorm.NormalizeKey(chain, addr)
		counts, firstBlock, lastBlock, err := s.dataCounts(ctx, chain, normalized)
		if err != nil {
			return err
		}
		return s.Store.UpdateWorkspaceAddressStats(ctx, wsID, chain, addr, status,
			counts[0], counts[1], counts[2], counts[3], firstBlock, lastBlock)
	}
	return s.Store.SyncWorkspaceAddressStats(ctx, wsID, orgID, chain, addr, status)
}

// dataCounts reads the actual deduplicated data plane. Coverage row_count is
// request metadata and becomes inaccurate when adjacent intervals are merged.
//
// 计数必须是**去重后**的行数（ReplacingMergeTree 的最终语义），但 count() ... FINAL
// 在大表上是整表合并读。实测 2026-09-17（tron 地址 TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj，
// 本机真实数据）：
//
//	transactions      count() FINAL  3.163 s  ->  uniqExact(tx_hash)                        0.571 s
//	token_transfers   count() FINAL 43.531 s  ->  uniqExact(tx_hash, log_index, token_address) 0.802 s
//	logs              count() FINAL  0.878 s  ->  uniqExact(tx_hash, log_index)             0.075 s
//	  （三条的返回值逐值相同：1260 = 1260、28979 = 28979、0 = 0）
//
// 等价性依据：ReplacingMergeTree 的去重键就是 ORDER BY（排序键），chain 已由 WHERE 固定，
// 所以每个键的剩余列唯一标识一行，uniqExact(剩余列) 与 count() FINAL 同值；差别只在于
// uniqExact 不做 FINAL 合并读，from_address/to_address 上的 bloom_filter 依然生效。
// min()/max(block_number) 与去重无关（去掉重复行不改变极值），因此原样保留、只去 FINAL。
//
// 这四条查询来自下载 finalize / benchmark 的后台路径：没有客户端等待它们（worker 被杀后
// 实测有 3 条孤儿查询仍在跑 870 s、单条 7 GiB），却把 ClickHouse 顶到 75% 内存 / 800% CPU，
// 连带把 explorer 读端点的 p95 从 <1 s 抬到 3-10 s。除了改写成非 FINAL 形态，这里再套一层
// 硬上限（MaintenanceScanTimeout / maintenanceScanSettings，30 s + 4 GiB），
// 保证后台统计不会再出现数百秒、数 GiB 的失控查询。调用方是 best-effort（失败只记日志），
// 所以超限只会让一个统计值不更新，不会影响任何用户请求。
func (s *Service) dataCounts(ctx context.Context, chain, addr string) ([4]int64, int64, int64, error) {
	queries := []string{
		"SELECT uniqExact(tx_hash), coalesce(toInt64(min(block_number)),0), coalesce(toInt64(max(block_number)),0) FROM blockchain.transactions WHERE chain=? AND (from_address=? OR to_address=?)",
		"SELECT uniqExact(tx_hash, log_index, token_address), coalesce(toInt64(min(block_number)),0), coalesce(toInt64(max(block_number)),0) FROM blockchain.token_transfers WHERE chain=? AND (from_address=? OR to_address=?)",
		"SELECT uniqExact(tx_hash, log_index, contract_address, token_id), coalesce(toInt64(min(block_number)),0), coalesce(toInt64(max(block_number)),0) FROM blockchain.nft_transfers WHERE chain=? AND (from_address=? OR to_address=?)",
		"SELECT uniqExact(tx_hash, log_index), coalesce(toInt64(min(block_number)),0), coalesce(toInt64(max(block_number)),0) FROM blockchain.logs WHERE chain=? AND address=?",
	}
	ctx, cancel := context.WithTimeout(ctx, clickhouse.MaintenanceScanTimeout)
	defer cancel()
	ctx = clickhouse.MaintenanceScanContext(ctx)
	var counts [4]int64
	var firstBlock, lastBlock int64
	for i, query := range queries {
		args := []any{chain, addr, addr}
		if i == 3 {
			args = []any{chain, addr}
		}
		var count uint64
		var first, last int64
		if err := s.CH.DB().QueryRow(ctx, query, args...).Scan(&count, &first, &last); err != nil {
			return counts, 0, 0, err
		}
		counts[i] = int64(count)
		if first > 0 && (firstBlock == 0 || first < firstBlock) {
			firstBlock = first
		}
		if last > lastBlock {
			lastBlock = last
		}
	}
	return counts, firstBlock, lastBlock, nil
}

// CatalogEntry 目录行（含案件/标签名）。
type CatalogEntry struct {
	model.WorkspaceAddress
	CaseNames []string `json:"case_names"`
	TagNames  []string `json:"tag_names"`
}

// Catalog 列出地址目录。orgID/wsID 由 handler 从已认证主体解析后传入；
// wsID 为空表示该组织下全部工作区（见 store.ListWorkspaceAddressesScoped 的说明）。
func (s *Service) Catalog(ctx context.Context, orgID, wsID, chain, q string, caseIDs, tagIDs []string, tagMode, syncStatus, cursor string, limit int) ([]CatalogEntry, string, error) {
	if strings.TrimSpace(orgID) == "" {
		return nil, "", tenantScopeError
	}
	rows, next, err := s.Store.ListWorkspaceAddressesScoped(ctx, orgID, wsID, chain, q, caseIDs, tagIDs, tagMode, syncStatus, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]CatalogEntry, 0, len(rows))
	for _, w := range rows {
		cases, _ := s.Store.AddressCaseNames(ctx, wsID, w.Chain, w.NormalizedAddress)
		tags, _ := s.Store.AddressTagNames(ctx, wsID, w.Chain, w.NormalizedAddress)
		out = append(out, CatalogEntry{WorkspaceAddress: w, CaseNames: cases, TagNames: tags})
	}
	return out, next, nil
}

func (s *Service) Meta(ctx context.Context, orgID, wsID, chain, addr string) (*CatalogEntry, error) {
	if strings.TrimSpace(orgID) == "" || strings.TrimSpace(wsID) == "" {
		return nil, tenantScopeError
	}
	w, err := s.Store.GetWorkspaceAddress(ctx, wsID, chain, addr)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, nil
	}
	cases, _ := s.Store.AddressCaseNames(ctx, wsID, chain, addr)
	tags, _ := s.Store.AddressTagNames(ctx, wsID, chain, addr)
	return &CatalogEntry{WorkspaceAddress: *w, CaseNames: cases, TagNames: tags}, nil
}
