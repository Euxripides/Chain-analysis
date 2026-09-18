package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
)

func normalizeWorkspaceAddress(chain, address string) string {
	address = strings.TrimSpace(address)
	if strings.EqualFold(strings.TrimSpace(chain), "tron") {
		return address
	}
	return strings.ToLower(address)
}

// ---- Workspace addresses ----

func (s *Store) UpsertWorkspaceAddress(ctx context.Context, wsID, orgID, chain, addr, displayName, primaryCaseID string) error {
	var dn, pc any
	if displayName != "" {
		dn = displayName
	}
	if primaryCaseID != "" {
		pc = primaryCaseID
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO workspace_addresses "+
		"(organization_id, workspace_id, chain, address, normalized_address, display_name, primary_case_id) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7) "+
		"ON CONFLICT (workspace_id, chain, normalized_address) "+
		"DO UPDATE SET address=EXCLUDED.address, display_name=COALESCE(EXCLUDED.display_name, workspace_addresses.display_name), "+
		"primary_case_id=COALESCE(EXCLUDED.primary_case_id, workspace_addresses.primary_case_id), updated_at=now()",
		orgID, wsID, chain, strings.TrimSpace(addr), normalizeWorkspaceAddress(chain, addr), dn, pc)
	return err
}

// ListWorkspaceAddresses 地址目录（单工作区，keyset cursor 按地址；案件/标签/状态筛选，§61）。
func (s *Store) ListWorkspaceAddresses(ctx context.Context, wsID, chain, q string, caseIDs, tagIDs []string, tagMode, syncStatus, cursor string, limit int) ([]model.WorkspaceAddress, string, error) {
	var orgID string
	if err := s.Pool.QueryRow(ctx, "SELECT organization_id::text FROM workspaces WHERE id=$1", wsID).Scan(&orgID); err != nil {
		return nil, "", err
	}
	return s.listWorkspaceAddresses(ctx, orgID, wsID, chain, q, caseIDs, tagIDs, tagMode, syncStatus, cursor, limit)
}

// ListWorkspaceAddressesScoped 按租户作用域列出地址目录。
//
// P0 修复：HTTP 读路径必须使用本方法。wsID 为空表示该组织下全部工作区
// （主体只有组织级绑定时的合法视图），但**永远不会**跨组织。
func (s *Store) ListWorkspaceAddressesScoped(ctx context.Context, orgID, wsID, chain, q string, caseIDs, tagIDs []string, tagMode, syncStatus, cursor string, limit int) ([]model.WorkspaceAddress, string, error) {
	return s.listWorkspaceAddresses(ctx, orgID, wsID, chain, q, caseIDs, tagIDs, tagMode, syncStatus, cursor, limit)
}

func (s *Store) listWorkspaceAddresses(ctx context.Context, orgID, wsID, chain, q string, caseIDs, tagIDs []string, tagMode, syncStatus, cursor string, limit int) ([]model.WorkspaceAddress, string, error) {
	args := []any{orgID, wsID}
	sql := "SELECT wa.id, wa.chain, wa.address, wa.normalized_address, COALESCE(wa.display_name,''), wa.primary_case_id::text, " +
		"wa.transaction_count, wa.token_transfer_count, wa.nft_transfer_count, wa.log_count, wa.total_activity_count, " +
		"COALESCE(wa.first_data_block,0), COALESCE(wa.last_data_block,0), wa.first_data_at, wa.last_data_at, wa.last_sync_at, wa.sync_status, wa.updated_at " +
		"FROM workspace_addresses wa WHERE wa.organization_id=$1 AND ($2='' OR wa.workspace_id::text=$2)"
	ph := func() string { return "$" + itoa(len(args)) }
	if chain != "" {
		args = append(args, chain)
		sql += " AND wa.chain=" + ph()
	}
	if q != "" {
		args = append(args, "%"+strings.ToLower(q)+"%")
		sql += " AND (wa.normalized_address LIKE " + ph() + " OR lower(COALESCE(wa.display_name,'')) LIKE " + ph() + ")"
	}
	if syncStatus != "" {
		args = append(args, syncStatus)
		sql += " AND wa.sync_status=" + ph()
	}
	if len(caseIDs) > 0 {
		args = append(args, caseIDs)
		sql += " AND EXISTS (SELECT 1 FROM address_case_links l WHERE l.workspace_id=wa.workspace_id AND l.chain=wa.chain " +
			"AND l.normalized_address=wa.normalized_address AND l.case_id = ANY(" + ph() + "))"
	}
	if len(tagIDs) > 0 {
		args = append(args, tagIDs)
		if tagMode == "all" {
			args = append(args, len(tagIDs))
			sql += " AND (SELECT COUNT(*) FROM (SELECT 1 FROM address_tag_links t WHERE t.workspace_id=wa.workspace_id " +
				"AND t.chain=wa.chain AND t.normalized_address=wa.normalized_address AND t.tag_id = ANY(" + ph() + ") GROUP BY t.tag_id) x) = " + ph()
		} else {
			sql += " AND EXISTS (SELECT 1 FROM address_tag_links t WHERE t.workspace_id=wa.workspace_id " +
				"AND t.chain=wa.chain AND t.normalized_address=wa.normalized_address AND t.tag_id = ANY(" + ph() + "))"
		}
	}
	if cursor != "" {
		args = append(args, cursor)
		sql += " AND wa.normalized_address > " + ph()
	}
	args = append(args, limit+1)
	sql += " ORDER BY wa.normalized_address LIMIT " + ph()

	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []model.WorkspaceAddress
	next := ""
	for rows.Next() {
		var w model.WorkspaceAddress
		var fda, lda, lsa pgtype.Timestamptz
		if err := rows.Scan(&w.ID, &w.Chain, &w.Address, &w.NormalizedAddress, &w.DisplayName, &w.PrimaryCaseID,
			&w.TransactionCount, &w.TokenTransferCount, &w.NftTransferCount, &w.LogCount, &w.TotalActivityCount,
			&w.FirstDataBlock, &w.LastDataBlock, &fda, &lda, &lsa, &w.SyncStatus, &w.UpdatedAt); err != nil {
			return nil, "", err
		}
		if fda.Valid {
			t := fda.Time
			w.FirstDataAt = &t
		}
		if lda.Valid {
			t := lda.Time
			w.LastDataAt = &t
		}
		if lsa.Valid {
			t := lsa.Time
			w.LastSyncAt = &t
		}
		out = append(out, w)
	}
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].NormalizedAddress
	}
	return out, next, rows.Err()
}

func (s *Store) GetWorkspaceAddress(ctx context.Context, wsID, chain, addr string) (*model.WorkspaceAddress, error) {
	rows, err := s.Pool.Query(ctx, "SELECT wa.id, wa.chain, wa.address, wa.normalized_address, COALESCE(wa.display_name,''), wa.primary_case_id::text, "+
		"wa.transaction_count, wa.token_transfer_count, wa.nft_transfer_count, wa.log_count, wa.total_activity_count, "+
		"COALESCE(wa.first_data_block,0), COALESCE(wa.last_data_block,0), wa.first_data_at, wa.last_data_at, wa.last_sync_at, wa.sync_status, wa.updated_at "+
		"FROM workspace_addresses wa WHERE wa.workspace_id=$1 AND wa.chain=$2 AND wa.normalized_address=$3",
		wsID, chain, normalizeWorkspaceAddress(chain, addr))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	var w model.WorkspaceAddress
	var fda, lda, lsa pgtype.Timestamptz
	err = rows.Scan(&w.ID, &w.Chain, &w.Address, &w.NormalizedAddress, &w.DisplayName, &w.PrimaryCaseID,
		&w.TransactionCount, &w.TokenTransferCount, &w.NftTransferCount, &w.LogCount, &w.TotalActivityCount,
		&w.FirstDataBlock, &w.LastDataBlock, &fda, &lda, &lsa, &w.SyncStatus, &w.UpdatedAt)
	if fda.Valid {
		t := fda.Time
		w.FirstDataAt = &t
	}
	if lda.Valid {
		t := lda.Time
		w.LastDataAt = &t
	}
	if lsa.Valid {
		t := lsa.Time
		w.LastSyncAt = &t
	}
	return &w, err
}

// SyncWorkspaceAddressStats 从 Coverage 聚合刷新目录统计（worker 调用，§46）。
func (s *Store) SyncWorkspaceAddressStats(ctx context.Context, wsID, orgID, chain, addr, syncStatus string) error {
	var tx, tt, nft, logs, total int64
	var firstBlock, lastBlock int64
	rows, err := s.Pool.Query(ctx, "SELECT dataset, COALESCE(SUM(row_count),0), MIN(from_block), MAX(to_block) "+
		"FROM dataset_coverage WHERE chain=$1 AND subject=$2 AND status='COMPLETE' GROUP BY dataset", chain, normalizeWorkspaceAddress(chain, addr))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ds string
		var rc, fb, lb int64
		if err := rows.Scan(&ds, &rc, &fb, &lb); err != nil {
			return err
		}
		switch ds {
		case "transactions":
			tx = rc
		case "token_transfers":
			tt = rc
		case "nft_transfers":
			nft = rc
		case "logs":
			logs = rc
		}
		total += rc
		if firstBlock == 0 || fb < firstBlock {
			firstBlock = fb
		}
		if lb > lastBlock {
			lastBlock = lb
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.UpdateWorkspaceAddressStats(ctx, wsID, chain, addr, syncStatus, tx, tt, nft, logs, firstBlock, lastBlock)
}

func (s *Store) UpdateWorkspaceAddressStats(ctx context.Context, wsID, chain, addr, syncStatus string, tx, tt, nft, logs, firstBlock, lastBlock int64) error {
	var firstBlockArg any
	if firstBlock != 0 {
		firstBlockArg = firstBlock
	}
	total := tx + tt + nft + logs
	_, err := s.Pool.Exec(ctx, "UPDATE workspace_addresses SET "+
		"transaction_count=$4, token_transfer_count=$5, nft_transfer_count=$6, log_count=$7, total_activity_count=$8, "+
		"first_data_block=COALESCE($9, first_data_block), last_data_block=GREATEST(COALESCE(last_data_block,0), $10), "+
		"last_sync_at=now(), sync_status=$11, updated_at=now() "+
		"WHERE workspace_id=$1 AND chain=$2 AND normalized_address=$3",
		wsID, chain, normalizeWorkspaceAddress(chain, addr), tx, tt, nft, logs, total, firstBlockArg, lastBlock, syncStatus)
	return err
}

func (s *Store) SetWorkspaceAddressSyncStatus(ctx context.Context, wsID, chain, addr, status string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE workspace_addresses SET sync_status=$4, updated_at=now() "+
		"WHERE workspace_id=$1 AND chain=$2 AND normalized_address=$3", wsID, chain, normalizeWorkspaceAddress(chain, addr), status)
	return err
}

// ---- Token metadata ----

func (s *Store) UpsertTokenMetadata(ctx context.Context, chain, contract, name, symbol string, decimals int, logoURL, tokenType, source string) error {
	if source == "" {
		source = "ankr"
	}
	chain = strings.ToLower(strings.TrimSpace(chain))
	contract = strings.TrimSpace(contract)
	if chain != "tron" {
		contract = strings.ToLower(contract)
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO token_metadata (chain, contract_address, normalized_address, name, symbol, decimals, logo_url, token_type, metadata_source) "+
		"VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8) "+
		"ON CONFLICT (chain, contract_address) DO UPDATE SET "+
		"name=COALESCE(EXCLUDED.name, token_metadata.name), "+
		"symbol=COALESCE(EXCLUDED.symbol, token_metadata.symbol), "+
		"decimals=COALESCE(EXCLUDED.decimals, token_metadata.decimals), "+
		"logo_url=COALESCE(EXCLUDED.logo_url, token_metadata.logo_url), "+
		"token_type=COALESCE(EXCLUDED.token_type, token_metadata.token_type), updated_at=now()",
		chain, contract, name, symbol, decimals, logoURL, tokenType, source)
	return err
}

func (s *Store) TokenMetadatas(ctx context.Context, chain string, contracts []string) (map[string]model.TokenMetadata, error) {
	out := map[string]model.TokenMetadata{}
	if len(contracts) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, "SELECT chain, contract_address, name, symbol, decimals, logo_url, token_type, verified, metadata_source "+
		"FROM token_metadata WHERE chain=$1 AND contract_address = ANY($2)", chain, contracts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m model.TokenMetadata
		if err := rows.Scan(&m.Chain, &m.ContractAddress, &m.Name, &m.Symbol, &m.Decimals, &m.LogoURL, &m.TokenType, &m.Verified, &m.MetadataSource); err != nil {
			return nil, err
		}
		out[m.ContractAddress] = m
	}
	return out, rows.Err()
}

// ---- Update previews ----

func (s *Store) CreateUpdatePreview(ctx context.Context, wsID, orgID, chain string, targetBlock int64, requestJSON, resultJSON []byte, ttl time.Duration) (string, error) {
	id := uuid.NewString()
	_, err := s.Pool.Exec(ctx, "INSERT INTO update_previews (id, organization_id, workspace_id, chain, target_block, request_json, result_json, expires_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6,$7,$8)", id, orgID, wsID, chain, targetBlock, requestJSON, resultJSON, time.Now().Add(ttl))
	return id, err
}

// UpdatePreviewScope 返回预览所属的租户作用域。
//
// 更新确认（POST /api/v1/updates/{id}/confirm）会据此把工作区状态写回**预览自己的**
// 作用域，而不是 Store.DefaultOrgWorkspace。
func (s *Store) UpdatePreviewScope(ctx context.Context, id string) (orgID, wsID string, err error) {
	err = s.Pool.QueryRow(ctx, "SELECT COALESCE(organization_id::text,''), COALESCE(workspace_id::text,'') FROM update_previews WHERE id=$1", id).Scan(&orgID, &wsID)
	return
}

func (s *Store) GetUpdatePreview(ctx context.Context, id string) (targetBlock int64, requestJSON, resultJSON []byte, expiresAt time.Time, err error) {
	err = s.Pool.QueryRow(ctx, "SELECT target_block, request_json, result_json, expires_at FROM update_previews WHERE id=$1", id).
		Scan(&targetBlock, &requestJSON, &resultJSON, &expiresAt)
	return
}

func (s *Store) DeleteExpiredPreviews(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, "DELETE FROM update_previews WHERE expires_at < now()")
	return tag.RowsAffected(), err
}

// ---- Export v1.2 ----

func (s *Store) CreateExportV2(ctx context.Context, wsID, orgID string, createdBy, jobID *string, dataset, format string, zip bool, snapshot []byte) (*model.Export, error) {
	e := &model.Export{
		ID: uuid.NewString(), JobID: jobID, Dataset: dataset, Format: format, Zip: zip, Status: "PENDING",
	}
	_, err := s.Pool.Exec(ctx, "INSERT INTO exports (id, job_id, dataset, format, zip, status, organization_id, workspace_id, created_by, query_snapshot, expires_at) "+
		"VALUES ($1,$2,$3,$4,$5,'PENDING',$6,$7,$8,$9::jsonb, now() + interval '24 hours')",
		e.ID, jobID, dataset, format, zip, orgID, wsID, createdBy, snapshot)
	return e, err
}

func (s *Store) UpdateExportV2(ctx context.Context, id, status, objectRef, sha string, rows int64, errMsg string) error {
	_, err := s.Pool.Exec(ctx, "UPDATE exports SET status=$2, object_ref=$3, sha256=$4, rows=$5, error=$6, "+
		"finished_at=CASE WHEN $2 IN ('COMPLETED','FAILED') THEN now() ELSE finished_at END, updated_at=now() "+
		"WHERE id=$1", id, status, objectRef, sha, rows, errMsg)
	return err
}

func (s *Store) CreateExportTicket(ctx context.Context, exportID string, userID *string, orgID, wsID string, ttl time.Duration) (string, error) {
	id := uuid.NewString()
	_, err := s.Pool.Exec(ctx, "INSERT INTO export_tickets (id, export_id, user_id, organization_id, workspace_id, expires_at) "+
		"VALUES ($1,$2,$3,$4,$5,$6)", id, exportID, userID, orgID, wsID, time.Now().Add(ttl))
	return id, err
}

func (s *Store) GetExportTicket(ctx context.Context, id string) (exportID string, expiresAt time.Time, err error) {
	err = s.Pool.QueryRow(ctx, "SELECT export_id, expires_at FROM export_tickets WHERE id=$1", id).Scan(&exportID, &expiresAt)
	return
}
