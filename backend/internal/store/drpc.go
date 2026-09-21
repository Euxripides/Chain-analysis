package store

// 本文件是 Solana dRPC 双通道的存储面：性能档位、运行状态、账户额度快照、分类密钥与按通道用量。
// 只 import internal/provider/route 的公开类型；drpc 与 drpcmgmt 都 import 本包，
// 反向依赖会成环，所以 AccountSnapshotRow 在本文件定义并由调用方填充。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/blockchain-platform/blockchain-platform/internal/provider/route"
)

// store 必须能直接充当路由注册表的档位来源（规范 §1.3）。
var _ route.ProfileSource = (*Store)(nil)

// profileColumns 是 route.Profile 的完整列投影，读写共用一份，避免 SELECT 与 Scan 漂移。
const profileColumns = `provider_key, chain, dataset, operation_class, mode, shard_concurrency,
	request_concurrency, requests_per_minute, requests_per_second, cu_per_minute, page_size,
	batch_size, request_weight, request_timeout_seconds, hard_ceiling, revision, verified_at,
	verification_source`

// scanProfile 读一行档位。numeric 先落到 pgtype.Numeric 再转 float64：
// 档位表的 numeric(18,6) 允许为 NULL，直接扫 *float64 会踩到空值语义。
func scanProfile(row pgx.Row) (route.Profile, error) {
	var p route.Profile
	var rpm, rps pgtype.Numeric
	var verifiedAt pgtype.Timestamptz
	err := row.Scan(&p.ProviderKey, &p.Chain, &p.Dataset, &p.OperationClass, &p.Mode,
		&p.ShardConcurrency, &p.RequestConcurrency, &rpm, &rps, &p.CUPerMinute, &p.PageSize,
		&p.BatchSize, &p.RequestWeight, &p.RequestTimeoutSeconds, &p.HardCeiling,
		&p.Revision, &verifiedAt, &p.VerificationSource)
	if err != nil {
		return route.Profile{}, err
	}
	if v, err := rpm.Float64Value(); err == nil {
		p.RequestsPerMinute = v.Float64
	}
	if v, err := rps.Float64Value(); err == nil {
		p.RequestsPerSecond = v.Float64
	}
	if verifiedAt.Valid {
		t := verifiedAt.Time
		p.VerifiedAt = &t
	}
	return p, nil
}

// PerformanceProfile 实现 route.ProfileSource：按 (Provider, 链, 数据集, 操作类, 模式) 取档位。
//
// dataset 先精确匹配再回退 '*'：三个 history 数据集共享同一 Data API 档位，
// 数据库只存一行 '*'，不必为每个数据集复制档位（规范 §2.1 种子）。
// 接口契约没有 error，因此查不到（含数据库不可用）一律返回 false，由调用方走默认档位。
func (s *Store) PerformanceProfile(ctx context.Context, providerKey, chain, dataset, operation, mode string) (route.Profile, bool) {
	providerKey, chain = normalizeLookup(providerKey), normalizeLookup(chain)
	dataset, operation, mode = strings.TrimSpace(dataset), strings.TrimSpace(operation), normalizeLookup(mode)
	if providerKey == "" || mode == "" {
		return route.Profile{}, false
	}
	row := s.Pool.QueryRow(ctx, `SELECT `+profileColumns+`
		FROM provider_performance_profiles
		WHERE provider_key=$1 AND chain=$2 AND operation_class=$4 AND mode=$5 AND dataset IN ($3,'*')
		ORDER BY (dataset = $3) DESC
		LIMIT 1`, providerKey, chain, dataset, operation, mode)
	p, err := scanProfile(row)
	if err != nil {
		return route.Profile{}, false
	}
	return p, true
}

// ListPerformanceProfiles 返回某个 Provider 的全部档位（后台展示用）。
// 模式按 safe -> fast -> turbo 排序：前端表格顺序不能依赖主键的字典序。
func (s *Store) ListPerformanceProfiles(ctx context.Context, providerKey string) ([]route.Profile, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+profileColumns+`
		FROM provider_performance_profiles
		WHERE provider_key=$1
		ORDER BY chain, dataset, operation_class,
		         CASE mode WHEN 'safe' THEN 1 WHEN 'fast' THEN 2 WHEN 'turbo' THEN 3 ELSE 4 END`,
		normalizeLookup(providerKey))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []route.Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdatePerformanceProfile 更新一行档位：revision 自增，修改前后的值写入 audit_events。
//
// 后台 PATCH 已经把权限、Step-up 与 If-Match 校验做完；这里只保证「读旧值 + 自增 + 写审计」
// 三件事原子且可核对。审计在提交后 best-effort 写入：审计失败不回滚已生效的档位
// （与 store 其余写路径一致，审计表是 append-only 的旁路）。
func (s *Store) UpdatePerformanceProfile(ctx context.Context, p route.Profile, reason, actorID string) (route.Profile, error) {
	providerKey := normalizeLookup(p.ProviderKey)
	chain := normalizeLookup(p.Chain)
	dataset := strings.TrimSpace(p.Dataset)
	operation := strings.TrimSpace(p.OperationClass)
	mode := normalizeLookup(p.Mode)
	if providerKey == "" || chain == "" || dataset == "" || operation == "" || mode == "" {
		return route.Profile{}, fmt.Errorf("update performance profile: provider/chain/dataset/operation/mode 均不能为空")
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return route.Profile{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// FOR UPDATE：并发 PATCH 下 revision 必须严格递增，不能让两个请求读到同一个旧 revision。
	before, err := scanProfile(tx.QueryRow(ctx, `SELECT `+profileColumns+`
		FROM provider_performance_profiles
		WHERE provider_key=$1 AND chain=$2 AND dataset=$3 AND operation_class=$4 AND mode=$5
		FOR UPDATE`, providerKey, chain, dataset, operation, mode))
	if err != nil {
		if err == pgx.ErrNoRows {
			return route.Profile{}, fmt.Errorf("performance profile %s/%s/%s/%s/%s: %w",
				providerKey, chain, dataset, operation, mode, pgx.ErrNoRows)
		}
		return route.Profile{}, err
	}

	after, err := scanProfile(tx.QueryRow(ctx, `UPDATE provider_performance_profiles SET
			shard_concurrency=$6, request_concurrency=$7, requests_per_minute=$8, requests_per_second=$9,
			cu_per_minute=$10, page_size=$11, batch_size=$12, request_weight=$13,
			request_timeout_seconds=$14, hard_ceiling=$15, verified_at=$16, verification_source=$17,
			revision=revision+1, updated_by=$18, updated_at=now()
		WHERE provider_key=$1 AND chain=$2 AND dataset=$3 AND operation_class=$4 AND mode=$5
		RETURNING `+profileColumns,
		providerKey, chain, dataset, operation, mode,
		p.ShardConcurrency, p.RequestConcurrency, p.RequestsPerMinute, p.RequestsPerSecond,
		p.CUPerMinute, p.PageSize, p.BatchSize, p.RequestWeight,
		p.RequestTimeoutSeconds, p.HardCeiling, p.VerifiedAt, verificationSourceOrInitial(p.VerificationSource),
		actorUUID(actorID)))
	if err != nil {
		return route.Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return route.Profile{}, err
	}

	_ = s.InsertAudit(ctx, actorAuditID(actorID), "provider.performance.update",
		"provider_performance_profile",
		fmt.Sprintf("%s:%s:%s:%s:%s", providerKey, chain, dataset, operation, mode),
		map[string]any{"reason": reason, "actor": actorID, "before": before, "after": after})
	return after, nil
}

// ProviderRuntimeState 是 provider_runtime_states 的一行：某个 Provider 在一条链上的
// 某个操作类当前算出来的档位与并发。档位表是配置，本结构是运行结果（规范 §1.3）。
type ProviderRuntimeState struct {
	ProviderKey       string
	Chain             string
	OperationClass    string
	RequestedMode     string
	EffectiveMode     string
	TargetConcurrency int
	ActiveConcurrency int
	RequestsPerMinute float64
	RequestsPerSecond float64
	CUPerMinute       int64
	DegradeReason     string
	ChannelStates     map[string]any
	UpdatedAt         time.Time
}

// RuntimeStates 返回某个 Provider 的全部运行状态（后台展示用），字段名与表列一一对应。
// 通道相关字段（api_channel/request_concurrency/已用 CU）不在这里：它们要么属于档位表，
// 要么属于 provider_usage，由调用方按契约合并，存储层不造派生值。
func (s *Store) RuntimeStates(ctx context.Context, providerKey string) ([]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT provider_key, chain, operation_class, requested_mode,
			effective_mode, target_concurrency, active_concurrency, requests_per_minute,
			requests_per_second, cu_per_minute, degrade_reason, channel_states, updated_at
		FROM provider_runtime_states WHERE provider_key=$1
		ORDER BY chain, operation_class`, normalizeLookup(providerKey))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			key, chain, operation, requested, effective, reason string
			target, active                                      int
			rpm, rps                                            pgtype.Numeric
			cu                                                  int64
			channelStates                                       []byte
			updatedAt                                           time.Time
		)
		if err := rows.Scan(&key, &chain, &operation, &requested, &effective, &target, &active,
			&rpm, &rps, &cu, &reason, &channelStates, &updatedAt); err != nil {
			return nil, err
		}
		var rpmV, rpsV float64
		if v, err := rpm.Float64Value(); err == nil {
			rpmV = v.Float64
		}
		if v, err := rps.Float64Value(); err == nil {
			rpsV = v.Float64
		}
		out = append(out, map[string]any{
			"provider_key": key, "chain": chain, "operation_class": operation,
			"requested_mode": requested, "effective_mode": effective,
			"target_concurrency": target, "active_concurrency": active,
			"requests_per_minute": rpmV, "requests_per_second": rpsV, "cu_per_minute": cu,
			"degrade_reason": reason, "channel_states": decodeJSONMap(channelStates),
			"updated_at": updatedAt,
		})
	}
	return out, rows.Err()
}

// UpsertRuntimeState 写一行运行状态。带「业务值真的变了才写」的守卫：
// 这是下载引擎的心跳路径（数秒一次），无条件 UPDATE 会持续产生 WAL 与死元组。
// updated_at 因此语义是「最后一次真正变化的时间」，与 ankr_performance_control 的既有约定一致。
func (s *Store) UpsertRuntimeState(ctx context.Context, st ProviderRuntimeState) error {
	providerKey := normalizeLookup(st.ProviderKey)
	if providerKey == "" {
		return fmt.Errorf("upsert runtime state: provider_key 不能为空")
	}
	channelStates, err := json.Marshal(orEmptyMap(st.ChannelStates))
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO provider_runtime_states
			(provider_key, chain, operation_class, requested_mode, effective_mode, target_concurrency,
			 active_concurrency, requests_per_minute, requests_per_second, cu_per_minute, degrade_reason,
			 channel_states, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,now())
		ON CONFLICT (provider_key, chain, operation_class) DO UPDATE SET
			requested_mode=$4, effective_mode=$5, target_concurrency=$6, active_concurrency=$7,
			requests_per_minute=$8, requests_per_second=$9, cu_per_minute=$10, degrade_reason=$11,
			channel_states=$12::jsonb, updated_at=now()
		WHERE provider_runtime_states.requested_mode IS DISTINCT FROM $4
		   OR provider_runtime_states.effective_mode IS DISTINCT FROM $5
		   OR provider_runtime_states.target_concurrency IS DISTINCT FROM $6
		   OR provider_runtime_states.active_concurrency IS DISTINCT FROM $7
		   OR provider_runtime_states.requests_per_minute IS DISTINCT FROM $8
		   OR provider_runtime_states.requests_per_second IS DISTINCT FROM $9
		   OR provider_runtime_states.cu_per_minute IS DISTINCT FROM $10
		   OR provider_runtime_states.degrade_reason IS DISTINCT FROM $11
		   OR provider_runtime_states.channel_states IS DISTINCT FROM $12::jsonb`,
		providerKey, st.Chain, st.OperationClass, st.RequestedMode, st.EffectiveMode,
		st.TargetConcurrency, st.ActiveConcurrency, st.RequestsPerMinute, st.RequestsPerSecond,
		st.CUPerMinute, st.DegradeReason, string(channelStates))
	return err
}

// AccountSnapshotRow 对应 provider_account_snapshots 的一行（文档 §11.2）。
//
// 为什么在 store 里定义：drpcmgmt 依赖 store，若这里 import drpcmgmt 会成环。
// 余额用字符串承载 numeric(38,18)：float64 存不下 38 位精度，而"原样保存数值、
// 不固定货币单位"是文档 §11.2 的硬要求。空串表示 NULL。
type AccountSnapshotRow struct {
	ProviderKey        string
	AccountTier        string
	AccountBalance     string
	AccountBalanceUnit string
	KeyID              string
	KeyName            string
	KeyEnabled         *bool
	CUSpentToday       *int64
	CUDayLimit         *int64
	CURemainingToday   *int64
	RateLimit          *float64
	RequestsHour       int64
	RequestsDay        int64
	CUHour             int64
	CUDay              int64
	Source             string
	SourceUpdatedAt    *time.Time
	SyncedAt           time.Time
	SyncStatus         string
	SyncErrorClass     string
	RawHash            string
}

// accountSnapshotColumns 与 AccountSnapshotRow 一一对应，读路径共用。
const accountSnapshotColumns = `provider_key, account_tier, account_balance, account_balance_unit,
	key_id, key_name, key_enabled, cu_spent_today, cu_day_limit, cu_remaining_today, rate_limit,
	requests_hour, requests_day, cu_hour, cu_day, source, source_updated_at, synced_at,
	sync_status, sync_error_class, raw_hash`

func scanAccountSnapshot(row pgx.Row) (AccountSnapshotRow, error) {
	var (
		snap                 AccountSnapshotRow
		balance, rate        pgtype.Numeric
		keyEnabled           pgtype.Bool
		spent, limit, remain pgtype.Int8
		sourceUpdated        pgtype.Timestamptz
	)
	err := row.Scan(&snap.ProviderKey, &snap.AccountTier, &balance, &snap.AccountBalanceUnit,
		&snap.KeyID, &snap.KeyName, &keyEnabled, &spent, &limit, &remain, &rate,
		&snap.RequestsHour, &snap.RequestsDay, &snap.CUHour, &snap.CUDay, &snap.Source,
		&sourceUpdated, &snap.SyncedAt, &snap.SyncStatus, &snap.SyncErrorClass, &snap.RawHash)
	if err != nil {
		return AccountSnapshotRow{}, err
	}
	snap.AccountBalance = numericToString(balance)
	if rate.Valid {
		if v, err := rate.Float64Value(); err == nil {
			f := v.Float64
			snap.RateLimit = &f
		}
	}
	if keyEnabled.Valid {
		b := keyEnabled.Bool
		snap.KeyEnabled = &b
	}
	snap.CUSpentToday = int8Ptr(spent)
	snap.CUDayLimit = int8Ptr(limit)
	snap.CURemainingToday = int8Ptr(remain)
	if sourceUpdated.Valid {
		t := sourceUpdated.Time
		snap.SourceUpdatedAt = &t
	}
	return snap, nil
}

// InsertAccountSnapshot 追加一条额度快照。append-only：后台额度曲线按时间点回溯，
// 覆盖最新值会让对账失去历史（文档 §11.2）。
func (s *Store) InsertAccountSnapshot(ctx context.Context, snap AccountSnapshotRow) error {
	providerKey := normalizeLookup(snap.ProviderKey)
	if providerKey == "" || strings.TrimSpace(snap.Source) == "" {
		return fmt.Errorf("insert account snapshot: provider_key 与 source 不能为空")
	}
	balance, err := numericParam(snap.AccountBalance)
	if err != nil {
		return fmt.Errorf("insert account snapshot: account_balance %q 不是合法数值: %w", snap.AccountBalance, err)
	}
	syncedAt := snap.SyncedAt
	if syncedAt.IsZero() {
		syncedAt = time.Now().UTC()
	}
	tier := snap.AccountTier
	if strings.TrimSpace(tier) == "" {
		tier = "unknown"
	}
	unit := snap.AccountBalanceUnit
	if strings.TrimSpace(unit) == "" {
		unit = "provider_unit"
	}
	// sync_status 是 NOT NULL 且无默认值：空值按 error 处理，
	// 因为"同步没给出状态"绝不能展示成 ok（文档 §11.2）。
	syncStatus := snap.SyncStatus
	if strings.TrimSpace(syncStatus) == "" {
		syncStatus = "error"
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO provider_account_snapshots
			(provider_key, account_tier, account_balance, account_balance_unit, key_id, key_name,
			 key_enabled, cu_spent_today, cu_day_limit, cu_remaining_today, rate_limit,
			 requests_hour, requests_day, cu_hour, cu_day, source, source_updated_at, synced_at,
			 sync_status, sync_error_class, raw_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		providerKey, tier, balance, unit, snap.KeyID, snap.KeyName, snap.KeyEnabled,
		snap.CUSpentToday, snap.CUDayLimit, snap.CURemainingToday, snap.RateLimit,
		snap.RequestsHour, snap.RequestsDay, snap.CUHour, snap.CUDay, snap.Source,
		snap.SourceUpdatedAt, syncedAt, syncStatus, snap.SyncErrorClass, snap.RawHash)
	return err
}

// LatestAccountSnapshot 返回最近一条快照。ok=false 表示还没有同步过，
// 调用方据此显示"未同步"而不是零值（文档 §11.5：不能把缺失当可用）。
func (s *Store) LatestAccountSnapshot(ctx context.Context, providerKey string) (*AccountSnapshotRow, bool, error) {
	snap, err := scanAccountSnapshot(s.Pool.QueryRow(ctx, `SELECT `+accountSnapshotColumns+`
		FROM provider_account_snapshots WHERE provider_key=$1
		ORDER BY synced_at DESC, id DESC LIMIT 1`, normalizeLookup(providerKey)))
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &snap, true, nil
}

// accountSnapshotHistoryMax 是历史查询的硬上限：后台曲线按天看，
// 5 分钟同步频率下一天 288 条，留足余量同时避免请求路径无界扫描。
const accountSnapshotHistoryMax = 5000

// AccountSnapshotHistory 返回 since 之后的快照，最新在前。
func (s *Store) AccountSnapshotHistory(ctx context.Context, providerKey string, since time.Time) ([]AccountSnapshotRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+accountSnapshotColumns+`
		FROM provider_account_snapshots WHERE provider_key=$1 AND synced_at >= $2
		ORDER BY synced_at DESC LIMIT $3`, normalizeLookup(providerKey), since, accountSnapshotHistoryMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountSnapshotRow{}
	for rows.Next() {
		snap, err := scanAccountSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// ListProviderSecretsByKind 按 secret_kind 分组返回密钥摘要，每个 kind 只给当前有效
// （或最新历史）一条：active 优先，其次 retiring/revoked 里 rotated_at 最新的一条。
// 只返回引用名与摘要字段，明文与完整值永远不出库（文档 §11.1）。
func (s *Store) ListProviderSecretsByKind(ctx context.Context, providerKey string) (map[string]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT secret_kind, secret_ref, prefix, fingerprint, status,
			rotated_at, last_used_at
		FROM provider_secrets WHERE provider_key=$1
		ORDER BY secret_kind,
		         CASE status WHEN 'active' THEN 0 WHEN 'retiring' THEN 1 ELSE 2 END,
		         rotated_at DESC`, normalizeLookup(providerKey))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var (
			kind, ref, prefix, fingerprint, status string
			rotatedAt                              time.Time
			lastUsed                               pgtype.Timestamptz
		)
		if err := rows.Scan(&kind, &ref, &prefix, &fingerprint, &status, &rotatedAt, &lastUsed); err != nil {
			return nil, err
		}
		if _, exists := out[kind]; exists {
			continue // 只保留排序最靠前的一条
		}
		item := map[string]any{
			"secret_kind": kind, "secret_ref": ref, "prefix": prefix,
			"fingerprint": fingerprint, "status": status, "rotated_at": rotatedAt,
		}
		if lastUsed.Valid {
			item["last_used_at"] = lastUsed.Time
		} else {
			item["last_used_at"] = nil
		}
		out[kind] = item
	}
	return out, rows.Err()
}

// UpsertSecretOfKind 写入某个 kind 的新密钥：先把同 Provider 同 kind 的 active 行改成
// retiring，再插入新 active 行。partial unique index 要求两步在同一事务内完成，
// 且这样轮换 runtime key 不会连带停掉两个管理 Token（文档 §11.1）。
// 明文不经过本方法：只接收受控引用与摘要。
func (s *Store) UpsertSecretOfKind(ctx context.Context, providerKey, kind, ref, prefix, fingerprint, note, actorID string) error {
	providerKey, kind = normalizeLookup(providerKey), normalizeLookup(kind)
	ref = strings.TrimSpace(ref)
	if providerKey == "" || kind == "" || ref == "" {
		return fmt.Errorf("upsert secret: provider_key/kind/secret_ref 不能为空")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `UPDATE provider_secrets SET status='retiring'
		WHERE provider_key=$1 AND secret_kind=$2 AND status='active'`, providerKey, kind); err != nil {
		return err
	}
	actor := actorUUID(actorID)
	if _, err := tx.Exec(ctx, `INSERT INTO provider_secrets
			(provider_key, secret_kind, secret_ref, prefix, fingerprint, status, note,
			 created_by, rotated_by, rotated_at)
		VALUES ($1,$2,$3,$4,$5,'active',$6,$7,$7,now())`,
		providerKey, kind, ref, prefix, fingerprint, note, actor); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkSecretUsed 记录密钥最近使用时间。调用方在请求路径上 best-effort 调用，
// 因此带 1 分钟节流：每个 HTTP 请求都写会持续产生 WAL 与死元组，
// 而秒级精度对后台展示没有价值。
func (s *Store) MarkSecretUsed(ctx context.Context, providerKey, kind string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE provider_secrets SET last_used_at=now()
		WHERE provider_key=$1 AND secret_kind=$2 AND status='active'
		  AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`,
		normalizeLookup(providerKey), normalizeLookup(kind))
	return err
}

// RoutingPoliciesForRouting 读取启用的路由策略并投影为 route.PolicyRow。
//
// api_channel/operation_class 从 config jsonb 取；老策略行还没有 config 时按 chain/dataset
// 推导内置默认值（规范 §1.2 的内置表），避免解析结果出现空通道导致按 Provider 级限流。
func (s *Store) RoutingPoliciesForRouting(ctx context.Context) ([]route.PolicyRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT policy_key, chain, dataset, enabled, primary_provider,
			coalesce(config->>'api_channel', ''), coalesce(config->>'operation_class', ''), revision
		FROM routing_policies WHERE enabled
		ORDER BY chain, dataset, policy_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []route.PolicyRow{}
	for rows.Next() {
		var p route.PolicyRow
		if err := rows.Scan(&p.PolicyKey, &p.Chain, &p.Dataset, &p.Enabled, &p.PrimaryProvider,
			&p.APIChannel, &p.OperationClass, &p.Revision); err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.APIChannel) == "" || strings.TrimSpace(p.OperationClass) == "" {
			channel, operation := builtinChannelAndOperation(p.Chain, p.Dataset)
			if strings.TrimSpace(p.APIChannel) == "" {
				p.APIChannel = channel
			}
			if strings.TrimSpace(p.OperationClass) == "" {
				p.OperationClass = operation
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// builtinChannelAndOperation 是内置默认路由表（规范 §1.2）的通道/操作类推导。
// TRON 的通道取决于 cfg.Tron.ProviderForDataset，库内无法推导，因此留空。
// Solana 的未知数据集（如 internal_transactions）同样留空：它没有对应通道，
// 编造一个会让调用方以为可以下载。
func builtinChannelAndOperation(chain, dataset string) (string, string) {
	switch normalizeLookup(chain) {
	case route.ChainSolana:
		switch normalizeLookup(dataset) {
		case route.DatasetTransactions, route.DatasetTokenTransfers, route.DatasetNFTTransfers:
			return route.ChannelDataAPI, route.OperationSolanaHistory
		case route.DatasetBalances:
			return route.ChannelSolanaRPC, route.OperationSolanaBalance
		case route.DatasetLogs:
			return route.ChannelSolanaRPC, route.OperationSolanaDetail
		}
		return "", ""
	case route.ChainTron:
		switch normalizeLookup(dataset) {
		case route.DatasetTransactions, route.DatasetTokenTransfers, route.DatasetInternalTransactions:
			return "", route.OperationTronHistory
		}
		return "", ""
	default:
		return route.ChannelAnkrAdvanced, route.OperationEVMHistory
	}
}

// UsageByChannel 汇总某个 Provider 最近一段时间各通道的请求数、方法调用数、CU、错误数与
// P95 时延（文档 §15.1）。api_channel 为空串的单通道 Provider 也会出现在结果里，
// 后台不需要区分"没有该通道"和"该通道没有用量"。
func (s *Store) UsageByChannel(ctx context.Context, providerKey string, since time.Time) (map[string]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT coalesce(api_channel, '') AS channel,
			count(*) AS requests,
			coalesce(sum(method_calls), 0) AS method_calls,
			coalesce(sum(credits), 0) AS cu,
			count(*) FILTER (WHERE status NOT IN ('ok', 'success', 'cache_hit', 'singleflight_hit')
			                   OR http_status >= 400) AS errors,
			coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) AS p95_ms
		FROM provider_usage
		WHERE provider=$1 AND request_time >= $2 AND usage_type <> 'trc20_enrichment_summary'
		GROUP BY 1 ORDER BY 1`, normalizeLookup(providerKey), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var (
			channel               string
			requests, methodCalls int64
			errors                int64
			cu, p95               float64
		)
		if err := rows.Scan(&channel, &requests, &methodCalls, &cu, &errors, &p95); err != nil {
			return nil, err
		}
		out[channel] = map[string]any{
			"requests": requests, "method_calls": methodCalls,
			"cu": cu, "errors": errors, "p95_ms": p95,
		}
	}
	return out, rows.Err()
}

// ---- 内部工具 ----

// normalizeLookup 统一小写去空白：库内 chain/provider/mode 都是小写字面量。
func normalizeLookup(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// actorUUID 把审计操作者转成可写 uuid 列的值；非 uuid（如测试用的 "tester"）写 NULL，
// 原始值仍由调用方放进审计 detail：不能让一个展示用标识把业务更新整体打回滚。
func actorUUID(actorID string) any {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return nil
	}
	if _, err := uuid.Parse(actorID); err != nil {
		return nil
	}
	return actorID
}

// actorAuditID 是 audit_events.user_id 需要的 *string 形态；非 uuid 同样写 NULL，
// 原始标识仍在审计 detail 里，保证更新本身不会因为一个展示用标识失败。
func actorAuditID(actorID string) *string {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return nil
	}
	if _, err := uuid.Parse(actorID); err != nil {
		return nil
	}
	return &actorID
}

func verificationSourceOrInitial(src string) string {
	if strings.TrimSpace(src) == "" {
		return "initial"
	}
	return strings.TrimSpace(src)
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// numericToString 原样输出 numeric 的十进制文本（避免 float64 丢精度）。
func numericToString(n pgtype.Numeric) string {
	if !n.Valid {
		return ""
	}
	v, err := n.Value()
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// numericParam 把十进制文本转成 numeric 参数；空串表示 NULL。
func numericParam(text string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	text = strings.TrimSpace(text)
	if text == "" {
		return n, nil
	}
	if err := n.Scan(text); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

func int8Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// decodeJSONMap 把 jsonb 列解成 map；解析失败返回空 map，展示路径不应因一条脏数据 500。
func decodeJSONMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}
