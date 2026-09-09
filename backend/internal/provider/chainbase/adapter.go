// V1.3 Chainbase Adapter（§75）：把 V1.2 internal/provider/chainbase 客户端包装成
// CountProvider 端口实现。Static Capability: transactions/token_transfers supported；
// Runtime: transactions AVAILABLE / token_transfers BLOCKED（由配置与真实测试决定，§73）。
package chainbase

import (
	"context"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/provider"
	"github.com/blockchain-platform/blockchain-platform/internal/provider/count"
)

// AdapterOptions Chainbase Adapter 配置（来源：V1.3 配置段，§75/§76）。
type AdapterOptions struct {
	// Client 底层 V1.2 客户端（保留 limiter/breaker/usage 行为）。
	Client *Client

	// TXEnabled / TokenEnabled 对应 COUNT_PROVIDER_CHAINBASE_TX_ENABLED /
	// COUNT_PROVIDER_CHAINBASE_TOKEN_ENABLED（§76：token 默认 false）。
	TXEnabled    bool
	TokenEnabled bool

	// TokenBlockReason 前端/报告显示的 BLOCKED 原因（§73）。
	TokenBlockReason string

	// ProbeBudget 探测预算（透传给 client.Count 的二进制探测兜底）。
	ProbeBudget int

	// Chains 支持的 chain（chain_id → true）。nil = 由 chain_capabilities 部署矩阵决定。
	Chains map[uint64]bool

	// DisplayName Provider Registry 显示名。
	DisplayName string
}

// Adapter Chainbase CountProvider。
type Adapter struct {
	opts AdapterOptions
}

// NewAdapter 构造 Chainbase Adapter。
func NewAdapter(opts AdapterOptions) *Adapter {
	if opts.TokenBlockReason == "" {
		opts.TokenBlockReason = "Free Plan endpoint effective rate block (V1.3: 11-Key scope matrix 0/2310 ok; keep BLOCKED)"
	}
	if opts.ProbeBudget <= 0 {
		opts.ProbeBudget = 22
	}
	if opts.DisplayName == "" {
		opts.DisplayName = "Chainbase"
	}
	return &Adapter{opts: opts}
}

func (a *Adapter) ID() string { return "chainbase" }

// DisplayName registry 展示名。
func (a *Adapter) DisplayName() string { return a.opts.DisplayName }

// Capabilities 静态 + Runtime（§25：Runtime 以真实测试/配置为准）。
func (a *Adapter) Capabilities(ctx context.Context) count.ProviderCapabilities {
	state := a.clientState()
	txStatus, txReason := count.StatusAvailable, ""
	switch state {
	case "NO_KEY":
		txStatus, txReason = count.StatusBlocked, "chainbase api key not configured"
	case "OPEN":
		txStatus, txReason = count.StatusThrottled, "circuit open"
	case "DEGRADED", "HALF_OPEN":
		txStatus, txReason = count.StatusDegraded, "degraded"
	}
	tx := count.DatasetCapability{
		Enabled: a.opts.TXEnabled, SupportsRange: true, RangeVerified: true,
		Semantics: count.SemanticsTotalRows, Accuracy: count.AccuracyExact,
		RuntimeStatus: txStatus, Reason: txReason,
	}
	token := count.DatasetCapability{
		Enabled: a.opts.TokenEnabled, SupportsRange: true, RangeVerified: true,
		Semantics: count.SemanticsTotalRows, Accuracy: count.AccuracyExact,
		RuntimeStatus: count.StatusBlocked,
		Reason:        a.opts.TokenBlockReason,
	}
	ds := map[count.DatasetKey]count.DatasetCapability{
		{ChainID: 0, Dataset: count.DatasetTransactions}:   tx,
		{ChainID: 0, Dataset: count.DatasetTokenTransfers}: token,
	}
	return count.ProviderCapabilities{
		ProviderID:           "chainbase",
		Chains:               a.opts.Chains,
		Datasets:             ds,
		SupportsBatch:        false,
		SupportsMultiDataset: false,
		RateLimits: []count.RateBucketPolicy{
			{Scope: count.ScopeAccount, ScopeKey: "chainbase", MaxConcurrency: 1, MaxRPS: 0.9, Burst: 1},
		},
		CostPerRequestCredits: a.creditsPerRequest(),
		CostPerRequestUSD:     a.costPerRequestUSD(),
	}
}

func (a *Adapter) clientState() string {
	if a.opts.Client == nil {
		return "NO_KEY"
	}
	if !a.opts.Client.Configured() {
		return "NO_KEY"
	}
	return a.opts.Client.Snapshot().State
}

// Count 单次 Count（无重试：重试策略由 Count Oracle Scheduler 按 provider 配置负责，§90）。
// 顺序：先做能力闸门（BLOCKED dataset 绝不产生网络请求，§73"不再制造 token 429"），再查凭据。
func (a *Adapter) Count(ctx context.Context, req count.CountRequest) (count.CountResult, error) {
	ds := string(req.Dataset)
	switch req.Dataset {
	case count.DatasetTransactions:
		if !a.opts.TXEnabled {
			return count.CountResult{}, count.Unsupported("chainbase", "dataset disabled by config: transactions")
		}
	case count.DatasetTokenTransfers:
		if !a.opts.TokenEnabled {
			return count.CountResult{}, count.Unsupported("chainbase",
				"dataset BLOCKED: "+a.opts.TokenBlockReason)
		}
	default:
		return count.CountResult{}, count.Unsupported("chainbase", "unsupported dataset: "+ds)
	}
	if a.opts.Client == nil || !a.opts.Client.Configured() {
		return count.CountResult{}, count.Auth("chainbase", "chainbase api key not configured")
	}
	if req.Subject.Type != count.SubjectAddress || req.Subject.Value == "" {
		return count.CountResult{}, count.Unsupported("chainbase", "only address subjects supported")
	}
	if req.ChainID == 0 {
		return count.CountResult{}, count.Unsupported("chainbase", "chain_id required")
	}
	// Canonical [start,end) → Chainbase from_block/to_block inclusive（§9：Adapter 自己转换）。
	from, to := req.Range.Start, uint64(0)
	if req.Range.End > 0 {
		if req.Range.End <= 1 {
			to = 0
		} else {
			to = req.Range.End - 1
		}
	}
	start := time.Now()
	raw, probeReqs, err := a.opts.Client.Count(ctx, int64(req.ChainID), ds, req.Subject.Value, from, to, a.opts.ProbeBudget)
	elapsed := time.Since(start)
	if err != nil {
		return count.CountResult{}, classifyCbErr(err)
	}
	return count.CountResult{
		RequestID:  req.RequestID,
		ProviderID: "chainbase",
		ChainID:    req.ChainID,
		Dataset:    req.Dataset,
		Subject:    req.Subject,
		Range:      req.Range,
		Count:      raw,
		Semantics:  count.SemanticsTotalRows,
		Accuracy:   count.AccuracyExact,
		Confidence: 0.99,
		Source:     count.SourceLiveProvider,
		ObservedAt: time.Now(),
		Latency:    elapsed,
		Cost: count.CountCost{
			Requests: int64(probeReqs),
			Credits:  float64(probeReqs) * a.opts.Client.credits,
			USD:      float64(probeReqs) * a.opts.Client.costUSD,
		},
		ProviderMeta: count.ProviderMeta{Fields: map[string]string{"endpoint": a.opts.Client.endpoint}},
	}, nil
}

// Health 快照（§91 provider state machine 的 adapter 侧视图；同时维护
// chainbase_circuit_state gauge（0=CLOSED/NO_KEY 1=DEGRADED 2=OPEN 3=HALF_OPEN，V1.2 语义））。
func (a *Adapter) Health(ctx context.Context) count.ProviderHealth {
	state := a.clientState()
	msg := ""
	healthy := state == "CLOSED" || state == "DEGRADED" || state == "HALF_OPEN"
	switch state {
	case "NO_KEY":
		msg = "chainbase api key not configured"
	case "OPEN":
		msg = "chainbase circuit state: " + state
	}
	if state == "" {
		state = "AVAILABLE"
	}
	disp := "AVAILABLE"
	switch state {
	case "CLOSED":
		disp = "AVAILABLE"
	case "DEGRADED", "HALF_OPEN":
		disp = "DEGRADED"
	case "OPEN":
		disp = "THROTTLED"
		healthy = false
	case "NO_KEY":
		disp = "BLOCKED"
	}
	_ = healthy
	switch state {
	case "CLOSED", "NO_KEY", "", "AVAILABLE":
		ChainbaseCircuitState.Set(0)
	case "DEGRADED":
		ChainbaseCircuitState.Set(1)
	case "OPEN", "BLOCKED":
		ChainbaseCircuitState.Set(2)
	case "HALF_OPEN":
		ChainbaseCircuitState.Set(3)
	}
	return count.ProviderHealth{
		ProviderID: "chainbase",
		State:      disp,
		Healthy:    state != "OPEN" && state != "NO_KEY" && state != "BLOCKED",
		Message:    msg,
		UpdatedAt:  time.Now(),
	}
}

// classifyCbErr V1.1 provider.Error → Port 统一 Error Class（§55）。
func classifyCbErr(err error) error {
	pe, ok := err.(*provider.Error)
	if !ok {
		return count.NewCountError("chainbase", count.ErrUnknown, err.Error(), true)
	}
	msg := strings.ToLower(pe.Message)
	cls := count.ErrUnknown
	switch pe.Class {
	case provider.ClassRateLimit:
		if strings.Contains(msg, "quota") || strings.Contains(msg, "exhaust") || strings.Contains(msg, "insufficient credit") {
			cls = count.ErrQuota
			return count.NewCountError("chainbase", cls, pe.Message, false)
		}
		cls = count.ErrRateLimit
	case provider.ClassAuth:
		cls = count.ErrAuth
	case provider.ClassConnectTimeout, provider.ClassHeaderTimeout,
		provider.ClassBodyIdleTimeout, provider.ClassOverallTimeout, provider.ClassRequestTimeout:
		cls = count.ErrTimeout
	case provider.ClassConnectionLimit, provider.ClassUpstream:
		cls = count.ErrConnection
	case provider.ClassInvalidJSON, provider.ClassProviderInvalid, provider.ClassResponseTooLarge, provider.ClassBodyTruncated:
		cls = count.ErrInvalidResponse
	case provider.ClassInvalidRequest:
		cls = count.ErrUnsupported
	case provider.ClassCancelled:
		cls = count.ErrCancelled
	case provider.ClassUnsupported:
		cls = count.ErrUnsupported
	}
	if pe.HTTPStatus >= 500 {
		cls = count.ErrProvider5xx
	}
	if pe.HTTPStatus == 429 {
		cls = count.ErrRateLimit
	}
	retryable := pe.Retryable && cls != count.ErrAuth && cls != count.ErrQuota && cls != count.ErrUnsupported
	return count.NewCountError("chainbase", cls, pe.Message, retryable)
}

func (a *Adapter) creditsPerRequest() float64 {
	if a.opts.Client == nil {
		return 0
	}
	return a.opts.Client.credits
}

func (a *Adapter) costPerRequestUSD() float64 {
	if a.opts.Client == nil {
		return 0
	}
	return a.opts.Client.costUSD
}
