package drpcmgmt

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/shopspring/decimal"
)

// persist 落库三件事：额度快照（append-only）、provider_descriptors 摘要、跨阈值告警。
// 任何一步失败只告警不影响同步结果：管理数据的价值在于"尽力记录并如实标注"。
func (s *Service) persist(ctx context.Context, snap Snapshot) {
	// 没有任何管理接口被调用（管理 Token 缺失 / 同步未启用）时 Source 为空，
	// 而 provider_account_snapshots.source 非空：这种"没有数据来源"的状态不落额度快照行，
	// 同步状态仍会写进 provider_descriptors 摘要，供后台展示。
	if s.store != nil && snap.Source != "" {
		if err := s.store.InsertAccountSnapshot(ctx, toStoreRow(snap)); err != nil {
			s.log.Warn("drpc management: insert account snapshot failed", "error", err)
		}
	}
	s.writeLastTestSummary(ctx, snap)
	s.writeAlerts(ctx, snap)
}

// toStoreRow 把 Snapshot 映射成 store 行。
// 余额以十进制字符串原样写入；未知余额写空串（NULL），不写 "0"（开发文档 §11.2）。
func toStoreRow(snap Snapshot) store.AccountSnapshotRow {
	row := store.AccountSnapshotRow{
		ProviderKey:        ProviderKey,
		AccountTier:        snap.Tier,
		AccountBalanceUnit: snap.BalanceUnit,
		KeyID:              snap.KeyID,
		KeyName:            snap.KeyName,
		KeyEnabled:         snap.KeyEnabled,
		CUSpentToday:       snap.CUSpentToday,
		CUDayLimit:         snap.CUDayLimit,
		CURemainingToday:   snap.CURemainingToday,
		RateLimit:          snap.RateLimit,
		RequestsHour:       snap.RequestsHour,
		RequestsDay:        snap.RequestsDay,
		CUHour:             snap.CUHour,
		CUDay:              snap.CUDay,
		Source:             snap.Source,
		SourceUpdatedAt:    snap.SourceUpdatedAt,
		SyncedAt:           snap.SyncedAt,
		SyncStatus:         snap.SyncStatus,
		SyncErrorClass:     snap.SyncErrorClass,
		RawHash:            snap.RawHash,
	}
	row.AccountBalance = snap.BalanceString()
	return row
}

// snapshotFromRow 把库里的行还原成 Snapshot（进程重启后 LastSnapshot 的回退路径）。
func snapshotFromRow(row *store.AccountSnapshotRow) Snapshot {
	snap := Snapshot{
		Tier:             row.AccountTier,
		BalanceUnit:      row.AccountBalanceUnit,
		KeyID:            row.KeyID,
		KeyName:          row.KeyName,
		KeyEnabled:       row.KeyEnabled,
		CUSpentToday:     row.CUSpentToday,
		CUDayLimit:       row.CUDayLimit,
		CURemainingToday: row.CURemainingToday,
		RateLimit:        row.RateLimit,
		RequestsHour:     row.RequestsHour,
		RequestsDay:      row.RequestsDay,
		CUHour:           row.CUHour,
		CUDay:            row.CUDay,
		Source:           row.Source,
		SourceUpdatedAt:  row.SourceUpdatedAt,
		SyncedAt:         row.SyncedAt,
		SyncStatus:       row.SyncStatus,
		SyncErrorClass:   row.SyncErrorClass,
		RawHash:          row.RawHash,
	}
	if text := strings.TrimSpace(row.AccountBalance); text != "" {
		if value, err := decimal.NewFromString(text); err == nil {
			snap.Balance = &value
			snap.balanceRaw = text
		}
	}
	if strings.TrimSpace(snap.Tier) == "" {
		snap.Tier = TierUnknown
	}
	if strings.TrimSpace(snap.BalanceUnit) == "" {
		snap.BalanceUnit = DefaultBalanceUnit
	}
	return snap
}

// lastTestSummary 是 provider_descriptors.last_test_result 里的非敏感摘要。
// 只放工作流 D 列出的七个字段：tier/balance/balance_unit/cu_spent_today/cu_day_limit/
// synced_at/sync_status。不含任何 Key、Token、指纹或响应原文。
type lastTestSummary struct {
	Tier         string  `json:"tier"`
	Balance      *string `json:"balance"`
	BalanceUnit  string  `json:"balance_unit"`
	CUSpentToday *int64  `json:"cu_spent_today"`
	CUDayLimit   *int64  `json:"cu_day_limit"`
	SyncedAt     string  `json:"synced_at"`
	SyncStatus   string  `json:"sync_status"`
}

// 用 jsonb || 合并而不是整体替换：s6ProviderProbe 也写同一列（通道健康、探针耗时等），
// 整体替换会把探针摘要冲掉。这里只覆盖上面列出的七个非敏感字段。
const updateLastTestSummarySQL = `UPDATE provider_descriptors
	SET last_test_result = COALESCE(last_test_result, '{}'::jsonb) || $2::jsonb
	WHERE provider_key = $1`

func (s *Service) writeLastTestSummary(ctx context.Context, snap Snapshot) {
	if s.pool == nil {
		return
	}
	summary := lastTestSummary{
		Tier:         snap.Tier,
		BalanceUnit:  snap.BalanceUnit,
		CUSpentToday: snap.CUSpentToday,
		CUDayLimit:   snap.CUDayLimit,
		SyncedAt:     snap.SyncedAt.UTC().Format(time.RFC3339Nano),
		SyncStatus:   snap.SyncStatus,
	}
	if snap.Balance != nil {
		balance := snap.Balance.String()
		summary.Balance = &balance
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		s.log.Warn("drpc management: encode last_test_result summary failed", "error", err)
		return
	}
	if _, err := s.pool.Exec(ctx, updateLastTestSummarySQL, ProviderKey, string(raw)); err != nil {
		s.log.Warn("drpc management: update provider_descriptors.last_test_result failed", "error", err)
	}
}
