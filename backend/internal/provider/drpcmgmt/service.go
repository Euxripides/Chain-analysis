// Package drpcmgmt 同步 dRPC 管理面信息（账户余额、Key 当日 CU、聚合请求量与 CU）。
//
// 为什么单独成包：数据面（internal/provider/drpc）只关心"还能不能发请求"，管理面关心
// "账户还剩多少、Key 是否启用、用量曲线"；两者的凭据、调用频率与失败语义都不同
// （管理接口官方限制 10 请求/分钟，开发文档 §3.3）。本包不 import drpc/route，而是通过
// Options.OnSnapshot 回调把额度交给 app 注入数据面，避免包循环。
//
// 容错原则（规范 §1.5、开发文档 §11.2/§11.3）：官方文档只说明管理接口的字段语义，
// 没有给出完整响应样例。因此响应以 json.RawMessage 承载、解析走宽松路径（parse.go）：
// 解析不到的字段留零值或 nil，
// 并在 SyncErrorClass 里标注 unexpected_shape；绝不 panic，绝不猜测或编造数值，
// 也绝不把 account balance 固定当成 USD（单位单独保存在 BalanceUnit，默认 provider_unit）。
package drpcmgmt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"github.com/blockchain-platform/blockchain-platform/internal/secret"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
	"github.com/shopspring/decimal"
)

const (
	// ProviderKey 是本包同步的 Provider（provider_descriptors.provider_key）。
	ProviderKey = "drpc"

	// SyncStatus 取值（规范 §1.5）。
	SyncStatusOK      = "ok"
	SyncStatusNoToken = "ok_no_management_token"
	SyncStatusError   = "error"

	// SyncErrorClass 取值。HTTP/网络失败按状态码分类；解析失败分两类：
	// invalid_json 表示响应根本不是 JSON（SyncStatus=error），
	// unexpected_shape 表示 JSON 合法但找不到约定字段（SyncStatus 仍为 ok，字段留零值）。
	ErrClassNoToken         = "no_management_token"
	ErrClassUnauthorized    = "unauthorized"
	ErrClassClientError     = "client_error"
	ErrClassServerError     = "server_error"
	ErrClassNetwork         = "network"
	ErrClassInvalidJSON     = "invalid_json"
	ErrClassUnexpectedShape = "unexpected_shape"
	ErrClassDisabled        = "disabled"

	// Source 取值：本次快照的数据来自哪些管理接口（前端"数据来源"标签直接使用）。
	// 调用失败时 Source 仍标注"尝试过的接口"，与 SyncStatus=error 一起看才是完整语义。
	SourceStatistics = "drpc_statistics_api"
	SourceKeys       = "drpc_keys_api"
	SourceBoth       = SourceStatistics + "+" + SourceKeys

	// DefaultBalanceUnit 是官方未给出单位时的占位。开发文档 §11.2：dRPC 只把 balance
	// 定义为数值，未确认货币单位前不得在代码里固定成 USD，也不加货币符号。
	DefaultBalanceUnit = "provider_unit"

	// TierUnknown 是官方未返回档位时的占位（与迁移的 account_tier 默认值一致）。
	TierUnknown = "unknown"

	// StatisticsPath / KeysPath 是两类管理接口路径（开发文档 §3.3）。
	StatisticsPath = "/api/metrics/json"
	KeysPath       = "/api/team/keys/list"
)

const (
	// minSyncInterval 是同步周期下限：规范 §1.5 要求 5 分钟，config 已校验一次，这里再钳一次。
	minSyncInterval = 5 * time.Minute
	// debounceWindow 是后台手工同步的防抖窗口（规范 §1.5：60 秒内返回上次快照）。
	debounceWindow = 60 * time.Second
	// minRequestSpacing 是两次管理接口调用之间的最小间隔。官方限制 10 请求/分钟
	// （开发文档 §3.3）；一次同步最多 3 个调用（metrics hour/day + keys），100ms 间隔远低于配额，
	// 但能保证突发时不会把多次调用打成"同一瞬间"。
	minRequestSpacing = 100 * time.Millisecond
	// defaultHTTPTimeout 在 Options.HTTPTimeout 未设置时兜底。
	defaultHTTPTimeout = 30 * time.Second
	// maxResponseBytes 限制单次管理响应体大小，避免异常响应吃满内存。
	maxResponseBytes = 8 << 20
)

// ErrManagementSync 表示一次同步没有拿到完整的可用管理数据（SyncStatus=error）。
var ErrManagementSync = errors.New("drpc management sync failed")

// Options 是管理同步的配置（规范 §1.5）。
type Options struct {
	Enabled            bool
	StatisticsTokenRef string
	KeysTokenRef       string
	Secrets            secret.Resolver
	BaseURL            string // https://drpc.org
	SyncInterval       time.Duration
	StaleAfter         time.Duration
	SafeOnlyAfter      time.Duration
	HTTPTimeout        time.Duration
	// RuntimeKeyFingerprint 由 app 注入：本系统实际使用的 runtime API Key 的指纹
	// （sha256 前 16 hex，与 secret.RefStatus.Fingerprint / provider_secrets.fingerprint 同法）。
	// 规范 §1.5 的 Options 未列出该字段，由工作流 D 按集成需要补充。匹配顺序为
	// 指纹 -> Key 名 -> Key ID；都匹配不到时退回"启用且当日 CU 消耗最大"的 Key（见 selectTeamKey）。
	RuntimeKeyFingerprint string
	// Tier 由 app 从 DRPC_TIER 注入（paid|free|unknown）。
	// 官方管理接口不返回套餐档位，档位只能来自配置；不注入时保持 unknown，
	// 页面显示"层级未知"，而不是把未知当成免费或付费。
	Tier string
	// OnSnapshot 由 app 注入：把额度注入 drpc.Client.SetQuotaSnapshot 并更新 Prometheus Gauge。
	// 只在本轮同步不是 error 时回调：失败时字段未知，注入空额度会把数据面闸门误判成"没有额度"。
	OnSnapshot func(Snapshot)
	Log        *slog.Logger
}

// Snapshot 是一次管理同步的合并结果（规范 §1.5）。
//
// JSON 形态对齐后台契约（开发文档 §11.4）：balance 以字符串输出，numeric(38,18) 的精度
// 不能被 JSON number / JS Number 吃掉；未知余额输出 null，不编造 0。
type Snapshot struct {
	Tier             string           `json:"tier"`
	Balance          *decimal.Decimal `json:"balance"`
	BalanceUnit      string           `json:"balance_unit"`
	KeyID            string           `json:"key_id"`
	KeyName          string           `json:"key_name"`
	KeyEnabled       *bool            `json:"key_enabled"`
	CUSpentToday     *int64           `json:"cu_spent_today"`
	CUDayLimit       *int64           `json:"cu_day_limit"`
	CURemainingToday *int64           `json:"cu_remaining_today"`
	RateLimit        *float64         `json:"rate_limit"`
	RequestsHour     int64            `json:"requests_hour"`
	RequestsDay      int64            `json:"requests_day"`
	CUHour           int64            `json:"cu_hour"`
	CUDay            int64            `json:"cu_day"`
	Source           string           `json:"source"`
	SourceUpdatedAt  *time.Time       `json:"source_updated_at"`
	SyncedAt         time.Time        `json:"synced_at"`
	SyncStatus       string           `json:"sync_status"`
	SyncErrorClass   string           `json:"sync_error_class"`
	RawHash          string           `json:"raw_hash"`

	// balanceRaw 保存上游返回的余额原始文本（例如 "12.345000000000000000"）。
	// decimal 会把尾零规范化成 "12.345"，而"原样保存数值"是开发文档 §11.2 的硬要求，
	// 所以落库与 JSON 输出优先用这份原文；该字段不导出，也不参与 JSON 编码。
	balanceRaw string
}

// BalanceString 返回应当落库/输出的余额文本：优先上游原文，其次是 decimal 的规范化结果。
// 两者都没有时返回空串（落库为空 = NULL），绝不返回 "0" 冒充已知余额。
//
// app 把它注入 drpc.Client.SetQuotaSnapshot（QuotaSnapshot.Balance 是 string）时用这个
// 而不是 Balance.String()：decimal 会规范化尾零（"12.345000000000000000" -> "12.345"），
// 而展示与对账需要上游原文。
func (s Snapshot) BalanceString() string {
	if s.balanceRaw != "" {
		return s.balanceRaw
	}
	if s.Balance != nil {
		return s.Balance.String()
	}
	return ""
}

// snapshotAlias 去掉 Snapshot 的方法集，避免下面的 MarshalJSON 递归调用自己。
type snapshotAlias Snapshot

// MarshalJSON 把 Balance 渲染成字符串（nil -> null），其余字段原样输出。
func (s Snapshot) MarshalJSON() ([]byte, error) {
	payload := struct {
		snapshotAlias
		Balance *string `json:"balance"`
	}{snapshotAlias: snapshotAlias(s)}
	if text := s.BalanceString(); text != "" {
		payload.Balance = &text
	}
	return json.Marshal(payload)
}

// snapshotStore 是本包对 store 包的最小依赖面（规范 §1.5 冻结的两个方法）。
// 用接口而不是 *store.Store：单测不需要真实 PostgreSQL，而 New 的入参仍是 *store.Store。
type snapshotStore interface {
	InsertAccountSnapshot(ctx context.Context, snap store.AccountSnapshotRow) error
	LatestAccountSnapshot(ctx context.Context, providerKey string) (*store.AccountSnapshotRow, bool, error)
}

// Service 周期同步 dRPC 管理面信息并落库。
//
// 并发模型：同一时刻只有一次同步在跑（syncMu），管理接口调用之间还有最小间隔（reqMu）；
// 快照与防抖时间戳由 mu 保护。Run 与后台手工触发（SyncDebounced）可以安全并发调用。
type Service struct {
	opts   Options
	log    *slog.Logger
	client *http.Client
	store  snapshotStore
	pool   *dbtx.Pool

	// now 可注入：防抖与请求间隔按它计算，单测据此在毫秒内验证 60 秒窗口。
	now func() time.Time

	syncMu sync.Mutex

	mu         sync.RWMutex
	last       *Snapshot
	lastSyncAt time.Time

	reqMu         sync.Mutex
	lastRequestAt time.Time
}

// New 构造管理同步服务。st 为 nil 时只维护内存快照（不落库、不写告警），
// 便于未接数据库的进程与单测使用。永不返回 nil。
func New(opts Options, st *store.Store, log *slog.Logger) *Service {
	var (
		snapStore snapshotStore
		pool      *dbtx.Pool
	)
	if st != nil {
		snapStore = st
		pool = st.Pool
	}
	if log != nil {
		opts.Log = log
	}
	return newService(opts, snapStore, pool)
}

func newService(opts Options, snapStore snapshotStore, pool *dbtx.Pool) *Service {
	opts = opts.normalized()
	return &Service{
		opts:   opts,
		log:    opts.Log,
		client: &http.Client{Timeout: opts.HTTPTimeout},
		store:  snapStore,
		pool:   pool,
		now:    time.Now,
	}
}

// normalized 补齐默认值并钳制同步周期（规范 §1.5：下限 5 分钟）。
func (o Options) normalized() Options {
	o.BaseURL = strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if o.BaseURL == "" {
		o.BaseURL = "https://drpc.org"
	}
	if o.HTTPTimeout <= 0 {
		o.HTTPTimeout = defaultHTTPTimeout
	}
	if o.SyncInterval < minSyncInterval {
		o.SyncInterval = minSyncInterval
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return o
}

// Sync 立刻同步一次：拉取 Statistics API（range=hour/day）与 Keys API，合并成 Snapshot 并落库。
//
// 返回的 Snapshot 永远可用（即使出错也带上 SyncStatus/SyncErrorClass）；error 只在
// SyncStatus=error 时非 nil，便于调用方记日志。
func (s *Service) Sync(ctx context.Context) (Snapshot, error) {
	return s.sync(ctx, false)
}

// SyncDebounced 是后台手工同步入口：60 秒内直接返回上次快照（规范 §1.5）。
func (s *Service) SyncDebounced(ctx context.Context) (Snapshot, error) {
	return s.sync(ctx, true)
}

func (s *Service) sync(ctx context.Context, debounced bool) (Snapshot, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	if debounced {
		if snap, ok := s.recentSnapshot(); ok {
			return snap, nil
		}
	}
	snap, err := s.syncOnce(ctx)
	s.record(snap)
	s.persist(ctx, snap)
	s.notify(snap)
	return snap, err
}

// syncOnce 是一次完整同步。所有调用串行执行，间隔由 getJSON 保证。
func (s *Service) syncOnce(ctx context.Context) (Snapshot, error) {
	tier := strings.TrimSpace(s.opts.Tier)
	if tier == "" {
		tier = TierUnknown
	}
	snap := Snapshot{
		Tier:        tier,
		BalanceUnit: DefaultBalanceUnit,
		SyncedAt:    s.now().UTC(),
		SyncStatus:  SyncStatusOK,
	}
	if !s.opts.Enabled {
		snap.SyncStatus = SyncStatusError
		snap.SyncErrorClass = ErrClassDisabled
		return snap, fmt.Errorf("%w: %s", ErrManagementSync, ErrClassDisabled)
	}

	statsToken, statsErr := s.resolveToken(ctx, s.opts.StatisticsTokenRef)
	keysToken, keysErr := s.resolveToken(ctx, s.opts.KeysTokenRef)
	if statsErr != nil && keysErr != nil {
		// 两类管理 Token 都没有：管理面未配置，不是故障——数据面继续按本地档位运行。
		// 这里如实标注状态，不编造余额/额度，也不发任何管理请求（避免无凭据的 401 噪音）。
		snap.SyncStatus = SyncStatusNoToken
		s.log.Warn("drpc management tokens are not configured; quota sync skipped")
		return snap, nil
	}

	var (
		soft    []string // 数据缺口：不把 SyncStatus 变成 error
		hard    []string // HTTP/网络/JSON 失败
		hashes  []string
		missing []string
		sources []string
	)
	if statsErr != nil || keysErr != nil {
		soft = appendUnique(soft, ErrClassNoToken)
	}

	if statsToken != "" {
		sources = append(sources, SourceStatistics)
		hour := s.fetchMetrics(ctx, statsToken, "hour")
		day := s.fetchMetrics(ctx, statsToken, "day")
		noteMetricsFetch(hour, &hard, &hashes, &missing)
		noteMetricsFetch(day, &hard, &hashes, &missing)
		applyMetrics(&snap, hour.totals, day.totals)
	}
	if keysToken != "" {
		sources = append(sources, SourceKeys)
		fetched := s.fetchKeys(ctx, keysToken)
		if fetched.class != "" {
			hard = appendUnique(hard, fetched.class)
		} else {
			if fetched.hash != "" {
				hashes = append(hashes, fetched.hash)
			}
			applyTeamKeys(&snap, fetched.keys, s.opts.RuntimeKeyFingerprint)
		}
	}

	if len(missing) > 0 {
		soft = appendUnique(soft, ErrClassUnexpectedShape)
		s.log.Debug("drpc management response missing expected fields",
			"fields", strings.Join(appendUnique(nil, missing...), ","))
	}
	snap.Source = strings.Join(sources, "+")
	if len(hashes) > 0 {
		snap.RawHash = sha256Hex([]byte(strings.Join(hashes, "|")))
	}
	if len(hard) > 0 {
		snap.SyncStatus = SyncStatusError
		snap.SyncErrorClass = strings.Join(append(hard, soft...), ",")
		return snap, fmt.Errorf("%w: %s", ErrManagementSync, snap.SyncErrorClass)
	}
	snap.SyncStatus = SyncStatusOK
	snap.SyncErrorClass = strings.Join(soft, ",")
	return snap, nil
}

func noteMetricsFetch(fetch metricsFetch, hard, hashes, missing *[]string) {
	if fetch.hash != "" {
		*hashes = append(*hashes, fetch.hash)
	}
	if fetch.class != "" {
		*hard = appendUnique(*hard, fetch.class)
		return
	}
	*missing = append(*missing, fetch.missing...)
}

// applyMetrics 合并两个区间的聚合结果。
//
// 余额/档位/时间优先取 range=day（规范 §1.5 列出的规范调用），缺失时回退 hour；
// 请求量与 CU 按区间分别落位。参数为 nil 表示该次调用失败，对应字段保持零值。
func applyMetrics(snap *Snapshot, hour, day *metricsTotals) {
	for _, totals := range []*metricsTotals{day, hour} {
		if totals == nil {
			continue
		}
		if snap.Balance == nil && totals.Balance != nil {
			snap.Balance = totals.Balance
			snap.balanceRaw = totals.BalanceText
		}
		if snap.BalanceUnit == DefaultBalanceUnit && totals.BalanceUnit != "" {
			snap.BalanceUnit = totals.BalanceUnit
		}
		if snap.Tier == TierUnknown && totals.Tier != "" {
			snap.Tier = totals.Tier
		}
		if snap.SourceUpdatedAt == nil && totals.UpdatedAt != nil {
			snap.SourceUpdatedAt = totals.UpdatedAt
		}
	}
	if hour != nil {
		snap.RequestsHour = hour.Requests
		snap.CUHour = hour.CU
	}
	if day != nil {
		snap.RequestsDay = day.Requests
		snap.CUDay = day.CU
	}
}

// applyTeamKeys 把选中的 Key 写入快照，并据此计算当日剩余额度。
func applyTeamKeys(snap *Snapshot, keys []teamKey, selector string) {
	key, ok := selectTeamKey(keys, selector)
	if !ok {
		// 列表里没有可用 Key 时不猜：KeyID/KeyName 留空、KeyEnabled 留 nil（未知）。
		snap.CURemainingToday = nil
		return
	}
	snap.KeyID = key.ID
	snap.KeyName = key.Name
	snap.KeyEnabled = key.Enabled
	snap.CUSpentToday = key.CUSpentToday
	snap.CUDayLimit = key.CUDayLimit
	snap.RateLimit = key.RateLimit
	if snap.Tier == TierUnknown && key.Tier != "" {
		snap.Tier = key.Tier
	}
	if snap.SourceUpdatedAt == nil && key.UpdatedAt != nil {
		snap.SourceUpdatedAt = key.UpdatedAt
	}
	snap.CURemainingToday = remainingToday(snap.CUSpentToday, snap.CUDayLimit)
}

// remainingToday 只在日上限为正时计算剩余（规范 §1.5：cu_day_limit 空或 0 -> 不计算剩余，
// 前端显示"未设置日上限"）。返回 nil 表示"未设置上限"，绝不返回 0 冒充"剩余 0"。
func remainingToday(spent, limit *int64) *int64 {
	if limit == nil || *limit <= 0 {
		return nil
	}
	used := int64(0)
	if spent != nil {
		used = *spent
	}
	remaining := *limit - used
	if remaining < 0 {
		remaining = 0
	}
	return &remaining
}

// Run 周期循环，间隔 Options.SyncInterval（下限 5 分钟）。
// 启动先同步一次，后台首屏不必等一个周期；ctx 取消后立即返回。
func (s *Service) Run(ctx context.Context) {
	if !s.opts.Enabled {
		s.log.Info("drpc management sync is disabled")
		return
	}
	s.log.Info("drpc management sync started",
		"interval", s.opts.SyncInterval.String(), "base_url", s.opts.BaseURL)
	if _, err := s.Sync(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("drpc management sync failed", "error", err)
	}
	ticker := time.NewTicker(s.opts.SyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.Sync(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("drpc management sync failed", "error", err)
			}
		}
	}
}

// LastSnapshot 返回最近一次快照：先看内存，进程刚启动时回退数据库最新一条；
// 都没有时返回 (nil, nil)，调用方据此显示"未同步"而不是零值。
func (s *Service) LastSnapshot(ctx context.Context) (*Snapshot, error) {
	s.mu.RLock()
	if s.last != nil {
		snap := *s.last
		s.mu.RUnlock()
		return &snap, nil
	}
	s.mu.RUnlock()

	if s.store == nil {
		return nil, nil
	}
	row, ok, err := s.store.LatestAccountSnapshot(ctx, ProviderKey)
	if err != nil {
		return nil, fmt.Errorf("load latest %s account snapshot: %w", ProviderKey, err)
	}
	if !ok || row == nil {
		return nil, nil
	}
	snap := snapshotFromRow(row)
	return &snap, nil
}

// record 发布内存快照与同步时间戳（防抖与 LastSnapshot 都读它）。
func (s *Service) record(snap Snapshot) {
	s.mu.Lock()
	snapshot := snap
	s.last = &snapshot
	s.lastSyncAt = s.now()
	s.mu.Unlock()
}

// recentSnapshot 在防抖窗口内返回上次快照。
func (s *Service) recentSnapshot() (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.last == nil || s.lastSyncAt.IsZero() {
		return Snapshot{}, false
	}
	if s.now().Sub(s.lastSyncAt) >= debounceWindow {
		return Snapshot{}, false
	}
	return *s.last, true
}

// notify 回调 app。失败（SyncStatus=error）时不回调：字段未知，注入空额度会让数据面
// 把"同步失败"误判成"没有额度"；app 侧通过 Sync 的 error 与 LastSnapshot 观察失败。
func (s *Service) notify(snap Snapshot) {
	if s.opts.OnSnapshot == nil || snap.SyncStatus == SyncStatusError {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Error("drpc management OnSnapshot callback panicked", "panic", recovered)
		}
	}()
	s.opts.OnSnapshot(snap)
}

// appendUnique 去重追加（保持顺序），用于拼接 SyncErrorClass 与数据来源。
func appendUnique(list []string, items ...string) []string {
	for _, item := range items {
		if item == "" || slices.Contains(list, item) {
			continue
		}
		list = append(list, item)
	}
	return list
}
