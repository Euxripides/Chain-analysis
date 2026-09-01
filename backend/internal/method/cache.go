package method

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Registry 解析索引（§20）：
//   - Global Selector Cache：selector → candidate signatures
//   - Contract ABI Cache：chain + contract → parsed ABI
//
// Fast Resolver 只读本结构（§17：低延迟、无外部网络、不阻塞下载）。
// 刷新由后台 goroutine 周期性执行，热路径永远不访问数据库。
type Registry struct {
	mu         sync.RWMutex
	signatures map[string][]MethodCandidate
	abis       map[string]abiEntry
	events     map[string]EventSignatureRecord
	loadedAt   time.Time
}

type abiEntry struct {
	abi    *ABI
	record ABIRecord
}

func NewRegistry() *Registry {
	return &Registry{
		signatures: map[string][]MethodCandidate{},
		abis:       map[string]abiEntry{},
		events:     map[string]EventSignatureRecord{},
	}
}

func abiKey(chain, contract string) string {
	return strings.ToLower(chain) + "|" + strings.ToLower(contract)
}

// Signatures 返回该 selector 的全部候选（副本，调用方不得修改内部状态）。
func (r *Registry) Signatures(selector string) []MethodCandidate {
	if r == nil || selector == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := r.signatures[strings.ToLower(selector)]
	if len(list) == 0 {
		return nil
	}
	out := make([]MethodCandidate, len(list))
	copy(out, list)
	return out
}

// ABI 返回合约 ABI（含来源元信息）。
func (r *Registry) ABI(chain, contract string) (*ABI, ABIRecord, bool) {
	if r == nil || contract == "" {
		return nil, ABIRecord{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.abis[abiKey(chain, contract)]
	if !ok {
		return nil, ABIRecord{}, false
	}
	return entry.abi, entry.record, true
}

// EventSignature 返回 topic0 对应的事件签名（§29）。
func (r *Registry) EventSignature(topic0 string) (EventSignatureRecord, bool) {
	if r == nil || topic0 == "" {
		return EventSignatureRecord{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.events[strings.ToLower(topic0)]
	return rec, ok
}

func (r *Registry) LoadedAt() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loadedAt
}

// SetSignatures 全量替换签名索引。
func (r *Registry) SetSignatures(records []SignatureRecord) {
	index := make(map[string][]MethodCandidate, len(records))
	for _, rec := range records {
		selector := strings.ToLower(strings.TrimSpace(rec.Selector))
		if selector == "" || rec.Signature == "" {
			continue
		}
		name := rec.Name
		if name == "" {
			name = NameOf(rec.Signature)
		}
		index[selector] = append(index[selector], MethodCandidate{
			Name: name, Signature: rec.Signature, Source: rec.Source, Verified: rec.Verified,
		})
	}
	for selector := range index {
		sortCandidates(index[selector])
	}
	r.mu.Lock()
	r.signatures = index
	r.loadedAt = time.Now()
	r.mu.Unlock()
	RegistryEntries.WithLabelValues("selectors").Set(float64(len(index)))
}

// SetABIs 全量替换 ABI 索引；解析失败的 ABI 被跳过而不是让刷新整体失败。
func (r *Registry) SetABIs(records []ABIRecord) (loaded, failed int) {
	index := make(map[string]abiEntry, len(records))
	for _, rec := range records {
		parsed, err := ParseABI(rec.ABIJSON)
		if err != nil {
			failed++
			continue
		}
		key := abiKey(rec.Chain, rec.ContractAddress)
		// 同一合约多来源：保留优先级更高的（§30）
		if existing, ok := index[key]; ok && SourceRank(existing.record.Source) >= SourceRank(rec.Source) && existing.record.Verified {
			continue
		}
		index[key] = abiEntry{abi: parsed, record: rec}
		loaded++
	}
	r.mu.Lock()
	r.abis = index
	r.mu.Unlock()
	RegistryEntries.WithLabelValues("contract_abis").Set(float64(len(index)))
	return loaded, failed
}

// SetEventSignatures 全量替换事件签名索引。
func (r *Registry) SetEventSignatures(records []EventSignatureRecord) {
	index := make(map[string]EventSignatureRecord, len(records))
	for _, rec := range records {
		topic := strings.ToLower(strings.TrimSpace(rec.Topic0))
		if topic == "" {
			continue
		}
		if existing, ok := index[topic]; ok && SourceRank(existing.Source) >= SourceRank(rec.Source) {
			continue
		}
		index[topic] = rec
	}
	r.mu.Lock()
	r.events = index
	r.mu.Unlock()
	RegistryEntries.WithLabelValues("event_signatures").Set(float64(len(index)))
}

// maxCachedABIs 单进程 ABI 缓存上限（V1.3 为内存缓存，多实例共享缓存留待后续，§20）。
const maxCachedABIs = 20000

// Refresh 从控制面全量刷新索引。
func (r *Registry) Refresh(ctx context.Context, store Store) error {
	signatures, err := store.ListMethodSignatures(ctx)
	if err != nil {
		return err
	}
	r.SetSignatures(signatures)

	abis, err := store.ListContractABIs(ctx, maxCachedABIs)
	if err != nil {
		return err
	}
	r.SetABIs(abis)

	events, err := store.ListEventSignatures(ctx)
	if err != nil {
		return err
	}
	r.SetEventSignatures(events)
	return nil
}

// StartRefresh 周期刷新（默认 60s）。返回停止函数。
func (r *Registry) StartRefresh(ctx context.Context, store Store, interval time.Duration, onError func(error)) {
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.Refresh(ctx, store); err != nil && onError != nil {
					onError(err)
				}
			}
		}
	}()
}

// sortCandidates 候选排序：Source 优先级 → Verified → 签名字典序（稳定可复现）。
func sortCandidates(list []MethodCandidate) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && candidateLess(list[j], list[j-1]); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func candidateLess(a, b MethodCandidate) bool {
	if SourceRank(a.Source) != SourceRank(b.Source) {
		return SourceRank(a.Source) > SourceRank(b.Source)
	}
	if a.Verified != b.Verified {
		return a.Verified
	}
	return a.Signature < b.Signature
}

// ---- TTL 缓存（§20）----

type cacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

// TTLCache 有界内存 TTL 缓存。超过容量时整体清空（简单可预测，避免 LRU 复杂度）。
type TTLCache[T any] struct {
	mu       sync.RWMutex
	entries  map[string]cacheEntry[T]
	ttl      time.Duration
	capacity int
}

func NewTTLCache[T any](ttl time.Duration, capacity int) *TTLCache[T] {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if capacity <= 0 {
		capacity = 50000
	}
	return &TTLCache[T]{entries: make(map[string]cacheEntry[T], 1024), ttl: ttl, capacity: capacity}
}

func (c *TTLCache[T]) Get(key string) (T, bool) {
	var zero T
	if c == nil {
		return zero, false
	}
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return zero, false
	}
	return entry.value, true
}

func (c *TTLCache[T]) Put(key string, value T) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if len(c.entries) >= c.capacity {
		c.entries = make(map[string]cacheEntry[T], 1024)
	}
	c.entries[key] = cacheEntry[T]{value: value, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

func (c *TTLCache[T]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
