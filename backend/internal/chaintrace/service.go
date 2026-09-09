package chaintrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/blockchain-platform/blockchain-platform/internal/chain"
	rpc "github.com/blockchain-platform/blockchain-platform/internal/provider/rpc"
)

// ErrTraceNotAvailableLocally local_only 且本地无 Trace。
var ErrTraceNotAvailableLocally = errors.New("trace is not available locally")

// RawStore 大响应对象存储（§107）。
type RawStore interface {
	PutGzip(ctx context.Context, key string, data []byte) (int64, error)
}

// Service Trace 服务（§99/§100/§110）。
//
// Admission Gate：
//
//	本地已有 → 直接返回
//	local_only → 不请求 Provider
//	Provider 支持 Trace？→ 预算够吗？→ 才请求 → 落库
type Service struct {
	gateway *rpc.Gateway
	store   *Store
	raw     RawStore
	log     *slog.Logger

	// RawPersist 是否把原始响应写对象存储（§139：Trace/Debug always）
	rawPersist bool
}

func NewService(gateway *rpc.Gateway, store *Store, raw RawStore, rawPersist bool, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{gateway: gateway, store: store, raw: raw, rawPersist: rawPersist, log: log}
}

// Available 该链当前是否具备 Trace 能力（前端功能降级用，§185/§187）。
func (s *Service) Available(chainSlug string) bool {
	if s == nil || s.gateway == nil {
		return false
	}
	caps := s.gateway.Capabilities().ChainCapability(chainSlug)
	return caps.Trace || caps.Debug
}

// TransactionTrace 获取一笔交易的规范化调用树。
func (s *Service) TransactionTrace(ctx context.Context, chainSlug string, hash chain.Hash, opts Options) (*TraceResult, error) {
	chainSlug = strings.ToLower(strings.TrimSpace(chainSlug))
	mode, err := rpc.ParseConsistency(opts.Consistency)
	if err != nil {
		// 参数错误必须按 invalid_params 返回（400），不能落成 generic error：
		// writeRPCError 会把非 rpc.Error 一律归为 ClassUpstream，对外显示成
		// “Provider temporarily unavailable”，误导为上游故障（Investigation
		// E2E 曾以 mode=receipt 触发此问题，实际没有发出任何 Provider 请求）。
		return nil, &rpc.Error{Class: rpc.ClassInvalidParams, Chain: chainSlug, Method: "trace_transaction", Message: err.Error()}
	}

	// 1) 本地持久缓存优先（§108/§110/§228）
	if mode != rpc.ConsistencyForceRefresh && s.store != nil {
		cached, cacheErr := s.store.Get(ctx, chainSlug, hash)
		if cacheErr != nil {
			s.log.Warn("trace cache lookup failed", "err", cacheErr)
		} else if cached != nil {
			rpc.TraceCacheHitTotal.WithLabelValues(chainSlug).Inc()
			rpc.TraceRequestsTotal.WithLabelValues(chainSlug, "cache_hit").Inc()
			return cached, nil
		}
	}
	if mode == rpc.ConsistencyLocalOnly {
		return nil, ErrTraceNotAvailableLocally
	}

	// 2) 能力闸门：不得假设 Provider 必然支持 Trace（§8-6/§247-6）
	if !s.Available(chainSlug) {
		rpc.TraceRequestsTotal.WithLabelValues(chainSlug, "unsupported").Inc()
		return nil, &rpc.Error{Class: rpc.ClassTraceUnsupported, Chain: chainSlug,
			Method: "trace_transaction", Message: "no provider on this chain advertises trace support"}
	}

	// 3) 预算闸门：Trace 权重远高于普通调用（§109/§112）
	budget := rpc.BudgetContext{
		Purpose: rpc.PurposeTrace, InvestigationID: opts.InvestigationID, JobID: opts.JobID,
		Priority: rpc.PriorityInteractive,
	}
	if opts.MaxTraceCalls > 0 {
		budget.MaxRequests = opts.MaxTraceCalls
	}

	raw, source, providerName, err := s.fetch(ctx, chainSlug, hash, budget, mode)
	if err != nil {
		rpc.TraceRequestsTotal.WithLabelValues(chainSlug, "error").Inc()
		return nil, err
	}
	rpc.TraceRequestsTotal.WithLabelValues(chainSlug, "ok").Inc()

	root, detectedSource, err := Normalize(raw)
	if err != nil {
		return nil, &rpc.Error{Class: rpc.ClassInvalidResponse, Chain: chainSlug,
			Method: source, Message: "cannot normalize provider trace: " + err.Error()}
	}
	frames, depth := Summarize(root)
	result := &TraceResult{
		Chain: chainSlug, TxHash: hash, Root: root, Source: detectedSource,
		FrameCount: frames, MaxDepth: depth, FetchedAt: time.Now().UTC(), RawBytes: len(raw),
	}

	// 4) 原始响应落对象存储（§107/§139）
	if s.rawPersist && s.raw != nil {
		key := RawObjectKey(providerName, chainSlug, hash)
		if _, putErr := s.raw.PutGzip(ctx, key, raw); putErr != nil {
			s.log.Warn("trace raw persist failed", "err", putErr)
		} else {
			result.RawObjectKey = key
		}
	}

	// 5) 派生内部活动并持久化（§104/§108）
	activities := ExtractInternalActivity(result)
	if s.store != nil {
		if err := s.store.Put(ctx, result, activities); err != nil {
			s.log.Warn("trace persist failed", "err", err)
		}
	}
	return result, nil
}

// fetch 先 parity trace_transaction，运行时不支持再退到 geth debug_traceTransaction（§101）。
func (s *Service) fetch(ctx context.Context, chainSlug string, hash chain.Hash,
	budget rpc.BudgetContext, mode rpc.Consistency) (json.RawMessage, string, string, error) {
	response, err := s.gateway.Call(ctx, rpc.Request{
		Chain: chainSlug, Method: "trace_transaction", Params: []any{string(hash)},
		Class: rpc.ClassTrace, Budget: budget, Priority: rpc.PriorityInteractive, Consistency: mode,
	})
	if err == nil {
		return response.Result, "trace_transaction", response.Provider, nil
	}
	if !rpc.IsClass(err, rpc.ClassTraceUnsupported) && !rpc.IsClass(err, rpc.ClassMethodUnsupported) {
		return nil, "trace_transaction", "", err
	}
	debugResponse, debugErr := s.gateway.Call(ctx, rpc.Request{
		Chain: chainSlug, Method: "debug_traceTransaction",
		Params: []any{string(hash), map[string]any{"tracer": "callTracer"}},
		Class:  rpc.ClassDebugTrace, Budget: budget, Priority: rpc.PriorityInteractive, Consistency: mode,
	})
	if debugErr != nil {
		return nil, "debug_traceTransaction", "", debugErr
	}
	return debugResponse.Result, "debug_traceTransaction", debugResponse.Provider, nil
}

// InternalCalls 读取已持久化的内部调用（不触发 Provider）。
func (s *Service) InternalCalls(ctx context.Context, chainSlug string, hash chain.Hash) ([]InternalActivity, error) {
	if s.store == nil {
		return nil, nil
	}
	return s.store.InternalCalls(ctx, chainSlug, hash)
}

// RawObjectKey 原始 Trace 的对象键（§107）。
func RawObjectKey(providerName, chainSlug string, hash chain.Hash) string {
	providerName = safePathSegment(providerName)
	if providerName == "" {
		providerName = "unknown"
	}
	return fmt.Sprintf("raw/rpc/%s/%s/trace/%s.json.gz", providerName,
		safePathSegment(chainSlug), strings.ToLower(string(hash)))
}

func safePathSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			out.WriteRune(r)
		}
	}
	return out.String()
}
