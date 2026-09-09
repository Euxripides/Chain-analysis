// Package addressmeta：Workspace 地址目录与案件/标签链接（`4-`9）。
package addressmeta

import (
	"context"
	"fmt"
	"strings"

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

func (s *Service) Workspace(ctx context.Context) (orgID, wsID string, err error) {
	return s.Store.DefaultOrgWorkspace(ctx)
}

// EnsureLinked 确保地址在目录中并挂上案件/标签（Metadata 与下载解耦，`26/`77.5）。
func (s *Service) EnsureLinked(ctx context.Context, chain, addr, caseName string, tags []string, createMissing bool) error {
	orgID, wsID, err := s.Workspace(ctx)
	if err != nil {
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
func (s *Service) SyncFromCoverage(ctx context.Context, chain, addr, status string) error {
	orgID, wsID, err := s.Workspace(ctx)
	if err != nil {
		return err
	}
	if s.CH != nil {
		normalized := addr
		if chain != "tron" {
			normalized = strings.ToLower(addr)
		}
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
func (s *Service) dataCounts(ctx context.Context, chain, addr string) ([4]int64, int64, int64, error) {
	queries := []string{
		"SELECT count(), coalesce(toInt64(min(block_number)),0), coalesce(toInt64(max(block_number)),0) FROM blockchain.transactions FINAL WHERE chain=? AND (from_address=? OR to_address=?)",
		"SELECT count(), coalesce(toInt64(min(block_number)),0), coalesce(toInt64(max(block_number)),0) FROM blockchain.token_transfers FINAL WHERE chain=? AND (from_address=? OR to_address=?)",
		"SELECT count(), coalesce(toInt64(min(block_number)),0), coalesce(toInt64(max(block_number)),0) FROM blockchain.nft_transfers FINAL WHERE chain=? AND (from_address=? OR to_address=?)",
		"SELECT count(), coalesce(toInt64(min(block_number)),0), coalesce(toInt64(max(block_number)),0) FROM blockchain.logs FINAL WHERE chain=? AND address=?",
	}
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

func (s *Service) Catalog(ctx context.Context, chain, q string, caseIDs, tagIDs []string, tagMode, syncStatus, cursor string, limit int) ([]CatalogEntry, string, error) {
	_, wsID, err := s.Workspace(ctx)
	if err != nil {
		return nil, "", err
	}
	rows, next, err := s.Store.ListWorkspaceAddresses(ctx, wsID, chain, q, caseIDs, tagIDs, tagMode, syncStatus, cursor, limit)
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

func (s *Service) Meta(ctx context.Context, chain, addr string) (*CatalogEntry, error) {
	_, wsID, err := s.Workspace(ctx)
	if err != nil {
		return nil, err
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
