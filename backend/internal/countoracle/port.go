// V1.3 Count Oracle Provider Port（§2-§5/§33-§47/§53-§54）。
//
// 本文件定义 Download Planner 依赖的稳定接口（Oracle）与解析结果类型，
// 以及 Canonical CountRequest/CountResult 与 V1.2 model 的映射。
// 约束（§103）：Planner 不 import Provider package；不判断 provider_id；
// Chainbase 只是 Adapter；新 Provider 只需要注册进 Registry。
package countoracle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/model"
	pc "github.com/blockchain-platform/blockchain-platform/internal/provider/count"
)

// ResolutionStatus 解析状态（§45）。
type ResolutionStatus string

const (
	StatusResolved    ResolutionStatus = "RESOLVED"
	StatusCacheHit    ResolutionStatus = "CACHE_HIT"
	StatusProfileHit  ResolutionStatus = "PROFILE_HIT"
	StatusPartial     ResolutionStatus = "PARTIAL"
	StatusUnavailable ResolutionStatus = "UNAVAILABLE"
	StatusDeadline    ResolutionStatus = "DEADLINE"
	StatusFailOpen    ResolutionStatus = "FAIL_OPEN"
)

// RouteStrategy（§35：P0 只实现 SINGLE_PROVIDER 与 FAIL_OPEN）。
type RouteStrategy string

const (
	StrategySingleProvider RouteStrategy = "SINGLE_PROVIDER"
	StrategyFailOpen       RouteStrategy = "FAIL_OPEN"
	// P1 预留
	StrategyFirstSuccess RouteStrategy = "FIRST_SUCCESS"
	StrategyHedged       RouteStrategy = "HEDGED"
	StrategyConsensus    RouteStrategy = "CONSENSUS"
	StrategyCacheOnly    RouteStrategy = "CACHE_ONLY"
	StrategyProfileOnly  RouteStrategy = "PROFILE_ONLY"
)

// ProviderAttemptSummary 单 Provider 尝试摘要（§45 Attempts）。
type ProviderAttemptSummary struct {
	ProviderID string
	Status     string // ok | skipped | error
	ErrorClass string
	LatencyMS  int64
	Reason     string
}

// CountResolution（§45：Planner 不直接接裸 CountResult）。
type CountResolution struct {
	Result *pc.CountResult

	Status ResolutionStatus

	Reason string

	FallbackRecommended bool

	Attempts []ProviderAttemptSummary
}

// Oracle Download Planner 依赖的稳定接口（§46）。Planner 只依赖本接口。
type Oracle interface {
	// Enabled 总开关（provider 已注册且 COUNT_ORACLE_ENABLED）。
	Enabled() bool
	// Aware Count-aware Planning（false = Shadow Mode §89/§97）。
	Aware() bool
	// Configured 是否存在可用（已配置 credential 的）Provider。
	Configured() bool
	// Capable 该 (chain,dataset) 是否可路由到某个可用 Provider（§33 Router 输入）。
	Capable(ctx context.Context, chain, dataset string) bool

	// Count V1.2 Planner 兼容门面：model 请求 → model 结果（内部走 Provider Port）。
	Count(ctx context.Context, r model.CountRequest) model.CountResult

	// Resolve 规范解析入口（§46）：Provider-agnostic 请求 → 规范化解析。
	Resolve(ctx context.Context, req pc.CountRequest) CountResolution

	// RecordObservation 记录观测（异步；失败只记日志）。
	RecordObservation(obs *model.CountObservation)
	// RecordActual 任务完成后样本（predicted vs actual）→ Calibration（§22/§24/§51）。
	RecordActual(provider, chain, dataset, addressHash string, predicted, actual int64)
	// Shadow Port Shadow 模式（§97/§98：计算但不改 Planner 决策）。
	Shadow() bool
	// RecordShadowDiff Shadow Diff（§98：old/new 决策对照）。
	RecordShadowDiff(ctx context.Context, d *model.ShadowDiff)

	// Run 后台心跳：Runtime Profiles / Health / Registry 落库。
	Run(ctx context.Context)
}

// ---- Canonical mapping ----

// datasetOf model dataset → 规范 DatasetType（未知 → 空 + false）。
func datasetOf(ds string) (pc.DatasetType, bool) {
	switch ds {
	case "transactions":
		return pc.DatasetTransactions, true
	case "token_transfers":
		return pc.DatasetTokenTransfers, true
	case "logs":
		return pc.DatasetLogs, true
	case "nft_transfers":
		return pc.DatasetNFTTransfers, true
	case "traces":
		return pc.DatasetTraces, true
	case "internal_transactions":
		return pc.DatasetInternalTransfers, true
	case "contract_creations":
		return pc.DatasetContractCreations, true
	}
	return "", false
}

func datasetName(ds pc.DatasetType) string { return string(ds) }

// canonicalRequest model（Planner 视角）→ 规范请求。
func canonicalRequest(r model.CountRequest, chainID uint64, deadline time.Time, purpose pc.CountPurpose) (pc.CountRequest, bool) {
	d, ok := datasetOf(r.Dataset)
	if !ok {
		return pc.CountRequest{}, false
	}
	start := r.FromBlock
	end := uint64(0)
	if r.ToBlock > 0 {
		end = r.ToBlock + 1 // [from, to] inclusive → [from, to+1) exclusive
	}
	creq := pc.CountRequest{
		ChainID:           chainID,
		Dataset:           d,
		Subject:           pc.SubjectRef{Type: pc.SubjectAddress, Value: r.Address},
		Range:             pc.BlockRange{Start: start, End: end},
		Purpose:           purpose,
		Priority:          pc.PriorityPlanner,
		Deadline:          deadline,
		MinConfidence:     0,
		QueryShapeVersion: 1, // §54
	}
	creq.RequestID = canonicalHash(creq)
	return creq, true
}

// resultToModel 规范结果 → V1.2 model.CountResult（Planner 兼容层）。
func resultToModel(res pc.CountResult, r model.CountRequest) model.CountResult {
	cls, _ := datasetNameOf(res.Dataset)
	return model.CountResult{
		Provider:      res.ProviderID,
		Chain:         r.Chain,
		Dataset:       cls,
		RawCount:      res.Count,
		AdjustedCount: res.Count,
		FromBlock:     r.FromBlock,
		ToBlock:       r.ToBlock,
		LatencyMS:     res.Latency.Milliseconds(),
		CacheHit:      false,
		Confidence:    res.Confidence,
		Calibration:   1,
		Source:        string(res.Source),
		ProbeRequests: int(res.Cost.Requests),
	}
}

func datasetNameOf(ds pc.DatasetType) (string, bool) { return string(ds), ds != "" }

// modelFailResult 失败 model 结果（Fail Open，§17）。
func modelFailResult(r model.CountRequest, class, msg string, providerID string) model.CountResult {
	if providerID == "" {
		providerID = "chainbase"
	}
	return model.CountResult{Provider: providerID, Chain: r.Chain, Dataset: r.Dataset,
		FromBlock: r.FromBlock, ToBlock: r.ToBlock, RawCount: -1, Source: "fallback",
		FailureClass: class, FailureMsg: msg}
}

// canonicalHash 规范请求哈希（§53/§54：Singleflight + Canonical Cache 的统一 key；
// SHA256(canonical json)，字段顺序固定 + query_shape_version）。
func canonicalHash(req pc.CountRequest) string {
	b, err := json.Marshal(canonicalShapeOf(req))
	if err != nil {
		sum := sha256.Sum256([]byte(req.Subject.Value + "|" + string(req.Dataset)))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type canonicalShape struct {
	QueryShapeVersion int            `json:"v"`
	ChainID           uint64         `json:"c"`
	Dataset           pc.DatasetType `json:"d"`
	SubjectType       pc.SubjectType `json:"st"`
	Subject           string         `json:"s"`
	RangeStart        uint64         `json:"rs"`
	RangeEnd          uint64         `json:"re"`
}

func canonicalShapeOf(req pc.CountRequest) canonicalShape {
	return canonicalShape{QueryShapeVersion: req.QueryShapeVersion, ChainID: req.ChainID,
		Dataset: req.Dataset, SubjectType: req.Subject.Type, Subject: req.Subject.Value,
		RangeStart: req.Range.Start, RangeEnd: req.Range.End}
}
