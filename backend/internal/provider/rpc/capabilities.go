package rpc

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- Capability Model（§18-§21）----
//
// 能力来源优先级（§20）：admin > probe > runtime learning > static。
// runtime learning 只能把能力"关掉"，绝不会凭错误把能力打开。

const (
	CapSourceStatic  = "static"
	CapSourceProbe   = "probe"
	CapSourceRuntime = "runtime"
	CapSourceAdmin   = "admin"
)

func capSourceRank(source string) int {
	switch source {
	case CapSourceAdmin:
		return 3
	case CapSourceProbe:
		return 2
	case CapSourceRuntime:
		return 1
	default:
		return 0
	}
}

// CapabilityRecord 一条 provider+chain 能力记录。
type CapabilityRecord struct {
	Provider   string        `json:"provider"`
	Chain      string        `json:"chain"`
	Capability CapabilitySet `json:"capability"`
}

// CapabilityStore 能力缓存（内存），可由 PostgreSQL 持久化加载。
type CapabilityStore struct {
	mu      sync.RWMutex
	records map[string]CapabilitySet
}

func NewCapabilityStore() *CapabilityStore {
	return &CapabilityStore{records: map[string]CapabilitySet{}}
}

func capKey(provider, chainSlug string) string {
	return strings.ToLower(provider) + "|" + strings.ToLower(chainSlug)
}

// Get 返回能力集。
func (s *CapabilityStore) Get(provider, chainSlug string) (CapabilitySet, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	caps, ok := s.records[capKey(provider, chainSlug)]
	return caps, ok
}

// Set 按来源优先级写入。低优先级不会覆盖高优先级（除非过期）。
func (s *CapabilityStore) Set(provider, chainSlug string, caps CapabilitySet, source string) {
	caps.Source = source
	caps.CheckedAt = time.Now()
	key := capKey(provider, chainSlug)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.records[key]; ok && capSourceRank(existing.Source) > capSourceRank(source) {
		return
	}
	s.records[key] = caps
}

// LearnUnsupported 运行时学习：Provider 明确回报不支持时关闭对应能力（§20）。
func (s *CapabilityStore) LearnUnsupported(provider, chainSlug string, class MethodClass, reason ErrorClass) {
	key := capKey(provider, chainSlug)
	s.mu.Lock()
	defer s.mu.Unlock()
	caps, ok := s.records[key]
	if !ok {
		return
	}
	switch {
	case reason == ClassArchiveRequired:
		caps.Archive = false
	case class == ClassTrace:
		caps.Trace = false
	case class == ClassDebugTrace:
		caps.Debug = false
	default:
		return
	}
	caps.Source = CapSourceRuntime
	caps.CheckedAt = time.Now()
	s.records[key] = caps
}

// All 全部能力记录（Capability Dashboard，§145）。
func (s *CapabilityStore) All() []CapabilityRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CapabilityRecord, 0, len(s.records))
	for key, caps := range s.records {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		out = append(out, CapabilityRecord{Provider: parts[0], Chain: parts[1], Capability: caps})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Chain != out[j].Chain {
			return out[i].Chain < out[j].Chain
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}

// ChainCapability 聚合某条链上所有 Provider 的能力（前端功能降级用，§185/§186/§187）。
func (s *CapabilityStore) ChainCapability(chainSlug string) CapabilitySet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var merged CapabilitySet
	suffix := "|" + strings.ToLower(chainSlug)
	for key, caps := range s.records {
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		merged.StandardRPC = merged.StandardRPC || caps.StandardRPC
		merged.Archive = merged.Archive || caps.Archive
		merged.Batch = merged.Batch || caps.Batch
		merged.Trace = merged.Trace || caps.Trace
		merged.Debug = merged.Debug || caps.Debug
		merged.WebSocket = merged.WebSocket || caps.WebSocket
		merged.GRPC = merged.GRPC || caps.GRPC
		if caps.MaxBatchSize > merged.MaxBatchSize {
			merged.MaxBatchSize = caps.MaxBatchSize
		}
		if caps.MaxLogBlockRange > merged.MaxLogBlockRange {
			merged.MaxLogBlockRange = caps.MaxLogBlockRange
		}
		if caps.MaxTraceBlockRange > merged.MaxTraceBlockRange {
			merged.MaxTraceBlockRange = caps.MaxTraceBlockRange
		}
	}
	return merged
}
