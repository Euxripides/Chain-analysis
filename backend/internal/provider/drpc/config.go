// Package drpc 实现 dRPC Solana 双通道 Provider：Data API 历史页 + Solana JSON-RPC。
//
// 与旧实现（internal/provider/solana）的关系：本包**自包含**，刻意不 import 旧传输层，
// 复用的 Solana 解析辅助函数在来源处注明；两者都实现 provider.Provider，未配置 dRPC 时
// 线上继续走旧 Provider（规范 §0：回退，不回归）。
//
// 三条硬约束（规范 §1.4/§3）：
//  1. Solana 地址是 base58 大小写敏感字符串，任何 ToLower 都会得到另一个（无效的）地址；
//  2. Key 只允许出现在构造后的请求 URL 中，日志、错误、用量行一律经 maskURL/sanitizeText 脱敏；
//  3. 拿不到的字段留空或计数，绝不用本地猜测补齐（不伪造数据）。
package drpc

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
)

// 规范 §1.4 冻结常量。
const (
	ProviderKey      = "drpc"
	ChannelDataAPI   = "data_api"
	ChannelSolanaRPC = "solana_rpc"
)

// 本包服务的链与数据集。
const (
	ChainSolana = "solana"

	DatasetTransactions         = "transactions"
	DatasetTokenTransfers       = "token_transfers"
	DatasetNFTTransfers         = "nft_transfers"
	DatasetInternalTransactions = "internal_transactions"
	DatasetLogs                 = "logs"
	DatasetBalances             = "balances"

	// 操作类别与 route 内置路由表（规范 §1.2）保持一致，Profile 按它解析。
	OperationSolanaHistory = "solana_history"
	OperationSolanaBalance = "solana_balance"
	OperationSolanaDetail  = "solana_detail"
)

// 性能模式。数据库 CHECK 只允许这三档（开发文档 §10.1）。
const (
	ModeSafe  = "safe"
	ModeFast  = "fast"
	ModeTurbo = "turbo"

	// DefaultMode 是没有 ModeResolver（或 resolver 返回空）时的模式。
	//
	// FetchRequest 里没有性能模式字段（QueryMode 表达的是"全量/区间"的用户意图，
	// 不是并发档位），因此由 app 通过 Options.ModeResolver 注入"当前任务请求模式"。
	DefaultMode = ModeFast
)

// 账户层级。paid 之外（free/unknown/空）一律按未知层级钳制（规范 §3.4）。
const (
	TierPaid    = "paid"
	TierFree    = "free"
	TierUnknown = "unknown"
)

// CU 与价格常量。
const (
	// CUDataAPIHistory：官方页面标明 Data API 每次调用 1837 CU（开发文档 §3.1）。
	CUDataAPIHistory int64 = 1837
	// CURPCCall：官方 CU 表 Solana RPC 方法通常 20 CU（开发文档 §3.1）。
	CURPCCall int64 = 20
	// CURPCHealth：getHealth 0 CU；探针用它，避免健康检查本身消耗额度。
	CURPCHealth int64 = 0
	// CURPCSignatures：getSignaturesForAddress 也按普通 RPC 方法 20 CU 计。
	CURPCSignatures int64 = CURPCCall

	// USDPerCU：官方当前价格页 1,000,000 CU = 0.30 USD（开发文档 §15.1）。
	//
	// 注意：开发文档要求正式价格写入 provider_pricing 并带生效时间，不把数值散落在代码中。
	// 本包只用它做**本地估算**（UsageRow.CostUSD），对账仍以 dRPC Statistics API 为准；
	// 引入定价表读取之前不要把这个常量当作计费依据。
	USDPerCU = 0.30 / 1e6

	// FreeTierMaxRPS：免费/未知层级下 RPC 通道的 RPS 上限。
	// 45,000 / 20 / 60 = 37.5，按开发文档 §6.2 实现取 36（不发出突发请求）。
	FreeTierMaxRPS = 36

	// DefaultUnknownTierCUPerMinute 对应 DRPC_UNKNOWN_TIER_CU_PER_MINUTE 的默认值。
	DefaultUnknownTierCUPerMinute int64 = 45000

	// DefaultHistoryPageSize 对应 DRPC_SOLANA_HISTORY_PAGE_SIZE 的默认值。
	DefaultHistoryPageSize = 100
	// maxSignaturesPerCall 是 Solana RPC getSignaturesForAddress 的硬上限。
	maxSignaturesPerCall = 1000
	// maxResponseBytes 是单次响应的读取上限：超过即按 body 截断处理，绝不静默截半。
	maxResponseBytes = 8 << 20
)

// maxBalanceAccounts 是单地址余额快照的账户上限：超过即显式失败，绝不静默截断。
//
// 旧实现实测 20000 个账户约 2.2MB，低于 8MB 响应上限，留有余量
// （来源：internal/provider/solana/balances.go）。声明为变量以便测试注入更小的上限。
var maxBalanceAccounts = 20000

// 密钥缓存与探针缓存时长。
const (
	// keyCacheTTL：每次请求都经 secret.Resolver 解析 RuntimeKeyRef，带 30s 缓存，
	// 使后台轮换/新增 Key 后不重启进程即可生效。
	keyCacheTTL = 30 * time.Second
	// probeCacheTTL：Health 的 30s 缓存，避免健康探针把配额打爆（规范 §1.4 关键行为 9）。
	probeCacheTTL = 30 * time.Second
)

// Options 构造 dRPC Provider（规范 §1.4 冻结字段）。
type Options struct {
	Enabled bool
	// RPCHistory uses finalized signature pagination and transaction details
	// for historical datasets. This avoids Data API rows without a real slot.
	RPCHistory bool
	// DisableProbes prevents Health and ProbeChannels from making test requests.
	// Fetch remains available for real download work.
	DisableProbes bool
	Tier          string // paid | free | unknown
	// BaseURL 形如 https://lb.drpc.live/solana（不含 key）。
	BaseURL            string
	RuntimeKeyRef      string
	StatisticsTokenRef string
	KeysTokenRef       string
	Secrets            Resolver

	HistoryPageSize        int
	MaxTxVersion           int
	RPCTimeout             time.Duration
	DataTimeout            time.Duration
	SlotTimeSearchLimit    int
	SlotTimeMargin         time.Duration
	UnknownTierCUPerMinute int64
	QuotaWarnPercent       int
	QuotaSafeOnlyPercent   int
	QuotaStopPercent       int
	BalanceWarn            string
	ProfileSource          ProfileSource
	UsageHook              provider.UsageHook
	Log                    *slog.Logger

	// ---- 以下为规范 §1.4 之外的增补项（只增不改，零值时取内置默认）----

	// ModeResolver 由 app 注入"当前任务请求模式"；返回空串时退回 DefaultMode。
	ModeResolver func(ctx context.Context) string
	// ManagementStaleAfter / ManagementSafeOnlyAfter 与 DRPC_MANAGEMENT_*_SECONDS 对应：
	// 管理快照超过前者时 TURBO 不可用，超过后者时最多 SAFE（开发文档 §6.4）。
	ManagementStaleAfter    time.Duration
	ManagementSafeOnlyAfter time.Duration
	// Now 是时钟注入点，仅供测试使用（生产为 nil → time.Now）。
	Now func() time.Time
	// KeyCacheTTL allows the provider center to apply key and switch changes
	// across API and worker processes within its one-second control window.
	KeyCacheTTL     time.Duration
	BaseURLResolver func(context.Context) (string, error)
}

// config 是 Options 归一化后的内部配置。零值选项在这里补齐默认值，
// 使调用方（app.go 接线）不必重复配置默认值。
type config struct {
	enabled       bool
	rpcHistory    bool
	disableProbes bool
	tier          string
	baseURL       string
	// keyRef 为空表示未配置 runtime key。
	keyRef string

	historyPageSize int
	maxTxVersion    int
	rpcTimeout      time.Duration
	dataTimeout     time.Duration
	slotTimeLimit   int
	slotTimeMargin  time.Duration

	unknownTierCUPerMinute int64
	warnPercent            int
	safeOnlyPercent        int
	stopPercent            int
	balanceWarn            string

	staleAfter    time.Duration
	safeOnlyAfter time.Duration

	profileSource ProfileSource
	secrets       Resolver
	sleep         sleepFunc
	hook          provider.UsageHook
	log           *slog.Logger

	modeResolver    func(ctx context.Context) string
	now             func() time.Time
	keyCacheTTL     time.Duration
	baseURLResolver func(context.Context) (string, error)
}

func (o Options) normalize() config {
	cfg := config{
		enabled:                o.Enabled,
		rpcHistory:             o.RPCHistory,
		disableProbes:          o.DisableProbes,
		tier:                   normalizeTier(o.Tier),
		baseURL:                strings.TrimRight(strings.TrimSpace(o.BaseURL), "/"),
		keyRef:                 strings.TrimSpace(o.RuntimeKeyRef),
		historyPageSize:        o.HistoryPageSize,
		maxTxVersion:           o.MaxTxVersion,
		rpcTimeout:             o.RPCTimeout,
		dataTimeout:            o.DataTimeout,
		slotTimeLimit:          o.SlotTimeSearchLimit,
		slotTimeMargin:         o.SlotTimeMargin,
		unknownTierCUPerMinute: o.UnknownTierCUPerMinute,
		warnPercent:            o.QuotaWarnPercent,
		safeOnlyPercent:        o.QuotaSafeOnlyPercent,
		stopPercent:            o.QuotaStopPercent,
		balanceWarn:            strings.TrimSpace(o.BalanceWarn),
		staleAfter:             o.ManagementStaleAfter,
		safeOnlyAfter:          o.ManagementSafeOnlyAfter,
		profileSource:          o.ProfileSource,
		secrets:                o.Secrets,
		hook:                   o.UsageHook,
		log:                    o.Log,
		modeResolver:           o.ModeResolver,
		now:                    o.Now,
		keyCacheTTL:            o.KeyCacheTTL,
		baseURLResolver:        o.BaseURLResolver,
	}
	if cfg.keyCacheTTL <= 0 {
		cfg.keyCacheTTL = keyCacheTTL
	}
	if cfg.historyPageSize <= 0 {
		cfg.historyPageSize = DefaultHistoryPageSize
	}
	if cfg.maxTxVersion < 1 {
		cfg.maxTxVersion = 1
	}
	if cfg.rpcTimeout <= 0 {
		cfg.rpcTimeout = 30 * time.Second
	}
	if cfg.dataTimeout <= 0 {
		cfg.dataTimeout = 60 * time.Second
	}
	if cfg.slotTimeLimit <= 0 {
		cfg.slotTimeLimit = 64
	}
	if cfg.slotTimeMargin <= 0 {
		cfg.slotTimeMargin = 120 * time.Second
	}
	if cfg.unknownTierCUPerMinute <= 0 {
		cfg.unknownTierCUPerMinute = DefaultUnknownTierCUPerMinute
	}
	if cfg.warnPercent <= 0 {
		cfg.warnPercent = 80
	}
	if cfg.safeOnlyPercent <= 0 {
		cfg.safeOnlyPercent = 95
	}
	if cfg.stopPercent <= 0 {
		cfg.stopPercent = 100
	}
	if cfg.staleAfter <= 0 {
		cfg.staleAfter = 15 * time.Minute
	}
	if cfg.safeOnlyAfter <= 0 {
		cfg.safeOnlyAfter = time.Hour
	}
	if cfg.log == nil {
		cfg.log = slog.Default()
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.sleep == nil {
		cfg.sleep = sleepContext
	}
	return cfg
}

func normalizeTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case TierPaid:
		return TierPaid
	case TierFree:
		return TierFree
	default:
		return TierUnknown
	}
}

// paidTier 为真时允许按 Profile 满速运行；free/unknown/空一律按未知层级钳制。
func (c config) paidTier() bool { return c.tier == TierPaid }

// ---- Profile（规范 §1.4：为避免包循环 drpc 不 import route）----

// Profile 是 route.Profile 的同名字段子集 + OperationClass/Mode。
//
// 字段名必须与 route.Profile 一致，app 侧用结构体字面量逐字段拷贝即可对接。
type Profile struct {
	ProviderKey    string
	Chain          string
	Dataset        string
	OperationClass string
	Mode           string

	ShardConcurrency      int
	RequestConcurrency    int
	RequestsPerMinute     float64
	RequestsPerSecond     float64
	CUPerMinute           int64
	PageSize              int
	BatchSize             int
	RequestWeight         int
	RequestTimeoutSeconds int
	HardCeiling           int

	Revision           int64
	VerifiedAt         *time.Time
	VerificationSource string

	// EffectiveMode / ClampReason 由本包在钳制后填写，供 Health 与后台展示，
	// 不参与 ProfileSource 的输入。
	EffectiveMode string
	ClampReason   string
}

// ProfileSource 由 route.Registry 实现（结构相同的方法集，避免包循环）。
type ProfileSource interface {
	Profile(ctx context.Context, providerKey, chain, dataset, operation, mode string) (Profile, bool)
}

// Resolver 解析受控密钥引用（env://NAME、store://<provider>/<kind>）。
//
// 规范 §1.4 里该字段写作 secret.Resolver；这里声明为只含 Resolve 的最小接口：
// 方法集是 secret.Resolver 的子集，因此 *secret.Manager 或 secret.Resolver 值都能直接传入，
// 而本包不必 import 并行开发中的 internal/secret，编译期自洽（不改变任何运行时行为）。
// 拿不到明文（未配置、引用失效、resolver 为 nil）统一按 DRPC_NOT_CONFIGURED 处理。
type Resolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// defaultProfiles 是 ProfileSource 缺失/未命中时的内置默认矩阵（开发文档 §6.2）。
//
// data_api：并发 2/4/8，RPM 10/30/60，单请求 1837 CU，Shard 并发 1/2/4，权重 4；
// solana_rpc：并发 8/16/32，RPS 20/40/80，单方法调用 20 CU，硬上限 48，权重 1。
// 免费/未知层级还会在此基础上按 CU/分钟钳制（见 quota.go）。
func defaultProfile(channel, mode string) Profile {
	mode = normalizeMode(mode)
	p := Profile{
		ProviderKey: ProviderKey,
		Chain:       ChainSolana,
		Mode:        mode,
		BatchSize:   1,
		// 首期不启用 Batch（开发文档 §6.2：避免依赖未验证的付费层 Batch 上限）。
		RequestTimeoutSeconds: 60,
	}
	switch channel {
	case ChannelDataAPI:
		p.OperationClass = OperationSolanaHistory
		p.PageSize = DefaultHistoryPageSize
		p.RequestWeight = 4
		switch mode {
		case ModeSafe:
			p.ShardConcurrency, p.RequestConcurrency = 1, 2
			p.RequestsPerMinute = 10
		case ModeTurbo:
			p.ShardConcurrency, p.RequestConcurrency = 4, 8
			p.RequestsPerMinute = 60
		default:
			p.ShardConcurrency, p.RequestConcurrency = 2, 4
			p.RequestsPerMinute = 30
		}
		p.CUPerMinute = int64(p.RequestsPerMinute) * CUDataAPIHistory
		p.HardCeiling = 8
		p.RequestTimeoutSeconds = 60
	default:
		p.OperationClass = OperationSolanaDetail
		p.PageSize = DefaultHistoryPageSize
		p.RequestWeight = 1
		switch mode {
		case ModeSafe:
			p.RequestConcurrency = 8
			p.RequestsPerSecond = 20
		case ModeTurbo:
			p.RequestConcurrency = 32
			p.RequestsPerSecond = 80
		default:
			p.RequestConcurrency = 16
			p.RequestsPerSecond = 40
		}
		p.CUPerMinute = int64(p.RequestsPerSecond) * 60 * CURPCCall
		// 硬上限 48（开发文档 §6.2），Profile 里的 32 是目标值。
		p.HardCeiling = 48
		p.RequestTimeoutSeconds = 30
	}
	return p
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModeSafe:
		return ModeSafe
	case ModeTurbo:
		return ModeTurbo
	default:
		return ModeFast
	}
}

// modeRank 用于"就低"比较：SAFE < FAST < TURBO。
func modeRank(mode string) int {
	switch normalizeMode(mode) {
	case ModeSafe:
		return 0
	case ModeTurbo:
		return 2
	default:
		return 1
	}
}

// lowerMode 返回两档中较低的一档。
func lowerMode(a, b string) string {
	if modeRank(a) <= modeRank(b) {
		return normalizeMode(a)
	}
	return normalizeMode(b)
}
